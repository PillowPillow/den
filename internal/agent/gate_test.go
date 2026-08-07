package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PillowPillow/den/internal/sbx"
)

// The mixin name and the dispatcher directory are ONE string. If they ever
// diverge the gate watches a kit nobody generates — and reports "has not
// reported yet" forever, on a sandbox whose agent may be stale.
func TestMixinNameFlattensTheSandboxSeparator(t *testing.T) {
	cases := map[string]string{
		"alpha":      "den-alpha",
		"api.feat12": "den-api-feat12",
	}
	for sandbox, want := range cases {
		if got := MixinName(sandbox); got != want {
			t.Errorf("MixinName(%q) = %q, want %q", sandbox, got, want)
		}
	}
}

// okLog is a complete dispatcher run in which den's mixin passed — the journal
// of a healthy sandbox, transcribed from a real one (2026-07-31).
const okLog = `=== dispatcher run 2026-07-31T15:34:24Z ===
> /etc/durable-startup.d/001-startup-claude/000-cmd.sh
ok /etc/durable-startup.d/001-startup-claude/000-cmd.sh
> /etc/durable-startup.d/002-startup-den-alpha/000-cmd.sh
agent claude: up to date
ok /etc/durable-startup.d/002-startup-den-alpha/000-cmd.sh
=== dispatcher complete ===
`

// failLog is the measured failure path of smoke #2 §D4: an agent whose update
// command exits non-zero, three bounded attempts, then the fail-closed abort.
const failLog = `=== dispatcher run 2026-07-31T15:34:24Z ===
> /etc/durable-startup.d/002-startup-den-epsilon/000-cmd.sh
agent broken: attempt 1/3 failed:
agent broken: attempt 2/3 failed:
agent broken: attempt 3/3 failed:
agent broken: FATAL update failed after 3 attempts (fail-closed)
fail /etc/durable-startup.d/002-startup-den-epsilon/000-cmd.sh exit=1
`

// The four constants below are the TWO-ENTRY journal this branch introduced:
// den's kit directory now holds a link phase (`000-cmd.sh`) AND the freshness
// command (`001-cmd.sh`). Transcribed from the real VM on 2026-08-07 (sbx
// v0.37.1, spec 2026-08-07-mounts-design §"Mesuré le 2026-08-07"), down to the
// `den mounts:` line the link phase prints and the fact that a refused link
// phase aborts the run before `001-cmd.sh` is ever announced.
//
// They exist because every earlier gate fixture hardcoded ONE entry, which is
// what let the gate read the link phase's `ok` as the freshness verdict.
const twoEntryDir = "/etc/durable-startup.d/002-startup-den-smoke"

// linkOKFreshnessFail: the link phase passed, the agent update did NOT. This is
// the journal of a sandbox whose agent is stale, and reading it as a pass is a
// verbatim return of defect #18 — den attaching, exit 0, in silence, to a
// sandbox whose agent was never updated.
const linkOKFreshnessFail = `=== dispatcher run 2026-08-07T10:00:00Z ===
> ` + twoEntryDir + `/000-cmd.sh
den mounts: /home/agent/.linkme -> /tmp/den-mount-smoke/linkme
ok ` + twoEntryDir + `/000-cmd.sh
> ` + twoEntryDir + `/001-cmd.sh
agent claude: attempt 1/3 failed:
agent claude: FATAL update failed after 3 attempts (fail-closed)
fail ` + twoEntryDir + `/001-cmd.sh exit=1
`

// linkOKFreshnessRunning: the link phase passed and the freshness command has
// been ANNOUNCED but has not reported. The ordinary first seconds of a spawn.
const linkOKFreshnessRunning = `=== dispatcher run 2026-08-07T10:00:00Z ===
> ` + twoEntryDir + `/000-cmd.sh
den mounts: /home/agent/.linkme -> /tmp/den-mount-smoke/linkme
ok ` + twoEntryDir + `/000-cmd.sh
> ` + twoEntryDir + `/001-cmd.sh
`

// linkFail: the link phase refused. `001-cmd.sh` does not appear AT ALL — the
// dispatcher cut on `exit=1` (measured 2026-08-07), which is precisely why the
// freshness command must stay last and the link phase must go first.
const linkFail = `=== dispatcher run 2026-08-07T10:00:00Z ===
> ` + twoEntryDir + `/000-cmd.sh
den mounts: FATAL /home/agent/.linkme is a non-empty directory (from mounts[0]) — den refuses to replace it
fail ` + twoEntryDir + `/000-cmd.sh exit=1
`

