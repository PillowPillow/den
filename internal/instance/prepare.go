package instance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/mod/semver"
	"gopkg.in/yaml.v3"

	"github.com/PillowPillow/den/v2/internal/config"
	"github.com/PillowPillow/den/v2/internal/nest"
	"github.com/PillowPillow/den/v2/internal/registry"
	"github.com/PillowPillow/den/v2/internal/sbx"
	"github.com/PillowPillow/den/v2/internal/sbxenv"
	"github.com/PillowPillow/den/v2/internal/template"
	"github.com/PillowPillow/den/v2/internal/worktree"
)

// Plan is everything Create needs, decided before any side effect.
type Plan struct {
	Instance   string
	Resolved   *nest.Resolved
	Repos      []nest.Repo
	Worktree   *worktree.Name // nil without -w
	RunDir     string         // <project>/.den/run/<instance> or <denHome>/run/<instance>
	GenDir     string         // <denHome>/gen/<instance>
	Workspace  string         // project folder when start is ".", else RunDir (P10)
	Start      string         // VM path mapped from `start:`, the one the record keeps
	Workdir    string         // --workdir: this attach or exec only, never recorded
	ConfigHash string
	State      State
	Record     *registry.Record // set when State == Live or Recorded
	Overlays   []sbxenv.Mount   // read-only, after every other mount; empty on the Live path
}

// Prepare resolves o into a Plan and makes every refusal den can decide before
// the first side effect. It writes nothing but the nest index nest.Find claims.
func Prepare(ctx context.Context, d Deps, o Options) (*Plan, error) {
	if o.NoInput && o.Selection.Interactive {
		return nil, errors.New("-i cannot be used with den exec: it never asks")
	}
	file, global, err := nest.Find(d.DenHome, d.Cwd, o.Nest)
	if err != nil {
		return nil, err
	}
	res, err := nest.Resolve(d.DenHome, global, file, o.Nest)
	if err != nil {
		return nil, err
	}
	var wt *worktree.Name
	component := ""
	if o.Branch != "" {
		if component, err = config.FlattenSandboxComponent("branch", o.Branch); err != nil {
			return nil, err
		}
		wt = &worktree.Name{Dir: component, Branch: o.Branch}
	}
	instance, err := sbx.SandboxName(o.Nest, component)
	if err != nil {
		return nil, err
	}
	if err := sbx.ValidateCreatableSandboxName(instance); err != nil {
		return nil, err
	}
	if err := CheckSbx(ctx, d.Sbx); err != nil {
		return nil, err
	}
	state, rec, err := Lookup(ctx, d, instance)
	if err != nil {
		return nil, err
	}
	if state == Foreign {
		return nil, fmt.Errorf("a sandbox named %q exists but den V2 did not create it — remove it with "+
			"`sbx rm %s` (or v1's `den rm %s`), then retry", instance, instance, instance)
	}
	// Only once the state is known: a running VM never depends on a template build, so a Live
	// instance attaches with the remedy as a warning (beside the drift warning its hash triggers).
	if res.TemplateErr != nil {
		if state != Live {
			return nil, res.TemplateErr
		}
		res.Warnings = append(res.Warnings, res.TemplateErr.Error())
	}

	p := &Plan{
		Instance: instance, Resolved: res, Worktree: wt, State: state, Record: rec,
		RunDir: runDir(d.DenHome, res, instance),
		GenDir: filepath.Join(d.DenHome, "gen", instance),
	}
	p.Workspace = p.RunDir
	if res.Start == "." {
		p.Workspace = res.File.Dir
	}
	if state == Live {
		return attachPlan(p, o)
	}
	// Checked before the selection so -i never asks for repos only to refuse afterwards.
	if _, ok := res.Env[sbxenv.RepoMapEnv]; ok {
		return nil, fmt.Errorf("`%s` is set by den in every instance — remove it from `env:` in %s or in nest %s of %s", sbxenv.RepoMapEnv, res.GlobalFile, res.Name, res.File.Path)
	}

	repos, err := nest.Select(ctx, res, o.Selection, d.Prompt)
	if err != nil {
		return nil, err
	}
	if repos, err = nest.AddRepos(res, repos, o.Selection.Repos, d.Cwd); err != nil {
		return nil, err
	}
	if state == Recorded {
		if err := checkRecordedWorktrees(rec, res, repos, wt); err != nil {
			return nil, err
		}
	}
	if err := checkRepos(ctx, d.Git, res, repos, wt != nil); err != nil {
		return nil, err
	}
	if err := checkTemplate(ctx, d, res); err != nil {
		return nil, err
	}
	if err := checkKits(res); err != nil {
		return nil, err
	}
	if err := checkLayers(res); err != nil {
		return nil, err
	}
	mounts, err := writableMounts(ctx, d.Git, res, repos, wt, p.Workspace)
	if err != nil {
		return nil, err
	}
	own := []struct{ kind, dir string }{{"run", p.RunDir}, {"gen", p.GenDir}}
	if p.Overlays, err = overlays(res, mounts, own); err != nil {
		return nil, err
	}
	// Refused even with --workdir: the record keeps the mapped start for every later attach.
	start, err := mapStart(res, repos, o.Selection)
	if err != nil {
		return nil, err
	}
	p.Repos, p.Start, p.Workdir = repos, start, o.Workdir
	if p.ConfigHash, err = configHash(res, repos, start, o.Branch); err != nil {
		return nil, err
	}
	return p, nil
}

