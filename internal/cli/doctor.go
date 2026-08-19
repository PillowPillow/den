package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/converge"
	"github.com/PillowPillow/den/internal/doctor"
	"github.com/PillowPillow/den/internal/manifest"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/source"
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
			// The policy checks are held rather than appended blind: whether
			// THIS report states why the machine could not be observed is a
			// fact only the composed report holds, and it decides how the
			// source lines below explain themselves (see sourceDetail).
			policy := networkPolicyChecks(cmd.Context(), deps, runner)
			checks = append(checks, policy...)
			checks = append(checks,
				sourceChecks(cmd.Context(), home, runner, g, causeStated(deps, policy))...)

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

// networkPolicyChecks answers one question: does this machine's sbx policy
// answer at all?
//
// It is a FAIL, not a warning: sbx requires a one-time
// `sbx policy init <allow-all|balanced|deny-all>` before any policy command
// works, and until it is run den cannot converge a source (the shared state
// read fails) nor spawn anything (the settle loop's `policy check` fails). A
// machine in that state is not degraded, it is unusable — and it used to pass
// `den doctor` with "all good" (reported 2026-08-18).
//
// Run HERE and not in internal/doctor, like the live list and the source
// checks above: that package promises "no side effects, no network" and runs
// no sbx. cli owns the runner; doctor owns the verdict shape.
//
// Skipped entirely when sbx is not on the PATH: the `sbx` check already failed
// on that, and a second FAIL saying the same thing in other words only makes
// the report harder to act on. Same rule as the live list, which degrades to
// "unknown" rather than accusing every sandbox.
//
// den does NOT run `sbx policy init` itself, here or anywhere: allow-all,
// balanced and deny-all are a machine-wide security posture, and choosing one
// for the user is exactly the silent normalization spec §2 forbids.
func networkPolicyChecks(ctx context.Context, deps doctor.Deps, runner sbx.Runner) []doctor.Check {
	if _, err := deps.LookPath("sbx"); err != nil {
		return nil
	}
	if _, err := sbx.LocalNetworkPolicy(ctx, runner); err != nil {
		// Flattened onto one line: sbx's refusal is a four-line paragraph
		// (message, blank line, "Initialize it with:", the command), and the
		// report is a column of one-line checks. strings.Fields collapses every
		// run of whitespace, so the remedy survives — it is the only part the
		// user acts on — while the layout of the column does too.
		return []doctor.Check{{Name: "sbx policy", Level: doctor.LevelFail,
			Detail: strings.Join(strings.Fields(err.Error()), " ")}}
	}
	return []doctor.Check{{Name: "sbx policy", Level: doctor.LevelOK,
		Detail: "local network policy readable"}}
}

// causeStated reports whether this report already says WHY den could not
// observe the machine. It is what lets the source lines point at that
// explanation instead of each repeating it (sourceDetail).
//
// Two carriers, and only two. The `sbx` check states a missing binary — the
// whole cause, in that case — and the `sbx policy` FAIL carries sbx's own
// refusal when the policy is what did not answer. Everything else that can
// fail the shared read is stated NOWHERE else in the report: ReadSbxState also
// gives up when `secret ls -g` fails, and when either output parses into
// nothing den recognizes — a header changed by a newer sbx is the expected
// shape of that, not a broken machine. On those exits the policy check is a
// plain [ok], so the answer here is false and each source line keeps printing
// the cause itself.
//
// Duplicated on those exits, then, rather than deduplicated into silence:
// doctor exits 1 and a user must be able to read why. Repetition costs
// attention; a report that fails while naming no cause costs the diagnosis.
func causeStated(deps doctor.Deps, policy []doctor.Check) bool {
	if _, err := deps.LookPath("sbx"); err != nil {
		return true
	}
	for _, c := range policy {
		if c.Blocking() {
			return true
		}
	}
	return false
}

