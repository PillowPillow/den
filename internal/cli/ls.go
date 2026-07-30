package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/spf13/cobra"
)

// newLsCmd lists live sandboxes. Without labels on the sbx side, `den ls` is
// `sbx ls --json` with each name split into (nest, worktree) — see
// sbx.Sandbox.Nest and sbx.Sandbox.Worktree.
func newLsCmd(denHome *string, runner sbx.Runner) *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List live sandboxes",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}

			boxes, err := sbx.Ls(cmd.Context(), runner)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(boxes) == 0 {
				fmt.Fprintln(out, "no live sandbox")
				return nil
			}

			// Declared nests only MARK unknown sandboxes, they never filter
			// them: a live VM stays visible even if its nest was deleted from
			// ~/.den/nests. Broken nests and an unreadable nests/ root are
			// reported by name on stderr and never turn into an error here.
			nests, broken, err := nest.ListNests(home)
			for _, bn := range broken {
				fmt.Fprintf(cmd.ErrOrStderr(), "nest %s unreadable: %v\n", bn.Name, bn.Err)
			}
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "listing nests: %v\n", err)
			}
			declared := map[string]bool{}
			for _, n := range nests {
				declared[n.Name] = true
			}

			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tNEST\tWORKTREE\tSTATUS\tWORKSPACES")
			for _, b := range boxes {
				nestName := b.Nest()
				if !declared[nestName] {
					nestName += " ?" // not declared in ~/.den/nests
				}
				wt := b.Worktree()
				if wt == "" {
					wt = "-"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\n", b.Name, nestName, wt, b.Status, len(b.Workspaces))
			}
			return w.Flush()
		},
	}
}