// linkDiedBeforeAnyMount: the link phase ANNOUNCED itself and then died without
// a FATAL line of its own — the VM's shell failing, or the dispatcher killing
// the script. The start line is the whole reason den can still tell this from a
// failed agent update: before it, a link phase that failed before its first
// den_link left no marker at all, and the gate sent the user to the agent
// registry to fix a `mounts:` fault.
const linkDiedBeforeAnyMount = `=== dispatcher run 2026-08-07T10:00:00Z ===
> ` + twoEntryDir + `/000-cmd.sh
den mounts: linking 2 mount(s)
/etc/durable-startup.d/002-startup-den-smoke/000-cmd.sh: line 12: killed
fail ` + twoEntryDir + `/000-cmd.sh exit=137
`

// linkOKFreshnessOK: both entries passed. The healthy two-entry sandbox.
const linkOKFreshnessOK = `=== dispatcher run 2026-08-07T10:00:00Z ===
> ` + twoEntryDir + `/000-cmd.sh
den mounts: /home/agent/.linkme -> /tmp/den-mount-smoke/linkme
ok ` + twoEntryDir + `/000-cmd.sh
> ` + twoEntryDir + `/001-cmd.sh
agent claude: up to date
ok ` + twoEntryDir + `/001-cmd.sh
=== dispatcher complete ===
`

// #18, RETURNED. This is the regression test for the defect the mounts branch
// silently restored: with a link phase in front of it, the gate returned on the
// link's `ok` and never read the freshness verdict at all, so a sandbox whose
// agent update FAILED was reported as passing and attached to, exit 0, in
// silence. §9.1 promises "a sandbox never starts with a stale agent".
func TestParseKitLogFailsWhenFreshnessFailsBehindAPassingLinkPhase(t *testing.T) {
	v := ParseKitLog([]byte(linkOKFreshnessFail), "den-smoke")
	if v.State != GateFailed {
		t.Fatalf("state = %v, want GateFailed: the link phase's `ok` must not decide the gate "+
			"— the freshness command exited non-zero and the agent is stale", v.State)
	}
	if !strings.Contains(v.Line, "001-cmd.sh") || !strings.Contains(v.Line, "exit=1") {
		t.Errorf("the line that decided must be the FRESHNESS verdict; got %q", v.Line)
	}
}

// A freshness command that has been announced and has not answered is PENDING.
// Before the fix the link phase's `ok` returned GatePassed here too, which made
// WaitFreshness stop waiting milliseconds into a wait that takes ~35 s — den
// attached while `claude update` was still running.
func TestParseKitLogIsPendingWhileFreshnessRunsBehindAPassingLinkPhase(t *testing.T) {
	if v := ParseKitLog([]byte(linkOKFreshnessRunning), "den-smoke"); v.State != GatePending {
		t.Errorf("state = %v, want GatePending: the link phase passing says nothing about "+
			"the agent", v.State)
	}
}

// A failing LINK phase is a failing link phase, not a failing agent update. The
// remedy is in `mounts:` / `ssh.dir`, and a reason that sends the user to the
// agent registry costs them the diagnosis the journal already contains.
func TestParseKitLogNamesTheLinkPhaseWhenItRefuses(t *testing.T) {
	v := ParseKitLog([]byte(linkFail), "den-smoke")
	if v.State != GateFailed {
		t.Fatalf("state = %v, want GateFailed", v.State)
	}
	if !strings.Contains(v.Reason, "link phase") {
		t.Errorf("the reason must name the link phase, not the agent update; got %q", v.Reason)
	}
	if !strings.Contains(v.Reason, "non-empty directory") {
		t.Errorf("the link phase's own FATAL line is the diagnosis and must travel; got %q", v.Reason)
	}
	if !strings.Contains(v.Remedy, "mounts:") {
		t.Errorf("the remedy must name the key to fix; got %q", v.Remedy)
	}
	if !strings.Contains(v.Line, "exit=1") {
		t.Errorf("the verdict line must still be carried; got %q", v.Line)
	}
}

