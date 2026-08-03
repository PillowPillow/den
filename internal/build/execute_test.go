package build

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/sbx"
)

// buildableStack writes a stack whose provision files really exist, since
// Execute reads them. Returns the stack and the den home it lives under.
func buildableStack(t *testing.T, name, image, base string, stepNames ...string) (*config.Stack, string) {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, "stacks", name)
	steps := make([]string, 0, len(stepNames))
	for _, n := range stepNames {
		p := filepath.Join(dir, n)
		writeFile(t, p, "echo "+n+"\n")
		steps = append(steps, p)
	}
	return &config.Stack{
		Name: name, Image: image, Base: base, Dir: dir,
		Provision: config.Provision{Steps: steps},
	}, home
}

// The sequence, in order, with the image name coming from den. This is the
// assertion the whole change exists for: `template save` carries `image:`, so
// it cannot disagree with what the spawn later looks for.
func TestExecuteRunsTheWholeSequenceInOrder(t *testing.T) {
	s, home := buildableStack(t, "devx", "devx:v1", "claude", "one.sh", "two.sh")
	fake := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(`{"sandboxes":[]}`)},
	}}
	if err := Execute(context.Background(), []Step{{Stack: s, Build: true}},
		Deps{Sbx: fake, DenHome: home}, &strings.Builder{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	scratch := ScratchDir(home, "devx")
	want := [][]string{
		{"ls", "--json"},
		{"create", "--name", "devx-build", "claude", scratch},
		{"exec", "devx-build", "--", "bash", "-lc", "echo one.sh\n"},
		{"exec", "devx-build", "--", "bash", "-lc", "echo two.sh\n"},
		{"stop", "devx-build"},
		{"template", "save", "devx-build", "devx:v1"},
		{"rm", "--force", "devx-build"},
	}
	if len(fake.Calls) != len(want) {
		t.Fatalf("calls =\n  %v\nwant %d calls", fake.Calls, len(want))
	}
	for i := range want {
		if !slices.Equal(fake.Calls[i], want[i]) {
			t.Errorf("call %d =\n  %v\nwant\n  %v", i, fake.Calls[i], want[i])
		}
	}
}

// derivedStackChain is a two-step chain — a root stack, then a stack derived
// FROM it — shared by the two tests below that need a real chain rather than
// one stack in isolation: the derived-argv assertion and the leftover-before-
// the-first-create assertion both need devx built (or refused) ahead of
// dgdevx.
func derivedStackChain(t *testing.T) (root, derived *config.Stack, home string) {
	t.Helper()
	root, home = buildableStack(t, "devx", "devx:v1", "claude", "one.sh")
	dir := filepath.Join(home, "stacks", "dgdevx")
	step := filepath.Join(dir, "one.sh")
	writeFile(t, step, "echo one.sh\n")
	derived = &config.Stack{
		Name: "dgdevx", Image: "dgdevx:v1", Parent: "devx", ParentImage: "devx:v1", Dir: dir,
		Provision: config.Provision{Steps: []string{step}},
	}
	return root, derived, home
}

// The derived path — a stack built FROM another stack's image, via
// `--template` — is untested by every test above: buildableStack only ever
// sets Base. It is also the multi-stack case the single-`ls` hoist changes
// the observable sequence for: ONE `ls --json` for the WHOLE chain, at the
// very top, not one interleaved before each stack's own create. And ancestors
// build FIRST: dgdevx's `--template` argument is devx's IMAGE, which only
// exists once devx's own sequence — including `template save` — has already
// run.
func TestExecuteBuildsADerivedStackFromItsParentImageAfterItsAncestor(t *testing.T) {
	root, derived, home := derivedStackChain(t)
	fake := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(`{"sandboxes":[]}`)},
	}}
	if err := Execute(context.Background(),
		[]Step{{Stack: root, Build: true}, {Stack: derived, Build: true}},
		Deps{Sbx: fake, DenHome: home}, &strings.Builder{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	rootScratch := ScratchDir(home, "devx")
	derivedScratch := ScratchDir(home, "dgdevx")
	want := [][]string{
		{"ls", "--json"}, // ONE call for the whole chain, not one per stack
		{"create", "--name", "devx-build", "claude", rootScratch},
		{"exec", "devx-build", "--", "bash", "-lc", "echo one.sh\n"},
		{"stop", "devx-build"},
		{"template", "save", "devx-build", "devx:v1"},
		{"rm", "--force", "devx-build"},
		{"create", "--name", "dgdevx-build", "--template", "devx:v1", sbx.PositionalAgent, derivedScratch},
		{"exec", "dgdevx-build", "--", "bash", "-lc", "echo one.sh\n"},
		{"stop", "dgdevx-build"},
		{"template", "save", "dgdevx-build", "dgdevx:v1"},
		{"rm", "--force", "dgdevx-build"},
	}
	if len(fake.Calls) != len(want) {
		t.Fatalf("calls =\n  %v\nwant %d calls", fake.Calls, len(want))
	}
	for i := range want {
		if !slices.Equal(fake.Calls[i], want[i]) {
			t.Errorf("call %d =\n  %v\nwant\n  %v", i, fake.Calls[i], want[i])
		}
	}
}

// THE payoff of hoisting the pre-existing-sandbox check to a SINGLE ls for
// the whole chain: a leftover belonging to the LAST stack of a two-stack
// chain must be caught before the FIRST stack is even created. Before the
// hoist, this check lived inside buildOne and ran once per stack — right
// before that stack's own create — so devx would have been built in full
// (minutes of work) before dgdevx's own turn ever discovered the leftover.
func TestExecuteRefusesALeftoverBeforeBuildingAnyEarlierStack(t *testing.T) {
	root, derived, home := derivedStackChain(t)
	fake := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(`{"sandboxes":[{"name":"dgdevx-build","status":"running"}]}`)},
	}}
	err := Execute(context.Background(),
		[]Step{{Stack: root, Build: true}, {Stack: derived, Build: true}},
		Deps{Sbx: fake, DenHome: home}, &strings.Builder{})
	if err == nil {
		t.Fatal("Execute built over a leftover build sandbox belonging to a later stack")
	}
	if !strings.Contains(err.Error(), "sbx rm --force dgdevx-build") {
		t.Errorf("error %q does not name the offending sandbox", err)
	}
	if fake.HasCalled("create") {
		t.Error("den created devx's build sandbox before discovering dgdevx-build's leftover — " +
			"the whole-chain ls hoist is not doing its job")
	}
}

