package huhui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
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

			if _, err := p.MultiSelect(context.Background(), prompt.MultiSelectRequest{
				Title:   "pick",
				Options: []prompt.Option{{Value: "web", Label: "web"}},
			}); !errors.Is(err, prompt.ErrNoTerminal) {
				t.Errorf("MultiSelect must refuse with ErrNoTerminal, got %v", err)
			}
			if _, err := p.Confirm(context.Background(), prompt.ConfirmRequest{Question: "apply?"}); !errors.Is(err, prompt.ErrNoTerminal) {
				t.Errorf("Confirm must refuse with ErrNoTerminal, got %v", err)
			}
			if _, err := p.Line(context.Background(), prompt.LineRequest{Question: "where?"}); !errors.Is(err, prompt.ErrNoTerminal) {
				t.Errorf("Line must refuse with ErrNoTerminal, got %v", err)
			}
			if _, err := p.Secret(context.Background(), prompt.SecretRequest{Prompt: "token"}); !errors.Is(err, prompt.ErrNoTerminal) {
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
		if _, err := p.Confirm(context.Background(), prompt.ConfirmRequest{Question: "apply?"}); !errors.Is(err, prompt.ErrNoTerminal) {
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

// optionsFor is extracted out of MultiSelect specifically so this test can
// reach it without a terminal — otherwise the Description-folding fix it
// covers would ship untested, which is testable logic hiding behind a gate
// that only a real form could pass.
//
// Every case compares whole huh.Option[string] values with slices.Equal
// (==), not field by field. huh.Option[string] is comparable — Key, Value
// and an unexported `selected` field, all comparable types — and == reaches
// the unexported field too. Building the expected value with huh's own
// NewOption/Selected and comparing therefore asserts Key, Value AND selected
// in one line, through huh's public API only: this test never names the
// `selected` field itself, so it stays correct even if huh renames it. That
// is what covers "a field silently got dropped" — exactly the Description
// bug this test exists to catch — for every field at once, including any
// huh adds later.
func TestOptionsFor(t *testing.T) {
	t.Run("an option's Description survives, folded into the Key", func(t *testing.T) {
		got := optionsFor(prompt.MultiSelectRequest{
			Options: []prompt.Option{
				{Value: "api", Label: "api", Description: "(not mapped in /home/user/.den/config.yaml)"},
			},
		})
		want := []huh.Option[string]{huh.NewOption("api (not mapped in /home/user/.den/config.yaml)", "api")}
		if !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("an option with no Description is unchanged", func(t *testing.T) {
		got := optionsFor(prompt.MultiSelectRequest{
			Options: []prompt.Option{{Value: "web", Label: "web"}},
		})
		want := []huh.Option[string]{huh.NewOption("web", "web")}
		if !slices.Equal(got, want) {
			t.Errorf("got %v, want %v (no trailing space from an empty Description)", got, want)
		}
	})

	t.Run("Preselected reaches every option's Selected", func(t *testing.T) {
		for _, preselected := range []bool{true, false} {
			got := optionsFor(prompt.MultiSelectRequest{
				Options:     []prompt.Option{{Value: "a", Label: "a"}, {Value: "b", Label: "b"}},
				Preselected: preselected,
			})
			want := []huh.Option[string]{
				huh.NewOption("a", "a").Selected(preselected),
				huh.NewOption("b", "b").Selected(preselected),
			}
			if !slices.Equal(got, want) {
				t.Errorf("Preselected=%v: got %v, want %v", preselected, got, want)
			}
		}
	})
}

// Confirm's own comment (huhui.go) says den never defaults to yes on a plan
// (spec 2026-08-14 §7.1) — Affirmative/Negative left at huh's defaults, the field built
// with no explicit .Value(true). This pins ONLY that construction: the value
// huh.NewConfirm() reports for a fresh, untouched field is false.
//
// It does NOT pin that a bare Enter in a real terminal submits this value
// rather than activating the affirmative button — that half needs an actual
// terminal to drive the form and stays untested here, same as every other
// keystroke behavior this package's gate keeps out of a hermetic suite. If a
// huh upgrade ever flips this constructed default to true, this test is the
// one line that catches it before den's own suite would notice the drift.
func TestConfirmDefaultsToNo(t *testing.T) {
	var yes bool
	c := huh.NewConfirm().Title("apply this plan?").Value(&yes)
	if v, ok := c.GetValue().(bool); !ok || v {
		t.Fatalf("huh's constructed confirm default must be false: %v (ok=%v)", v, ok)
	}
}
