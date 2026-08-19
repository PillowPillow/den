// Package huhui renders den's prompts with charmbracelet/huh.
//
// It is the ONLY package in den that imports charmbracelet, and
// internal/prompt/hermeticity_test.go holds that line. Everything else in den
// speaks to prompt.Prompter, so the 26 modules this dependency brings stay
// behind one door and the deletion test on them stays cheap.
//
// It is also the only package in den with no behavioural test coverage worth
// the name, on the same terms as ports.ListenScanner and ports.OpenURL: a test
// that drove a real form would need a terminal, and no test in this repo
// acquires one (CLAUDE.md). What IS tested is the gate below — the half that
// decides whether a form is built at all.
package huhui

import (
	"errors"
	"fmt"
	"os"

	"github.com/PillowPillow/den/internal/prompt"
	"github.com/charmbracelet/huh"
	"golang.org/x/term"
)

// Prompter renders on a pair of descriptors.
//
// They are fields rather than package-level os.Stdin/os.Stdout reads so the
// gate is testable: huhui_test.go builds one over /dev/null and asserts the
// refusal, which is the only way this file's most important behaviour can be
// exercised without a tty.
type Prompter struct {
	In  *os.File
	Out *os.File
}

// New binds the process's real descriptors. It is what SystemDeps wires.
func New() *Prompter {
	return &Prompter{In: os.Stdin, Out: os.Stdout}
}

// gate refuses before any form exists, and it is the reason this package can be
// trusted at all.
//
// huh FAILS OPEN (measured 2026-08-18, spec §3.d): given /dev/null on stdin it
// prints its escape sequences into whatever is there, returns the default
// selection with a nil error, and then the process never exits. den's callers
// each check Deps.IsTTY and refuse in their own words before reaching this
// package — this second check exists because after this change that gate is the
// only thing between a cron job and a microVM built from a selection nobody
// made. A safety property that depends on every caller remembering is not a
// safety property.
//
// BOTH descriptors are required, which is #60's rule: with stdout redirected,
// a form nobody can see is worse than a refusal naming the flag that does the
// same job without asking.
func (p *Prompter) gate() error {
	if p.In == nil || p.Out == nil {
		return fmt.Errorf("%w: no descriptors are bound", prompt.ErrNoTerminal)
	}
	if !term.IsTerminal(int(p.In.Fd())) || !term.IsTerminal(int(p.Out.Fd())) {
		return prompt.ErrNoTerminal
	}
	return nil
}

// run executes one single-field form, in line, on den's descriptors.
//
// WithAccessible is never called, and that is a decision, not an omission
// (spec §8): accessible mode replaces the form with a plaintext question, and
// den does not have a degraded mode — it has refusals that name the flag doing
// the same job.
//
// No alt-screen: measured 2026-08-18, huh's default emits no ^[[?1049h, so
// den's own output — above all the converge plan a human is being asked to
// consent to — stays on screen above the form. internal/converge/render.go
// calls that plan the trust boundary; a form that scrolled it away would make
// the confirmation uninformed consent.
func (p *Prompter) run(field huh.Field) error {
	// ctrl+d joins ctrl+c on Quit. The bufio path this package replaced read
	// through a Scanner, so a terminal EOF refused loudly — "-i: input ended
	// before the selection was confirmed (a pipe, a closed terminal)"
	// (internal/spawn/interactive.go at 8514bd2), because confirming the
	// current state instead would be den deciding for the user. huh's default
	// spends that keystroke on half-page scrolling (keymap.go:151 and :166 bind
	// ctrl+d to Select/MultiSelect HalfPageDown), which turns EOF into a silent
	// no-answer. It has to reach the fail-closed cancel path below.
	//
	// Rebinding the form-level Quit is the WHOLE fix, verified in huh@v1.0.0:
	// WithKeyMap reassigns f.keymap (form.go:290) before propagating it to the
	// groups, and Form.Update matches f.keymap.Quit and RETURNS (form.go:557-563)
	// before group.Update ever runs (form.go:615) — no field's keymap sees
	// ctrl+d at all. Clearing HalfPageDown as well would be dead code twice
	// over: unreachable by that order, and never advertised either way, since
	// HalfPageDown appears in neither MultiSelect.KeyBinds
	// (field_multiselect.go:249-273) nor Select.KeyBinds (field_select.go:307-320).
	//
	// The trade, stated rather than left to be discovered: ctrl+d now aborts
	// whatever is already typed, where the canonical-mode EOF it restores fired
	// only on an empty line, and it takes ctrl+d away from textinput's
	// DeleteCharacterForward (bubbles textinput.go:78) — including while a
	// Select filter is being typed. Fail-closed is worth all three.
	km := huh.NewDefaultKeyMap()
	km.Quit.SetKeys("ctrl+c", "ctrl+d")
	err := huh.NewForm(huh.NewGroup(field)).
		WithKeyMap(km).
		WithInput(p.In).
		WithOutput(p.Out).
		Run()
	if errors.Is(err, huh.ErrUserAborted) {
		// ctrl+c and ctrl+d are answers, and the answer is no. Callers wrap
		// this error into their own refusal (e.g. "-i: reading the selection:
		// cancelled; ..." in internal/spawn); den exits non-zero and applies
		// nothing. This does NOT print a cancel-specific line of its own —
		// confirm() only prints "nothing was applied" when the answer is an
		// explicit No, not on an abort (internal/cli/answers.go).
		return errors.New("cancelled")
	}
	return err
}

