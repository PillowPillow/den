package converge

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/PillowPillow/den/internal/worktree"
)

// TestMain neutralizes the machine's git configuration and the redirecting
// variables, exactly as internal/source and internal/cli do: this package's
// discovery tests run REAL git in temporary directories, and an inherited
// GIT_DIR has already made this suite commit into unrelated repositories.
func TestMain(m *testing.M) {
	worktree.NeutralizeGitEnvironment()
	os.Exit(m.Run())
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// manifestTreeWithNests builds a source checkout exporting one stack and the
// given nests, keyed by name. The nests' bodies are the test's own: what these
// tests vary is what a nest DECLARES, not how the source is shaped.
func manifestTreeWithNests(t *testing.T, nests map[string]string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "stacks", "base", "stack.yaml"), "image: base:v1\n")

	manifest := "schema_version: 1\nkind: source\nmetadata: { name: dg, version: 1.0.0 }\nexports:\n  nests:\n"
	for _, name := range sortedKeys(nests) {
		writeFile(t, filepath.Join(root, "nests", name+".yaml"), nests[name])
		manifest += "    - { name: " + name + ", path: nests/" + name + ".yaml }\n"
	}
	manifest += "  stacks:\n    - { name: base, path: stacks/base/stack.yaml }\n"
	writeFile(t, filepath.Join(root, "den-source.yaml"), manifest)
	return root
}

func sortedKeys(m map[string]string) []string {
	return slices.Sorted(maps.Keys(m))
}
