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
	"sort"
	"strings"

	"golang.org/x/mod/semver"
	"gopkg.in/yaml.v3"

	"github.com/PillowPillow/den/v2/internal/config"
	"github.com/PillowPillow/den/v2/internal/nest"
	"github.com/PillowPillow/den/v2/internal/origin"
	"github.com/PillowPillow/den/v2/internal/prompt"
	"github.com/PillowPillow/den/v2/internal/registry"
	scanrepos "github.com/PillowPillow/den/v2/internal/repos"
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
	// SessionsDir is <denHome>/sessions/<instance>, mounted at sbxenv.SessionsVMPath; empty when the
	// nest's sessions is false, and on the Live path.
	SessionsDir string
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
	if _, err := CheckSbx(ctx, d.Sbx); err != nil {
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
	// A running VM keeps the mounts it was created with: there is nothing to consent to.
	if res.MissingErr != nil && state == Live {
		res.Warnings = append(res.Warnings, res.MissingErr.Error())
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
	if wt != nil && res.Layout == "central" {
		if err := checkWorktreeFolders(res, repos, wt); err != nil {
			return nil, err
		}
	}
	if err := checkRepos(ctx, d.Git, d.DenHome, res, repos, wt != nil); err != nil {
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
	if res.Sessions {
		p.SessionsDir = SessionsDir(d.DenHome, instance)
		if err := checkSessions(p); err != nil {
			return nil, err
		}
	}
	mounts, err := writableMounts(ctx, d.Git, res, repos, wt, p.Workspace, p.SessionsDir)
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
		// The start folder may be in a repo with no folder: name that cause, not only its effect.
		if res.MissingErr != nil {
			return nil, fmt.Errorf("%w\n%v", res.MissingErr, err)
		}
		return nil, err
	}
	p.Repos, p.Start, p.Workdir = repos, start, o.Workdir
	if p.ConfigHash, err = configHash(res, repos, start, o.Branch); err != nil {
		return nil, err
	}
	// Last, so den never asks and then refuses on a check that did not need the answer.
	if res.MissingErr != nil {
		if err := confirmMissing(ctx, d, o, res.MissingErr, instance); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// confirmMissing asks before den starts an instance without the keys that have no folder. Without
// a terminal there is nobody to ask, and an instance quietly short of a repo is what den refuses.
func confirmMissing(ctx context.Context, d Deps, o Options, missing *nest.MissingReposError, instance string) error {
	n := len(missing.Missing)
	what := "this repo"
	if n > 1 {
		what = fmt.Sprintf("these %d repos", n)
	}
	if o.NoInput {
		return fmt.Errorf("%w\nden exec never asks: clone %s, or run `den up %s` in a terminal to start %s without %s",
			missing, what, o.Nest, instance, what)
	}
	if d.IsTTY == nil || !d.IsTTY() {
		return fmt.Errorf("%w\nden up asks before it starts %s without %s: clone %s, or run it in a terminal",
			missing, instance, what, what)
	}
	fmt.Fprintf(d.Err, "warning: %s\n", missing)
	ok, err := d.Prompt.Confirm(ctx, prompt.ConfirmRequest{Question: fmt.Sprintf("start %s without %s?", instance, what)})
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s not started: clone %s, then run `den up %s` again", instance, what, o.Nest)
	}
	return nil
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

// CheckSbx refuses an sbx below MinVersion, and one whose version it cannot read. It returns the
// release version it read.
func CheckSbx(ctx context.Context, r sbx.Runner) (string, error) {
	raw, err := sbx.Version(ctx, r)
	if err != nil {
		return "", err
	}
	v, ok := sbx.ReleaseVersion(raw)
	if !ok {
		return "", fmt.Errorf("could not read a version out of `sbx version` (got %q); den needs sbx %s or later — "+
			"check which sbx is on PATH", raw, sbx.MinVersion)
	}
	if semver.Compare(v, sbx.MinVersion) < 0 {
		return "", fmt.Errorf("sbx %s is older than %s, the oldest sbx den supports — upgrade sbx", raw, sbx.MinVersion)
	}
	return v, nil
}

// SessionsDir is the folder that keeps an instance's Claude sessions on the host. It is keyed by
// the instance name alone, so the instance made again under that name finds its sessions, and
// `claude --continue` resumes the last one. It lies outside the run and gen dirs: `den rm` deletes those.
func SessionsDir(denHome, instance string) string {
	return filepath.Join(denHome, "sessions", instance)
}

// checkSessions refuses a sessions path Create could not make a folder of: a file, or a link whose
// folder is missing, as when the disk that keeps the sessions is not mounted. Create would find out
// only after it made the worktrees, and with a bare `mkdir: file exists`. An absent path is fine:
// Create makes it.
func checkSessions(p *Plan) error {
	for _, dir := range []string{filepath.Dir(p.SessionsDir), p.SessionsDir} {
		if _, err := os.Lstat(dir); err != nil {
			continue
		}
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			return fmt.Errorf("the sessions folder %s is a file, or a link to a folder that is missing — move it away or "+
				"bring its folder back, then run `%s` again, or set `sessions: false` on nest %q in %s",
				dir, upCommand(p), p.Resolved.Name, p.Resolved.File.Path)
		}
	}
	return nil
}

func runDir(denHome string, res *nest.Resolved, instance string) string {
	if res.File.Global {
		return filepath.Join(denHome, "run", instance)
	}
	return filepath.Join(res.File.Dir, ".den", "run", instance)
}

func checkRepos(ctx context.Context, g worktree.Git, denHome string, res *nest.Resolved, repos []nest.Repo, withWorktree bool) error {
	for _, r := range repos {
		st, err := os.Stat(r.Path)
		if err != nil {
			return fmt.Errorf("nest %q: repo %s: %v — %s", res.Name, r.Path, &config.FileError{Err: err}, repoFix(denHome, res, r))
		}
		if !st.IsDir() {
			return fmt.Errorf("nest %q: repo %s is not a directory — %s", res.Name, r.Path, repoFix(denHome, res, r))
		}
		if !withWorktree {
			continue
		}
		// The root, not any directory git answers for: a worktree of the
		// enclosing repo would put the wrong tree at the repo's VM path.
		out, err := g.Run(ctx, r.Path, "rev-parse", "--show-toplevel")
		if err != nil || worktree.ResolvePath(strings.TrimSpace(string(out))) != worktree.ResolvePath(r.Path) {
			return fmt.Errorf("nest %q: repo %s is not the root of a git repository, and -w makes a worktree of every repo — "+
				"drop -w, or %s", res.Name, r.Path, repoFix(denHome, res, r))
		}
	}
	return nil
}

// checkWorktreeFolders refuses what worktree.Path would collapse: central puts every worktree at
// <root>/<branch>/<folder name>, so two repos sharing a folder name would land on one path.
func checkWorktreeFolders(res *nest.Resolved, repos []nest.Repo, wt *worktree.Name) error {
	seen := map[string]nest.Repo{}
	for _, r := range repos {
		folder := filepath.Base(r.Path)
		if first, ok := seen[folder]; ok {
			return fmt.Errorf("repos %s and %s share the folder name %q, and -w puts both at %s — "+
				"set worktrees.layout: per-repo, or drop one", first.Name, r.Name, folder, worktree.Path(res.Layout, res.Root, wt.Dir, r.Path))
		}
		seen[folder] = r
	}
	return nil
}

func repoFix(denHome string, res *nest.Resolved, r nest.Repo) string {
	if r.AdHoc {
		return "fix or drop `--repo " + r.Path + "`"
	}
	if r.FromTable {
		id, _ := origin.Identity(r.Origin)
		return fmt.Sprintf("run `den scan <the folder that holds the clone>`: %s maps %s to this folder",
			scanrepos.TablePath(denHome), id)
	}
	if r.Key != "" {
		return fmt.Sprintf("fix `repos.%s` in %s", r.Key, r.DeclaredIn)
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
func writableMounts(ctx context.Context, g worktree.Git, res *nest.Resolved, repos []nest.Repo, wt *worktree.Name, workspace, sessions string) ([]rwMount, error) {
	mounts := []rwMount{{workspace, workspace}}
	if sessions != "" {
		mounts = append(mounts, rwMount{sessions, sbxenv.SessionsVMPath})
	}
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
		// The keys started without: once one is cloned the hash moves, and a Live instance warns of drift.
		Missing []string `yaml:"missing,omitempty"`
		// Only false is hashed: a key always present would move the hash of every instance made before it.
		NoAutoTrust bool `yaml:"no_auto_trust,omitempty"`
		// Omitted when unset, for the same reason: an instance made before resources: keeps its hash.
		CPUs   int    `yaml:"cpus,omitempty"`
		Memory string `yaml:"memory,omitempty"`
		// Only false is hashed, as for auto_trust: an instance made before sessions: keeps its hash.
		NoSessions bool `yaml:"no_sessions,omitempty"`
	}{
		Template: t.Choice.Value, Tag: sbxTemplate(t), KitImage: t.KitImage,
		Kits: res.Kits, Allow: res.Allow, Env: res.Env,
		Ports: res.Ports, Start: start, Layers: res.Layers, Profile: res.Profile,
		Branch: branch, Layout: res.Layout, Root: res.Root, NoAutoTrust: !res.AutoTrust,
		CPUs: res.CPUs, Memory: res.Memory, NoSessions: !res.Sessions,
	}
	if t.Record != nil {
		in.DefinitionHash = t.Record.DefinitionHash
	}
	for _, r := range repos {
		in.Repos = append(in.Repos, r.Path)
	}
	if res.MissingErr != nil {
		for _, m := range res.MissingErr.Missing {
			in.Missing = append(in.Missing, m.Key)
		}
		sort.Strings(in.Missing)
	}
	raw, err := yaml.Marshal(in)
	if err != nil {
		return "", fmt.Errorf("hashing the config of nest %q: %w", res.Name, err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
