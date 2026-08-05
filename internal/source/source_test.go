// internal/source/source_test.go
package source

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PillowPillow/den/internal/worktree"
)

// gitCmd runs git for FIXTURE BUILDING only — production code goes through
// worktree.Git.
func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// makeSourceRepo builds a VALID source repo and returns its file:// URL.
func makeSourceRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "team-stacks")
	if err := os.MkdirAll(filepath.Join(dir, "stacks", "devx"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stacks", "devx", "stack.yaml"),
		[]byte("image: devx:v1\nbase: claude\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "init", "-b", "main")
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-m", "init")
	return "file://" + dir
}

func TestLocate(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(Dir(home, "corp"), 0o755); err != nil {
		t.Fatal(err)
	}
	root, src, name, err := Locate(home, "corp:devx")
	if err != nil || root != Dir(home, "corp") || src != "corp" || name != "devx" {
		t.Fatalf("Locate = (%q,%q,%q,%v)", root, src, name, err)
	}
	root, src, name, err = Locate(home, "devx")
	if err != nil || root != home || src != "" || name != "devx" {
		t.Fatalf("Locate bare = (%q,%q,%q,%v)", root, src, name, err)
	}
	if _, _, _, err := Locate(home, "ghost:devx"); err == nil ||
		!strings.Contains(err.Error(), "den source add") {
		t.Fatalf("expected a missing-source error naming the remedy, got: %v", err)
	}
}

func TestStale(t *testing.T) {
	home := t.TempDir()
	dir := Dir(home, "corp")
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	head := filepath.Join(dir, ".git", "HEAD")
	if err := os.WriteFile(head, []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if Stale(home, "corp", now) {
		t.Error("fresh HEAD judged stale")
	}
	old := now.Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(head, old, old); err != nil {
		t.Fatal(err)
	}
	if !Stale(home, "corp", now) {
		t.Error("8-day-old fetch judged fresh")
	}
}

// TestLastFetchPrefersFetchHead locks in the precedence the brief states
// explicitly: FETCH_HEAD wins over HEAD when both exist. This is exactly the
// case `den source update` produces — fetch rewrites FETCH_HEAD while HEAD
// keeps its original clone-time mtime — so a reversed precedence would judge
// a just-updated source stale.
func TestLastFetchPrefersFetchHead(t *testing.T) {
	home := t.TempDir()
	dir := Dir(home, "corp")
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	old := now.Add(-8 * 24 * time.Hour)

	head := filepath.Join(dir, ".git", "HEAD")
	if err := os.WriteFile(head, []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(head, old, old); err != nil {
		t.Fatal(err)
	}

	fetchHead := filepath.Join(dir, ".git", "FETCH_HEAD")
	if err := os.WriteFile(fetchHead, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(fetchHead, now, now); err != nil {
		t.Fatal(err)
	}

	last, ok := LastFetch(home, "corp")
	if !ok || !last.Equal(now) {
		t.Fatalf("LastFetch = (%v,%v), want (%v,true) from FETCH_HEAD, not HEAD's stale mtime", last, ok, now)
	}
	if Stale(home, "corp", now) {
		t.Error("fresh FETCH_HEAD ignored in favor of stale HEAD")
	}
}

// TestNames pins Names' contract, distinct from List's: names only, sorted,
// no git and no lint run, and a non-directory entry inside sources/ (e.g. a
// stray file) is skipped rather than reported as an installed source.
func TestNames(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(Dir(home, "zed"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(Dir(home, "corp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(Root(home), "stray-file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	names, err := Names(home)
	if err != nil {
		t.Fatalf("Names: %v", err)
	}
	want := []string{"corp", "zed"}
	if len(names) != len(want) || names[0] != want[0] || names[1] != want[1] {
		t.Fatalf("Names = %v, want %v (sorted, stray file excluded)", names, want)
	}
}

// TestNamesOnMissingSourcesDir pins the same doctrine List documents: a den
// home that never added a source has no sources/ directory at all, and that
// is an empty list, not an error.
func TestNamesOnMissingSourcesDir(t *testing.T) {
	home := t.TempDir()
	names, err := Names(home)
	if err != nil {
		t.Fatalf("Names: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("Names = %v, want an empty list", names)
	}
}

func TestListReadsCloneAndLint(t *testing.T) {
	home := t.TempDir()
	url := makeSourceRepo(t)
	gitCmd(t, t.TempDir(), "clone", url, Dir(home, "corp"))
	infos, err := List(context.Background(), worktree.NewGit(), home)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 1 || infos[0].Name != "corp" {
		t.Fatalf("infos = %+v", infos)
	}
	if !strings.HasPrefix(infos[0].URL, "file://") {
		t.Errorf("URL = %q", infos[0].URL)
	}
	if infos[0].Head == "" {
		t.Error("Head = \"\", want the cloned commit's short SHA")
	}
	if len(infos[0].LintErrs) != 0 {
		t.Errorf("valid source reported lint errors: %v", infos[0].LintErrs)
	}
}
