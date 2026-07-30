package sbx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests exercise Exec, NOT sbx: the binary invoked is always "sh" (or a
// made-up name for the "not found" case), never "sbx". They stay hermetic and
// fast (milliseconds) — no network, nothing outside the current process.

// A missing binary must leave exec.ErrNotFound detectable in the chain:
// that's what lets a caller distinguish "sbx missing from the PATH" from any
// application failure.
func TestExecRunMissingBinary(t *testing.T) {
	e := &Exec{Bin: "den-binary-that-does-not-exist-x7q"}
	if _, err := e.Run(context.Background(), "ls"); !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("err = %v, want exec.ErrNotFound in the chain", err)
	}
}

// The FIRST-CONTACT failure — sbx not yet installed — is the most likely of
// all, and the only one the user can fix themselves. The message it produces
// must say what to do, and must not leave the user staring at
// `exec: "sbx": executable file not found in $PATH` (os/exec's message, with
// no remedy).
//
// `den doctor` is named because it diagnoses EXACTLY this case
// (internal/doctor/doctor.go, step 1: "sbx binary not found in the PATH") —
// and because before this test, `den doctor` appeared in NO user-facing
// message in the project, only in comments.
//
// The absence of the os/exec wording is asserted SEPARATELY from the presence
// of the actionable one: a message that added the translation without
// dropping the os/exec line would stay green on the presence assertion alone.
func TestExecRunMissingBinaryProducesAnActionableMessage(t *testing.T) {
	const bin = "den-binary-that-does-not-exist-x7q"
	e := &Exec{Bin: bin}

	_, err := e.Run(context.Background(), "ls", "--json")
	if err == nil {
		t.Fatal("a missing binary must produce an error")
	}
	message := err.Error()

	for _, want := range []string{bin, "not found in the PATH", "den doctor"} {
		if !strings.Contains(message, want) {
			t.Errorf("the message must contain %q; got: %s", want, message)
		}
	}
	for _, leftover := range []string{"executable file not found", "exec: "} {
		if strings.Contains(message, leftover) {
			t.Errorf("leftover os/exec wording %q in the message: %s", leftover, message)
		}
	}
	// The error chain must not be sacrificed to the message: it's what lets a
	// caller recognize this case without comparing strings.
	if !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("exec.ErrNotFound must stay detectable in the chain; err = %v", err)
	}
}

// Same requirement on Attach: it's what `den sh` and the final attach of
// `den <nest>` go through. There's no reason the message should read
// differently on one side or the other.
func TestExecAttachMissingBinaryProducesAnActionableMessage(t *testing.T) {
	const bin = "den-binary-that-does-not-exist-x7q"
	e := &Exec{Bin: bin}

	err := e.Attach(context.Background(), "exec", "-it", "api", "bash", "-l")
	if err == nil {
		t.Fatal("a missing binary must produce an error")
	}
	message := err.Error()

	for _, want := range []string{bin, "not found in the PATH", "den doctor"} {
		if !strings.Contains(message, want) {
			t.Errorf("the message must contain %q; got: %s", want, message)
		}
	}
	if strings.Contains(message, "executable file not found") {
		t.Errorf("leftover os/exec wording in the message: %s", message)
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("exec.ErrNotFound must stay detectable in the chain; err = %v", err)
	}
}

// The exit code must stay reachable via errors.As, and stderr must survive in
// the message: both are lost if the error isn't wrapped with %w.
func TestExecRunPreservesExitCodeAndStderr(t *testing.T) {
	e := &Exec{Bin: "sh"}
	_, err := e.Run(context.Background(), "-c", "echo boom >&2; exit 3")
	if err == nil {
		t.Fatalf("expected an error, got nil")
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("errors.As(err, &exitErr) must succeed; err = %v (%T)", err, err)
	}
	if exitErr.ExitCode() != 3 {
		t.Errorf("exit code = %d, want 3", exitErr.ExitCode())
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("message = %q, must contain the stderr output (%q)", err.Error(), "boom")
	}
}

