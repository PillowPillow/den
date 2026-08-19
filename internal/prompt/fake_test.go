package prompt

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A Fake that runs out of scripted answers REFUSES. It must never return a
// zero value and a nil error.
//
// That is not tidiness: an exhausted script returning ([]string(nil), nil) is
// the exact shape of the bug this whole design exists to keep out — a
// selection nobody made, reported as success (spec §3.d). A test whose script
// is one answer short would then pass while asserting on a phantom.
func TestFakeRefusesWhenTheScriptRunsOut(t *testing.T) {
	f := &Fake{}
	if _, err := f.MultiSelect(context.Background(), MultiSelectRequest{Title: "pick"}); err == nil {
		t.Fatal("an exhausted Fake must refuse, not answer nothing")
	}
	_, err := f.Confirm(context.Background(), ConfirmRequest{Question: "apply?"})
	if err == nil {
		t.Fatal("an exhausted Fake must refuse on Confirm too")
	}
	if !strings.Contains(err.Error(), "Confirm") {
		t.Errorf("the refusal must name the method whose script ran out: %v", err)
	}
}

// The Fake records what den ASKED, not only what den did. Assertions on the
// rendered checklist move here when the bufio renderer goes away (spec §6).
func TestFakeRecordsTheRequest(t *testing.T) {
	f := &Fake{MultiSelectAnswers: [][]string{{"worker"}}}
	got, err := f.MultiSelect(context.Background(), MultiSelectRequest{
		Title:       "nest api: 2 optional repo(s)",
		Options:     []Option{{Value: "worker", Label: "worker"}},
		Preselected: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != "worker" {
		t.Errorf("scripted answer not returned: %v", got)
	}
	if len(f.MultiSelects) != 1 {
		t.Fatalf("the request must be recorded once, got %d", len(f.MultiSelects))
	}
	if !f.MultiSelects[0].Preselected {
		t.Error("Preselected must be recorded: it is how a test reads the starting state")
	}
	if f.MultiSelects[0].Title != "nest api: 2 optional repo(s)" {
		t.Errorf("the title must be recorded verbatim: %q", f.MultiSelects[0].Title)
	}
}

// Err wins over the script, so a test can exercise a caller's error path.
func TestFakeErrWinsOverTheScript(t *testing.T) {
	boom := errors.New("boom")
	f := &Fake{ConfirmAnswers: []bool{true}, Err: boom}
	if _, err := f.Confirm(context.Background(), ConfirmRequest{Question: "apply?"}); !errors.Is(err, boom) {
		t.Errorf("Err must win over a scripted answer, got %v", err)
	}
}
