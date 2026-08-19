package spawn

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/manifest"
	"github.com/PillowPillow/den/internal/nest"
	"github.com/PillowPillow/den/internal/prompt"
	"github.com/PillowPillow/den/internal/sbx"
)

// optionalRepos is the fixture of the checklist: one required repo the user
// cannot decline, and two optional ones they can.
func optionalRepos() []nest.Repo {
	// The required repo is named "backend", NOT "api": the nest is called
	// "api", and a required repo sharing that name would make every assertion
	// on the rendered checklist match the header line instead — an assertion
	// that can never fail.
	return []nest.Repo{
		{Path: "/dev/backend"},
		{Path: "/dev/worker", Optional: true},
		{Path: "/dev/docs", Optional: true},
	}
}

// runChecklist drives the -i checklist (boxes start full) with a scripted
// answer, and hands back the --without list plus the request den built.
//
// checked is what the human leaves ticked. It replaces the old input string:
// the numbered protocol is gone, so a test says which repos survive rather
// than which keys were typed.
func runChecklist(t *testing.T, checked []string) ([]string, prompt.MultiSelectRequest, error) {
	t.Helper()
	return promptWith(t, checked, false, nil)
}

// promptHome is the den home these unit-level checklist tests render under. A
// path that is NOT the default home on any machine: the annotation under test
// names the mapping file, and a fixture that happened to match the real home
// would let a regression to the bare filename pass unnoticed.
//
// promptMappingPath is what the checklist now RECEIVES: since a manifested
// source resolves its keys through source-config/<name>.yaml, the file to name
// is the caller's answer, not a path this layer derives (see
// interactiveWithout). Spawn passes config.GlobalPath for a local nest, which
// is what these tests reproduce.
const promptHome = "/fixture/den-home"

var promptMappingPath = config.GlobalPath(promptHome)

