package prompt

import (
	"context"
	"fmt"
)

// Fake is the Prompter every test uses.
//
// A PRODUCTION file, not a _test.go one, and for the reason internal/sbx/fake.go
// is: internal/cli, internal/spawn and internal/converge all need it, and a
// double that lives in one package's test files cannot be shared by three.
//
// The context is accepted and ignored (`_ context.Context`): this double
// answers from a slice and never blocks, so there is no in-flight work for a
// cancellation to reach, and what matters — that a cancelled context aborts a
// form — lives in the renderer (internal/prompt/huhui).
//
// What stays unenforced is PROPAGATION. The compiler guarantees a context
// arrives, never that it is the caller's own: a future call site passing
// context.Background() would compile, run, and silently lose the cancellation
// this interface exists to carry. Recording the ctx here is what a test would
// need to assert against that, and nothing does it today.
//
// It records requests as well as scripting answers. That is what makes the
// old checklist's rendered-bytes assertions survive the move: the header line
// and the unmapped-key annotation become a Title and a Description, and a test
// reads them back off MultiSelects rather than off a bytes.Buffer.
type Fake struct {
	// Scripted answers, consumed in order, one per call.
	MultiSelectAnswers [][]string
	ConfirmAnswers     []bool
	LineAnswers        []string
	SecretAnswers      []string
	// Err, when set, is returned by EVERY method before the script is touched.
	Err error

	// Recorded requests, in call order.
	MultiSelects []MultiSelectRequest
	Confirms     []ConfirmRequest
	Lines        []LineRequest
	Secrets      []SecretRequest
}

// exhausted is the refusal a short script gets. It names the method, because
// "the script ran out" with four methods in play sends the reader to the wrong
// field.
func exhausted(method string) error {
	return fmt.Errorf("prompt.Fake: %s was called with no scripted answer left — "+
		"add one to Fake.%sAnswers", method, method)
}

func (f *Fake) MultiSelect(_ context.Context, r MultiSelectRequest) ([]string, error) {
	f.MultiSelects = append(f.MultiSelects, r)
	if f.Err != nil {
		return nil, f.Err
	}
	if len(f.MultiSelectAnswers) == 0 {
		return nil, exhausted("MultiSelect")
	}
	answer := f.MultiSelectAnswers[0]
	f.MultiSelectAnswers = f.MultiSelectAnswers[1:]
	return answer, nil
}

func (f *Fake) Confirm(_ context.Context, r ConfirmRequest) (bool, error) {
	f.Confirms = append(f.Confirms, r)
	if f.Err != nil {
		return false, f.Err
	}
	if len(f.ConfirmAnswers) == 0 {
		return false, exhausted("Confirm")
	}
	answer := f.ConfirmAnswers[0]
	f.ConfirmAnswers = f.ConfirmAnswers[1:]
	return answer, nil
}

func (f *Fake) Line(_ context.Context, r LineRequest) (string, error) {
	f.Lines = append(f.Lines, r)
	if f.Err != nil {
		return "", f.Err
	}
	if len(f.LineAnswers) == 0 {
		return "", exhausted("Line")
	}
	answer := f.LineAnswers[0]
	f.LineAnswers = f.LineAnswers[1:]
	return answer, nil
}

func (f *Fake) Secret(_ context.Context, r SecretRequest) (string, error) {
	f.Secrets = append(f.Secrets, r)
	if f.Err != nil {
		return "", f.Err
	}
	if len(f.SecretAnswers) == 0 {
		return "", exhausted("Secret")
	}
	answer := f.SecretAnswers[0]
	f.SecretAnswers = f.SecretAnswers[1:]
	return answer, nil
}