// A context canceled BEFORE the process starts is the one case where os/exec
// surfaces the reason on its own: Cmd.Start returns ctx.Err() without
// launching anything. This is the easy case, and it proves NOTHING about the
// next one.
func TestExecRunContextCanceledBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	e := &Exec{Bin: "sh"}
	if _, err := e.Run(ctx, "-c", "true"); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled in the chain", err)
	}
}

// The REAL Ctrl-C case: the context is canceled WHILE the process runs.
// Measured repeatedly: os/exec kills the process and Cmd.Wait PREFERS the
// process's error to the context's, so cmd.Run returns a "signal: killed"
// that wraps no context reason. ExecError's comment used to claim the
// property below anyway — hence Run's explicit read of ctx.Err().
//
// Both reasons are exercised, because they don't behave the same for the user
// (Ctrl-C versus the settle-loop's timeout) and nothing guarantees os/exec
// treats them identically.
//
// # Why this test synchronizes on a witness rather than the clock
//
// Its precondition — the process is STILL RUNNING when the context is
// interrupted — was assumed, never observed, by an earlier version that
// canceled after a fixed 20 ms. Two distinct defects came out of that, both in
// the probe and neither in Run:
//
//  1. Both triggers were racing. The "deadline exceeded" case carried a 20 ms
//     deadline, and a shared goroutine also called cancel() after 20 ms;
//     whichever won set ctx.Err(). When the goroutine won, the observed
//     reason was Canceled and all four assertions failed at once.
//  2. The interruption could beat the fork/exec. Measured latency between
//     Start and the script's first executed byte, over 300 runs: p50 = 0.7 ms,
//     max = 1.0 ms idle; p50 = 8.5 ms, p99 = 22.2 ms, MAX = 26.2 ms under 12x
//     CPU saturation. A 20 ms deadline therefore fires REGULARLY before the
//     process starts: Cmd.Start returns ctx.Err() without launching anything,
//     there's no *exec.ExitError to find, and the test failed on the one
//     assertion that requires a process actually killed.
//
// Hence the witness: the script creates a file before sleeping, and its
// existence is ASSERTED afterward. The "cancellation" case becomes exact —
// the goroutine waits for the witness then cancels, with no clock assumption.
// The "deadline exceeded" case can't be, since a deadline runs from creation:
// its margin is set to 500 ms, 19x the worst measured startup under
// saturation, with the witness as a guard — without it, a failure would point
// at the margin needing a bump instead of accusing an untested property of
// Run.
func TestExecRunCancellationReasonSurvivesProcessDeath(t *testing.T) {
	// deadlineMargin: see the godoc above. 500 ms = 19x the measured MAX
	// (26.2 ms) under saturation.
	const deadlineMargin = 500 * time.Millisecond

	cases := []struct {
		name string
		// context returns a context whose in-flight interruption is already
		// armed, and which is the ONLY trigger. witness is the file the
		// script creates on startup.
		context      func(t *testing.T, witness string) context.Context
		reason       error
		reasonAbsent error
		wantMessage  string
	}{
		{
			name: "cancellation",
			context: func(t *testing.T, witness string) context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				t.Cleanup(cancel)
				// No deadline on this context: ctx.Err() can only be
				// context.Canceled. And cancellation only fires once startup
				// is CONFIRMED: this case makes no timing assumption at all.
				go func() {
					waitForWitness(witness)
					cancel()
				}()
				return ctx
			},
			reason:       context.Canceled,
			reasonAbsent: context.DeadlineExceeded,
			wantMessage:  "interrupted (canceled)",
		},
		{
			name: "deadline exceeded",
			context: func(t *testing.T, _ string) context.Context {
				ctx, cancel := context.WithTimeout(context.Background(), deadlineMargin)
				// cancel is only called AFTER the test body: during Run, the
				// deadline is the only trigger, and ctx.Err() can only be
				// context.DeadlineExceeded.
				t.Cleanup(cancel)
				return ctx
			},
			reason:       context.DeadlineExceeded,
			reasonAbsent: context.Canceled,
			wantMessage:  "interrupted (deadline exceeded)",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			witness := filepath.Join(t.TempDir(), "started")
			ctx := c.context(t, witness)

			e := &Exec{Bin: "sh", DrainDelay: 50 * time.Millisecond}
			// `exec sleep 5`, not `sleep 5`: without exec, dash forks and the
			// grandchild keeps the pipe (see
			// TestExecRunBoundsWaitWhenAGrandchildHoldsThePipe).
			_, err := e.Run(ctx, "-c", "touch "+witness+"; exec sleep 5")
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			// THE PRECONDITION, checked rather than assumed: this whole test is
			// about an interruption occurring WHILE the process runs. If the
			// process never started, Cmd.Start returned ctx.Err() without
			// launching anything — a case already covered by
			// TestExecRunContextCanceledBeforeStart, which proves nothing about
			// this one.
			if _, errWitness := os.Stat(witness); errWitness != nil {
				t.Fatalf("the process never started (witness %s absent): the interruption beat "+
					"the fork/exec, this test measures nothing it claims to — "+
					"increase the deadline margin (%v)", witness, deadlineMargin)
			}
			if !errors.Is(err, c.reason) {
				t.Errorf("errors.Is(err, %v) = false; err = %v", c.reason, err)
			}
			if errors.Is(err, c.reasonAbsent) {
				t.Errorf("errors.Is(err, %v) = true: the two reasons are conflated; err = %v",
					c.reasonAbsent, err)
			}
			// The message must SAY the interruption: a bare "signal: killed"
			// reads like sbx crashing, exactly the confusion ExecError's
			// comment claimed to avoid.
			if !strings.Contains(err.Error(), c.wantMessage) {
				t.Errorf("message = %q, want it to contain %q", err.Error(), c.wantMessage)
			}
			// Without this assertion, joining the cancellation reason could
			// have replaced the original chain instead of adding to it: the
			// other two ExecError properties would slip through unnoticed.
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				t.Errorf("the original *exec.ExitError was lost; err = %v (%T)", err, err)
			}
		})
	}
}