// optionsFor builds huh's option list from a request. Extracted out of
// MultiSelect, which sits behind the gate, so this — the one piece of this
// package's logic that isn't "call huh and return" — is reachable by a test
// that never touches a terminal.
//
// huh v1.0.0's Option[T] carries no per-option description (option.go:6-10:
// Key, Value, and an unexported selected — nothing else), so a Description
// with nowhere else to go is folded into the Key. Dropping it instead would
// take the one thing it exists to say off the screen: promptOptionalRepos
// (internal/spawn/interactive.go) fills it with the config file to fix for a
// repo key with no host mapping, and den names the file to fix and the
// remedy (spec §2) — silently losing that string here would violate that on
// every render, with nothing failing to say so.
func optionsFor(r prompt.MultiSelectRequest) []huh.Option[string] {
	options := make([]huh.Option[string], 0, len(r.Options))
	for _, o := range r.Options {
		key := o.Label
		if o.Description != "" {
			key += " " + o.Description
		}
		options = append(options, huh.NewOption(key, o.Value).Selected(r.Preselected))
	}
	return options
}

func (p *Prompter) MultiSelect(r prompt.MultiSelectRequest) ([]string, error) {
	if err := p.gate(); err != nil {
		return nil, err
	}
	var chosen []string
	field := huh.NewMultiSelect[string]().
		Title(r.Title).
		Options(optionsFor(r)...).
		Value(&chosen)
	// No Limit call: a MultiSelect with no floor is what lets a `select:
	// prompt` nest be confirmed empty (measured, spec §3.f), which is that
	// mode's entire contract.
	if err := p.run(field); err != nil {
		return nil, err
	}
	return chosen, nil
}

func (p *Prompter) Confirm(r prompt.ConfirmRequest) (bool, error) {
	if err := p.gate(); err != nil {
		return false, err
	}
	var yes bool
	// Affirmative/Negative are left at their defaults, and the field starts on
	// the negative: den never defaults to yes on a plan (spec 2026-08-14 §7.1).
	if err := p.run(huh.NewConfirm().Title(r.Question).Value(&yes)); err != nil {
		return false, err
	}
	return yes, nil
}

func (p *Prompter) Line(r prompt.LineRequest) (string, error) {
	if err := p.gate(); err != nil {
		return "", err
	}
	var line string
	if err := p.run(huh.NewInput().Title(r.Question).Value(&line)); err != nil {
		return "", err
	}
	return line, nil
}

func (p *Prompter) Secret(r prompt.SecretRequest) (string, error) {
	if err := p.gate(); err != nil {
		return "", err
	}
	var secret string
	field := huh.NewInput().
		Title(r.Prompt).
		// EchoModeNone, not EchoModePassword: the mask mode renders one
		// character per keystroke, disclosing the credential's LENGTH to a
		// screen-share or a terminal recording, and leaves the masked line in
		// scrollback. EchoModeNone "displays nothing as characters are
		// entered" (huh@v1.0.0 field_input.go:189-191). The term.ReadPassword
		// this replaces echoed nothing, and internal/cli/answers.go promises
		// "Never echoed" at the call site — this line is what keeps that
		// sentence true.
		EchoMode(huh.EchoModeNone).
		Value(&secret)
	if err := p.run(field); err != nil {
		return "", err
	}
	return secret, nil
}
