package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/source"
	"github.com/PillowPillow/den/internal/worktree"
	"github.com/spf13/cobra"
)

// newSourceCmd manages team source repositories (spec 2026-08-04 §3). git is
// the injected worktree.Git — the SAME injection `den rm` already receives —
// so the whole tree tests against file:// remotes.
func newSourceCmd(denHome *string, git worktree.Git) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "source",
		Short: "Manage team source repositories (stacks/nests shared over git)",
	}

	var name string
	add := &cobra.Command{
		Use:   "add <url>",
		Short: "Clone a source repository under <den home>/sources/ and validate it",
		Args:  exactlyOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}
			resolved, err := source.Add(cmd.Context(), git, home, args[0], name)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"source %q installed — its objects are addressed %s:<name> (e.g. `den %s:<nest>`)\n",
				resolved, resolved, resolved)
			return nil
		},
	}
	add.Flags().StringVar(&name, "name", "", "install name (default: the URL's basename)")
	cmd.AddCommand(add)

	update := &cobra.Command{
		Use:   "update [name]",
		Short: "Fetch and fast-forward one source, or every installed source when no name is given",
		Args:  atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(args) == 1 {
				if err := source.Update(cmd.Context(), git, home, args[0]); err != nil {
					return err
				}
				fmt.Fprintf(out, "source %q updated\n", args[0])
				return nil
			}
			return updateAllSources(cmd.Context(), git, home, out)
		},
	}
	cmd.AddCommand(update)

	ls := &cobra.Command{
		Use:   "ls",
		Short: "List installed sources",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}
			infos, err := source.List(cmd.Context(), git, home)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(infos) == 0 {
				fmt.Fprintln(out, "(none)")
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tHEAD\tLAST FETCH\tURL")
			for _, info := range infos {
				head := info.Head
				if head == "" {
					head = "-"
				}
				last := "never"
				if !info.LastFetch.IsZero() {
					last = info.LastFetch.Format("2006-01-02 15:04")
				}
				line := fmt.Sprintf("%s\t%s\t%s\t%s", info.Name, head, last, info.URL)
				if len(info.LintErrs) > 0 {
					line += fmt.Sprintf(" — INVALID (run `den source update %s` after the repo is fixed)", info.Name)
				}
				fmt.Fprintln(w, line)
			}
			return w.Flush()
		},
	}
	cmd.AddCommand(ls)

	var force bool
	rm := &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove an installed source",
		Args:  exactlyOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}
			if err := source.Remove(cmd.Context(), git, home, args[0], force); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "source %q removed\n", args[0])
			return nil
		},
	}
	rm.Flags().BoolVar(&force, "force", false,
		"remove even with local changes, or commits not reachable from any remote-tracking ref")
	cmd.AddCommand(rm)

	return cmd
}

// updateAllSources drives a bare `den source update`: every installed
// source, in source.List's order. Failures accumulate rather than aborting
// the loop — one source stuck behind a VPN, or pointed at a remote that has
// moved, must not hide whether the others are current. Each failure is
// prefixed with its OWN source name here, deliberately: source.Update's bare
// `git fetch` error carries no name at all (mutate.go), so leaving that out
// would make a fetch failure in a multi-source update impossible to
// attribute.
func updateAllSources(ctx context.Context, git worktree.Git, home string, out io.Writer) error {
	infos, err := source.List(ctx, git, home)
	if err != nil {
		return err
	}
	if len(infos) == 0 {
		fmt.Fprintln(out, "no source installed")
		return nil
	}
	var failures []string
	for _, info := range infos {
		if err := source.Update(ctx, git, home, info.Name); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", info.Name, err))
			continue
		}
		fmt.Fprintf(out, "source %q updated\n", info.Name)
	}
	if len(failures) > 0 {
		return fmt.Errorf("%d source(s) failed to update:\n  - %s",
			len(failures), strings.Join(failures, "\n  - "))
	}
	return nil
}
