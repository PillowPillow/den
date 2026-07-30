package config_test

import (
	"path/filepath"
	"testing"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
)

// The example shipped in examples/den-home must load and validate without
// error: it's the starting point users copy into ~/.den.
func TestExampleDenHomeIsValid(t *testing.T) {
	// Resolve requires an absolute denHome (derived paths go to `git worktree`
	// and `sbx create`): filepath.Abs rather than a relative Join.
	home, err := filepath.Abs(filepath.Join("..", "..", "examples", "den-home"))
	if err != nil {
		t.Fatalf("resolving the example's path: %v", err)
	}

	g, err := config.LoadGlobal(home)
	if err != nil {
		t.Fatalf("loading the example: %v", err)
	}
	if errs := g.Validate(); len(errs) != 0 {
		t.Fatalf("the example does not validate: %v", errs)
	}

	stacks, err := config.LoadStacks(home)
	if err != nil {
		t.Fatalf("loading the example's stacks: %v", err)
	}
	// The shipped example must be flawless: no broken stack, and the default
	// stack resolves through the sole source of truth.
	if len(stacks.Broken) != 0 {
		t.Fatalf("the shipped example must be flawless, %d broken stack(s): %+v",
			len(stacks.Broken), stacks.Broken)
	}
	if _, err := stacks.Get(g.Defaults.Stack); err != nil {
		t.Errorf("defaults.stack = %q missing from the example's stacks: %v", g.Defaults.Stack, err)
	}

	nests, broken, err := nest.ListNests(home)
	if err != nil {
		t.Fatalf("loading the example's nests: %v", err)
	}
	if len(broken) != 0 {
		var names []string
		for _, c := range broken {
			names = append(names, c.Name)
		}
		t.Fatalf("the shipped example must be flawless, %d broken nest(s): %v (detail: %+v)",
			len(broken), names, broken)
	}
	if len(nests) == 0 {
		t.Fatal("the example declares no nest")
	}
	for _, n := range nests {
		if _, err := nest.Resolve(home, g, stacks, n, nest.Options{}); err != nil {
			t.Errorf("nest %q from the example does not resolve: %v", n.Name, err)
		}
	}
}
