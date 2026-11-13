package instance

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/PillowPillow/den/internal/nest"
	"github.com/PillowPillow/den/internal/registry"
	"github.com/PillowPillow/den/internal/sbxenv"
	"github.com/PillowPillow/den/internal/worktree"
)

// EnvFileName is the one file den writes into a run dir.
const EnvFileName = "sbxenv.yaml"

// claudeHome is where the den-claude agent reads Claude's user files in the VM.
const claudeHome = "/home/agent/.claude"

// gitTrio is what -w mounts for one repo (S1): the worktree at the main clone
// path, the common git dir, and a read-only .git pointing at its admin dir.
type gitTrio struct {
	worktree, common, gitdir string
}

// Create makes the worktrees, writes the generated files and the record, then
// runs `sbx env create`. The record goes before sbx, so a failed create still
// leaves `den rm` something to replay.
func Create(ctx context.Context, d Deps, p *Plan) (registry.Record, error) {
	res := p.Resolved
	rec := registry.Record{
		Instance: p.Instance, Nest: res.Name, ConfigFile: res.File.Path,
		RunDir: p.RunDir, GenDir: p.GenDir, ConfigHash: p.ConfigHash, Start: p.Start,
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
			trio, created, err := ensureWorktree(ctx, d.Git, p, r)
			if err != nil {
				return registry.Record{}, err
			}
			trios[r.Name] = trio
			entry.Mount, entry.Worktree = trio.worktree, created
		}
		rec.Repos = append(rec.Repos, entry)
	}

	if err := writeGenerated(d.DenHome, p, trios); err != nil {
		return registry.Record{}, err
	}
	envFile := filepath.Join(p.RunDir, EnvFileName)
	env, err := sbxenv.Render(envOf(d.DenHome, p, trios))
	if err != nil {
		return registry.Record{}, err
	}
	if err := writeGenFile(envFile, env); err != nil {
		return registry.Record{}, err
	}
	if !res.File.Global {
		if err := ignoreDenDir(res.File.Dir); err != nil {
			return registry.Record{}, err
		}
	}

	rec.EnvFiles = append([]string{envFile}, res.Layers...)
	if err := registry.Write(d.DenHome, rec); err != nil {
		return registry.Record{}, err
	}
	args := append([]string{"--no-input", "env", "create", "--force", "--skip-host-commands"}, rec.EnvFiles...)
	if err := d.Sbx.Stream(ctx, d.Err, args...); err != nil {
		return rec, fmt.Errorf("creating %s: %w — fix the cause, then run `den up %s` again, or `den rm %s`",
			p.Instance, err, res.Name, p.Instance)
	}
	// The VM exists: a failed listing must not cost the user the attach.
	if err := d.Sbx.Stream(ctx, d.Out, "--no-input", "ports", p.Instance); err != nil {
		fmt.Fprintf(d.Err, "warning: listing the ports of %s: %v — run `sbx ports %s`\n", p.Instance, err, p.Instance)
	}
	return rec, nil
}

