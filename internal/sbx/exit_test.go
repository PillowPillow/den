package sbx

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// The whole point of #60: a command that fails inside the sandbox must be able
// to fail den with ITS status, not with den's blanket 1. The code travels
// inside ExecError's chain (Unwrap already exposes the *exec.ExitError), and
// this is the reader that pulls it out.
//
// Attach rather than Pipe, though Pipe is the method that will carry this in
// production: Pipe does not exist yet (Task 2), and the property under test is
// ExecError's chain, which both methods build identically. runner_test.go
// already runs `&Exec{Bin: "sh"}` — this file's one licensed process spawn.
func TestExitCodeOfReadsTheChildStatus(t *testing.T) {
	e := &Exec{Bin: "sh"}
	err := e.Attach(context.Background(), "-c", "exit 42")
	if err == nil {
		t.Fatal("a child exiting 42 must produce an error")
	}
	code, ok := ExitCodeOf(err)
	if !ok || code != 42 {
		t.Errorf("ExitCodeOf = (%d, %v), want (42, true)", code, ok)
	}
}

// An error that is not a process status must not be mistaken for one: a den
// config error reaching os.Exit as "status 0" would report success.
func TestExitCodeOfIgnoresAnythingElse(t *testing.T) {
	if code, ok := ExitCodeOf(errors.New("no sandbox named api")); ok {
		t.Errorf("ExitCodeOf = (%d, true) on a plain error, want (_, false)", code)
	}
	if code, ok := ExitCodeOf(nil); ok {
		t.Errorf("ExitCodeOf = (%d, true) on nil, want (_, false)", code)
	}
}

// A process killed by a signal has NO exit status: os/exec reports -1, and
// os.Exit(-1) means 255 — a fabricated status the child never chose. den keeps
// its own 1 there, and says so.
func TestExitCodeOfRefusesASignaledChild(t *testing.T) {
	err := &ExecError{Bin: "sbx", Err: fakeExitError{code: -1}}
	if code, ok := ExitCodeOf(err); ok {
		t.Errorf("ExitCodeOf = (%d, true) on a signaled child, want (_, false)", code)
	}
}

func TestChildExitCarriesTheCodeInItsMessage(t *testing.T) {
	err := error(&ChildExit{Code: 42})
	if got, want := err.Error(), "the command exited with status 42"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	var child *ChildExit
	if !errors.As(fmt.Errorf("wrapped: %w", err), &child) || child.Code != 42 {
		t.Error("ChildExit must stay reachable through errors.As once wrapped")
	}
}

// fakeExitError stands in for an *exec.ExitError with a negative status. A
// real one is only produced by a signaled process, which this suite must not
// create: killing a child on a timer is the kind of race that makes a suite
// flaky on a loaded CI runner.
type fakeExitError struct{ code int }

func (fakeExitError) Error() string   { return "signal: killed" }
func (e fakeExitError) ExitCode() int { return e.code }
