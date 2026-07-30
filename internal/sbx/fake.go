package sbx

import (
	"context"
	"slices"
	"strings"
)

// Response is what the Fake returns for a given call.
type Response struct {
	Output []byte
	Err    error
}

// Fake is the test double of Runner.
//
// It lives in the production package rather than in a `_test.go` ON PURPOSE:
// policy, cli and agent all need it, and a double per package would drift
// from the real contract right away. `internal/` already bounds its scope.
type Fake struct {
	// Calls records every invocation, Run and Attach alike, in order. It's what
	// general assertions check.
	Calls [][]string

	// Attaches records ONLY the calls to Attach, in order. Run and Attach are
	// irreconcilable (see runner.go): a Run whose argv starts with "exec" must
	// never count as an attach, and an attach must stay detectable even if a
	// legitimate Run has a similar argv. Attach feeds both Calls and Attaches;
	// Run feeds only Calls.
	Attaches [][]string

	// Responses maps a call to a response, key = args joined by a space (e.g.
	// "ls --json"). An argument that itself contains a space (a workspace path,
	// say) can therefore produce a key ambiguous with another call: avoid that
	// when scripting Responses.
	Responses map[string]Response

	// Default is used when no Responses entry matches.
	Default Response

	// AttachErr is returned by Attach. The fact that the attach happened is
	// still recorded in Calls even if it fails.
	AttachErr error
}

// Run returns a COPY of the scripted output (never the underlying slice): a
// caller that mutates the received result must not corrupt later calls that
// fall back on the same Responses entry or on Default.
func (f *Fake) Run(_ context.Context, args ...string) ([]byte, error) {
	f.Calls = append(f.Calls, slices.Clone(args))
	if r, ok := f.Responses[strings.Join(args, " ")]; ok {
		return slices.Clone(r.Output), r.Err
	}
	return slices.Clone(f.Default.Output), f.Default.Err
}

func (f *Fake) Attach(_ context.Context, args ...string) error {
	f.Calls = append(f.Calls, slices.Clone(args))
	f.Attaches = append(f.Attaches, slices.Clone(args))
	return f.AttachErr
}

// HasCalled reports whether a call started with this argument prefix.
// Assertion by prefix, not equality: a test checking "a create did happen"
// must not break because one more path got mounted.
func (f *Fake) HasCalled(prefix ...string) bool {
	for _, a := range f.Calls {
		if len(a) >= len(prefix) && slices.Equal(a[:len(prefix)], prefix) {
			return true
		}
	}
	return false
}

// HasAttached reports whether an ATTACH (never a Run) started with this
// prefix. Same pattern as HasCalled, but over Attaches: use it to assert that
// an interactive shell was wired up, or that no attach happened (policy
// blocked, `--detach`).
func (f *Fake) HasAttached(prefix ...string) bool {
	for _, a := range f.Attaches {
		if len(a) >= len(prefix) && slices.Equal(a[:len(prefix)], prefix) {
			return true
		}
	}
	return false
}

// Compile-time guard: Fake must stay substitutable for Runner. Lives here,
// with production code, not in a `_test.go`: if Runner changes, the failure
// must surface at `go build ./...`, not at the first `go test` of some
// downstream package that consumes Fake.
var _ Runner = (*Fake)(nil)