// waitForWitness blocks until the file the script creates on startup
// appears, returning at the latest after witnessTimeout.
//
// Called from a goroutine: it therefore can't fail the test itself (t.Fatalf
// outside the test goroutine is forbidden). It gives up silently on timeout,
// and it's the test body's Stat that judges — a single place gets to decide.
func waitForWitness(witness string) {
	// 100x the worst measured startup under 12x CPU saturation (26.2 ms): this
	// bound exists only so a goroutine doesn't leak if the script fails to
	// start, never to arbitrate a race.
	const witnessTimeout = 3 * time.Second
	deadline := time.Now().Add(witnessTimeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(witness); err == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

// Killing the process is NOT enough to unblock Run: dash forks for `sleep 5`,
// and the grandchild keeps stdout's pipe open — Wait blocks on the copy as
// long as it lives. Measured: 5.007 s for a cancellation at 20 ms, versus
// 21 ms with `exec sleep 5` (no fork) or a direct binary.
//
// ⚠️ ASSUMPTION, not a fact: that `sbx` also leaves a descendant holding the
// pipe (a microVM supervisor) has NEVER been observed — `sbx` isn't
// installed, everything above is measured on `sh`. If the assumption is
// false, this bound never fires in production; it costs nothing either way,
// since a drain cut short on a success is now reported as a success. Recorded
// in spec §14.1 along with what would falsify it.
//
// The test doesn't measure an absolute duration (that would be flaky under
// load) but the SEPARATION: returning well before the grandchild's natural
// end.
//
// The cancellation waits for the witness, for the same reason as
// TestExecRunCancellationReasonSurvivesProcessDeath (see its godoc) — but here
// the risk wasn't a failure, it was an EMPTY SUCCESS: canceled before dash had
// forked (up to 26.2 ms of measured startup latency under saturation, for a
// fixed 20 ms delay), Run would return right away, no grandchild, no held
// pipe, and the bound never exercised — and the "under 2 s" assertion would
// pass anyway. A test that passes vacuously is indistinguishable from one that
// passes.
//
// THE SCRIPT'S SHAPE IS THE TEST. `sleep 5 & touch ...; wait` places the fork
// BEFORE the witness: once the witness appears, the grandchild already
// exists and has inherited the pipe. The naive form — `touch ...; sleep 5` —
// does the opposite and RUINS the test, measured: the witness precedes the
// fork there, the cancellation kills dash before it forked, no grandchild
// holds the pipe, and Run returns in 13 ms even with WaitDelay disarmed. The
// table, context canceled on witness:
//
//	"touch T; sleep 5"        WaitDelay=0     →   13 ms   (no grandchild)
//	"sleep 5 & touch T; wait" WaitDelay=0     → 5001 ms   (the pipe is held)
//	"sleep 5 & touch T; wait" WaitDelay=50ms  →   59 ms   (the bound applies)
//	"sleep 5 & touch T; wait" WaitDelay=2s    → 2006 ms   (at its value)
//
// It's the second line that gives the third its meaning: without it, the
// bound isn't what makes Run return.
func TestExecRunBoundsWaitWhenAGrandchildHoldsThePipe(t *testing.T) {
	witness := filepath.Join(t.TempDir(), "started")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		waitForWitness(witness)
		cancel()
	}()

	e := &Exec{Bin: "sh", DrainDelay: 50 * time.Millisecond}
	start := time.Now()
	// No `exec` here, unlike the other test, and that's the whole point: the
	// `&` forks a grandchild, and it's the one holding the pipe. The
	// fork-then-witness order is what makes the precondition observable (see
	// godoc).
	if _, err := e.Run(ctx, "-c", "sleep 5 & touch "+witness+"; wait"); err == nil {
		t.Fatal("expected an error, got nil")
	}
	// The precondition, checked: without the witness, the grandchild was never
	// forked, no pipe was held, and the duration assertion below would be
	// true for the wrong reason.
	if _, err := os.Stat(witness); err != nil {
		t.Fatalf("witness %s is absent: the grandchild was never forked, no pipe was "+
			"held, and the wait bound was therefore never exercised", witness)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Run took %v to return: the wait bound doesn't apply "+
			"(the grandchild holds the pipe for 5 s)", elapsed)
	}
}

