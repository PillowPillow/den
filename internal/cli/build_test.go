package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/build"
	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/doctor"
	"github.com/PillowPillow/den/internal/sbx"
)

// recordingBuild is the injected build.Script. Nothing here spawns a process:
// issue #8 requires it explicitly, and the repo's suite forbids it anyway —
// the real build.ExecScript runs a USER'S shell script, so a test tree that
// inherited it would execute arbitrary code on the machine running `go test`.
type recordingBuild struct{ ran []string }

func (r *recordingBuild) Run(_ context.Context, s *config.Stack, _ io.Writer) error {
	r.ran = append(r.ran, s.Name)
	return nil
}

var _ build.Script = (*recordingBuild)(nil)

// buildDenHome writes a den home with a three-stack chain and a build.sh for
// each: base ← mid ← leaf.
func buildDenHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for _, s := range []struct{ name, yaml string }{
		{"base", "image: base:v1\n"},
		{"mid", "image: mid:v1\nparent: base\n"},
		{"leaf", "image: leaf:v1\nparent: mid\n"},
	} {
		dir := filepath.Join(home, "stacks", s.name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "stack.yaml"), []byte(s.yaml), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, build.ScriptName), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

// runBuild runs `den build` through the REAL command tree, with Deps built BY
// HAND — never SystemDeps, whose Build field is the real script runner.
func runBuild(t *testing.T, f sbx.Runner, script build.Script, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	deps := Deps{Doctor: doctor.FakeDeps(), Sbx: f, Build: script}
	return executeCmdSeparateStreams(t, NewRootCmdWith(deps), args...)
}

func TestBuildWithoutAnArgumentBuildsEverythingInOrder(t *testing.T) {
	home := buildDenHome(t)
	f := &sbx.Fake{}
	script := &recordingBuild{}

	if _, _, err := runBuild(t, f, script, "--den-home", home, "build"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Roots are walked in NAME order (base, leaf, mid), and each one's
	// ancestors come out first: base, then leaf pulls mid ahead of itself.
	if got := strings.Join(script.ran, ","); got != "base,mid,leaf" {
		t.Errorf("ran = %v, want base,mid,leaf", script.ran)
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
		"template ls --json": {Output: []byte(
			`{"images":[{"repository":"docker.io/library/base","tag":"v1"}]}`)},
	}}
	script := &recordingBuild{}

	stdout, _, err := runBuild(t, f, script, "--den-home", home, "build", "leaf")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.Join(script.ran, ","); got != "mid,leaf" {
		t.Errorf("ran = %v, want mid,leaf — base is already built, leaf is the target", script.ran)
	}
	if !strings.Contains(stdout, "base") || !strings.Contains(stdout, "skipped") {
		t.Errorf("stdout = %q, want the skipped ancestor announced by name", stdout)
	}
}

func TestBuildForcePropagatesToTheAncestors(t *testing.T) {
	home := buildDenHome(t)
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"template ls --json": {Output: []byte(
			`{"images":[{"repository":"docker.io/library/base","tag":"v1"},
			            {"repository":"docker.io/library/mid","tag":"v1"}]}`)},
	}}
	script := &recordingBuild{}

	if _, _, err := runBuild(t, f, script, "--den-home", home, "build", "leaf", "--force"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.Join(script.ran, ","); got != "base,mid,leaf" {
		t.Errorf("ran = %v, want the whole chain under --force", script.ran)
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
	script := &recordingBuild{}

	_, stderr, err := runBuild(t, &sbx.Fake{}, script, "--den-home", home, "build")
	if err != nil {
		t.Fatalf("a broken stack must not fail the whole build: %v", err)
	}
	if !strings.Contains(stderr, "typo") {
		t.Errorf("stderr = %q, want the broken stack named", stderr)
	}
	if strings.Contains(strings.Join(script.ran, ","), "typo") {
		t.Errorf("ran = %v, want the broken stack not built", script.ran)
	}
}

// The other half of the doctrine above, and the defect measured on this branch
// (2026-08-03): a broken stack used as a `parent:` made `den build` refuse and
// build NOTHING. The healthy chain must still be built, and the stack whose
// ancestry reaches the broken one must be named on stderr with the stack at
// fault — otherwise it is a stack den silently forgot.
func TestBuildNamesAStackWhoseParentIsBrokenWithoutFailing(t *testing.T) {
	home := buildDenHome(t)
	for _, s := range []struct{ name, yaml string }{
		{"typo", "imag: nope\n"},
		// A build.sh of its own, so that not being built can only come from its
		// ancestry — never from den having nothing to run.
		{"orphan", "image: orphan:v1\nparent: typo\n"},
	} {
		dir := filepath.Join(home, "stacks", s.name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "stack.yaml"), []byte(s.yaml), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "stacks", "orphan", build.ScriptName),
		[]byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := &recordingBuild{}

	_, stderr, err := runBuild(t, &sbx.Fake{}, script, "--den-home", home, "build")
	if err != nil {
		t.Fatalf("a broken PARENT must not fail the whole build: %v", err)
	}
	if got := strings.Join(script.ran, ","); got != "base,mid,leaf" {
		t.Errorf("ran = %v, want the healthy chain built anyway", script.ran)
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
	dir := filepath.Join(home, "stacks", "orphan")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stack.yaml"),
		[]byte("image: orphan:v1\nparent: nowhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, build.ScriptName), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := &recordingBuild{}

	_, stderr, err := runBuild(t, &sbx.Fake{}, script, "--den-home", home, "build")
	if err != nil {
		t.Fatalf("a missing PARENT must not fail the whole build: %v", err)
	}
	if got := strings.Join(script.ran, ","); got != "base,mid,leaf" {
		t.Errorf("ran = %v, want the healthy chain built anyway", script.ran)
	}
	for _, want := range []string{"orphan", "nowhere", "parent:", filepath.Join(dir, "stack.yaml")} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr, want)
		}
	}
}

