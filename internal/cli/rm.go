package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/worktree"
	"github.com/spf13/cobra"
)

// gitProbeTimeout bounds EACH call to worktree.Remove (one per repo). Remove
// chains several sequential git probes, and a repo on a dead network mount
// would hang them all, so `den rm` would never return.
//
// Package variable, not const: a test must be able to shrink it to check the
// deadline is really set without waiting 30s.
var gitProbeTimeout = 30 * time.Second

// newRmCmd destroys a sandbox. The agent profile — mounted from config_dir —
// is NEVER touched: that is the whole point of a RW config_dir, and wiping it
// would force the user to /login again after every removal.
func newRmCmd(denHome *string, runner sbx.Runner, g worktree.Git) *cobra.Command {
	var keepWorktrees, force bool

	cmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "Destroy a sandbox (the agent profile persists)",
		Args:  exactlyOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}

			boxes, err := sbx.Ls(cmd.Context(), runner)
			if err != nil {
				return err
			}
			if sbx.Find(boxes, name) == nil {
				return fmt.Errorf("sandbox %q not found (live: %v)", name, liveNames(boxes))
			}

			out := cmd.OutOrStdout()

			// Worktrees first: if one is dirty we stop BEFORE destroying the
			// sandbox. The reverse would leave the user with no VM and an error
			// about a directory.
			if !keepWorktrees {
				if err := cleanWorktrees(cmd.Context(), home, name, g, force, out, cmd.ErrOrStderr()); err != nil {
					return err
				}
			}

			if _, err := runner.Run(cmd.Context(), "rm", "--force", name); err != nil {
				return err
			}
			fmt.Fprintf(out, "sandbox %s destroyed (the agent profile is kept)\n", name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&keepWorktrees, "keep-worktrees", false,
		"keep the worktrees den created")
	cmd.Flags().BoolVar(&force, "force", false,
		"remove worktrees even when they carry uncommitted changes")
	return cmd
}

// cleanWorktrees removes, through worktree.Remove, the worktrees den created
// for this sandbox — one per repo of the nest. Best-effort on RESOLUTION (a
// nest deleted from ~/.den/nests must not prevent destroying a live sandbox);
// strict on REMOVAL (a dirty worktree stops everything — see worktree.Remove).
//
// out carries the success messages, warnW the best-effort warnings: a script
// reading only stdout must not see a successful `den rm` hiding a warning.
//
// worktree.Remove never deletes a worktree, it moves it to den_home's trash and
// returns the path of the entry it created. That path is the ONLY trace the
// user gets of where their work went, so it is announced for every worktree
// actually moved.
func cleanWorktrees(ctx context.Context, home, sandboxName string, g worktree.Git, force bool, out, warnW io.Writer) error {
	nestName, wt := sbx.SplitName(sandboxName)
	if wt == "" {
		return nil // no worktree: nothing to clean up
	}

	// sbx.Ls validates NOTHING of what it reads: a sandbox listed by `sbx ls`
	// may have been created outside den, with a name sbx accepts but den would
	// refuse ("api../../evade"). Without this check that name travels as-is to
	// worktree.Path and sends Remove outside worktree_root.
	if err := sbx.ValidateSandboxName(sandboxName); err != nil {
		return fmt.Errorf("cleaning up worktrees: %w", err)
	}

	// A command validates what it USES: here exactly WorktreeLayout and
	// WorktreeRoot, both read below to build worktree.Remove's Target.
	//
	// The validating LoadGlobal would be wrong here: it would fail `den rm` on
	// a bad `agents.claude.update` — unrelated to worktrees — and leave the
	// user with a live VM they can no longer destroy (doctrine T13/T16). But
	// validating nothing would be just as wrong: LoadGlobalUnvalidated only
	// defaults the EMPTY layout, so a `centrl` survives and den would clean up
	// somewhere else SILENTLY. Hence the explicit check on those two fields.
	gl, err := config.LoadGlobalUnvalidated(home)
	if err != nil {
		return err
	}
	if errs := gl.ValidateWorktree(); len(errs) > 0 {
		return fmt.Errorf("cleaning up worktrees: %w", config.ConfigError(home, errs))
	}
	n, err := nest.LoadNest(home, nestName)
	if err != nil {
		// wt and gl.WorktreeRoot are both available here: without them the user
		// learns a worktree was abandoned, but not where to go find it.
		where := filepath.Join(gl.WorktreeRoot, wt)
		if gl.WorktreeLayout == "per-repo" {
			where = fmt.Sprintf("every repo of the nest, under <repo>/.den/%s", wt)
		}
		fmt.Fprintf(warnW, "nest %q unreadable: worktree %q could not be cleaned up "+
			"(expected under %s): %v\n", nestName, wt, where, err)
		return nil
	}

	for _, repo := range n.Repos {
		// One deadline PER repo, not one for the whole loop: a broken repo must
		// not eat the budget of the next repos of the same nest.
		repoCtx, cancel := context.WithTimeout(ctx, gitProbeTimeout)
		dest, err := worktree.Remove(repoCtx, g, worktree.Target{
			DenHome:  home,
			Layout:   gl.WorktreeLayout,
			Root:     gl.WorktreeRoot,
			Nest:     sandboxName,
			Worktree: wt,
			RepoPath: repo.Path,
			Force:    force,
		})
		cancel()
		if err != nil {
			return err
		}
		if dest == "" {
			continue // the directory was already gone: nothing to announce
		}
		fmt.Fprintf(out, "worktree moved to trash: %s\n", dest)
	}
	return nil
}