// promptWith drives the checklist through a scripted Fake and hands back both
// the --without list and the request den built.
//
// checked is what the human leaves ticked — the Fake's answer. It replaces the
// old input string ("2\n\n"): the numbered protocol is gone, and a test now
// says which repos survive rather than which keys were typed.
//
// prompts is the nest's `select: prompt` mode, the single parameter
// promptOptionalRepos takes: false is `-i` (boxes start full, both flags
// offered), true is a prompting nest (boxes start empty, `--only` alone).
func promptWith(t *testing.T, checked []string, prompts bool,
	mapping map[string]string) ([]string, prompt.MultiSelectRequest, error) {
	t.Helper()
	f := &prompt.Fake{MultiSelectAnswers: [][]string{checked}}
	without, err := promptOptionalRepos(context.Background(), f, promptMappingPath, "api",
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
	if _, err := promptOptionalRepos(context.Background(), f, promptMappingPath, "api", repos, true,
		map[string]string{"worker": "/dev/worker"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The same guard its four siblings carry: an index panic IS a failure, but
	// it names the line rather than the property, and the reader then has to
	// work out that the checklist never opened.
	if len(f.MultiSelects) != 1 {
		t.Fatalf("the checklist must have been opened exactly once, got %d", len(f.MultiSelects))
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

func TestPromptExcludesEverythingUnchecked(t *testing.T) {
	without, _, err := runChecklist(t, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Equal(without, []string{"worker", "docs"}) {
		t.Errorf("got %v, want [worker docs]", without)
	}
}

func TestPromptExcludesOnlyWhatWasUnchecked(t *testing.T) {
	without, _, err := runChecklist(t, []string{"worker"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Equal(without, []string{"docs"}) {
		t.Errorf("got %v, want [docs]", without)
	}
}

// A Prompter that cannot answer is a REFUSAL, never a confirmation — and the
// refusal names the flag that makes the same selection without asking.
//
// This test is the descendant of the EOF-before-confirmation case, and it
// guards the same property under a new mechanism. It matters more now, not
// less: the bufio reader refused on EOF by itself, whereas huh hands back the
// default selection with a nil error and then never lets the process exit
// (spec §3.d). den's fail-closed behaviour is no longer inherited from the
// reader — it is this code, and this test.
func TestPromptRefusesWhenThePrompterCannotAnswer(t *testing.T) {
	f := &prompt.Fake{Err: errors.New("no terminal")}
	_, err := promptOptionalRepos(context.Background(), f, promptMappingPath, "api", optionalRepos(), false, nil)
	if err == nil {
		t.Fatal("a prompter error must be an error, never a silent confirmation")
	}
	if !strings.Contains(err.Error(), "--only") || !strings.Contains(err.Error(), "--without") {
		t.Errorf("the refusal must name the non-interactive equivalents: %v", err)
	}
}

// A nil Prompter is "no way to ask", never "take the defaults". An unwired
// double must refuse here rather than let a caller mount a selection nobody
// made.
//
// The message is held to the same clause as its error-path sibling above: this
// refusal is the one a user meets while den is between wiring sites (nothing
// fills spawn.Deps.Prompt until the real renderer lands), so it is the last
// place that can hand over a command which works.
func TestPromptRefusesANilPrompter(t *testing.T) {
	_, err := promptOptionalRepos(context.Background(), nil, promptMappingPath, "api", optionalRepos(), false, nil)
	if err == nil {
		t.Fatal("a nil prompter must refuse")
	}
	if !strings.Contains(err.Error(), "--only") || !strings.Contains(err.Error(), "--without") {
		t.Errorf("the refusal must name the non-interactive equivalents: %v", err)
	}
}

// Required repos are always mounted (spec §6.2): they are not offered at all,
// so no option carries the required repo's name — and the answer inverts
// against the OFFERED list alone.
func TestPromptOffersOptionalReposOnly(t *testing.T) {
	without, req, err := runChecklist(t, []string{"docs"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, o := range req.Options {
		if o.Value == "backend" || o.Label == "backend" {
			t.Errorf("a required repo must not appear in the checklist: %+v", req.Options)
		}
	}
	if !slices.Equal(without, []string{"worker"}) {
		t.Errorf("got %v, want [worker]: only the offered repos may be inverted", without)
	}
}

// denTestOptional is denTest with two optional repos added to the nest — the
// shape `-i` exists for.
func denTestOptional(t *testing.T) (denHome string, repos map[string]string) {
	t.Helper()
	denHome, api := denTest(t)
	repos = map[string]string{"api": api}
	root := t.TempDir()
	for _, name := range []string{"worker", "docs"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		repos[name] = path
	}
	write(t, filepath.Join(denHome, "nests", "api.yaml"),
		"stack: devx\nrepos:\n"+
			"  - { path: "+repos["api"]+" }\n"+
			"  - { path: "+repos["worker"]+", optional: true }\n"+
			"  - { path: "+repos["docs"]+", optional: true }\n")
	return denHome, repos
}

// THE acceptance criterion: the interactive path and the flag path are the
// same path. Compared on the rendered argv, not by eye — two selections that
// merely LOOK equivalent are exactly what this test exists to catch.
func TestInteractiveProducesTheSameArgvAsTheEquivalentWithout(t *testing.T) {
	denHome, _ := denTestOptional(t)

	interactiveFake, interactiveDeps := fakeDeps()
	interactiveDeps.Prompt = &prompt.Fake{MultiSelectAnswers: [][]string{{"worker"}}} // leave "docs" out
	interactiveDeps.IsTTY = func() bool { return true }
	if err := Spawn(context.Background(), denHome,
		Options{Nest: "api", Interactive: true}, interactiveDeps); err != nil {
		t.Fatalf("interactive spawn: %v", err)
	}

	flagFake, flagDeps := fakeDeps()
	if err := Spawn(context.Background(), denHome,
		Options{Nest: "api", Without: []string{"docs"}}, flagDeps); err != nil {
		t.Fatalf("--without spawn: %v", err)
	}

	if !slices.EqualFunc(interactiveFake.Calls, flagFake.Calls, slices.Equal) {
		t.Errorf("-i and the equivalent --without must produce the SAME sbx calls\n-i:      %v\n--without: %v",
			interactiveFake.Calls, flagFake.Calls)
	}
	if !slices.EqualFunc(interactiveFake.Attaches, flagFake.Attaches, slices.Equal) {
		t.Errorf("-i and the equivalent --without must attach identically\n-i:      %v\n--without: %v",
			interactiveFake.Attaches, flagFake.Attaches)
	}
	// Guard on the guard: an assertion comparing two empty lists would pass
	// whatever the selection did.
	if !interactiveFake.HasCalled("create") {
		t.Fatalf("no create to compare; calls: %v", interactiveFake.Calls)
	}
}

// -i and --only/--without contradict each other: refuse, naming both. The repo
// refuses rather than normalizing in silence (spec §2), and the two other
// readings — flags as the initial state, or flags winning — are both things a
// user can misread.
func TestSpawnRefusesInteractiveWithASelectionFlag(t *testing.T) {
	for _, c := range []struct {
		name string
		o    Options
	}{
		{"--without", Options{Nest: "api", Interactive: true, Without: []string{"docs"}}},
		{"--only", Options{Nest: "api", Interactive: true, Only: []string{"docs"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			denHome, _ := denTestOptional(t)
			f, d := fakeDeps()
			// An empty script REFUSES: the contradiction is caught before
			// anything asks.
			d.Prompt = &prompt.Fake{}
			d.IsTTY = func() bool { return true }

			err := Spawn(context.Background(), denHome, c.o, d)
			if err == nil {
				t.Fatal("-i together with a selection flag must be refused")
			}
			if !strings.Contains(err.Error(), "-i") || !strings.Contains(err.Error(), c.name) {
				t.Errorf("the refusal must name both flags in play: %v", err)
			}
			if len(f.Calls) != 0 {
				t.Errorf("no sbx call may precede the refusal: %v", f.Calls)
			}
		})
	}
}

// No terminal (pipe, CI, scripted --detach): fail cleanly, naming the
// non-interactive equivalents. Never block on a read that will not come.
func TestSpawnRefusesInteractiveWithoutATerminal(t *testing.T) {
	denHome, _ := denTestOptional(t)
	f, d := fakeDeps()
	d.Prompt = &prompt.Fake{} // an empty script REFUSES: with no terminal nothing may ask
	d.IsTTY = func() bool { return false }

	err := Spawn(context.Background(), denHome, Options{Nest: "api", Interactive: true}, d)
	if err == nil {
		t.Fatal("-i without a terminal must fail rather than read a stream nobody types into")
	}
	if !strings.Contains(err.Error(), "--only") || !strings.Contains(err.Error(), "--without") {
		t.Errorf("the refusal must name the non-interactive equivalents: %v", err)
	}
	// The liveness listing precedes this refusal now — the checklist only opens
	// once den knows there is no sandbox to attach to. It creates nothing, which
	// is what this assertion has always been about.
	if !createdNothing(f) {
		t.Errorf("the refusal must create nothing: %v", f.Calls)
	}
}

// A nil IsTTY is "no terminal", not "assume one": a caller that forgot to wire
// the probe must get the clean refusal, not a spawn hanging on a read.
func TestSpawnTreatsAnUnwiredTerminalProbeAsNoTerminal(t *testing.T) {
	denHome, _ := denTestOptional(t)
	_, d := fakeDeps()
	d.Prompt = &prompt.Fake{} // an empty script REFUSES: an unwired probe must ask nothing

	if err := Spawn(context.Background(), denHome,
		Options{Nest: "api", Interactive: true}, d); err == nil {
		t.Fatal("an unwired IsTTY must refuse -i, not assume a terminal")
	}
}

// Nothing to ask: say so and carry on. An empty checklist would leave the user
// staring at a prompt with no lines, wondering what to type.
func TestSpawnContinuesWhenTheNestHasNoOptionalRepo(t *testing.T) {
	denHome, _ := denTest(t) // its single repo is required
	f, d := fakeDeps()
	var out bytes.Buffer
	d.Out = &out
	// An empty script REFUSES if asked. It cannot tell "nothing was asked"
	// apart from "the terminal gate refused first" on its own — the create
	// asserted below is what discriminates, since a refusal creates nothing.
	d.Prompt = &prompt.Fake{}
	d.IsTTY = func() bool { return false }

	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Interactive: true}, d); err != nil {
		t.Fatalf("a nest with no optional repo has nothing to ask: %v", err)
	}
	if !strings.Contains(out.String(), "optional") {
		t.Errorf("den must say why it asked nothing:\n%s", out.String())
	}
	if !f.HasCalled("create") {
		t.Errorf("the spawn must go on; calls: %v", f.Calls)
	}
}

// The checklist asks through the INJECTED Prompter — never through os.Stdin,
// which no test can supply and which a nil Prompter must not fall back on.
//
// The list it offers is read off the recorded request rather than off a
// buffer: den no longer draws it, and what it puts in the request is what a
// renderer has to show. The length guard comes first because a checklist that
// never opened records nothing, and an assertion on nothing passes.
func TestSpawnPromptsThroughTheInjectedPrompter(t *testing.T) {
	denHome, _ := denTestOptional(t)
	_, d := fakeDeps()
	p := &prompt.Fake{MultiSelectAnswers: [][]string{{"worker", "docs"}}}
	d.Prompt = p
	d.IsTTY = func() bool { return true }

	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Interactive: true}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(p.MultiSelects) != 1 {
		t.Fatalf("the checklist must have been opened exactly once, got %d", len(p.MultiSelects))
	}
	var offered []string
	for _, o := range p.MultiSelects[0].Options {
		offered = append(offered, o.Value)
	}
	if !slices.Equal(offered, []string{"worker", "docs"}) {
		t.Errorf("the checklist must offer the optional repos, got %v", offered)
	}
}

// denTestPrompting is denTestOptional's nest, with `select: prompt` and every
// repo optional — the generic nest of the spec, in miniature.
func denTestPrompting(t *testing.T) string {
	t.Helper()
	denHome, repos := denTestOptional(t)
	write(t, filepath.Join(denHome, "nests", "generic.yaml"),
		"stack: devx\nselect: prompt\nrepos:\n"+
			"  - { path: "+repos["api"]+", optional: true }\n"+
			"  - { path: "+repos["worker"]+", optional: true }\n"+
			"  - { path: "+repos["docs"]+", optional: true }\n")
	return denHome
}

// THE acceptance criterion of the mode, and the same one -i already carries:
// the checklist is a source of input placed in front of nest.Resolve, never a
// second selection rule. Compared on the rendered argv — two selections that
// merely LOOK equivalent are exactly what this test exists to catch.
func TestPromptModeProducesTheSameArgvAsTheEquivalentOnly(t *testing.T) {
	denHome := denTestPrompting(t)

	promptFake, promptDeps := fakeDeps()
	promptDeps.Prompt = &prompt.Fake{MultiSelectAnswers: [][]string{{"api", "worker"}}} // tick api and worker
	promptDeps.IsTTY = func() bool { return true }
	if err := Spawn(context.Background(), denHome, Options{Nest: "generic"}, promptDeps); err != nil {
		t.Fatalf("prompting spawn: %v", err)
	}

	flagFake, flagDeps := fakeDeps()
	if err := Spawn(context.Background(), denHome,
		Options{Nest: "generic", Only: []string{"api", "worker"}}, flagDeps); err != nil {
		t.Fatalf("--only spawn: %v", err)
	}

	if !slices.EqualFunc(promptFake.Calls, flagFake.Calls, slices.Equal) {
		t.Errorf("select: prompt and the equivalent --only must produce the SAME sbx calls\nprompt: %v\n--only: %v",
			promptFake.Calls, flagFake.Calls)
	}
	if !promptFake.HasCalled("create") {
		t.Fatalf("no create to compare; calls: %v", promptFake.Calls)
	}
}

// A prompt cannot be literally mandatory: spawn already refuses -i without a
// terminal, and den exec exists for pipes and CI. The refusal names the
// non-interactive form, in the same breath.
func TestPromptModeRefusesWithoutATerminal(t *testing.T) {
	denHome := denTestPrompting(t)
	f, d := fakeDeps()
	d.IsTTY = func() bool { return false }

	err := Spawn(context.Background(), denHome, Options{Nest: "generic"}, d)
	if err == nil {
		t.Fatal("a prompting nest with no terminal and no --only must be refused")
	}
	if !strings.Contains(err.Error(), "--only") {
		t.Errorf("the refusal must name the non-interactive form, got: %v", err)
	}
	if f.HasCalled("create") {
		t.Errorf("refused, yet something was created: %v", f.Calls)
	}
}

// Every message that offers a way OUT of the checklist must name a command that
// works on the nest it is printed for. `--without` is refused on a `select:
// prompt` nest — there is no default selection to subtract from — so naming it
// there sends the user to den's own refusal, in the one kind of sentence that is
// followed rather than read.
//
// Both modes are asserted in one test because what must hold is the DIFFERENCE:
// on an ordinary nest both flags work and both are still named, and a
// "simplification" that drops the mode would silence one half or the other with
// every existing test still passing.
func TestTheOfferedFlagsFollowTheNestMode(t *testing.T) {
	// The checklist's own offer, read off the request's title — the words den
	// hands the renderer, which is all den still owns of that message.
	_, prompting, err := promptWith(t, nil, true, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(prompting.Title, "--only") {
		t.Errorf("a prompting checklist must name the flag that works on it: %q", prompting.Title)
	}
	if strings.Contains(prompting.Title, "--without") {
		t.Errorf("a prompting checklist must not offer a flag den refuses on that nest: %q", prompting.Title)
	}
	_, interactive, err := promptWith(t, []string{"worker", "docs"}, false, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(interactive.Title, "--only") || !strings.Contains(interactive.Title, "--without") {
		t.Errorf("-i keeps both equivalents: they both work on an ordinary nest: %q", interactive.Title)
	}

	// The no-terminal refusal, which is the message a pipe or a CI job meets
	// INSTEAD of that footer — same rule, other surface.
	denHome := denTestPrompting(t)
	_, d := fakeDeps()
	d.IsTTY = func() bool { return false }
	err = Spawn(context.Background(), denHome, Options{Nest: "generic"}, d)
	if err == nil {
		t.Fatal("a prompting nest with no terminal and no --only must be refused")
	}
	if strings.Contains(err.Error(), "--without") {
		t.Errorf("the refusal must not name a flag den refuses on this nest: %v", err)
	}
}

// --only answers the question, so there is nothing left to ask: no terminal is
// needed and none is probed. This is what makes the mode usable from `den
// exec`, a script and CI.
func TestPromptModeWithOnlyNeedsNoTerminal(t *testing.T) {
	denHome := denTestPrompting(t)
	f, d := fakeDeps()
	d.IsTTY = func() bool { return false }

	if err := Spawn(context.Background(), denHome,
		Options{Nest: "generic", Only: []string{"api"}}, d); err != nil {
		t.Fatalf("--only on a prompting nest must not need a terminal: %v", err)
	}
	if !f.HasCalled("create") {
		t.Errorf("nothing was created: %v", f.Calls)
	}
}

// -i on a prompting nest is REDUNDANT, not contradictory: it asks for the
// checklist the nest opens anyway. Accepted, and identical.
func TestPromptModeAcceptsRedundantInteractiveFlag(t *testing.T) {
	denHome := denTestPrompting(t)

	bare, bareDeps := fakeDeps()
	bareDeps.Prompt = &prompt.Fake{MultiSelectAnswers: [][]string{{"api"}}}
	bareDeps.IsTTY = func() bool { return true }
	if err := Spawn(context.Background(), denHome, Options{Nest: "generic"}, bareDeps); err != nil {
		t.Fatalf("bare spawn: %v", err)
	}

	withI, withIDeps := fakeDeps()
	withIDeps.Prompt = &prompt.Fake{MultiSelectAnswers: [][]string{{"api"}}}
	withIDeps.IsTTY = func() bool { return true }
	if err := Spawn(context.Background(), denHome,
		Options{Nest: "generic", Interactive: true}, withIDeps); err != nil {
		t.Fatalf("-i spawn: %v", err)
	}

	if !slices.EqualFunc(bare.Calls, withI.Calls, slices.Equal) {
		t.Errorf("-i on a prompting nest must change nothing\nbare: %v\n-i:   %v", bare.Calls, withI.Calls)
	}
}

// Decision 6: on a live sandbox, no prompt at all. Asking someone to pick
// repos that cannot be mounted is the silence §2 forbids — and the question
// would be put to somebody with no way to guess it is pointless.
//
// The Prompter FAILS if asked: an assertion on the recorded request — or on
// the rendered output, when the checklist still drew one — would pass on a
// prompt that was opened and then ignored.
//
// All four methods, though only MultiSelect is reachable from spawn: the
// interface is the contract, and a method left to answer would be a hole the
// day another question moves into this package.
type failingPrompter struct{ t *testing.T }

func (p failingPrompter) refuse() {
	p.t.Fatal("the checklist was opened on a live sandbox: nothing it collects can be mounted")
}

func (p failingPrompter) MultiSelect(context.Context, prompt.MultiSelectRequest) ([]string, error) {
	p.refuse()
	return nil, nil
}

func (p failingPrompter) Confirm(context.Context, prompt.ConfirmRequest) (bool, error) {
	p.refuse()
	return false, nil
}

func (p failingPrompter) Line(context.Context, prompt.LineRequest) (string, error) {
	p.refuse()
	return "", nil
}

func (p failingPrompter) Secret(context.Context, prompt.SecretRequest) (string, error) {
	p.refuse()
	return "", nil
}

func TestPromptModeDoesNotPromptWhenAttaching(t *testing.T) {
	denHome := denTestPrompting(t)

	// Create it once, with a selection.
	_, first := fakeDeps()
	first.Prompt = &prompt.Fake{MultiSelectAnswers: [][]string{{"api"}}}
	first.IsTTY = func() bool { return true }
	if err := Spawn(context.Background(), denHome, Options{Nest: "generic"}, first); err != nil {
		t.Fatalf("first spawn: %v", err)
	}

	// Attach it. Same name, no flags — the checklist must stay shut.
	f, d := fakeDeps()
	d.Prompt = failingPrompter{t}
	d.IsTTY = func() bool { return true }
	var out bytes.Buffer
	d.Out = &out
	// The Fake reports `generic` as running — the same scripting every attach
	// test in this package uses (spawn_test.go:309).
	f.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"generic","status":"running","workspaces":["/w/api"]}]}`),
	}
	if err := Spawn(context.Background(), denHome, Options{Nest: "generic"}, d); err != nil {
		t.Fatalf("attaching spawn: %v", err)
	}
	if !strings.Contains(out.String(), "--as") {
		t.Errorf("the attach message must name the way to run a different set:\n%s", out.String())
	}
}

// The same guard, reached by the OTHER entry point. The condition that closes
// the checklist is `live == nil`, ahead of both entry points, so `-i` on a live
// sandbox draws no checklist for exactly the reason a prompting nest draws none:
// nothing a selection collects can be mounted on a VM whose mounts come from its
// creation.
//
// An order that depends on a configuration key would be two spawn sequences to
// keep true, and §6 describes one — this test is what makes the second reading
// fail out loud rather than drift.
//
// The checklist stays shut; the SILENCE around it does not, and this test
// asserted that silence until the ruling reversed it. It used to read "on an
// `api` nest the explanatory `--as` lines do not print, deliberately — the
// remedy answers a question this user never asked". They did ask: they typed
// `-i`, and den dropped the flag without a word. The explanation now prints for
// every nest, and this is where an ordinary one is held to it.
func TestInteractiveDoesNotPromptWhenAttaching(t *testing.T) {
	denHome, repos := denTestOptional(t)

	f, d := fakeDeps()
	d.Prompt = failingPrompter{t}
	d.IsTTY = func() bool { return true }
	var out bytes.Buffer
	d.Out = &out
	f.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"api","status":"running","workspaces":["` + repos["api"] + `"]}]}`),
	}

	if err := Spawn(context.Background(), denHome,
		Options{Nest: "api", Interactive: true}, d); err != nil {
		t.Fatalf("attaching spawn: %v", err)
	}
	if f.HasCalled("create") {
		t.Errorf("no create must happen on a live sandbox; calls: %v", f.Calls)
	}
	if !strings.Contains(out.String(), "already live") {
		t.Errorf("the attach must be announced:\n%s", out.String())
	}
	// The reversed assertion: a discarded `-i` is never silent, on an ordinary
	// nest as on a prompting one.
	if !strings.Contains(out.String(), "--as") {
		t.Errorf("a dropped -i must be explained on an ordinary nest too:\n%s", out.String())
	}
	// Read on a DISTINCT substring from the line above, so neither can cover for
	// the other: this nest declares two optional repos, so the no-record case is
	// worth a line here — and that is exactly what
	// TestAttachSaysNothingAboutASelectionThatCannotExist proves den drops on a
	// nest that declares none.
	if !strings.Contains(out.String(), "no creation record") {
		t.Errorf("den must say it cannot tell which repos this sandbox holds:\n%s", out.String())
	}
}

// The guard on that line's OTHER side. `reportUnrebuiltSelection` used to fire on
// any nest, and on one with no optional repo it described a problem that cannot
// exist there — every repo is mounted, so the full list den resolves IS what the
// sandbox was created with — while naming `--only`/`--without` over repos
// nest.Resolve refuses to remove ("is a required repo of this nest"). A remedy
// that fails, under a diagnostic nobody needed.
//
// denTest's single repo is required (TestSpawnContinuesWhenTheNestHasNoOptionalRepo
// reads it the same way), and the VM is scripted to mount exactly it: the mute
// this guard also lifts is reportUnmountedRepos', so a mismatched fixture would
// litter the output this test reads.
//
// The `--as` line still prints — the `-i` was dropped and that stays worth
// saying. The two facts are independent, and this is the nest that tells them
// apart.
func TestAttachSaysNothingAboutASelectionThatCannotExist(t *testing.T) {
	denHome, repo := denTest(t)

	f, d := fakeDeps()
	d.Prompt = failingPrompter{t}
	d.IsTTY = func() bool { return true }
	var out bytes.Buffer
	d.Out = &out
	f.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"api","status":"running","workspaces":["` + repo + `"]}]}`),
	}

	if err := Spawn(context.Background(), denHome,
		Options{Nest: "api", Interactive: true}, d); err != nil {
		t.Fatalf("attaching spawn: %v", err)
	}
	rendered := out.String()
	if strings.Contains(rendered, "no creation record") {
		t.Errorf("a nest with no optional repo has no selection to have lost:\n%s", rendered)
	}
	for _, flag := range []string{"--only", "--without"} {
		if strings.Contains(rendered, flag) {
			t.Errorf("%s cannot remove a required repo: naming it is a remedy that fails:\n%s",
				flag, rendered)
		}
	}
	if !strings.Contains(rendered, "--as") {
		t.Errorf("the dropped -i is still worth explaining:\n%s", rendered)
	}
}

// denTestKeyNest: a nest whose ONE optional repo is a `key:` entry this machine
// does not map — the shape the spec's §2.4 example has, and the shape that makes
// the attach branch's re-derivation fatal rather than merely noisy.
//
// The required repo keeps the spawn viable: a nest that resolves to nothing has
// no workspaces and would fail for a reason that has nothing to do with keys.
//
// selectBlock is the lever: the SAME nest with and without `select: prompt` is
// what lets the two entry points of the checklist be tested against one fixture,
// which is the whole point — they must behave identically here.
func denTestKeyNest(t *testing.T, selectBlock string) string {
	t.Helper()
	denHome, api := denTest(t)
	write(t, filepath.Join(denHome, "nests", "crm.yaml"),
		"stack: devx\n"+selectBlock+"repos:\n"+
			"  - { path: "+api+" }\n"+
			"  - { key: crm, optional: true }\n")
	return denHome
}

func denTestPromptingKeys(t *testing.T) string {
	t.Helper()
	return denTestKeyNest(t, "select: prompt\n")
}

// THE acceptance criterion of the rebuild: a prompting nest survives its own
// second spawn.
//
// Without it, den refuses the attach with `repo key "crm" is not mapped on this
// machine` — a refusal aimed at a repo the user deliberately left out, on a VM
// that is running, over a key the checklist would have let them decline again.
// The generic nest of the spec is unusable past its first spawn.
//
// The refusal is upstream of every report, so suppressing the drift warnings
// cannot reach it: the selection itself has to come back.
func TestPromptModeAttachRebuildsTheSelectionFromTheRecord(t *testing.T) {
	denHome := denTestPromptingKeys(t)

	// Create it, declining the unmapped key — the checklist starts empty, so
	// confirming as-is is exactly that.
	_, first := fakeDeps()
	// The boxes start empty on a prompting nest, so an empty answer IS
	// "confirming as-is" — the unmapped key is declined.
	first.Prompt = &prompt.Fake{MultiSelectAnswers: [][]string{nil}}
	first.IsTTY = func() bool { return true }
	if err := Spawn(context.Background(), denHome, Options{Nest: "crm"}, first); err != nil {
		t.Fatalf("first spawn: %v", err)
	}

	f, d := fakeDeps()
	d.Prompt = failingPrompter{t}
	d.IsTTY = func() bool { return true }
	var out bytes.Buffer
	d.Out = &out
	f.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"crm","status":"running","workspaces":["/w/api"]}]}`),
	}

	if err := Spawn(context.Background(), denHome, Options{Nest: "crm"}, d); err != nil {
		t.Fatalf("attaching a prompting nest must not refuse over a repo it was never created with: %v", err)
	}
	if f.HasCalled("create") {
		t.Errorf("no create must happen on a live sandbox; calls: %v", f.Calls)
	}
}

// denTestPromptingRepos: denTestPrompting with three REAL git repositories, so
// `-w` can be exercised on every one of them.
//
// denTestPrompting's worker and docs are bare directories: worktree.Ensure
// refuses those, so a regression there would surface as a git error rather than
// as the extra worktrees this fixture exists to catch.
func denTestPromptingRepos(t *testing.T) (denHome string, repos map[string]string) {
	t.Helper()
	denHome, api := denTest(t)
	repos = map[string]string{"api": api}
	root := t.TempDir()
	for _, name := range []string{"worker", "docs"} {
		path := filepath.Join(root, name)
		createRepo(t, path)
		repos[name] = path
	}
	write(t, filepath.Join(denHome, "nests", "generic.yaml"),
		"stack: devx\nselect: prompt\nrepos:\n"+
			"  - { path: "+repos["api"]+", optional: true }\n"+
			"  - { path: "+repos["worker"]+", optional: true }\n"+
			"  - { path: "+repos["docs"]+", optional: true }\n")
	return denHome, repos
}

// What the rebuild is worth, read off the OUTPUT rather than off internal
// state. The readout used to be step 3's `worktree <repo>: <path>` lines, one
// per resolved repo — that line is a CREATION announcement and no longer prints
// on the attach branch, which creates nothing. What is left, and is just as
// direct a readout of r.Repos, is reportUnmountedRepos: against a VM that mounts
// none of them it prints one `is not mounted` line per resolved repo.
//
// So the readout is the SECOND half, on a mismatched VM, and it is an equality
// (exactly one line, naming api) rather than a presence check. The first half
// requires the silence that a correct rebuild produces; asserting silence alone
// would not do — two empty comparisons pass whatever the selection did, the trap
// the "Guard on the guard" of TestInteractiveProducesTheSameArgvAsTheEquivalentWithout
// already names.
//
// `-w` is also the case where re-derivation used to cost disk, not just noise:
// two worktrees created for repos this sandbox was never spawned with, which the
// user then had to find and remove.
func TestPromptModeAttachResolvesOnlyTheRecordedRepos(t *testing.T) {
	denHome, _ := denTestPromptingRepos(t)

	_, first := fakeDeps()
	first.Prompt = &prompt.Fake{MultiSelectAnswers: [][]string{{"api"}}} // tick api, decline worker and docs
	first.IsTTY = func() bool { return true }
	if err := Spawn(context.Background(), denHome,
		Options{Nest: "generic", Worktree: "feat"}, first); err != nil {
		t.Fatalf("first spawn: %v", err)
	}
	// The mounts come from the RECORD, so the fixture and den agree on the
	// worktree path without this test recomputing the layout.
	recorded, err := manifest.Read(denHome, "generic.feat")
	if err != nil {
		t.Fatalf("reading the record the first spawn wrote: %v", err)
	}
	if len(recorded.Repos) != 1 || recorded.Repos[0].Name != "api" {
		t.Fatalf("the fixture must record exactly api; got %+v", recorded.Repos)
	}

	f, d := fakeDeps()
	d.Prompt = failingPrompter{t}
	d.IsTTY = func() bool { return true }
	var out bytes.Buffer
	d.Out = &out
	// The git dirs are scripted too, exactly as the create branch passed them:
	// a VM missing them is a real defect (reportMissingGitDirs) and has its own
	// test — leaving them out here would print an unrelated warning into the
	// output this test reads.
	f.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"generic.feat","status":"running","workspaces":` +
			jsonStrings(append([]string{recorded.Repos[0].Mount}, recorded.GitDirs...)) + `}]}`),
	}
	if err := Spawn(context.Background(), denHome,
		Options{Nest: "generic", Worktree: "feat"}, d); err != nil {
		t.Fatalf("attaching spawn: %v", err)
	}

	if strings.Contains(out.String(), "is not mounted") {
		t.Errorf("the VM mounts exactly what the record names; nothing may be reported:\n%s",
			out.String())
	}
	// On disk too: a warning could be silent while the directory exists.
	for _, name := range []string{"worker", "docs"} {
		if _, err := os.Stat(filepath.Join(denHome, "worktrees", "feat", name)); err == nil {
			t.Errorf("a worktree was created for %s, which this sandbox was never spawned with", name)
		}
	}

	// Guard on the guard: with a workspace the VM does NOT mount, the warning
	// must fire — and name exactly the recorded repo. Without this, every
	// assertion above passes on a spawn that resolved nothing at all.
	mismatched, md := fakeDeps()
	md.Prompt = failingPrompter{t}
	md.IsTTY = func() bool { return true }
	var mismatchedOut bytes.Buffer
	md.Out = &mismatchedOut
	mismatched.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"generic.feat","status":"running","workspaces":["/w/elsewhere"]}]}`),
	}
	if err := Spawn(context.Background(), denHome,
		Options{Nest: "generic", Worktree: "feat"}, md); err != nil {
		t.Fatalf("attaching spawn (mismatched): %v", err)
	}
	var unmounted []string
	for _, line := range strings.Split(mismatchedOut.String(), "\n") {
		if strings.HasSuffix(line, " is not mounted") {
			// "  - <path> is not mounted": both the bullet and the indent are
			// presentation, and the path is what this test reads.
			unmounted = append(unmounted,
				strings.TrimPrefix(strings.TrimSpace(strings.TrimSuffix(line, " is not mounted")), "- "))
		}
	}
	if len(unmounted) != 1 || unmounted[0] != recorded.Repos[0].Mount {
		t.Errorf("the attach must resolve exactly the recorded repo (%s); reported: %v\n%s",
			recorded.Repos[0].Mount, unmounted, mismatchedOut.String())
	}
}

// No record — a sandbox created before records existed, or one created outside
// den. den must never refuse and strand a live VM (doctrine T13/T16), so the
// attach still works; what it must NOT do is report every optional repo as "not
// mounted", since the list it would compare against is a selection nobody made.
//
// It says so, though, and says it PLAINLY: this case is ordinary, and a
// `warning:` on it would teach the reader to skip the marker on the case that is
// a fault (TestPromptModeAttachNamesARecordItCouldNotRead). The two halves of
// `recordedErr != nil` are asserted apart for that reason alone.
func TestPromptModeAttachWithoutARecordStillAttaches(t *testing.T) {
	denHome := denTestPrompting(t)

	f, d := fakeDeps()
	d.Prompt = failingPrompter{t}
	d.IsTTY = func() bool { return true }
	var out bytes.Buffer
	d.Out = &out
	f.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"generic","status":"running","workspaces":["/w/api"]}]}`),
	}

	if err := Spawn(context.Background(), denHome, Options{Nest: "generic"}, d); err != nil {
		t.Fatalf("a sandbox with no record must still be attachable: %v", err)
	}
	if strings.Contains(out.String(), "is not mounted") {
		t.Errorf("with no record there is no selection to compare against:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "--as") {
		t.Errorf("the attach message keeps its remedy with no record:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "no creation record") ||
		!strings.Contains(out.String(), "--only") {
		t.Errorf("den must say it could not tell which repos this sandbox holds, and how to say so:\n%s",
			out.String())
	}
	// The nest is a prompting one, where `--without` is refused: the line that
	// offers a way through must not offer that one.
	if strings.Contains(out.String(), "--without") {
		t.Errorf("the way through must be a command that works on this nest:\n%s", out.String())
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.Contains(line, "no creation record") && strings.Contains(line, "warning:") {
			t.Errorf("an absent record is ordinary, not a fault: %q", line)
		}
	}
}

// The rebuild's condition must be the checklist's condition — this test is what
// makes them stay one condition.
//
// `-i` is the checklist's OTHER entry point. Shutting the checklist on a live
// sandbox therefore took the selection away from `-i` too, and a rebuild scoped
// to `select: prompt` alone would leave that half shut with nothing put back: on
// a nest with an optional `key:` this machine does not map, `den spawn crm -i`
// against a running sandbox refused where it used to prompt. One guard turned
// the selection off, the other has to turn it back on, over the same set of
// spawns.
//
// The nest here is denTestPromptingKeys' own, MINUS `select: prompt` — same
// repos, same unmapped key — so nothing but the entry point differs between the
// two tests.
func TestInteractiveAttachRebuildsTheSelectionFromTheRecord(t *testing.T) {
	denHome := denTestKeyNest(t, "")

	// Created with the key declined. --without, not the checklist: on a
	// non-prompting nest a bare create mounts every optional repo, and this
	// sandbox has to be one the user narrowed.
	_, first := fakeDeps()
	if err := Spawn(context.Background(), denHome,
		Options{Nest: "crm", Without: []string{"crm"}}, first); err != nil {
		t.Fatalf("first spawn: %v", err)
	}

	f, d := fakeDeps()
	d.Prompt = failingPrompter{t}
	d.IsTTY = func() bool { return true }
	var out bytes.Buffer
	d.Out = &out
	f.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"crm","status":"running","workspaces":["/w/api"]}]}`),
	}

	if err := Spawn(context.Background(), denHome,
		Options{Nest: "crm", Interactive: true}, d); err != nil {
		t.Fatalf("-i on a live sandbox must not refuse over a repo it was never created with: %v", err)
	}
	if f.HasCalled("create") {
		t.Errorf("no create must happen on a live sandbox; calls: %v", f.Calls)
	}
}

// The other half of `recordedErr != nil`, and the one no test reached: a record
// den could READ the path of but not decode.
//
// A newer SCHEMA, not random bytes, because that is the case the doctrine is
// about — the record may belong to a newer den, so den neither refuses over it
// nor deletes it (T13/T16). The attach must still work, the file must still be
// there afterwards, and the user must be told which file den gave up on: told
// nothing, they meet the compound failure — an unmapped optional `key:` then
// refuses the attach, with a remedy that works and no hint that den had an
// answer it could not read.
func TestPromptModeAttachNamesARecordItCouldNotRead(t *testing.T) {
	denHome := denTestPrompting(t)
	path, err := manifest.Path(denHome, "generic")
	if err != nil {
		t.Fatal(err)
	}
	write(t, path, "schema: 9999\nsandbox: generic\n")

	f, d := fakeDeps()
	d.Prompt = failingPrompter{t}
	d.IsTTY = func() bool { return true }
	var out bytes.Buffer
	d.Out = &out
	f.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"generic","status":"running","workspaces":["/w/api"]}]}`),
	}

	if err := Spawn(context.Background(), denHome, Options{Nest: "generic"}, d); err != nil {
		t.Fatalf("an unreadable record must never refuse a spawn: %v", err)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "warning:") || !strings.Contains(rendered, path) {
		t.Errorf("den must name the record it could not read:\n%s", rendered)
	}
	if !strings.Contains(rendered, "--only") {
		t.Errorf("the message must name the way through:\n%s", rendered)
	}
	if strings.Contains(rendered, "--without") {
		t.Errorf("the way through must be a command that works on this prompting nest:\n%s", rendered)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("den deleted a record it could not read — it may belong to a newer den: %v", err)
	}
}