// A link phase that dies BEFORE any den_link is still a mounts fault. The
// discriminator used to be one-way — a marker proved the link phase, its
// absence proved nothing — and the absence was read as proof of a failed
// freshness command: den refused the spawn telling the user to fix the agent's
// `update:` command in the registry, for a fault in `mounts:`, while the
// updater had never run at all.
func TestParseKitLogDoesNotBlameTheAgentWhenTheLinkPhaseDiesEarly(t *testing.T) {
	v := ParseKitLog([]byte(linkDiedBeforeAnyMount), "den-smoke")
	if v.State != GateFailed {
		t.Fatalf("state = %v, want GateFailed", v.State)
	}
	if !strings.Contains(v.Reason, "link phase") {
		t.Errorf("the reason must name the link phase; got %q", v.Reason)
	}
	if strings.Contains(v.Reason, "freshness command exited") || strings.Contains(v.Remedy, "registry") {
		t.Errorf("the agent updater never ran — it must not be blamed; got reason %q remedy %q",
			v.Reason, v.Remedy)
	}
	if !strings.Contains(v.Remedy, "mounts:") {
		t.Errorf("the remedy must name the key to look at; got %q", v.Remedy)
	}
	// "refused" is a den DECISION, and none was taken here: saying so would send
	// the reader looking for a FATAL line the journal does not carry.
	if strings.Contains(v.Reason, "refused") {
		t.Errorf("no refusal was printed, the reason must not claim one; got %q", v.Reason)
	}
}

func TestParseKitLogReadsAPassingTwoEntryRun(t *testing.T) {
	v := ParseKitLog([]byte(linkOKFreshnessOK), "den-smoke")
	if v.State != GatePassed {
		t.Fatalf("state = %v, want GatePassed", v.State)
	}
	if !strings.Contains(v.Line, "001-cmd.sh") {
		t.Errorf("the line carried must be the FRESHNESS verdict, the one that decided; got %q", v.Line)
	}
}

// BACKWARD COMPATIBILITY. A sandbox created by an older den carries a ONE-entry
// journal, and must still gate exactly as it did — the mounts branch changed
// what den WRITES, and a reader that only works on the new shape would turn
// every pre-existing sandbox into a 90-second timeout on attach. This is the
// same case drift.go's `n > 1` guard exists for.
func TestParseKitLogStillGatesAOneEntryLegacyJournal(t *testing.T) {
	legacyDir := "/etc/durable-startup.d/002-startup-den-legacy"
	pass := "=== dispatcher run 2026-07-31T15:34:24Z ===\n" +
		"> " + legacyDir + "/000-cmd.sh\n" +
		"agent claude: up to date\n" +
		"ok " + legacyDir + "/000-cmd.sh\n" +
		"=== dispatcher complete ===\n"
	fail := "=== dispatcher run 2026-07-31T15:34:24Z ===\n" +
		"> " + legacyDir + "/000-cmd.sh\n" +
		"agent claude: FATAL update failed after 3 attempts (fail-closed)\n" +
		"fail " + legacyDir + "/000-cmd.sh exit=1\n"

	if v := ParseKitLog([]byte(pass), "den-legacy"); v.State != GatePassed {
		t.Errorf("state = %v, want GatePassed on a one-entry journal", v.State)
	}
	v := ParseKitLog([]byte(fail), "den-legacy")
	if v.State != GateFailed {
		t.Fatalf("state = %v, want GateFailed on a one-entry journal", v.State)
	}
	// And it is read as the FRESHNESS command failing: on a legacy journal
	// `000-cmd.sh` IS the freshness command, and nothing in it says `den mounts:`.
	if !strings.Contains(v.Reason, "freshness") {
		t.Errorf("a one-entry journal's failure is the freshness command; got %q", v.Reason)
	}
}

func TestParseKitLogReadsAPassingRun(t *testing.T) {
	v := ParseKitLog([]byte(okLog), "den-alpha")
	if v.State != GatePassed {
		t.Errorf("state = %v, want GatePassed (%v)", v.State, GatePassed)
	}
}

// The failure carries the LINE. §9.1 says the journal is what made the
// 2026-07-27 bug diagnosable; a verdict that drops it sends the user back into
// the VM to read what den has already read.
func TestParseKitLogReadsAFailingRunAndKeepsTheLine(t *testing.T) {
	v := ParseKitLog([]byte(failLog), "den-epsilon")
	if v.State != GateFailed {
		t.Fatalf("state = %v, want GateFailed", v.State)
	}
	if !strings.Contains(v.Line, "exit=1") {
		t.Errorf("the line that decided must travel with the verdict; got %q", v.Line)
	}
}