// The failing step is NAMED. Without it the user sees a wall of build log and
// an exit code, and has to count the stages to learn which script died.
func TestExecuteNamesTheFailingStep(t *testing.T) {
	s, home := buildableStack(t, "devx", "devx:v1", "claude", "one.sh", "two.sh")
	fake := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(`{"sandboxes":[]}`)},
		"exec devx-build -- bash -lc echo two.sh\n": {Err: errors.New("exit status 1")},
	}}
	err := Execute(context.Background(), []Step{{Stack: s, Build: true}},
		Deps{Sbx: fake, DenHome: home}, &strings.Builder{})
	if err == nil {
		t.Fatal("Execute succeeded over a failing step")
	}
	for _, want := range []string{"devx", "step 2/2", "two.sh"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// THE message shape spec §6 promises, and what it must NOT contain.
//
// `%w` around the *sbx.ExecError rendered `Bin + strings.Join(Args, " ")`
// first, and Args holds the whole includes+step text: a real failure printed
// the entire provisioning script, then a lone `: `, and only on the last line
// the one actionable fact. Spec §14.1 names that shape as a defect of the
// pre-#8 experience; the payload made it worse. The cause now comes from
// sbx.ExecError.Detail — the stderr — and the argv is gone.
func TestExecuteNamesTheFailingStepWithoutInliningThePayload(t *testing.T) {
	s, home := buildableStack(t, "devx", "devx:v1", "claude", "one.sh")
	payload := "echo one.sh\n"
	fake := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(`{"sandboxes":[]}`)},
		"exec devx-build -- bash -lc " + payload: {Err: &sbx.ExecError{
			Bin:    "sbx",
			Args:   []string{"exec", "devx-build", "--", "bash", "-lc", payload},
			Stderr: "E: Unable to locate package ripgrep",
			Err:    errors.New("exit status 1"),
		}},
	}}
	err := Execute(context.Background(), []Step{{Stack: s, Build: true}},
		Deps{Sbx: fake, DenHome: home}, &strings.Builder{})
	if err == nil {
		t.Fatal("Execute succeeded over a failing step")
	}
	msg := err.Error()
	if strings.Contains(msg, "echo one.sh") {
		t.Errorf("the provisioning payload is inlined in the message, ahead of the cause:\n%s", msg)
	}
	if strings.Contains(msg, "-lc") {
		t.Errorf("the sbx argv is still rendered in the message:\n%s", msg)
	}
	if !strings.Contains(msg, "E: Unable to locate package ripgrep") {
		t.Errorf("message %q drops the stderr, which is the only actionable line", msg)
	}
	// The chain SURVIVES the reformatting: dropping the argv from the rendering
	// must not drop the error from the chain, or every downstream errors.As on
	// an exit code stops working.
	var execErr *sbx.ExecError
	if !errors.As(err, &execErr) {
		t.Errorf("errors.As no longer reaches the *sbx.ExecError through %T", err)
	}
}

