package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/doctor"
	"github.com/PillowPillow/den/internal/manifest"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/worktree"
	"github.com/spf13/cobra"
)

// newDoctorCmd takes its system accesses as a parameter rather than hard-wiring
// doctor.SystemDeps(): that is what lets a test exercise both branches of the
// exit contract without depending on the machine it runs on. The runner and the
// Git are here for the same reason, and for one more: they are the two accesses
// internal/doctor must never gain — see the live-list comment below.
func newDoctorCmd(denHome *string, deps doctor.Deps, runner sbx.Runner, g worktree.Git) *cobra.Command {
	var fix, force bool

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose den's configuration and environment",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// --force only has meaning as a modifier of --fix (it relaxes
			// one of --fix's own refusals, below). Read on its own it does
			// nothing: without this refusal `den doctor --force` would print
			// a plain report with no sign the flag it was given had any
			// effect — den refuses a flag it would otherwise silently
			// ignore. Checked before home even resolves: a flag-consistency
			// mistake is not a config problem, and must not be masked by one.
			if force && !fix {
				return fmt.Errorf(
					"--force has no effect without --fix — run `den doctor --fix --force`")
			}

			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "den home: %s\n\n", home)

			checks := doctor.Run(home, deps)

			// The live list is read HERE, not in internal/doctor: that package
			// promises "no side effects, no network" in its very first line and
			// never runs sbx. cli already owns deps.Sbx — and already carries
			// the mutation for `den rm` — so the boundary stays exactly where
			// it was.
			//
			// An error is NOT a failure: it means the answer is unknown, which
			// LiveSandboxes models explicitly so that a missing sbx skips the
			// check instead of reporting every healthy sandbox as an orphan.
			live := doctor.LiveSandboxes{}
			if boxes, err := sbx.Ls(cmd.Context(), runner); err == nil {
				live.Known = true
				live.Names = liveNames(boxes)
			}
			manifests, broken, err := manifest.List(home)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "reading creation records: %v\n", err)
			}
			for _, b := range broken {
				// The remedy travels with the report, because den itself will
				// not act on that file: it could not read it, so it cannot know
				// it is worthless — a record written by a NEWER den lands here
				// too, and deleting it would destroy that den's only trace of a
				// live sandbox.
				fmt.Fprintf(cmd.ErrOrStderr(), "creation record %s unreadable: %v — den leaves "+
					"it alone (it may belong to another version of den); delete it by hand once "+
					"its sandbox is gone\n", b.Path, b.Err)
			}
			checks = append(checks, doctor.OrphanCheck(live, manifests))

			failures, warnings := 0, 0
			for _, c := range checks {
				mark := "ok  "
				// Blocking() rather than a comparison copied here: doctor owns
				// the decision of what weighs on the exit code.
				switch {
				case c.Blocking():
					mark = "FAIL"
					failures++
				case c.Level == doctor.LevelWarning:
					mark = "warn"
					warnings++
				}
				fmt.Fprintf(out, "[%s] %-16s %s\n", mark, c.Name, c.Detail)
			}

			// --fix runs AFTER the report: the user sees what den is about to
			// touch on the same screen, and a diagnostic that mutates before
			// printing would be unreadable when it fails halfway.
			if fix {
				if err := reclaimOrphans(cmd.Context(), home, doctor.Orphans(live, manifests),
					manifests, g, force, out); err != nil {
					return err
				}
			}

			// The ORDER of these two blocks carries the exit contract and is not
			// interchangeable: both end in a return, so whichever comes first
			// decides. Swapped, a failing check accompanied by a mere warning
			// would return nil — `den doctor` at 0 on a broken install, under
			// self-contradicting output.
			if failures > 0 {
				return fmt.Errorf("%d failing check(s)", failures)
			}
			// A warning does NOT change the exit code — that is its whole point
			// — but "all good" under a [warn] line would read as a
			// contradiction.
			if warnings > 0 {
				fmt.Fprintf(out, "\nno failure, but %d warning(s): review the [warn] lines\n",
					warnings)
				return nil
			}
			fmt.Fprintln(out, "\nall good")
			return nil
		},
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "reclaim the worktrees of sandboxes that no longer exist")
	cmd.Flags().BoolVar(&force, "force", false,
		"with --fix, reclaim them even when they carry uncommitted changes")
	return cmd
}

// reclaimOrphans sends each orphan's worktrees to the trash, through the same
// body `den rm` uses (cleanFromManifest): one definition of what den is
// allowed to move, so the two commands can never diverge on it.
//
// The first refusal stops the loop and is returned. That is deliberate: a
// dirty worktree is the user's uncommitted work, and continuing past it would
// bury the one message they need under the reclaim lines of the next sandbox.
func reclaimOrphans(ctx context.Context, home string, orphans []doctor.Orphan,
	manifests []manifest.Manifest, g worktree.Git, force bool, out io.Writer) error {

	byName := make(map[string]manifest.Manifest, len(manifests))
	for _, m := range manifests {
		byName[m.Sandbox] = m
	}
	for _, o := range orphans {
		fmt.Fprintf(out, "\nreclaiming %s...\n", o.Sandbox)
		if err := cleanFromManifest(ctx, home, byName[o.Sandbox], g, force, out); err != nil {
			return err
		}
	}
	return nil
}
