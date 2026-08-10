package sbx

import (
	"errors"
	"fmt"
)

// ExitCodeOf pulls the status of the process sbx ran out of an error chain,
// and reports whether there was one at all.
//
// It lives HERE, with the runner whose ExecError carries that chain, rather
// than in internal/cli where it is consumed: interpreting what sbx returned is
// sbx's business, and a second reader elsewhere would be a second place for
// the -1 rule below to be forgotten. It also keeps internal/cli clear of
// `os/exec`, which TestCliImportsNoRawPortOrSystemAccess
// (internal/ports/hermeticity_test.go) forbids it to import.
//
// The match is on the METHOD SET, `interface{ ExitCode() int }`, not on
// *exec.ExitError. Two consequences, both wanted: this file needs no os/exec
// import at all, and a test can exercise the negative-status branch below with
// a three-line double instead of signaling a real process — killing a child on
// a timer is the kind of race that makes a suite flaky on a loaded runner.
//
// A NEGATIVE status is refused rather than returned. os/exec answers -1 for a
// process killed by a signal — it has no status of its own — and os.Exit(-1)
// means 255 on this platform: den would report a status the child never chose.
// On that path den keeps its own 1, which is true (den's run failed) instead
// of invented.
func ExitCodeOf(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	var exitErr interface{ ExitCode() int }
	if !errors.As(err, &exitErr) {
		return 0, false
	}
	code := exitErr.ExitCode()
	if code < 0 {
		return 0, false
	}
	return code, true
}

// ChildExit says: the command den ran INSIDE the sandbox exited with this
// status, and den's own machinery did not fail.
//
// The distinction is the whole of #60's "usable in CI" requirement. Without a
// type here, `den exec api -- false` and a den config error are the same event
// to cmd/den/main.go, which exits 1 on any error. With it, main exits on the
// child's status AND prints nothing: the child already wrote whatever it had
// to say, and a "den: ..." line over it would be den claiming an error that
// is not its own.
//
// A status of 1 remains indistinguishable from a den failure BY THE CODE
// ALONE, and no code can be reserved without breaking the contract "the
// command's status becomes den's status". The `den:` prefix on stderr is what
// separates them for a human.
type ChildExit struct{ Code int }

func (e *ChildExit) Error() string {
	return fmt.Sprintf("the command exited with status %d", e.Code)
}