// attachPlan hashes the RECORDED selection and branch: hashing a fresh one
// would report drift on every `den up` of an instance created with --only or --without.
func attachPlan(p *Plan, o Options) (*Plan, error) {
	rec, res := p.Record, p.Resolved
	for _, r := range rec.Repos {
		repo := nest.Repo{Name: r.Name, Path: r.Repo}
		for _, c := range res.Repos {
			if c.Path == r.Repo {
				repo.Optional = c.Optional
			}
		}
		p.Repos = append(p.Repos, repo)
	}
	branch := ""
	p.Worktree = nil
	if rec.Worktree != nil {
		branch = rec.Worktree.Branch
		p.Worktree = &worktree.Name{Dir: rec.Worktree.Dir, Branch: rec.Worktree.Branch}
	}
	p.Start, p.Workdir = rec.Start, o.Workdir
	// A start the recorded repos cannot map is a config change: it must hash
	// differently, never refuse an attach to a running VM.
	start, startErr := mapStart(res, p.Repos, o.Selection)
	var err error
	if p.ConfigHash, err = configHash(res, p.Repos, hashedStart(res, start, startErr), branch); err != nil {
		return nil, err
	}
	return p, nil
}

// checkRecordedWorktrees refuses a retry that would drop a worktree den
// created: Create rewrites the record from the new plan, and `den rm` would
// never reclaim a worktree the record no longer names.
func checkRecordedWorktrees(rec *registry.Record, res *nest.Resolved, repos []nest.Repo, wt *worktree.Name) error {
	for _, r := range rec.Repos {
		if !r.Worktree {
			continue
		}
		var now string
		for _, repo := range repos {
			if repo.Path == r.Repo && wt != nil {
				now = worktree.Path(res.Layout, res.Root, wt.Dir, repo.Path)
			}
		}
		switch {
		case now == "":
			return fmt.Errorf("repo %s of %s has a worktree den created at %s, and this selection drops it — "+
				"run `den rm %s` first, or keep that repo", r.Name, rec.Instance, r.Mount, rec.Instance)
		case worktree.ResolvePath(now) != worktree.ResolvePath(r.Mount):
			return fmt.Errorf("repo %s of %s has a worktree den created at %s, but the config now puts it at %s — "+
				"run `den rm %s` first, or restore `worktrees:` in %s", r.Name, rec.Instance, r.Mount, now, rec.Instance, res.File.Path)
		}
	}
	return nil
}