// `--without` subtracts from a default selection, and a `select: prompt` nest
// declares it has none — accepted, the flag silenced the checklist and mounted
// the maximal set minus what was named, which is the thirty-repo default the
// mode exists not to have. The refusal is the reading a user cannot misread.
//
// The ordinary nest is in the same test: `--without` still works there, and a
// refusal that fired for everyone would be caught here rather than three
// packages away.
//
// The refusal precedes even the `sbx ls` listing — no call at all, not merely no
// create — because its verdict comes from the nest file and nothing else. That
// is the placement assertion: it sits at step 0bis, right after the nest is
// loaded, since step 0 itself runs before there is a nest to ask.
func TestSpawnRefusesWithoutOnAPromptingNest(t *testing.T) {
	promptingHome := denTestPrompting(t)
	f, d := fakeDeps()

	err := Spawn(context.Background(), promptingHome,
		Options{Nest: "generic", Without: []string{"docs"}}, d)
	if err == nil {
		t.Fatal("--without on a nest with no default selection must be refused")
	}
	for _, want := range []string{
		"generic",
		"--without",
		"`--only repo,...`",
		filepath.Join(promptingHome, "nests", "generic.yaml"),
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must carry %q; got: %v", want, err)
		}
	}
	if len(f.Calls) != 0 {
		t.Errorf("the refusal is decided on the nest file alone: no sbx call may precede it: %v", f.Calls)
	}

	// `-i --without` on the same nest: the refusal that wins must be THIS one,
	// not the `-i` contradiction — that one tells the user to keep `--without`,
	// which this nest rejects. Two correct-looking refusals, and only one of them
	// hands over a command that works.
	pair, pd := fakeDeps()
	pairErr := Spawn(context.Background(), promptingHome,
		Options{Nest: "generic", Interactive: true, Without: []string{"docs"}}, pd)
	if pairErr == nil {
		t.Fatal("-i with --without on a prompting nest must be refused")
	}
	if !strings.Contains(pairErr.Error(), "`--only repo,...`") {
		t.Errorf("the refusal must hand over the flag that works on this nest: %v", pairErr)
	}
	if strings.Contains(pairErr.Error(), "--without is the non-interactive form") {
		t.Errorf("the refusal must not tell the user to keep a flag den rejects here: %v", pairErr)
	}
	if len(pair.Calls) != 0 {
		t.Errorf("no sbx call may precede the refusal: %v", pair.Calls)
	}

	// The floor: on a nest with a default selection, subtracting from it is
	// exactly what --without is for, and it still spawns.
	ordinaryHome, _ := denTestOptional(t)
	ordinary, od := fakeDeps()
	if err := Spawn(context.Background(), ordinaryHome,
		Options{Nest: "api", Without: []string{"docs"}}, od); err != nil {
		t.Fatalf("--without on an ordinary nest must keep working: %v", err)
	}
	if !ordinary.HasCalled("create") {
		t.Errorf("nothing was created; calls: %v", ordinary.Calls)
	}
}

