package den

import (
	"io/fs"
	"sort"
	"testing"
)

// go:embed silently skips names starting with "." or "_" and cannot embed an
// empty directory (see internal/config/example_test.go's sibling comment on
// the same risk for the on-disk copy). This test is the guard rail: without
// it, an embed directive that took nothing would leave every deninit and cli
// test that uses ExampleDenHome passing against an empty tree instead of
// failing loudly.
func TestExampleDenHomeCarriesTheExpectedFiles(t *testing.T) {
	var got []string
	err := fs.WalkDir(ExampleDenHome, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			got = append(got, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the embedded example: %v", err)
	}
	sort.Strings(got)

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
