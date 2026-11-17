package instance

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/PillowPillow/den/v2/internal/registry"
	"github.com/PillowPillow/den/v2/internal/sbx"
	"github.com/PillowPillow/den/v2/internal/worktree"
)

// Remove deletes an instance den created: refusals first, then the VM, then the
// worktrees den made, then its files, the record last. There is no trash, so a
// dirty worktree refuses the whole removal, VM included, unless force.
func Remove(ctx context.Context, d Deps, instance string, force bool) error {
	rec, err := registry.Read(d.DenHome, instance)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("no den V2 record for %q — den only removes what it created; use `sbx rm %s`", instance, instance)
	}
	if err != nil {
		return err
	}
	file, err := registry.Path(d.DenHome, instance)
	if err != nil {
		return err
	}
	targets, err := removalPlan(d.DenHome, file, rec, force)
	if err != nil {
		return err
	}
	if err := checkSiblings(d.DenHome, instance, targets); err != nil {
		return fmt.Errorf("den rm %s removed nothing: %w", instance, err)
	}
	var refusals []error
	for _, t := range targets {
		if err := worktree.CheckRemovable(ctx, d.Git, t); err != nil {
			refusals = append(refusals, err)
		}
	}
	if len(refusals) > 0 {
		return fmt.Errorf("den rm %s removed nothing: %w", instance, errors.Join(refusals...))
	}

	boxes, err := sbx.Ls(ctx, d.Sbx)
	if err != nil {
		return err
	}
	if sbx.Find(boxes, instance) != nil {
		if err := d.Sbx.Stream(ctx, d.Err, vmRemoval(rec)...); err != nil {
			return fmt.Errorf("removing the VM of %s: %w — nothing else was removed; fix the cause, then run `den rm %s` again",
				instance, err, instance)
		}
	}

	var failed []error
	for _, t := range targets {
		if err := worktree.Remove(ctx, d.Git, t); err != nil {
			failed = append(failed, err)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("the VM of %s is gone but some worktrees stay, and so does %s: %w — fix the cause, then run `den rm %s` again",
			instance, file, errors.Join(failed...), instance)
	}

	for _, dir := range []string{rec.RunDir, rec.GenDir} {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("removing %s: %w — fix the cause, then run `den rm %s` again", dir, err, instance)
		}
	}
	return registry.Remove(d.DenHome, instance)
}

// removalPlan refuses a record that names a directory den never places, since
// Remove deletes those wholesale, and returns the worktrees den created.
// A repo den found in place (Worktree: false) is never a target.
func removalPlan(denHome, file string, rec registry.Record, force bool) ([]worktree.Target, error) {
	runDirs := []string{filepath.Join(denHome, "run", rec.Instance)}
	if filepath.IsAbs(rec.Project) {
		runDirs = append(runDirs, filepath.Join(rec.Project, ".den", "run", rec.Instance))
	}
	if !slices.Contains(runDirs, rec.RunDir) {
		return nil, fmt.Errorf("%s: run_dir %q is not a run dir den places (%s) — den deletes nothing it did not place; "+
			"fix the record or remove the instance by hand", file, rec.RunDir, strings.Join(runDirs, " or "))
	}
	if gen := filepath.Join(denHome, "gen", rec.Instance); rec.GenDir != gen {
		return nil, fmt.Errorf("%s: gen_dir %q is not %q, where den places it — den deletes nothing it did not place; "+
			"fix the record or remove the instance by hand", file, rec.GenDir, gen)
	}
	var targets []worktree.Target
	for _, r := range rec.Repos {
		if !r.Worktree {
			continue
		}
		if rec.Worktree == nil {
			return nil, fmt.Errorf("%s: repo %s is marked as a worktree den created, but the record has no worktree — "+
				"fix the record or remove the instance by hand", file, r.Name)
		}
		targets = append(targets, worktree.Target{
			Layout: rec.Worktree.Layout, Root: rec.Worktree.Root, Worktree: rec.Worktree.Dir,
			RepoPath: r.Repo, WorktreePath: r.Mount, Force: force,
		})
	}
	return targets, nil
}

// checkSiblings refuses to remove a worktree another instance mounts: two nests
// sharing a repo and a -w branch share its worktree, which only the first created.
// An unreadable record could be such a sibling, so it refuses too.
func checkSiblings(denHome, instance string, targets []worktree.Target) error {
	if len(targets) == 0 {
		return nil
	}
	records, broken, err := registry.List(denHome)
	if err != nil {
		return err
	}
	if len(broken) > 0 {
		return fmt.Errorf("%v — den cannot tell whether that record's instance mounts a worktree this one created; "+
			"fix or remove %s, then retry", broken[0].Err, broken[0].Path)
	}
	for _, t := range targets {
		mine := worktree.ResolvePath(t.WorktreePath)
		for _, other := range records {
			if other.Instance == instance {
				continue
			}
			for _, r := range other.Repos {
				if worktree.ResolvePath(r.Mount) == mine {
					return fmt.Errorf("worktree %s is also mounted by instance %s — run `den rm %s` first",
						t.WorktreePath, other.Instance, other.Instance)
				}
			}
		}
	}
	return nil
}

// vmRemoval prefers `env rm`, which undoes what `env create` set up; it needs every
// file the create read, and an empty list would name no environment at all.
// A file whose hash differs from the create's may have been rewritten by a VM
// that mounts it, so `env rm` would run what that VM wrote: `rm` reads no file.
func vmRemoval(rec registry.Record) []string {
	intact := len(rec.EnvFiles) > 0 && len(rec.EnvHashes) == len(rec.EnvFiles)
	for i := 0; intact && i < len(rec.EnvFiles); i++ {
		sum, err := fileHash(rec.EnvFiles[i])
		intact = err == nil && sum == rec.EnvHashes[i]
	}
	if !intact {
		return []string{"--no-input", "rm", "--force", rec.Instance}
	}
	return append([]string{"--no-input", "env", "rm", "--force", "--skip-host-commands"}, rec.EnvFiles...)
}