// The compound failure of the attach branch, and the remedy it must name.
//
// A live sandbox, no record den can read, and an optional `key:` this machine
// does not map: den resolves every repo the nest declares, and nest.Resolve
// refuses over a repo the user had declined — on a VM that is running. The
// refusal stays (den never drops a repo on its own), but its create-branch
// remedy does not apply here: mapping the key would not put the repo in a
// sandbox whose mounts are frozen at its creation.
//
// The two entry points of the rebuild are both exercised, because they need
// DIFFERENT remedies and that is the whole point of naming one: `-i` on an
// ordinary nest can subtract the key, a prompting nest cannot (den refuses
// `--without` on it) and must name `--only` instead.
//
// BOTH halves of "no record" are exercised too, and that is not redundancy:
// `selectionUnknown` covers an ABSENT record (ordinary — a sandbox older than
// records, or one created outside den) and an UNREADABLE one (a fault). One
// sentence serves both, so it must not claim a read that failed; tested against
// the absent fixture alone, "den could not read the record" passed while telling
// half its readers about a failure that never happened.
func TestAttachRefusalOnAnUnmappedKeyNamesTheRemedyThatWorks(t *testing.T) {
	for _, c := range []struct {
		name        string
		denHome     func(t *testing.T) string
		o           Options
		want, avoid string
	}{
		{
			name:    "-i on an ordinary nest",
			denHome: func(t *testing.T) string { return denTestKeyNest(t, "") },
			o:       Options{Nest: "crm", Interactive: true},
			want:    "`--without crm`",
			avoid:   "--only",
		},
		{
			name:    "a prompting nest, no record at all",
			denHome: denTestPromptingKeys,
			o:       Options{Nest: "crm"},
			want:    "`--only repo,...`",
			avoid:   "--without crm",
		},
		{
			// A newer SCHEMA, not random bytes: the record may belong to a newer
			// den, which is why den neither refuses over it nor deletes it
			// (T13/T16) — the same fixture TestPromptModeAttachNamesARecordItCouldNotRead
			// proves reaches manifest.Read's error path.
			name: "a prompting nest whose record den cannot read",
			denHome: func(t *testing.T) string {
				denHome := denTestPromptingKeys(t)
				path, err := manifest.Path(denHome, "crm")
				if err != nil {
					t.Fatal(err)
				}
				write(t, path, "schema: 9999\nsandbox: crm\n")
				// Guard on the fixture: a record written somewhere manifest.Read
				// does not look would make this row the absent-record case again,
				// silently — the two would then be one test twice.
				if _, err := manifest.Read(denHome, "crm"); err == nil ||
					errors.Is(err, os.ErrNotExist) {
					t.Fatalf("the fixture must produce an UNREADABLE record, got: %v", err)
				}
				return denHome
			},
			o:     Options{Nest: "crm"},
			want:  "`--only repo,...`",
			avoid: "--without crm",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			denHome := c.denHome(t)
			f, d := fakeDeps()
			d.Prompt = failingPrompter{t}
			d.IsTTY = func() bool { return true }
			f.Responses["ls --json"] = sbx.Response{
				Output: []byte(`{"sandboxes":[{"name":"crm","status":"running","workspaces":["/w/api"]}]}`),
			}

			err := Spawn(context.Background(), denHome, c.o, d)
			if err == nil {
				t.Fatal("an unmapped key that den ends up selecting must refuse, live or not")
			}
			// The config path is the RESOLVED one, tied to this sentence rather
			// than merely present somewhere in the chain: a message spelling
			// `config.yaml` by hand names a file the reader cannot find under
			// DEN_HOME, and config.GlobalPath is its sole definition.
			for _, want := range []string{
				"already LIVE",
				"crm",
				c.want,
				// True of an absent record and of an unreadable one alike: this
				// one sentence serves both, and only one of them is a failure.
				"has no record it could read",
				"in " + filepath.Join(denHome, "config.yaml") + " would not put that repo",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal must carry %q; got: %v", want, err)
				}
			}
			if strings.Contains(err.Error(), c.avoid) {
				t.Errorf("the remedy must be a command that works on this nest; got: %v", err)
			}
			if f.HasCalled("create") {
				t.Errorf("the refusal must create nothing; calls: %v", f.Calls)
			}
		})
	}
}

