// internal/source/mutate_test.go
package source

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/worktree"
)

func TestAddClonesAndNames(t *testing.T) {
	home := t.TempDir()
	url := makeSourceRepo(t) // "file:///.../team-stacks"
	name, err := Add(context.Background(), worktree.NewGit(), home, url, "")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if name != "team-stacks" {
		t.Errorf("name = %q, want team-stacks (basename of the URL)", name)
	}
	if _, err := os.Stat(filepath.Join(Dir(home, name), "stacks", "devx", "stack.yaml")); err != nil {
		t.Errorf("clone content missing: %v", err)
	}
}

func TestAddRefusesInvalidSourceAndCleansUp(t *testing.T) {
	home := t.TempDir()
	// A repo whose stack has a strict-YAML typo: lint must fail post-clone.
	dir := filepath.Join(t.TempDir(), "bad")
	if err := os.MkdirAll(filepath.Join(dir, "stacks", "devx"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stacks", "devx", "stack.yaml"),
		[]byte("image: devx:v1\nbase: claude\negres: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "init", "-b", "main")
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-m", "bad")

	_, err := Add(context.Background(), worktree.NewGit(), home, "file://"+dir, "bad")
	if err == nil || !strings.Contains(err.Error(), "egres") {
		t.Fatalf("expected the lint refusal, got: %v", err)
	}
	if _, statErr := os.Stat(Dir(home, "bad")); !os.IsNotExist(statErr) {
		t.Error("invalid clone was left behind")
	}
}

func TestAddRefusesExistingSource(t *testing.T) {
	home := t.TempDir()
	url := makeSourceRepo(t)
	if _, err := Add(context.Background(), worktree.NewGit(), home, url, "corp"); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(context.Background(), worktree.NewGit(), home, url, "corp"); err == nil {
		t.Fatal("expected a refusal on an existing source")
	}
}

func TestUpdateFastForwards(t *testing.T) {
	home := t.TempDir()
	url := makeSourceRepo(t)
	upstream := strings.TrimPrefix(url, "file://")
	if _, err := Add(context.Background(), worktree.NewGit(), home, url, "corp"); err != nil {
		t.Fatal(err)
	}
	// Upstream grows a second valid stack.
	if err := os.MkdirAll(filepath.Join(upstream, "stacks", "extra"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(upstream, "stacks", "extra", "stack.yaml"),
		[]byte("image: extra:v1\nbase: claude\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, upstream, "add", "-A")
	gitCmd(t, upstream, "commit", "-m", "extra stack")

	if err := Update(context.Background(), worktree.NewGit(), home, "corp"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, err := os.Stat(filepath.Join(Dir(home, "corp"), "stacks", "extra", "stack.yaml")); err != nil {
		t.Errorf("fast-forward did not land: %v", err)
	}
}

func TestUpdateRefusesInvalidUpstreamAndKeepsHead(t *testing.T) {
	home := t.TempDir()
	url := makeSourceRepo(t)
	upstream := strings.TrimPrefix(url, "file://")
	if _, err := Add(context.Background(), worktree.NewGit(), home, url, "corp"); err != nil {
		t.Fatal(err)
	}
	// Upstream breaks its stack.
	if err := os.WriteFile(filepath.Join(upstream, "stacks", "devx", "stack.yaml"),
		[]byte("image: devx:v1\nbase: claude\negres: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, upstream, "add", "-A")
	gitCmd(t, upstream, "commit", "-m", "break it")

	err := Update(context.Background(), worktree.NewGit(), home, "corp")
	if err == nil || !strings.Contains(err.Error(), "egres") {
		t.Fatalf("expected the pre-fast-forward lint refusal, got: %v", err)
	}
	// The clone must still lint clean: HEAD did not move.
	if errs := lintErrsOf(t, home, "corp"); len(errs) != 0 {
		t.Errorf("HEAD moved onto the broken tree: %v", errs)
	}
}

func TestUpdateRefusesDirtyWorktree(t *testing.T) {
	home := t.TempDir()
	url := makeSourceRepo(t)
	if _, err := Add(context.Background(), worktree.NewGit(), home, url, "corp"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(Dir(home, "corp"), "wip.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Update(context.Background(), worktree.NewGit(), home, "corp")
	if err == nil || !strings.Contains(err.Error(), "commit or discard") {
		t.Fatalf("expected the dirty-tree refusal, got: %v", err)
	}
}

func TestRemoveRefusesDirtyThenRemovesClean(t *testing.T) {
	home := t.TempDir()
	url := makeSourceRepo(t)
	if _, err := Add(context.Background(), worktree.NewGit(), home, url, "corp"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(Dir(home, "corp"), "wip.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Remove(context.Background(), worktree.NewGit(), home, "corp"); err == nil {
		t.Fatal("expected the dirty-tree refusal")
	}
	if err := os.Remove(filepath.Join(Dir(home, "corp"), "wip.txt")); err != nil {
		t.Fatal(err)
	}
	if err := Remove(context.Background(), worktree.NewGit(), home, "corp"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(Dir(home, "corp")); !os.IsNotExist(err) {
		t.Error("clone still present")
	}
}

// lintErrsOf re-lints an installed source, a tiny wrapper kept in the test:
// production reads lint through List.
func lintErrsOf(t *testing.T, home, name string) []error {
	t.Helper()
	infos, err := List(context.Background(), worktree.NewGit(), home)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range infos {
		if i.Name == name {
			return i.LintErrs
		}
	}
	t.Fatalf("source %q not listed", name)
	return nil
}