// CheckSbx refuses an sbx below MinVersion, and one whose version it cannot read.
func CheckSbx(ctx context.Context, r sbx.Runner) error {
	raw, err := sbx.Version(ctx, r)
	if err != nil {
		return err
	}
	v, ok := sbx.ReleaseVersion(raw)
	if !ok {
		return fmt.Errorf("could not read a version out of `sbx version` (got %q); den needs sbx %s or later — "+
			"check which sbx is on PATH", raw, sbx.MinVersion)
	}
	if semver.Compare(v, sbx.MinVersion) < 0 {
		return fmt.Errorf("sbx %s is older than %s, the oldest sbx den supports — upgrade sbx", raw, sbx.MinVersion)
	}
	return nil
}

func runDir(denHome string, res *nest.Resolved, instance string) string {
	if res.File.Global {
		return filepath.Join(denHome, "run", instance)
	}
	return filepath.Join(res.File.Dir, ".den", "run", instance)
}

func checkRepos(ctx context.Context, g worktree.Git, res *nest.Resolved, repos []nest.Repo, withWorktree bool) error {
	for _, r := range repos {
		st, err := os.Stat(r.Path)
		if err != nil {
			return fmt.Errorf("nest %q: repo %s: %v — %s", res.Name, r.Path, &config.FileError{Err: err}, repoFix(res, r))
		}
		if !st.IsDir() {
			return fmt.Errorf("nest %q: repo %s is not a directory — %s", res.Name, r.Path, repoFix(res, r))
		}
		if !withWorktree {
			continue
		}
		// The root, not any directory git answers for: a worktree of the
		// enclosing repo would put the wrong tree at the repo's VM path.
		out, err := g.Run(ctx, r.Path, "rev-parse", "--show-toplevel")
		if err != nil || worktree.ResolvePath(strings.TrimSpace(string(out))) != worktree.ResolvePath(r.Path) {
			return fmt.Errorf("nest %q: repo %s is not the root of a git repository, and -w makes a worktree of every repo — "+
				"drop -w, or %s", res.Name, r.Path, repoFix(res, r))
		}
	}
	return nil
}

func repoFix(res *nest.Resolved, r nest.Repo) string {
	if r.AdHoc {
		return "fix or drop `--repo " + r.Path + "`"
	}
	if r.Key != "" {
		return fmt.Sprintf("fix `repos.%s` in %s", r.Key, res.GlobalFile)
	}
	return fmt.Sprintf("fix `repos:` of nest %q in %s", res.Name, res.File.Path)
}

// checkTemplate refuses a template sbx cannot start or whose copy no longer holds the files its
// build read; an image is sbx's to pull, so it is not checked here.
func checkTemplate(ctx context.Context, d Deps, res *nest.Resolved) error {
	t := res.Template
	if t.Choice.IsImage() {
		return nil
	}
	list, err := sbx.Templates(ctx, d.Sbx)
	if err != nil {
		return err
	}
	if sbx.FindTemplate(list, t.Choice.Tag()) == nil {
		return template.NotBuilt(t.Choice, fmt.Sprintf("is not among sbx's templates (no %s)", t.Choice.Tag()))
	}
	// The kits and allowlist come from the copy: at another commit than the build's, the VM would
	// start from one definition with the network and kits of another.
	if t.Record.Source.Kind == "repo" {
		head, err := template.Head(ctx, d.Git, t.Root())
		if err != nil {
			return err
		}
		if head != t.Record.Commit {
			return template.NotBuilt(t.Choice, fmt.Sprintf("was built from commit %s, but its copy %s is at %s", t.Record.Commit, t.Root(), head))
		}
	}
	return nil
}

// checkKits checks local kits only; config.Load has made every local one absolute.
func checkKits(res *nest.Resolved) error {
	for _, k := range res.Kits {
		if !filepath.IsAbs(k) {
			continue
		}
		spec := filepath.Join(k, "spec.yaml")
		if st, err := os.Stat(spec); err != nil || !st.Mode().IsRegular() {
			return fmt.Errorf("kit %s: no %s — a local kit is a directory holding spec.yaml; %s", k, spec, kitFix(res, k))
		}
	}
	return nil
}

