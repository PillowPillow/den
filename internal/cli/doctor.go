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
			// Appended as they come, both of them: no check here decides how
			// another one words itself. den used to hold the policy checks and
			// hand the source lines a flag saying "some check already states
			// why the machine is blind" — a test of EXISTENCE, not of
			// identity, which printed no cause at all when the two sbx reads
			// failed for different reasons (review PR82, I1). The source lines
			// now deduplicate among themselves; see sourceChecks.
			checks = append(checks, networkPolicyChecks(cmd.Context(), deps, runner)...)
			// ONE observation of the machine for every check that needs one.
			// It used to be read inside sourceChecks, which is where it was
			// needed and nowhere else; the undeclared-state report needs the
			// same inventory, and a second read would run the same subprocess
			// for a verdict that cannot differ — machine state, not source
			// state, the very argument PR82 settled one level down.
			//
			// Read UNCONDITIONALLY, sbx missing from the PATH included: the
			// failure is exactly what both callers need. sourceChecks turns it
			// into an unobserved plan (never an empty one, which would report a
			// configured machine as broken), and undeclaredChecks into a
			// "skipped" line naming the cause.
			state, observeErr := converge.ReadSbxState(cmd.Context(), runner)
			checks = append(checks, sourceChecks(cmd.Context(), home, runner, g,
				state, observeErr)...)
			checks = append(checks, undeclaredChecks(cmd.Context(), home, deps, runner,
				state, observeErr)...)

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

