package sbx

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestFakeRecordsCalls(t *testing.T) {
	f := &Fake{}
	if _, err := f.Run(context.Background(), "ls", "--json"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := f.Run(context.Background(), "rm", "--force", "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(f.Calls) != 2 {
		t.Fatalf("Calls = %v, want 2 calls", f.Calls)
	}
	if !f.HasCalled("rm", "--force") {
		t.Errorf("HasCalled(rm --force) must be true; calls: %v", f.Calls)
	}
	if !f.HasCalled("ls") {
		t.Errorf("HasCalled(ls) must be true; calls: %v", f.Calls)
	}
	if f.HasCalled("create") {
		t.Errorf("HasCalled(create) must be false; calls: %v", f.Calls)
	}
}

func TestFakeScriptedResponse(t *testing.T) {
	want := []byte(`{"sandboxes":[]}`)
	f := &Fake{Responses: map[string]Response{
		"ls --json": {Output: want},
	}}

	got, err := f.Run(context.Background(), "ls", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestFakeDefaultResponse(t *testing.T) {
	sentinel := errors.New("boom")
	f := &Fake{Default: Response{Err: sentinel}}

	if _, err := f.Run(context.Background(), "whatever", "you", "want"); !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want the default sentinel", err)
	}
}

// Attach is recorded as a call: tests for `den spawn` must be able to assert
// THAT the attach happened, and with which arguments.
func TestFakeAttachIsRecorded(t *testing.T) {
	f := &Fake{}
	if err := f.Attach(context.Background(), "exec", "-it", "api", "bash", "-l"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasCalled("exec", "-it", "api") {
		t.Errorf("the attach must be recorded; calls: %v", f.Calls)
	}
}

func TestFakeAttachCanFail(t *testing.T) {
	sentinel := errors.New("tty unavailable")
	f := &Fake{AttachErr: sentinel}
	if err := f.Attach(context.Background(), "exec", "-it", "api"); !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want the sentinel", err)
	}
}

// Run must return a copy of the scripted output: a caller that mutates the
// received slice must not corrupt later calls on the same Fake.
func TestFakeRunReturnsACopyOfTheOutput(t *testing.T) {
	f := &Fake{Default: Response{Output: []byte("original")}}

	first, err := f.Run(context.Background(), "ls", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	first[0] = 'X'

	second, err := f.Run(context.Background(), "ls", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(second) != "original" {
		t.Errorf("output = %q, want %q (mutating the first call must not leak)", second, "original")
	}
}

// Run and Attach are irreconcilable (see runner.go): a Run whose argv starts
// with "exec" must never pass for an attach, and an attach must stay
// detectable independently of Runs. Two distinct traces, not a key you'd have
// to guess inside Calls.
func TestFakeDistinguishesRunFromAttach(t *testing.T) {
	f := &Fake{}
	if _, err := f.Run(context.Background(), "exec", "-it", "api", "bash", "-l"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.HasAttached("exec") {
		t.Errorf("a Run must never count as an attach; attaches: %v", f.Attaches)
	}

	if err := f.Attach(context.Background(), "exec", "-it", "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasAttached("exec", "-it", "api") {
		t.Errorf("the attach must be recorded in Attaches; attaches: %v", f.Attaches)
	}
	if !f.HasCalled("exec", "-it", "api") {
		t.Errorf("Attach must keep feeding Calls too")
	}
}

// Pipes exists for the confusion Calls cannot resolve, exactly as Attaches
// does: a Run whose argv starts with "exec" must never be mistaken for a
// command actually run in the sandbox.
func TestFakeRecordsPipesApartFromCalls(t *testing.T) {
	f := &Fake{}
	if err := f.Pipe(context.Background(), "exec", "api", "go", "test"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := f.Run(context.Background(), "exec", "api", "true"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasPiped("exec", "api", "go", "test") {
		t.Errorf("the Pipe must be recorded; pipes: %v", f.Pipes)
	}
	if f.HasPiped("exec", "api", "true") {
		t.Errorf("a Run must never count as a Pipe; pipes: %v", f.Pipes)
	}
	if len(f.Calls) != 2 {
		t.Errorf("both calls must reach Calls; calls: %v", f.Calls)
	}
}

func TestFakePipeReturnsTheScriptedError(t *testing.T) {
	want := errors.New("boom")
	f := &Fake{PipeErr: want}
	if err := f.Pipe(context.Background(), "exec", "api", "false"); !errors.Is(err, want) {
		t.Errorf("Pipe = %v, want %v", err, want)
	}
}

// The redaction lives on BOTH sides of the boundary, and this is the half a
// test can see: a value passed to RunSensitive must not appear in what the
// fake records, or every redaction assertion in den would be checking a
// property production has and the double does not.
func TestFakeRedactsSensitiveArguments(t *testing.T) {
	f := &Fake{}
	if _, err := f.RunSensitive(context.Background(), []int{4},
		"secret", "set-custom", "--env", "GITLAB_TOKEN", "sentinel-secret"); err != nil {
		t.Fatalf("RunSensitive: %v", err)
	}
	if len(f.Sensitive) != 1 {
		t.Fatalf("Sensitive = %v", f.Sensitive)
	}
	for _, recorded := range [][][]string{f.Calls, f.Sensitive} {
		for _, call := range recorded {
			for _, arg := range call {
				if arg == "sentinel-secret" {
					t.Fatalf("the secret was recorded verbatim: %v", call)
				}
			}
		}
	}
	if f.Sensitive[0][4] != RedactedArg {
		t.Errorf("argument 4 = %q, want %q", f.Sensitive[0][4], RedactedArg)
	}
}

// A piped secret never reaches argv at all, and the fake keeps no copy of it.
func TestFakeRecordsPipedInputByArgvOnly(t *testing.T) {
	f := &Fake{}
	if _, err := f.RunInput(context.Background(), []byte("sentinel-secret"),
		"secret", "set", "--all-sandboxes", "--registry", "registry.example.test:443", "--password-stdin"); err != nil {
		t.Fatalf("RunInput: %v", err)
	}
	if len(f.Inputs) != 1 || len(f.Calls) != 1 {
		t.Fatalf("Inputs = %v, Calls = %v", f.Inputs, f.Calls)
	}
	for _, arg := range f.Inputs[0] {
		if arg == "sentinel-secret" {
			t.Fatal("the piped value must never be in argv")
		}
	}
}

// The real runner redacts the same positions, in the one place a secret could
// escape: the error a failing command produces.
func TestExecRunSensitiveRedactsTheErrorArgv(t *testing.T) {
	e := &Exec{Bin: "false"} // exits 1, whatever the arguments
	_, err := e.RunSensitive(context.Background(), []int{1}, "arg", "sentinel-secret")
	if err == nil {
		t.Fatal("expected the failure of `false`")
	}
	if strings.Contains(err.Error(), "sentinel-secret") {
		t.Fatalf("the error leaks the secret: %v", err)
	}
	if !strings.Contains(err.Error(), RedactedArg) {
		t.Errorf("error = %q, expected the redacted marker", err.Error())
	}
}
