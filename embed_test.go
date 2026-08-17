package den

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/PillowPillow/den/internal/config"
)

// embeddedFiles lists the non-directory entries of an embedded home, sorted.
// Shared by the two guards below: both fail on an embed directive that took
// nothing, and neither can see that through the FS API alone.
func embeddedFiles(t *testing.T, home fs.FS) []string {
	t.Helper()
	var got []string
	err := fs.WalkDir(home, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			got = append(got, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the embedded home: %v", err)
	}
	sort.Strings(got)
	return got
}

// go:embed silently skips names starting with "." or "_" and cannot embed an
// empty directory (see internal/config/example_test.go's sibling comment on
// the same risk for the on-disk copy). This test is the guard rail: without
// it, an embed directive that took nothing would leave every deninit and cli
// test that uses ExampleDenHome passing against an empty tree instead of
// failing loudly.
func TestExampleDenHomeCarriesTheExpectedFiles(t *testing.T) {
	got := embeddedFiles(t, ExampleDenHome)

	want := []string{
		"examples/den-home/config.yaml",
		"examples/den-home/nests/example.yaml",
		"examples/den-home/stacks/devx/stack.yaml",
	}
	if len(got) != len(want) {
		t.Fatalf("embedded files = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("embedded files = %v, want %v", got, want)
			break
		}
	}
}

// The source-aware home is defined by what it does NOT carry: no nest, no
// stack, no defaults.stack. An extra file here is not cosmetic — a
// nests/example.yaml would reappear in every `den init --source`, and a
// defaults.stack would name a stacks/ directory this home never creates,
// which `den doctor` reads as a fault on a home that is exactly right.
//
// Loading it too, not just listing it: an embedded config.yaml that no
// longer decodes would otherwise be discovered by the first user running
// `den init --source`, not by the suite. It is written to a temporary home
// because config.LoadGlobal reads a den home from disk — the same bytes take
// the same path as at init time.
func TestSourceAwareDenHomeIsAValidHomeWithoutLocalContent(t *testing.T) {
	got := embeddedFiles(t, SourceAwareDenHome)
	want := []string{"examples/den-home-source/config.yaml"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("embedded files = %v, want %v", got, want)
	}

	data, err := fs.ReadFile(SourceAwareDenHome, want[0])
	if err != nil {
		t.Fatalf("reading the embedded config: %v", err)
	}
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), data, 0o644); err != nil {
		t.Fatalf("writing the temporary home: %v", err)
	}

	g, err := config.LoadGlobal(home)
	if err != nil {
		t.Fatalf("loading the source-aware home: %v", err)
	}
	if errs := g.Validate(); len(errs) != 0 {
		t.Fatalf("the source-aware home does not validate: %v", errs)
	}
	if g.Defaults.Stack != "" {
		t.Errorf("defaults.stack = %q, expected none: this home ships no stacks/", g.Defaults.Stack)
	}
	if len(g.Repos) != 0 {
		t.Errorf("repos = %v, expected none: a source's repo mapping lives in source-config/", g.Repos)
	}
	if g.Defaults.Agent == "" || len(g.Agents) == 0 {
		t.Errorf("the home must still carry the personal agent settings: %+v", g.Defaults)
	}
}