// kitFix names every declaration of k: res.Kits merges the template's, the nest's and the profile's.
func kitFix(res *nest.Resolved, k string) string {
	var at []string
	if slices.Contains(res.Template.Def.Kits, k) {
		at = append(at, fmt.Sprintf("`kits:` %s", ofTemplate(res)))
	}
	inProfile := slices.Contains(res.ProfileKits, k)
	if n, ok := res.File.File.Nests[res.Name]; !ok || slices.Contains(n.Kits, k) || (len(at) == 0 && !inProfile) {
		at = append(at, fmt.Sprintf("`kits:` of nest %q in %s", res.Name, res.File.Path))
	}
	if inProfile {
		at = append(at, fmt.Sprintf("`profiles.%s` in %s", res.Profile, res.GlobalFile))
	}
	return "fix " + strings.Join(at, " and ")
}

func checkLayers(res *nest.Resolved) error {
	for _, l := range res.Layers {
		st, err := os.Stat(l)
		if err != nil {
			return fmt.Errorf("layer %s: %v — fix `layers:` of nest %q in %s", l, &config.FileError{Err: err}, res.Name, res.File.Path)
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("layer %s is not a file — fix `layers:` of nest %q in %s", l, res.Name, res.File.Path)
		}
	}
	return nil
}

// rwMount is a read-write mount envOf declares: its host source and the VM path it lands at.
type rwMount struct{ source, target string }

// writableMounts lists the read-write mounts envOf declares. Under -w the main
// clone is not one of them (its worktree lands at its path), nor is the worktree root.
func writableMounts(ctx context.Context, g worktree.Git, res *nest.Resolved, repos []nest.Repo, wt *worktree.Name, workspace string) ([]rwMount, error) {
	mounts := []rwMount{{workspace, workspace}}
	for _, r := range repos {
		if wt == nil {
			mounts = append(mounts, rwMount{r.Path, r.Path})
			continue
		}
		common, err := worktree.CommonGitDir(ctx, g, r.Path)
		if err != nil {
			return nil, err
		}
		mounts = append(mounts,
			rwMount{worktree.Path(res.Layout, res.Root, wt.Dir, r.Path), r.Path},
			rwMount{common, path.Join(sbxenv.GitMountRoot, r.Name)})
	}
	return mounts, nil
}

