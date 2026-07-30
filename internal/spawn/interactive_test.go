package spawn

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/nest"
)

// optionalRepos is the fixture of the checklist: one required repo the user
// cannot decline, and two optional ones they can.
func optionalRepos() []nest.Repo {
	return []nest.Repo{
		{Path: "/dev/api"},
		{Path: "/dev/worker", Optional: true},
		{Path: "/dev/docs", Optional: true},
	}
}

func prompt(t *testing.T, input string) ([]string, string, error) {
	t.Helper()
	var out bytes.Buffer
	without, err := promptOptionalRepos(&out, strings.NewReader(input), "api", optionalRepos())
	return without, out.String(), err
}

// Everything checked is the DEFAULT: confirming without touching anything
// mounts the nest as declared, i.e. the same thing as passing no flag at all.
func TestPromptKeepsEverythingWhenConfirmedAsIs(t *testing.T) {
	without, _, err := prompt(t, "\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(without) != 0 {
		t.Errorf("confirming as-is must exclude nothing, got %v", without)
	}
}

func TestPromptExcludesEverythingUnchecked(t *testing.T) {
	without, _, err := prompt(t, "1 2\n\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Equal(without, []string{"worker", "docs"}) {
		t.Errorf("got %v, want [worker docs]", without)
	}
}

func TestPromptExcludesOnlyWhatWasUnchecked(t *testing.T) {
	without, _, err := prompt(t, "2\n\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Equal(without, []string{"docs"}) {
		t.Errorf("got %v, want [docs]", without)
	}
}

// A toggle is a TOGGLE: the same number twice returns to the initial state.
// Without this, a mis-typed number is unrecoverable and the only way out is
// Ctrl-C — after which the user reruns the whole command.
func TestPromptTogglesBackAndForth(t *testing.T) {
	without, _, err := prompt(t, "2\n2\n\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(without) != 0 {
		t.Errorf("toggling twice must return to the initial selection, got %v", without)
	}
}

// An unreadable line changes NOTHING and asks again: applying the valid part of
// "2 zzz" would silently act on half of what the user typed.
func TestPromptRejectsAnInvalidEntryAndAsksAgain(t *testing.T) {
	without, out, err := prompt(t, "zzz\n4\n2\n\n")
	if err != nil {
		t.Fatalf("an invalid entry must be recoverable, not fatal: %v", err)
	}
	if !slices.Equal(without, []string{"docs"}) {
		t.Errorf("got %v, want [docs]: only the valid entry may take effect", without)
	}
	if !strings.Contains(out, "zzz") || !strings.Contains(out, "4") {
		t.Errorf("both rejected entries must be named back to the user:\n%s", out)
	}
}

// The pipe case. The read returns nothing and will never return anything: say
// so rather than confirm a selection the user never made — this one creates a
// microVM.
func TestPromptRefusesAnInputThatEndsBeforeConfirmation(t *testing.T) {
	_, _, err := prompt(t, "")
	if err == nil {
		t.Fatal("EOF before confirmation must be an error, never a silent confirmation")
	}
	if !strings.Contains(err.Error(), "--only") || !strings.Contains(err.Error(), "--without") {
		t.Errorf("the error must name the non-interactive equivalents: %v", err)
	}
}

// Required repos are always mounted (spec §6.2): they are not offered, and
// their numbers are not in the user's namespace either — otherwise "1" would
// designate different repos depending on how many required ones precede it.
func TestPromptOffersOptionalReposOnly(t *testing.T) {
	without, out, err := prompt(t, "1\n\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "api") && !strings.Contains(out, "nest api") {
		t.Errorf("a required repo must not appear in the checklist:\n%s", out)
	}
	if !slices.Equal(without, []string{"worker"}) {
		t.Errorf("got %v, want [worker]: 1 must designate the first OPTIONAL repo", without)
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
	interactiveDeps.In = strings.NewReader("2\n\n") // uncheck "docs"
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
			d.In = strings.NewReader("\n")
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
	d.In = strings.NewReader("\n") // readable, but nobody is there to type
	d.IsTTY = func() bool { return false }

	err := Spawn(context.Background(), denHome, Options{Nest: "api", Interactive: true}, d)
	if err == nil {
		t.Fatal("-i without a terminal must fail rather than read a stream nobody types into")
	}
	if !strings.Contains(err.Error(), "--only") || !strings.Contains(err.Error(), "--without") {
		t.Errorf("the refusal must name the non-interactive equivalents: %v", err)
	}
	if len(f.Calls) != 0 {
		t.Errorf("no sbx call may precede the refusal: %v", f.Calls)
	}
}

// A nil IsTTY is "no terminal", not "assume one": a caller that forgot to wire
// the probe must get the clean refusal, not a spawn hanging on a read.
func TestSpawnTreatsAnUnwiredTerminalProbeAsNoTerminal(t *testing.T) {
	denHome, _ := denTestOptional(t)
	_, d := fakeDeps()
	d.In = strings.NewReader("\n")

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
	d.In = strings.NewReader("") // nothing to read: nothing is asked
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

// The checklist is written where the user can see it while typing, and read
// from the injected stream — never from os.Stdin, which no test can supply.
func TestSpawnPromptsOnTheInjectedStreams(t *testing.T) {
	denHome, _ := denTestOptional(t)
	var out bytes.Buffer
	_, d := fakeDeps()
	d.Out = &out
	d.In = strings.NewReader("\n")
	d.IsTTY = func() bool { return true }

	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Interactive: true}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "worker") || !strings.Contains(out.String(), "docs") {
		t.Errorf("the checklist must list the optional repos:\n%s", out.String())
	}
}