// Same treatment for the CREATE failure, whose argv carries the scratch path
// and, on a derived stack, the parent's `--template`. den has already named the
// stack and the operation; the argv in front of the cause is the same defect at
// a smaller scale.
func TestExecuteDoesNotInlineTheCreateArgvOnAFailedCreate(t *testing.T) {
	s, home := buildableStack(t, "devx", "devx:v1", "claude", "one.sh")
	scratch := ScratchDir(home, "devx")
	fake := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(`{"sandboxes":[]}`)},
		"create --name devx-build claude " + scratch: {Err: &sbx.ExecError{
			Bin:    "sbx",
			Args:   []string{"create", "--name", "devx-build", "claude", scratch},
			Stderr: "ERROR: no space left on device",
			Err:    errors.New("exit status 1"),
		}},
	}}
	err := Execute(context.Background(), []Step{{Stack: s, Build: true}},
		Deps{Sbx: fake, DenHome: home}, &strings.Builder{})
	if err == nil {
		t.Fatal("Execute succeeded over a failing create")
	}
	msg := err.Error()
	if strings.Contains(msg, scratch) {
		t.Errorf("the create argv (with its scratch path) is inlined ahead of the cause:\n%s", msg)
	}
	if !strings.Contains(msg, "no space left on device") {
		t.Errorf("message %q drops the stderr", msg)
	}
}

// The provisioning output reaches the user. Every step's bytes used to be
// thrown away — `if _, err := d.Sbx.Run(...)` at all five call sites — leaving
// four minutes of apt-get represented by a stack name and an exit code.
func TestExecuteWritesEveryStepsOutput(t *testing.T) {
	s, home := buildableStack(t, "devx", "devx:v1", "claude", "one.sh", "two.sh")
	fake := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(`{"sandboxes":[]}`)},
		"exec devx-build -- bash -lc echo one.sh\n": {Output: []byte("installing go\n")},
		"exec devx-build -- bash -lc echo two.sh\n": {Output: []byte("installing node\n")},
	}}
	var out strings.Builder
	if err := Execute(context.Background(), []Step{{Stack: s, Build: true}},
		Deps{Sbx: fake, DenHome: home}, &out); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"installing go", "installing node"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output %q does not carry %q", out.String(), want)
		}
	}
}

