package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PillowPillow/den/v2/internal/config"
	"github.com/PillowPillow/den/v2/internal/nest"
	"github.com/PillowPillow/den/v2/internal/prompt"
	"github.com/PillowPillow/den/v2/internal/repos"
	"github.com/PillowPillow/den/v2/internal/worktree"
)

// scanMaxDepth is inclusive, the root being depth 0.
const scanMaxDepth = 20

func newScanCmd(deps Deps, denHome *string) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "scan [folder] [--yes]",
		Short: "Find the git clones under a folder and record where each origin is cloned",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}
			cwd, err := deps.Getwd()
			if err != nil {
				return fmt.Errorf("reading the current directory: %w", err)
			}
			root := cwd
			if len(args) == 1 {
				// Not filepath.Abs: a relative folder is relative to the injected cwd, not the process's.
				if root, err = config.ExpandPath(args[0]); err != nil {
					return err
				}
				if !filepath.IsAbs(root) {
					root = filepath.Join(cwd, root)
				}
			}
			root = filepath.Clean(root)
			// The walker never applies Skip to its root, and the den home holds template copies and
			// run folders that are den's, not the user's clones: recording them would map an origin
			// to a folder `den rm` or `den build` deletes.
			if within(worktree.ResolvePath(home), worktree.ResolvePath(root)) {
				return fmt.Errorf("%s is inside den's home %s, which holds den's own files, not your clones; "+
					"scan the folder that holds your clones instead (e.g. `den scan ~/dev`)", root, home)
			}

			table, err := repos.ReadTable(home)
			if err != nil {
				return err
			}
			declared, err := nest.DeclaredOrigins(home, cwd)
			if err != nil {
				return err
			}
			global, err := config.LoadGlobal(home)
			if err != nil {
				return err
			}

			errw := cmd.ErrOrStderr()
			if c, ok := repos.EnclosingClone(root); ok {
				fmt.Fprintf(errw, "%s is inside the clone %s; to find its siblings, run `den scan %s`\n",
					root, c, filepath.Dir(c))
			} else if repos.IsClone(root) {
				// The walk stops at a clone, so a scan from a clone's root records that one alone.
				fmt.Fprintf(errw, "%s is a clone; to find its siblings, run `den scan %s`\n", root, filepath.Dir(root))
			}
			skip := []string{home}
			if userHome, err := os.UserHomeDir(); err == nil {
				skip = append(skip, filepath.Join(userHome, "Library"))
			}
			clones, err := repos.Walk(root, repos.WalkOptions{
				MaxDepth: scanMaxDepth,
				Skip:     skipPaths(root, skip),
				Progress: func(dirs, clones int) {
					fmt.Fprintf(errw, "scanned %s, %s\n", plural(dirs, "folder", "folders"), plural(clones, "clone", "clones"))
				},
			})
			if err != nil {
				return fmt.Errorf("scanning %s: %w", root, err)
			}

			plan := repos.BuildPlan(clones, table, repos.Valid)
			out := cmd.OutOrStdout()
			printScanPlan(out, plan, table, declared)
			if err := applyScanPlan(cmd, deps, home, &table, plan, yes); err != nil {
				return err
			}
			printStillMissing(out, declared, table, global)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "write the additions without asking; a replacement still asks on a terminal")
	return cmd
}

