package nest

import (
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/config"
)

func cpus(n int) *int { return &n }

// resolveResourcesTest runs the full cascade for a `resources:` block placed at
// the three declaring levels plus the flags, and returns what spawn would send.
func resolveResourcesTest(t *testing.T, g *config.Global, stackRes, nestRes config.Resources,
	o Options) config.Resources {

	t.Helper()
	stacks := stacksTest()
	stacks.Healthy["dgdevx"].Resources = stackRes
	n := nestTest()
	n.Resources = nestRes
	r, err := Resolve("/home/me/.den", g, stacks, n, o)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return r.Resources
}

// The question that decides the whole cascade: the stack says 4 CPUs, the nest
// says 8. The nest is LOWER in the cascade (global ← stack ← nest ← flags), so
// the nest wins — the same order `egress:` and `env:` already follow.
func TestResolveResourcesNestBeatsStack(t *testing.T) {
	got := resolveResourcesTest(t, globalTest(),
		config.Resources{CPUs: cpus(4)},
		config.Resources{CPUs: cpus(8)},
		Options{})
	if got.CPUs == nil || *got.CPUs != 8 {
		t.Errorf("CPUs = %v, expected 8 (the nest wins over the stack)", got.CPUs)
	}
}

func TestResolveResourcesStackBeatsGlobal(t *testing.T) {
	g := globalTest()
	g.Resources = config.Resources{CPUs: cpus(2), Memory: "4g"}
	got := resolveResourcesTest(t, g,
		config.Resources{CPUs: cpus(4)},
		config.Resources{},
		Options{})
	if got.CPUs == nil || *got.CPUs != 4 {
		t.Errorf("CPUs = %v, expected 4 (the stack wins over the global)", got.CPUs)
	}
	// The memory the stack does not restate survives from the global: the
	// cascade merges FIELD by field, never block by block.
	if got.Memory != "4g" {
		t.Errorf("Memory = %q, expected 4g (kept from the global, the stack restates only cpus)", got.Memory)
	}
}

func TestResolveResourcesFlagsBeatNest(t *testing.T) {
	got := resolveResourcesTest(t, globalTest(),
		config.Resources{},
		config.Resources{CPUs: cpus(8), Memory: "16g"},
		Options{Resources: config.Resources{CPUs: cpus(2), Memory: "2g"}})
	if got.CPUs == nil || *got.CPUs != 2 || got.Memory != "2g" {
		t.Errorf("CPUs = %v, Memory = %q, expected 2 / 2g (the flags win)", got.CPUs, got.Memory)
	}
}

// Absent everywhere means ABSENT, never zero: spawn must omit `--cpus`, and a
// nil pointer is what carries that. `--cpus 0` and no flag mean the same thing
// to sbx v0.39.0 today, but by coincidence, and den does not build on those.
func TestResolveResourcesAbsentEverywhere(t *testing.T) {
	got := resolveResourcesTest(t, globalTest(), config.Resources{}, config.Resources{}, Options{})
	if got.CPUs != nil {
		t.Errorf("CPUs = %v, expected nil (nothing declared any)", *got.CPUs)
	}
	if got.Memory != "" {
		t.Errorf("Memory = %q, expected empty", got.Memory)
	}
}

// `cpus: 0` WRITTEN is not the same fact as `cpus:` absent, and this is why the
// field is a pointer: it is the only way a nest can send a stack's 8 back to
// sbx's own "auto".
func TestResolveResourcesExplicitZeroOverridesStack(t *testing.T) {
	got := resolveResourcesTest(t, globalTest(),
		config.Resources{CPUs: cpus(4)},
		config.Resources{CPUs: cpus(0)},
		Options{})
	if got.CPUs == nil {
		t.Fatal("CPUs = nil, expected an explicit 0 (the nest asked for sbx's auto)")
	}
	if *got.CPUs != 0 {
		t.Errorf("CPUs = %d, expected 0", *got.CPUs)
	}
}