// THE HAPPY PATH, the one no test covered when WaitDelay was introduced:
// `sbx create` SUCCEEDS while leaving behind a descendant that inherited
// stdout's pipe. The context is NEVER canceled here.
//
// WaitDelay isn't armed only by context cancellation: its timer also starts
// as soon as Wait observes the process exit. A success whose pipes are closed
// by WaitDelay makes exec.ErrWaitDelay surface INSTEAD of nil — so "den
// couldn't create the sandbox" while the sandbox EXISTS, and the spawn
// sequence abandoned (neither settle nor attach).
//
// It's exactly the scenario the bound exists to handle — a supervisor
// surviving `sbx create` — and which remains an ASSUMPTION about `sbx` (spec
// §14.1): the script below is `sh`, not `sbx`.
func TestExecRunDoesNotFailWhenADescendantSurvivesAProcessSuccess(t *testing.T) {
	cases := []struct {
		name   string
		script string
		want   string
	}{
		{"descendant launched after the write", "echo started; sleep 5 &", "started\n"},
		{"descendant launched before the write", "sleep 5 & echo created", "created\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := &Exec{Bin: "sh", DrainDelay: 50 * time.Millisecond}
			start := time.Now()

			output, err := e.Run(context.Background(), "-c", c.script)
			if err != nil {
				t.Fatalf("the process exited with SUCCESS: Run must not return an error; err = %v", err)
			}
			if string(output) != c.want {
				t.Errorf("output = %q, want %q: closing the pipe must not lose "+
					"what the direct child wrote before exiting", output, c.want)
			}
			// The bound must still apply: without it, this test would pass by
			// waiting out the descendant's 5 s, and would no longer prove the
			// success is returned WITHOUT waiting.
			if elapsed := time.Since(start); elapsed > 2*time.Second {
				t.Errorf("Run took %v: the wait bound no longer applies", elapsed)
			}
		})
	}
}

