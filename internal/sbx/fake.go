package sbx

import (
	"context"
	"io"
	"slices"
	"strings"
	"sync"
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
	// mu guards the recording fields, NOT the scripted ones.
	//
	// It exists because policy.Settle probes the allowlist concurrently
	// (probeAll): without it, `go test -race` reports a genuine data race on
	// the Calls append, and the recording can silently lose a call. The
	// scripted fields (Responses, Default, AttachErr) stay unguarded on
	// purpose — a test writes them BEFORE handing the Fake to production code,
	// and guarding them would suggest they may be rewritten mid-run.
	//
	// Consequence a test must know: under a concurrent caller the ORDER in
	// Calls is the order calls STARTED to be recorded, not the allowlist's.
	// Assert on presence (HasCalled) or on counts, never on the index of a
	// call that a concurrent pass produced.
	mu sync.Mutex

	// Calls records every invocation, Run and Attach alike, in order. It's what
	// general assertions check.
	Calls [][]string

	// Attaches records ONLY the calls to Attach, in order. Run and Attach are
	// irreconcilable (see runner.go): a Run whose argv starts with "exec" must
	// never count as an attach, and an attach must stay detectable even if a
	// legitimate Run has a similar argv. Attach feeds both Calls and Attaches;
	// Run feeds only Calls.
	Attaches [][]string

	// Pipes records ONLY the calls to Pipe, in order. Same reason as Attaches:
	// Calls conflates every method, so a Run whose argv starts with "exec"
	// would otherwise satisfy an assertion meant for a command actually run in
	// the sandbox. Pipe feeds both Calls and Pipes.
	Pipes [][]string

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

	// Inputs records ONLY the calls to RunInput, in order — the argv, never
	// the piped secret. A test asserting that a password travelled on stdin
	// reads this list and checks the value is absent from the argv.
	Inputs [][]string

	// Sensitive records ONLY the calls to RunSensitive, in order, ALREADY
	// redacted. Same reason as Inputs, opposite direction: here the value does
	// reach argv (sbx v0.38.0 leaves no choice for `secret set-custom`), so
	// what a test can see must be the redacted form.
	Sensitive [][]string

	// PipeErr is returned by Pipe. The call is still recorded when it fails —
	// a test asserting "den ran the command AND propagated its failure" needs
	// both halves.
	PipeErr error
}

// Run returns a COPY of the scripted output (never the underlying slice): a
// caller that mutates the received result must not corrupt later calls that
// fall back on the same Responses entry or on Default.
func (f *Fake) Run(_ context.Context, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.Calls = append(f.Calls, slices.Clone(args))
	f.mu.Unlock()
	if r, ok := f.Responses[strings.Join(args, " ")]; ok {
		return slices.Clone(r.Output), r.Err
	}
	return slices.Clone(f.Default.Output), f.Default.Err
}

// Stream records the call in Calls exactly as Run does — the argv sequence a
// test reads is the sequence of CALLS, and which method carried one is not
// something the golden or the ordering assertions should have to know — and
// WRITES the scripted output to out instead of returning it, which is the one
// behavioural difference the real Stream has.
//
// That difference is also what makes the method a test double can discriminate
// on without a Streams slice next to Attaches: a caller that went back to Run
// would leave out empty, since Run hands the bytes back rather than writing
// them. Attaches exists for a confusion Calls alone cannot resolve; this is not
// one, and Fake is a production type three packages share.
//
// A write error is deliberately ignored: out is a test's buffer, and the real
// Stream cannot report one either (os/exec's copier swallows it into the
// process's own outcome).
func (f *Fake) Stream(_ context.Context, out io.Writer, args ...string) error {
	f.mu.Lock()
	f.Calls = append(f.Calls, slices.Clone(args))
	f.mu.Unlock()
	r, ok := f.Responses[strings.Join(args, " ")]
	if !ok {
		r = f.Default
	}
	_, _ = out.Write(r.Output)
	return r.Err
}

func (f *Fake) Attach(_ context.Context, args ...string) error {
	f.mu.Lock()
	f.Calls = append(f.Calls, slices.Clone(args))
	f.Attaches = append(f.Attaches, slices.Clone(args))
	f.mu.Unlock()
	return f.AttachErr
}

func (f *Fake) Pipe(_ context.Context, args ...string) error {
	f.mu.Lock()
	f.Calls = append(f.Calls, slices.Clone(args))
	f.Pipes = append(f.Pipes, slices.Clone(args))
	f.mu.Unlock()
	return f.PipeErr
}

// HasCalled reports whether a call started with this argument prefix.
// Assertion by prefix, not equality: a test checking "a create did happen"
// must not break because one more path got mounted.
func (f *Fake) HasCalled(prefix ...string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
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
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.Attaches {
		if len(a) >= len(prefix) && slices.Equal(a[:len(prefix)], prefix) {
			return true
		}
	}
	return false
}

// HasPiped reports whether a PIPE (never a Run, never an Attach) started with
// this prefix. Same pattern as HasAttached, over Pipes: use it to assert that a
// non-interactive command was run, or that none was.
func (f *Fake) HasPiped(prefix ...string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.Pipes {
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

// RunInput records the ARGV and drops the input: the input is the secret, and
// a double that stored it would put a live credential in a test's memory and,
// on failure, in its output. A test asserting "den piped the password rather
// than passing it in argv" checks the argv it does record — the absence of the
// value there is the property.
func (f *Fake) RunInput(_ context.Context, _ []byte, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.Calls = append(f.Calls, slices.Clone(args))
	f.Inputs = append(f.Inputs, slices.Clone(args))
	f.mu.Unlock()
	if r, ok := f.Responses[strings.Join(args, " ")]; ok {
		return slices.Clone(r.Output), r.Err
	}
	return slices.Clone(f.Default.Output), f.Default.Err
}

// RunSensitive records the argv WITH the named positions redacted, and looks
// its response up under the redacted key too.
//
// Recording the redacted form is the point: `Fake.Calls` is what tests print
// on failure, and a golden or a t.Errorf that dumped the real argv would leak
// the value the production redaction exists to hide. The response lookup uses
// the same key so a test scripts the call it can actually see.
func (f *Fake) RunSensitive(_ context.Context, redactedIndexes []int, args ...string) ([]byte, error) {
	safe := RedactArgs(args, redactedIndexes)
	f.mu.Lock()
	f.Calls = append(f.Calls, safe)
	f.Sensitive = append(f.Sensitive, safe)
	f.mu.Unlock()
	if r, ok := f.Responses[strings.Join(safe, " ")]; ok {
		return slices.Clone(r.Output), r.Err
	}
	return slices.Clone(f.Default.Output), f.Default.Err
}