// The remedy the refusal above names, taken: `--only` gets a prompting nest
// attached where the bare form refuses, and it STANDS as the user's set — den
// does not rebuild one from a record it could not read, and there is nothing to
// rebuild it from anyway.
//
// This is also finding 9's verification, in executable form: `--only` is the one
// input that now reaches the attach branch of a prompting nest with the
// selection question shut (`--without` is refused at step 0bis), and it needs no
// rebuild because the user named the set outright.
func TestOnlyAttachesAPromptingNestWithNoRecord(t *testing.T) {
	denHome := denTestPromptingKeys(t)
	f, d := fakeDeps()
	d.Prompt = failingPrompter{t}
	d.IsTTY = func() bool { return true }
	f.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"crm","status":"running","workspaces":["/w/api"]}]}`),
	}

	if err := Spawn(context.Background(), denHome,
		Options{Nest: "crm", Only: []string{"api"}}, d); err != nil {
		t.Fatalf("--only must attach where the unmapped key it leaves out would refuse: %v", err)
	}
	if f.HasCalled("create") {
		t.Errorf("the sandbox is live: it must be attached, not recreated; calls: %v", f.Calls)
	}
}

// The other half of "--only stands": with a record den CAN read, the flag still
// wins over it. A selection flag answers the question outright — that is what
// silences the checklist in the first place — so rebuilding a selection from the
// record here would discard what the user typed, on the branch where they typed
// it most deliberately.
//
// Read off the output, because the mounts themselves cannot move on a live
// sandbox: asking for a repo the VM does not carry is exactly what
// reportUnmountedRepos is for, and its line firing for `worker` is the proof den
// resolved the user's set and not the record's.
func TestOnlyStandsOverTheRecordOnAPromptingAttach(t *testing.T) {
	denHome := denTestPrompting(t)

	_, first := fakeDeps()
	first.Prompt = &prompt.Fake{MultiSelectAnswers: [][]string{{"api"}}} // tick api alone
	first.IsTTY = func() bool { return true }
	if err := Spawn(context.Background(), denHome, Options{Nest: "generic"}, first); err != nil {
		t.Fatalf("first spawn: %v", err)
	}
	recorded, err := manifest.Read(denHome, "generic")
	if err != nil {
		t.Fatalf("reading the record the first spawn wrote: %v", err)
	}
	if len(recorded.Repos) != 1 || recorded.Repos[0].Name != "api" {
		t.Fatalf("the fixture must record exactly api; got %+v", recorded.Repos)
	}

	f, d := fakeDeps()
	d.Prompt = failingPrompter{t}
	d.IsTTY = func() bool { return true }
	var out bytes.Buffer
	d.Out = &out
	f.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"generic","status":"running","workspaces":["` +
			recorded.Repos[0].Mount + `"]}]}`),
	}

	if err := Spawn(context.Background(), denHome,
		Options{Nest: "generic", Only: []string{"api", "worker"}}, d); err != nil {
		t.Fatalf("attaching spawn: %v", err)
	}
	if !strings.Contains(out.String(), "worker") || !strings.Contains(out.String(), "is not mounted") {
		t.Errorf("den must resolve the SET THE USER NAMED and report what the VM lacks:\n%s", out.String())
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.Contains(line, "docs") && strings.Contains(line, "is not mounted") {
			t.Errorf("a repo outside --only must not be resolved at all: %q", line)
		}
	}
}

