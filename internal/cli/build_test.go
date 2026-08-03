package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/doctor"
	"github.com/PillowPillow/den/internal/sbx"
)

// buildStack writes one stack.yaml plus the provision step file its
// `provision.steps` names — Execute reads that file for real, unlike the old
// build.sh model where the CLI only had to record that a script RAN.
func buildStack(t *testing.T, home, name, yaml string) {
	t.Helper()
	dir := filepath.Join(home, "stacks", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stack.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "step.sh"), []byte("echo "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// buildDenHome writes a den home with a three-stack chain — base ← mid ← leaf
// — each declaring a real `provision.steps` entry.
func buildDenHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	buildStack(t, home, "base", "image: base:v1\nbase: claude\nprovision:\n  steps: [step.sh]\n")
	buildStack(t, home, "mid", "image: mid:v1\nparent: base\nprovision:\n  steps: [step.sh]\n")
	buildStack(t, home, "leaf", "image: leaf:v1\nparent: mid\nprovision:\n  steps: [step.sh]\n")
	return home
}

// builtStacks names, in build order, every stack `den build` actually ran
// through the sequence — read off the `template save <name>-build <image>`
// calls, which is the ONE call in the sequence naming exactly one stack.
// This is the new model's replacement for the old recordingBuild.ran: there is
// no script left to record having run, so the assertion moves to the sbx argv
// the real sequence produces.
func builtStacks(f *sbx.Fake) []string {
	var built []string
	for _, call := range f.Calls {
		if len(call) == 4 && call[0] == "template" && call[1] == "save" {
			built = append(built, strings.TrimSuffix(call[2], "-build"))
		}
	}
	return built
}

// runBuild runs `den build` through the REAL command tree, with Deps built BY
// HAND — never SystemDeps, whose Sbx would spawn a real `sbx` binary.
func runBuild(t *testing.T, f sbx.Runner, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	deps := Deps{Doctor: doctor.FakeDeps(), Sbx: f}
	return executeCmdSeparateStreams(t, NewRootCmdWith(deps), args...)
}

// noPriorSandboxes is what every build that reaches Execute needs: the ONE
// `sbx ls --json` Execute issues for the whole chain, reporting no leftover
// `<stack>-build` sandbox.
var noPriorSandboxes = sbx.Response{Output: []byte(`{"sandboxes":[]}`)}

func TestBuildWithoutAnArgumentBuildsEverythingInOrder(t *testing.T) {
	home := buildDenHome(t)
	f := &sbx.Fake{Responses: map[string]sbx.Response{"ls --json": noPriorSandboxes}}

	if _, _, err := runBuild(t, f, "--den-home", home, "build"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Roots are walked in NAME order (base, leaf, mid), and each one's
	// ancestors come out first: base, then leaf pulls mid ahead of itself.
	if got := strings.Join(builtStacks(f), ","); got != "base,mid,leaf" {
		t.Errorf("built = %v, want base,mid,leaf", builtStacks(f))
	}
	// And it spends no process on an inventory it does not consult: `den build`
	// rebuilds everything by definition.
	if f.HasCalled("template", "ls", "--json") {
		t.Errorf("`den build` must not read the image inventory; calls: %v", f.Calls)
	}
}

// `den build <stack>`: the ancestors are skipped when their image is there,
// the target never is.
func TestBuildOnATargetSkipsTheAncestorsAlreadyBuilt(t *testing.T) {
	home := buildDenHome(t)
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": noPriorSandboxes,
		"template ls --json": {Output: []byte(
			`{"images":[{"repository":"docker.io/library/base","tag":"v1"}]}`)},
	}}

	stdout, _, err := runBuild(t, f, "--den-home", home, "build", "leaf")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.Join(builtStacks(f), ","); got != "mid,leaf" {
		t.Errorf("built = %v, want mid,leaf — base is already built, leaf is the target", builtStacks(f))
	}
	if !strings.Contains(stdout, "base") || !strings.Contains(stdout, "skipped") {
		t.Errorf("stdout = %q, want the skipped ancestor announced by name", stdout)
	}
}

