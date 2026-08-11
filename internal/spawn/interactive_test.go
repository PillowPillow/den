package spawn

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/manifest"
	"github.com/PillowPillow/den/internal/nest"
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

func prompt(t *testing.T, input string) ([]string, string, error) {
	t.Helper()
	return promptWith(t, input, true, nil)
}

func promptWith(t *testing.T, input string, startChecked bool,
	mapping map[string]string) ([]string, string, error) {
	t.Helper()
	var out bytes.Buffer
	without, err := promptOptionalRepos(&out, strings.NewReader(input), "api",
		optionalRepos(), startChecked, mapping)
	return without, out.String(), err
}

// Decision 9: a `select: prompt` checklist starts EMPTY, and confirming it
// as-is excludes every optional repo. The -i checklist keeps starting full —
// the two answer different questions, and both readings live in this one test.
func TestPromptStartingStateFollowsTheMode(t *testing.T) {
	for _, c := range []struct {
		name         string
		startChecked bool
		want         []string
	}{
		{"-i starts full", true, nil},
		{"select: prompt starts empty", false, []string{"worker", "docs"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			without, _, err := promptWith(t, "\n", c.startChecked, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !slices.Equal(without, c.want) {
				t.Errorf("confirming as-is gave --without %v, want %v", without, c.want)
			}
		})
	}
}

// The header keeps its "required repos are always mounted" clause in BOTH
// modes: a select: prompt nest may declare required repos, and "none selected"
// alone would then be a lie.
func TestPromptHeaderAlwaysNamesRequiredRepos(t *testing.T) {
	for _, startChecked := range []bool{true, false} {
		_, out, err := promptWith(t, "\n", startChecked, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(out, "required repos are always mounted") {
			t.Errorf("startChecked=%v: header lost its required-repos clause:\n%s", startChecked, out)
		}
	}
}

// Decision 10: an unmapped key is ANNOTATED, never hidden and never refused.
// Refusing the tick here would make the checklist a second judge of the repo
// mapping, whose single judge is resolveRepoKeys — and it would be a mute
// refusal on the one surface where the user cannot yet see what they asked
// for.
func TestPromptAnnotatesUnmappedKeys(t *testing.T) {
	repos := []nest.Repo{
		{Path: "/dev/backend"},
		{Key: "worker", Optional: true},
		{Key: "docs", Optional: true},
	}
	var out bytes.Buffer
	if _, err := promptOptionalRepos(&out, strings.NewReader("\n"), "api", repos, false,
		map[string]string{"worker": "/dev/worker"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "docs") || !strings.Contains(rendered, "not mapped") {
		t.Errorf("an unmapped key must be annotated:\n%s", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, "worker") && strings.Contains(line, "not mapped") {
			t.Errorf("a MAPPED key must carry no annotation: %q", line)
		}
	}
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
	if strings.Contains(out, "backend") {
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
	promptDeps.In = strings.NewReader("1 2\n\n") // tick api and worker
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
	bareDeps.In = strings.NewReader("1\n\n")
	bareDeps.IsTTY = func() bool { return true }
	if err := Spawn(context.Background(), denHome, Options{Nest: "generic"}, bareDeps); err != nil {
		t.Fatalf("bare spawn: %v", err)
	}

	withI, withIDeps := fakeDeps()
	withIDeps.In = strings.NewReader("1\n\n")
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
// The input is a reader that FAILS if read: an assertion on the rendered
// output would pass on a prompt that was drawn and then ignored.
type failingReader struct{ t *testing.T }

func (r failingReader) Read([]byte) (int, error) {
	r.t.Fatal("the checklist was opened on a live sandbox: nothing it collects can be mounted")
	return 0, io.EOF
}

func TestPromptModeDoesNotPromptWhenAttaching(t *testing.T) {
	denHome := denTestPrompting(t)

	// Create it once, with a selection.
	_, first := fakeDeps()
	first.In = strings.NewReader("1\n\n")
	first.IsTTY = func() bool { return true }
	if err := Spawn(context.Background(), denHome, Options{Nest: "generic"}, first); err != nil {
		t.Fatalf("first spawn: %v", err)
	}

	// Attach it. Same name, no flags — the checklist must stay shut.
	f, d := fakeDeps()
	d.In = failingReader{t}
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
// sandbox is silent for exactly the reason a prompting nest is: nothing a
// selection collects can be mounted on a VM whose mounts come from its creation.
//
// An order that depends on a configuration key would be two spawn sequences to
// keep true, and §6 describes one — this test is what makes the second reading
// fail out loud rather than drift.
//
// It asserts the SILENCE only: on an `api` nest (no `select: prompt`) the
// explanatory `--as` lines do not print, deliberately — the remedy answers a
// question this user never asked.
func TestInteractiveDoesNotPromptWhenAttaching(t *testing.T) {
	denHome, repos := denTestOptional(t)

	f, d := fakeDeps()
	d.In = failingReader{t}
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
	first.In = strings.NewReader("\n")
	first.IsTTY = func() bool { return true }
	if err := Spawn(context.Background(), denHome, Options{Nest: "crm"}, first); err != nil {
		t.Fatalf("first spawn: %v", err)
	}

	f, d := fakeDeps()
	d.In = failingReader{t}
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
// state: step 3 prints one `worktree <repo>: <path>` line per RESOLVED repo, so
// the set of those lines is a direct readout of r.Repos.
//
// Asserting the drift warnings are silent would not do: two empty comparisons
// pass whatever the selection did — the trap the "Guard on the guard" of
// TestInteractiveProducesTheSameArgvAsTheEquivalentWithout already names. The
// second half of this test scripts a MISMATCHED workspace and requires the
// warning to fire, so the silence of the first half means something.
//
// `-w` is also the case where re-derivation costs disk, not just noise: two
// worktrees created for repos this sandbox was never spawned with, which the
// user then has to find and remove.
func TestPromptModeAttachResolvesOnlyTheRecordedRepos(t *testing.T) {
	denHome, _ := denTestPromptingRepos(t)

	_, first := fakeDeps()
	first.In = strings.NewReader("1\n\n") // tick api, decline worker and docs
	first.IsTTY = func() bool { return true }
	if err := Spawn(context.Background(), denHome,
		Options{Nest: "generic", Worktree: "feat"}, first); err != nil {
		t.Fatalf("first spawn: %v", err)
	}
	// The mount comes from the RECORD, so the fixture and den agree on the
	// worktree path without this test recomputing the layout.
	recorded, err := manifest.Read(denHome, "generic.feat")
	if err != nil {
		t.Fatalf("reading the record the first spawn wrote: %v", err)
	}
	if len(recorded.Repos) != 1 || recorded.Repos[0].Name != "api" {
		t.Fatalf("the fixture must record exactly api; got %+v", recorded.Repos)
	}

	f, d := fakeDeps()
	d.In = failingReader{t}
	d.IsTTY = func() bool { return true }
	var out bytes.Buffer
	d.Out = &out
	f.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"generic.feat","status":"running","workspaces":["` +
			recorded.Repos[0].Mount + `"]}]}`),
	}
	if err := Spawn(context.Background(), denHome,
		Options{Nest: "generic", Worktree: "feat"}, d); err != nil {
		t.Fatalf("attaching spawn: %v", err)
	}

	var worktreeLines []string
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			worktreeLines = append(worktreeLines, line)
		}
	}
	if len(worktreeLines) != 1 || !strings.HasPrefix(worktreeLines[0], "worktree api: ") {
		t.Errorf("the attach must resolve exactly the recorded repo; worktree lines: %v\n%s",
			worktreeLines, out.String())
	}
	// On disk too: the line could be missing while the directory exists.
	for _, name := range []string{"worker", "docs"} {
		if _, err := os.Stat(filepath.Join(denHome, "worktrees", "feat", name)); err == nil {
			t.Errorf("a worktree was created for %s, which this sandbox was never spawned with", name)
		}
	}

	// Guard on the guard: with a workspace the VM does NOT mount, the warning
	// must fire. Without this, every assertion above passes on a spawn that
	// resolved nothing at all.
	mismatched, md := fakeDeps()
	md.In = failingReader{t}
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
	if !strings.Contains(mismatchedOut.String(), "is not mounted") {
		t.Errorf("a repo the VM does not mount must still be reported:\n%s", mismatchedOut.String())
	}
}

// No record — a sandbox created before records existed, or one created outside
// den. den must never refuse and strand a live VM (doctrine T13/T16), so the
// attach still works; what it must NOT do is report every optional repo as "not
// mounted", since the list it would compare against is a selection nobody made.
func TestPromptModeAttachWithoutARecordStillAttaches(t *testing.T) {
	denHome := denTestPrompting(t)

	f, d := fakeDeps()
	d.In = failingReader{t}
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
	d.In = failingReader{t}
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