// A run in progress is PENDING, not absent: den's mixin is layered last, so the
// ordinary first ~35 s after a spawn look exactly like this.
func TestParseKitLogReportsARunInProgressAsPending(t *testing.T) {
	inProgress := `=== dispatcher run 2026-07-31T15:34:24Z ===
> /etc/durable-startup.d/001-startup-claude/000-cmd.sh
ok /etc/durable-startup.d/001-startup-claude/000-cmd.sh
> /etc/durable-startup.d/002-startup-den-alpha/000-cmd.sh
`
	if v := ParseKitLog([]byte(inProgress), "den-alpha"); v.State != GatePending {
		t.Errorf("state = %v, want GatePending", v.State)
	}
}

// ONLY THE LAST RUN COUNTS. The journal accumulates one block per boot — the
// dispatcher really does re-run when a stopped sandbox is restarted (measured
// 2026-07-31, which is what smoke #2 left open) — so a failure from a previous
// boot describes a sandbox that no longer exists in that state.
func TestParseKitLogReadsOnlyTheLastRun(t *testing.T) {
	v := ParseKitLog([]byte(failLog+okLog), "den-alpha")
	if v.State != GatePassed {
		t.Errorf("state = %v, want GatePassed: a failure from an earlier boot must not "+
			"condemn the run that followed it", v.State)
	}
	// ... and symmetrically, a pass followed by a failure is a failure.
	v = ParseKitLog([]byte(okLog+failLog), "den-epsilon")
	if v.State != GateFailed {
		t.Errorf("state = %v, want GateFailed", v.State)
	}
}

// An EARLIER kit failing is den's problem too: the dispatcher does `exit $rc`
// at the first non-zero command (§14.0), so den's freshness command — layered
// last on purpose — never ran, and an agent that was never updated is exactly
// what §9.1 forbids starting with.
func TestParseKitLogTreatsAnAbortedRunAsAFailure(t *testing.T) {
	aborted := `=== dispatcher run 2026-07-31T15:34:24Z ===
> /etc/durable-startup.d/001-startup-claude/000-cmd.sh
fail /etc/durable-startup.d/001-startup-claude/000-cmd.sh exit=127
`
	v := ParseKitLog([]byte(aborted), "den-alpha")
	if v.State != GateFailed {
		t.Fatalf("state = %v, want GateFailed", v.State)
	}
	if !strings.Contains(v.Reason, "never ran") {
		t.Errorf("the reason must say den's command never ran, not that it failed; got %q", v.Reason)
	}
}

// A COMPLETED run that never names den's mixin is a sandbox carrying no den
// mixin at all — what a VM created by an older den looks like. Worth saying,
// never worth refusing over: waiting cannot fix it.
func TestParseKitLogReportsAMissingMixinAsAbsent(t *testing.T) {
	foreign := `=== dispatcher run 2026-07-31T15:34:24Z ===
> /etc/durable-startup.d/001-startup-claude/000-cmd.sh
ok /etc/durable-startup.d/001-startup-claude/000-cmd.sh
=== dispatcher complete ===
`
	if v := ParseKitLog([]byte(foreign), "den-alpha"); v.State != GateAbsent {
		t.Errorf("state = %v, want GateAbsent", v.State)
	}
}

// A kit named `den-api` must not read the lines of `002-startup-den-api-feat12`.
// The match is on a whole path SEGMENT; a substring test would confuse a nest
// with its own worktrees, and report the wrong sandbox's verdict.
func TestParseKitLogDoesNotConfuseASandboxWithItsWorktrees(t *testing.T) {
	other := `=== dispatcher run 2026-07-31T15:34:24Z ===
> /etc/durable-startup.d/002-startup-den-api-feat12/000-cmd.sh
fail /etc/durable-startup.d/002-startup-den-api-feat12/000-cmd.sh exit=1
=== dispatcher complete ===
`
	if v := ParseKitLog([]byte(other), "den-api"); v.State != GateFailed {
		// The run DID fail — an earlier (foreign) kit aborted it — but the
		// reason must not claim den-api's own command failed.
		t.Fatalf("state = %v, want GateFailed (the run aborted before den-api's kit)", v.State)
	}
	v := ParseKitLog([]byte(other), "den-api")
	if !strings.Contains(v.Reason, "never ran") {
		t.Errorf("den-api's command never ran: the reason must say so, not blame it; got %q", v.Reason)
	}
}

