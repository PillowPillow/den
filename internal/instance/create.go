package instance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/PillowPillow/den/v2/internal/nest"
	"github.com/PillowPillow/den/v2/internal/registry"
	"github.com/PillowPillow/den/v2/internal/sbx"
	"github.com/PillowPillow/den/v2/internal/sbxenv"
	"github.com/PillowPillow/den/v2/internal/template"
	"github.com/PillowPillow/den/v2/internal/worktree"
)

// EnvFileName is the one file den writes into a run dir.
const EnvFileName = "sbxenv.yaml"

// gitTrio is what -w mounts for one repo (S1): the worktree at the main clone
// path, the common git dir, and a read-only .git pointing at its admin dir.
type gitTrio struct {
	worktree, common, gitdir string
}

// Create makes the worktrees, writes the generated files and the record, then
// runs `sbx env create`. The record goes before sbx, so a failed create still
// leaves `den rm` something to replay; a failure before it rolls back the
// worktrees this call made, which no record would ever name.
func Create(ctx context.Context, d Deps, p *Plan) (registry.Record, error) {
	var fresh []worktree.Target
	rec, err := writeRecord(ctx, d, p, &fresh)
	if err != nil {
		return registry.Record{}, rollback(context.WithoutCancel(ctx), d.Git, fresh, err)
	}
	args := append([]string{"--no-input", "env", "create", "--force", "--skip-host-commands"}, rec.EnvFiles...)
	if err := d.Sbx.Stream(ctx, d.Err, args...); err != nil {
		return rec, fmt.Errorf("creating %s: %w%s — fix the cause, then run `%s` again, or `den rm %s`",
			p.Instance, err, imageHint(p), upCommand(p), p.Instance)
	}
	// The VM exists: a failed listing must not cost the user the attach.
	if err := d.Sbx.Stream(ctx, d.Out, "--no-input", "port", "ls", p.Instance); err != nil {
		fmt.Fprintf(d.Err, "warning: listing the ports of %s: %v — run `sbx port ls %s`\n", p.Instance, err, p.Instance)
	}
	return rec, nil
}

// writeRecord does every step up to and including registry.Write, appending to
// fresh each worktree it creates as soon as it exists.
func writeRecord(ctx context.Context, d Deps, p *Plan, fresh *[]worktree.Target) (registry.Record, error) {
	res := p.Resolved
	rec := registry.Record{
		Instance: p.Instance, Nest: res.Name, ConfigFile: res.File.Path,
		RunDir: p.RunDir, GenDir: p.GenDir, ConfigHash: p.ConfigHash, Start: p.Start,
	}
	rec.Template = res.Template.Choice.Value
	if !res.Template.Choice.IsImage() {
		rec.Tag = res.Template.Choice.Tag()
	}
	if !res.File.Global {
		rec.Project = res.File.Dir
	}
	if p.Worktree != nil {
		rec.Worktree = &registry.Worktree{Branch: p.Worktree.Branch, Dir: p.Worktree.Dir, Layout: res.Layout, Root: res.Root}
	}

	trios := map[string]gitTrio{}
	for _, r := range p.Repos {
		entry := registry.Repo{Name: r.Name, Repo: r.Path, Mount: r.Path, Target: r.Path}
		if p.Worktree != nil {
			trio, created, err := ensureWorktree(ctx, d.Git, p, r, fresh)
			if err != nil {
				return registry.Record{}, err
			}
			trios[r.Name] = trio
			entry.Mount, entry.Worktree = trio.worktree, created
		}
		rec.Repos = append(rec.Repos, entry)
	}

	if err := writeGenerated(p, trios, repoMapOf(p, rec)); err != nil {
		return registry.Record{}, err
	}
	envFile := filepath.Join(p.RunDir, EnvFileName)
	env, err := sbxenv.Render(envOf(p, trios))
	if err != nil {
		return registry.Record{}, err
	}
	if err := WriteGenFile(envFile, env); err != nil {
		return registry.Record{}, err
	}
	if !res.File.Global {
		if err := ignoreDenDir(res.File.Dir); err != nil {
			return registry.Record{}, err
		}
	}

	rec.EnvFiles = append([]string{envFile}, res.Layers...)
	for _, f := range rec.EnvFiles {
		sum, err := fileHash(f)
		if err != nil {
			return registry.Record{}, err
		}
		rec.EnvHashes = append(rec.EnvHashes, sum)
	}
	// sbx creates a missing mount source itself, but world-readable: a transcript holds every prompt
	// and every tool output of the session, secrets a command printed included. Last before the record:
	// a step that failed after it would leave an empty folder that no record lets `den rm` remove.
	if p.SessionsDir != "" {
		if err := ensureSessions(d, p); err != nil {
			return registry.Record{}, fmt.Errorf("creating the sessions folder %s: %w — fix the cause, then run `%s` again, "+
				"or set `sessions: false` on nest %q in %s", p.SessionsDir, err, upCommand(p), res.Name, res.File.Path)
		}
	}
	if err := registry.Write(d.DenHome, rec); err != nil {
		return registry.Record{}, err
	}
	return rec, nil
}