// On FAILURE the log must still arrive, and arrive FIRST: the user reads what
// the step was doing, then why it stopped. A log written after the error — or
// not at all — makes the cause unreadable exactly when it matters.
//
// This also pins the teardown-failure path in the same run, because the two
// share the ordering: the `rm --force` warning is emitted by a defer, so it
// lands after the step log and before the returned error is ever rendered. And
// a teardown that FAILS must not overwrite the error that caused the teardown —
// losing "step 1/1 failed" behind "could not remove the sandbox" would report
// the consequence instead of the cause.
func TestExecuteWritesTheFailingStepsOutputBeforeItsErrorAndWarnsOnAFailedTeardown(t *testing.T) {
	s, home := buildableStack(t, "devx", "devx:v1", "claude", "one.sh")
	fake := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(`{"sandboxes":[]}`)},
		"exec devx-build -- bash -lc echo one.sh\n": {
			Output: []byte("Reading package lists...\n"),
			Err:    &sbx.ExecError{Bin: "sbx", Stderr: "E: Unable to locate package ripgrep", Err: errors.New("exit status 1")},
		},
		"rm --force devx-build": {Err: errors.New("sandbox is busy")},
	}}
	var out strings.Builder
	err := Execute(context.Background(), []Step{{Stack: s, Build: true}},
		Deps{Sbx: fake, DenHome: home}, &out)
	if err == nil {
		t.Fatal("Execute succeeded over a failing step")
	}
	log := out.String()
	if !strings.Contains(log, "Reading package lists...") {
		t.Errorf("the failing step's output was discarded:\n%s", log)
	}
	// The teardown warning goes to the SAME stream, after the step's log.
	warn := strings.Index(log, "warning: build sandbox devx-build could not be removed")
	if warn < 0 {
		t.Fatalf("a teardown that failed was silent:\n%s", log)
	}
	if warn < strings.Index(log, "Reading package lists...") {
		t.Errorf("the teardown warning precedes the step log it should follow:\n%s", log)
	}
	// And the returned error is still the STEP's, not the teardown's: the
	// teardown failure is a warning, it does not replace the cause.
	if !strings.Contains(err.Error(), "step 1/1") {
		t.Errorf("error = %q, want the failing step, not the teardown", err)
	}
	if strings.Contains(err.Error(), "could not be removed") {
		t.Errorf("the teardown failure replaced the cause: %q", err)
	}
}

// Teardown is an INVARIANT, not a `trap` each script had to remember. A failed
// build must not leave the VM behind, and it must not save an image either.
func TestExecuteTearsDownAfterAFailedStep(t *testing.T) {
	s, home := buildableStack(t, "devx", "devx:v1", "claude", "one.sh")
	fake := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(`{"sandboxes":[]}`)},
		"exec devx-build -- bash -lc echo one.sh\n": {Err: errors.New("boom")},
	}}
	_ = Execute(context.Background(), []Step{{Stack: s, Build: true}},
		Deps{Sbx: fake, DenHome: home}, &strings.Builder{})
	if !fake.HasCalled("rm", "--force", "devx-build") {
		t.Error("no teardown after a failed step — the build VM leaks")
	}
	if fake.HasCalled("template", "save") {
		t.Error("den saved an image over a failed build")
	}
}

// A pre-existing `<stack>-build` is a REFUSAL, never a blind `rm --force`:
// that name is a legal nest name, so cleaning it up could destroy a real
// sandbox of the user's. The message names the remedy.
func TestExecuteRefusesAPreexistingBuildSandbox(t *testing.T) {
	s, home := buildableStack(t, "devx", "devx:v1", "claude", "one.sh")
	fake := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(`{"sandboxes":[{"name":"devx-build","status":"running"}]}`)},
	}}
	err := Execute(context.Background(), []Step{{Stack: s, Build: true}},
		Deps{Sbx: fake, DenHome: home}, &strings.Builder{})
	if err == nil {
		t.Fatal("Execute reused a pre-existing build sandbox")
	}
	if !strings.Contains(err.Error(), "sbx rm --force devx-build") {
		t.Errorf("error %q does not name the remedy", err)
	}
	if fake.HasCalled("create") {
		t.Error("den created over a pre-existing sandbox")
	}
}

