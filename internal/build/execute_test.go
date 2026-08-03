package build

import (
	"context"
	"errors"
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