// overlays mounts read-only, where the VM sees it, every file den or sbx reads
// for this instance that lies under a read-write mount (P12): a kit carries the
// egress allowlist and credentials, and sbx acts on a layer at every create and
// rm. S6 measured that a read-only mount listed after its read-write parent
// holds in the VM even against root and umount. Every overlay names a host path
// that exists: sbx fails opaquely on a missing one.
func overlays(res *nest.Resolved, mounts []rwMount, own []struct{ kind, dir string }) ([]sbxenv.Mount, error) {
	type read struct{ path, fix string }
	denYAML := func(path string, global bool) string {
		if global {
			return "set DEN_HOME to " + filepath.Dir(worktree.ResolvePath(path))
		}
		return "run den from " + filepath.Dir(worktree.ResolvePath(path))
	}
	// The den.yaml declaring the template is res.File or the global file: Choose looks nowhere else.
	protected := []read{{res.File.Path, denYAML(res.File.Path, res.File.Global)}}
	if _, err := os.Stat(res.GlobalFile); err == nil {
		protected = append(protected, read{res.GlobalFile, denYAML(res.GlobalFile, true)})
	}
	t := res.Template
	if !t.Choice.IsImage() {
		// As the choice names it, not Defs.Path: a symlink on the way is what the VM could swap.
		protected = append(protected, read{filepath.Join(t.Root(), config.DefinitionFileName),
			fmt.Sprintf("fix `templates.%s` in %s", t.Choice.Key, t.Choice.DeclaredIn.Path)})
	}
	for _, k := range res.Kits {
		if filepath.IsAbs(k) {
			protected = append(protected, read{k, kitFix(res, k)})
		}
	}
	for _, l := range res.Layers {
		protected = append(protected, read{l, fmt.Sprintf("fix `layers:` of nest %q in %s", res.Name, res.File.Path)})
	}
	for _, b := range []struct {
		field string
		paths []string
	}{{"includes", t.Def.Build.Includes}, {"steps", t.Def.Build.Steps}} {
		for _, f := range b.paths {
			fix := fmt.Sprintf("fix `build.%s` %s", b.field, ofTemplate(res))
			seen, err := seenThrough(f, fix, mounts)
			if err != nil {
				return nil, err
			}
			if len(seen) == 0 {
				continue
			}
			// Missing, it could not be overlaid, and the VM could write the script den build would run.
			if _, err := os.Stat(f); err != nil {
				return nil, fmt.Errorf("`build.%s` entry %s: %v — %s", b.field, f, &config.FileError{Err: err}, fix)
			}
			protected = append(protected, read{f, fix})
		}
	}
	var out []sbxenv.Mount
	targets := map[string]bool{}
	for _, r := range protected {
		for _, o := range own {
			for _, f := range []string{filepath.Clean(r.path), worktree.ResolvePath(r.path)} {
				for _, d := range []string{filepath.Clean(o.dir), worktree.ResolvePath(o.dir)} {
					if _, under := relUnder(f, d); under {
						return nil, fmt.Errorf("%s lies under den's %s dir %s, which `den rm` deletes — move it out of den's %s dir %s, and %s",
							r.path, o.kind, o.dir, o.kind, o.dir, r.fix)
					}
				}
			}
		}
		seen, err := seenThrough(r.path, r.fix, mounts)
		if err != nil {
			return nil, err
		}
		for _, o := range seen {
			if !targets[o.Target] {
				targets[o.Target] = true
				out = append(out, o)
			}
		}
	}
	return out, nil
}

// seenThrough returns a read-only overlay of f for each mount that holds its
// written or its symlink-resolved form; a symlink outside every mount is out of
// the VM's reach, so the resolved form is overlaid where the VM sees it. The
// target is computed from the very forms that matched: mixing a lexical form
// with a resolved one would yield a ../ path.
func seenThrough(f, fix string, mounts []rwMount) ([]sbxenv.Mount, error) {
	forms := []string{filepath.Clean(f)}
	if r := worktree.ResolvePath(f); r != forms[0] {
		forms = append(forms, r)
	}
	var out []sbxenv.Mount
	for _, c := range forms {
		for _, m := range mounts {
			for _, s := range []string{filepath.Clean(m.source), worktree.ResolvePath(m.source)} {
				rel, ok := relUnder(c, s)
				if !ok {
					continue
				}
				// S6c: sbx env create fails on an overlay at a symlink's path, whatever its source.
				if link := symlinkBelow(s, rel); link != "" {
					return nil, fmt.Errorf("%s crosses the symlink %s inside the mounted folder %s, which the VM can "+
						"replace — move it out of every mounted folder or reference its real path %s, and %s",
						f, link, s, worktree.ResolvePath(f), fix)
				}
				if rel == "." {
					return nil, fmt.Errorf("%s is the mounted folder %s itself, so its read-only overlay would share a target with the mount — "+
						"put it in a sub folder, or outside every mounted folder, and %s", f, s, fix)
				}
				out = append(out, sbxenv.Mount{Path: c, Target: filepath.Join(m.target, rel), ReadOnly: true})
				break
			}
		}
	}
	return out, nil
}

// symlinkBelow returns the first symlink among the components rel adds to
// root, root itself excluded: a link at or above a mount root is outside the VM.
func symlinkBelow(root, rel string) string {
	if rel == "." {
		return ""
	}
	p := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		p = filepath.Join(p, part)
		if st, err := os.Lstat(p); err == nil && st.Mode()&os.ModeSymlink != 0 {
			return p
		}
	}
	return ""
}