// instantGate is the real budget with a clock that only moves when the loop
// sleeps: the wait is exercised in full, in no wall-clock time.
func instantGate() GateOptions {
	o := DefaultGateOptions()
	clock := time.Now()
	o.Sleep = func(d time.Duration) { clock = clock.Add(d) }
	o.Now = func() time.Time { return clock }
	return o
}

func TestWaitFreshnessReadsTheJournalThroughSbxExec(t *testing.T) {
	f := &sbx.Fake{Default: sbx.Response{Output: []byte(okLog)}}

	v, err := WaitFreshness(context.Background(), f, "alpha", instantGate(), nil)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if v.State != GatePassed {
		t.Errorf("state = %v, want GatePassed", v.State)
	}
	if !f.HasCalled("exec", "alpha", "cat", KitLogPath) {
		t.Errorf("the journal is read through `sbx exec`; calls: %v", f.Calls)
	}
	if len(f.Calls) != 1 {
		t.Errorf("a verdict already in the journal must cost ONE read, not a poll; calls: %v", f.Calls)
	}
}

// The "waiting" line is announced ONCE, and only when den is really going to
// wait: a gate that has already reported — a re-attach, a sandbox spawned a
// minute ago — must not print it at all.
func TestWaitFreshnessAnnouncesTheWaitOnceAndOnlyWhenItWaits(t *testing.T) {
	silent := &sbx.Fake{Default: sbx.Response{Output: []byte(okLog)}}
	announced := 0
	if _, err := WaitFreshness(context.Background(), silent, "alpha", instantGate(),
		func() { announced++ }); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if announced != 0 {
		t.Errorf("nothing was waited for: the wait must not be announced (%d times)", announced)
	}

	pending := &sbx.Fake{Default: sbx.Response{Output: []byte("=== dispatcher run X ===\n")}}
	announced = 0
	v, err := WaitFreshness(context.Background(), pending, "alpha", instantGate(),
		func() { announced++ })
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if v.State != GatePending {
		t.Errorf("state = %v, want GatePending: the budget ran out with no verdict", v.State)
	}
	if announced != 1 {
		t.Errorf("the wait must be announced exactly once, not per poll; got %d", announced)
	}
	if len(pending.Calls) < 2 {
		t.Errorf("a pending gate must be POLLED, not read once; calls: %v", pending.Calls)
	}
}

// A journal den cannot read at all is an error, not a silent pass: §9.1 makes
// the update fail-closed, and "I could not check" must never render as "it is
// fine".
func TestWaitFreshnessFailsWhenTheJournalCannotBeRead(t *testing.T) {
	f := &sbx.Fake{Default: sbx.Response{Err: errors.New("boom")}}

	if _, err := WaitFreshness(context.Background(), f, "alpha", instantGate(), nil); err == nil {
		t.Fatal("an unreadable journal must be an error, never a silent pass")
	}
}

// Incomplete options are refused rather than completed — policy.Options'
// doctrine, and it applies here for a sharper reason: a zero Timeout produces a
// gate that reports "has not yet" on every spawn, which reads as den checking
// when it is not.
func TestWaitFreshnessRefusesIncompleteOptions(t *testing.T) {
	f := &sbx.Fake{Default: sbx.Response{Output: []byte(okLog)}}

	_, err := WaitFreshness(context.Background(), f, "alpha", GateOptions{}, nil)
	if err == nil {
		t.Fatal("zero options must be refused")
	}
	if !strings.Contains(err.Error(), "DefaultGateOptions") {
		t.Errorf("the refusal must name the constructor to build them from; got: %v", err)
	}
	if len(f.Calls) != 0 {
		t.Errorf("unusable options must be caught before any call; calls: %v", f.Calls)
	}
}

// A clock that never advances must not hang the suite: the round guard is
// arithmetic, not temporal, exactly as policy.Options.maxRounds is.
func TestWaitFreshnessSurvivesAFrozenClock(t *testing.T) {
	f := &sbx.Fake{Default: sbx.Response{Output: []byte("=== dispatcher run X ===\n")}}
	o := DefaultGateOptions()
	frozen := time.Now()
	o.Sleep = func(time.Duration) {}
	o.Now = func() time.Time { return frozen }

	v, err := WaitFreshness(context.Background(), f, "alpha", o, nil)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if v.State != GatePending {
		t.Errorf("state = %v, want GatePending", v.State)
	}
	if len(f.Calls) > o.maxRounds() {
		t.Errorf("the round guard did not bound the loop: %d calls for %d rounds",
			len(f.Calls), o.maxRounds())
	}
}