// The previous property's precondition, isolated: os/exec's copier drains the
// pipe CONTINUOUSLY, so whatever the direct child wrote before exiting is
// already collected by the time WaitDelay closes the pipe.
//
// 240 KiB, well beyond a pipe's buffer (64 KiB): a truncation would show up
// here, where "started\n" would fit in the buffer regardless. Whatever the
// DESCENDANT would write after the close is lost, and that's intended.
func TestExecRunDoesNotTruncateOutputAlreadyWrittenByTheDirectChild(t *testing.T) {
	const lines = 40000
	e := &Exec{Bin: "sh", DrainDelay: 50 * time.Millisecond}

	output, err := e.Run(context.Background(), "-c", "yes line | head -n 40000; sleep 5 &")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := bytes.Count(output, []byte("\n")); got != lines {
		t.Errorf("truncated output: %d lines out of %d expected (%d bytes)",
			got, lines, len(output))
	}
}

// exitState returns a REAL *os.ProcessState for the requested exit code.
// Built by running a process: os.ProcessState's fields aren't exported, and a
// hand-rolled double would prove nothing about the type actually inspected.
func exitState(t *testing.T, script string) *os.ProcessState {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	_ = cmd.Run() // failure is expected for nonzero codes
	if cmd.ProcessState == nil {
		t.Fatalf("no ProcessState for %q", script)
	}
	return cmd.ProcessState
}

// The guard's three conditions, one at a time. End-to-end tests only cover
// the first one: os/exec only returns ErrWaitDelay on a success status, so
// the other two cover states we can't provoke on demand. Without this test
// they'd stay unproven — and a guard that's too broad would turn an sbx
// FAILURE into a silent success.
func TestDrainCutShortOnSuccessRequiresAllThreeConditions(t *testing.T) {
	success := exitState(t, "true")
	failure := exitState(t, "exit 3")
	otherErr := errors.New("some failure")

	cases := []struct {
		name   string
		err    error
		ctxErr error
		state  *os.ProcessState
		want   bool
	}{
		{"drain cut short on a success", exec.ErrWaitDelay, nil, success, true},
		{"drain cut short, wrapped", fmt.Errorf("x: %w", exec.ErrWaitDelay), nil, success, true},
		{"other error", otherErr, nil, success, false},
		{"no error", nil, nil, success, false},
		{"context canceled", exec.ErrWaitDelay, context.Canceled, success, false},
		{"context deadline exceeded", exec.ErrWaitDelay, context.DeadlineExceeded, success, false},
		{"process exited with failure", exec.ErrWaitDelay, nil, failure, false},
		{"process never started", exec.ErrWaitDelay, nil, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := drainCutShortOnSuccess(c.err, c.ctxErr, c.state); got != c.want {
				t.Errorf("drainCutShortOnSuccess(%v, %v, %v) = %v, want %v",
					c.err, c.ctxErr, c.state, got, c.want)
			}
		})
	}
}

// Exec's NULL value must be the safe value: `&Exec{Bin: "sbx"}` with no
// explicit setting must be bounded, not suspended forever. The meaning of 0
// is inverted between the field ("take the default") and cmd.WaitDelay
// ("wait forever"), exactly the kind of inversion a refactor loses. Checked
// against effectiveDelay rather than by running a process: the bound is
// already proven by the previous test, here it's the default's CHOICE.
func TestEffectiveDelayNeverReturnsZero(t *testing.T) {
	if d := effectiveDelay(0); d != defaultDrainDelay {
		t.Errorf("effectiveDelay(0) = %v, want the default %v (0 = wait forever on os/exec's side)",
			d, defaultDrainDelay)
	}
	if d := effectiveDelay(-1); d != defaultDrainDelay {
		t.Errorf("effectiveDelay(-1) = %v, want the default %v", d, defaultDrainDelay)
	}
	if d := effectiveDelay(50 * time.Millisecond); d != 50*time.Millisecond {
		t.Errorf("effectiveDelay(50ms) = %v: an explicit setting must be respected", d)
	}
}

