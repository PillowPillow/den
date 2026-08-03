package doctor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/config"
)

// TestRunExampleDenHomeOnlyFailsOnTheNestRepo locks down README.md's promise
// right after `cp -R examples/den-home ~/.den`: the placeholder nest repo
// (~/dev/my-project) is the only diagnostic a fresh copy must fail on. The
// phantom `kit: ./kit` this test would have caught (examples/ ships no kit/
// directory — git does not track empty ones) already broke that promise
// once; this is what stops it from happening silently again, since go:embed
// (a later task) inherits whatever examples/den-home contains byte for byte.
//
// Stat is rigged to fail on the placeholder repo ONLY, everything else comes
// from FakeDeps (every other path exists, sbx is on PATH, git is recent
// enough, the agent has keys) — so a failure here can only be the example
// itself regressing, never the machine running the suite.
func TestRunExampleDenHomeOnlyFailsOnTheNestRepo(t *testing.T) {
	home, err := filepath.Abs(filepath.Join("..", "..", "examples", "den-home"))
	if err != nil {
		t.Fatalf("resolving the example's path: %v", err)
	}

	// nest.ListNests expands the "~" in repos.path at load time (see
	// internal/nest/nest.go), so Run sees the same absolute path a real user
	// would — computed the same way, not hard-coded, so a change to the
	// example's placeholder doesn't silently desync this test from it.
	placeholderRepo, err := config.ExpandPath("~/dev/my-project")
	if err != nil {
		t.Fatalf("expanding the example's placeholder repo path: %v", err)
	}

	d := FakeDeps()
	d.Stat = func(p string) (os.FileInfo, error) {
		if p == placeholderRepo {
			return nil, errors.New("not found")
		}
		return nil, nil
	}

	checks := Run(home, d)

	var blocking []Check
	for _, c := range checks {
		if c.Blocking() {
			blocking = append(blocking, c)
		}
	}
	if len(blocking) != 1 {
		t.Fatalf("expected exactly one blocking check (the placeholder nest repo), got %d: %+v",
			len(blocking), blocking)
	}
	if blocking[0].Name != "nest example" {
		t.Errorf(`expected the sole blocking check to be "nest example", got %q (%+v)`,
			blocking[0].Name, blocking[0])
	}
	if !strings.Contains(blocking[0].Detail, placeholderRepo) {
		t.Errorf("expected the blocking check to name the placeholder repo %q, got %q",
			placeholderRepo, blocking[0].Detail)
	}
}
