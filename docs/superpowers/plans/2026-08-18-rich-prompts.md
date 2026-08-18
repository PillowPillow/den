# Rich Prompts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace den's four numbered/`bufio` prompts with `charmbracelet/huh` forms, behind an injected `Prompter` interface, without letting the library's fail-open behaviour reach a machine.

**Architecture:** A leaf package `internal/prompt` declares the `Prompter` interface, its four request types and a production `Fake`. `internal/prompt/huhui` is the only package that imports `charmbracelet` and re-checks the terminal itself before building any form. `spawn`, `cli` and `converge` consume the interface. The existing `IsTTY` refusals stay exactly where they are, above the interface.

**Tech Stack:** Go 1.26, cobra, yaml.v3, golang.org/x/{mod,term}, and — new here — github.com/charmbracelet/huh v1.0.0.

**Spec:** `docs/superpowers/specs/2026-08-18-rich-prompts-design.md`

## Global Constraints

- **Language:** code, comments and user-facing messages in **English**. The spec and handoffs under `docs/superpowers/` are French. (CLAUDE.md)
- **Comment style:** long "why" comments at the decision site — what was rejected, what regression the choice prevents. Terse code visibly does not match the surroundings. (CLAUDE.md)
- **Refusals name the file to fix and the remedy.** den refuses rather than normalizing in silence. (spec §2)
- **No test calls `t.Parallel()`, opens a socket, spawns a process, or acquires a tty.** (CLAUDE.md)
- **Strict YAML decoding everywhere** — untouched by this plan, listed so nobody relaxes it in passing.
- **`task check`** = lint » typecheck » test, fail-fast. Run it before every commit. `gofmt` is enforced, not advisory.
- **Goldens have no `-update` flag** — edit `testdata/*.golden` by hand.
- **`huh` version floor:** `github.com/charmbracelet/huh v1.0.0` (its own `go 1.23.0`). 26 new modules; the binary stays static and cgo-free.
- **`WithAccessible` is NEVER enabled** (spec §8). It is a plaintext fallback, and den has refusals, not fallbacks.
- **Only `internal/prompt/huhui` may import `github.com/charmbracelet/*`; only `internal/cli` may import `internal/prompt/huhui`.** (spec §6, enforced by Task 5.)

---

## File Structure

**Created**

| File | Responsibility |
|---|---|
| `internal/prompt/prompt.go` | The `Prompter` interface, four request types, `ErrNoTerminal`. No third-party imports, ever. |
| `internal/prompt/fake.go` | `Fake` — scripted answers **and** recorded requests. A **production** file, like `internal/sbx/fake.go`, because `cli`, `spawn` and `converge` all need it. |
| `internal/prompt/fake_test.go` | Pins the `Fake`'s own refusal when its script runs out. |
| `internal/prompt/huhui/huhui.go` | The only importer of `huh`. Builds forms; gates on the real descriptors first. |
| `internal/prompt/huhui/huhui_test.go` | The gate's negative verdicts, against `/dev/null` and a regular file. |
| `internal/prompt/hermeticity_test.go` | Import guard, both halves. |

**Modified**

| File | Change |
|---|---|
| `internal/spawn/spawn.go:75` | `Deps.In io.Reader` **deleted**; `Deps.Prompt prompt.Prompter` added. |
| `internal/spawn/interactive.go` | `promptOptionalRepos` builds a request instead of drawing a list; `parseToggles` deleted. |
| `internal/spawn/interactive_test.go` | 18 input sites move from `strings.NewReader` to a scripted `Fake`. |
| `internal/cli/up.go:35` | `d.In = cmd.InOrStdin()` **deleted**; `d.Prompt = deps.Prompt` added. |
| `internal/cli/root.go` | `Deps.ReadSecret` **deleted**, `Deps.Prompt` added; `SystemDeps` wires `huhui.New()`. |
| `internal/cli/answers.go` | `askRepositoryRoots` and `confirm` go through `d.Prompt`. |
| `internal/spawn/isterminal_darwin.go`, `isterminal_linux.go` | Comment correction only (Task 6). |

---

### Task 1: `internal/prompt` — the interface, the requests, the Fake

Nothing consumes this yet. It exists so Task 2 has something to compile against.

**Files:**
- Create: `internal/prompt/prompt.go`
- Create: `internal/prompt/fake.go`
- Test: `internal/prompt/fake_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `prompt.Prompter` (interface with `MultiSelect`, `Confirm`, `Line`, `Secret`); `prompt.Option{Value, Label, Description string}`; `prompt.MultiSelectRequest{Title string, Options []Option, Preselected bool}`; `prompt.ConfirmRequest{Question string}`; `prompt.LineRequest{Question string}`; `prompt.SecretRequest{Prompt string}`; `prompt.ErrNoTerminal error`; `*prompt.Fake` with fields `MultiSelectAnswers [][]string`, `ConfirmAnswers []bool`, `LineAnswers []string`, `SecretAnswers []string`, `Err error`, and recordings `MultiSelects []MultiSelectRequest`, `Confirms []ConfirmRequest`, `Lines []LineRequest`, `Secrets []SecretRequest`.

- [ ] **Step 1: Write the interface and request types**

Create `internal/prompt/prompt.go`:

```go
// Package prompt is den's ONE question-asking surface: four requests, one
// interface, and no third-party import.
//
// It is a leaf on purpose. The real implementation (internal/prompt/huhui)
// pulls 26 modules; every consumer — internal/spawn, internal/cli,
// internal/converge — imports THIS package instead, so the library's name
// appears in exactly one place in den. internal/prompt/hermeticity_test.go
// holds that line mechanically rather than by habit.
package prompt

import "errors"

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
	Title string
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
type Prompter interface {
	// MultiSelect returns the Values of the CHECKED options.
	MultiSelect(MultiSelectRequest) ([]string, error)
	Confirm(ConfirmRequest) (bool, error)
	Line(LineRequest) (string, error)
	Secret(SecretRequest) (string, error)
}
```

- [ ] **Step 2: Write the failing test for the Fake**

Create `internal/prompt/fake_test.go`:

```go
package prompt