// SSH access for EVERY sandbox rests on a mechanism nothing in this file
// makes visible: neither Run nor Attach set cmd.Env, so the sbx process
// inherits den's environment — and with it SSH_AUTH_SOCK, the host's SSH
// agent socket. It's the only support for `ssh.mode: agent-forward`, the
// config DEFAULT (internal/config/config.go), which adds neither an argv
// argument to `sbx create` nor a mixin entry.
//
// Nothing in the code expressed this dependency: setting `cmd.Env = ...`, for
// any good reason — neutralizing a variable, restricting the environment —
// would cut SSH access for every sandbox without breaking a single other
// test. These two tests are what forbid it; the precedent already exists in
// the project (internal/worktree does set a cmd.Env, to neutralize the git
// configuration).
//
// WHAT'S PROVEN HERE, and nothing more: the process den launches receives
// den's environment. Whether sbx then propagates that socket INTO the microVM
// isn't verifiable without sbx, and remains a spec assumption.
//
// The value is SET by the test (t.Setenv), not assumed: asserting
// SSH_AUTH_SOCK is non-empty without setting it would pass on a machine with
// an agent running and fail everywhere else. t.Setenv forbids t.Parallel in
// the calling test.
func TestExecRunTransmitsDenEnvironment(t *testing.T) {
	const socket = "/tmp/den-test-agent-ssh-run.sock"
	t.Setenv("SSH_AUTH_SOCK", socket)

	e := &Exec{Bin: "sh"}
	out, err := e.Run(context.Background(), "-c", `printf %s "$SSH_AUTH_SOCK"`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := string(out); got != socket {
		t.Errorf("SSH_AUTH_SOCK seen by the process = %q, want %q — the process Run launches "+
			"must inherit den's environment (cmd.Env left nil); without this inheritance, "+
			"`ssh.mode: agent-forward`, the default, would give sandboxes no SSH access at all",
			got, socket)
	}
}

// Same property on Attach. It matters just as much: `den sh` and the final
// attach of `den <nest>` go through it, and a stripped environment wouldn't
// be any more visible there than on Run.
//
// The witness goes through a FILE rather than stdout, because Attach
// deliberately wires cmd.Stdout to the current process's — there's nothing to
// capture without hijacking os.Stdout for the whole suite. The file lives in
// t.TempDir(): nothing is written outside the test's bounds.
func TestExecAttachTransmitsDenEnvironment(t *testing.T) {
	const socket = "/tmp/den-test-agent-ssh-attach.sock"
	t.Setenv("SSH_AUTH_SOCK", socket)
	witness := filepath.Join(t.TempDir(), "socket-seen")

	e := &Exec{Bin: "sh"}
	// `sh -c SCRIPT ARG0`: the first argument after the script becomes $0.
	if err := e.Attach(context.Background(), "-c", `printf %s "$SSH_AUTH_SOCK" > "$0"`, witness); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	content, err := os.ReadFile(witness)
	if err != nil {
		t.Fatalf("reading witness %s: %v", witness, err)
	}
	if got := string(content); got != socket {
		t.Errorf("SSH_AUTH_SOCK seen by the process = %q, want %q — the process Attach launches "+
			"must inherit den's environment (cmd.Env left nil)", got, socket)
	}
}

// Attach is the ONLY method where context cancellation must do NOTHING: a
// Ctrl-C typed in the sandbox's shell is delivered by the tty driver to the
// process group, not relayed through den's context; if the context ends while
// Attach runs, the shell must neither be killed nor have its normal outcome
// replaced by a context error.
func TestExecAttachIgnoresContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	e := &Exec{Bin: "sh"}
	if err := e.Attach(ctx, "-c", "sleep 0.05"); err != nil {
		t.Errorf("unexpected error: %v (context cancellation must not affect Attach)", err)
	}
}