// A positional and a declared key can carry the SAME short name, and only the
// declared one is a selection: `manifest.Repo.Name` is `Repo.Name()`, which for
// a command-line entry is the basename of the path typed.
//
// Counted as "selected", an ad-hoc mount named `crm` makes the rebuild omit the
// DECLARED `key: crm` from --without, nest.Resolve selects it again, and
// resolveRepoKeys refuses the attach of a live VM over the very key the user
// declined. The scenario is ordinary: mount a checkout on the fly, decline the
// declared repo of the same name because this machine does not map it, come
// back tomorrow.
func TestPromptModeAttachIgnoresAdHocMountsWhenRebuilding(t *testing.T) {
	denHome := denTestPromptingKeys(t)
	// The basename is what collides, so it is what the fixture must control.
	adHoc := filepath.Join(t.TempDir(), "crm")
	if err := os.MkdirAll(adHoc, 0o755); err != nil {
		t.Fatal(err)
	}

	_, first := fakeDeps()
	first.Prompt = &prompt.Fake{MultiSelectAnswers: [][]string{nil}} // starts empty: `key: crm` is declined
	first.IsTTY = func() bool { return true }
	if err := Spawn(context.Background(), denHome,
		Options{Nest: "crm", Repos: []string{adHoc}}, first); err != nil {
		t.Fatalf("first spawn: %v", err)
	}
	// Guard on the fixture: without a command-line entry named `crm` in the
	// record, this test proves nothing.
	recorded, err := manifest.Read(denHome, "crm")
	if err != nil {
		t.Fatalf("reading the record the first spawn wrote: %v", err)
	}
	var adHocRecorded bool
	for _, r := range recorded.Repos {
		if r.Name == "crm" && r.Origin == manifest.OriginCommandLine {
			adHocRecorded = true
		}
	}
	if !adHocRecorded {
		t.Fatalf("the fixture must record an ad-hoc `crm`; got %+v", recorded.Repos)
	}

	f, d := fakeDeps()
	d.Prompt = failingPrompter{t}
	d.IsTTY = func() bool { return true }
	f.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"crm","status":"running","workspaces":["/w/api"]}]}`),
	}

	if err := Spawn(context.Background(), denHome, Options{Nest: "crm"}, d); err != nil {
		t.Fatalf("an ad-hoc mount must not pass for the declared repo of the same name: %v", err)
	}
	if f.HasCalled("create") {
		t.Errorf("no create must happen on a live sandbox; calls: %v", f.Calls)
	}
}

// The spec's own headline command, `den spawn dg:digitaleo --as leo-fix`, and a
// seam no per-task review could see: task 1 covers `--as` on an ordinary nest,
// tasks 5-6 cover a prompting nest without `--as`, and nothing covered the
// composition.
//
// What the composition has to hold is decision 11 plus decision 12 at once: the
// label enters the sandbox NAME, the repos still do not — so `--as` is what lets
// two different selections of one generic nest coexist, and each one's record is
// read back under its own name. Asserted on the two things that carry it: the
// name `sbx create` receives, and the second spawn attaching that name instead
// of creating it.
func TestPromptModeComposesWithAnInstanceLabel(t *testing.T) {
	denHome := denTestPrompting(t)

	f, d := fakeDeps()
	d.Prompt = &prompt.Fake{MultiSelectAnswers: [][]string{{"api"}}} // tick api only
	d.IsTTY = func() bool { return true }
	if err := Spawn(context.Background(), denHome,
		Options{Nest: "generic", Instance: "leo-fix"}, d); err != nil {
		t.Fatalf("prompting nest with --as: %v", err)
	}
	if !f.HasCalled("create", "--name", "generic.leo-fix") {
		t.Fatalf("the label must reach the sandbox name; calls: %v", f.Calls)
	}
	// The record is written under the LABELLED name, which is what makes the
	// rebuild below read this selection rather than the unlabelled sandbox's.
	recorded, err := manifest.Read(denHome, "generic.leo-fix")
	if err != nil {
		t.Fatalf("reading the record: %v", err)
	}
	if len(recorded.Repos) != 1 || recorded.Repos[0].Name != "api" {
		t.Fatalf("the label must not change what the checklist selected; got %+v", recorded.Repos)
	}

	// Re-attach it: same nest, same label, no checklist, selection rebuilt.
	attachFake, attachDeps := fakeDeps()
	attachDeps.Prompt = failingPrompter{t}
	attachDeps.IsTTY = func() bool { return true }
	var out bytes.Buffer
	attachDeps.Out = &out
	attachFake.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"generic.leo-fix","status":"running","workspaces":["` +
			recorded.Repos[0].Mount + `"]}]}`),
	}
	if err := Spawn(context.Background(), denHome,
		Options{Nest: "generic", Instance: "leo-fix"}, attachDeps); err != nil {
		t.Fatalf("attaching a labelled prompting sandbox: %v", err)
	}
	if attachFake.HasCalled("create") {
		t.Errorf("the labelled sandbox is live: it must be attached, not recreated; calls: %v",
			attachFake.Calls)
	}
	if strings.Contains(out.String(), "is not mounted") {
		t.Errorf("the rebuild must read the LABELLED record, so nothing is missing:\n%s", out.String())
	}
}

