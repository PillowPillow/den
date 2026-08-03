package build

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/config"
)

// recordingScript is the injected Script. It never spawns anything — issue #8
// requires it in so many words ("aucun test ne lance de script réel"), and the
// repo's suite forbids it anyway.
type recordingScript struct {
	ran  []string
	fail map[string]error
	// emit is written to out before returning, to prove the script's own
	// output reaches the same stream den writes its stage lines on.
	emit string
}

func (r *recordingScript) Run(_ context.Context, s *config.Stack, out io.Writer) error {
	r.ran = append(r.ran, s.Name)
	if r.emit != "" {
		fmt.Fprint(out, r.emit)
	}
	return r.fail[s.Name]
}

// withScripts drops an executable build.sh into each named stack directory.
// The file only has to EXIST — nothing here runs it.
func withScripts(t *testing.T, steps []Step, names ...string) {
	t.Helper()
	for _, s := range steps {
		if len(names) > 0 && !slices.Contains(names, s.Stack.Name) {
			continue
		}
		path := ScriptPath(s.Stack)
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExecuteRunsTheStepsInOrder(t *testing.T) {
	steps := plan(t, fixture, "delta", true, &fakeImages{})
	withScripts(t, steps)

	script := &recordingScript{}
	var out bytes.Buffer
	if err := Execute(context.Background(), steps, script, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.Join(script.ran, ","); got != "alpha,gamma,delta" {
		t.Errorf("ran = %v, want alpha,gamma,delta", script.ran)
	}
}

// A skipped step runs nothing but is ANNOUNCED, with the flag that overrides
// it: a silent skip and a forgotten stack look identical from the outside.
func TestExecuteAnnouncesASkippedStepWithoutRunningIt(t *testing.T) {
	steps := plan(t, fixture, "delta", false, &fakeImages{present: []string{"alpha:v1"}})
	withScripts(t, steps, "gamma", "delta")

	script := &recordingScript{}
	var out bytes.Buffer
	if err := Execute(context.Background(), steps, script, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if slices.Contains(script.ran, "alpha") {
		t.Errorf("ran = %v, want alpha never run", script.ran)
	}
	text := out.String()
	for _, want := range []string{"alpha", "skipped", "alpha:v1", "--force"} {
		if !strings.Contains(text, want) {
			t.Errorf("output = %q, want it to contain %q", text, want)
		}
	}
}

// EVERY script is checked before the FIRST one runs. `den build dgdevx` that
// built its base image for four minutes and only then found dgdevx has no
// build.sh would have burned that time to reach a refusal den could have made
// instantly.
func TestExecuteChecksEveryScriptBeforeRunningAny(t *testing.T) {
	steps := plan(t, fixture, "delta", true, &fakeImages{})
	withScripts(t, steps, "alpha", "gamma") // delta has none

	script := &recordingScript{}
	err := Execute(context.Background(), steps, script, io.Discard)
	if err == nil {
		t.Fatal("expected a refusal on a stack with no build.sh")
	}
	if len(script.ran) != 0 {
		t.Errorf("ran = %v, want nothing run before the refusal", script.ran)
	}
	msg := err.Error()
	for _, want := range []string{`"delta"`, ScriptName, "delta:v1"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message = %q, want it to contain %q", msg, want)
		}
	}
}

// A SKIPPED step needs no script: it is not going to be run, and demanding one
// would refuse a `den build dgdevx` whose base image is a prebuilt one nobody
// rebuilds locally.
func TestExecuteDoesNotDemandAScriptForASkippedStep(t *testing.T) {
	steps := plan(t, fixture, "delta", false, &fakeImages{present: []string{"alpha:v1"}})
	withScripts(t, steps, "gamma", "delta") // alpha has none, and is skipped

	if err := Execute(context.Background(), steps, &recordingScript{}, io.Discard); err != nil {
		t.Fatalf("a skipped step must not need a build.sh: %v", err)
	}
}

// A failing script stops the chain and NAMES the stack. Without that line the
// user sees a wall of build log and an exit code, and has to count the stages
// to learn which stack died.
func TestExecuteNamesTheStackWhoseScriptFailed(t *testing.T) {
	steps := plan(t, fixture, "delta", true, &fakeImages{})
	withScripts(t, steps)

	script := &recordingScript{fail: map[string]error{"gamma": errors.New("exit status 2")}}
	err := Execute(context.Background(), steps, script, io.Discard)
	if err == nil {
		t.Fatal("expected the script failure to travel back")
	}
	msg := err.Error()
	for _, want := range []string{`"gamma"`, ScriptName, "exit status 2"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message = %q, want it to contain %q", msg, want)
		}
	}
	// And it stops there: delta must not be built on top of a base that failed.
	if slices.Contains(script.ran, "delta") {
		t.Errorf("ran = %v, want the chain stopped at gamma", script.ran)
	}
}

// The script's own output and den's stage lines share one stream, in order: a
// build is something the user watches, and splitting the two would interleave
// wrongly.
func TestExecuteWritesTheScriptOutputOnTheSameStream(t *testing.T) {
	steps := plan(t, fixture, "zeta", false, &fakeImages{})
	withScripts(t, steps)

	script := &recordingScript{emit: "layer 1/3\n"}
	var out bytes.Buffer
	if err := Execute(context.Background(), steps, script, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := out.String()
	stage := strings.Index(text, "zeta")
	body := strings.Index(text, "layer 1/3")
	if stage < 0 || body < 0 || stage > body {
		t.Errorf("output = %q, want den's stage line before the script's own output", text)
	}
}

// ScriptPath is the SOLE definition of where a build script lives: the
// refusal and the execution must name the same file, or den would refuse a
// path it never tried to run.
func TestScriptPathIsUnderTheStackDirectory(t *testing.T) {
	s := &config.Stack{Name: "devx", Dir: filepath.Join("home", "stacks", "devx")}
	if got, want := ScriptPath(s), filepath.Join("home", "stacks", "devx", ScriptName); got != want {
		t.Errorf("ScriptPath = %q, want %q", got, want)
	}
}