// sourceChecks diagnoses every manifested source installed in this home.
//
// Read HERE, not in internal/doctor, for the reason the live list above is:
// observing a source means running sbx, and that package runs none. cli owns
// the runner; doctor owns the verdict (doctor.SourceCheck).
//
// One check per source, in sorted order, and a legacy source produces none —
// it declares nothing to converge, so there is nothing doctor could judge.
//
// The cause of an UNOBSERVABLE machine is stated on the FIRST source line that
// can state it, and every following one points at that line by name. It is a
// fact about the machine, identical in every source's plan — printing it per
// source repeated sbx's four-line refusal down the report and buried the
// verdicts it was meant to explain (review PR82). Deduplicating among the
// source lines THEMSELVES is what makes the count one whatever else the report
// contains; see sourceDetail.
//
// "The first line that CAN state it" is the exact claim, and the qualifier is
// load-bearing: a blocked source prints its own refusal instead (see
// sourceDetail), so a home whose only unobservable source is also blocked
// states the machine cause on no source line at all — as it did before this
// dedup existed. The cause is still `den source status <name>`, and the `sbx
// policy` line when the policy read is what failed.
// state and observeErr are the caller's single observation of the machine (see
// the read in RunE). They are PARAMETERS rather than a read of its own for the
// reason this function already gives below about re-reading per source: the
// verdict is machine state, and two reads of it can disagree.
func sourceChecks(ctx context.Context, home string, runner sbx.Runner,
	g worktree.Git, state *converge.SbxState, observeErr error) []doctor.Check {
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
	// A FAILED observation lands in every source's plan, so it is stated on the
	// first line that can state it and pointed at from the rest — see
	// sourceDetail below.
	var checks []doctor.Check
	// statedBy is the source whose line already printed that cause, empty
	// while none has. The sorted iteration is what makes the pointer honest:
	// source.Names returns os.ReadDir's entries, which are sorted by name, so
	// the source it names has already been printed ABOVE the line naming it.
	statedBy := ""
	for _, name := range manifested {
		plan, err := svc.StatusWith(ctx, home, name, state, observeErr)
		if err != nil {
			checks = append(checks, doctor.Check{Name: "source " + name, Level: doctor.LevelFail,
				Detail: err.Error()})
			continue
		}
		detail, stated := sourceDetail(plan, statedBy)
		if stated {
			statedBy = name
		}
		checks = append(checks, doctor.SourceCheck(name, plan.Status, detail))
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
// — they all come from the one read in sourceChecks — so printing it per source
// repeated sbx's four-line refusal down the report and buried the verdicts it
// was meant to explain (review PR82).
//
// statedBy is the source whose line printed that cause above; empty means no
// line has, and then THIS one prints it — unless it is blocked, in which case
// it owes its own refusal instead and states nothing about the machine. So a
// cause is stated once by the lines that can state it, never twice, and the
// question of whether some OTHER kind of check happens to carry it never
// arises. den used to ask exactly that question, and answered
// it by existence rather than by identity: when `secret ls -g` failed on one
// cause and doctor's own `policy ls` on another, every source line pointed at
// a check stating the second while the first was printed nowhere at all
// (review PR82, I1).
//
// The returned bool says this line PRINTED the cause — never merely that the
// plan is unobserved. The caller advances statedBy on it, so a true from a
// line carrying no cause would delete that cause from the whole report, which
// is the fault this shape removes.
//
// Only the unobserved cause is ever deduplicated. A source that is ALSO blocked
// keeps its blocking refusal printed here, because that one IS about the
// source: RequireUsable's message is what Status prepends to Warnings, and a
// blocked source must say what a spawn would refuse (see Status). The two are
// told apart by the status — with a failed observation every resource is
// unknown, so AggregateStatus can only answer `unknown`, and `blocked` on top
// of an unobserved plan can only have come from that prepend.
//
// This lives in doctor and nowhere else — not because `den source status` is
// exempt. Its bare form loops over every source too and does print the cause
// once per source. It is that the loop is what holds statedBy;
// converge.RenderStatus renders one plan at a time and cannot know whether
// another line already carried the cause.
func sourceDetail(p *converge.Plan, statedBy string) (string, bool) {
	detail := fmt.Sprintf("version %s: %s", p.Version, p.Status)
	unobserved := p.Unobserved != nil && p.Status != source.StatusBlocked
	if unobserved && statedBy != "" {
		return detail + fmt.Sprintf(
			" — den could not observe this machine: same cause as `source %s` above", statedBy), false
	}
	// Warnings[0] is the unobserved cause on an unobserved plan — the ONE
	// wording, appended by Status (converge.unobservedWarning) and prepended
	// past only by a blocking refusal, which the status above excluded. The
	// bool is returned from HERE, where the cause is actually concatenated.
	if len(p.Warnings) > 0 {
		return detail + " — " + p.Warnings[0], unobserved
	}
	if p.Status != source.StatusReady {
		return detail + fmt.Sprintf(" — `den source status %s` says what is missing", p.Source), false
	}
	return detail, false
}

// undeclaredChecks reports the machine state sbx writes and no den source
// declares — the governance gap issue #88 exists to close.
//
// den's model is that `~/.den` is the single source of truth. sbx v0.39.0
// ships three other authors writing to the same machine (`sbx setup` /
// `sbx secret set`, `sbx skills import`, `sbx mcp add`), and den said nothing
// about any of them. Silence is the one answer spec §2 forbids: den refuses
// rather than normalizing without saying so, and here it did neither — it
// simply did not look.
//
// Nothing is ever removed, here or behind `--fix`. den never deletes what it
// did not create; naming it is the entire remedy, and the user decides.
//
// Read HERE and not in internal/doctor, like the live list, the policy check
// and the source lines above: that package promises "no side effects, no
// network" in its first line. cli owns the runner; doctor owns the verdict
// (doctor.UndeclaredCheck).
//
// Skipped entirely when sbx is not on the PATH, the same rule
// networkPolicyChecks follows: the `sbx` check already failed on that, and
// three more lines restating it in other words only make the report harder to
// act on.
func undeclaredChecks(ctx context.Context, home string, deps doctor.Deps, runner sbx.Runner,
	state *converge.SbxState, observeErr error) []doctor.Check {

	if _, err := deps.LookPath("sbx"); err != nil {
		return nil
	}
	var checks []doctor.Check
	// Secrets first: it is the one surface den can name entry by entry, and
	// the only one where "undeclared" is a comparison rather than a
	// tautology.
	if observeErr != nil {
		checks = append(checks, doctor.UndeclaredCheck(
			doctor.UnobservedSurface("sbx secrets", "sbx secret ls -g")))
	} else {
		checks = append(checks, doctor.UndeclaredCheck(doctor.NamedSurface(
			"sbx secrets", "sbx secret ls -g", state.SecretEntries(), declaredSecrets(home))))
	}
	// The opaque surfaces. Each read is its OWN subprocess and its own
	// failure: one unreadable store must not delete the report of the others,
	// which is the same reason doctor.Run never stops at the first problem.
	for _, store := range sbx.Stores {
		occupied, err := sbx.ReadStore(ctx, runner, store)
		if err != nil {
			checks = append(checks, doctor.UndeclaredCheck(
				doctor.UnobservedSurface(store.Name, store.Look)))
			continue
		}
		checks = append(checks, doctor.UndeclaredCheck(
			doctor.OpaqueSurface(store.Name, store.Look, occupied)))
	}
	return checks
}

// declaredSecrets collects the secret identities every manifested source in
// this home claims.
//
// A source whose manifest den cannot READ contributes nothing, and the
// direction of that concession is deliberate: a credential then shows up as
// undeclared — a line too many — where swallowing the failure would hide a
// present credential, which is the blind spot this whole report repairs. The
// unreadable manifest is not lost either: sourceChecks above already fails
// that source's own line, which is where the remedy belongs.
//
// Legacy sources (no manifest at all) declare nothing by construction. That is
// not a defect of theirs: they predate the resource vocabulary, and den has
// never claimed to converge them.
func declaredSecrets(home string) map[string]bool {
	names, err := source.Names(home)
	if err != nil {
		// No sources/ directory is the normal case. Everything present is then
		// undeclared, which is the honest answer on a machine with no source.
		return nil
	}
	declared := map[string]bool{}
	for _, name := range names {
		root := source.Dir(home, name)
		if !source.HasManifest(root) {
			continue
		}
		m, err := source.LoadManifest(root)
		if err != nil {
			continue
		}
		for _, id := range converge.DeclaredSecrets(m) {
			declared[id] = true
		}
	}
	return declared
}