func TestBuildRefusesAnUnknownStack(t *testing.T) {
	home := buildDenHome(t)
	script := &recordingBuild{}

	_, _, err := runBuild(t, &sbx.Fake{}, script, "--den-home", home, "build", "nope")
	if err == nil {
		t.Fatal("expected a refusal on a stack that is not declared")
	}
	if !strings.Contains(err.Error(), "base") {
		t.Errorf("message = %q, want it to list the declared stacks", err)
	}
	if len(script.ran) != 0 {
		t.Errorf("ran = %v, want nothing built after the refusal", script.ran)
	}
}

// A den with no stacks at all says where to declare one, rather than exiting 0
// on a silent no-op.
func TestBuildOnAnEmptyDenSaysWhereStacksGo(t *testing.T) {
	home := t.TempDir()
	stdout, _, err := runBuild(t, &sbx.Fake{}, &recordingBuild{}, "--den-home", home, "build")
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

	stdout, stderr, err := runBuild(t, &sbx.Fake{}, &recordingBuild{}, "--den-home", home, "build")
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
	_, _, err := runBuild(t, f, &recordingBuild{}, "--den-home", t.TempDir(), "build", "a", "b")
	if err == nil {
		t.Fatal("`den build a b` must be rejected on argument count")
	}
	if len(f.Calls) != 0 {
		t.Errorf("a rejected argument count must reach sbx for nothing; calls: %v", f.Calls)
	}
}

// The bench defect (2026-08-03): a den holding one stack den cannot build —
// `image:` and no build.sh, i.e. an image sbx pulls — made `den build` refuse
// and build NOTHING. den's own spawn check already treats that shape as
// legitimate; the two must agree.
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
	script := &recordingBuild{}

	stdout, _, err := runBuild(t, &sbx.Fake{}, script, "--den-home", home, "build")
	if err != nil {
		t.Fatalf("a stack den cannot build must not refuse the whole command: %v", err)
	}
	if got := strings.Join(script.ran, ","); got != "base,mid,leaf" {
		t.Errorf("ran = %v, want every buildable stack", script.ran)
	}
	if !strings.Contains(stdout, "pulled") || !strings.Contains(stdout, build.ScriptName) {
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
	script := &recordingBuild{}

	_, _, err := runBuild(t, &sbx.Fake{}, script, "--den-home", home, "build", "pulled")
	if err == nil {
		t.Fatal("expected a refusal on a named stack with no build.sh")
	}
	if !strings.Contains(err.Error(), build.ScriptName) {
		t.Errorf("message = %q, want it to name the missing script", err)
	}
	if len(script.ran) != 0 {
		t.Errorf("ran = %v, want nothing built", script.ran)
	}
}

// A Deps with no build runner takes a clean refusal, never a nil dereference —
// the doctrine every other injected field of cli.Deps states for itself. The
// wiring tests in root_deps_test.go build Deps by hand and leave it unset.
func TestBuildWithoutABuildRunnerRefusesCleanly(t *testing.T) {
	home := buildDenHome(t)
	_, _, err := runBuild(t, &sbx.Fake{}, nil, "--den-home", home, "build")
	if err == nil {
		t.Fatal("expected a refusal when no build runner is wired")
	}
}