// ensureSessions makes the instance's sessions folder and its parent, both with mode 700 again when
// they already exist: the VM of an earlier instance wrote the folder and can have changed its mode,
// and MkdirAll leaves an existing one as it is. A symlink is the user's own placement and keeps its
// target's mode. A folder that already holds something is named on stderr: the new VM reads every
// transcript and loads the memory an earlier one left, which nothing else would tell.
func ensureSessions(d Deps, p *Plan) error {
	if err := os.MkdirAll(p.SessionsDir, 0o700); err != nil {
		return err
	}
	for _, dir := range []string{filepath.Dir(p.SessionsDir), p.SessionsDir} {
		st, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		if st.Mode()&fs.ModeSymlink != 0 {
			continue
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(p.SessionsDir)
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		fmt.Fprintf(d.Err, "%s loads the sessions kept in %s\n", p.Instance, p.SessionsDir)
	}
	return nil
}

// fileHash is the hex sha256 of a file's bytes.
func fileHash(name string) (string, error) {
	raw, err := os.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("hashing %s: %w", name, err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// rollback never forces: a worktree the user already wrote into stays, and the error names it.
func rollback(ctx context.Context, g worktree.Git, fresh []worktree.Target, cause error) error {
	errs := []error{cause}
	for i := len(fresh) - 1; i >= 0; i-- {
		if err := worktree.Remove(ctx, g, fresh[i]); err != nil {
			errs = append(errs, fmt.Errorf("rolling back worktree %s failed, so it stays on disk: %w", fresh[i].WorktreePath, err))
		}
	}
	return errors.Join(errs...)
}

// ensureWorktree reports den as the creator of a worktree that was absent, or
// that the previous record of this instance already credited to den: `den rm`
// reclaims only those.
func ensureWorktree(ctx context.Context, g worktree.Git, p *Plan, r nest.Repo, fresh *[]worktree.Target) (gitTrio, bool, error) {
	res := p.Resolved
	_, statErr := os.Stat(worktree.Path(res.Layout, res.Root, p.Worktree.Dir, r.Path))
	absent := errors.Is(statErr, fs.ErrNotExist)
	created := absent || recordedAsCreated(p.Record, r.Path)
	wt, err := worktree.Ensure(ctx, g, res.Layout, res.Root, *p.Worktree, r.Path)
	if err != nil {
		return gitTrio{}, false, err
	}
	if absent {
		*fresh = append(*fresh, worktree.Target{Layout: res.Layout, Root: res.Root, Worktree: p.Worktree.Dir, RepoPath: r.Path, WorktreePath: wt})
	}
	// A worktree found in place is the user's: locking it would leave den's
	// lock on a tree `den rm` never reclaims.
	if created {
		if err := worktree.Lock(ctx, g, r.Path, wt); err != nil {
			return gitTrio{}, false, err
		}
	}
	admin, err := worktree.AdminDir(ctx, g, wt)
	if err != nil {
		return gitTrio{}, false, err
	}
	common, err := worktree.CommonGitDir(ctx, g, r.Path)
	if err != nil {
		return gitTrio{}, false, err
	}
	// The VM sees the common dir at /den-git/<repo> only: an admin dir anywhere
	// else would leave the generated .git pointing at nothing.
	rel, err := filepath.Rel(worktree.ResolvePath(common), admin)
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if err != nil || len(parts) != 2 || parts[0] != "worktrees" || parts[1] == ".." || parts[1] == "." {
		return gitTrio{}, false, fmt.Errorf("worktree %s: git keeps its admin directory at %s, not under %s — "+
			"den can only mount that layout; run `git worktree repair` in %s, then retry", wt, admin,
			filepath.Join(common, "worktrees"), r.Path)
	}
	return gitTrio{worktree: wt, common: common, gitdir: path.Join(sbxenv.GitMountRoot, r.Name, filepath.ToSlash(rel))}, created, nil
}

func recordedAsCreated(rec *registry.Record, repo string) bool {
	if rec == nil {
		return false
	}
	for _, r := range rec.Repos {
		if r.Repo == repo && r.Worktree {
			return true
		}
	}
	return false
}

// writeGenerated rewrites GenDir from scratch, so a repo the selection dropped leaves no stale .git file behind.
func writeGenerated(p *Plan, trios map[string]gitTrio, repoMap sbxenv.RepoMap) error {
	if err := os.RemoveAll(p.GenDir); err != nil {
		return fmt.Errorf("clearing %s: %w", p.GenDir, err)
	}
	// One kit per instance (T3): a kit shared by instances on two templates would carry the image of
	// whichever was created last, and sbx reads it again at every create.
	if err := WriteDenClaudeKit(denClaudeKitDir(p), p.Resolved.Template.KitImage, p.Resolved.AutoTrust); err != nil {
		return err
	}
	if err := writeRepoMap(denClaudeKitDir(p), repoMap); err != nil {
		return err
	}
	if err := WriteGenFile(filepath.Join(denClaudeKitDir(p), sbxenv.TrustJSONFile), sbxenv.RenderTrustJSON(trustedOf(p))); err != nil {
		return err
	}
	if err := WriteGenFile(filepath.Join(p.GenDir, "network", "spec.yaml"), sbxenv.NetworkKit(p.Resolved.Allow)); err != nil {
		return err
	}
	for _, r := range p.Repos {
		if t, ok := trios[r.Name]; ok {
			if err := WriteGenFile(gitFilePath(p, r), sbxenv.GitFile(t.gitdir)); err != nil {
				return err
			}
		}
	}
	return nil
}

// WriteDenClaudeKit writes <dir>/spec.yaml, the den-claude kit on image.
func WriteDenClaudeKit(dir, image string, autoTrust bool) error {
	spec, err := sbxenv.DenClaudeKit(image, autoTrust)
	if err != nil {
		return err
	}
	return WriteGenFile(filepath.Join(dir, "spec.yaml"), spec)
}

// trustedOf lists every mounted repo, then the start folder when it differs: a `start: .` project
// folder is no mounted repo, and nothing measured shows that an entry at a repo root covers a sub folder.
func trustedOf(p *Plan) []string {
	if !p.Resolved.AutoTrust {
		return nil
	}
	var out []string
	for _, r := range p.Repos {
		out = append(out, r.Path)
	}
	if !slices.Contains(out, p.Start) {
		out = append(out, p.Start)
	}
	return out
}

func denClaudeKitDir(p *Plan) string {
	return filepath.Join(p.GenDir, sbxenv.AgentName)
}

// sbxTemplate is the sbx template the VM starts from: the template's tag, or "" for an image, which
// the den-claude kit carries instead.
func sbxTemplate(t *template.Resolved) string {
	if t.Choice.IsImage() {
		return ""
	}
	return t.Choice.Tag()
}

func gitFilePath(p *Plan, r nest.Repo) string {
	return filepath.Join(p.GenDir, "git", r.Name+".git")
}

func envOf(p *Plan, trios map[string]gitTrio) sbxenv.Env {
	res := p.Resolved
	// A copy: res.Env feeds configHash, and a DEN_REPOS in it would report drift on every attach.
	env := make(map[string]string, len(res.Env)+1)
	for k, v := range res.Env {
		env[k] = v
	}
	env[sbxenv.RepoMapEnv] = sbxenv.RepoMapVMPath
	e := sbxenv.Env{
		Name:      p.Instance,
		Kits:      append(append([]string{denClaudeKitDir(p)}, res.Kits...), filepath.Join(p.GenDir, "network")),
		Workspace: p.Workspace,
		Env:       env,
		Template:  sbxTemplate(res.Template),
		CPUs:      res.CPUs,
		Memory:    res.Memory,
	}
	for _, r := range p.Repos {
		t, ok := trios[r.Name]
		if !ok {
			e.Mounts = append(e.Mounts, sbxenv.Mount{Path: r.Path})
			continue
		}
		e.Mounts = append(e.Mounts,
			sbxenv.Mount{Path: t.worktree, Target: r.Path},
			sbxenv.Mount{Path: t.common, Target: path.Join(sbxenv.GitMountRoot, r.Name)},
			sbxenv.Mount{Path: gitFilePath(p, r), Target: filepath.Join(r.Path, ".git"), ReadOnly: true})
	}
	if p.SessionsDir != "" {
		e.Mounts = append(e.Mounts, sbxenv.Mount{Path: p.SessionsDir, Target: sbxenv.SessionsVMPath})
	}
	// Last: a later entry wins in sbx, and an overlay sits under a read-write mount (P12, S6).
	e.Mounts = append(e.Mounts, p.Overlays...)
	for _, port := range res.Ports {
		e.Ports = append(e.Ports, sbxenv.Port{Sandbox: port.Sandbox, Host: port.Host})
	}
	return e
}

// ignoreDenDir keeps run dirs out of the project's git status; a .gitignore the user wrote stays theirs.
func ignoreDenDir(project string) error {
	ignore := filepath.Join(project, ".den", ".gitignore")
	if _, err := os.Stat(ignore); err == nil {
		return nil
	}
	return WriteGenFile(ignore, []byte("*\n"))
}

// WriteGenFile writes a den-generated file, creating its directory.
func WriteGenFile(name string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(name), err)
	}
	if err := os.WriteFile(name, content, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", name, err)
	}
	return nil
}

// Attach runs Claude in the instance, in workdir when set, else in its start folder (P1). With
// addDirs, every recorded repo that neither is nor holds that folder becomes an --add-dir (P17).
// The arguments go after sbx run's `--`, which appends them to the kit entrypoint: the entrypoint
// itself never passed one (P15). Without the variable, --add-dir loads a folder's skills but not
// its CLAUDE.md; it rides on this run, not in the kit's environment block, which auto_trust: false drops.
func Attach(ctx context.Context, d Deps, r registry.Record, workdir string, addDirs bool) error {
	start := startOf(r, workdir)
	args := []string{"run", "--name", r.Instance, "--workdir", start}
	var dirs []string
	if addDirs {
		for _, repo := range r.Repos {
			if _, held := relUnder(start, repo.Target); !held {
				dirs = append(dirs, "--add-dir", repo.Target)
			}
		}
	}
	if len(dirs) > 0 {
		args = append(append(args, "-e", "CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1", "--"), dirs...)
	}
	return d.Sbx.Attach(ctx, args...)
}

func startOf(r registry.Record, workdir string) string {
	if workdir != "" {
		return workdir
	}
	return r.Start
}

// ensure prepares the instance and creates it unless its VM runs. A live
// instance is never recreated: den warns about drift instead.
func ensure(ctx context.Context, d Deps, o Options) (*Plan, registry.Record, error) {
	p, err := Prepare(ctx, d, o)
	if err != nil {
		return nil, registry.Record{}, err
	}
	if p.State == Live && o.Branch != "" {
		if err := checkBranch(p, o.Branch); err != nil {
			return nil, registry.Record{}, err
		}
	}
	for _, w := range p.Resolved.Warnings {
		fmt.Fprintf(d.Err, "warning: %s\n", w)
	}
	if p.State == Live {
		if p.ConfigHash != p.Record.ConfigHash {
			fmt.Fprintf(d.Err, "warning: %s changed since %s was created — run `den rm %s` then `%s` to apply it\n",
				p.Resolved.File.Path, p.Instance, p.Instance, upCommand(p))
		}
		if s := o.Selection; len(s.Only) > 0 || len(s.Without) > 0 || len(s.Repos) > 0 || s.Interactive {
			fmt.Fprintf(d.Err, "warning: %s is ignored: %s is running with the repos it was created with — "+
				"run `den rm %s` then `%s` to change them\n", selectionFlag(s), p.Instance, p.Instance, upCommand(p))
		}
		return p, *p.Record, nil
	}
	rec, err := Create(ctx, d, p)
	if err != nil {
		return nil, registry.Record{}, err
	}
	return p, rec, nil
}

// imageHint names the key to fix when the VM failed to start from an image: a mistyped short name
// reads as an image reference, so the user otherwise sees sbx's pull error and no hint that den
// looked for a template first. A template's failure needs none: its name was found. It is worded
// as a condition because the attach error it follows can also be a first session that exited
// non-zero or a Ctrl-C, where the image started fine.
func imageHint(p *Plan) string {
	c := p.Resolved.Template.Choice
	if !c.IsImage() {
		return ""
	}
	return fmt.Sprintf(": if the VM never started, no template is named %q in %s, and sbx could not start the image %s — fix %s",
		c.Value, strings.Join(c.Searched, " or "), c.Image, c.Origin)
}

// Up creates the instance unless its VM runs, then attaches unless detach.
func Up(ctx context.Context, d Deps, o Options, detach bool) error {
	p, rec, err := ensure(ctx, d, o)
	if err != nil {
		return err
	}
	if p.State != Live {
		printAlerts(ctx, d, rec.Instance)
	}
	for _, n := range template.Notices(ctx, d.Git, p.Resolved.Template) {
		fmt.Fprintln(d.Err, n)
	}
	if detach {
		err = startDetached(ctx, d, p, rec.Instance)
	} else {
		err = Attach(ctx, d, rec, p.Workdir, p.Resolved.AddDirs)
	}
	if err != nil && p.State != Live {
		return fmt.Errorf("%w%s", err, imageHint(p))
	}
	return err
}

// startDetached leaves the VM running whether ensure created it, found it
// running or found it stopped: ensure never starts a stopped Live VM, and
// `sbx run --detach` is the sbx call that starts one without attaching.
func startDetached(ctx context.Context, d Deps, p *Plan, instance string) error {
	if _, err := d.Sbx.Run(ctx, "--no-input", "run", "--detach", "--name", instance); err != nil {
		return fmt.Errorf("starting %s: %w — run `%s` to attach, or `den rm %s` then `%s --detach`",
			instance, err, upCommand(p), instance, upCommand(p))
	}
	return nil
}

// Exec is Up's scripted twin: it never asks, and pipes one command through sbx
// in the start folder. The command's own status comes back as a *sbx.ChildExit.
func Exec(ctx context.Context, d Deps, o Options, argv []string) error {
	o.NoInput = true
	p, rec, err := ensure(ctx, d, o)
	if err != nil {
		return err
	}
	args := append([]string{"--no-input", "exec", "-w", startOf(rec, p.Workdir), rec.Instance}, argv...)
	if err := d.Sbx.Pipe(ctx, args...); err != nil {
		if code, ok := sbx.ExitCodeOf(err); ok && code > 0 {
			return &sbx.ChildExit{Code: code}
		}
		return err
	}
	return nil
}

// upCommand is the `den up` that makes p again, -w included.
func upCommand(p *Plan) string {
	if p.Worktree != nil {
		return fmt.Sprintf("den up %s -w %s", p.Resolved.Name, p.Worktree.Branch)
	}
	return "den up " + p.Resolved.Name
}

// checkBranch refuses a branch other than the recorded one: two branches can
// flatten to one instance name, and attaching would hand over the wrong branch.
func checkBranch(p *Plan, typed string) error {
	recorded := ""
	if p.Record.Worktree != nil {
		recorded = p.Record.Worktree.Branch
	}
	if recorded == typed {
		return nil
	}
	return fmt.Errorf("instance %s was created from branch %q, not %q: both name the same instance — "+
		"pick another branch name, or run `den rm %s` first", p.Instance, recorded, typed, p.Instance)
}