// Every provision file of the WHOLE chain is read before the first create.
// Building four minutes of base image to then discover a missing script spends
// that time to reach a refusal den could make instantly.
func TestExecuteReadsEveryProvisionFileBeforeTheFirstCreate(t *testing.T) {
	good, home := buildableStack(t, "devx", "devx:v1", "claude", "one.sh")
	broken := &config.Stack{
		Name: "dgdevx", Image: "dgdevx:v1", Base: "claude",
		Dir:       filepath.Join(home, "stacks", "dgdevx"),
		Provision: config.Provision{Steps: []string{filepath.Join(home, "stacks", "dgdevx", "gone.sh")}},
	}
	fake := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(`{"sandboxes":[]}`)},
	}}
	err := Execute(context.Background(),
		[]Step{{Stack: good, Build: true}, {Stack: broken, Build: true}},
		Deps{Sbx: fake, DenHome: home}, &strings.Builder{})
	if err == nil {
		t.Fatal("Execute started a chain with an unreadable step")
	}
	if fake.HasCalled("create") {
		t.Error("den created a VM before discovering the unreadable step")
	}
}

// The scratch is EMPTY at every build, not merely present. Spec §6 calls it
// "un dossier **vide** monté dans la VM de build"; the VM has it mounted
// read-write, so after one build it is whatever that build left there, and the
// next build would mount the residue. A build whose result depends on what a
// previous build happened to write is the one property a reproducible image
// must not have.
func TestExecuteEmptiesTheScratchBeforeEachBuild(t *testing.T) {
	s, home := buildableStack(t, "devx", "devx:v1", "claude", "one.sh")
	scratch := ScratchDir(home, "devx")
	// Stand in for what the previous build's VM wrote into the mount.
	residue := filepath.Join(scratch, "leftover.tar.gz")
	writeFile(t, residue, "from the last build\n")

	fake := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(`{"sandboxes":[]}`)},
	}}
	if err := Execute(context.Background(), []Step{{Stack: s, Build: true}},
		Deps{Sbx: fake, DenHome: home}, &strings.Builder{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if _, err := os.Stat(residue); !os.IsNotExist(err) {
		t.Errorf("%s survived the build: the next VM mounts the last build's residue", residue)
	}
	// Emptied, and still THERE: `sbx create` needs the path to exist.
	if fi, err := os.Stat(scratch); err != nil || !fi.IsDir() {
		t.Errorf("Stat(%s) = (%v, %v), want an existing directory", scratch, fi, err)
	}
}

// The scratch path is derived from DenHome and the stack name, and an empty
// either side collapses it — to the SHARED `cache/build` root, whose RemoveAll
// would wipe every stack's scratch, or to a RELATIVE path under whatever
// directory den runs from. Unreachable through the CLI, guarded anyway: Deps
// and Step are exported bare structs, the doctrine sbx.CreateArgv states for
// its own inputs.
func TestExecuteRefusesToPrepareAScratchFromAnEmptyDenHome(t *testing.T) {
	s, _ := buildableStack(t, "devx", "devx:v1", "claude", "one.sh")
	fake := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(`{"sandboxes":[]}`)},
	}}
	err := Execute(context.Background(), []Step{{Stack: s, Build: true}},
		Deps{Sbx: fake, DenHome: ""}, &strings.Builder{})
	if err == nil {
		t.Fatal("Execute prepared a build scratch from an empty den home")
	}
	if fake.HasCalled("create") {
		t.Errorf("den created a VM over a scratch it should have refused; calls: %v", fake.Calls)
	}
}

