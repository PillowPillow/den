package instance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/mod/semver"
	"gopkg.in/yaml.v3"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
	"github.com/PillowPillow/den/internal/registry"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/worktree"
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

	repos, err := nest.Select(ctx, res, o.Selection, d.Prompt)
	if err != nil {
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
	if err := checkProfile(d.DenHome, res); err != nil {
		return nil, err
	}
	if err := checkImage(ctx, d.Sbx, res); err != nil {
		return nil, err
	}
	if err := checkKits(res); err != nil {
		return nil, err
	}
	if err := checkLayers(res, repos, p.Workspace); err != nil {
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
	if r.Key != "" {
		return fmt.Sprintf("fix `repos.%s` in %s", r.Key, res.GlobalFile)
	}
	return fmt.Sprintf("fix `repos:` of nest %q in %s", res.Name, res.File.Path)
}

func checkProfile(denHome string, res *nest.Resolved) error {
	path := filepath.Join(denHome, "profiles", res.Profile, "settings.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("profile %q of nest %q: %s: %v — create it with "+
			`{"permissions": {"defaultMode": "bypassPermissions"}}`, res.Profile, res.Name, path, &config.FileError{Err: err})
	}
	var s struct {
		Permissions struct {
			DefaultMode string `json:"defaultMode"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return fmt.Errorf("%s: %v — fix that JSON", path, err)
	}
	// A17: Claude always runs in bypassPermissions in a den VM, and only user
	// settings or a flag can turn it on.
	if s.Permissions.DefaultMode != "bypassPermissions" {
		return fmt.Errorf(`%s: permissions.defaultMode is %q; a den VM runs Claude in "bypassPermissions" — set it there`,
			path, s.Permissions.DefaultMode)
	}
	return nil
}

func checkImage(ctx context.Context, r sbx.Runner, res *nest.Resolved) error {
	list, err := sbx.Templates(ctx, r)
	if err != nil {
		return err
	}
	if sbx.FindTemplate(list, res.Base.Image) == nil {
		return fmt.Errorf("base %q: image %q is not among sbx's templates — run `den build %s`",
			res.BaseName, res.Base.Image, res.BaseName)
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

// kitFix names every declaration of k: res.Kits merges the base's and the nest's.
func kitFix(res *nest.Resolved, k string) string {
	var at []string
	if slices.Contains(res.Base.Kits, k) {
		at = append(at, fmt.Sprintf("`kits:` of base %q in %s", res.BaseName, res.BaseFile.Path))
	}
	if n, ok := res.File.File.Nests[res.Name]; !ok || slices.Contains(n.Kits, k) || len(at) == 0 {
		at = append(at, fmt.Sprintf("`kits:` of nest %q in %s", res.Name, res.File.Path))
	}
	return "fix " + strings.Join(at, " and ")
}

// checkLayers enforces P7: sbx acts on a layer at every create and rm, so a
// layer the VM can rewrite would hand the VM those host-side actions.
func checkLayers(res *nest.Resolved, repos []nest.Repo, workspace string) error {
	type mounted struct{ what, dir string }
	var dirs []mounted
	if !res.File.Global {
		dirs = append(dirs, mounted{"the project folder", res.File.Dir})
	}
	for _, r := range repos {
		dirs = append(dirs, mounted{"repo " + r.Name, r.Path})
	}
	dirs = append(dirs, mounted{"the worktree root", res.Root}, mounted{"the workspace", workspace})
	for _, l := range res.Layers {
		st, err := os.Stat(l)
		if err != nil {
			return fmt.Errorf("layer %s: %v — fix `layers:` of nest %q in %s", l, &config.FileError{Err: err}, res.Name, res.File.Path)
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("layer %s is not a file — fix `layers:` of nest %q in %s", l, res.Name, res.File.Path)
		}
		for _, m := range dirs {
			if within(l, m.dir) {
				return fmt.Errorf("layer %s is under %s (%s), which the VM can write — move it out of every "+
					"mounted folder and fix `layers:` of nest %q in %s", l, m.what, m.dir, res.Name, res.File.Path)
			}
		}
	}
	return nil
}

// within checks the lexical and the resolved form of both paths: a symlink
// inside a mount resolves outside it, yet the VM can replace that symlink.
func within(child, parent string) bool {
	for _, c := range []string{filepath.Clean(child), worktree.ResolvePath(child)} {
		for _, p := range []string{filepath.Clean(parent), worktree.ResolvePath(parent)} {
			if under(c, p) {
				return true
			}
		}
	}
	return false
}

// under compares component-wise: /a/bc is not under /a/b.
func under(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
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
func configHash(res *nest.Resolved, repos []nest.Repo, start, branch string) (string, error) {
	in := struct {
		Base    string            `yaml:"base"`
		Image   string            `yaml:"image"`
		Kits    []string          `yaml:"kits"`
		Allow   []string          `yaml:"allow"`
		Env     map[string]string `yaml:"env"`
		Ports   []config.Port     `yaml:"ports"`
		Start   string            `yaml:"start"`
		Layers  []string          `yaml:"layers"`
		Profile string            `yaml:"profile"`
		Repos   []string          `yaml:"repos"`
		Branch  string            `yaml:"branch"`
		Layout  string            `yaml:"layout"`
		Root    string            `yaml:"root"`
	}{
		Base: res.BaseName, Image: res.Base.Image, Kits: res.Kits, Allow: res.Allow, Env: res.Env,
		Ports: res.Ports, Start: start, Layers: res.Layers, Profile: res.Profile,
		Branch: branch, Layout: res.Layout, Root: res.Root,
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
