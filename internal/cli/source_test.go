package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/worktree"
)

// gitCmd runs git for FIXTURE BUILDING only — production code goes through
// worktree.Git. Copied from internal/source/source_test.go's shape: test
// files cannot import another package's test helpers.
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

func TestSourceAddUpdateLsRm(t *testing.T) {
	home := t.TempDir()
	url := makeSourceRepo(t)

	run := func(args ...string) (string, error) {
		root := NewRootCmdWith(Deps{Git: worktree.NewGit()})
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(append(args, "--den-home", home))
		err := root.Execute()
		return out.String(), err
	}

	if _, err := run("source", "add", url, "--name", "corp"); err != nil {
		t.Fatalf("add: %v", err)
	}
	out, err := run("source", "ls")
	if err != nil {
		t.Fatalf("ls: %v", err)
	}
	if !strings.Contains(out, "corp") || !strings.Contains(out, "file://") {
		t.Errorf("ls output lacks name or url:\n%s", out)
	}
	if _, err := run("source", "update", "corp"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := run("source", "rm", "corp"); err != nil {
		t.Fatalf("rm: %v", err)
	}
	out, _ = run("source", "ls")
	if strings.Contains(out, "corp") {
		t.Errorf("removed source still listed:\n%s", out)
	}
}

// TestSourceUpdateAllWhenNoName locks the three behaviors the brief calls
// out for a bare `den source update`: (a) no arg updates every installed
// source, (b) one failing source does not prevent the others from updating,
// (c) the combined error names each failing source — by its own name, not
// merely by whatever the underlying git error happens to mention (source.
// Update's bare `git fetch` error carries NO source name at all).
func TestSourceUpdateAllWhenNoName(t *testing.T) {
	home := t.TempDir()
	url := makeSourceRepo(t)

	run := func(args ...string) (string, error) {
		root := NewRootCmdWith(Deps{Git: worktree.NewGit()})
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(append(args, "--den-home", home))
		err := root.Execute()
		return out.String(), err
	}

	if _, err := run("source", "add", url, "--name", "good"); err != nil {
		t.Fatalf("add good: %v", err)
	}
	if _, err := run("source", "add", url, "--name", "bad"); err != nil {
		t.Fatalf("add bad: %v", err)
	}

	// Hand-break "bad": point its remote at a path that no longer exists, so
	// `git fetch` fails deep inside source.Update. gitCmd runs real,
	// unwrapped git — production code never takes this path.
	gitCmd(t, filepath.Join(home, "sources", "bad"), "remote", "set-url", "origin", "file:///does/not/exist/at/all")

	out, err := run("source", "update")
	if err == nil {
		t.Fatal("expected an error: source \"bad\" cannot fetch from its broken remote")
	}
	if !strings.Contains(err.Error(), "bad") {
		t.Errorf("combined error does not name the failing source \"bad\": %v", err)
	}
	if !strings.Contains(out, `"good" updated`) {
		t.Errorf("the valid source was not reported updated despite the other's failure:\n%s", out)
	}
}