// A skipped step is ANNOUNCED, never silent: "already built" and "den forgot
// it" must not look the same from the outside.
func TestExecuteAnnouncesSkippedSteps(t *testing.T) {
	s := &config.Stack{Name: "devx", Image: "devx:v1"}
	var out strings.Builder
	fake := &sbx.Fake{}
	if err := Execute(context.Background(),
		[]Step{{Stack: s, Skipped: "image devx:v1 already built (--force rebuilds it)"}},
		Deps{Sbx: fake, DenHome: t.TempDir()}, &out); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out.String(), "already built") {
		t.Errorf("output %q does not carry the skip reason", out.String())
	}
	if len(fake.Calls) != 0 {
		t.Errorf("a skipped step touched sbx: %v", fake.Calls)
	}
}

// The whole argv sequence of a two-stack chain, in one artefact. It is what
// the previous model could not have: running a real build.sh is not testable,
// so the ordering was only ever asserted piecemeal.
//
// Paths are rewritten to <scratch> so the golden does not carry a t.TempDir().
//
// THE BLANK LINES IN THE GOLDEN ARE LOAD-BEARING — do not "clean them up".
// Each recorded call is one line, but the last argument of an `exec` is the
// PAYLOAD, which contains newlines of its own, so one call spans several lines
// of the file. devx's exec shows both sources at once: the blank line after
// `common::go_tools() { :; }` is the separator Provisioning.Payload puts
// between the includes and the step (the include's own trailing newline, then
// the joining one), and the blank line after `common::go_tools` is the step
// file's trailing newline followed by the line this loop adds per call.
// Deleting either would assert a payload den does not send — a missing
// separator welds the include's last line onto the step's first, and a missing
// trailing newline shifts every line number a shell error reports.
func TestExecuteSequenceGolden(t *testing.T) {
	home := t.TempDir()
	devxDir := filepath.Join(home, "stacks", "devx")
	writeFile(t, filepath.Join(devxDir, "go.sh"), "common::go_tools\n")
	writeFile(t, filepath.Join(home, "lib", "common.sh"), "common::go_tools() { :; }\n")
	devx := &config.Stack{
		Name: "devx", Image: "devx:v1", Base: "claude", Dir: devxDir,
		Provision: config.Provision{
			Includes: []string{filepath.Join(home, "lib", "common.sh")},
			Steps:    []string{filepath.Join(devxDir, "go.sh")},
		},
	}
	dgdevxDir := filepath.Join(home, "stacks", "dgdevx")
	writeFile(t, filepath.Join(dgdevxDir, "glab.sh"), "echo glab\n")
	dgdevx := &config.Stack{
		Name: "dgdevx", Image: "dgdevx:v1", Parent: "devx", ParentImage: "devx:v1", Dir: dgdevxDir,
		Provision: config.Provision{Steps: []string{filepath.Join(dgdevxDir, "glab.sh")}},
	}

	fake := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(`{"sandboxes":[]}`)},
	}}
	if err := Execute(context.Background(),
		[]Step{{Stack: devx, Build: true}, {Stack: dgdevx, Build: true}},
		Deps{Sbx: fake, DenHome: home}, &strings.Builder{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var got strings.Builder
	for _, call := range fake.Calls {
		line := strings.Join(call, " ")
		line = strings.ReplaceAll(line, ScratchDir(home, "devx"), "<scratch:devx>")
		line = strings.ReplaceAll(line, ScratchDir(home, "dgdevx"), "<scratch:dgdevx>")
		got.WriteString(line + "\n")
	}
	golden := filepath.Join("testdata", "sequence-two-stacks.golden")
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("reading %s: %v — there is no -update flag, write it by hand", golden, err)
	}
	if got.String() != string(want) {
		t.Errorf("sequence mismatch\n--- got ---\n%s\n--- want (%s) ---\n%s", got.String(), golden, want)
	}
}
