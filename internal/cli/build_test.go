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
