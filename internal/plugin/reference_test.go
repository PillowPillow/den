package plugin

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestReferenceCopiesMatchDocs(t *testing.T) {
	root := moduleRoot(t)
	pages := []string{"den-yaml.md", "commands.md", "templates.md", "security.md", "ssh.md", "files.md", "setup.md"}
	for _, p := range pages {
		doc, err := os.ReadFile(filepath.Join(root, "docs", p))
		if err != nil {
			t.Fatal(err)
		}
		ref, err := os.ReadFile(filepath.Join(root, "plugins", "den", "reference", p))
		if err != nil {
			t.Errorf("plugins/den/reference/%s is unreadable: run `task plugin-sync`: %v", p, err)
			continue
		}
		if !bytes.Equal(doc, ref) {
			t.Errorf("plugins/den/reference/%s differs from docs/%s: run `task plugin-sync`", p, p)
		}
	}

	found, err := filepath.Glob(filepath.Join(root, "plugins", "den", "reference", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range found {
		names = append(names, filepath.Base(f))
	}
	want := append([]string(nil), pages...)
	sort.Strings(want)
	sort.Strings(names)
	if !reflect.DeepEqual(names, want) {
		t.Errorf("reference/ must hold exactly %v, found %v", pages, names)
	}
}
