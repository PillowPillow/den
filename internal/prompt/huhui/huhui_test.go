package huhui

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/PillowPillow/den/internal/prompt"
	"github.com/charmbracelet/huh"
)

// The gate refuses on a descriptor that is not a terminal, and it refuses
// BEFORE building a form. This is the one part of this package a hermetic
// suite can exercise, and it is the part that matters.
//
// Measured 2026-08-18 (spec §3.d): handed /dev/null, huh does not refuse. It
// confirms the default selection nobody chose, returns a nil error, and then
// the process never exits — a 5 s timeout kills it while a control binary
// without huh exits 0 instantly. `< /dev/null` is the canonical CI and cron
// stdin, so without this gate a scheduled `den up -i` would create a microVM
// with a phantom repo set and hang the job that asked for it.
//
// Every method is covered, not just MultiSelect: a gate on three methods out of
// four is a gate on none.
func TestEveryMethodRefusesWithoutATerminal(t *testing.T) {
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("opening %s: %v", os.DevNull, err)
	}
	t.Cleanup(func() { devNull.Close() })

	regular, err := os.Create(filepath.Join(t.TempDir(), "not-a-terminal"))
	if err != nil {
		t.Fatalf("creating a regular file: %v", err)
	}
	t.Cleanup(func() { regular.Close() })

	for _, f := range []struct {
		name string
		file *os.File
	}{
		{"/dev/null", devNull},
		{"a regular file", regular},
	} {
		t.Run(f.name, func(t *testing.T) {
			p := &Prompter{In: f.file, Out: f.file}

			if _, err := p.MultiSelect(prompt.MultiSelectRequest{
				Title:   "pick",
				Options: []prompt.Option{{Value: "web", Label: "web"}},
			}); !errors.Is(err, prompt.ErrNoTerminal) {
				t.Errorf("MultiSelect must refuse with ErrNoTerminal, got %v", err)
			}
			if _, err := p.Confirm(prompt.ConfirmRequest{Question: "apply?"}); !errors.Is(err, prompt.ErrNoTerminal) {
				t.Errorf("Confirm must refuse with ErrNoTerminal, got %v", err)
			}
			if _, err := p.Line(prompt.LineRequest{Question: "where?"}); !errors.Is(err, prompt.ErrNoTerminal) {
				t.Errorf("Line must refuse with ErrNoTerminal, got %v", err)
			}
			if _, err := p.Secret(prompt.SecretRequest{Prompt: "token"}); !errors.Is(err, prompt.ErrNoTerminal) {
				t.Errorf("Secret must refuse with ErrNoTerminal, got %v", err)
			}
		})
	}
}

// Both descriptors are required, and this is the residual shape #60 closed for
// `-i`: a real terminal on one side and a redirect on the other must still
// refuse. A form the user cannot see is worse than a refusal that names the
// flag doing the same job.
//
// Only the negative half is testable here — a suite that acquired a tty would
// stop being hermetic (CLAUDE.md) — so this asserts that a non-terminal on
// EITHER side is enough to refuse.
func TestOneNonTerminalDescriptorIsEnoughToRefuse(t *testing.T) {
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("opening %s: %v", os.DevNull, err)
	}
	t.Cleanup(func() { devNull.Close() })

	// os.Stdin under `go test` is not a terminal either, so this pairs two
	// non-terminals; the assertion is that the AND is what decides, not that
	// one particular side did.
	for _, p := range []*Prompter{
		{In: devNull, Out: os.Stdout},
		{In: os.Stdin, Out: devNull},
	} {
		if _, err := p.Confirm(prompt.ConfirmRequest{Question: "apply?"}); !errors.Is(err, prompt.ErrNoTerminal) {
			t.Errorf("a non-terminal on either side must refuse, got %v", err)
		}
	}
}

// New() binds the process's real descriptors. Asserted structurally, never by
// running a form: this test must not touch a terminal.
func TestNewBindsTheProcessDescriptors(t *testing.T) {
	p := New()
	if p.In != os.Stdin || p.Out != os.Stdout {
		t.Error("New must bind os.Stdin and os.Stdout")
	}
}

// selectedOf reads huh.Option's unexported `selected` field through reflect.
//
// huh v1.0.0's Option has Selected(bool) as a fluent SETTER only — no getter
// (option.go:29-32) — so there is no exported way to ask a built Option
// whether it is selected. reflect.Value.Bool() can still read an unexported
// bool field without going through Interface(), which is what would panic;
// FieldByName itself returns a zero Value (and .Bool() then panics) if huh
// ever renames the field, so this fails loudly on a library upgrade rather
// than silently reporting "not selected" and passing a broken test.
func selectedOf(o huh.Option[string]) bool {
	return reflect.ValueOf(o).FieldByName("selected").Bool()
}

// optionsFor is extracted out of MultiSelect specifically so this test can
// reach it without a terminal — otherwise the Description-folding fix it
// covers would ship untested, which is testable logic hiding behind a gate
// that only a real form could pass.
func TestOptionsFor(t *testing.T) {
	t.Run("an option's Description survives, folded into the Key", func(t *testing.T) {
		got := optionsFor(prompt.MultiSelectRequest{
			Options: []prompt.Option{
				{Value: "api", Label: "api", Description: "(not mapped in /home/user/.den/config.yaml)"},
			},
		})
		if len(got) != 1 {
			t.Fatalf("got %d options, want 1", len(got))
		}
		if want := "api (not mapped in /home/user/.den/config.yaml)"; got[0].Key != want {
			t.Errorf("Key = %q, want %q", got[0].Key, want)
		}
		if got[0].Value != "api" {
			t.Errorf("Value = %q, want %q (untouched by the annotation)", got[0].Value, "api")
		}
	})

	t.Run("an option with no Description is unchanged", func(t *testing.T) {
		got := optionsFor(prompt.MultiSelectRequest{
			Options: []prompt.Option{{Value: "web", Label: "web"}},
		})
		if len(got) != 1 {
			t.Fatalf("got %d options, want 1", len(got))
		}
		if got[0].Key != "web" {
			t.Errorf("Key = %q, want %q (no trailing space from an empty Description)", got[0].Key, "web")
		}
		if got[0].Value != "web" {
			t.Errorf("Value = %q, want %q", got[0].Value, "web")
		}
	})

	t.Run("Preselected reaches every option's Selected", func(t *testing.T) {
		for _, preselected := range []bool{true, false} {
			got := optionsFor(prompt.MultiSelectRequest{
				Options:     []prompt.Option{{Value: "a", Label: "a"}, {Value: "b", Label: "b"}},
				Preselected: preselected,
			})
			for _, o := range got {
				if selectedOf(o) != preselected {
					t.Errorf("Preselected=%v: option %q selected=%v, want %v",
						preselected, o.Key, selectedOf(o), preselected)
				}
			}
		}
	})
}
