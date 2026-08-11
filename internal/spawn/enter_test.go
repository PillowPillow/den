package spawn

import (
	"context"
	"errors"
	"testing"

	"github.com/PillowPillow/den/internal/sbx"
)

// The shape spec §14.0 records: `sbx exec [flags] SANDBOX COMMAND [ARG...]`.
// A postponed -w would land as an argument of `bash -l` instead of setting the
// working directory, which is why the FULL argv is asserted, in order.
func TestEnterWithNoCommandOpensALoginShell(t *testing.T) {
	f := &sbx.Fake{}
	if err := Enter(context.Background(), f, "api", Command{Workdir: "/w/api", TTY: true}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasAttached("exec", "-it", "-w", "/w/api", "api", "bash", "-l") {
		t.Errorf("attaches = %v", f.Attaches)
	}
}

// The command replaces `bash -l`, and nothing else about the argv moves.
func TestEnterRunsTheGivenCommand(t *testing.T) {
	f := &sbx.Fake{}
	c := Command{Argv: []string{"go", "test", "./..."}, Workdir: "/w/api"}
	if err := Enter(context.Background(), f, "api", c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasPiped("exec", "-w", "/w/api", "api", "go", "test", "./...") {
		t.Errorf("pipes = %v", f.Pipes)
	}
}

// TTY false ⇒ NO -it, and the call goes through Pipe, not Attach. Attach would
// swallow the Ctrl-C that must kill a long command (see Runner's godoc).
func TestEnterWithoutATtyPipesAndPassesNoFlag(t *testing.T) {
	f := &sbx.Fake{}
	if err := Enter(context.Background(), f, "api", Command{Argv: []string{"true"}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(f.Attaches) != 0 {
		t.Errorf("a non-interactive command must not attach; attaches = %v", f.Attaches)
	}
	if !f.HasPiped("exec", "api", "true") {
		t.Errorf("pipes = %v", f.Pipes)
	}
}

// No -i either, and that is measured, not assumed: a piped stdin reaches the
// command with NO flag on sbx v0.38.0 (spec §14.0), where docker exec would
// require -i. Passing one den does not need is a divergence from the attested
// surface.
func TestEnterPassesNoInteractiveFlagWithoutATty(t *testing.T) {
	f := &sbx.Fake{}
	if err := Enter(context.Background(), f, "api", Command{Argv: []string{"cat"}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, a := range f.Pipes {
		for _, arg := range a {
			if arg == "-i" || arg == "-it" {
				t.Errorf("no interactive flag belongs on the non-tty path; pipes = %v", f.Pipes)
			}
		}
	}
}

// An empty workdir means "let the VM decide": den must not invent one, and
// must not pass a bare -w with nothing after it.
func TestEnterOmitsTheWorkdirWhenThereIsNone(t *testing.T) {
	f := &sbx.Fake{}
	if err := Enter(context.Background(), f, "api", Command{Argv: []string{"true"}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasPiped("exec", "api", "true") {
		t.Errorf("pipes = %v", f.Pipes)
	}
}

// TTY: true with a given command is the fourth matrix cell — `den exec api --
// vim` on a terminal — and Tasks 6/7 depend on this exact shape: it still goes
// through Attach, and the command replaces `bash -l` exactly as it does on the
// non-tty path.
func TestEnterWithATtyAndACommandAttachesTheCommand(t *testing.T) {
	f := &sbx.Fake{}
	c := Command{Argv: []string{"vim"}, Workdir: "/w/api", TTY: true}
	if err := Enter(context.Background(), f, "api", c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasAttached("exec", "-it", "-w", "/w/api", "api", "vim") {
		t.Errorf("attaches = %v", f.Attaches)
	}
}

// The contract of #60: the command's status becomes den's status. Enter is
// where the runner's error becomes the typed one main knows how to exit on,
// because both doors (den exec, den spawn) must answer identically.
func TestEnterTurnsAChildFailureIntoChildExit(t *testing.T) {
	f := &sbx.Fake{PipeErr: &sbx.ExecError{Bin: "sbx", Err: fakeExitError{code: 42}}}
	err := Enter(context.Background(), f, "api", Command{Argv: []string{"false"}})
	var child *sbx.ChildExit
	if !errors.As(err, &child) {
		t.Fatalf("err = %v, want a *sbx.ChildExit", err)
	}
	if child.Code != 42 {
		t.Errorf("Code = %d, want 42", child.Code)
	}
}

// The Attach mirror of the test above: the mapping sits AFTER the TTY
// dispatch, applied to whichever method's error comes back, not duplicated
// inside each arm. Without this test, a future refactor that moved the
// ExitCodeOf check into the Pipe arm only would still pass every other test
// in this file — TTY: true never scripts AttachErr anywhere else — while
// silently reverting `den exec`'s interactive exit-status contract that #60 and
// the team-lead ruling above it require.
func TestEnterTurnsAnAttachedChildFailureIntoChildExitToo(t *testing.T) {
	f := &sbx.Fake{AttachErr: &sbx.ExecError{Bin: "sbx", Err: fakeExitError{code: 3}}}
	err := Enter(context.Background(), f, "api", Command{Workdir: "/w/api", TTY: true})
	var child *sbx.ChildExit
	if !errors.As(err, &child) {
		t.Fatalf("err = %v, want a *sbx.ChildExit", err)
	}
	if child.Code != 3 {
		t.Errorf("Code = %d, want 3", child.Code)
	}
}

// A stand-in for a real *exec.ExitError: building one with a usable status
// means actually running and reaping a process, which this package's tests
// never do. sbx.ExitCodeOf matches on the method set precisely so this works.
type fakeExitError struct{ code int }

func (fakeExitError) Error() string   { return "exit status 42" }
func (e fakeExitError) ExitCode() int { return e.code }

// An sbx failure that is NOT a child status stays den's error, with den's
// message: "sbx not found in the PATH" must not be reported as "the command
// exited with status ...".
func TestEnterLeavesADenFailureAlone(t *testing.T) {
	want := errors.New("sbx is not installed")
	f := &sbx.Fake{PipeErr: want}
	if err := Enter(context.Background(), f, "api", Command{Argv: []string{"true"}}); !errors.Is(err, want) {
		t.Errorf("err = %v, want it to keep %v", err, want)
	}
}