// The refusal half of decision 10, which the spec's test surface asks for "mot
// pour mot" and which no test in the tree asserted: the annotation half was
// covered, the refusal half was not.
//
// A CHECKED unmapped key must refuse, and the refusal must carry all three
// things resolveRepoKeys puts in it — the key, the file to edit, and the escape
// it offers because the repo is optional. den never drops a repo on its own
// (spec §2), so this is the one place the checklist's permissive annotation gets
// settled.
//
// The escape is `--only` HERE and `--without <key>` on an ordinary nest
// (TestUnmappedKeyOffersTheEscapeThatWorksOnThisNest holds the pair): den
// refuses `--without` on a prompting nest, so offering it in this very refusal
// would answer a refusal with a command that is itself refused.
func TestPromptModeRefusesACheckedUnmappedKey(t *testing.T) {
	denHome := denTestPromptingKeys(t)

	f, d := fakeDeps()
	d.Prompt = &prompt.Fake{MultiSelectAnswers: [][]string{{"crm"}}} // tick `crm`, which nothing maps here
	d.IsTTY = func() bool { return true }

	err := Spawn(context.Background(), denHome, Options{Nest: "crm"}, d)
	if err == nil {
		t.Fatal("a checked unmapped key must refuse: den never drops a repo on its own")
	}
	for _, want := range []string{
		`repo key "crm" is not mapped on this machine`,
		filepath.Join(denHome, "config.yaml"),
		"--only",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must carry %q; got: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "--without crm") {
		t.Errorf("the escape must not name a flag den refuses on this nest: %v", err)
	}
	if !createdNothing(f) {
		t.Errorf("the refusal must create nothing; calls: %v", f.Calls)
	}
}

