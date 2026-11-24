package cli

import (
	"bytes"
	"testing"

	"github.com/PillowPillow/den/v2/internal/sbx"
)

func TestVersionPrintsTheStampedVersion(t *testing.T) {
	old := Version
	Version = "v2.0.0-test"
	t.Cleanup(func() { Version = old })

	var out bytes.Buffer
	root := NewRoot(Deps{Out: &out, Err: &out})
	root.SetArgs([]string{"version"})
	if err := root.Execute(); err != nil {
		t.Fatalf("den version: %v", err)
	}
	if got, want := out.String(), "den v2.0.0-test\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestMinVersionIsTheSpecFloor(t *testing.T) {
	if sbx.MinVersion != "v0.54.0" {
		t.Errorf("sbx.MinVersion = %q, want v0.54.0", sbx.MinVersion)
	}
}
