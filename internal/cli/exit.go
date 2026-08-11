package cli

import (
	"errors"

	"github.com/PillowPillow/den/internal/sbx"
)

// ExitStatus maps an error returned by Execute onto den's process status, and
// says whether the message belongs to den.
//
// Split out of cmd/den/main.go rather than written there: main is the one file
// in this module a test cannot exercise (os.Exit ends the test binary), and
// "which failures den prefixes" is a contract, not plumbing. main keeps the
// one `if` that calls this.
//
// A ChildExit of 0 is impossible from the exec path — a command that succeeds
// returns no error — but it is clamped anyway: an error that exits 0 would
// report success to a CI runner, which is the exact failure this whole change
// exists to prevent.
func ExitStatus(err error) (code int, denOwnsMessage bool) {
	var child *sbx.ChildExit
	if errors.As(err, &child) && child.Code > 0 {
		return child.Code, false
	}
	return 1, true
}