import (
	"errors"
	"strings"
	"testing"
)

// A Fake that runs out of scripted answers REFUSES. It must never return a
// zero value and a nil error.
//
// That is not tidiness: an exhausted script returning ([]string(nil), nil) is
// the exact shape of the bug this whole design exists to keep out — a
// selection nobody made, reported as success (spec §3.d). A test whose script
// is one answer short would then pass while asserting on a phantom.
func TestFakeRefusesWhenTheScriptRunsOut(t *testing.T) {
	f := &Fake{}
	if _, err := f.MultiSelect(MultiSelectRequest{Title: "pick"}); err == nil {
		t.Fatal("an exhausted Fake must refuse, not answer nothing")
	}
	_, err := f.Confirm(ConfirmRequest{Question: "apply?"})
	if err == nil {
		t.Fatal("an exhausted Fake must refuse on Confirm too")
	}
	if !strings.Contains(err.Error(), "Confirm") {
		t.Errorf("the refusal must name the method whose script ran out: %v", err)
	}
}

// The Fake records what den ASKED, not only what den did. Assertions on the
// rendered checklist move here when the bufio renderer goes away (spec §6).
func TestFakeRecordsTheRequest(t *testing.T) {
	f := &Fake{MultiSelectAnswers: [][]string{{"worker"}}}
	got, err := f.MultiSelect(MultiSelectRequest{
		Title:       "nest api: 2 optional repo(s)",
		Options:     []Option{{Value: "worker", Label: "worker"}},
		Preselected: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != "worker" {
		t.Errorf("scripted answer not returned: %v", got)
	}
	if len(f.MultiSelects) != 1 {
		t.Fatalf("the request must be recorded once, got %d", len(f.MultiSelects))
	}
	if !f.MultiSelects[0].Preselected {
		t.Error("Preselected must be recorded: it is how a test reads the starting state")
	}
	if f.MultiSelects[0].Title != "nest api: 2 optional repo(s)" {
		t.Errorf("the title must be recorded verbatim: %q", f.MultiSelects[0].Title)
	}
}

// Err wins over the script, so a test can exercise a caller's error path.
func TestFakeErrWinsOverTheScript(t *testing.T) {
	boom := errors.New("boom")
	f := &Fake{ConfirmAnswers: []bool{true}, Err: boom}
	if _, err := f.Confirm(ConfirmRequest{Question: "apply?"}); !errors.Is(err, boom) {
		t.Errorf("Err must win over a scripted answer, got %v", err)
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/prompt/ -count=1`
Expected: FAIL — `undefined: Fake`.

- [ ] **Step 4: Write the Fake**

Create `internal/prompt/fake.go`:

```go
package prompt

import "fmt"

// Fake is the Prompter every test uses.
//
// A PRODUCTION file, not a _test.go one, and for the reason internal/sbx/fake.go
// is: internal/cli, internal/spawn and internal/converge all need it, and a
// double that lives in one package's test files cannot be shared by three.
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

func (f *Fake) MultiSelect(r MultiSelectRequest) ([]string, error) {
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

func (f *Fake) Confirm(r ConfirmRequest) (bool, error) {
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

func (f *Fake) Line(r LineRequest) (string, error) {
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

func (f *Fake) Secret(r SecretRequest) (string, error) {
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
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/prompt/ -count=1`
Expected: PASS (3 tests).

- [ ] **Step 6: Run the full check**

Run: `task check`
Expected: PASS. Nothing else in the tree references the new package yet.

- [ ] **Step 7: Commit**

```bash
git add internal/prompt/
git commit -m "feat(prompt): den's four questions get one interface

A leaf package: the interface, four request types, and a production Fake that
records requests as well as scripting answers. Nothing consumes it yet.

The Fake refuses when its script runs out rather than returning a zero value,
because an answer nobody gave reported as success is the exact failure this
design exists to keep out of den."
```

---

### Task 2: The `-i` checklist goes through the Prompter

The slice that fixes the measured complaint. It is shippable alone: after it, `-i` renders as a real toggle list in tests' terms, and Task 3 gives it a real renderer.

**Files:**
- Modify: `internal/spawn/interactive.go` (`promptOptionalRepos`, `interactiveWithout`; delete `parseToggles`)
- Modify: `internal/spawn/spawn.go:75` (`Deps.In` → `Deps.Prompt`)
- Modify: `internal/cli/up.go:35`
- Modify: `internal/cli/root.go` (add `Deps.Prompt`, thread it into `spawn.Deps`)
- Test: `internal/spawn/interactive_test.go` (18 input sites)

**Interfaces:**
- Consumes: `prompt.Prompter`, `prompt.Fake`, `prompt.MultiSelectRequest`, `prompt.Option` (Task 1).
- Produces: `spawn.Deps.Prompt prompt.Prompter`; `cli.Deps.Prompt prompt.Prompter`; `promptOptionalRepos(p prompt.Prompter, mappingPath, nestName string, repos []nest.Repo, prompts bool, mapping map[string]string) ([]string, error)` — same return contract as before, a `--without` list.

- [ ] **Step 1: Write the failing test for the request den builds**

Replace the `promptWith` helper and `TestPromptStartingStateFollowsTheMode` / `TestPromptHeaderAlwaysNamesRequiredRepos` / `TestPromptAnnotatesUnmappedKeys` in `internal/spawn/interactive_test.go` with these. Keep `optionalRepos`, `promptHome` and `promptMappingPath` exactly as they are.

```go
// promptWith drives the checklist through a scripted Fake and hands back both
// the --without list and the request den built.
//
// checked is what the human leaves ticked — the Fake's answer. It replaces the
// old input string ("2\n\n"): the numbered protocol is gone, and a test now
// says which repos survive rather than which keys were typed.
func promptWith(t *testing.T, checked []string, prompts bool,
	mapping map[string]string) ([]string, prompt.MultiSelectRequest, error) {
	t.Helper()
	f := &prompt.Fake{MultiSelectAnswers: [][]string{checked}}
	without, err := promptOptionalRepos(f, promptMappingPath, "api",
		optionalRepos(), prompts, mapping)
	if len(f.MultiSelects) == 0 {
		return without, prompt.MultiSelectRequest{}, err
	}
	return without, f.MultiSelects[0], err
}

// Decision 9: a `select: prompt` checklist starts EMPTY, and confirming it
// as-is excludes every optional repo. The -i checklist keeps starting full —
// the two answer different questions, and both readings live in this one test.
//
// Since the move to the Prompter, "confirming as-is" is expressed as the
// answer the renderer would return for an untouched form: everything for -i,
// nothing for a prompting nest. Preselected is asserted alongside, so the test
// pins the STARTING STATE too and not only its consequence.
func TestPromptStartingStateFollowsTheMode(t *testing.T) {
	for _, c := range []struct {
		name        string
		prompts     bool
		asIs        []string
		wantWithout []string
	}{
		{"-i starts full", false, []string{"worker", "docs"}, nil},
		{"select: prompt starts empty", true, nil, []string{"worker", "docs"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			without, req, err := promptWith(t, c.asIs, c.prompts, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !slices.Equal(without, c.wantWithout) {
				t.Errorf("confirming as-is gave --without %v, want %v", without, c.wantWithout)
			}
			if req.Preselected == c.prompts {
				t.Errorf("prompts=%v must build Preselected=%v, got %v",
					c.prompts, !c.prompts, req.Preselected)
			}
		})
	}
}

// The header keeps its "required repos are always mounted" clause in BOTH
// modes: a select: prompt nest may declare required repos, and "none selected"
// alone would then be a lie.
//
// It is read off the REQUEST now, not off a rendered buffer — the renderer
// owns the pixels, den owns the words (spec §6).
func TestPromptHeaderAlwaysNamesRequiredRepos(t *testing.T) {
	for _, prompts := range []bool{true, false} {
		_, req, err := promptWith(t, nil, prompts, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(req.Title, "required repos are always mounted") {
			t.Errorf("prompts=%v: title lost its required-repos clause: %q", prompts, req.Title)
		}
	}
}

// Decision 10: an unmapped key is ANNOTATED, never hidden and never refused.
// Refusing the tick here would make the checklist a second judge of the repo
// mapping, whose single judge is resolveRepoKeys.
//
// The annotation names the FILE, path included: the bare "config.yaml" this
// line used to print was a remedy nobody could follow under DEN_HOME.
func TestPromptAnnotatesUnmappedKeys(t *testing.T) {
	repos := []nest.Repo{
		{Path: "/dev/backend"},
		{Key: "worker", Optional: true},
		{Key: "docs", Optional: true},
	}
	f := &prompt.Fake{MultiSelectAnswers: [][]string{nil}}
	if _, err := promptOptionalRepos(f, promptMappingPath, "api", repos, true,
		map[string]string{"worker": "/dev/worker"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var docs, worker prompt.Option
	for _, o := range f.MultiSelects[0].Options {
		switch o.Value {
		case "docs":
			docs = o
		case "worker":
			worker = o
		}
	}
	if !strings.Contains(docs.Description, "not mapped") {
		t.Errorf("an unmapped key must be annotated, got %q", docs.Description)
	}
	// config.GlobalPath, not filepath.Join here: that function is the sole
	// definition of where the file lives, and this assertion exists to keep the
	// message and the reader agreeing on ONE string.
	if !strings.Contains(docs.Description, config.GlobalPath(promptHome)) {
		t.Errorf("the annotation must name the mapping file: %q", docs.Description)
	}
	// A mapped key carries NO annotation: an annotation on every line annotates
	// nothing.
	if worker.Description != "" {
		t.Errorf("a mapped key must not be annotated, got %q", worker.Description)
	}
}
```

Add `"github.com/PillowPillow/den/internal/prompt"` to the file's imports, and drop `"bytes"` if nothing else in the file uses it.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/spawn/ -run TestPrompt -count=1`
Expected: FAIL — `too many arguments in call to promptOptionalRepos` / `undefined: prompt`.

- [ ] **Step 3: Rewrite `promptOptionalRepos` to build a request**

In `internal/spawn/interactive.go`, replace the whole `promptOptionalRepos` function (and delete `parseToggles` entirely) with:

```go
// promptOptionalRepos asks for a nest's OPTIONAL repos and returns the short
// names of the ones left unchecked — a `--without` list.
//
// Translating to the existing flag rather than building a second selection path
// is the whole design: nest.Resolve keeps applying the one rule it already
// owns (required repos always mounted, short names unique), and `-i` is just
// another way to fill its input. That is what makes "-i produces the same
// sandbox as the equivalent --without" true by construction rather than by
// coincidence — see TestInteractiveProducesTheSameArgvAsTheEquivalentWithout.
//
// prompts is the nest's MODE — `select: prompt` — and it is the one thing this
// function needs to know about it, because both of the things it decides follow
// from it and neither may disagree with the other.
//
// It decides the initial state of every box, which is NOT cosmetic. `-i` starts
// full, because confirming an -i checklist without touching it must produce
// exactly what `den up` alone produces. A `select: prompt` nest starts EMPTY,
// because it has no default selection to propose by definition — and thirty
// ticked boxes would turn an empty confirmation into a thirty-repo mount.
//
// It also decides which flags the title names, and that is the same fact read
// from the other end: a nest with no default selection is exactly the nest
// `--without` is refused on. Passed as ONE parameter rather than as a
// `startChecked` plus an equivalents string, because two parameters carrying one
// fact are two things to keep in agreement.
//
// mapping is the personal `repos:` of <denHome>/config.yaml, used to ANNOTATE
// the keys it does not carry — mappingPath is beside it because the annotation
// names that file, and the two must describe the same one (unmappedNote).
// Annotation only: ticking an unmapped key stays possible, and the refusal that
// follows is resolveRepoKeys', which names the key, the file and the clone URL.
//
// Required repos are neither listed nor offered (spec §6.2): they are always
// mounted, and offering them would let a human decline what den then mounts
// anyway.
//
// This function draws NOTHING. It builds a request and inverts the answer; the
// Prompter owns every byte on the terminal. That split is what lets the suite
// assert on what den ASKED without a tty ever existing (CLAUDE.md).
func promptOptionalRepos(p prompt.Prompter, mappingPath, nestName string, repos []nest.Repo,
	prompts bool, mapping map[string]string) ([]string, error) {
	// A nil Prompter is "no way to ask", never "assume the defaults": an
	// unwired double must refuse here rather than let the caller mount a
	// selection nobody made. Same rule as a nil IsTTY, one layer down.
	if p == nil {
		return nil, fmt.Errorf("-i: no prompter is wired — this is a den defect; %s",
			nonInteractiveEquivalents(prompts))
	}

	optional := make([]nest.Repo, 0, len(repos))
	for _, r := range repos {
		if r.Optional {
			optional = append(optional, r)
		}
	}

	selected := "none selected"
	if !prompts {
		selected = "all selected"
	}
	options := make([]prompt.Option, 0, len(optional))
	for _, r := range optional {
		options = append(options, prompt.Option{
			Value:       r.Name(),
			Label:       r.Name(),
			Description: unmappedNote(r, mapping, mappingPath),
		})
	}

	keep, err := p.MultiSelect(prompt.MultiSelectRequest{
		Title: fmt.Sprintf("nest %s: %d optional repo(s), %s — required repos are always mounted (%s)",
			nestName, len(optional), selected, nonInteractiveEquivalents(prompts)),
		Options:     options,
		Preselected: !prompts,
	})
	if err != nil {
		return nil, fmt.Errorf("-i: reading the selection: %w", err)
	}

	// The answer names what STAYS; den's flag names what goes. Inverting here,
	// against the offered list rather than against the nest's full repo list,
	// is what keeps a required repo out of `--without` even if a Prompter
	// echoed one back.
	var without []string
	for _, r := range optional {
		if !slices.Contains(keep, r.Name()) {
			without = append(without, r.Name())
		}
	}
	return without, nil
}
```

Update the file's imports: drop `"bufio"`, `"io"`, `"strconv"`, `"strings"`; add `"slices"` and `"github.com/PillowPillow/den/internal/prompt"`. Keep `"fmt"` and `"os"`.

- [ ] **Step 4: Point `interactiveWithout` at the Prompter**

In the same file, replace the tail of `interactiveWithout` — everything from `in := d.In` to the closing `return` — with:

```go
	return promptOptionalRepos(d.Prompt, mappingPath, n.Name, n.Repos, n.PromptsForRepos(), mapping)
```

and delete the `in := d.In` / `if in == nil` block above it. Also change the "nothing to ask" branch's writer usage nowhere — it still prints to `d.Out`, unchanged.

- [ ] **Step 5: Swap the field on `spawn.Deps`**

In `internal/spawn/spawn.go`, delete the `In io.Reader` field and its godoc (line 75 and the comment block above it), and add in its place:

```go
	// Prompt is how the `-i` checklist asks. Injected like every other side
	// effect of this package: the real one (internal/prompt/huhui) takes over
	// the terminal, so a suite that inherited it would fight the test runner
	// for stdin.
	//
	// Nil is NOT "use the default": promptOptionalRepos refuses on a nil
	// Prompter. The field it replaces was an io.Reader with an os.Stdin
	// fallback, and that fallback is deliberately gone — a checklist that can
	// silently reach for the process's real stdin is a checklist that can be
	// answered by a pipe nobody meant to point at it.
	Prompt prompt.Prompter
```

Add `"github.com/PillowPillow/den/internal/prompt"` to that file's imports; remove `"io"` only if nothing else in the file uses it (`Out`/`Err` are `io.Writer`, so it stays).

- [ ] **Step 6: Rewire `cli`**

In `internal/cli/up.go`, replace line 35:

```go
	d.In = cmd.InOrStdin()
```

with:

```go
	d.Prompt = deps.Prompt
```

In `internal/cli/root.go`, add to `Deps` (next to `IsTTY`):

```go
	// Prompt is den's single question-asking surface, shared by the `-i`
	// checklist, `den converge`'s confirmation, the repository-roots question
	// and every credential read. Injected for the reason ReadSecret was, which
	// this field absorbs: the real one puts the terminal in raw mode, and a
	// test that inherited it would do that to the suite's own stdin.
	Prompt prompt.Prompter
```

Add the import. Leave `ReadSecret` in place for now — Task 4 removes it.

- [ ] **Step 7: Port the 18 input sites**

In `internal/spawn/interactive_test.go`, every `d.In = strings.NewReader(...)` becomes a scripted Fake on `d.Prompt`. The translation is mechanical — the numbered line said which boxes to TOGGLE, the script says which repos stay CHECKED:

| Old input | Mode | New |
|---|---|---|
| `strings.NewReader("\n")` on `-i` | starts full | `&prompt.Fake{MultiSelectAnswers: [][]string{{"worker", "docs"}}}` — every optional repo of the fixture |
| `strings.NewReader("2\n\n")` on `-i` | untick "docs" | `&prompt.Fake{MultiSelectAnswers: [][]string{{"worker"}}}` |
| `strings.NewReader("\n")` on `select: prompt` | starts empty | `&prompt.Fake{MultiSelectAnswers: [][]string{nil}}` |
| `strings.NewReader("1\n\n")` on `select: prompt` | tick the 1st | `&prompt.Fake{MultiSelectAnswers: [][]string{{"api"}}}` — the 1st optional repo of THAT test's fixture |
| `strings.NewReader("1 2\n\n")` on `select: prompt` | tick the 1st and 2nd | `&prompt.Fake{MultiSelectAnswers: [][]string{{"api", "worker"}}}` |
| `strings.NewReader("")` (nothing is asked) | — | `&prompt.Fake{}` — an empty script, which REFUSES if anything asks. That is the assertion. |

Read each test's own fixture before translating: the repo names differ between `denTestOptional` and `denTestPromptingRepos`, and a number referred to a position in that test's list.

For `TestInteractiveProducesTheSameArgvAsTheEquivalentWithout` specifically, line 260 becomes:

```go
	interactiveDeps.Prompt = &prompt.Fake{MultiSelectAnswers: [][]string{{"worker"}}} // leave "docs" out
```

**Its argv comparison at lines 273-285 does not change.** If you find yourself editing an assertion about `Calls`, `Attaches`, a `--without` list, or a refusal message, STOP: invariant 1 or the terminal gate is broken, and the test is telling you so (spec §6, "signal d'arrêt").

- [ ] **Step 8: Run the spawn suite**

Run: `go test ./internal/spawn/ -count=1`
Expected: PASS. Every test in `interactive_test.go` passes with its behavioural assertions untouched.

- [ ] **Step 9: Run the full check**

Run: `task check`
Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add internal/spawn/ internal/cli/up.go internal/cli/root.go
git commit -m "feat(spawn): the -i checklist asks through the Prompter

promptOptionalRepos builds a request and inverts the answer; it draws nothing.
The numbered toggle protocol and parseToggles are gone.

spawn.Deps.In is deleted with them: one reader, one writer, and an io.Reader
left on Deps would invite a future caller to read stdin under the terminal
gate. A nil Prompter refuses rather than falling back to os.Stdin.

The 18 tests that scripted numbers now script a Fake. Their behavioural
assertions -- argv, --without lists, refusals -- are unchanged; that is what
proves no second selection path was born."
```

---

### Task 3: The `huhui` adapter and its closed gate

**Files:**
- Create: `internal/prompt/huhui/huhui.go`
- Test: `internal/prompt/huhui/huhui_test.go`
- Modify: `internal/cli/root.go` (`SystemDeps`)
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Consumes: `prompt.Prompter` and its request types (Task 1).
- Produces: `huhui.New() *huhui.Prompter`, satisfying `prompt.Prompter`; `huhui.Prompter{In, Out *os.File}` for tests that need to point it at a non-terminal.

- [ ] **Step 1: Add the dependency**

Run:

```bash
go get github.com/charmbracelet/huh@v1.0.0
```

Expected: 26 new modules in `go.mod`, all indirect except `huh`.

- [ ] **Step 2: Write the failing gate test**

Create `internal/prompt/huhui/huhui_test.go`:

```go
package huhui

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/PillowPillow/den/internal/prompt"
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
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/prompt/huhui/ -count=1`
Expected: FAIL — `undefined: Prompter`.

- [ ] **Step 4: Write the adapter**

Create `internal/prompt/huhui/huhui.go`:

```go
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
	err := huh.NewForm(huh.NewGroup(field)).
		WithInput(p.In).
		WithOutput(p.Out).
		Run()
	if errors.Is(err, huh.ErrUserAborted) {
		// ctrl+c is an answer, and the answer is no. Callers turn this into
		// their own "nothing was spawned" / "nothing was applied" refusal.
		return fmt.Errorf("cancelled")
	}
	return err
}

func (p *Prompter) MultiSelect(r prompt.MultiSelectRequest) ([]string, error) {
	if err := p.gate(); err != nil {
		return nil, err
	}
	var chosen []string
	options := make([]huh.Option[string], 0, len(r.Options))
	for _, o := range r.Options {
		options = append(options, huh.NewOption(o.Label, o.Value).Selected(r.Preselected))
	}
	field := huh.NewMultiSelect[string]().
		Title(r.Title).
		Options(options...).
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
	// the negative: den never defaults to yes on a plan (spec §7.1).
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
		EchoMode(huh.EchoModePassword).
		Value(&secret)
	if err := p.run(field); err != nil {
		return "", err
	}
	return secret, nil
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/prompt/huhui/ -count=1`
Expected: PASS (3 tests).

- [ ] **Step 6: Wire it into `SystemDeps`**

In `internal/cli/root.go`, add to the `SystemDeps()` literal, next to `IsTTY`:

```go
		Prompt: huhui.New(),
```

Add `"github.com/PillowPillow/den/internal/prompt/huhui"` to the imports. This is the single place in den that names the adapter — `cmd/den/main.go` calls `cli.Execute()` and builds no Deps, so threading a parameter through `Execute` and `NewRootCmd` would buy nothing.

- [ ] **Step 7: Measure the real binary**

Run:

```bash
task build
ls -l den
```

Expected: a binary larger than the 7 291 330 B recorded in the spec, and smaller than the 11 MB upper bound it states. Record the actual number — the spec's §3.b asks for exactly this measurement and labels its own figure an upper bound.

- [ ] **Step 8: Run the full check**

Run: `task check`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add go.mod go.sum internal/prompt/huhui/ internal/cli/root.go
git commit -m "feat(prompt): huh renders den's prompts, behind a closed gate

The only package in den that imports charmbracelet. Every method re-checks both
descriptors and returns ErrNoTerminal before building a form.

That second check is not belt and braces. Measured: given /dev/null, huh prints
its escapes into the pipe, returns the default selection with a nil error, and
the process then never exits. Callers already gate on IsTTY, but after this
change that gate is the only thing between a cron job and a microVM built from
a selection nobody made -- and a safety property that depends on every caller
remembering is not a safety property.

Inline, never alt-screen: the converge plan a human consents to stays on screen."
```

---

### Task 4: `confirm`, `askRepositoryRoots` and `ReadSecret` follow

**Files:**
- Modify: `internal/cli/answers.go` (`askRepositoryRoots:272`, `confirm:308`, `collectInitialAnswers:108-129`)
- Modify: `internal/cli/root.go` (delete `Deps.ReadSecret` and its `SystemDeps` entry)
- Test: `internal/cli/answers_test.go`, `internal/cli/converge_test.go`

**Interfaces:**
- Consumes: `cli.Deps.Prompt` (Task 2), `huhui.New()` (Task 3).
- Produces: `askRepositoryRoots(p prompt.Prompter) ([]string, error)`; `confirm(cmd *cobra.Command, d Deps, yes, changes bool) (bool, error)` — **signature unchanged**.

- [ ] **Step 1: Write the failing tests**

Add to `internal/cli/answers_test.go`:

```go
// The plan a human consents to is printed by the CALLER and must still be on
// screen when the question is asked (internal/converge/render.go: the trust
// boundary). The Prompter is handed a question, never the plan — a prompt that
// redrew or replaced it would be uninformed consent.
func TestConfirmAsksWithoutSwallowingThePrintedPlan(t *testing.T) {
	f := &prompt.Fake{ConfirmAnswers: []bool{true}}
	d := Deps{Prompt: f, IsTTY: func() bool { return true }}
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	ok, err := confirm(cmd, d, false, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("a scripted yes must confirm")
	}
	if len(f.Confirms) != 1 {
		t.Fatalf("exactly one question must be asked, got %d", len(f.Confirms))
	}
	if !strings.Contains(f.Confirms[0].Question, "apply this plan") {
		t.Errorf("the question must still name what is being applied: %q", f.Confirms[0].Question)
	}
}

// --yes and the no-terminal branch both answer WITHOUT asking. The gate stays
// above the Prompter: a run that cannot ask must not build a form (spec §5.2).
func TestConfirmNeverAsksWhenItDoesNotNeedTo(t *testing.T) {
	for _, c := range []struct {
		name    string
		d       Deps
		yes     bool
		changes bool
		want    bool
	}{
		{"--yes answers without asking", Deps{Prompt: &prompt.Fake{}}, true, true, true},
		{"no terminal refuses without asking",
			Deps{Prompt: &prompt.Fake{}, IsTTY: func() bool { return false }}, false, true, false},
		{"nothing to change needs no decision",
			Deps{Prompt: &prompt.Fake{}, IsTTY: func() bool { return true }}, false, false, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			var out bytes.Buffer
			cmd.SetOut(&out)
			got, err := confirm(cmd, c.d, c.yes, c.changes)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("confirm = %v, want %v", got, c.want)
			}
			// An empty Fake refuses when asked; reaching here proves nothing
			// asked. Asserted explicitly so the reason is legible.
			if f := c.d.Prompt.(*prompt.Fake); len(f.Confirms) != 0 {
				t.Errorf("no question may be asked on this path, got %d", len(f.Confirms))
			}
		})
	}
}

// askRepositoryRoots reads ONE line and keeps the splitting, the ~ expansion
// and the validation on den's side. The Prompter returns raw text: a prompter
// that knew what a path is would be a second judge of den's config.
func TestAskRepositoryRootsSplitsAndExpandsOnDensSide(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	f := &prompt.Fake{LineAnswers: []string{"~/dev  " + home + "/work"}}

	roots, err := askRepositoryRoots(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(roots) != 2 {
		t.Fatalf("one line carries several roots, got %v", roots)
	}
	if roots[0] != filepath.Join(home, "dev") {
		t.Errorf("~ must be expanded by den, got %q", roots[0])
	}
	if len(f.Lines) != 1 {
		t.Fatalf("exactly one line must be read, got %d", len(f.Lines))
	}
	if !strings.Contains(f.Lines[0].Question, "never clones") {
		t.Errorf("the question must still say den only looks: %q", f.Lines[0].Question)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/cli/ -run 'TestConfirm|TestAskRepositoryRoots' -count=1`
Expected: FAIL — `too many arguments in call to askRepositoryRoots`, `unknown field Prompt`.

- [ ] **Step 3: Port `askRepositoryRoots`**

In `internal/cli/answers.go`, replace the function with:

```go
// askRepositoryRoots reads the directories to scan. They are answers to ONE
// execution: den never stores them (spec §7.2), which is also why the question
// says what they are for rather than presenting them as a setting.
//
// The Prompter returns the line RAW. Splitting on whitespace, expanding `~` and
// validating each entry stay here, exactly where they were: a Prompter that
// knew what a path is would be a second judge of den's config, and there is one
// judge (config.ExpandPath).
func askRepositoryRoots(p prompt.Prompter) ([]string, error) {
	line, err := p.Line(prompt.LineRequest{
		Question: "Where do your working repositories live? (space-separated directories, " +
			"empty line to skip — den only looks, it never clones)",
	})
	if err != nil {
		return nil, fmt.Errorf("reading the repository roots: %w", err)
	}
	var roots []string
	for _, field := range strings.Fields(line) {
		expanded, err := config.ExpandPath(field)
		if err != nil {
			return nil, err
		}
		roots = append(roots, expanded)
	}
	return roots, nil
}
```

Keep everything below `roots = append(...)` as it is in the current file — copy the existing loop body verbatim rather than retyping it, so the error wording does not drift.

- [ ] **Step 4: Port `confirm`**

Replace the tail of `confirm` — from `fmt.Fprint(cmd.OutOrStdout(), "\napply this plan? [y/N] ")` to the end — with:

```go
	// The plan is ALREADY on screen, printed by the caller. The question names
	// what it applies to and nothing else: internal/converge/render.go calls
	// that plan the trust boundary, and a prompt that redrew it — or that took
	// the screen and scrolled it away — would turn consent into a guess.
	ok, err := d.Prompt.Confirm(prompt.ConfirmRequest{Question: "apply this plan?"})
	if err != nil {
		return false, fmt.Errorf("reading the confirmation: %w", err)
	}
	if !ok {
		fmt.Fprintln(cmd.OutOrStdout(), "nothing was applied")
	}
	return ok, nil
}
```

The three early returns above it — `yes`, the `IsTTY` refusal, and `!changes` — do not move. They are the gate, and they stay above the Prompter.

- [ ] **Step 5: Port the credential read**

In `collectInitialAnswers`, replace the `d.ReadSecret == nil` guard and the `d.ReadSecret(prompt + ": ")` call with:

```go
		if d.Prompt == nil {
			return converge.Answers{}, fmt.Errorf(
				"credential %q must be typed, and no prompter is wired — this is a den defect; "+
					"pass `--answers <file>` with `from_env:` as a workaround", name)
		}
		// Never echoed, and never carried in a flag: an argv is visible to
		// every process on the machine (spec §5.3).
		value, err := d.Prompt.Secret(prompt.SecretRequest{Prompt: label})
```

Rename the local variable `prompt` (it shadows the new package) to `label` throughout that loop — the two lines above the call read `label := m.Inputs.Credentials[name].Prompt` and `if strings.TrimSpace(label) == "" { label = name }`.

Then delete `in := bufio.NewReader(cmd.InOrStdin())` and pass `d.Prompt` to `askRepositoryRoots`; drop `"bufio"` from the imports if nothing else uses it.

- [ ] **Step 6: Delete `ReadSecret`**

In `internal/cli/root.go`, delete the `ReadSecret` field with its godoc, and the `ReadSecret:` entry in `SystemDeps` including its `os.Stdin` comment. Remove `"golang.org/x/term"` from that file's imports if `term` is now unused there.

- [ ] **Step 7: Fix the tests that injected a secret recorder**

Run `grep -rn "ReadSecret" internal/cli/` and replace each test's recorder with `&prompt.Fake{SecretAnswers: []string{"..."}}` on `Deps.Prompt`. Assertions on what was read do not change; assertions on the prompt string move to `f.Secrets[0].Prompt`.

- [ ] **Step 8: Run the cli suite**

Run: `go test ./internal/cli/ -count=1`
Expected: PASS.

- [ ] **Step 9: Run the full check**

Run: `task check`
Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add internal/cli/
git commit -m "feat(cli): converge's three prompts go through the Prompter

confirm, askRepositoryRoots and the credential read all ask through one
interface. Deps.ReadSecret is absorbed into Prompt.Secret -- it was already
this shape, and its godoc already carried the reason.

The gates do not move: --yes, the no-terminal refusal and the nothing-to-change
shortcut all answer before any form is built. askRepositoryRoots still splits
and expands on den's side; a prompter that knew what a path is would be a
second judge of den's config."
```

---

### Task 5: The import guard

**Files:**
- Create: `internal/prompt/hermeticity_test.go`

**Interfaces:**
- Consumes: the package layout produced by Tasks 1-4.
- Produces: nothing consumed by later tasks.

- [ ] **Step 1: Write the guard**

Create `internal/prompt/hermeticity_test.go`, modelled on `internal/ports/hermeticity_test.go` — read that file first and follow its `moduleRoot` helper and its error wording:

```go
package prompt

// This guard holds den's dependency shape for the huh work, and it asserts TWO
// things because either alone is worthless:
//
//  1. Only internal/prompt/huhui imports github.com/charmbracelet/*.
//  2. Only internal/cli imports internal/prompt/huhui.
//
// Without (2), internal/spawn could import the adapter directly and the whole
// reason internal/prompt exists as a leaf — keeping the checklist's package
// free of a 26-module graph — would be an aspiration no test defends. The
// promise in the design is "one package in den knows the name huh"; (1) alone
// promises only "one package says it out loud".
//
// SYNTAX ANALYSIS (go/build + go/parser), not a shell-out to `go list`, and the
// same documented limit as internal/ports/hermeticity_test.go: build.ImportDir
// applies THIS machine's GOOS/GOARCH, so a platform-restricted file would be
// invisible to this guard when run elsewhere.
```

Copy `moduleRoot`, `modulePath` and `importsOfDir` from `internal/ports/hermeticity_test.go` verbatim into this file (they are unexported helpers of a different package, so they cannot be shared; the ports file's own comments explain each one and travel with them). Then add:

```go
// huhuiPackage is the one package allowed to name the library, and cliPackage
// the one allowed to name huhui. Written as import paths relative to the
// module, resolved against modulePath so this file never hardcodes
// "github.com/PillowPillow/den".
const (
	huhuiPackage = "internal/prompt/huhui"
	cliPackage   = "internal/cli"
	libraryPrefix = "github.com/charmbracelet/"
)

func TestOnlyTheAdapterKnowsTheLibrary(t *testing.T) {
	root := moduleRoot(t)
	module := modulePath(t, root)
	adapterImport := module + "/" + huhuiPackage

	var scanned int
	for _, top := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				return nil
			}
			// testdata holds fixtures, not den's own packages, and some of
			// them are deliberately malformed.
			if d.Name() == "testdata" {
				return fs.SkipDir
			}
			imports, files := importsOfDir(t, path)
			if files == 0 {
				return nil
			}
			scanned++
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				t.Fatalf("relativizing %s: %v", path, relErr)
			}
			pkg := filepath.ToSlash(rel)
			for _, imp := range imports {
				if strings.HasPrefix(imp, libraryPrefix) && pkg != huhuiPackage {
					t.Errorf("%s imports %s: only %s may name the TUI library — "+
						"speak to prompt.Prompter instead, and let %s render",
						pkg, imp, huhuiPackage, huhuiPackage)
				}
				if imp == adapterImport && pkg != cliPackage {
					t.Errorf("%s imports %s: only %s wires the adapter — "+
						"importing it here drags 26 modules into a package that "+
						"only needs the prompt.Prompter interface",
						pkg, imp, cliPackage)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", top, err)
		}
	}
	// Guard on the guard: a walk that parsed nothing would make both
	// assertions above vacuously true.
	if scanned < 10 {
		t.Fatalf("only %d packages scanned — the walk is not finding den's tree", scanned)
	}
}
```

Add `"io/fs"`, `"path/filepath"`, `"strings"` and `"testing"` to the imports, plus the ones the copied helpers need (`"errors"`, `"go/build"`, `"go/parser"`, `"go/token"`, `"os"`, `"strconv"`).

- [ ] **Step 2: Run it to verify it passes**

Run: `go test ./internal/prompt/ -run TestOnly -count=1`
Expected: PASS.

- [ ] **Step 3: Verify the guard actually catches a violation**

Temporarily add `_ "github.com/charmbracelet/huh"` to `internal/spawn/interactive.go`, then run the guard.

Run: `go test ./internal/prompt/ -run TestOnly -count=1`
Expected: FAIL, naming `internal/spawn` and `github.com/charmbracelet/huh`.

Then remove the temporary import and re-run to confirm PASS. A guard nobody has seen fail is a guard nobody has tested.

- [ ] **Step 4: Run the full check**

Run: `task check`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/prompt/hermeticity_test.go
git commit -m "test(prompt): pin the adapter behind one door

Two assertions: only huhui imports charmbracelet, and only cli imports huhui.
The second is what keeps internal/spawn free of the 26-module graph -- without
it, the leaf package's whole reason for existing is defended by nothing.

Verified by breaking it: an import added to internal/spawn fails the guard by
name."
```

---

### Task 6: Correct the two false claims the code carries

Documentation only. It closes the loop the spec's §2 opens: the interdiction that blocked this work cited a section that does not say it, and a ban on a dependency the module already requires.

**Files:**
- Modify: `internal/spawn/interactive.go` (the `promptOptionalRepos` godoc's closing paragraph — the "bufio.Scanner, no TUI library" claim, if any trace of it survives Task 2)
- Modify: `internal/spawn/isterminal_darwin.go:18-20`
- Modify: `internal/spawn/isterminal_linux.go` (the same sentence)
- Modify: `docs/superpowers/specs/2026-08-11-nest-instances-design.md` (decision 4)

- [ ] **Step 1: Amend decision 4 of the 2026-08-11 spec**

In `docs/superpowers/specs/2026-08-11-nest-instances-design.md`, append to decision 4 (after "hors scope ici, à chiffrer sur mesure réelle."), in French, matching that document's voice:

```markdown
   **Renversée le 2026-08-18** par `2026-08-18-rich-prompts-design.md`. La mesure réelle que cette
   décision attendait est tombée : ce n'est pas la longueur de la liste qui gênait, c'est le geste.
   Et son fondement écrit était faux — HANDOFF §8 ne porte aucune revendication de dépendances, et
   `golang.org/x/term`, nommé ici comme exclu, est un `require` direct de `go.mod` depuis
   l'onboarding des sources. Les quatre invites de den passent à `huh`.
```

- [ ] **Step 2: Correct the `isterminal` claim on both platforms**

In `internal/spawn/isterminal_darwin.go`, replace:

```go
// on 2026-08-14, because there is no fourth option: this module allows stdlib +
// cobra + yaml.v3 only, which rules out `golang.org/x/term` and
// `golang.org/x/sys`, and no stdlib route to a real terminal test avoids the
// ioctl.
```

with:

```go
// on 2026-08-14, on a premise that was ALREADY FALSE when it was written: the
// original text said "this module allows stdlib + cobra + yaml.v3 only, which
// rules out `golang.org/x/term` and `golang.org/x/sys`". `golang.org/x/term`
// was a direct require of go.mod at the time, imported by internal/cli/root.go
// for the credential read, and internal/ports/hermeticity_test.go listed it.
//
// So the three firsts bought something a dependency already in the tree offered
// (`term.IsTerminal`). This file is kept anyway, and the correction is written
// here rather than acted on: replacing a measured mechanism on two platforms is
// its own change, with its own measurement to redo, and the 2026-08-18 rich-
// prompts work names it as a separate ticket rather than smuggling it in.
```

Apply the same correction to the corresponding sentence in `internal/spawn/isterminal_linux.go`.

- [ ] **Step 3: Verify no stale TUI claim survives in `interactive.go`**

Run: `grep -n "bufio\|TUI\|HANDOFF" internal/spawn/interactive.go`
Expected: no match. Task 2 rewrote that godoc; if a sentence survived, delete it now — it is the false citation the whole spec §2 is about.

- [ ] **Step 4: Run the full check**

Run: `task check`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/spawn/ docs/superpowers/specs/2026-08-11-nest-instances-design.md
git commit -m "docs: retire the interdiction this work disproved

Decision 4 of the nest-instances spec is amended in place, and the isterminal
files stop claiming golang.org/x/term is banned -- it was a direct require of
go.mod on the day that sentence was written, imported by internal/cli/root.go.

The ioctl those files carry is left alone on purpose: replacing a measured
mechanism on two platforms is its own change with its own measurement, named as
a separate ticket rather than smuggled into this one."
```

---

## Verification

After Task 6, confirm the whole thing end to end:

- [ ] `task check` passes.
- [ ] `go test ./... -count=1` passes.
- [ ] `grep -rn "charmbracelet" internal/ cmd/ --include="*.go"` names only `internal/prompt/huhui/`.
- [ ] `grep -rn "parseToggles\|ReadSecret\|Deps.In" internal/ --include="*.go"` returns nothing.
- [ ] `./den up <a nest with optional repos> -i` in a real terminal draws a toggle list; arrow, space, enter work; the resulting sandbox matches `--without` for the same choice.
- [ ] `./den up <nest> -i < /dev/null` refuses, naming `--only`/`--without`, and **exits** — it does not hang.
- [ ] `./den converge` prints its plan, and the plan is still on screen while the confirmation is drawn.
- [ ] The binary size measured at Task 3 Step 7 is recorded in the spec's §3.b, replacing the upper bound.
