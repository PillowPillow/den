package cli

import (
	"errors"
	"fmt"
	"testing"

	"github.com/PillowPillow/den/internal/sbx"
)

// cmd/den/main.go cannot be tested through os.Exit, so the DECISION it applies
// lives here, where it can be. What main keeps is one `if`.
func TestExitStatusYieldsTheChildStatusAndStaysSilent(t *testing.T) {
	code, denOwns := ExitStatus(fmt.Errorf("running the command: %w", &sbx.ChildExit{Code: 42}))
	if code != 42 {
		t.Errorf("code = %d, want 42", code)
	}
	if denOwns {
		t.Error("den must NOT print its own line over a child's failure: the child already spoke")
	}
}

// Every other error is den's, and keeps den's status and den's message.
func TestExitStatusKeepsDenOwnFailuresAtOne(t *testing.T) {
	code, denOwns := ExitStatus(errors.New(`sandbox "api" not found`))
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !denOwns {
		t.Error("den must print its own errors, prefixed, as it always has")
	}
}

// A zero-status child never reaches this path (no error at all), but a
// ChildExit{0} built by mistake must not turn a failure into a success.
func TestExitStatusNeverReportsSuccessOnAnError(t *testing.T) {
	if code, _ := ExitStatus(&sbx.ChildExit{Code: 0}); code == 0 {
		t.Error("an error must never exit 0")
	}
}
