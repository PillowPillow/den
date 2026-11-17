package cli

import (
	"bytes"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/PillowPillow/den/v2/internal/config"
	"github.com/PillowPillow/den/v2/internal/registry"
	"github.com/PillowPillow/den/v2/internal/sbx"
)

func newLsCmd(deps Deps, denHome *string) *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List the instances den created, with the state of their VM",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}
			records, broken, err := registry.List(home)
			if err != nil {
				return err
			}
			for _, b := range broken {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: unreadable record %s: %v\n", b.Path, b.Err)
			}
			boxes, err := sbx.Ls(cmd.Context(), deps.Sbx)
			if err != nil {
				return err
			}
			var buf bytes.Buffer
			tw := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "INSTANCE\tNEST\tBRANCH\tSTATUS\tPROJECT")
			for _, r := range records {
				status := "missing"
				if box := sbx.Find(boxes, r.Instance); box != nil {
					status = box.Status
				}
				branch := ""
				if r.Worktree != nil {
					branch = r.Worktree.Branch
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.Instance, r.Nest, branch, status, r.Project)
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			// Empty trailing cells (a global nest has no project) would leave padding at line ends.
			for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
				fmt.Fprintln(cmd.OutOrStdout(), strings.TrimRight(line, " "))
			}
			return nil
		},
	}
}