// The resolved block must not ALIAS the nest's pointer: a consumer that wrote
// through it would edit the loaded nest, and every test here would still pass.
func TestResolveResourcesDoesNotAliasTheNest(t *testing.T) {
	n := nestTest()
	n.Resources = config.Resources{CPUs: cpus(8)}
	r, err := Resolve("/home/me/.den", globalTest(), stacksTest(), n, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Resources.CPUs == n.Resources.CPUs {
		t.Error("Resolved.Resources.CPUs aliases the nest's pointer — copy the value instead")
	}
}

// Refusal BEFORE the first side effect (spec §6), and only on the WINNER: a
// global `memory: 512m` a nest overrides never reaches sbx, so it must not
// refuse the spawn.
func TestResolveResourcesRefusesOnlyTheWinningMemory(t *testing.T) {
	g := globalTest()
	g.Resources = config.Resources{Memory: "512m"}
	got := resolveResourcesTest(t, g, config.Resources{}, config.Resources{Memory: "8g"}, Options{})
	if got.Memory != "8g" {
		t.Errorf("Memory = %q, expected 8g", got.Memory)
	}
}

// And the message names the FILE to fix — the level the winning value came
// from, not whichever file was read last.
func TestResolveResourcesRefusalNamesTheDeclaringFile(t *testing.T) {
	tests := []struct {
		name       string
		g          func(*config.Global)
		stack      config.Resources
		nest       config.Resources
		opts       Options
		wantInFile string
	}{
		{
			name:       "global",
			g:          func(g *config.Global) { g.Resources = config.Resources{Memory: "512m"} },
			wantInFile: "/home/me/.den/config.yaml",
		},
		{
			name:       "stack",
			g:          func(*config.Global) {},
			stack:      config.Resources{Memory: "512m"},
			wantInFile: "stack.yaml",
		},
		{
			name:       "nest",
			g:          func(*config.Global) {},
			nest:       config.Resources{Memory: "512m"},
			wantInFile: "/home/me/.den/nests/fullstack.yaml",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := globalTest()
			tc.g(g)
			stacks := stacksTest()
			stacks.Healthy["dgdevx"].Resources = tc.stack
			stacks.Healthy["dgdevx"].Dir = "/home/me/.den/stacks/dgdevx"
			n := nestTest()
			n.Resources = tc.nest
			_, err := Resolve("/home/me/.den", g, stacks, n, tc.opts)
			if err == nil {
				t.Fatal("expected a refusal on a memory below sbx's 1 GiB minimum")
			}
			if !strings.Contains(err.Error(), tc.wantInFile) {
				t.Errorf("error = %q, expected it to name %s", err, tc.wantInFile)
			}
			if !strings.Contains(err.Error(), "1 GiB") {
				t.Errorf("error = %q, expected it to state the minimum", err)
			}
		})
	}
}

// A value typed on the command line has no file to send the user to, and the
// message must not invent one.
func TestResolveResourcesRefusalOnAFlagNamesTheFlag(t *testing.T) {
	_, err := Resolve("/home/me/.den", globalTest(), stacksTest(), nestTest(),
		Options{Resources: config.Resources{Memory: "512m"}})
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !strings.Contains(err.Error(), "--memory") {
		t.Errorf("error = %q, expected it to name the `--memory` flag", err)
	}
	if strings.Contains(err.Error(), ".yaml") {
		t.Errorf("error = %q, must not send the user to a file they did not write it in", err)
	}
}

func TestResolveResourcesRefusesNegativeCPUs(t *testing.T) {
	g := globalTest()
	g.Resources = config.Resources{CPUs: cpus(-1)}
	_, err := Resolve("/home/me/.den", g, stacksTest(), nestTest(), Options{})
	if err == nil {
		t.Fatal("expected a refusal on a negative cpu count")
	}
	if !strings.Contains(err.Error(), "/home/me/.den/config.yaml") {
		t.Errorf("error = %q, expected it to name config.yaml", err)
	}
}
