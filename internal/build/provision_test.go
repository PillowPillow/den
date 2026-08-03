package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/config"
)

// The whole semantics of `includes` in one assertion: its text comes back
// ahead of EVERY step, not just the first. A payload built once and reused
// would look identical on step 1 and be wrong on step 2.
func TestPayloadRepeatsTheIncludesForEveryStep(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "common.sh"), "common::gh() { :; }\n")
	writeFile(t, filepath.Join(dir, "one.sh"), "common::gh\n")
	writeFile(t, filepath.Join(dir, "two.sh"), "echo two\n")

	p, err := ReadProvisioning(&config.Stack{
		Name: "devx", Dir: dir,
		Provision: config.Provision{
			Includes: []string{filepath.Join(dir, "common.sh")},
			Steps:    []string{filepath.Join(dir, "one.sh"), filepath.Join(dir, "two.sh")},
		},
	})
	if err != nil {
		t.Fatalf("ReadProvisioning: %v", err)
	}
	for i, wantTail := range []string{"common::gh\n", "echo two\n"} {
		got := p.Payload(i)
		if !strings.HasPrefix(got, "common::gh() { :; }\n") {
			t.Errorf("payload %d does not start with the includes:\n%s", i, got)
		}
		if !strings.HasSuffix(got, wantTail) {
			t.Errorf("payload %d does not end with its own step:\n%s", i, got)
		}
	}
}

// No includes is the normal case for a stack whose steps are self-contained.
// The payload is then the step, verbatim — not a step with a stray leading
// newline, which would shift every line number a shell error reports.
func TestPayloadWithoutIncludesIsTheStepVerbatim(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "one.sh"), "echo one\n")

	p, err := ReadProvisioning(&config.Stack{
		Name: "devx", Dir: dir,
		Provision: config.Provision{Steps: []string{filepath.Join(dir, "one.sh")}},
	})
	if err != nil {
		t.Fatalf("ReadProvisioning: %v", err)
	}
	if got := p.Payload(0); got != "echo one\n" {
		t.Errorf("Payload(0) = %q, want the step verbatim", got)
	}
}

// A file that is not there is named with its full path — the reason the read
// happens before the first `sbx create` at all (Task 6).
func TestReadProvisioningNamesTheMissingFile(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "provision", "gone.sh")
	_, err := ReadProvisioning(&config.Stack{
		Name: "devx", Dir: dir,
		Provision: config.Provision{Steps: []string{missing}},
	})
	if err == nil {
		t.Fatal("ReadProvisioning accepted a missing step")
	}
	errStr := err.Error()
	for _, want := range []string{"devx", missing} {
		if !strings.Contains(errStr, want) {
			t.Errorf("error %q does not name %q", errStr, want)
		}
	}
	// The path appears exactly once: config.FileError suppresses the OS
	// PathError's redundant path, so the path is named only in our prefix.
	if count := strings.Count(errStr, missing); count != 1 {
		t.Errorf("path appears %d times in %q, want 1", count, errStr)
	}
	// The error carries the translated reason, not the raw OS error.
	if !strings.Contains(errStr, "file does not exist") {
		t.Errorf("error %q does not contain translated reason 'file does not exist'", errStr)
	}
}

// Order is significant on BOTH lists, and a Go map would not preserve it.
func TestReadProvisioningKeepsDeclarationOrder(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a.sh", "b.sh", "c.sh"} {
		writeFile(t, filepath.Join(dir, n), "echo "+n+"\n")
	}
	p, err := ReadProvisioning(&config.Stack{
		Name: "devx", Dir: dir,
		Provision: config.Provision{Steps: []string{
			filepath.Join(dir, "c.sh"), filepath.Join(dir, "a.sh"), filepath.Join(dir, "b.sh"),
		}},
	})
	if err != nil {
		t.Fatalf("ReadProvisioning: %v", err)
	}
	for i, want := range []string{"c.sh", "a.sh", "b.sh"} {
		if filepath.Base(p.Steps[i].Path) != want {
			t.Errorf("step %d = %s, want %s — declaration order is the execution order",
				i, filepath.Base(p.Steps[i].Path), want)
		}
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