func TestBuildForcePropagatesToTheAncestors(t *testing.T) {
	home := buildDenHome(t)
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": noPriorSandboxes,
		// Deliberately AVAILABLE BUT UNREAD: --force takes the
		// `force || s.Name == target` branch in Plan before `images.Has` is
		// ever called, so this response proves --force ignores the inventory
		// even when it says the ancestors are already built — not merely that
		// den never asked in the first place.
		"template ls --json": {Output: []byte(
			`{"images":[{"repository":"docker.io/library/base","tag":"v1"},
			            {"repository":"docker.io/library/mid","tag":"v1"}]}`)},
	}}

	if _, _, err := runBuild(t, f, "--den-home", home, "build", "leaf", "--force"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.Join(builtStacks(f), ","); got != "base,mid,leaf" {
		t.Errorf("built = %v, want the whole chain under --force", builtStacks(f))
	}
	if f.HasCalled("template", "ls", "--json") {
		t.Errorf("--force must not read the image inventory; calls: %v", f.Calls)
	}
}

// A broken stack is reported BY NAME on stderr and not built — a `den build`
// that silently walked past it would look like it built everything.
func TestBuildNamesABrokenStackWithoutFailing(t *testing.T) {
	home := buildDenHome(t)
	dir := filepath.Join(home, "stacks", "typo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stack.yaml"), []byte("imag: nope\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &sbx.Fake{Responses: map[string]sbx.Response{"ls --json": noPriorSandboxes}}

	_, stderr, err := runBuild(t, f, "--den-home", home, "build")
	if err != nil {
		t.Fatalf("a broken stack must not fail the whole build: %v", err)
	}
	if !strings.Contains(stderr, "typo") {
		t.Errorf("stderr = %q, want the broken stack named", stderr)
	}
	if strings.Contains(strings.Join(builtStacks(f), ","), "typo") {
		t.Errorf("built = %v, want the broken stack not built", builtStacks(f))
	}
}

// The other half of the doctrine above, and the defect measured on this branch
// (2026-08-03): a broken stack used as a `parent:` made `den build` refuse
// and build NOTHING. The healthy chain must still be built, and the stack whose
// ancestry reaches the broken one must be named on stderr with the stack at
// fault — otherwise it is a stack den silently forgot.
func TestBuildNamesAStackWhoseParentIsBrokenWithoutFailing(t *testing.T) {
	home := buildDenHome(t)
	dir := filepath.Join(home, "stacks", "typo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stack.yaml"), []byte("imag: nope\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Its own `provision.steps`, so that not being built can only come from its
	// ancestry — never from den having nothing to run.
	buildStack(t, home, "orphan", "image: orphan:v1\nparent: typo\nprovision:\n  steps: [step.sh]\n")
	f := &sbx.Fake{Responses: map[string]sbx.Response{"ls --json": noPriorSandboxes}}

	_, stderr, err := runBuild(t, f, "--den-home", home, "build")
	if err != nil {
		t.Fatalf("a broken PARENT must not fail the whole build: %v", err)
	}
	if got := strings.Join(builtStacks(f), ","); got != "base,mid,leaf" {
		t.Errorf("built = %v, want the healthy chain built anyway", builtStacks(f))
	}
	if !strings.Contains(stderr, "orphan") || !strings.Contains(stderr, "typo") {
		t.Errorf("stderr = %q, want the excluded stack named with the ancestor at fault", stderr)
	}
}

// Same for a `parent:` naming a stack that does not exist: the healthy chain is
// built, the stack that cannot be is named — and here the remedy IS its own
// `parent:` line, so the report names its file. `den build <that stack>` keeps
// refusing; only the no-target form walks around it.
func TestBuildNamesAStackWhoseParentDoesNotExistWithoutFailing(t *testing.T) {
	home := buildDenHome(t)
	buildStack(t, home, "orphan", "image: orphan:v1\nparent: nowhere\nprovision:\n  steps: [step.sh]\n")
	f := &sbx.Fake{Responses: map[string]sbx.Response{"ls --json": noPriorSandboxes}}

	_, stderr, err := runBuild(t, f, "--den-home", home, "build")
	if err != nil {
		t.Fatalf("a missing PARENT must not fail the whole build: %v", err)
	}
	if got := strings.Join(builtStacks(f), ","); got != "base,mid,leaf" {
		t.Errorf("built = %v, want the healthy chain built anyway", builtStacks(f))
	}
	dir := filepath.Join(home, "stacks", "orphan")
	for _, want := range []string{"orphan", "nowhere", "parent:", filepath.Join(dir, "stack.yaml")} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr, want)
		}
	}
}

