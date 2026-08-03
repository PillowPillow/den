package doctor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/config"
)

// insideExampleHome reports whether p is home or a descendant of it, using
// filepath.Rel rather than strings.HasPrefix: a raw prefix check on unclean
// paths would treat "examples/den-home-evil" as inside "examples/den-home"
// merely because the string starts the same way.
func insideExampleHome(home, p string) bool {
	rel, err := filepath.Rel(home, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// TestRunExampleDenHomeOnlyFailsOnTheNestRepo locks down README.md's promise
// right after `cp -R examples/den-home ~/.den`: the placeholder nest repo
// (~/dev/my-project) is the only diagnostic a fresh copy must fail on. The
// phantom `kit: ./kit` this test would have caught (examples/ ships no kit/
// directory — git does not track empty ones) already broke that promise
// once; this is what stops it from happening silently again, since go:embed
// (a later task) inherits whatever examples/den-home contains byte for byte.
//
// Stat is scoped, not fully faked nor fully real:
//   - the placeholder nest repo fails, unconditionally — that's the one
//     diagnostic this test exists to prove stays alone.
//   - any path INSIDE examples/den-home (kit paths, stack paths, ...) goes
//     to the REAL os.Stat: those files are checked into git, so their
//     existence is a fact about the repository, identical on every machine
//     running this suite from this checkout — reading them is what makes a
//     resurrected `kit: ./kit` actually fail the test again.
//   - everything else falls back to FakeDeps' "exists". Today that scope is
//     provably equivalent to a blanket `os.Stat` fallthrough: Run's only
//     other Stat call is ssh.dir (doctor.go), gated on `ssh.mode == "mount"`,
//     and the example uses "agent-forward" — but ssh.dir resolves under the
//     real $HOME, so the day the example's config.yaml switches to "mount"
//     (one line), a blanket fallthrough would silently start reading
//     ~/.ssh_sbx on whatever machine runs the suite, and this test's verdict
//     would depend on whether that path happens to exist there. Scoping the
//     fallthrough to paths inside examples/den-home keeps the test correct
//     under both configurations, at no extra cost.
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
		if insideExampleHome(home, p) {
			return os.Stat(p)
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