// The checklist's annotation names the file of THE den home this spawn is
// running under — end to end, through Spawn, over a temporary home that is
// nobody's default.
//
// The unit test above asserts the same string against a fixture path; this one
// asserts the THREADING, which is what finding 15 actually broke: denHome is a
// parameter of Spawn, and the annotation printed the bare "config.yaml" because
// nothing carried it down to unmappedNote. Under DEN_HOME — the mechanism that
// makes this very suite hermetic — that named a file the reader would not find,
// while the refusal one keystroke later (the test above) named the real one.
// Two messages about one file, disagreeing on where it is.
func TestPromptAnnotationNamesTheRunningDenHomesConfig(t *testing.T) {
	denHome := denTestPromptingKeys(t)

	_, d := fakeDeps()
	// Confirm as-is: the nest prompts, so the boxes start empty and `crm`
	// stays unticked — nothing refuses.
	p := &prompt.Fake{MultiSelectAnswers: [][]string{nil}}
	d.Prompt = p
	d.IsTTY = func() bool { return true }

	if err := Spawn(context.Background(), denHome, Options{Nest: "crm"}, d); err != nil {
		t.Fatalf("declining the unmapped key must spawn: %v", err)
	}
	// The guard before the assertion: a checklist that never opened records no
	// option, and "no option names the wrong file" is a test that cannot fail.
	if len(p.MultiSelects) != 1 {
		t.Fatalf("the checklist must have been opened exactly once, got %d", len(p.MultiSelects))
	}
	want := config.GlobalPath(denHome)
	var annotated bool
	for _, o := range p.MultiSelects[0].Options {
		if strings.Contains(o.Description, want) {
			annotated = true
		}
	}
	if !annotated {
		t.Errorf("the checklist annotation must name %s, not a bare filename: %+v",
			want, p.MultiSelects[0].Options)
	}
}

// A LOCAL nest resolves its `key:` repos through config.yaml — and the
// checklist has to say so too.
//
// The regression this locks: the mapping handed to the checklist became nil
// for every nest outside a manifested source (nil is nest.Resolve's signal to
// fall back on config.yaml), so `-i` annotated every keyed repo as "not
// mapped" while the spawn one step later mounted it perfectly well. The screen
// where a user decides whether to include a repo must not tell them it is
// unavailable.
func TestInteractiveAnnotatesNothingForAMappedLocalKey(t *testing.T) {
	denHome, api := denTest(t)
	optional := filepath.Join(t.TempDir(), "docs")
	if err := os.MkdirAll(optional, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(denHome, "config.yaml"),
		readAll(t, filepath.Join(denHome, "config.yaml"))+
			"repos:\n  docs: "+optional+"\n")
	write(t, filepath.Join(denHome, "nests", "api.yaml"),
		"stack: devx\nrepos:\n"+
			"  - { path: "+api+" }\n"+
			"  - { key: docs, optional: true }\n")

	fake, d := fakeDeps()
	// Accept the default selection: `-i` starts full, so the one optional repo
	// stays ticked.
	p := &prompt.Fake{MultiSelectAnswers: [][]string{{"docs"}}}
	d.Prompt = p
	d.IsTTY = func() bool { return true }
	if err := Spawn(context.Background(), denHome,
		Options{Nest: "api", Interactive: true}, d); err != nil {
		t.Fatalf("interactive spawn: %v", err)
	}
	// The guard the negative assertion below needs: a checklist that never
	// opened annotates nothing, and "nothing says not mapped" would pass on a
	// spawn that asked no question at all.
	if len(p.MultiSelects) != 1 {
		t.Fatalf("the checklist must have been opened exactly once, got %d", len(p.MultiSelects))
	}
	for _, o := range p.MultiSelects[0].Options {
		if strings.Contains(o.Description, "not mapped") {
			t.Errorf("the checklist calls a mapped key unmapped: %+v", o)
		}
	}
	// And the spawn really did mount it, so the assertion above is about the
	// annotation and not about a repo that was dropped.
	mounted := false
	for _, c := range fake.Calls {
		if c[0] == "create" && slices.Contains(c, optional) {
			mounted = true
		}
	}
	if !mounted {
		t.Errorf("the mapped optional repo was not mounted; calls: %v", fake.Calls)
	}
}

func readAll(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
