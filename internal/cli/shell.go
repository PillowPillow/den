package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PillowPillow/den/v2/internal/config"
	"github.com/PillowPillow/den/v2/internal/registry"
	"github.com/PillowPillow/den/v2/internal/sbx"
	"github.com/PillowPillow/den/v2/internal/worktree"
)

func newShellCmd(deps Deps, denHome *string) *cobra.Command {
	var workdir string
	cmd := &cobra.Command{
		Use:   "shell [<instance>] [--workdir <vm path>]",
		Short: "Open a bash login shell in an instance",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}
			var rec registry.Record
			if len(args) == 1 {
				rec, err = registry.Read(home, args[0])
				if errors.Is(err, fs.ErrNotExist) {
					return fmt.Errorf("no V2 instance %q — run `den ls` to see the instances den knows", args[0])
				}
			} else {
				cwd, werr := deps.Getwd()
				if werr != nil {
					return fmt.Errorf("reading the current directory: %w", werr)
				}
				rec, err = pickShellInstance(cmd, deps, home, cwd)
			}
			if err != nil {
				return err
			}
			start := rec.Start
			if workdir != "" {
				start = workdir
			}
			// No --no-input: this is an interactive attach (ruling R3).
			return deps.Sbx.Attach(cmd.Context(), "exec", "-it", "-w", start, rec.Instance, "bash", "-l")
		},
	}
	cmd.Flags().StringVar(&workdir, "workdir", "", "folder the shell starts in, as a VM path")
	return cmd
}

func pickShellInstance(cmd *cobra.Command, deps Deps, home, cwd string) (registry.Record, error) {
	records, _, err := registry.List(home)
	if err != nil {
		return registry.Record{}, err
	}
	if len(records) == 0 {
		return registry.Record{}, errors.New("no instance to open a shell in — run `den up <nest>` first")
	}
	here := worktree.ResolvePath(cwd)
	var best []registry.Record
	bestLen := -1
	for _, r := range records {
		depth := containingDepth(r, here)
		if depth < 0 {
			continue
		}
		if depth > bestLen {
			best, bestLen = nil, depth
		}
		if depth == bestLen {
			best = append(best, r)
		}
	}
	if len(best) == 1 {
		return best[0], nil
	}
	if len(best) > 1 {
		records = best
	}
	boxes, err := sbx.Ls(cmd.Context(), deps.Sbx)
	if err != nil {
		return registry.Record{}, err
	}
	var running []registry.Record
	for _, r := range records {
		if box := sbx.Find(boxes, r.Instance); box != nil && !box.IsStopped() {
			running = append(running, r)
		}
	}
	if len(running) == 1 {
		return running[0], nil
	}
	names := make([]string, len(records))
	for i, r := range records {
		names[i] = r.Instance
	}
	return registry.Record{}, fmt.Errorf("cannot tell which instance you mean (%s) — run `den shell <instance>`", strings.Join(names, ", "))
}

// containingDepth is the length of the longest Mount or Repo of r that contains here, or -1.
func containingDepth(r registry.Record, here string) int {
	depth := -1
	for _, repo := range r.Repos {
		for _, p := range []string{repo.Mount, repo.Repo} {
			if p == "" || !filepath.IsAbs(p) {
				continue
			}
			p = worktree.ResolvePath(p)
			if within(p, here) && len(p) > depth {
				depth = len(p)
			}
		}
	}
	return depth
}

func within(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