// ensureWorktree reports den as the creator of a worktree that was absent, or
// that the previous record of this instance already credited to den: `den rm`
// reclaims only those.
func ensureWorktree(ctx context.Context, g worktree.Git, p *Plan, r nest.Repo) (gitTrio, bool, error) {
	res := p.Resolved
	_, statErr := os.Stat(worktree.Path(res.Layout, res.Root, p.Worktree.Dir, r.Path))
	created := errors.Is(statErr, fs.ErrNotExist) || recordedAsCreated(p.Record, r.Path)
	wt, err := worktree.Ensure(ctx, g, res.Layout, res.Root, *p.Worktree, r.Path)
	if err != nil {
		return gitTrio{}, false, err
	}
	if err := worktree.Lock(ctx, g, r.Path, wt); err != nil {
		return gitTrio{}, false, err
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
func writeGenerated(denHome string, p *Plan, trios map[string]gitTrio) error {
	kit := filepath.Join(denClaudeKitDir(denHome), "spec.yaml")
	if current, err := os.ReadFile(kit); err != nil || !bytes.Equal(current, sbxenv.DenClaudeKit()) {
		if err := writeGenFile(kit, sbxenv.DenClaudeKit()); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(p.GenDir); err != nil {
		return fmt.Errorf("clearing %s: %w", p.GenDir, err)
	}
	if err := writeGenFile(filepath.Join(p.GenDir, "network", "spec.yaml"), sbxenv.NetworkKit(p.Resolved.Allow)); err != nil {
		return err
	}
	for _, r := range p.Repos {
		if t, ok := trios[r.Name]; ok {
			if err := writeGenFile(gitFilePath(p, r), sbxenv.GitFile(t.gitdir)); err != nil {
				return err
			}
		}
	}
	return nil
}

// denClaudeKitDir starts with a dot, which no instance name can: `den rm kits` must never own it.
func denClaudeKitDir(denHome string) string {
	return filepath.Join(denHome, "gen", ".kits", sbxenv.AgentName)
}

func gitFilePath(p *Plan, r nest.Repo) string {
	return filepath.Join(p.GenDir, "git", r.Name+".git")
}

func envOf(denHome string, p *Plan, trios map[string]gitTrio) sbxenv.Env {
	res := p.Resolved
	profile := filepath.Join(denHome, "profiles", res.Profile)
	e := sbxenv.Env{
		Name:      p.Instance,
		Kits:      append(append([]string{denClaudeKitDir(denHome)}, res.Kits...), filepath.Join(p.GenDir, "network")),
		Workspace: p.Workspace,
		Mounts:    []sbxenv.Mount{{Path: profile, Target: sbxenv.ProfileTarget, ReadOnly: true}},
		Env:       res.Env,
		Template:  res.Base.Image,
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
	for _, name := range []string{"CLAUDE.md", "skills"} {
		if _, err := os.Stat(filepath.Join(profile, name)); err == nil {
			e.Mounts = append(e.Mounts, sbxenv.Mount{Path: filepath.Join(profile, name), Target: path.Join(claudeHome, name), ReadOnly: true})
		}
	}
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
	return writeGenFile(ignore, []byte("*\n"))
}

func writeGenFile(name string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(name), err)
	}
	if err := os.WriteFile(name, content, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", name, err)
	}
	return nil
}

// Attach runs Claude in the instance, in its start folder (P1).
func Attach(ctx context.Context, d Deps, r registry.Record) error {
	return d.Sbx.Attach(ctx, "run", "--name", r.Instance, "--workdir", r.Start)
}

// Up creates the instance unless its VM runs, then attaches unless detach.
// A live instance is never recreated: den warns about drift instead.
func Up(ctx context.Context, d Deps, o Options, detach bool) error {
	p, err := Prepare(ctx, d, o)
	if err != nil {
		return err
	}
	if p.State == Live && o.Branch != "" {
		if err := checkBranch(p, o.Branch); err != nil {
			return err
		}
	}
	for _, w := range p.Resolved.Warnings {
		fmt.Fprintf(d.Err, "warning: %s\n", w)
	}
	if p.State == Live {
		if p.ConfigHash != p.Record.ConfigHash {
			fmt.Fprintf(d.Err, "warning: %s changed since %s was created — run `den rm %s` then `den up` to apply it\n",
				p.Resolved.File.Path, p.Instance, p.Instance)
		}
		if s := o.Selection; len(s.Only) > 0 || len(s.Without) > 0 || s.Interactive {
			fmt.Fprintf(d.Err, "warning: %s is ignored: %s is running with the repos it was created with — "+
				"run `den rm %s` then `den up` to change them\n", selectionFlag(s), p.Instance, p.Instance)
		}
	}
	var rec registry.Record
	if p.State == Live {
		rec = *p.Record
		rec.Start = p.Start
	} else if rec, err = Create(ctx, d, p); err != nil {
		return err
	}
	if detach {
		return nil
	}
	return Attach(ctx, d, rec)
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
