package config

import (
	"path/filepath"
	"testing"
)

func TestFindProjectWalksUp(t *testing.T) {
	root := t.TempDir()
	want := writeFile(t, root, "proj/den.yaml", minimalNest)
	deep := filepath.Join(root, "proj", "a", "b")
	writeFile(t, deep, "x", "")
	got, ok, err := FindProject(deep, filepath.Join(root, "home"))
	if err != nil || !ok || got != want {
		t.Errorf("got %q %v %v, want %q", got, ok, err, want)
	}
}

func TestFindProjectSkipsTheGlobalFile(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	writeFile(t, home, "den.yaml", "schema: 1\n")
	got, ok, err := FindProject(filepath.Join(home, "wt"), home)
	if err != nil || ok {
		t.Errorf("got %q %v %v, want not found", got, ok, err)
	}
	outer := writeFile(t, root, "den.yaml", minimalNest)
	got, ok, err = FindProject(filepath.Join(home, "wt"), home)
	if err != nil || !ok || got != outer {
		t.Errorf("got %q %v %v, want %q", got, ok, err, outer)
	}
}

func TestFindProjectNone(t *testing.T) {
	_, ok, err := FindProject(t.TempDir(), t.TempDir())
	if err != nil || ok {
		t.Errorf("ok=%v err=%v", ok, err)
	}
}
