package spawn

import (
	"context"
	"strings"
	"testing"
)

// sandboxNameFrom spawns with the given options and returns the name `sbx
// create` received. Asserting on the ARGV rather than on an internal variable
// is what makes the test survive a refactor of the naming block: the name that
// matters is the one sbx is told, not the one den computed.
func sandboxNameFrom(t *testing.T, denHome string, o Options) string {
	t.Helper()
	f, d := fakeDeps()
	if err := Spawn(context.Background(), denHome, o, d); err != nil {
		t.Fatalf("spawn %+v: %v", o, err)
	}
	for _, call := range f.Calls {
		if len(call) == 0 || call[0] != "create" {
			continue
		}
		for i, arg := range call {
			if arg == "--name" && i+1 < len(call) {
				return call[i+1]
			}
		}
	}
	t.Fatalf("no `create --name` in calls: %v", f.Calls)
	return ""
}

// The naming table of the spec (§ "Le nom de sandbox"). One test, four rows:
// the rule is a single rule and reading it in one place is the point.
func TestInstanceNamesTheSandbox(t *testing.T) {
	for _, c := range []struct {
		name string
		o    Options
		want string
	}{
		{"bare", Options{Nest: "api"}, "api"},
		{"worktree only", Options{Nest: "api", Worktree: "feature/123"}, "api.feature-123"},
		{"instance only", Options{Nest: "api", Instance: "reco"}, "api.reco"},
		{"instance wins over worktree",
			Options{Nest: "api", Worktree: "feature/123", Instance: "reco"}, "api.reco"},
	} {
		t.Run(c.name, func(t *testing.T) {
			denHome, _ := denTest(t)
			if got := sandboxNameFrom(t, denHome, c.o); got != c.want {
				t.Errorf("sandbox name = %q, want %q", got, c.want)
			}
		})
	}
}

// --as goes through the SAME flattening as -w: one charset, one rewrite, no
// second path to keep in sync.
func TestInstanceIsFlattenedLikeAWorktree(t *testing.T) {
	denHome, _ := denTest(t)
	if got := sandboxNameFrom(t, denHome, Options{Nest: "api", Instance: "feat/x"}); got != "api.feat-x" {
		t.Errorf("sandbox name = %q, want %q", got, "api.feat-x")
	}
}

// An instance that cannot be named is refused BEFORE any side effect, like
// every other name den builds.
func TestSpawnRefusesAnUnnameableInstance(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := fakeDeps()
	err := Spawn(context.Background(), denHome, Options{Nest: "api", Instance: "-x"}, d)
	if err == nil {
		t.Fatal("an instance starting with `-` must be refused: it is indistinguishable from a flag")
	}
	if !strings.Contains(err.Error(), "instance") {
		t.Errorf("the refusal must name what is wrong (instance), got: %v", err)
	}
	if f.HasCalled("create") {
		t.Errorf("refused, yet something was created: %v", f.Calls)
	}
}