// relUnder compares component-wise: /a/bc is not under /a/b.
func relUnder(child, parent string) (string, bool) {
	rel, err := filepath.Rel(parent, child)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

// ofTemplate names where the template's definition is written, for a remedy.
func ofTemplate(res *nest.Resolved) string {
	return fmt.Sprintf("of template %q in %s", res.Template.Choice.Name(), res.Template.Defs.Path)
}

// mapStart turns `start:` into a VM path. A repo mounts at its main clone
// path, with -w too, so a worktree changes nothing here.
func mapStart(res *nest.Resolved, repos []nest.Repo, sel nest.Selection) (string, error) {
	switch res.Start {
	case "":
		if len(repos) == 0 {
			return "", fmt.Errorf("nest %q: no repo is selected, so the start folder has no default — keep a repo, "+
				"or set `start:` in %s", res.Name, res.File.Path)
		}
		return repos[0].Path, nil
	case ".":
		return res.File.Dir, nil
	}
	name, sub, _ := strings.Cut(filepath.ToSlash(res.Start), "/")
	for _, r := range repos {
		if r.Name == name {
			return filepath.Join(r.Path, filepath.FromSlash(sub)), nil
		}
	}
	var names []string
	for _, r := range res.Repos {
		if r.Name == name {
			return "", fmt.Errorf("nest %q: start %q is in repo %q, which %s dropped — keep that repo, "+
				"or fix `start:` in %s", res.Name, res.Start, name, selectionFlag(sel), res.File.Path)
		}
		names = append(names, r.Name)
	}
	return "", fmt.Errorf("nest %q: start %q names no repo of the nest (repos: %s) — fix `start:` in %s",
		res.Name, res.Start, strings.Join(names, ", "), res.File.Path)
}

func selectionFlag(sel nest.Selection) string {
	switch {
	case len(sel.Only) > 0:
		return "--only"
	case len(sel.Without) > 0:
		return "--without"
	case len(sel.Repos) > 0:
		return "--repo"
	case sel.Interactive:
		return "-i"
	}
	return "the recorded selection"
}

// hashedStart hashes an unmappable start as written, so it still differs from any mapped one.
func hashedStart(res *nest.Resolved, mapped string, mapErr error) string {
	if mapErr != nil {
		return "unmapped:" + res.Start
	}
	return mapped
}

// configHash covers what an instance is built from, never File nor Warnings:
// an edit to another nest of the same den.yaml must not report drift.
// A rebuilt template moves definition_hash or kit_image (its digest), so a Live instance reports drift.
func configHash(res *nest.Resolved, repos []nest.Repo, start, branch string) (string, error) {
	t := res.Template
	in := struct {
		Template       string            `yaml:"template"`
		Tag            string            `yaml:"tag"`
		KitImage       string            `yaml:"kit_image"`
		DefinitionHash string            `yaml:"definition_hash"`
		Kits           []string          `yaml:"kits"`
		Allow          []string          `yaml:"allow"`
		Env            map[string]string `yaml:"env"`
		Ports          []config.Port     `yaml:"ports"`
		Start          string            `yaml:"start"`
		Layers         []string          `yaml:"layers"`
		Profile        string            `yaml:"profile"`
		Repos          []string          `yaml:"repos"`
		Branch         string            `yaml:"branch"`
		Layout         string            `yaml:"layout"`
		Root           string            `yaml:"root"`
	}{
		Template: t.Choice.Value, Tag: sbxTemplate(t), KitImage: t.KitImage,
		Kits: res.Kits, Allow: res.Allow, Env: res.Env,
		Ports: res.Ports, Start: start, Layers: res.Layers, Profile: res.Profile,
		Branch: branch, Layout: res.Layout, Root: res.Root,
	}
	if t.Record != nil {
		in.DefinitionHash = t.Record.DefinitionHash
	}
	for _, r := range repos {
		in.Repos = append(in.Repos, r.Path)
	}
	raw, err := yaml.Marshal(in)
	if err != nil {
		return "", fmt.Errorf("hashing the config of nest %q: %w", res.Name, err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