// sourceChecks diagnoses every manifested source installed in this home.
//
// Read HERE, not in internal/doctor, for the reason the live list above is:
// observing a source means running sbx, and that package runs none. cli owns
// the runner; doctor owns the verdict (doctor.SourceCheck).
//
// One check per source, in sorted order, and a legacy source produces none —
// it declares nothing to converge, so there is nothing doctor could judge.
//
// causeStated comes from the report being composed above, never from anything
// this function can see: it says whether an earlier check already explains an
// unobservable machine. Passed down to sourceDetail, which is where it decides
// how a source line explains itself.
func sourceChecks(ctx context.Context, home string, runner sbx.Runner, g worktree.Git,
	causeStated bool) []doctor.Check {
	names, err := source.Names(home)
	if err != nil {
		// A home with no sources/ directory is the normal case, not a fault.
		return nil
	}
	// The manifested sources are selected BEFORE the observation below, so a
	// home holding only legacy ones still runs no sbx at all — it declares
	// nothing to judge, and reading the machine to judge nothing would be a
	// subprocess this function used to owe nobody.
	manifested := make([]string, 0, len(names))
	for _, name := range names {
		if source.HasManifest(source.Dir(home, name)) {
			manifested = append(manifested, name)
		}
	}
	if len(manifested) == 0 {
		return nil
	}
	svc := converge.Service{Git: g, Sbx: runner}
	// ONE observation for every source: Status re-reading the machine per
	// source ran the same two sbx subprocesses once per source, for a verdict
	// that cannot change between two of them — it is machine state, not source
	// state (review PR82).
	//
	// A FAILED observation lands in every source's plan, so it is stated once
	// rather than once per source WHEN an earlier check already carries it —
	// see causeStated above and sourceDetail below.
	state, observeErr := converge.ReadSbxState(ctx, runner)
	var checks []doctor.Check
	for _, name := range manifested {
		plan, err := svc.StatusWith(ctx, home, name, state, observeErr)
		if err != nil {
			checks = append(checks, doctor.Check{Name: "source " + name, Level: doctor.LevelFail,
				Detail: err.Error()})
			continue
		}
		checks = append(checks, doctor.SourceCheck(name, plan.Status, sourceDetail(plan, causeStated)))
	}
	return checks
}

// sourceDetail is the one line doctor prints for a source: its version, its
// verdict, and the first thing to do about it. The full report — every
// resource, every nest — is `den source status <name>`, which the detail names
// when there is something to read there.
//
// An UNOBSERVABLE machine is the one case where the plan's own first warning
// is not always what doctor prints. Every source's plan carries the same cause
// — they all come from the one read above — so printing it per source repeated
// sbx's four-line refusal down the report and buried the verdicts it was meant
// to explain (review PR82).
//
// It is dropped for a pointer ONLY when causeStated says an earlier check
// carries that cause; the sole judge of that is causeStated, which the composed
// report answers and this function cannot. Otherwise the line keeps printing
// the cause, repetition and all — see there for why silence would be worse.
// That is also why the pointer names no particular carrier line: which check
// holds the cause depends on how far den got.
//
// Only the unobserved cause is ever dropped. A source that is ALSO blocked
// keeps its blocking refusal printed here, because that one IS about the
// source: RequireUsable's message is what Status prepends to Warnings, and a
// blocked source must say what a spawn would refuse (see Status). The two are
// told apart by the status — with a failed observation every resource is
// unknown, so AggregateStatus can only answer `unknown`, and `blocked` on top
// of an unobserved plan can only have come from that prepend.
//
// This lives in doctor and nowhere else — not because `den source status` is
// exempt. Its bare form loops over every source too and does print the cause
// once per source. It is that deduplicating needs the whole report to be
// composed first, to know whether the cause survives being dropped;
// converge.RenderStatus renders one plan at a time and cannot answer that.
func sourceDetail(p *converge.Plan, causeStated bool) string {
	detail := fmt.Sprintf("version %s: %s", p.Version, p.Status)
	if causeStated && p.Unobserved != nil && p.Status != source.StatusBlocked {
		return detail + " — den could not observe this machine: the sbx check above carries the cause"
	}
	if len(p.Warnings) > 0 {
		return detail + " — " + p.Warnings[0]
	}
	if p.Status != source.StatusReady {
		return detail + fmt.Sprintf(" — `den source status %s` says what is missing", p.Source)
	}
	return detail
}
