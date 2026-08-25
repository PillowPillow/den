package cli

import (
	"strings"
	"testing"
)

// `den nest show` is the cascade's dry-run, and a cascade is exactly the kind
// of configuration whose winner cannot be read off any single file: four levels
// can each state `cpus:`. The listing is where a user sees which one won,
// without spawning a VM to find out.
func TestNestShowRendersTheResolvedResources(t *testing.T) {
	dir := t.TempDir()
	writeUnder(t, dir, "config.yaml", `
agents:
  claude:
    update: "claude update"
defaults:
  agent: claude
  stack: devx
resources:
  cpus: 2
  memory: 4g
`)
	// The stack raises the memory its toolchain needs; the nest raises the CPU
	// count. Field by field, both survive — which is the property this listing
	// has to show, and the one a whole-block override would destroy.
	writeUnder(t, dir, "stacks/devx/stack.yaml", "image: devx:v1\nresources:\n  memory: 8g\n")
	writeUnder(t, dir, "nests/api.yaml",
		"stack: devx\nrepos:\n  - { path: /dev/api }\nresources:\n  cpus: 8\n")
	t.Setenv("DEN_HOME", dir)

	out, err := run(t, "nest", "show", "api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"resources:", "cpus: 8", "memory: 8g"} {
		if !strings.Contains(out, want) {
			t.Errorf("output must carry %q:\n%s", want, out)
		}
	}
	// The levels that LOST must not appear: a listing showing every candidate
	// would leave the reader doing the resolution den just did.
	for _, gone := range []string{"cpus: 2", "memory: 4g"} {
		if strings.Contains(out, gone) {
			t.Errorf("output carries the overridden %q:\n%s", gone, out)
		}
	}
}

// Nothing declared prints no block at all: den does not know sbx's defaults —
// they depend on the host, and no sbx command reports them — so a line stating
// one would be den asserting a number it has never measured.
func TestNestShowPrintsNoResourcesWhenNoneDeclared(t *testing.T) {
	testDenHome(t)

	out, err := run(t, "nest", "show", "api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "resources:") {
		t.Errorf("a `resources:` block appeared with nothing declared:\n%s", out)
	}
}
