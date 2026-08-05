package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/doctor"
	"github.com/PillowPillow/den/internal/manifest"
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
				// The scan runs even here, and that is the whole point: "no
				// live sandbox, and four recorded worktrees still on disk" is
				// exactly the state worth reporting, and it is unreachable
				// from a return placed above it.
				reportOrphans(out, home, boxes)
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
			if err := w.Flush(); err != nil {
				return err
			}
			reportOrphans(out, home, boxes)
			return nil
		},
	}
}

// reportOrphans names the sandboxes den recorded creating that no longer have
// a VM, and the directories they left behind.
//
// FAIL-OPEN, strictly: `den ls` is the command a user types when everything
// else is broken, and it must never fail over its own extra. A record that
// cannot be read is skipped in silence here — `den doctor` is the command that
// names it, and it is the one with a remedy to offer.
//
// The comparison is free: this command already holds both the live list and
// den home.
func reportOrphans(out io.Writer, home string, boxes []sbx.Sandbox) {
	manifests, _, err := manifest.List(home)
	if err != nil {
		return
	}
	orphans := doctor.Orphans(doctor.LiveSandboxes{Known: true, Names: liveNames(boxes)}, manifests)
	if len(orphans) == 0 {
		return
	}
	fmt.Fprintln(out)
	for _, o := range orphans {
		if len(o.Worktrees) == 0 {
			// The record outlived its sandbox but den created nothing for it:
			// worth naming, since `den doctor --fix` will drop the record, but
			// there is no directory to point at.
			fmt.Fprintf(out, "orphan: %s — no live sandbox (nothing left on disk)\n", o.Sandbox)
			continue
		}
		fmt.Fprintf(out, "orphan: %s — no live sandbox, worktrees still on disk: %s\n",
			o.Sandbox, strings.Join(o.Worktrees, ", "))
	}
	fmt.Fprintln(out, "reclaim them with `den doctor --fix`")
}
