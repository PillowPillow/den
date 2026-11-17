package repos

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func walkRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func mkClone(t *testing.T, dir, config string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
}

func paths(cs []Clone) []string {
	out := []string{}
	for _, c := range cs {
		out = append(out, c.Path)
	}
	return out
}

func walkOK(t *testing.T, root string, o WalkOptions) []Clone {
	t.Helper()
	if o.MaxDepth == 0 {
		o.MaxDepth = 20
	}
	cs, err := Walk(root, o)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

const cfgA = "[remote \"origin\"]\n\turl = git@github.com:acme/api.git\n"

func TestWalkFindsClonesAndStopsInside(t *testing.T) {
	root := walkRoot(t)
	mkClone(t, filepath.Join(root, "b"), cfgA)
	mkClone(t, filepath.Join(root, "a"), "")
	mkClone(t, filepath.Join(root, "b", "inner"), cfgA)
	got := walkOK(t, root, WalkOptions{})
	want := []string{filepath.Join(root, "a"), filepath.Join(root, "b")}
	if !reflect.DeepEqual(paths(got), want) {
		t.Fatalf("got %v, want %v", paths(got), want)
	}
	if len(got[1].Remotes) != 1 || got[1].Remotes[0].Name != "origin" {
		t.Fatalf("remotes = %+v", got[1].Remotes)
	}
	if len(got[0].Remotes) != 0 {
		t.Fatalf("a clone with an empty config keeps empty remotes: %+v", got[0].Remotes)
	}
}

func TestWalkKeepsCloneWithUnreadableConfig(t *testing.T) {
	root := walkRoot(t)
	if err := os.MkdirAll(filepath.Join(root, "x", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := walkOK(t, root, WalkOptions{})
	if len(got) != 1 || got[0].Remotes != nil {
		t.Fatalf("got %+v", got)
	}
}

func TestWalkSkipsWorktreeFiles(t *testing.T) {
	root := walkRoot(t)
	if err := os.MkdirAll(filepath.Join(root, "wt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "wt", ".git"), []byte("gitdir: /x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := walkOK(t, root, WalkOptions{}); len(got) != 0 {
		t.Fatalf("got %v", paths(got))
	}
}

func TestWalkSkipsNamedDirs(t *testing.T) {
	root := walkRoot(t)
	for _, n := range []string{"node_modules", "vendor", ".hidden", "ok/node_modules"} {
		mkClone(t, filepath.Join(root, n, "r"), cfgA)
	}
	mkClone(t, filepath.Join(root, "ok", "r"), cfgA)
	got := walkOK(t, root, WalkOptions{})
	want := []string{filepath.Join(root, "ok", "r")}
	if !reflect.DeepEqual(paths(got), want) {
		t.Fatalf("got %v, want %v", paths(got), want)
	}
}

func TestWalkSkipsListedPaths(t *testing.T) {
	root := walkRoot(t)
	mkClone(t, filepath.Join(root, "Library", "r"), cfgA)
	mkClone(t, filepath.Join(root, "keep", "r"), cfgA)
	got := walkOK(t, root, WalkOptions{Skip: []string{filepath.Join(root, "Library") + "/"}})
	want := []string{filepath.Join(root, "keep", "r")}
	if !reflect.DeepEqual(paths(got), want) {
		t.Fatalf("got %v, want %v", paths(got), want)
	}
}

func TestWalkDoesNotFollowSymlinks(t *testing.T) {
	root := walkRoot(t)
	mkClone(t, filepath.Join(root, "real"), cfgA)
	mkClone(t, filepath.Join(root, "sub", "deep"), cfgA)
	for name, target := range map[string]string{
		"toroot":   root,
		"toslash":  "/",
		"toanc":    filepath.Join(root, "sub"),
		"toclone":  filepath.Join(root, "real"),
		"sub/up":   root,
		"sub/self": filepath.Join(root, "sub"),
	} {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "gitlink"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real", ".git"), filepath.Join(root, "gitlink", ".git")); err != nil {
		t.Fatal(err)
	}
	got := walkOK(t, root, WalkOptions{})
	want := []string{filepath.Join(root, "real"), filepath.Join(root, "sub", "deep")}
	if !reflect.DeepEqual(paths(got), want) {
		t.Fatalf("got %v, want %v", paths(got), want)
	}
}

func TestWalkRespectsMaxDepth(t *testing.T) {
	root := walkRoot(t)
	mkClone(t, filepath.Join(root, "a", "b", "c"), cfgA)
	if got := walkOK(t, root, WalkOptions{MaxDepth: 2}); len(got) != 0 {
		t.Fatalf("depth 3 clone found with MaxDepth 2: %v", paths(got))
	}
	if got := walkOK(t, root, WalkOptions{MaxDepth: 3}); len(got) != 1 {
		t.Fatalf("depth 3 clone missed with MaxDepth 3: %v", paths(got))
	}
}

func TestWalkRootIsAClone(t *testing.T) {
	root := walkRoot(t)
	mkClone(t, root, cfgA)
	mkClone(t, filepath.Join(root, "inner"), cfgA)
	got := walkOK(t, root, WalkOptions{})
	if !reflect.DeepEqual(paths(got), []string{root}) {
		t.Fatalf("got %v", paths(got))
	}
}

func TestWalkUnreadableRootIsAnError(t *testing.T) {
	if _, err := Walk(filepath.Join(walkRoot(t), "missing"), WalkOptions{MaxDepth: 5}); err == nil {
		t.Fatal("want an error")
	}
}

func TestWalkReportsProgress(t *testing.T) {
	root := walkRoot(t)
	mkClone(t, filepath.Join(root, "a"), cfgA)
	var calls [][2]int
	walkOK(t, root, WalkOptions{Progress: func(d, c int) { calls = append(calls, [2]int{d, c}) }})
	if !reflect.DeepEqual(calls, [][2]int{{2, 1}}) {
		t.Fatalf("calls = %v", calls)
	}
}

func TestEnclosingClone(t *testing.T) {
	root := walkRoot(t)
	mkClone(t, filepath.Join(root, "r"), cfgA)
	sub := filepath.Join(root, "r", "x", "y")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, ok := EnclosingClone(sub); !ok || got != filepath.Join(root, "r") {
		t.Fatalf("got %q %v", got, ok)
	}
	if _, ok := EnclosingClone(filepath.Join(root, "r")); ok {
		t.Fatal("a clone is not its own enclosing clone")
	}
	if _, ok := EnclosingClone(filepath.Join(root, "elsewhere")); ok {
		t.Fatal("no clone above")
	}
}

func TestWalkDropsPathsWithControlCharacters(t *testing.T) {
	root := walkRoot(t)
	mkClone(t, filepath.Join(root, "a\rb"), cfgA)
	mkClone(t, filepath.Join(root, "x\x1b[2Jy"), cfgA)
	mkClone(t, filepath.Join(root, "del\x7f", "r"), cfgA)
	mkClone(t, filepath.Join(root, "ok"), cfgA)
	got := walkOK(t, root, WalkOptions{})
	if want := []string{filepath.Join(root, "ok")}; !reflect.DeepEqual(paths(got), want) {
		t.Fatalf("got %q, want %q", paths(got), want)
	}
}

func TestIsClone(t *testing.T) {
	root := walkRoot(t)
	mkClone(t, filepath.Join(root, "r"), cfgA)
	if !IsClone(filepath.Join(root, "r")) {
		t.Error("a folder holding a .git folder is a clone")
	}
	if IsClone(root) || IsClone(filepath.Join(root, "absent")) {
		t.Error("a folder without .git is not a clone")
	}
}
