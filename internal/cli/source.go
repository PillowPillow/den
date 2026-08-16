package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/converge"
	"github.com/PillowPillow/den/internal/source"
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

	var updateFlags convergenceFlags
	update := &cobra.Command{
		Use:   "update [name]",
		Short: "Fetch and fast-forward one source, or every installed source when no name is given",
		Args:  atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}
			if len(args) == 1 {
				return updateSource(cmd, d, home, args[0], updateFlags)
			}
			return updateAllSources(cmd, d, home, updateFlags)
		},
	}
	updateFlags.bind(update, false, false)
	cmd.AddCommand(update)

	status := &cobra.Command{
		Use:   "status [name]",
		Short: "Report what a manifested source needs and what this machine already has",
		Args:  atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}
			return sourceStatus(cmd, d, home, args)
		},
	}
	cmd.AddCommand(status)

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

// updateSource dispatches `den source update <name>` on the same verdict
// `den source add` uses: whether the INSTALLED source carries a contract.
//
// A legacy source keeps the fast-forward it has always had. A manifested one
// gets the version policy of spec §11.2 — den updates to an exactly named
// version, after a plan and a confirmation, never because a branch moved.
func updateSource(cmd *cobra.Command, d Deps, home, name string, flags convergenceFlags) error {
	out := cmd.OutOrStdout()
	if !source.HasManifest(source.Dir(home, name)) {
		// Not installed lands here too, deliberately: source.Update owns that
		// refusal and its wording, and duplicating the check would give the user
		// two different messages for one situation.
		if err := source.Update(cmd.Context(), d.Git, home, name); err != nil {
			return err
		}
		fmt.Fprintf(out, "source %q updated\n", name)
		return nil
	}

	c, err := source.FetchCandidate(cmd.Context(), d.Git, home, name)
	if err != nil {
		return err
	}
	defer c.Close()

	if err := requireManifest(c, name, fmt.Sprintf(
		"the fetched update dropped it, so den cannot tell what to converge — the team must "+
			"restore %s, or `den source rm %s` and reinstall it as a legacy source",
		source.ManifestFile, name)); err != nil {
		return err
	}
	// The same judge as add and the legacy update: an invalid update leaves the
	// installed checkout exactly where it was.
	if errs := source.Lint(c.Root); len(errs) > 0 {
		return fmt.Errorf("%w\nthe local clone stays on its last valid state — nothing changed",
			source.LintRefusal(name, "the fetched update", errs))
	}

	configured := ""
	switch personal, loadErr := source.LoadPersonal(home, name); {
	case loadErr == nil:
		configured = personal.Version
	case errors.Is(loadErr, os.ErrNotExist):
		// Installed but never configured here — configured stays "", which
		// DecideUpdate reads as a first install (spec §11.2).
	default:
		// Any OTHER LoadPersonal error — a strict-decode rejection, a bad
		// schema_version, a permission fault — must NOT fall through to the
		// "never configured" case above: DecideUpdate's FIRST check is
		// `configured == ""`, and it returns UpdateConverge on that check
		// before the downgrade refusal (c < 0) further down is ever reached.
		// A machine configured for 2.0.0 whose personal file went corrupt
		// would then accept a team publish of 1.0.0 as a legitimate first
		// install — den converges forward only, and this file is the only
		// record of what "forward" means here. Refuse instead of guessing.
		return fmt.Errorf(
			"source %q: cannot read the personal configuration at %s (%w) — den will not decide "+
				"whether the fetched update is a downgrade with that file unreadable; fix or "+
				"restore it by hand, then retry",
			name, source.PersonalPath(home, name), loadErr)
	}
	head, err := d.Git.Run(cmd.Context(), source.Dir(home, name), "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	action, err := source.DecideUpdate(name, configured, c.Manifest.Metadata.Version,
		strings.TrimSpace(string(head)) == c.Commit)
	if err != nil {
		return err
	}
	switch action {
	case source.UpdateUnchanged:
		fmt.Fprintf(out, "source %q is unchanged: version %s, on the commit this machine converged\n",
			name, configured)
		return nil
	case source.UpdateDrift:
		// A warning, not a refusal: nothing is wrong with the machine. What den
		// refuses is to move it, because the contract did not change — see
		// source.DecideUpdate.
		fmt.Fprintf(out,
			"source %q: the team published new content on version %s without changing the version.\n"+
				"den converges an exact version, so nothing was applied and the checkout is "+
				"untouched — including any provision script the new commits changed. Ask the team "+
				"to publish a greater `metadata.version`.\n", name, configured)
		return nil
	}
	return runConvergence(cmd, d, converge.ModeUpdate, home, name, c, flags, nil)
}

// sourceStatus is `den source status [name]`: what each manifested source
// needs, and what this machine already has.
//
// Read-only, and the exit code is the verdict a script acts on: `blocked` and
// `unknown` are failures, `partially_ready` is not. A missing working
// repository is a normal state of a correctly installed source — den does not
// clone, so failing on it would make the command red on every machine that has
// not cloned everything yet (spec §12.1).
func sourceStatus(cmd *cobra.Command, d Deps, home string, args []string) error {
	names := args
	if len(names) == 0 {
		all, err := source.Names(home)
		if err != nil {
			return err
		}
		names = all
	}
	out := cmd.OutOrStdout()
	if len(names) == 0 {
		fmt.Fprintln(out, "no source installed")
		return nil
	}

	svc := converge.Service{Git: d.Git, Sbx: d.Sbx}
	var failures []string
	for i, name := range names {
		if i > 0 {
			fmt.Fprintln(out)
		}
		// A legacy source is NAMED rather than skipped: a user looking at a
		// list of their sources must see all of them, and "why is corp not
		// here" is a worse question than one line saying it has no contract.
		if !source.HasManifest(source.Dir(home, name)) {
			fmt.Fprintf(out, "source: %s  legacy — no %s, so den converges nothing for it\n",
				name, source.ManifestFile)
			continue
		}
		plan, err := svc.Status(cmd.Context(), home, name)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		converge.RenderStatus(out, plan)
		if !converge.Succeeds(plan.Status) {
			failures = append(failures, fmt.Sprintf("%s: %s", name, plan.Status))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%d source(s) den cannot use as configured:\n  - %s",
			len(failures), strings.Join(failures, "\n  - "))
	}
	return nil
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
//
// Each source is dispatched INDEPENDENTLY through updateSource, so a
// manifested one gets its own plan and its own confirmation in this loop: two
// sources are two contracts, and a single "yes" must never cover both.
func updateAllSources(cmd *cobra.Command, d Deps, home string, flags convergenceFlags) error {
	out := cmd.OutOrStdout()
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
		if err := updateSource(cmd, d, home, name, flags); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", name, err))
			continue
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%d source(s) failed to update:\n  - %s",
			len(failures), strings.Join(failures, "\n  - "))
	}
	return nil
}