// applyScanPlan decides what to write before writing once, so a refused or impossible
// replacement never leaves the table rewritten. It updates *table to what is on disk.
func applyScanPlan(cmd *cobra.Command, deps Deps, home string, table *repos.Table, plan repos.Plan, yes bool) error {
	out := cmd.OutOrStdout()
	adds, replaces := len(plan.Adds), len(plan.Replaces)
	if adds+replaces == 0 {
		fmt.Fprintln(out, "nothing to write")
		return nil
	}
	tty := deps.IsTTY()
	ask := func(q string) (bool, error) {
		return deps.Prompt.Confirm(cmd.Context(), prompt.ConfirmRequest{Question: q})
	}

	var write []repos.Change
	switch {
	case !tty && !yes && adds == 0:
		return fmt.Errorf("den scan found %s; run it in a terminal: --yes never writes a replacement",
			plural(replaces, "replacement", "replacements"))
	case !tty && !yes:
		return fmt.Errorf("den scan found %s; run it in a terminal, or with --yes to write the additions",
			plural(adds+replaces, "change", "changes"))
	case !yes:
		ok, err := ask(fmt.Sprintf("write %s and %s to %s?",
			plural(adds, "addition", "additions"), plural(replaces, "replacement", "replacements"), repos.TablePath(home)))
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(out, "nothing written")
			return nil
		}
		write = append(append(write, plan.Adds...), plan.Replaces...)
	default:
		write = append(write, plan.Adds...)
		// --yes covers additions only: a replacement moves the folder an existing nest mounts, and
		// the remote that triggers it lives in a .git/config a VM can rewrite.
		switch {
		case replaces == 0:
		case !tty:
			for _, c := range plan.Replaces {
				fmt.Fprintf(out, "not written (needs a terminal): %s\n", replaceLine(c))
			}
		default:
			ok, err := ask(fmt.Sprintf("replace %s?", plural(replaces, "entry", "entries")))
			if err != nil {
				return err
			}
			if ok {
				write = append(write, plan.Replaces...)
			}
		}
	}
	if len(write) == 0 {
		fmt.Fprintln(out, "nothing written")
		return nil
	}
	next := repos.Table{Repos: map[string]repos.Entry{}}
	for id, e := range table.Repos {
		next.Repos[id] = e
	}
	next.Apply(write...)
	if err := repos.WriteTable(home, next); err != nil {
		return err
	}
	*table = next
	fmt.Fprintf(out, "wrote %s to %s\n", plural(len(write), "entry", "entries"), repos.TablePath(home))
	return nil
}

// printScanPlan prints identities and paths only: a remote URL can carry a token, and the plan is
// built from .git/config files a VM may have written.
func printScanPlan(w io.Writer, plan repos.Plan, table repos.Table, declared []nest.Declared) {
	for _, c := range plan.Replaces {
		fmt.Fprintln(w, replaceLine(c))
	}
	for _, d := range plan.Duplicates {
		fmt.Fprintf(w, "duplicate %s: using %s, not %s\n", d.Identity, d.Chosen, strings.Join(d.Others, ", "))
	}
	isDeclared := map[string]bool{}
	for _, d := range declared {
		isDeclared[d.Identity] = true
	}
	others := 0
	for _, c := range plan.Adds {
		if isDeclared[c.Identity] {
			fmt.Fprintf(w, "add %s → %s\n", c.Identity, c.Path)
		} else {
			others++
		}
	}
	// A count, not a list: forty lines of clones no key names get confirmed without being read.
	if others > 0 {
		fmt.Fprintf(w, "and %s\n", plural(others, "other clone", "other clones"))
	}
	for _, id := range plan.Stale {
		fmt.Fprintf(w, "stale %s: %s no longer holds it\n", id, table.Repos[id].Path)
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func replaceLine(c repos.Change) string {
	return fmt.Sprintf("replace %s: %s → %s", c.Identity, c.Old, c.Path)
}

// printStillMissing resolves a key the way den up does: a global path wins, else the table.
// The origin printed is the one the user wrote in den.yaml, never one read from a clone.
func printStillMissing(w io.Writer, declared []nest.Declared, table repos.Table, global *config.Loaded) {
	for _, d := range declared {
		if global.File.Repos[d.Key].Path != "" || table.Repos[d.Identity].Path != "" {
			continue
		}
		fmt.Fprintf(w, "still missing: %s (%s): git clone %s\n", d.Key, d.File, d.Origin)
	}
}

// skipPaths adds, for each skipped folder below root, its path as the walker spells it from root:
// the walker compares lexically, and /var against /private/var on macOS would otherwise miss.
func skipPaths(root string, skip []string) []string {
	rootResolved := worktree.ResolvePath(root)
	var out []string
	for _, s := range skip {
		out = append(out, filepath.Clean(s))
		if r := worktree.ResolvePath(s); within(rootResolved, r) {
			rel, _ := filepath.Rel(rootResolved, r)
			out = append(out, filepath.Join(root, rel))
		}
	}
	return out
}
