package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/doctor"
)

// runInit runs `den init` against a given den home, the same shape as
// runDoctor (doctor_test.go): the whole command, not deninit.Run directly, so
// the assertions cover cobra's wiring (--den-home, RunE's error becoming a
// non-zero Execute) too.
func runInit(t *testing.T, home string) (string, error) {
	t.Helper()
	cmd := newInitCmd(&home)
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs(nil)
	err := cmd.Execute()
	return out.String(), err
}

// TestInitCreatesALoadableDenHome is the link to T2: config_dir and
// worktree_root now default at LOAD time relative to the LIVE den home
// (internal/config/config.go), and examples/den-home no longer hardcodes
// "~/.den" anywhere. Without that, a home created under t.TempDir() would
// resolve config_dir under the developer's real ~/.den instead — this test
// is what would catch that regression coming back.
func TestInitCreatesALoadableDenHome(t *testing.T) {
	home := t.TempDir()

	out, err := runInit(t, home)
	if err != nil {
		t.Fatalf("den init on a blank home: %v\n%s", err, out)
	}

	g, err := config.LoadGlobal(home)
	if err != nil {
		t.Fatalf("loading the home den init just created: %v", err)
	}
	if errs := g.Validate(); len(errs) != 0 {
		t.Fatalf("the home den init created does not validate: %v", errs)
	}

	if !strings.HasPrefix(g.WorktreeRoot, home) {
		t.Errorf("worktree_root = %q, want it resolved under %q", g.WorktreeRoot, home)
	}
	claude, ok := g.Agents["claude"]
	if !ok {
		t.Fatal("expected an agents.claude entry from the example")
	}
	if !strings.HasPrefix(claude.ConfigDir, home) {
		t.Errorf("agents.claude.config_dir = %q, want it resolved under %q", claude.ConfigDir, home)
	}
}

// insideHome reports whether p is home or a descendant of it (same shape as
// doctor's insideExampleHome, scoped to the temp home this test writes
// instead of the on-disk examples/den-home).
func insideHome(home, p string) bool {
	rel, err := filepath.Rel(home, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// TestInitThenDoctorOnlyFailsOnTheNestRepo is this task's acceptance
// criterion: `den init` followed by `den doctor` on the same home must give a
// home whose SOLE failure is the placeholder nest repo — the same promise
// internal/doctor/example_test.go locks down for the on-disk copy, checked
// here end to end through the actual command that ships to users.
func TestInitThenDoctorOnlyFailsOnTheNestRepo(t *testing.T) {
	home := t.TempDir()
	if _, err := runInit(t, home); err != nil {
		t.Fatalf("den init: %v", err)
	}

	placeholderRepo, err := config.ExpandPath("~/dev/my-project")
	if err != nil {
		t.Fatalf("expanding the example's placeholder repo path: %v", err)
	}

	d := doctor.FakeDeps()
	d.Stat = func(p string) (os.FileInfo, error) {
		if p == placeholderRepo {
			return nil, errors.New("not found")
		}
		if insideHome(home, p) {
			return os.Stat(p)
		}
		return nil, errors.New("not found (outside the den home den init wrote)")
	}

	out, err := runDoctor(t, home, d)
	if err == nil {
		t.Fatalf("expected den doctor to fail on the placeholder nest repo, got a clean exit:\n%s", out)
	}
	if strings.Count(out, "[FAIL]") != 1 {
		t.Errorf("expected exactly one [FAIL] line, got:\n%s", out)
	}
	if !strings.Contains(out, placeholderRepo) {
		t.Errorf("expected the failure to name the placeholder repo %q, got:\n%s", placeholderRepo, out)
	}
}

// TestInitRefusesASecondCall is the other half of the acceptance criteria: a
// second `den init` on an already-initialized home refuses and writes
// nothing — checked by content, not just by absence of a new error, since a
// refusal that still rewrote the OTHER two files untouched would pass a
// weaker assertion.
func TestInitRefusesASecondCall(t *testing.T) {
	home := t.TempDir()
	if _, err := runInit(t, home); err != nil {
		t.Fatalf("first den init: %v", err)
	}
	nestPath := filepath.Join(home, "nests", "example.yaml")
	before, err := os.ReadFile(nestPath)
	if err != nil {
		t.Fatalf("reading %s after the first init: %v", nestPath, err)
	}

	out, err := runInit(t, home)
	if err == nil {
		t.Fatalf("expected a second den init to refuse, got a clean exit:\n%s", out)
	}
	if !strings.Contains(err.Error(), "already initialized") {
		t.Errorf("error = %q, want it to say \"already initialized\"", err)
	}
	if !strings.Contains(err.Error(), config.GlobalPath(home)) {
		t.Errorf("error = %q, want it to name %q", err, config.GlobalPath(home))
	}

	after, err := os.ReadFile(nestPath)
	if err != nil {
		t.Fatalf("reading %s after the refused init: %v", nestPath, err)
	}
	if string(after) != string(before) {
		t.Errorf("%s changed across the refused second init", nestPath)
	}
}
