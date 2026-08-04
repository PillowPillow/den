// internal/source/mutate_test.go
package source

import (
	"context"
	"errors"
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

func TestUpdateRefusesImpossibleFastForward(t *testing.T) {
	home := t.TempDir()
	url := makeSourceRepo(t)
	upstream := strings.TrimPrefix(url, "file://")
	if _, err := Add(context.Background(), worktree.NewGit(), home, url, "corp"); err != nil {
		t.Fatal(err)
	}
	// rev-parse HEAD rather than reading .git/refs/heads/main directly: the
	// latter is an implementation detail (packed-refs would already break it)
	// and a git command is what production code itself uses to observe state.
	before, err := worktree.NewGit().Run(context.Background(), Dir(home, "corp"), "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	// Upstream rewrites history instead of growing it: the amended commit
	// still lints clean, so control must reach the ff-only merge and refuse
	// there, not at the lint gate.
	gitCmd(t, upstream, "commit", "--amend", "-m", "rewritten history")

	err = Update(context.Background(), worktree.NewGit(), home, "corp")
	if err == nil || !strings.Contains(err.Error(), "cannot fast-forward") {
		t.Fatalf("expected the ff-only refusal, got: %v", err)
	}
	// `den source rm` (no --force) is the WRONG remedy here — the fetch
	// below orphans the local commit, so plain `rm` will itself refuse (see
	// the Remove assertions at the end of this test). The refusal must name
	// the remedy that actually works, not merely mention the command that
	// will bounce the user right back.
	if !strings.Contains(err.Error(), "den source rm --force") {
		t.Errorf("refusal does not name the working remedy (den source rm --force): %v", err)
	}
	after, err := worktree.NewGit().Run(context.Background(), Dir(home, "corp"), "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("HEAD moved despite the impossible fast-forward: before=%q after=%q", before, after)
	}
	// The lint probe's throwaway worktree must not linger in the clone's
	// registration: only the main worktree should remain.
	out, err := worktree.NewGit().Run(context.Background(), Dir(home, "corp"), "worktree", "list")
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(strings.TrimSpace(string(out)), "\n") + 1; lines != 1 {
		t.Errorf("worktree list has %d entries, want 1 (main only):\n%s", lines, out)
	}

	// The fetch above force-updated origin/main (a clone's default fetch
	// refspec has a leading "+", allowing non-fast-forward updates to
	// remote-tracking refs) — orphaning the very commit `main` was on. That
	// commit is now indistinguishable, from local state alone, from
	// genuinely unpushed work: `den source rm` must refuse it and name
	// --force as the deliberate override, and `den source rm --force` must
	// actually remove the clone rather than leaving only a manual `rm -rf`.
	if err := Remove(context.Background(), worktree.NewGit(), home, "corp", false); err == nil {
		t.Fatal("expected Remove to refuse: the fetch orphaned the local commit")
	} else if !strings.Contains(err.Error(), "--force") {
		t.Errorf("refusal does not name --force: %v", err)
	}
	if err := Remove(context.Background(), worktree.NewGit(), home, "corp", true); err != nil {
		t.Fatalf("Remove --force: %v", err)
	}
	if _, statErr := os.Stat(Dir(home, "corp")); !os.IsNotExist(statErr) {
		t.Error("clone still present after Remove --force")
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
	dirtyErr := Remove(context.Background(), worktree.NewGit(), home, "corp", false)
	if dirtyErr == nil {
		t.Fatal("expected the dirty-tree refusal")
	}
	if !strings.Contains(dirtyErr.Error(), "--force") {
		t.Errorf("dirty-tree refusal does not name --force: %v", dirtyErr)
	}
	if err := os.Remove(filepath.Join(Dir(home, "corp"), "wip.txt")); err != nil {
		t.Fatal(err)
	}
	if err := Remove(context.Background(), worktree.NewGit(), home, "corp", false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(Dir(home, "corp")); !os.IsNotExist(err) {
		t.Error("clone still present")
	}
}

// TestRemoveForceSkipsBothChecks pins the fix round 2 escape hatch: force
// must skip the dirty check AND the unpushed-commit check, not just one of
// them — a user with nothing worth keeping needs a single command, not a
// manual `rm -rf` when it turns out only one guard would have let them
// through.
func TestRemoveForceSkipsBothChecks(t *testing.T) {
	home := t.TempDir()
	url := makeSourceRepo(t)
	if _, err := Add(context.Background(), worktree.NewGit(), home, url, "corp"); err != nil {
		t.Fatal(err)
	}
	dir := Dir(home, "corp")
	// Dirty AND carrying an unpushed commit: without force, either guard
	// alone would refuse.
	if err := os.WriteFile(filepath.Join(dir, "stacks", "devx", "extra.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-m", "local work, not pushed")
	if err := os.WriteFile(filepath.Join(dir, "wip.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Remove(context.Background(), worktree.NewGit(), home, "corp", true); err != nil {
		t.Fatalf("Remove --force: %v", err)
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Error("clone still present after Remove --force")
	}
}

// TestRemoveRefusesUntrackedWorkHiddenByLocalConfig locks in the CRITICAL
// fix: `git status --porcelain` HONOURS status.showUntrackedFiles, it does
// not override it. NeutralizeGitEnvironment only silences the *global*
// config (GIT_CONFIG_GLOBAL=/dev/null), so setting the flag in the CLONE'S
// OWN local config reproduces the blindness a contributor could trigger by
// accident, entirely hermetically.
func TestRemoveRefusesUntrackedWorkHiddenByLocalConfig(t *testing.T) {
	home := t.TempDir()
	url := makeSourceRepo(t)
	if _, err := Add(context.Background(), worktree.NewGit(), home, url, "corp"); err != nil {
		t.Fatal(err)
	}
	dir := Dir(home, "corp")
	gitCmd(t, dir, "config", "status.showUntrackedFiles", "no")
	untracked := filepath.Join(dir, "stacks", "mine", "stack.yaml")
	if err := os.MkdirAll(filepath.Dir(untracked), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(untracked, []byte("image: mine:v1\nbase: claude\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Remove(context.Background(), worktree.NewGit(), home, "corp", false)
	if err == nil || !strings.Contains(err.Error(), "local changes") {
		t.Fatalf("expected the dirty-tree refusal despite showUntrackedFiles=no, got: %v", err)
	}
	if _, statErr := os.Stat(untracked); statErr != nil {
		t.Errorf("untracked work was deleted despite local showUntrackedFiles=no: %v", statErr)
	}
}

// TestUpdateRefusesUntrackedWorkHiddenByLocalConfig is Update's half of the
// same CRITICAL fix — the dirty check at mutate.go:86 had the identical
// blind spot, letting a fetch+ff proceed over an untracked file the local
// config hid from --porcelain.
func TestUpdateRefusesUntrackedWorkHiddenByLocalConfig(t *testing.T) {
	home := t.TempDir()
	url := makeSourceRepo(t)
	if _, err := Add(context.Background(), worktree.NewGit(), home, url, "corp"); err != nil {
		t.Fatal(err)
	}
	dir := Dir(home, "corp")
	gitCmd(t, dir, "config", "status.showUntrackedFiles", "no")
	if err := os.WriteFile(filepath.Join(dir, "wip.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Update(context.Background(), worktree.NewGit(), home, "corp")
	if err == nil || !strings.Contains(err.Error(), "local changes") {
		t.Fatalf("expected the dirty-tree refusal despite showUntrackedFiles=no, got: %v", err)
	}
}

// TestUpdateRefusesNoUpstreamBranch pins the IMPORTANT fix: a checked-out
// branch with no upstream (exactly what a contributor gets from `git
// checkout -b`) must be refused by name, never silently mishandled by
// reading FETCH_HEAD's first advertised entry.
func TestUpdateRefusesNoUpstreamBranch(t *testing.T) {
	home := t.TempDir()
	url := makeSourceRepo(t)
	if _, err := Add(context.Background(), worktree.NewGit(), home, url, "corp"); err != nil {
		t.Fatal(err)
	}
	dir := Dir(home, "corp")
	gitCmd(t, dir, "checkout", "-b", "wip")
	err := Update(context.Background(), worktree.NewGit(), home, "corp")
	if err == nil || !strings.Contains(err.Error(), "no upstream") {
		t.Fatalf("expected the no-upstream refusal, got: %v", err)
	}
}

// TestRemoveSucceedsOnNoUpstreamBranchWhenNothingUnpushed pins the deliberate
// DIVERGENCE from Update's no-upstream handling: `den source rm` is the
// escape hatch Update's own ff-only refusal names, so requiring an upstream
// here — the same way Update does — would make that hatch unreachable on
// exactly the branch shape (untracked local work-in-progress) the fetch-ref
// fix exists to handle. Remove's guard (unpushedAnywhere) needs no upstream
// at all, so a clean, untracked branch removes cleanly.
func TestRemoveSucceedsOnNoUpstreamBranchWhenNothingUnpushed(t *testing.T) {
	home := t.TempDir()
	url := makeSourceRepo(t)
	if _, err := Add(context.Background(), worktree.NewGit(), home, url, "corp"); err != nil {
		t.Fatal(err)
	}
	dir := Dir(home, "corp")
	gitCmd(t, dir, "checkout", "-b", "wip")
	if err := Remove(context.Background(), worktree.NewGit(), home, "corp", false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Error("clone still present")
	}
}

// TestRemoveRefusesUnpushedCommitsOnNoUpstreamBranch proves unpushedAnywhere
// still catches what requireUpstream+aheadOfUpstream would have: a local
// commit on an untracked branch is exactly as unpushed as one on a tracked
// branch, and "--branches --not --remotes" needs no "@{u}" to see it.
func TestRemoveRefusesUnpushedCommitsOnNoUpstreamBranch(t *testing.T) {
	home := t.TempDir()
	url := makeSourceRepo(t)
	if _, err := Add(context.Background(), worktree.NewGit(), home, url, "corp"); err != nil {
		t.Fatal(err)
	}
	dir := Dir(home, "corp")
	gitCmd(t, dir, "checkout", "-b", "wip")
	if err := os.WriteFile(filepath.Join(dir, "stacks", "devx", "extra.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-m", "local work on an untracked branch")

	err := Remove(context.Background(), worktree.NewGit(), home, "corp", false)
	if err == nil || !strings.Contains(err.Error(), "not reachable from any remote-tracking ref") {
		t.Fatalf("expected the unpushed-commits refusal, got: %v", err)
	}
	if _, statErr := os.Stat(dir); statErr != nil {
		t.Errorf("clone removed despite an unpushed commit on an untracked branch: %v", statErr)
	}
}

// TestUpdateRefusesUnpushedCommits pins the second IMPORTANT fix: a clean
// working tree is not enough — a committed-but-unpushed commit must also
// block Update, so the ff-only refusal never gets a chance to recommend
// `den source rm` on work that would be lost.
func TestUpdateRefusesUnpushedCommits(t *testing.T) {
	home := t.TempDir()
	url := makeSourceRepo(t)
	if _, err := Add(context.Background(), worktree.NewGit(), home, url, "corp"); err != nil {
		t.Fatal(err)
	}
	dir := Dir(home, "corp")
	if err := os.WriteFile(filepath.Join(dir, "stacks", "devx", "extra.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-m", "local work, not pushed")

	err := Update(context.Background(), worktree.NewGit(), home, "corp")
	if err == nil || !strings.Contains(err.Error(), "unpushed commit") {
		t.Fatalf("expected the unpushed-commits refusal, got: %v", err)
	}
}

// TestRemoveRefusesUnpushedCommits is Remove's half: it is the command that
// actually destroys the clone, so it is the one the doctrine protects most.
func TestRemoveRefusesUnpushedCommits(t *testing.T) {
	home := t.TempDir()
	url := makeSourceRepo(t)
	if _, err := Add(context.Background(), worktree.NewGit(), home, url, "corp"); err != nil {
		t.Fatal(err)
	}
	dir := Dir(home, "corp")
	if err := os.WriteFile(filepath.Join(dir, "stacks", "devx", "extra.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-m", "local work, not pushed")

	err := Remove(context.Background(), worktree.NewGit(), home, "corp", false)
	if err == nil || !strings.Contains(err.Error(), "not reachable from any remote-tracking ref") {
		t.Fatalf("expected the unpushed-commits refusal, got: %v", err)
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("refusal does not name --force: %v", err)
	}
	if _, statErr := os.Stat(dir); statErr != nil {
		t.Errorf("clone removed despite an unpushed commit: %v", statErr)
	}
}

// TestAddRefusesInvalidNameFromURLBasename pins the untested name-validation
// path the reviewer flagged: a URL basename can itself be an illegal sandbox
// name component ("." is reserved for the <nest>.<worktree> separator), and
// that must produce the SAME "pass --name" remedy as an explicit bad name.
func TestAddRefusesInvalidNameFromURLBasename(t *testing.T) {
	home := t.TempDir()
	_, err := Add(context.Background(), worktree.NewGit(), home, "file:///anywhere/team.stacks", "")
	if err == nil || !strings.Contains(err.Error(), "pass `--name") {
		t.Fatalf("expected the invalid-name refusal naming --name, got: %v", err)
	}
}

// TestAddNeverDeletesAPreexistingDirectory pins the rollback guarantee: Add
// only ever os.RemoveAll's a directory IT created (the post-clone lint
// failure path). A directory that already exists for unrelated reasons must
// survive the "already installed" refusal untouched.
func TestAddNeverDeletesAPreexistingDirectory(t *testing.T) {
	home := t.TempDir()
	dir := Dir(home, "existing")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "marker.txt")
	if err := os.WriteFile(marker, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	url := makeSourceRepo(t)
	if _, err := Add(context.Background(), worktree.NewGit(), home, url, "existing"); err == nil {
		t.Fatal("expected the already-installed refusal")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("Add deleted a directory it did not create: %v", err)
	}
}

// failingRemoveGit forces `worktree remove` to fail while delegating every
// other call to the real git — the honest way to exercise the "removal
// failed, fall back to prune" branch without needing git itself to refuse.
type failingRemoveGit struct{ worktree.Git }

func (g failingRemoveGit) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if len(args) >= 2 && args[0] == "worktree" && args[1] == "remove" {
		return nil, errors.New("forced failure for test")
	}
	return g.Git.Run(ctx, dir, args...)
}

// TestUpdatePrunesStaleWorktreeRegistrationWhenRemoveFails pins the MINOR
// fix: pruning only drops a registration whose directory is already gone,
// so the fix must remove the probe directory BEFORE pruning, on the failure
// path too. Without the fix this would leave a dangling entry in `git
// worktree list`.
func TestUpdatePrunesStaleWorktreeRegistrationWhenRemoveFails(t *testing.T) {
	home := t.TempDir()
	url := makeSourceRepo(t)
	if _, err := Add(context.Background(), worktree.NewGit(), home, url, "corp"); err != nil {
		t.Fatal(err)
	}
	dir := Dir(home, "corp")

	if err := Update(context.Background(), failingRemoveGit{worktree.NewGit()}, home, "corp"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	out, err := worktree.NewGit().Run(context.Background(), dir, "worktree", "list")
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(strings.TrimSpace(string(out)), "\n") + 1; lines != 1 {
		t.Errorf("worktree list has %d entries after a failed remove, want 1 (main only) once pruned:\n%s", lines, out)
	}
}

// TestUpdateFetchesTheBranchsOwnRemoteNotOrigin pins the RESIDUAL of the
// FETCH_HEAD finding: the fetch itself was still hardcoded to "origin"
// while the merge handle is "@{u}". A branch tracking a differently-named
// remote would have that real remote left unfetched while "origin" (which
// this branch has nothing to do with) got fetched — `merge --ff-only @{u}`
// would then read stale data and, if nothing NEW happened to be on
// "origin" either, silently report success. This clones from one remote,
// re-points the branch's upstream at a SECOND one, grows both, and asserts
// only the branch's own remote's content lands.
func TestUpdateFetchesTheBranchsOwnRemoteNotOrigin(t *testing.T) {
	home := t.TempDir()
	originURL := makeSourceRepo(t)
	if _, err := Add(context.Background(), worktree.NewGit(), home, originURL, "corp"); err != nil {
		t.Fatal(err)
	}
	dir := Dir(home, "corp")
	// "other" is a CLONE of origin, not a second independent makeSourceRepo:
	// two independently built repos share no history, so aheadOfUpstream
	// (correctly) sees the local commit as absent from an unrelated
	// "other/main" and refuses — flakily, since two makeSourceRepo calls'
	// root commits collide in SHA only when git's 1-second commit-timestamp
	// resolution happens to land both "init" commits in the same second.
	// Cloning guarantees "other" starts from the SAME commit as HEAD, which
	// is what the fix under test actually assumes: a branch's upstream
	// pointing somewhere origin isn't, on a shared history.
	otherParent := t.TempDir()
	otherDir := filepath.Join(otherParent, "other-remote")
	gitCmd(t, otherParent, "clone", strings.TrimPrefix(originURL, "file://"), otherDir)
	otherURL := "file://" + otherDir
	gitCmd(t, dir, "remote", "add", "other", otherURL)
	gitCmd(t, dir, "fetch", "other")
	gitCmd(t, dir, "branch", "--set-upstream-to=other/main")

	// Grow "origin" — the WRONG remote for this branch. Its content must
	// NOT land.
	originDir := strings.TrimPrefix(originURL, "file://")
	if err := os.MkdirAll(filepath.Join(originDir, "stacks", "wrong"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(originDir, "stacks", "wrong", "stack.yaml"),
		[]byte("image: wrong:v1\nbase: claude\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, originDir, "add", "-A")
	gitCmd(t, originDir, "commit", "-m", "grows the WRONG remote")

	// Grow "other" — the branch's actual, configured upstream. This is what
	// Update must fetch and fast-forward onto.
	if err := os.MkdirAll(filepath.Join(otherDir, "stacks", "right"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(otherDir, "stacks", "right", "stack.yaml"),
		[]byte("image: right:v1\nbase: claude\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, otherDir, "add", "-A")
	gitCmd(t, otherDir, "commit", "-m", "grows the branch's real upstream")

	if err := Update(context.Background(), worktree.NewGit(), home, "corp"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "stacks", "right", "stack.yaml")); err != nil {
		t.Errorf("fast-forward onto the branch's own upstream did not land: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "stacks", "wrong", "stack.yaml")); !os.IsNotExist(err) {
		t.Error("Update fetched origin instead of the branch's own configured remote")
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
