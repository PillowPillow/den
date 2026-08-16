package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/converge"
	"github.com/PillowPillow/den/internal/source"
	"github.com/PillowPillow/den/internal/worktree"
	"github.com/spf13/cobra"
)

// newSourceCmd manages team source repositories (spec 2026-08-04 §3). git is
// the injected worktree.Git — the SAME injection `den rm` already receives —
// so the whole tree tests against file:// remotes.
func newSourceCmd(denHome *string, d Deps) *cobra.Command {
	git := d.Git
	cmd := &cobra.Command{
		Use:   "source",
		Short: "Manage team source repositories (stacks/nests shared over git)",
	}

	var addFlags convergenceFlags
	add := &cobra.Command{
		Use:   "add <url>",
		Short: "Clone a source repository under <den home>/sources/ and validate it",
		Args:  exactlyOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}
			return addSource(cmd, d, home, args[0], addFlags)
		},
	}
	addFlags.bind(add, false, true)
	cmd.AddCommand(add)

	var configureFlags convergenceFlags
	configure := &cobra.Command{
		Use:   "configure <name>",
		Short: "Reconverge an installed source on this machine, without contacting its remote",
		Args:  exactlyOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}
			return configureSource(cmd, d, home, args[0], configureFlags)
		},
	}
	configureFlags.bind(configure, false, false)
	cmd.AddCommand(configure)

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

// addSource dispatches `den source add` on the ONE thing that separates the
// two installations: whether the repository carries a den-source.yaml.
//
// The probe is a clone into the den home's cache, before any mutation — a
// manifest can only be read from a checkout, and reading it must not be what
// installs the source. A legacy repository is then handed to source.Add, which
// clones again: the second clone is the price of leaving that path EXACTLY as
// it was, lint refusal and self-removal included, and it is paid only by
// sources that have no contract.
func addSource(cmd *cobra.Command, d Deps, home, url string, flags convergenceFlags) error {
	c, err := source.AcquireCandidate(cmd.Context(), d.Git, home, url)
	if err != nil {
		return err
	}
	defer c.Close()

	if c.Manifest == nil {
		// Dropped now rather than at the deferred Close: the legacy path below
		// clones into the same den home, and leaving a staging directory around
		// for the duration would show up in nothing but confusion.
		c.Close()
		resolved, err := source.Add(cmd.Context(), d.Git, home, url, flags.Name)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(),
			"source %q installed — its objects are addressed %s:<name> (e.g. `den spawn %s:<nest>`)\n",
			resolved, resolved, resolved)
		return nil
	}

	name, err := source.ResolveNamespace(cmd.Context(), d.Git, home, url, flags.Name, c.Manifest)
	if err != nil {
		return err
	}
	if errs := source.Lint(c.Root); len(errs) > 0 {
		return source.LintRefusal(name, url, errs)
	}
	// No fresh global configuration on this path: `den source add` adds a source
	// to a den home that already exists. Creating one is `den init`'s job, and
	// only it knows the user asked for a home.
	return runConvergence(cmd, d, converge.ModeAdd, home, name, c, flags, nil)
}

// configureSource is `den source configure <name>`: the same convergence, over
// the INSTALLED checkout (spec §11.1).
//
// It contacts no remote. That is what makes it the command for the two things
// that happen after an installation — a repository cloned since, and a run
// interrupted halfway — without a fetch changing what is being converged under
// the user's feet.
//
// No usability gate on the receipt here, deliberately: source.RequireUsable
// refuses while an `applying` receipt is in place, and this is the command that
// clears it. Gating it would leave a partial application unresumable.
func configureSource(cmd *cobra.Command, d Deps, home, name string, flags convergenceFlags) error {
	c, err := source.InstalledCandidate(cmd.Context(), d.Git, home, name)
	if err != nil {
		return err
	}
	defer c.Close()

	if err := requireManifest(c, name, fmt.Sprintf(
		"it is a legacy source, which declares nothing to converge; `den source update %s` fetches "+
			"it and `den spawn %s:<nest>` uses it", name, name)); err != nil {
		return err
	}
	return runConvergence(cmd, d, converge.ModeConfigure, home, name, c, flags, nil)
}

// updateAllSources drives a bare `den source update`: every installed
// source, in source.Names's order (sorted — os.ReadDir's own order).
// source.Names, not source.List, on purpose: List lints every source and
// runs two git commands per source to build the report `den source ls`
// shows, and none of that feeds this loop — it uses nothing but the name,
// and source.Update lints the fetched tree itself. Failures accumulate
// rather than aborting the loop — one source stuck behind a VPN, or pointed
// at a remote that has moved, must not hide whether the others are current.
// Each failure is prefixed with its OWN source name here, deliberately:
// source.Update's bare `git fetch` error carries no name at all
// (mutate.go), so leaving that out would make a fetch failure in a
// multi-source update impossible to attribute.
func updateAllSources(ctx context.Context, git worktree.Git, home string, out io.Writer) error {
	names, err := source.Names(home)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		fmt.Fprintln(out, "no source installed")
		return nil
	}
	var failures []string
	for _, name := range names {
		if err := source.Update(ctx, git, home, name); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		fmt.Fprintf(out, "source %q updated\n", name)
	}
	if len(failures) > 0 {
		return fmt.Errorf("%d source(s) failed to update:\n  - %s",
			len(failures), strings.Join(failures, "\n  - "))
	}
	return nil
}