func TestBuildRefusesAnUnknownStack(t *testing.T) {
	home := buildDenHome(t)
	f := &sbx.Fake{}

	_, _, err := runBuild(t, f, "--den-home", home, "build", "nope")
	if err == nil {
		t.Fatal("expected a refusal on a stack that is not declared")
	}
	if !strings.Contains(err.Error(), "base") {
		t.Errorf("message = %q, want it to list the declared stacks", err)
	}
	if len(builtStacks(f)) != 0 {
		t.Errorf("built = %v, want nothing built after the refusal", builtStacks(f))
	}
}

// A den with no stacks at all says where to declare one, rather than exiting 0
// on a silent no-op.
func TestBuildOnAnEmptyDenSaysWhereStacksGo(t *testing.T) {
	home := t.TempDir()
	stdout, _, err := runBuild(t, &sbx.Fake{}, "--den-home", home, "build")
	if err != nil {
		t.Fatalf("an empty den is not an error: %v", err)
	}
	if !strings.Contains(stdout, filepath.Join(home, "stacks")) {
		t.Errorf("stdout = %q, want the stacks directory named", stdout)
	}
}

// A den whose ONLY stack is broken produces an empty chain, and that used to
// return before the report loop: the user was told "no stack declared" about a
// stack they had just written. The two diagnoses are different — one says where
// to create a stack, the other says which one to fix.
func TestBuildOnADenWhoseOnlyStackIsBrokenSaysWhatToFix(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "stacks", "typo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stack.yaml"), []byte("imag: nope\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := runBuild(t, &sbx.Fake{}, "--den-home", home, "build")
	if err != nil {
		t.Fatalf("a broken stack must not fail the build: %v", err)
	}
	if !strings.Contains(stderr, "typo") {
		t.Errorf("stderr = %q, want the broken stack named even when nothing is left to build", stderr)
	}
	if strings.Contains(stdout, "no stack declared") {
		t.Errorf("stdout = %q, want it not to claim the den declares nothing: typo is declared, it is broken", stdout)
	}
}

func TestBuildRejectsTwoArguments(t *testing.T) {
	f := &sbx.Fake{}
	_, _, err := runBuild(t, f, "--den-home", t.TempDir(), "build", "a", "b")
	if err == nil {
		t.Fatal("`den build a b` must be rejected on argument count")
	}
	if len(f.Calls) != 0 {
		t.Errorf("a rejected argument count must reach sbx for nothing; calls: %v", f.Calls)
	}
}

// The bench defect (2026-08-03): a den holding one stack den cannot build —
// `image:` and no `provision.steps`, i.e. an image sbx pulls — made `den
// build` refuse and build NOTHING. den's own spawn check already treats that
// shape as legitimate; the two must agree.
func TestBuildSkipsAStackItCannotBuildInsteadOfRefusing(t *testing.T) {
	home := buildDenHome(t)
	dir := filepath.Join(home, "stacks", "pulled")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stack.yaml"),
		[]byte("image: docker/sandbox-templates:shell-docker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &sbx.Fake{Responses: map[string]sbx.Response{"ls --json": noPriorSandboxes}}

	stdout, _, err := runBuild(t, f, "--den-home", home, "build")
	if err != nil {
		t.Fatalf("a stack den cannot build must not refuse the whole command: %v", err)
	}
	if got := strings.Join(builtStacks(f), ","); got != "base,mid,leaf" {
		t.Errorf("built = %v, want every buildable stack", builtStacks(f))
	}
	if !strings.Contains(stdout, "pulled") || !strings.Contains(stdout, "provision.steps") {
		t.Errorf("stdout = %q, want the skipped stack named with the reason", stdout)
	}
}

// Naming it explicitly is the other half: the user asked for that build, and a
// skip line would read as success.
func TestBuildRefusesANamedStackItCannotBuild(t *testing.T) {
	home := buildDenHome(t)
	dir := filepath.Join(home, "stacks", "pulled")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stack.yaml"), []byte("image: pulled:v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &sbx.Fake{}

	_, _, err := runBuild(t, f, "--den-home", home, "build", "pulled")
	if err == nil {
		t.Fatal("expected a refusal on a named stack with no `provision.steps`")
	}
	if !strings.Contains(err.Error(), "provision.steps") {
		t.Errorf("message = %q, want it to name the missing declaration", err)
	}
	if len(builtStacks(f)) != 0 {
		t.Errorf("built = %v, want nothing built", builtStacks(f))
	}
}
