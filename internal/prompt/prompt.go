// Package prompt is den's ONE question-asking surface: four requests, one
// interface, and no third-party import.
//
// It is a leaf on purpose. The real implementation (internal/prompt/huhui)
// pulls 26 modules; every consumer — internal/spawn, internal/cli,
// internal/converge — imports THIS package instead, so the library's name
// appears in exactly one place in den. internal/prompt/hermeticity_test.go
// holds that line mechanically rather than by habit.
package prompt

import (
	"context"
	"errors"
)

// ErrNoTerminal is what a real Prompter returns when there is no terminal to
// draw on. It is a BACKSTOP, never the message a user reads: every caller
// checks Deps.IsTTY first and refuses in its own words, naming the flag that
// makes the same choice without a prompt (`--only`, `--without`, `--yes`).
//
// It exists because the library den uses fails OPEN (spec §3.d, measured
// 2026-08-18): with /dev/null on stdin, huh confirms the default selection
// nobody chose, returns a nil error, and then never lets the process exit. A
// caller that forgot its gate must hit this error instead of spawning a microVM
// with a phantom selection and hanging the job that asked for it.
var ErrNoTerminal = errors.New("no terminal to prompt on")

// Option is one line of a MultiSelect.
//
// Description carries what the old checklist printed as a trailing annotation
// (an unmapped repo key naming the config file to fix). It is a field rather
// than text appended to Label because the renderer decides how an annotation
// looks, and the caller decides what it says.
type Option struct {
	// Value is what MultiSelect returns for a checked line — den's short repo
	// name, never the label.
	Value string
	// Label is the line the human reads.
	Label string
	// Description is the secondary line, empty when there is nothing to add.
	Description string
}

// MultiSelectRequest is the repo checklist.
type MultiSelectRequest struct {
	Title   string
	Options []Option
	// Preselected is the initial state of EVERY box, and it is one field
	// carrying one fact on purpose (spec §5.3, invariant 2). `-i` starts full,
	// because confirming an untouched -i checklist must produce exactly what
	// `den up` alone produces; a `select: prompt` nest starts empty, because it
	// has no default selection to propose by definition.
	Preselected bool
}

// ConfirmRequest is a yes/no on a plan the caller has ALREADY printed.
// The renderer must not redraw or hide that plan: it is the trust boundary
// (internal/converge/render.go), and a confirmation that hid it would be
// uninformed consent.
type ConfirmRequest struct {
	Question string
}

// LineRequest reads one line of free text. It returns the line RAW: splitting,
// `~` expansion and validation stay with the caller (askRepositoryRoots), so a
// Prompter never learns what a path is.
type LineRequest struct {
	Question string
}

// SecretRequest reads a credential without echoing it.
type SecretRequest struct {
	Prompt string
}

// Prompter asks a human exactly four kinds of question.
//
// Injected like every other system access in den (cli.Deps): the real one binds
// the process's actual descriptors and puts them in raw mode, so a suite that
// inherited it would try to take over the test runner's terminal. Deps.ReadSecret
// was already this shape, and its godoc already said why; this interface
// generalizes it to the other three.
//
// Every method takes a context, and that is the ONE thing a Prompter cannot be
// asked for later. internal/cli/root.go builds a signal.NotifyContext over
// os.Interrupt and SIGTERM and dispatches through ExecuteContext precisely so
// a signal cancels the work in flight; a prompt outside that context makes the
// promise false at the only moment den is guaranteed to be waiting on a human.
// Measured in huh@v1.0.0: Form.Run() is RunWithContext(context.Background())
// (form.go:657-658), so an interface without a ctx leaves the renderer no way
// to be cancelled at all — den would keep a form on a terminal after the
// signal that was supposed to end the process. Nothing here is third-party:
// context is standard library, so the package doc's promise above still holds.
type Prompter interface {
	// MultiSelect returns the Values of the CHECKED options.
	MultiSelect(ctx context.Context, r MultiSelectRequest) ([]string, error)
	Confirm(ctx context.Context, r ConfirmRequest) (bool, error)
	Line(ctx context.Context, r LineRequest) (string, error)
	Secret(ctx context.Context, r SecretRequest) (string, error)
}
