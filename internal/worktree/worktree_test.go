package worktree

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMain neutralizes the machine's git configuration and the redirecting
// variables via NeutralizeGitEnvironment (see worktree.go): the module reads
// ignore rules to decide on a deletion (a ~/.gitignore_global — this
// machine's carries ".sbx" — would make dirtiness verdicts depend on the
// machine running the suite), and this file's helpers run raw `git`, which
// GIT_DIR + GIT_WORK_TREE would otherwise redirect to whatever repository the
// environment designates instead of the test's temp directory — reproduced
// once: the suite had created branches and commits INSIDE den's own
// repository and moved its HEAD.
func TestMain(m *testing.M) {
	NeutralizeGitEnvironment()
	os.Exit(m.Run())
}

func TestPath(t *testing.T) {
	cases := []struct {
		layout, root, wt, repo, expected string
	}{
		{"central", "/den/worktrees", "feat12", "/dev/api", "/den/worktrees/feat12/api"},
		{"per-repo", "/den/worktrees", "feat12", "/dev/api", "/dev/api/.den/feat12"},
	}
	for _, c := range cases {
		if got := Path(c.layout, c.root, c.wt, c.repo); got != c.expected {
			t.Errorf("Path(%s,...) = %q, expected %q", c.layout, got, c.expected)
		}
	}
}

// The worktree of a repo passed on the command line belongs to no nest's
// repos:, so `den rm` has nothing to iterate — that is issue #46. It IS
// recoverable without den having stored anything: the directory carries a
// `.git` pointing at its repository, which is exactly what Orphans reads.
func TestOrphansRecoversAWorktreeNoRepoAccountsFor(t *testing.T) {
	repo := testRepo(t, "hotfix")
	root := t.TempDir()
	if _, err := Ensure(context.Background(), NewGit(), "central", root, wtName("feat"), repo); err != nil {
		t.Fatalf("setup: %v", err)
	}

	found, skipped, err := Orphans(context.Background(), NewGit(), root, "feat", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("nothing must be skipped here; got %v", skipped)
	}
	if len(found) != 1 {
		t.Fatalf("found = %v, expected exactly the worktree of %s", found, repo)
	}
	if found[0].Dir != filepath.Join(root, "feat", "hotfix") {
		t.Errorf("Dir = %q, expected %q", found[0].Dir, filepath.Join(root, "feat", "hotfix"))
	}
	if !samePath(found[0].RepoPath, repo) {
		t.Errorf("RepoPath = %q, expected %q — recovered from the worktree's own .git",
			found[0].RepoPath, repo)
	}
}

// A repo the caller ALREADY handles must not come back as an orphan: it would
// be removed twice, and the second Remove would report on a directory that is
// already in the trash. The de-duplication goes through Path — the very
// function that placed the directory — and not through a basename heuristic.
func TestOrphansSkipsWhatTheCallerAlreadyKnows(t *testing.T) {
	declared := testRepo(t, "api")
	adhoc := testRepo(t, "hotfix")
	root := t.TempDir()
	for _, repo := range []string{declared, adhoc} {
		if _, err := Ensure(context.Background(), NewGit(), "central", root, wtName("feat"), repo); err != nil {
			t.Fatalf("setup %s: %v", repo, err)
		}
	}

	found, skipped, err := Orphans(context.Background(), NewGit(), root, "feat", []string{declared})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("a known repo is skipped SILENTLY, not warned about; got %v", skipped)
	}
	if len(found) != 1 || !samePath(found[0].RepoPath, adhoc) {
		t.Fatalf("found = %v, expected only %s", found, adhoc)
	}
}

// No worktree directory at all is the NOMINAL case (a sandbox spawned without
// -w, a worktree already cleaned up). Returning an error would make `den rm`
// warn about a perfectly ordinary teardown.
func TestOrphansOnAnAbsentDirectoryIsNotAnError(t *testing.T) {
	found, skipped, err := Orphans(context.Background(), NewGit(), t.TempDir(), "feat", nil)
	if err != nil {
		t.Fatalf("an absent <root>/<wt> must not be an error: %v", err)
	}
	if len(found) != 0 || len(skipped) != 0 {
		t.Errorf("found = %v, skipped = %v, expected both empty", found, skipped)
	}
}

// Path("central", root, "", repo) is <root>/<repo>: enumerating with an empty
// worktree name would offer every entry of the user's worktree_root — including
// the worktrees of OTHER nests — for removal. Same refusal as removeParentDir's.
func TestOrphansRefusesAnEmptyWorktreeName(t *testing.T) {
	repo := testRepo(t, "api")
	root := t.TempDir()
	if _, err := Ensure(context.Background(), NewGit(), "central", root, wtName("feat"), repo); err != nil {
		t.Fatalf("setup: %v", err)
	}

	found, _, err := Orphans(context.Background(), NewGit(), root, "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("found = %v, expected nothing: an empty worktree name designates no worktree", found)
	}
}

// GUARD 4 — the directory-eating case. worktree_root is an ordinary directory:
// nothing stops a user from cloning a repo into it. Its `.git` makes it look
// exactly like a worktree to a naive enumeration, and den would move the
// user's whole repository to the trash.
func TestOrphansSkipsARepositoryParkedUnderWorktreeRoot(t *testing.T) {
	root := t.TempDir()
	parked := filepath.Join(root, "feat", "myclone")
	createRepo(t, parked)

	found, skipped, err := Orphans(context.Background(), NewGit(), root, "feat", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("found = %v — a repository is NEVER removed as an orphan", found)
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0].Error(), parked) {
		t.Fatalf("skipped = %v, expected one reason naming %s", skipped, parked)
	}
	if !strings.Contains(skipped[0].Error(), "repository itself") {
		t.Errorf("reason = %q, expected it to say the directory is a repository", skipped[0])
	}
}

// GUARD 5 — the silent-no-op case. A repo passed on the command line may
// itself be a linked worktree (`den api ~/dev/api-wt`). The enumerated
// directory is then named after THAT worktree, while repoDir walks up to the
// main worktree, whose basename differs. Remove recomputes the path from the
// repo and would stat a directory that does not exist, conclude "already
// gone", and report success — leaving the worktree exactly where it was.
func TestOrphansSkipsAWorktreeWhoseRepoDirectoryNameDiffers(t *testing.T) {
	main := testRepo(t, "api")
	// A linked worktree of `main`, under a DIFFERENT basename: this is the repo
	// the user passed on the command line.
	linked := filepath.Join(filepath.Dir(main), "api-wt")
	git(t, main, "worktree", "add", "-b", "side", linked)

	root := t.TempDir()
	dir, err := Ensure(context.Background(), NewGit(), "central", root, wtName("feat"), linked)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if dir != filepath.Join(root, "feat", "api-wt") {
		t.Fatalf("setup produced %q, expected the directory to be named after the linked worktree", dir)
	}

	found, skipped, err := Orphans(context.Background(), NewGit(), root, "feat", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("found = %v — Remove would clean up %q instead and silently do nothing",
			found, Path("central", root, "feat", main))
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0].Error(), dir) {
		t.Fatalf("skipped = %v, expected one reason naming %s", skipped, dir)
	}
	if !strings.Contains(skipped[0].Error(), "by hand") {
		t.Errorf("reason = %q, expected it to tell the user what to do", skipped[0])
	}
}

// GUARD 2 — the user's own directory. Nothing forbids a `notes/` under
// <root>/<wt>: it is not git, den does not touch it, and it does not stop the
// real orphans beside it from being recovered.
func TestOrphansSkipsAPlainDirectoryAndKeepsGoing(t *testing.T) {
	repo := testRepo(t, "hotfix")
	root := t.TempDir()
	if _, err := Ensure(context.Background(), NewGit(), "central", root, wtName("feat"), repo); err != nil {
		t.Fatalf("setup: %v", err)
	}
	notes := filepath.Join(root, "feat", "notes")
	if err := os.MkdirAll(notes, 0o755); err != nil {
		t.Fatal(err)
	}

	found, skipped, err := Orphans(context.Background(), NewGit(), root, "feat", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(found) != 1 || !samePath(found[0].RepoPath, repo) {
		t.Fatalf("found = %v, expected the real orphan %s to survive the skip", found, repo)
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0].Error(), notes) {
		t.Fatalf("skipped = %v, expected one reason naming %s", skipped, notes)
	}
	// The WORDING matters: root here has no enclosing repository, so identify
	// must fail outright (guard 2). Without this, the test would also pass if
	// guard 3 fired, and the two cases would stop being told apart.
	if !strings.Contains(skipped[0].Error(), "not a git worktree") {
		t.Errorf("reason = %q, expected guard 2 (identify failed), not another guard", skipped[0])
	}
}

// GUARD 3 — worktree_root INSIDE a repository. git answers for the first
// repository found walking up, so an empty directory there identifies as a
// worktree of the enclosing repo. Only comparing git's --show-toplevel with
// the directory itself tells them apart.
func TestOrphansSkipsADirectoryUnderAnEnclosingRepository(t *testing.T) {
	enclosing := testRepo(t, "monorepo")
	root := filepath.Join(enclosing, "worktrees")
	plain := filepath.Join(root, "feat", "api")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}

	found, skipped, err := Orphans(context.Background(), NewGit(), root, "feat", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("found = %v — that directory is not a worktree, git just answered for %s",
			found, enclosing)
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0].Error(), "not the root of a git worktree") {
		t.Fatalf("skipped = %v, expected the reason to name what git actually answered", skipped)
	}
}

// GUARD 1 — a file, not a directory. den never removes an entry it did not
// place, and it certainly never removes a file.
func TestOrphansSkipsAFile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "feat"), 0o755); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(root, "feat", "README")
	if err := os.WriteFile(stray, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	found, skipped, err := Orphans(context.Background(), NewGit(), root, "feat", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("found = %v, expected nothing", found)
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0].Error(), stray) {
		t.Fatalf("skipped = %v, expected one reason naming %s", skipped, stray)
	}
}

// listHidingWorktrees answers `worktree list --porcelain` as if only the main
// worktree were registered, and delegates everything else to a real git. It
// isolates GUARD 6, which the other guards make otherwise unreachable through
// the filesystem alone: a directory that IS a worktree root of that repository,
// yet does not appear in the repository's registrations.
//
// Named field rather than an embedded Git, following fakePruneFailingGit in
// internal/cli: the delegation is then visible, and no method is promoted by
// accident. `dir` is the cwd git was invoked in — worktreeEntry invokes it in
// the REPOSITORY, so the answer lists the main worktree and nothing else.
type listHidingWorktrees struct{ real Git }

func (g listHidingWorktrees) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if len(args) >= 2 && args[0] == "worktree" && args[1] == "list" {
		return []byte("worktree " + dir + "\nbranch refs/heads/main\n\n"), nil
	}
	return g.real.Run(ctx, dir, args...)
}

func (g listHidingWorktrees) RunWithInput(ctx context.Context, dir string, input []byte, args ...string) ([]byte, error) {
	return g.real.RunWithInput(ctx, dir, input, args...)
}

// GUARD 6 — den asks the repository whether it knows this worktree. Without
// registration there is nothing to prune, and removing would be acting on a
// resemblance rather than on a fact.
func TestOrphansSkipsAnUnregisteredWorktree(t *testing.T) {
	repo := testRepo(t, "hotfix")
	root := t.TempDir()
	dir, err := Ensure(context.Background(), NewGit(), "central", root, wtName("feat"), repo)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	found, skipped, err := Orphans(context.Background(), listHidingWorktrees{real: NewGit()}, root, "feat", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("found = %v, expected nothing: the repository does not know this worktree", found)
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0].Error(), dir) {
		t.Fatalf("skipped = %v, expected one reason naming %s", skipped, dir)
	}
}

// testRepo creates a real git repo with one commit, under t.TempDir().
func testRepo(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	createRepo(t, dir)
	return dir
}

// createRepo creates a real git repo with one commit at the exact path
// requested — split out of testRepo for cases that need repos at a specific
// location (two same-named repos under different parents, for instance).
func createRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmds := [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@example.test"},
		{"config", "user.name", "Test"},
		{"commit", "--allow-empty", "-m", "initial"},
	}
	for _, c := range cmds {
		cmd := exec.Command("git", c...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", c, err, out)
		}
	}
}

// testRepoWithOrigin creates a *cloned* repo, so it carries a
// refs/remotes/origin/HEAD — what discovering the default branch depends on.
// testRepo's purely local repos have none, and so exercise the fallback.
func testRepoWithOrigin(t *testing.T, name string) string {
	t.Helper()
	base := t.TempDir()
	remote := filepath.Join(base, name+".git")
	git(t, base, "init", "-q", "--bare", "-b", "main", remote)

	source := filepath.Join(base, name+"-source")
	createRepo(t, source)
	git(t, source, "remote", "add", "origin", remote)
	git(t, source, "push", "-q", "origin", "main")

	clone := filepath.Join(base, name)
	git(t, base, "clone", "-q", remote, clone)
	git(t, clone, "config", "user.email", "test@example.test")
	git(t, clone, "config", "user.name", "Test")
	return clone
}

func TestEnsureCreatesTheWorktreeAndBranch(t *testing.T) {
	repo := testRepo(t, "api")
	root := t.TempDir()

	worktreePath, err := Ensure(context.Background(), NewGit(), "central", root, wtName("feat12"), repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if worktreePath != filepath.Join(root, "feat12", "api") {
		t.Errorf("path = %q", worktreePath)
	}
	if _, err := os.Stat(filepath.Join(worktreePath, ".git")); err != nil {
		t.Errorf("the worktree must exist: %v", err)
	}
	if got := branchOf(t, worktreePath); got != "feat12" {
		t.Errorf("branch = %q, expected feat12", got)
	}
}

// The directory name and the branch are TWO distinct names: "feature/123" is
// a perfectly ordinary branch name, but neither a flat directory name nor a
// sandbox-name component. den flattens the first and keeps the second.
func TestEnsureSeparatesDirNameFromBranch(t *testing.T) {
	repo := testRepo(t, "api")
	root := t.TempDir()

	worktreePath, err := Ensure(context.Background(), NewGit(), "central", root,
		Name{Dir: "feature-123", Branch: "feature/123"}, repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The directory is FLAT: "feature/123" would dig a subdirectory there,
	// which Path — and hence `den rm` — could not recover from the sandbox
	// name.
	if worktreePath != filepath.Join(root, "feature-123", "api") {
		t.Errorf("path = %q, expected %q", worktreePath, filepath.Join(root, "feature-123", "api"))
	}
	if got := branchOf(t, worktreePath); got != "feature/123" {
		t.Errorf("branch = %q, expected feature/123 — that is the user's branch", got)
	}
}

// Flattening is not injective: "feat/try" and "feat-try" aim at the same
// directory. Nothing tells them apart at the sandbox-name level, and it is
// the branch check that must catch it — without it den would attach the
// second request to the first request's worktree, on the wrong branch.
func TestEnsureRefusesTwoBranchesThatFlattenTheSame(t *testing.T) {
	repo := testRepo(t, "api")
	root := t.TempDir()

	worktreePath, err := Ensure(context.Background(), NewGit(), "central", root,
		Name{Dir: "feat-try", Branch: "feat/try"}, repo)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	_, err = Ensure(context.Background(), NewGit(), "central", root,
		Name{Dir: "feat-try", Branch: "feat-try"}, repo)
	if err == nil {
		t.Fatal("two branches on the same directory must produce an error")
	}
	for _, expected := range []string{worktreePath, "feat/try", "feat-try"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the message must contain %q to be actionable; got: %v", expected, err)
		}
	}
}

// Idempotence: re-spawning the same nest with the same -w must not break
// anything.
func TestEnsureIsIdempotent(t *testing.T) {
	repo := testRepo(t, "api")
	root := t.TempDir()

	first, err := Ensure(context.Background(), NewGit(), "central", root, wtName("feat12"), repo)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	second, err := Ensure(context.Background(), NewGit(), "central", root, wtName("feat12"), repo)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if first != second {
		t.Errorf("diverging paths: %q then %q", first, second)
	}
}

// The worktree exists but on ANOTHER branch: an actionable refusal (spec
// §11), never a silent checkout that would move the user's work.
func TestEnsureRefusesAWorktreeOnAnotherBranch(t *testing.T) {
	repo := testRepo(t, "api")
	root := t.TempDir()

	worktreePath, err := Ensure(context.Background(), NewGit(), "central", root, wtName("feat12"), repo)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	switchTo(t, worktreePath, "other")

	_, err = Ensure(context.Background(), NewGit(), "central", root, wtName("feat12"), repo)
	if err == nil {
		t.Fatal("a worktree on another branch must produce an error")
	}
	for _, expected := range []string{worktreePath, "feat12", "other"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the message must contain %q to be actionable; got: %v", expected, err)
		}
	}
}

// The branch already exists on the repo side: it gets checked out, never
// recreated (git would refuse).
func TestEnsureReusesAnExistingBranch(t *testing.T) {
	repo := testRepo(t, "api")
	root := t.TempDir()
	git(t, repo, "branch", "feat12")

	worktreePath, err := Ensure(context.Background(), NewGit(), "central", root, wtName("feat12"), repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := branchOf(t, worktreePath); got != "feat12" {
		t.Errorf("branch = %q, expected feat12", got)
	}
}

func TestEnsureNonexistentRepo(t *testing.T) {
	root := t.TempDir()
	_, err := Ensure(context.Background(), NewGit(), "central", root, wtName("feat12"), "/does/not/exist")
	if err == nil {
		t.Fatal("a nonexistent repo must produce an error")
	}
	// The path alone proves nothing: without den's guard, exec.Cmd already
	// fails the chdir with a message that names the path too. Only the "not
	// found" marker distinguishes den's guard from that os/exec fallout.
	for _, expected := range []string{"/does/not/exist", "not found"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the message must contain %q; got: %v", expected, err)
		}
	}
}

// An EACCES on the parent is not an absence: diagnosing "not found" would
// send the user chasing the wrong problem.
func TestEnsureDistinguishesAnInaccessibleRepo(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root traverses directories regardless of permissions")
	}
	base := t.TempDir()
	parent := filepath.Join(base, "locked")
	repo := filepath.Join(parent, "api")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })

	_, err := Ensure(context.Background(), NewGit(), "central", t.TempDir(), wtName("feat12"), repo)
	if err == nil {
		t.Fatal("an inaccessible repo must produce an error")
	}
	if strings.Contains(err.Error(), "not found") {
		t.Errorf("an EACCES must not be diagnosed as an absence; got: %v", err)
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("the real cause must stay unwrappable via errors.Is; got: %v", err)
	}
}

// prepareWorktree sets up the fixture shared by almost every Remove test: a
// repo "api", its worktree "feat12" in central layout, and the Target that
// designates it — including den_home, since that is where the trash lives.
func prepareWorktree(t *testing.T) (repo, worktreePath string, target Target) {
	t.Helper()
	repo = testRepo(t, "api")
	target = Target{
		DenHome:  t.TempDir(),
		Layout:   "central",
		Root:     t.TempDir(),
		Nest:     "api.feat12",
		Worktree: "feat12",
		RepoPath: repo,
	}
	worktreePath, err := Ensure(context.Background(), NewGit(), target.Layout, target.Root, wtName(target.Worktree), target.RepoPath)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	return repo, worktreePath, target
}

// withForce returns a copy of target with Force set as requested.
func withForce(target Target, force bool) Target { target.Force = force; return target }

// In central layout the worktree lives in its own directory
// (<root>/<wt>/<repo>). Removing it must not leave that now-empty
// intermediate directory behind, or `den ls` fills up with empty directories
// for anyone who spawns and destroys sandboxes all day.
func TestRemoveDeletesTheIntermediateDirThatBecameEmpty(t *testing.T) {
	_, worktreePath, target := prepareWorktree(t)
	parent := filepath.Dir(worktreePath)

	if _, err := Remove(context.Background(), NewGit(), target); err != nil {
		t.Fatalf("removal: %v", err)
	}
	if _, err := os.Stat(parent); !os.IsNotExist(err) {
		t.Errorf("%s should have disappeared with its last worktree (err = %v)", parent, err)
	}
	// worktree_root itself STAYS: it is a user setting, not a directory den
	// created.
	if _, err := os.Stat(target.Root); err != nil {
		t.Errorf("worktree_root must not be touched: %v", err)
	}
}

// The counterpart that makes the rule safe: the intermediate directory is
// SHARED by every repo of the same nest (spec §13.5). Removing it on the
// first repo would delete a directory still in use — os.Remove refuses to,
// and that refusal is exactly what the rule relies on.
func TestRemoveKeepsTheIntermediateDirStillInUse(t *testing.T) {
	_, worktreePath, target := prepareWorktree(t)
	parent := filepath.Dir(worktreePath)

	repoB := filepath.Join(t.TempDir(), "web")
	createRepo(t, repoB)
	targetB := target
	targetB.RepoPath = repoB
	worktreePathB, err := Ensure(context.Background(), NewGit(), target.Layout, target.Root,
		wtName(target.Worktree), repoB)
	if err != nil {
		t.Fatalf("setting up the second repo: %v", err)
	}

	if _, err := Remove(context.Background(), NewGit(), target); err != nil {
		t.Fatalf("removing the first: %v", err)
	}
	if _, err := os.Stat(worktreePathB); err != nil {
		t.Fatalf("the second repo's worktree must be intact: %v", err)
	}
	if _, err := os.Stat(parent); err != nil {
		t.Errorf("%s still carries a worktree: it must remain: %v", parent, err)
	}

	if _, err := Remove(context.Background(), NewGit(), targetB); err != nil {
		t.Fatalf("removing the second: %v", err)
	}
	if _, err := os.Stat(parent); !os.IsNotExist(err) {
		t.Errorf("with the last worktree gone, %s should have disappeared (err = %v)", parent, err)
	}
}

// Same refusal, for content den did not put there: what is not den's is not
// den's to delete. And crucially, no error either way — the worktree removal
// itself did succeed.
func TestRemoveKeepsIntermediateDirHoldingSomethingElse(t *testing.T) {
	_, worktreePath, target := prepareWorktree(t)
	parent := filepath.Dir(worktreePath)
	intruder := filepath.Join(parent, "notes.txt")
	if err := os.WriteFile(intruder, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Remove(context.Background(), NewGit(), target); err != nil {
		t.Fatalf("a non-empty intermediate directory must not fail the removal: %v", err)
	}
	if _, err := os.Stat(intruder); err != nil {
		t.Errorf("den must not touch what it did not put there: %v", err)
	}
}

// The directory disappeared by hand: Remove prunes the registration and
// returns "" without error. This is exactly the case where the empty
// intermediate directory lingers most reliably, so it must be cleaned up
// here too, not only after a successful move to the trash.
func TestRemoveDeletesIntermediateDirAfterManualRemoval(t *testing.T) {
	_, worktreePath, target := prepareWorktree(t)
	parent := filepath.Dir(worktreePath)
	if err := os.RemoveAll(worktreePath); err != nil {
		t.Fatal(err)
	}

	dest, err := Remove(context.Background(), NewGit(), target)
	if err != nil {
		t.Fatalf("removal: %v", err)
	}
	if dest != "" {
		t.Errorf("nothing was moved to the trash, dest = %q", dest)
	}
	if _, err := os.Stat(parent); !os.IsNotExist(err) {
		t.Errorf("%s should have disappeared too (err = %v)", parent, err)
	}
}

// The guard: Remove is exported, and a Target without a worktree name makes
// Path resolve to `<root>/<repo>`, whose parent IS worktree_root itself.
// Without this refusal, such a call would erase the user's worktree root the
// moment it happened to be empty.
// The root is EMPTY here, and that is the whole test: with a worktree still
// inside, os.Remove would fail on ENOTEMPTY anyway and the guard would go
// unexercised — the test would stay green without it.
func TestRemoveDoesNotTouchRootWithoutWorktreeName(t *testing.T) {
	target := Target{
		DenHome:  t.TempDir(),
		Layout:   "central",
		Root:     t.TempDir(),
		Nest:     "api",
		Worktree: "",
		RepoPath: testRepo(t, "api"),
	}

	if _, err := Remove(context.Background(), NewGit(), target); err != nil {
		t.Fatalf("removal: %v", err)
	}
	if _, err := os.Stat(target.Root); err != nil {
		t.Errorf("worktree_root must never be deleted: %v", err)
	}
}

// Spec §14: refuse when dirty without --force. Losing uncommitted work would
// be the worst possible side effect of a `den rm`.
func TestRemoveRefusesADirtyWorktree(t *testing.T) {
	_, worktreePath, target := prepareWorktree(t)
	if err := os.WriteFile(filepath.Join(worktreePath, "draft.txt"), []byte("wip"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Remove(context.Background(), NewGit(), target)
	if err == nil {
		t.Fatal("a worktree with uncommitted changes must be refused without force")
	}
	// "an error + an intact directory" is what git alone already provides:
	// dropping den's guard entirely would leave both assertions green. The
	// markers below are what distinguishes den's guard from git's own net.
	for _, expected := range []string{worktreePath, "draft.txt", "uncommitted", "--keep-worktrees"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the message must contain %q; got: %v", expected, err)
		}
	}
	if _, err := os.Stat(worktreePath); err != nil {
		t.Errorf("the refused worktree must be INTACT: %v", err)
	}

	if _, err := Remove(context.Background(), NewGit(), withForce(target, true)); err != nil {
		t.Fatalf("with force, the removal must succeed: %v", err)
	}
}

// Idempotence of removal: `den rm` may land on a worktree already removed by
// hand. Without this branch git would fail on the missing path.
func TestRemoveOnAnAlreadyAbsentWorktreeIsIdempotent(t *testing.T) {
	_, _, target := prepareWorktree(t)
	if _, err := Remove(context.Background(), NewGit(), target); err != nil {
		t.Fatalf("first removal: %v", err)
	}

	if _, err := Remove(context.Background(), NewGit(), target); err != nil {
		t.Errorf("an already-absent worktree must return nil; got: %v", err)
	}
	// A worktree den never created is treated the same way.
	never := target
	never.Worktree = "never-created"
	if _, err := Remove(context.Background(), NewGit(), never); err != nil {
		t.Errorf("a never-created worktree must return nil; got: %v", err)
	}
}

// Dirtiness is not limited to untracked files: a COMMITTED file, then
// modified, is work just as destructible. An implementation that only looked
// at untracked files (`git ls-files --others`) would pass the draft test
// while still destroying this work.
func TestRemoveRefusesAModifiedTrackedFile(t *testing.T) {
	_, worktreePath, target := prepareWorktree(t)
	tracked := filepath.Join(worktreePath, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("committed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, worktreePath, "add", "tracked.txt")
	git(t, worktreePath, "commit", "-m", "add tracked.txt")

	// Uncommitted modification of a tracked file: nothing "other" about it,
	// entirely tracked.
	if err := os.WriteFile(tracked, []byte("work in progress\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Remove(context.Background(), NewGit(), target)
	if err == nil {
		t.Fatal("an uncommitted modification of a tracked file must be refused without force")
	}
	// git also refuses on its own; accepting ONLY den's message is what
	// distinguishes our guard from git's own safety net — otherwise the user
	// gets an English "fatal:" instead of an actionable instruction.
	for _, expected := range []string{worktreePath, "uncommitted", "--keep-worktrees"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the message must contain %q to be actionable; got: %v", expected, err)
		}
	}
	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("the refused worktree must be INTACT: %v", err)
	}
	content, err := os.ReadFile(tracked)
	if err != nil {
		t.Fatalf("the modified file must be INTACT: %v", err)
	}
	if string(content) != "work in progress\n" {
		t.Errorf("the uncommitted work was altered: %q", content)
	}
}

// Spec §13.4-3: the branch starts from the repo's default branch, not
// whatever HEAD happened to be on. On a multi-repo nest, the current HEAD
// would start each repo from a different base with no warning.
func TestEnsureStartsFromTheDefaultBranch(t *testing.T) {
	repo := testRepoWithOrigin(t, "api")
	root := t.TempDir()
	// Move the current HEAD away from the default branch.
	git(t, repo, "checkout", "-q", "-b", "old")
	git(t, repo, "commit", "--allow-empty", "-m", "must NOT serve as a base")

	defaultCommit := git(t, repo, "rev-parse", "main")
	offTopic := git(t, repo, "rev-parse", "old")

	worktreePath, err := Ensure(context.Background(), NewGit(), "central", root, wtName("feat12"), repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	base := git(t, worktreePath, "rev-parse", "HEAD")
	if base == offTopic {
		t.Errorf("the branch started from the current HEAD (%s) instead of the default branch (%s)", offTopic, defaultCommit)
	}
	if base != defaultCommit {
		t.Errorf("base = %s, expected %s (the default branch)", base, defaultCommit)
	}
}

// The start point is a tracking ref: without --no-track, feat12 would track
// origin/main and `git push` would fail, offering to push onto main instead.
func TestEnsureDoesNotTrackTheDefaultBranch(t *testing.T) {
	repo := testRepoWithOrigin(t, "api")
	worktreePath, err := Ensure(context.Background(), NewGit(), "central", t.TempDir(), wtName("feat12"), repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if upstream, err := tolerantGit(t, worktreePath, "config", "--get", "branch.feat12.merge"); err == nil {
		t.Errorf("feat12 must not track an upstream branch; got: %q", upstream)
	}
}

// Fallback: a purely local repo, without an origin, remains perfectly
// legitimate.
func TestEnsureFallsBackWhenRepoHasNoOrigin(t *testing.T) {
	repo := testRepo(t, "api")
	if _, err := tolerantGit(t, repo, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		t.Fatal("setup: this repo should not have an origin/HEAD")
	}
	worktreePath, err := Ensure(context.Background(), NewGit(), "central", t.TempDir(), wtName("feat12"), repo)
	if err != nil {
		t.Fatalf("a repo without an origin must go through the fallback: %v", err)
	}
	if got := branchOf(t, worktreePath); got != "feat12" {
		t.Errorf("branch = %q, expected feat12", got)
	}
}

// An ignored file is exactly what one does not commit AND cannot find again:
// neither `git status --porcelain` nor the `git worktree remove` safety net
// sees it. This is the one case where Remove used to destroy without a word
// of warning.
func TestRemoveRefusesAnIgnoredFile(t *testing.T) {
	_, worktreePath, target := prepareWorktree(t)
	writeFile(t, filepath.Join(worktreePath, ".gitignore"), ".env\n")
	git(t, worktreePath, "add", ".gitignore")
	git(t, worktreePath, "commit", "-m", "ignore .env")
	writeFile(t, filepath.Join(worktreePath, ".env"), "SECRET=hunter2\n")

	_, err := Remove(context.Background(), NewGit(), target)
	if err == nil {
		t.Fatal("an unrecoverable ignored file must be refused without force")
	}
	for _, expected := range []string{worktreePath, ".env"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the message must name %q; got: %v", expected, err)
		}
	}
	content, err := os.ReadFile(filepath.Join(worktreePath, ".env"))
	if err != nil || string(content) != "SECRET=hunter2\n" {
		t.Errorf("the secret must be INTACT; got %q, err %v", content, err)
	}
}

// Counterpart: a directory ignored as a whole is regenerable cache. Refusing
// on it would make den rm unusable on any JS or Python project.
func TestRemoveAcceptsADirIgnoredAsAWhole(t *testing.T) {
	_, worktreePath, target := prepareWorktree(t)
	writeFile(t, filepath.Join(worktreePath, ".gitignore"), "node_modules/\n")
	git(t, worktreePath, "add", ".gitignore")
	git(t, worktreePath, "commit", "-m", "ignore node_modules")
	if err := os.MkdirAll(filepath.Join(worktreePath, "node_modules", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(worktreePath, "node_modules", "pkg", "index.js"), "module.exports={}\n")

	if _, err := Remove(context.Background(), NewGit(), target); err != nil {
		t.Fatalf("a directory ignored as a whole must not block removal: %v", err)
	}
}

// Directory gone: the git registration survives and blocks any later Ensure.
// Returning nil without doing anything would be lying about a cleanup that
// never happened.
func TestRemoveClearsAnOrphanedRegistration(t *testing.T) {
	repo, worktreePath, target := prepareWorktree(t)
	root := target.Root
	if err := os.RemoveAll(worktreePath); err != nil {
		t.Fatal(err)
	}
	if list := git(t, repo, "worktree", "list"); !strings.Contains(list, "prunable") {
		t.Fatalf("setup: expected an orphaned registration, got:\n%s", list)
	}

	if _, err := Remove(context.Background(), NewGit(), target); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if list := git(t, repo, "worktree", "list"); strings.Contains(list, "prunable") {
		t.Errorf("the orphaned registration must be cleared, got:\n%s", list)
	}
	// The proof that matters: re-spawning the same nest becomes possible
	// again.
	if _, err := Ensure(context.Background(), NewGit(), "central", root, wtName("feat12"), repo); err != nil {
		t.Errorf("the re-spawn must succeed after cleanup: %v", err)
	}
}

// As long as the orphaned registration is there, Ensure must say what to do,
// not relay git's "fatal: ... missing but already registered".
func TestEnsureReportsAnOrphanedRegistration(t *testing.T) {
	repo, worktreePath, target := prepareWorktree(t)
	root := target.Root
	if err := os.RemoveAll(worktreePath); err != nil {
		t.Fatal(err)
	}

	_, err := Ensure(context.Background(), NewGit(), "central", root, wtName("feat12"), repo)
	if err == nil {
		t.Fatal("an orphaned registration must produce an actionable error")
	}
	for _, expected := range []string{worktreePath, "den rm"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the message must contain %q; got: %v", expected, err)
		}
	}
	if strings.Contains(err.Error(), "fatal:") {
		t.Errorf("the message must not relay git's English fatal error; got: %v", err)
	}
}

// worktree_root is global and Path only keeps the repo's basename: two nests
// pointing at same-named repos aim at the same directory. Without a guard,
// the second repo walks away with the first repo's worktree and the agent
// commits to the wrong place.
func TestEnsureRefusesAWorktreeBelongingToAnotherRepo(t *testing.T) {
	base := t.TempDir()
	repoA := filepath.Join(base, "acme", "api")
	repoB := filepath.Join(base, "beta", "api")
	createRepo(t, repoA)
	createRepo(t, repoB)
	root := t.TempDir()

	worktreePathA, err := Ensure(context.Background(), NewGit(), "central", root, wtName("feat12"), repoA)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err = Ensure(context.Background(), NewGit(), "central", root, wtName("feat12"), repoB)
	if err == nil {
		t.Fatalf("%s's worktree must not be served to %s", repoA, repoB)
	}
	for _, expected := range []string{repoA, repoB} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the message must name both repos, missing %q; got: %v", expected, err)
		}
	}
	// A's worktree remains A's.
	//
	// Both sides are RESOLVED before comparison: git returns the path through
	// EvalSymlinks, while the test holds the one t.TempDir gave. On macOS the
	// two always differ ($TMPDIR lives under /var, a symlink to
	// /private/var), so this test used to fail on that sole difference of
	// form — on a perfectly intact repo. That is what samePath exists for,
	// already used elsewhere in this package for the same comparison.
	got := git(t, worktreePathA, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if !strings.HasPrefix(resolvePath(got), resolvePath(repoA)+string(filepath.Separator)) {
		t.Errorf("A's worktree changed repo: %s (expected under %s)", got, repoA)
	}
}

// In per-repo layout the target is ALWAYS under a repo: without verifying the
// path is a worktree root, git answers for the enclosing repo and den
// validates an empty directory, which it would mount as is into the VM.
func TestEnsureRefusesADirThatIsNotAWorktree(t *testing.T) {
	repo := testRepo(t, "api")
	// The repo is on feat12: the branch matches, so the spec §11 guard sees
	// nothing wrong.
	git(t, repo, "checkout", "-q", "-b", "feat12")
	target := filepath.Join(repo, ".den", "feat12")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := Ensure(context.Background(), NewGit(), "per-repo", t.TempDir(), wtName("feat12"), repo)
	if err == nil {
		t.Fatal("an empty directory is not a worktree: Ensure must refuse")
	}
	if !strings.Contains(err.Error(), target) {
		t.Errorf("the message must name %q; got: %v", target, err)
	}
}

// cmd.Dir is not isolation: GIT_DIR takes precedence. den is meant to run
// under agents and git hooks, where these variables are set — and
// dirtyFiles would then decide on a destruction based on a different
// repository.
func TestGitNeutralizesGitEnvironmentVariables(t *testing.T) {
	victim := testRepo(t, "victim")
	other := testRepo(t, "other")
	git(t, other, "checkout", "-q", "-b", "other-branch")

	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)

	out, err := NewGit().Run(context.Background(), victim, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "main" {
		t.Errorf("the environment's GIT_DIR hijacked the command: branch = %q, expected main", got)
	}
}

// per-repo layout: without an exclusion, the user's repo stays permanently
// "?? .den/" — persistent noise, a git-add -A risk, and a guaranteed false
// positive for any future "clean" check.
func TestEnsurePerRepoDoesNotDirtyTheRepo(t *testing.T) {
	repo := testRepo(t, "api")

	if _, err := Ensure(context.Background(), NewGit(), "per-repo", t.TempDir(), wtName("feat12"), repo); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status := git(t, repo, "status", "--porcelain"); status != "" {
		t.Errorf("the parent repo must stay clean; git status: %q", status)
	}

	// Idempotence: a second worktree must not rewrite the line.
	if _, err := Ensure(context.Background(), NewGit(), "per-repo", t.TempDir(), wtName("feat13"), repo); err != nil {
		t.Fatalf("second worktree: %v", err)
	}
	exclude := filepath.Join(repo, ".git", "info", "exclude")
	content, err := os.ReadFile(exclude)
	if err != nil {
		t.Fatalf("reading %s: %v", exclude, err)
	}
	if n := strings.Count(string(content), ".den/"); n != 1 {
		t.Errorf(".den/ must be excluded exactly once, found %d times in %s", n, exclude)
	}
}

// Remove must not delete a worktree that does not belong to the repo it is
// given: nothing guarantees the caller correctly pairs the two.
func TestRemoveRefusesAWorktreeFromAnotherRepo(t *testing.T) {
	base := t.TempDir()
	repoA := filepath.Join(base, "acme", "api")
	repoB := filepath.Join(base, "beta", "api")
	createRepo(t, repoA)
	createRepo(t, repoB)

	root := t.TempDir()
	worktreePath, err := Ensure(context.Background(), NewGit(), "central", root, wtName("feat12"), repoA)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Both repos are named "api": Path, which only keeps the basename, names
	// for repoB the worktree repoA just obtained.
	targetB := Target{
		DenHome: t.TempDir(), Layout: "central", Root: root,
		Nest: "beta.feat12", Worktree: "feat12", RepoPath: repoB, Force: true,
	}
	if got := Path(targetB.Layout, targetB.Root, targetB.Worktree, targetB.RepoPath); got != worktreePath {
		t.Fatalf("setup: both targets must aim at the same directory; %q vs %q", got, worktreePath)
	}

	_, err = Remove(context.Background(), NewGit(), targetB)
	if err == nil {
		t.Fatal("Remove must refuse a worktree foreign to the given repo")
	}
	// git already refuses on its own; only den's message markers distinguish
	// our guard from that fallback.
	for _, expected := range []string{repoA, repoB} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the message must name both repos, missing %q; got: %v", expected, err)
		}
	}
	if _, err := os.Stat(worktreePath); err != nil {
		t.Errorf("A's worktree must be INTACT: %v", err)
	}
}

// spyGit records the argv passed to git while delegating to the real thing:
// the only way to observe that Remove leaves git's own safety net armed.
type spyGit struct {
	real  Git
	calls [][]string
}

func (g *spyGit) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return g.RunWithInput(ctx, dir, nil, args...)
}

func (g *spyGit) RunWithInput(ctx context.Context, dir string, input []byte, args ...string) ([]byte, error) {
	g.calls = append(g.calls, append([]string(nil), args...))
	return g.real.RunWithInput(ctx, dir, input, args...)
}

// count reports how many calls start with this prefix. The COUNT is what
// distinguishes "one question asked for the whole batch" from "one question
// per entry" — the pattern this module already had to remove twice.
func (g *spyGit) count(prefix ...string) int {
	n := 0
	for _, a := range g.calls {
		if startsWith(a, prefix) {
			n++
		}
	}
	return n
}

func (g *spyGit) call(prefix ...string) []string {
	for _, a := range g.calls {
		if len(a) >= len(prefix) && slices.Equal(a[:len(prefix)], prefix) {
			return a
		}
	}
	return nil
}

// den no longer deletes: it moves. `git worktree remove` carries its own
// safety net, but that net is the SAME code as `git status`: when status
// sees nothing, remove sees nothing either. Calling `remove` would therefore
// reintroduce irreversibility without adding any protection.
func TestRemoveNeverCallsWorktreeRemove(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprintf("force=%v", force), func(t *testing.T) {
			_, _, target := prepareWorktree(t)
			spy := &spyGit{real: NewGit()}
			if _, err := Remove(context.Background(), spy, withForce(target, force)); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if argv := spy.call("worktree", "remove"); argv != nil {
				t.Errorf("`git worktree remove` was issued: %v", argv)
			}
			if argv := spy.call("worktree", "prune"); argv == nil {
				t.Errorf("the registration must be pruned; calls: %v", spy.calls)
			}
		})
	}
}

// A trailing "/" alone is not a discriminant: git collapses to a single
// entry `!! conf/` a directory the user NEVER ignored, as soon as its whole
// content is. And `*.env` is the most common way to ignore secrets — the
// collapsed directory is not regenerable cache.
func TestRemoveRefusesASecretInANonIgnoredDir(t *testing.T) {
	_, worktreePath, target := prepareWorktree(t)
	writeFile(t, filepath.Join(worktreePath, ".gitignore"), "*.env\n")
	git(t, worktreePath, "add", ".gitignore")
	git(t, worktreePath, "commit", "-m", "ignore .env files")
	if err := os.MkdirAll(filepath.Join(worktreePath, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(worktreePath, "conf", "prod.env")
	writeFile(t, secret, "SECRET=hunter2\n")

	// The directory itself is not ignored: only its content is.
	if _, err := tolerantGit(t, worktreePath, "check-ignore", "-q", "--", "conf/"); err == nil {
		t.Fatal("setup: conf/ should not be ignored itself")
	}

	_, err := Remove(context.Background(), NewGit(), target)
	if err == nil {
		t.Fatal("a non-ignored directory whose content is ignored must be refused without force")
	}
	if !strings.Contains(err.Error(), "conf/") {
		t.Errorf("the message must name conf/; got: %v", err)
	}
	content, err := os.ReadFile(secret)
	if err != nil || string(content) != "SECRET=hunter2\n" {
		t.Errorf("the secret must be INTACT; got %q, err %v", content, err)
	}
}

// Symmetric false positive: git quotes and escapes "special" paths, so an
// ignored-as-a-whole directory with a non-ASCII name or a space no longer
// ends with "/" and would make den rm refuse on plain cache — while showing
// the user an octal path they would not recognize.
func TestRemoveAcceptsAnIgnoredDirWithAQuotedName(t *testing.T) {
	_, worktreePath, target := prepareWorktree(t)
	writeFile(t, filepath.Join(worktreePath, ".gitignore"), "日本語/\nmy cache/\n")
	git(t, worktreePath, "add", ".gitignore")
	git(t, worktreePath, "commit", "-m", "ignore caches")
	for _, name := range []string{"日本語", "my cache"} {
		if err := os.MkdirAll(filepath.Join(worktreePath, name), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(worktreePath, name, "x.bin"), "cache\n")
	}

	if _, err := Remove(context.Background(), NewGit(), target); err != nil {
		t.Fatalf("an ignored dir with a quoted name must not block removal: %v", err)
	}
}

// `git worktree prune` silently skips locked worktrees — and `lock` exists
// precisely for removable volumes, hence for the case where the directory
// legitimately disappears. Without a re-check, Remove would return nil
// claiming to have cleaned up, and Ensure would point back to `den rm`: a
// closed loop.
func TestRemoveReportsALockedWorktree(t *testing.T) {
	repo, worktreePath, target := prepareWorktree(t)
	git(t, repo, "worktree", "lock", worktreePath)
	if err := os.RemoveAll(worktreePath); err != nil {
		t.Fatal(err)
	}

	for _, force := range []bool{false, true} {
		_, err := Remove(context.Background(), NewGit(), withForce(target, force))
		if err == nil {
			t.Fatalf("force=%v: Remove must not claim to have cleaned up a locked registration", force)
		}
		for _, expected := range []string{worktreePath, "git worktree unlock"} {
			if !strings.Contains(err.Error(), expected) {
				t.Errorf("force=%v: the message must contain %q; got: %v", force, expected, err)
			}
		}
	}
}

// A trailing "/" changes check-ignore's answer: in git's wildmatch, `*` and
// `**` match the empty string, and git applies the directory's own
// .gitignore to the empty component following the "/". Asking git WITH the
// "/" therefore means "is this directory OR its empty-named child ignored?"
// — and `logs/*`, like a nested .gitignore of `*`, are common idioms.
func TestRemoveRefusesASecretUnderAContentOnlyRule(t *testing.T) {
	cases := []struct {
		name   string
		root   string // .gitignore at the worktree root
		nested string // .gitignore inside conf/
	}{
		{"rule on content", "conf/*\n", ""},
		{"recursive rule", "conf/**\n", ""},
		{"nested gitignore", "unrelated\n", "*\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, worktreePath, target := prepareWorktree(t)
			writeFile(t, filepath.Join(worktreePath, ".gitignore"), c.root)
			git(t, worktreePath, "add", ".gitignore")
			git(t, worktreePath, "commit", "-m", "rules")
			if err := os.MkdirAll(filepath.Join(worktreePath, "conf"), 0o755); err != nil {
				t.Fatal(err)
			}
			if c.nested != "" {
				writeFile(t, filepath.Join(worktreePath, "conf", ".gitignore"), c.nested)
			}
			secret := filepath.Join(worktreePath, "conf", "prod.env")
			writeFile(t, secret, "SECRET=hunter2\n")

			// The directory itself is not ignored; only its content is.
			if _, err := tolerantGit(t, worktreePath, "check-ignore", "-q", "--", "conf"); err == nil {
				t.Fatal("setup: conf (without /) should not be ignored")
			}

			_, err := Remove(context.Background(), NewGit(), target)
			if err == nil {
				t.Fatal("a rule that only covers the CONTENT does not make the directory disposable")
			}
			if !strings.Contains(err.Error(), "conf/") {
				t.Errorf("the message must name conf/; got: %v", err)
			}
			if content, err := os.ReadFile(secret); err != nil || string(content) != "SECRET=hunter2\n" {
				t.Errorf("the secret must be INTACT; got %q, err %v", content, err)
			}
		})
	}
}

// `status.showUntrackedFiles = no` is a common performance setting on large
// repos. den reads git under the user's config: without explicitly asking
// for untracked files, the whole spec §14 guard disarms itself.
func TestRemoveSeesUntrackedWorkDespiteRepoConfig(t *testing.T) {
	_, worktreePath, target := prepareWorktree(t)
	git(t, worktreePath, "config", "status.showUntrackedFiles", "no")
	writeFile(t, filepath.Join(worktreePath, "draft.txt"), "wip\n")

	_, err := Remove(context.Background(), NewGit(), target)
	if err == nil {
		t.Fatal("the untracked draft must be refused regardless of repo config")
	}
	// den's own message markers: git may also refuse on its own side.
	for _, expected := range []string{"draft.txt", "uncommitted"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the message must contain %q; got: %v", expected, err)
		}
	}
	if _, err := os.Stat(filepath.Join(worktreePath, "draft.txt")); err != nil {
		t.Errorf("the draft must be INTACT: %v", err)
	}
}

// skip-worktree and assume-unchanged exist PRECISELY to carry local
// modifications one does not want to commit — and git then hides them
// entirely from `status`. The `git worktree remove` safety net does not see
// them either, so nothing protects that work.
func TestRemoveRefusesALocallyMarkedFile(t *testing.T) {
	for _, flag := range []string{"--skip-worktree", "--assume-unchanged"} {
		t.Run(flag, func(t *testing.T) {
			_, worktreePath, target := prepareWorktree(t)
			writeFile(t, filepath.Join(worktreePath, "local.conf"), "v1\n")
			git(t, worktreePath, "add", "local.conf")
			git(t, worktreePath, "commit", "-m", "add local.conf")
			git(t, worktreePath, "update-index", flag, "local.conf")
			writeFile(t, filepath.Join(worktreePath, "local.conf"), "IRREPLACEABLE WORK\n")

			// git reports nothing: that is the whole problem.
			if status := git(t, worktreePath, "status", "--porcelain"); status != "" {
				t.Fatalf("setup: git should report nothing, got %q", status)
			}

			_, err := Remove(context.Background(), NewGit(), target)
			if err == nil {
				t.Fatal("a locally marked, modified file must not be destroyed without force")
			}
			if !strings.Contains(err.Error(), "local.conf") {
				t.Errorf("the message must name local.conf; got: %v", err)
			}
			if content, err := os.ReadFile(filepath.Join(worktreePath, "local.conf")); err != nil ||
				string(content) != "IRREPLACEABLE WORK\n" {
				t.Errorf("the work must be INTACT; got %q, err %v", content, err)
			}
		})
	}
}

// sparse-checkout sets the SAME skip-worktree bits, but on files
// deliberately absent from disk. Counting them as dirty would make `den rm`
// impossible without --force on any sparse-checkout repo. The discriminant
// is presence: a file marked AND present carries local content git refuses
// to look at; marked and absent, it is simply outside the cone.
func TestRemoveAcceptsASparseCheckout(t *testing.T) {
	_, worktreePath, target := prepareWorktree(t)
	for _, d := range []string{"kept", "excluded"} {
		if err := os.MkdirAll(filepath.Join(worktreePath, d), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(worktreePath, d, "f.txt"), "content\n")
	}
	git(t, worktreePath, "add", ".")
	git(t, worktreePath, "commit", "-m", "two directories")
	git(t, worktreePath, "sparse-checkout", "set", "--no-cone", "kept")

	// Setup: git does mark files outside the cone. The exact flag depends on
	// repo config (`S`, or `s` if assume-unchanged also applies); only its
	// presence matters, not its letter.
	flags := git(t, worktreePath, "ls-files", "-v")
	if !strings.Contains(flags, " excluded/f.txt") ||
		strings.Contains(flags, "H excluded/f.txt") {
		t.Fatalf("setup: expected a mark on excluded/f.txt, got:\n%s", flags)
	}
	if _, err := os.Stat(filepath.Join(worktreePath, "excluded", "f.txt")); !os.IsNotExist(err) {
		t.Fatalf("setup: the out-of-cone file should be absent from disk")
	}

	if _, err := Remove(context.Background(), NewGit(), target); err != nil {
		t.Fatalf("a sparse-checkout must not block removal: %v", err)
	}
}

// core.ignoreStat sets the assume-unchanged bit on the WHOLE repo. Counting
// those entries without inspecting them made `den rm` impossible without
// --force on a perfectly clean worktree — and the bits SURVIVE the setting
// being removed, so the block was permanent.
func TestRemoveAcceptsARepoUnderIgnoreStat(t *testing.T) {
	prepare := func(t *testing.T) (worktreePath string, target Target) {
		t.Helper()
		_, worktreePath, target = prepareWorktree(t)
		git(t, worktreePath, "config", "core.ignoreStat", "true")
		// DISTINCT content: with three identical files, any permutation of
		// hashes would be a no-op and the "intact files must not be named"
		// assertion would prove nothing.
		for _, f := range []string{"f1.txt", "f2.txt", "f3.txt"} {
			writeFile(t, filepath.Join(worktreePath, f), "content of "+f+"\n")
		}
		git(t, worktreePath, "add", ".")
		git(t, worktreePath, "commit", "-m", "files")
		if flags := git(t, worktreePath, "ls-files", "-v"); !strings.Contains(flags, "h f1.txt") {
			t.Fatalf("setup: expected the assume-unchanged bit, got:\n%s", flags)
		}
		return worktreePath, target
	}

	t.Run("setting active", func(t *testing.T) {
		_, target := prepare(t)
		if _, err := Remove(context.Background(), NewGit(), target); err != nil {
			t.Fatalf("a clean worktree must stay removable: %v", err)
		}
	})

	// The case that makes the block permanent: the setting is removed, the
	// bits stay. Reading core.ignoreStat alone would therefore not unblock
	// anything.
	t.Run("setting removed but bits persist", func(t *testing.T) {
		worktreePath, target := prepare(t)
		git(t, worktreePath, "config", "--unset", "core.ignoreStat")
		if flags := git(t, worktreePath, "ls-files", "-v"); !strings.Contains(flags, "h f1.txt") {
			t.Fatalf("setup: the bits must survive --unset, got:\n%s", flags)
		}
		if _, err := Remove(context.Background(), NewGit(), target); err != nil {
			t.Fatalf("a clean worktree must stay removable: %v", err)
		}
	})

	// Counterpart: under the same setting, a REAL modification stays
	// refused.
	t.Run("real modification under the same setting", func(t *testing.T) {
		worktreePath, target := prepare(t)
		writeFile(t, filepath.Join(worktreePath, "f2.txt"), "IRREPLACEABLE WORK\n")
		if status := git(t, worktreePath, "status", "--porcelain"); status != "" {
			t.Fatalf("setup: git should see nothing, got %q", status)
		}

		_, err := Remove(context.Background(), NewGit(), target)
		if err == nil {
			t.Fatal("a real modification under assume-unchanged must be refused")
		}
		if !strings.Contains(err.Error(), "f2.txt") {
			t.Errorf("the message must name f2.txt; got: %v", err)
		}
		if strings.Contains(err.Error(), "f1.txt") || strings.Contains(err.Error(), "f3.txt") {
			t.Errorf("the intact files must not be named; got: %v", err)
		}
	})
}

// `git hash-object` is NOT safe on a non-regular path: on a fifo it never
// returns (measured: rc=124 on timeout, no failure), on a directory it exits
// with an English `fatal:`. A `den rm` that never returns has no result left
// to wrap.
func TestRemoveRefusesAPathGitCannotHash(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, path string)
	}{
		{"fifo", func(t *testing.T, path string) {
			if err := syscall.Mkfifo(path, 0o644); err != nil {
				t.Skipf("fifo unavailable: %v", err)
			}
		}},
		{"directory", func(t *testing.T, path string) {
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, worktreePath, target := prepareWorktree(t)
			tracked := filepath.Join(worktreePath, "tracked.txt")
			writeFile(t, tracked, "v1\n")
			git(t, worktreePath, "add", "tracked.txt")
			git(t, worktreePath, "commit", "-m", "add tracked.txt")
			git(t, worktreePath, "update-index", "--assume-unchanged", "tracked.txt")
			if err := os.Remove(tracked); err != nil {
				t.Fatal(err)
			}
			c.setup(t, tracked)

			// Bounded: before the fix the fifo blocks, and only this limit
			// gets it out.
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			start := time.Now()
			_, err := Remove(ctx, NewGit(), target)
			elapsed := time.Since(start)

			if elapsed > 5*time.Second {
				t.Errorf("Remove took %s: the probe blocked on the non-regular path", elapsed)
			}
			if err == nil {
				t.Fatal("a path git cannot hash must not be destroyed without force")
			}
			for _, expected := range []string{"tracked.txt", "uncommitted"} {
				if !strings.Contains(err.Error(), expected) {
					t.Errorf("the message must contain %q; got: %v", expected, err)
				}
			}
			if strings.Contains(err.Error(), "fatal:") {
				t.Errorf("the message must not relay git's English fatal error; got: %v", err)
			}
		})
	}
}

// git hashes the TEXT of a symlink (mode 120000); hash-object, on the other
// hand, FOLLOWS the link and would hash the target's content. The two never
// coincide, so a clean repo with a single tracked symlink was refused
// forever as soon as an index bit marked it.
func TestRemoveAcceptsAnIntactSymlink(t *testing.T) {
	cases := []struct{ name, target string }{
		{"valid symlink", "target.txt"},
		{"broken symlink", "/does/not/exist"},
		// Two texts a TrimSpace or a line-by-line comparison would corrupt:
		// without them, a comparison tolerating trimming would go
		// unnoticed.
		{"target has a trailing space", "target.txt "},
		{"target has an internal newline", "a\nb"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, worktreePath, target := prepareWorktree(t)
			writeFile(t, filepath.Join(worktreePath, "target.txt"), "TARGET CONTENT\n")
			if err := os.Symlink(c.target, filepath.Join(worktreePath, "link.txt")); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			git(t, worktreePath, "add", ".")
			git(t, worktreePath, "commit", "-m", "target and link")
			git(t, worktreePath, "update-index", "--skip-worktree", "link.txt")
			if status := git(t, worktreePath, "status", "--porcelain"); status != "" {
				t.Fatalf("setup: the worktree must be clean, got %q", status)
			}

			if _, err := Remove(context.Background(), NewGit(), target); err != nil {
				t.Fatalf("an intact symlink must not block removal: %v", err)
			}
		})
	}
}

// The property the whole round-4 fix rests on: hash-object applies
// .gitattributes filters. Without it, a repo in autocrlf whose disk is in
// CRLF — hence perfectly clean — would be refused.
func TestRemoveAppliesGitattributesFilters(t *testing.T) {
	_, worktreePath, target := prepareWorktree(t)
	git(t, worktreePath, "config", "core.autocrlf", "true")
	writeFile(t, filepath.Join(worktreePath, ".gitattributes"), "* text=auto\n")
	writeFile(t, filepath.Join(worktreePath, "f.txt"), "a line\nanother\n")
	git(t, worktreePath, "add", ".")
	git(t, worktreePath, "commit", "-m", "text")
	// What a checkout under autocrlf would produce: the disk switches to
	// CRLF.
	writeFile(t, filepath.Join(worktreePath, "f.txt"), "a line\r\nanother\r\n")
	git(t, worktreePath, "update-index", "--skip-worktree", "f.txt")
	if status := git(t, worktreePath, "status", "--porcelain"); status != "" {
		t.Fatalf("setup: the worktree must be clean, got %q", status)
	}

	if _, err := Remove(context.Background(), NewGit(), target); err != nil {
		t.Fatalf("converted line endings are not a modification: %v", err)
	}
}

// A tracked file replaced on disk by a broken symlink carries content the
// object store cannot return. os.Stat followed the link and used to
// exclude it.
func TestRemoveRefusesAMarkedFileReplacedBySymlink(t *testing.T) {
	_, worktreePath, target := prepareWorktree(t)
	tracked := filepath.Join(worktreePath, "tracked.txt")
	writeFile(t, tracked, "v1\n")
	git(t, worktreePath, "add", "tracked.txt")
	git(t, worktreePath, "commit", "-m", "add tracked.txt")
	git(t, worktreePath, "update-index", "--assume-unchanged", "tracked.txt")
	if err := os.Remove(tracked); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/does/not/exist", tracked); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, err := Remove(context.Background(), NewGit(), target)
	if err == nil {
		t.Fatal("a tracked file replaced by a broken symlink must not be destroyed without force")
	}
	if !strings.Contains(err.Error(), "tracked.txt") {
		t.Errorf("the message must name tracked.txt; got: %v", err)
	}
}

// The edge case that decides the mode check: a tracked file whose content is
// EXACTLY the text of the symlink replacing it. Comparing bytes alone would
// conclude "identical" and destroy the file; it is the index entry's mode
// (100644 vs 120000) that decides.
func TestRemoveRefusesAFileReplacedBySymlinkWithSameText(t *testing.T) {
	_, worktreePath, target := prepareWorktree(t)
	writeFile(t, filepath.Join(worktreePath, "target.txt"), "CONTENT\n")
	// Content with no newline, identical to the future symlink's text.
	if err := os.WriteFile(filepath.Join(worktreePath, "tracked.txt"), []byte("target.txt"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, worktreePath, "add", ".")
	git(t, worktreePath, "commit", "-m", "file and target")
	git(t, worktreePath, "update-index", "--skip-worktree", "tracked.txt")
	if err := os.Remove(filepath.Join(worktreePath, "tracked.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.txt", filepath.Join(worktreePath, "tracked.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, err := Remove(context.Background(), NewGit(), target)
	if err == nil {
		t.Fatal("a tracked file turned into a symlink is a modification, even with equal text")
	}
	if !strings.Contains(err.Error(), "tracked.txt") {
		t.Errorf("the message must name tracked.txt; got: %v", err)
	}
}

// core.fsmonitor delegates "what changed" to a daemon. A restarted daemon, a
// Watchman that lost its state, or a mount where inotify drops events all
// answer "nothing", and git believes them. The flag stays UPPERCASE in
// ls-files -v: the index-bit probe cannot see this case by construction.
func TestRemoveSeesAModificationDespiteFsmonitor(t *testing.T) {
	_, worktreePath, target := prepareWorktree(t)
	writeFile(t, filepath.Join(worktreePath, "tracked.txt"), "v1\n")
	git(t, worktreePath, "add", "tracked.txt")
	git(t, worktreePath, "commit", "-m", "add tracked.txt")

	// A v2 hook answering "nothing changed": a token, no paths.
	hook := filepath.Join(t.TempDir(), "fsmonitor.sh")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nprintf 'token\\0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, worktreePath, "config", "core.fsmonitor", hook)
	git(t, worktreePath, "config", "core.fsmonitorHookVersion", "2")
	git(t, worktreePath, "status", "--porcelain") // primes the poisoned cache

	writeFile(t, filepath.Join(worktreePath, "tracked.txt"), "IRREPLACEABLE WORK\n")
	if status := git(t, worktreePath, "status", "--porcelain"); strings.Contains(status, "tracked.txt") {
		t.Skipf("the fsmonitor hook did not take: git reports %q", status)
	}
	if flags := git(t, worktreePath, "ls-files", "-v"); !strings.Contains(flags, "H tracked.txt") {
		t.Fatalf("setup: expected an uppercase flag, got:\n%s", flags)
	}

	_, err := Remove(context.Background(), NewGit(), target)
	if err == nil {
		t.Fatal("a modification hidden by fsmonitor must not be destroyed without force")
	}
	if !strings.Contains(err.Error(), "tracked.txt") {
		t.Errorf("the message must name tracked.txt; got: %v", err)
	}
}

func TestRemoveRefusesWhenAProbeIsSilent(t *testing.T) {
	for _, probe := range []string{"ls-files", "hash-object"} {
		t.Run(probe, func(t *testing.T) {
			_, worktreePath, target := prepareWorktree(t)
			writeFile(t, filepath.Join(worktreePath, "local.conf"), "v1\n")
			git(t, worktreePath, "add", "local.conf")
			git(t, worktreePath, "commit", "-m", "add local.conf")
			git(t, worktreePath, "update-index", "--skip-worktree", "local.conf")

			g := gitFailingOn{real: NewGit(), prefix: []string{probe}}
			_, err := Remove(context.Background(), g, target)
			if err == nil {
				t.Fatalf("a failure of git %s must not authorize removal", probe)
			}
			// The real cause must surface: swallowing the error and refusing
			// only through a downstream guard would pass here by accident.
			if !strings.Contains(err.Error(), "simulated failure") {
				t.Errorf("the failure must surface unchanged; got: %v", err)
			}
			if _, err := os.Stat(worktreePath); err != nil {
				t.Errorf("the worktree must be INTACT: %v", err)
			}
		})
	}
}

// The dead-end guards only run once the directory is gone — i.e. when
// EvalSymlinks fails on both sides of samePath and falls back to two
// different raw strings. On macOS, $TMPDIR lives under /var -> private/var:
// resolution is structurally absent exactly where it is needed.
func TestDeadEndGuardsHoldUnderASymlinkedRoot(t *testing.T) {
	prepare := func(t *testing.T) (repo, worktreePath string, target Target) {
		t.Helper()
		repo = testRepo(t, "api")
		base := t.TempDir()
		real := filepath.Join(base, "real")
		if err := os.MkdirAll(real, 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(base, "link")
		if err := os.Symlink(real, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		target = Target{
			DenHome: t.TempDir(), Layout: "central", Root: link,
			Nest: "api.feat12", Worktree: "feat12", RepoPath: repo,
		}
		worktreePath, err := Ensure(context.Background(), NewGit(), target.Layout, target.Root, wtName(target.Worktree), target.RepoPath)
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
		return repo, worktreePath, target
	}

	t.Run("Remove on a locked registration", func(t *testing.T) {
		repo, worktreePath, target := prepare(t)
		git(t, repo, "worktree", "lock", worktreePath)
		if err := os.RemoveAll(worktreePath); err != nil {
			t.Fatal(err)
		}
		_, err := Remove(context.Background(), NewGit(), target)
		if err == nil {
			t.Fatal("Remove must not claim to have cleaned up a locked registration")
		}
		if !strings.Contains(err.Error(), "git worktree unlock") {
			t.Errorf("the message must cite git worktree unlock; got: %v", err)
		}
	})

	t.Run("Ensure on an orphaned registration", func(t *testing.T) {
		repo, worktreePath, _ := prepare(t)
		git(t, repo, "worktree", "lock", worktreePath)
		if err := os.RemoveAll(worktreePath); err != nil {
			t.Fatal(err)
		}
		_, err := Ensure(context.Background(), NewGit(), "central", filepath.Dir(filepath.Dir(worktreePath)), wtName("feat12"), repo)
		if err == nil {
			t.Fatal("a surviving registration must produce an error")
		}
		if strings.Contains(err.Error(), "fatal:") {
			t.Errorf("the message must not relay git's English fatal error; got: %v", err)
		}
		if !strings.Contains(err.Error(), "den rm") {
			t.Errorf("the message must point to den rm; got: %v", err)
		}
	})
}

// Under -z a rename occupies two records, and the second — the source path
// — carries no status prefix. Left unconsumed, it gets read back as a
// record of its own the moment its 3rd character is a space — and den then
// names the user a file that does not exist.
func TestRemoveDoesNotInventAFileOnARename(t *testing.T) {
	_, worktreePath, target := prepareWorktree(t)
	writeFile(t, filepath.Join(worktreePath, "ab cd.txt"), "content\n")
	git(t, worktreePath, "add", "ab cd.txt")
	git(t, worktreePath, "commit", "-m", "add ab cd.txt")
	git(t, worktreePath, "mv", "ab cd.txt", "xy.txt")

	_, err := Remove(context.Background(), NewGit(), target)
	if err == nil {
		t.Fatal("an uncommitted rename is a modification: refusal expected")
	}
	if !strings.Contains(err.Error(), "xy.txt") {
		t.Errorf("the message must name the renamed file; got: %v", err)
	}
	// "cd.txt" is what reading back the source path "ab cd.txt" would
	// produce.
	if strings.Contains(err.Error(), "cd.txt") {
		t.Errorf("the message invents a file from the source path; got: %v", err)
	}
}

// On macOS, /var and $TMPDIR are symlinks: worktree_root is reached through
// one in practice. git answers with the RESOLVED path while den handles the
// one it was given — without resolution, both idempotence and removal
// break. Linux alone cannot see this regression, /tmp not being a symlink
// there.
func TestEnsureFollowsASymlinkedWorktreeRoot(t *testing.T) {
	repo := testRepo(t, "api")
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	first, err := Ensure(context.Background(), NewGit(), "central", link, wtName("feat12"), repo)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	second, err := Ensure(context.Background(), NewGit(), "central", link, wtName("feat12"), repo)
	if err != nil {
		t.Fatalf("idempotence must hold through the symlink: %v", err)
	}
	if first != second {
		t.Errorf("diverging paths: %q then %q", first, second)
	}
	target := Target{
		DenHome: t.TempDir(), Layout: "central", Root: link,
		Nest: "api.feat12", Worktree: "feat12", RepoPath: repo,
	}
	if _, err := Remove(context.Background(), NewGit(), target); err != nil {
		t.Errorf("removal must hold through the symlink: %v", err)
	}
}

// ── The trash ────────────────────────────────────────────────────────────
//
// Five rounds of review proved that the enumeration of the ways git hides
// work does not converge, and that the `git worktree remove` safety net
// falls WITH `git status`'s, since it is the same code. The answer is not
// one more verdict: it is to never delete.

// readEntries returns the names of a directory's entries, or nil if it does
// not exist.
func readEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("reading %s: %v", dir, err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	slices.Sort(names)
	return names
}

// The structural property: the directory is not destroyed, it is moved.
func TestRemoveMovesTheWorktreeToTrash(t *testing.T) {
	repo, worktreePath, target := prepareWorktree(t)
	writeFile(t, filepath.Join(worktreePath, "tracked.txt"), "committed\n")
	git(t, worktreePath, "add", "tracked.txt")
	git(t, worktreePath, "commit", "-m", "add tracked.txt")

	dest, err := Remove(context.Background(), NewGit(), target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Errorf("the worktree must have left %s; got: %v", worktreePath, err)
	}
	trash := filepath.Join(target.DenHome, "trash")
	if dest == "" {
		t.Fatal("Remove must return the path of the trash entry")
	}
	if filepath.Dir(dest) != trash {
		t.Errorf("the entry must live under %s; got %s", trash, dest)
	}
	// The name must let the user find THEIR directory among others: without
	// the nest and the repo, the trash is an anonymous pile.
	for _, expected := range []string{target.Nest, filepath.Base(repo)} {
		if !strings.Contains(filepath.Base(dest), expected) {
			t.Errorf("the entry name must contain %q; got %q", expected, filepath.Base(dest))
		}
	}
	if content, err := os.ReadFile(filepath.Join(dest, "tracked.txt")); err != nil || string(content) != "committed\n" {
		t.Errorf("the content must have followed the move; got %q, err %v", content, err)
	}

	// The git registration must be pruned, or Ensure would refuse to
	// recreate this worktree forever.
	if list := git(t, repo, "worktree", "list"); strings.Contains(list, worktreePath) {
		t.Errorf("the registration must be pruned, got:\n%s", list)
	}
	if _, err := Ensure(context.Background(), NewGit(), target.Layout, target.Root, wtName(target.Worktree), target.RepoPath); err != nil {
		t.Errorf("the re-spawn must succeed after the move to trash: %v", err)
	}
	// The branch survives: the user's commits are never at stake.
	if _, err := tolerantGit(t, repo, "show-ref", "--verify", "refs/heads/"+target.Worktree); err != nil {
		t.Errorf("branch %s must survive: %v", target.Worktree, err)
	}
}

// THE case the trash exists to make reversible: --force no longer loses
// anything. An IGNORED file is the worst of the three — invisible to `git
// status`, to the `git worktree remove` safety net, and absent from the
// object store.
func TestRemoveWithForceKeepsTheWorkInTrash(t *testing.T) {
	_, worktreePath, target := prepareWorktree(t)
	writeFile(t, filepath.Join(worktreePath, ".gitignore"), ".env\n")
	git(t, worktreePath, "add", ".gitignore")
	git(t, worktreePath, "commit", "-m", "ignore .env")
	writeFile(t, filepath.Join(worktreePath, ".env"), "SECRET=hunter2\n")
	writeFile(t, filepath.Join(worktreePath, "draft.txt"), "IRREPLACEABLE WORK\n")

	dest, err := Remove(context.Background(), NewGit(), withForce(target, true))
	if err != nil {
		t.Fatalf("with force, the move to trash must succeed: %v", err)
	}
	for _, c := range []struct{ name, content string }{
		{".env", "SECRET=hunter2\n"},
		{"draft.txt", "IRREPLACEABLE WORK\n"},
	} {
		got, err := os.ReadFile(filepath.Join(dest, c.name))
		if err != nil || string(got) != c.content {
			t.Errorf("%s must be recoverable in %s; got %q, err %v", c.name, dest, got, err)
		}
	}
}

// den_home and worktree_root can live on different filesystems: os.Rename
// then fails with EXDEV. Copying a large worktree by hand would be slow and
// interruptible — the fallback trash lands under worktree_root, which
// necessarily shares the worktree's filesystem since it carries it.
func TestRemoveFallsBackWhenTrashIsOnAnotherFilesystem(t *testing.T) {
	_, worktreePath, target := prepareWorktree(t)
	elsewhere, err := os.MkdirTemp("/dev/shm", "den-trash-")
	if err != nil {
		t.Skipf("no second filesystem available: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(elsewhere) })
	if device(t, elsewhere) == device(t, worktreePath) {
		t.Skip("/dev/shm is on the same filesystem as the worktree")
	}
	target.DenHome = elsewhere
	writeFile(t, filepath.Join(worktreePath, "draft.txt"), "IRREPLACEABLE WORK\n")

	dest, err := Remove(context.Background(), NewGit(), withForce(target, true))
	if err != nil {
		t.Fatalf("a den_home on another filesystem must not prevent the trash: %v", err)
	}
	if filepath.Dir(dest) != filepath.Join(target.Root, ".trash") {
		t.Errorf("the fallback must live under %s; got %s", filepath.Join(target.Root, ".trash"), dest)
	}
	if got, err := os.ReadFile(filepath.Join(dest, "draft.txt")); err != nil ||
		string(got) != "IRREPLACEABLE WORK\n" {
		t.Errorf("the work must be recoverable; got %q, err %v", got, err)
	}
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Errorf("the worktree must have left %s; got: %v", worktreePath, err)
	}
}

func device(t *testing.T, path string) uint64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat of %s: %v", path, err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skipf("device number unavailable for %s", path)
	}
	return uint64(st.Dev)
}

// A trash without retention is a disk leak. The purge runs on the timestamp
// IN THE NAME — the one den wrote — and never on a name it cannot read: a
// directory the user stashed there must not go.
func TestRemovePurgesEntriesTooOld(t *testing.T) {
	_, _, target := prepareWorktree(t)
	trash := filepath.Join(target.DenHome, "trash")
	if err := os.MkdirAll(trash, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-trashRetention - 24*time.Hour).Format(timestampFormat)
	recent := time.Now().Add(-24 * time.Hour).Format(timestampFormat)

	toPurge := old + "-api.feat12-api"
	toKeep := []string{
		recent + "-api.feat12-api",
		// Stopped by the FORM guard: its 15th byte is not a "-".
		"stashed-by-the-user",
		// PASSES the form guard (byte 15 = "-") but its prefix is not a
		// date. This is the only witness that actually reaches parsing,
		// and hence the only one that holds its fail-closed behaviour:
		// without it, ignoring the ParseInLocation error leaves "when" at
		// the zero time, the gap exceeds retention, and the user's
		// stashed directory gets deleted.
		"hand-saved-copy-2026-07-28",
	}
	for _, name := range append([]string{toPurge}, toKeep...) {
		if err := os.MkdirAll(filepath.Join(trash, name), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(trash, name, "marker"), "x\n")
	}
	// A FILE with a perfectly timestamped name: not a trash entry, and the
	// purge must not touch it.
	witnessFile := old + "-file-stashed"
	writeFile(t, filepath.Join(trash, witnessFile), "user's note\n")
	toKeep = append(toKeep, witnessFile)

	// Setup: the parsing witness does pass the form guard, or it would
	// prove nothing more than "stashed-by-the-user".
	if n := "hand-saved-copy-2026-07-28"; n[len(timestampFormat)] != '-' {
		t.Fatalf("setup: %q must carry a \"-\" at position %d, it carries %q",
			n, len(timestampFormat), n[len(timestampFormat)])
	}

	if _, err := Remove(context.Background(), NewGit(), target); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	remaining := readEntries(t, trash)
	if slices.Contains(remaining, toPurge) {
		t.Errorf("the expired entry %q must be purged; remaining: %v", toPurge, remaining)
	}
	for _, keep := range toKeep {
		if !slices.Contains(remaining, keep) {
			t.Errorf("entry %q must NOT be purged; remaining: %v", keep, remaining)
		}
	}
}

// `git worktree lock` exists for removable volumes and network mounts. git
// refuses to touch it; den must refuse BEFORE moving, or it leaves a
// directory in the trash AND a registration that `prune` silently skips.
func TestRemoveRefusesALockedWorktreeStillPresent(t *testing.T) {
	repo, worktreePath, target := prepareWorktree(t)
	git(t, repo, "worktree", "lock", worktreePath)

	_, err := Remove(context.Background(), NewGit(), withForce(target, true))
	if err == nil {
		t.Fatal("a locked worktree must not be moved to the trash")
	}
	if !strings.Contains(err.Error(), "git worktree unlock") {
		t.Errorf("the message must cite git worktree unlock; got: %v", err)
	}
	if _, err := os.Stat(worktreePath); err != nil {
		t.Errorf("the locked worktree must be INTACT in place: %v", err)
	}
	if names := readEntries(t, filepath.Join(target.DenHome, "trash")); len(names) != 0 {
		t.Errorf("nothing must have been moved; trash: %v", names)
	}
}

// Without den_home there is no trash — and without a trash, den touches
// nothing. This is a caller mistake, not a user case: the refusal must name
// it rather than invent a location.
func TestRemoveRefusesWithoutDenHome(t *testing.T) {
	_, worktreePath, target := prepareWorktree(t)
	target.DenHome = ""
	// DIRTY on purpose: while this guard used to run after the dirtiness
	// check, the user got sent to a trash path built from an empty
	// den_home.
	writeFile(t, filepath.Join(worktreePath, "draft.txt"), "wip\n")

	_, err := Remove(context.Background(), NewGit(), target)
	if err == nil {
		t.Fatal("a Target without DenHome must be refused")
	}
	if !strings.Contains(err.Error(), "den_home") {
		t.Errorf("the refusal must name the real cause; got: %v", err)
	}
	if strings.Contains(err.Error(), "the trash") {
		t.Errorf("the message must not name a trash that does not exist; got: %v", err)
	}
	if _, err := os.Stat(worktreePath); err != nil {
		t.Errorf("the worktree must be INTACT: %v", err)
	}
}

// Two moves to trash for the same nest and repo within the same second
// carry the same computed name. Tested on the function rather than through
// two Remove calls: otherwise the collision would only occur if the clock
// cooperates.
func TestFreeEntryAvoidsCollisions(t *testing.T) {
	trash := t.TempDir()
	taken := filepath.Join(trash, "20260728-184530-api.feat12-api")
	if err := os.MkdirAll(taken, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(taken, "already-here"), "x\n")

	free, err := freeEntry(trash, "20260728-184530-api.feat12-api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if free == taken {
		t.Fatalf("freeEntry returned an already-occupied path: %s", free)
	}
	if _, err := os.Stat(free); !os.IsNotExist(err) {
		t.Errorf("%s must be free; got: %v", free, err)
	}
	// The original name must stay recognizable: a suffix, not a different
	// name.
	if !strings.HasPrefix(filepath.Base(free), "20260728-184530-api.feat12-api") {
		t.Errorf("the name must stay recognizable; got %q", filepath.Base(free))
	}
}

// ── Correctness: guards left unpinned ────────────────────────────────────

// startsWith says whether a git call's argv starts with this prefix.
func startsWith(args, prefix []string) bool {
	return len(args) >= len(prefix) && slices.Equal(args[:len(prefix)], prefix)
}

// gitFailingOn fails on an argument PREFIX, to check the direction of doubt:
// a mute probe must never authorize a destruction. It matches a PREFIX rather
// than a bare subcommand because the latter cannot tell `ls-files -v` from
// `ls-files -s` apart — the index probe then always fell first, and
// indexHashes' error stayed unreachable.
type gitFailingOn struct {
	real   Git
	prefix []string
}

func (g gitFailingOn) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return g.RunWithInput(ctx, dir, nil, args...)
}

func (g gitFailingOn) RunWithInput(ctx context.Context, dir string, input []byte, args ...string) ([]byte, error) {
	if startsWith(args, g.prefix) {
		return nil, errors.New("simulated failure of git " + strings.Join(g.prefix, " "))
	}
	return g.real.RunWithInput(ctx, dir, input, args...)
}

// scriptedGit answers a chosen output for one subcommand and delegates the
// rest. It exists ONLY to describe outputs real git produces but that no
// fixture triggers reliably — never to replace an observation.
type scriptedGit struct {
	real   Git
	prefix []string
	output []byte
}

func (g scriptedGit) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return g.RunWithInput(ctx, dir, nil, args...)
}

func (g scriptedGit) RunWithInput(ctx context.Context, dir string, input []byte, args ...string) ([]byte, error) {
	if startsWith(args, g.prefix) {
		return g.output, nil
	}
	return g.real.RunWithInput(ctx, dir, input, args...)
}

// Symmetric to ...RefusesAFileReplacedBySymlinkWithSameText: the index
// expects a LINK (120000), the disk carries a REGULAR file whose content is
// exactly the link's text. The hashes then coincide — git hashes the
// link's text as the blob content — and the file used to be destroyed
// without a word. modifiedLinks checked the mode; modifiedPaths ignored it.
func TestRemoveRefusesASymlinkReplacedByAFileWithSameText(t *testing.T) {
	_, worktreePath, target := prepareWorktree(t)
	writeFile(t, filepath.Join(worktreePath, "target.txt"), "CONTENT\n")
	if err := os.Symlink("target.txt", filepath.Join(worktreePath, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	git(t, worktreePath, "add", ".")
	git(t, worktreePath, "commit", "-m", "target and link")
	git(t, worktreePath, "update-index", "--skip-worktree", "link.txt")

	// The link becomes a regular file carrying EXACTLY the link's text.
	if err := os.Remove(filepath.Join(worktreePath, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktreePath, "link.txt"), []byte("target.txt"), 0o644); err != nil {
		t.Fatal(err)
	}
	// git reports nothing: that is the whole problem, and what stops a
	// third party from turning this test green instead of den.
	if status := git(t, worktreePath, "status", "--porcelain"); status != "" {
		t.Fatalf("setup: git should report nothing, got %q", status)
	}

	_, err := Remove(context.Background(), NewGit(), target)
	if err == nil {
		t.Fatal("a tracked symlink turned into a regular file is a modification, even with equal content")
	}
	if !strings.Contains(err.Error(), "link.txt") {
		t.Errorf("the message must name link.txt; got: %v", err)
	}
}

// A rename detected in the WORKING TREE (column Y): `mv` followed by
// `git add -N` produces it with real git. Consuming the source path only
// looked at column X, so "ab cd.txt" got read back as a record of its own —
// den then named "cd.txt", a file that does not exist.
func TestRemoveDoesNotInventAFileOnAWorkingTreeRename(t *testing.T) {
	_, worktreePath, target := prepareWorktree(t)
	writeFile(t, filepath.Join(worktreePath, "ab cd.txt"), "content long enough for rename detection\n")
	git(t, worktreePath, "add", "ab cd.txt")
	git(t, worktreePath, "commit", "-m", "add ab cd.txt")
	if err := os.Rename(filepath.Join(worktreePath, "ab cd.txt"), filepath.Join(worktreePath, "xy.txt")); err != nil {
		t.Fatal(err)
	}
	git(t, worktreePath, "add", "-N", "xy.txt")

	// Setup: it is column Y that git fills here, not X. rawGit and not
	// git: the latter trims, which would erase the very leading space that
	// distinguishes " R" from "R".
	if status := rawGit(t, worktreePath, "status", "--porcelain"); !strings.HasPrefix(status, " R ") {
		t.Skipf("git does not produce a column-Y rename here: %q", status)
	}

	_, err := Remove(context.Background(), NewGit(), target)
	if err == nil {
		t.Fatal("an uncommitted rename is a modification: refusal expected")
	}
	if !strings.Contains(err.Error(), "xy.txt") {
		t.Errorf("the message must name the renamed file; got: %v", err)
	}
	if strings.Contains(err.Error(), "cd.txt") {
		t.Errorf("the message invents a file from the source path; got: %v", err)
	}
}

// fakeHash has the shape of a hash without designating any object: what
// matters in these tests is den's DECISION, not what git would make of it.
var fakeHash = strings.Repeat("0", 40)

// The direction of doubt on the symlink path. These branches are
// fail-closed in the real code, but nothing turned red if they drifted.
func TestModifiedLinksIsFailClosed(t *testing.T) {
	_, worktreePath, _ := prepareWorktree(t)
	writeFile(t, filepath.Join(worktreePath, "not-a-link.txt"), "content\n")
	if err := os.Symlink("target.txt", filepath.Join(worktreePath, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// Readlink fails (EINVAL on a regular file): treating that as "nothing
	// modified" would let through a path den could not compare.
	t.Run("path unreadable as a symlink", func(t *testing.T) {
		index := map[string]indexEntry{"not-a-link.txt": {mode: symlinkMode, hash: fakeHash}}
		modified, err := modifiedLinks(worktreePath, index, []string{"not-a-link.txt"})
		if err == nil {
			t.Fatalf("an unreadable symlink must produce an error; got modified=%v", modified)
		}
		if !strings.Contains(err.Error(), "not-a-link.txt") {
			t.Errorf("the message must name the path; got: %v", err)
		}
	})

	// The index does not know this path: den has nothing to compare
	// against, so it counts it as dirty. The branch existed, but nothing
	// held it.
	t.Run("missing from index", func(t *testing.T) {
		modified, err := modifiedLinks(worktreePath, map[string]indexEntry{}, []string{"link.txt"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !slices.Contains(modified, "link.txt") {
			t.Errorf("a symlink missing from the index must count as modified; got %v", modified)
		}
	})

	// The index expects a regular file where the disk carries a symlink:
	// that is a modification, whatever the content.
	t.Run("unexpected index mode", func(t *testing.T) {
		index := map[string]indexEntry{"link.txt": {mode: "100644", hash: fakeHash}}
		modified, err := modifiedLinks(worktreePath, index, []string{"link.txt"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !slices.Contains(modified, "link.txt") {
			t.Errorf("an unexpected index mode must count as modified; got %v", modified)
		}
	})
}

// Same guards on the regular-file side.
func TestModifiedPathsIsFailClosed(t *testing.T) {
	_, worktreePath, _ := prepareWorktree(t)
	for _, f := range []string{"a.txt", "b.txt"} {
		writeFile(t, filepath.Join(worktreePath, f), "content of "+f+"\n")
	}

	// What this case pins is the BEHAVIOUR ("unknown ⇒ modified"), not a
	// branch: two conditions cover it, the empty mode and the empty hash.
	// The drift it catches is the one that matters — skipping a path the
	// index does not know instead of counting it as dirty.
	t.Run("missing from index", func(t *testing.T) {
		modified, err := modifiedPaths(context.Background(), NewGit(), worktreePath, map[string]indexEntry{}, []string{"a.txt"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !slices.Contains(modified, "a.txt") {
			t.Errorf("a path missing from the index must count as modified; got %v", modified)
		}
	})

	// Without the length guard, the argv-to-output alignment breaks and
	// den indexes out of the batch's bounds: a panic, not a refusal.
	t.Run("probe returns too few hashes", func(t *testing.T) {
		g := scriptedGit{real: NewGit(), prefix: []string{"hash-object"}, output: []byte(fakeHash + "\n")}
		index := map[string]indexEntry{
			"a.txt": {mode: "100644", hash: fakeHash},
			"b.txt": {mode: "100644", hash: fakeHash},
		}
		modified, err := modifiedPaths(context.Background(), g, worktreePath, index, []string{"a.txt", "b.txt"})
		if err == nil {
			t.Fatalf("a misaligned probe must produce an error; got modified=%v", modified)
		}
		for _, expected := range []string{"1", "2"} {
			if !strings.Contains(err.Error(), expected) {
				t.Errorf("the message must quantify the misalignment (%q); got: %v", expected, err)
			}
		}
	})
}

// `ls-files -s` and `ls-files -v` are two distinct probes that failingGit
// could not tell apart: the second always fell first, so indexHashes'
// error was unreachable by any test.
func TestRemoveRefusesWhenTheIndexIsSilent(t *testing.T) {
	_, worktreePath, target := prepareWorktree(t)
	writeFile(t, filepath.Join(worktreePath, "local.conf"), "v1\n")
	git(t, worktreePath, "add", "local.conf")
	git(t, worktreePath, "commit", "-m", "add local.conf")
	git(t, worktreePath, "update-index", "--skip-worktree", "local.conf")

	g := gitFailingOn{real: NewGit(), prefix: []string{"ls-files", "-s"}}
	_, err := Remove(context.Background(), g, target)
	if err == nil {
		t.Fatal("a failure of `git ls-files -s` must not authorize the move to trash")
	}
	if !strings.Contains(err.Error(), "simulated failure") {
		t.Errorf("the failure must surface unchanged; got: %v", err)
	}
	if _, err := os.Stat(worktreePath); err != nil {
		t.Errorf("the worktree must be INTACT: %v", err)
	}
}

// The parsing guards used to SKIP the unreadable record. Skipping loses a
// dirty entry silently — the exact fail-open direction the rest of this
// module refuses elsewhere.
func TestProbesRefuseUnreadableOutput(t *testing.T) {
	_, worktreePath, _ := prepareWorktree(t)
	statusPrefix := []string{"-c", "core.fsmonitor=", "status"}

	t.Run("truncated status", func(t *testing.T) {
		for _, output := range []string{"XY", "XYZfile.txt\x00", " M\x00"} {
			g := scriptedGit{real: NewGit(), prefix: statusPrefix, output: []byte(output)}
			dirty, err := dirtyFiles(context.Background(), g, worktreePath)
			if err == nil {
				t.Errorf("output %q: an unreadable record must produce an error; got %v", output, dirty)
			}
		}
	})

	t.Run("truncated ls-files", func(t *testing.T) {
		for _, output := range []string{"H", "Hpath.txt\x00"} {
			g := scriptedGit{real: NewGit(), prefix: []string{"ls-files", "-v"}, output: []byte(output)}
			marked, err := markedFiles(context.Background(), g, worktreePath)
			if err == nil {
				t.Errorf("output %q: an unreadable record must produce an error; got %v", output, marked)
			}
		}
	})

	// Indispensable counterpart: a CLEAN worktree's output is empty, and a
	// well-formed output ends with a NUL — hence an empty record. Refusing
	// on it would make `den rm` unusable.
	// This sub-case must ASSERT THE CONTENT, not just the absence of
	// error: if the scripted prefix stopped matching the real arguments,
	// real git would run on a clean worktree, return no error, and the
	// test would pass while proving nothing.
	t.Run("legitimate outputs", func(t *testing.T) {
		cases := []struct {
			output   string
			expected []string
		}{
			{"", nil},
			{"?? draft.txt\x00", []string{"draft.txt"}},
			{"R  new.txt\x00old.txt\x00", []string{"new.txt"}},
		}
		for _, c := range cases {
			g := scriptedGit{real: NewGit(), prefix: statusPrefix, output: []byte(c.output)}
			dirty, err := dirtyFiles(context.Background(), g, worktreePath)
			if err != nil {
				t.Errorf("output %q: unexpected error: %v", c.output, err)
				continue
			}
			if !slices.Equal(dirty, c.expected) {
				t.Errorf("output %q: dirty = %v, expected %v", c.output, dirty, c.expected)
			}
		}
	})
}

// ── Performance: one call per batch, not one fork per entry ────────────────

// blobHash replaces a `cat-file blob` fork with a marked symlink. The only
// judge of its correctness is git itself: compared against
// `git hash-object`, never against a value copied into the test.
func TestBlobHashMatchesGit(t *testing.T) {
	_, worktreePath, _ := prepareWorktree(t)
	texts := []string{
		"target.txt",
		"",
		"target.txt ",  // trailing space: a TrimSpace would eat it
		"a\nb",         // internal newline
		" \t edge \t ", // spaces on both sides
		"日本語/テスト.txt",  // non-ASCII
		strings.Repeat("x", 300),
	}
	for _, text := range texts {
		cmd := exec.Command("git", "hash-object", "-t", "blob", "--no-filters", "--stdin")
		cmd.Dir = worktreePath
		cmd.Stdin = strings.NewReader(text)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git hash-object %q: %v\n%s", text, err, out)
		}
		expected := strings.TrimSpace(string(out))

		got, err := blobHash(text, len(expected))
		if err != nil {
			t.Fatalf("blobHash(%q): %v", text, err)
		}
		if got != expected {
			t.Errorf("blobHash(%q) = %s, git says %s", text, got, expected)
		}
	}
}

// A repo in SHA-256 carries 64-character hashes. Betting on SHA-1 would
// report "modified" forever; an unknown length must therefore be an error,
// not a default choice.
func TestBlobHashRefusesAnUnknownFormat(t *testing.T) {
	for _, length := range []int{0, 32, 41, 128} {
		if _, err := blobHash("target.txt", length); err == nil {
			t.Errorf("a %d-character hash must be refused", length)
		}
	}
	// SHA-256 must pass, and give something other than SHA-1.
	sha1, err := blobHash("target.txt", 40)
	if err != nil {
		t.Fatal(err)
	}
	sha256, err := blobHash("target.txt", 64)
	if err != nil {
		t.Fatalf("a SHA-256 repo must be supported: %v", err)
	}
	if len(sha1) != 40 || len(sha256) != 64 || sha1 == sha256 {
		t.Errorf("inconsistent hashes: %q and %q", sha1, sha256)
	}
}

// Counterpart of the previous test: a MARKED symlink whose target changed
// must be refused. Without it, "always clean" would pass the
// intact-symlink fixtures without any assertion noticing.
func TestRemoveRefusesAMarkedSymlinkRepointed(t *testing.T) {
	_, worktreePath, target := prepareWorktree(t)
	writeFile(t, filepath.Join(worktreePath, "a.txt"), "A\n")
	writeFile(t, filepath.Join(worktreePath, "b.txt"), "B\n")
	if err := os.Symlink("a.txt", filepath.Join(worktreePath, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	git(t, worktreePath, "add", ".")
	git(t, worktreePath, "commit", "-m", "targets and link")
	git(t, worktreePath, "update-index", "--skip-worktree", "link.txt")

	if err := os.Remove(filepath.Join(worktreePath, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("b.txt", filepath.Join(worktreePath, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if status := git(t, worktreePath, "status", "--porcelain"); status != "" {
		t.Fatalf("setup: git should report nothing, got %q", status)
	}

	_, err := Remove(context.Background(), NewGit(), target)
	if err == nil {
		t.Fatal("a marked symlink repointed elsewhere is a modification: refusal expected")
	}
	if !strings.Contains(err.Error(), "link.txt") {
		t.Errorf("the message must name link.txt; got: %v", err)
	}
}

// The question "is this directory itself ignored?" is asked of git ONCE for
// the whole batch. One fork per entry cost 183 ms for 300 directories and
// 545 ms for 1000 — unbounded linear growth.
func TestRemoveAsksGitOnceAboutIgnoredDirs(t *testing.T) {
	_, worktreePath, target := prepareWorktree(t)
	var rules strings.Builder
	for _, d := range []string{"cache1", "cache2", "cache3"} {
		rules.WriteString(d + "/\n")
		if err := os.MkdirAll(filepath.Join(worktreePath, d), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(worktreePath, d, "x.bin"), "cache\n")
	}
	writeFile(t, filepath.Join(worktreePath, ".gitignore"), rules.String())
	git(t, worktreePath, "add", ".gitignore")
	git(t, worktreePath, "commit", "-m", "ignore caches")

	spy := &spyGit{real: NewGit()}
	if _, err := Remove(context.Background(), spy, target); err != nil {
		t.Fatalf("three directories ignored as a whole must not block removal: %v", err)
	}
	if n := spy.count("check-ignore"); n != 1 {
		t.Errorf("check-ignore must be called exactly once for the 3 directories; got %d; calls: %v",
			n, spy.calls)
	}
}

// ── Fix round 1: correct guards nothing was pinning ─────────────────────────

// I-1. `check-ignore` answers with a SUBSET of the batch, never a
// positional alignment. No test mixed both answers: two had only one
// non-ignored candidate (rc=1, empty table), the third only ignored
// candidates. "rc=0 ⇒ the whole batch is disposable" therefore passed the
// entire suite — and sent the secret to the trash WITHOUT refusal.
func TestRemoveDistinguishesTwoIgnoredDirsInTheSameBatch(t *testing.T) {
	_, worktreePath, target := prepareWorktree(t)
	writeFile(t, filepath.Join(worktreePath, ".gitignore"), "node_modules/\n*.env\n")
	git(t, worktreePath, "add", ".gitignore")
	git(t, worktreePath, "commit", "-m", "rules")
	for _, d := range []string{"node_modules", "conf"} {
		if err := os.MkdirAll(filepath.Join(worktreePath, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(worktreePath, "node_modules", "x.js"), "module.exports={}\n")
	secret := filepath.Join(worktreePath, "conf", "prod.env")
	writeFile(t, secret, "SECRET=hunter2\n")

	// Setup: BOTH come out collapsed as "!!", hence in the same batch.
	// Without this, the test would not mix anything and would fall back
	// on what the older tests already covered.
	status := rawGit(t, worktreePath, "status", "--porcelain", "--ignored=traditional")
	for _, expected := range []string{"!! conf/", "!! node_modules/"} {
		if !strings.Contains(status, expected) {
			t.Fatalf("setup: expected %q in status, got:\n%s", expected, status)
		}
	}

	_, err := Remove(context.Background(), NewGit(), target)
	if err == nil {
		t.Fatal("a secret in a non-ignored directory must be refused, even next to disposable cache")
	}
	if !strings.Contains(err.Error(), "conf/") {
		t.Errorf("the message must name conf/; got: %v", err)
	}
	// The other half of the discriminant: the disposable cache must NOT be
	// named, or "the whole batch is dirty" would pass just as well.
	if strings.Contains(err.Error(), "node_modules") {
		t.Errorf("node_modules/ is disposable cache and must not be named; got: %v", err)
	}
	if got, err := os.ReadFile(secret); err != nil || string(got) != "SECRET=hunter2\n" {
		t.Errorf("the secret must be INTACT; got %q, err %v", got, err)
	}
}

// I-3. worktreeEntry's godoc asserts that abstention is the only safe answer
// before a move. Nothing held it: under "error ⇒ not locked", Remove moves
// a worktree whose state it does not know and leaves a live registration
// that `prune` silently skips.
//
// Force is armed ON PURPOSE: it short-circuits dirtyFiles, so only the lock
// guard can still produce the refusal.
func TestRemoveRefusesWhenLockStatusIsUndecidable(t *testing.T) {
	_, worktreePath, target := prepareWorktree(t)

	g := gitFailingOn{real: NewGit(), prefix: []string{"worktree", "list"}}
	_, err := Remove(context.Background(), g, withForce(target, true))
	if err == nil {
		t.Fatal("an undecidable lock must not authorize the move to trash")
	}
	if !strings.Contains(err.Error(), "simulated failure") {
		t.Errorf("the failure must surface unchanged; got: %v", err)
	}
	if _, err := os.Stat(worktreePath); err != nil {
		t.Errorf("the worktree must be INTACT in place: %v", err)
	}
	if names := readEntries(t, filepath.Join(target.DenHome, "trash")); len(names) != 0 {
		t.Errorf("nothing must have been moved; trash: %v", names)
	}
}

// I-4. Symmetric to the previous one, on the "directory already gone"
// branch. The registration probe used to swallow the error: Remove then returned
// ("", nil) and claimed to have cleaned up without having been able to
// check anything — exactly what the neighbouring re-check exists to
// prevent.
func TestRemoveDoesNotClaimToHaveCleanedUpWithoutVerifying(t *testing.T) {
	_, worktreePath, target := prepareWorktree(t)
	if err := os.RemoveAll(worktreePath); err != nil {
		t.Fatal(err)
	}

	g := gitFailingOn{real: NewGit(), prefix: []string{"worktree", "list"}}
	_, err := Remove(context.Background(), g, target)
	if err == nil {
		t.Fatal("Remove must not return nil when it could not verify the registration")
	}
	if !strings.Contains(err.Error(), "simulated failure") {
		t.Errorf("the failure must surface unchanged; got: %v", err)
	}

	// Same discipline on Ensure's side, which reads the same listing to
	// decide.
	if _, err := Ensure(context.Background(), g, target.Layout, target.Root, wtName(target.Worktree), target.RepoPath); err == nil {
		t.Error("Ensure must not proceed either without being able to read the listing")
	}
}

// M-1. The nest name and the repo's basename compose the entry name.
// safeComponent's comment asserts that ".." cannot designate the trash's
// parent; nothing verified it.
func TestRemoveDoesNotEscapeTheTrash(t *testing.T) {
	_, _, target := prepareWorktree(t)
	target.Nest = "a/../../../escape"

	dest, err := Remove(context.Background(), NewGit(), target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	trash := filepath.Join(target.DenHome, "trash")
	if filepath.Dir(dest) != trash {
		t.Errorf("the entry must stay INSIDE %s; got it at %s", trash, dest)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("the entry must exist where Remove announces it: %v", err)
	}
}

// M-2. freeEntry's two guards, whose comment "Lstat and not Stat" was
// verified by nothing.
func TestFreeEntry(t *testing.T) {
	// A DANGLING symlink occupies the name just as much: os.Stat would say
	// "does not exist", and the following rename would fail with ENOTDIR.
	t.Run("a dangling symlink occupies the name", func(t *testing.T) {
		dir := t.TempDir()
		taken := filepath.Join(dir, "base")
		if err := os.Symlink("/does/not/exist", taken); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		free, err := freeEntry(dir, "base")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if free == taken {
			t.Errorf("freeEntry returned a name occupied by a broken symlink: %s", free)
		}
	})

	// Not being able to inspect is not "it's free".
	t.Run("inspection not possible", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root traverses directories regardless of permissions")
		}
		dir := filepath.Join(t.TempDir(), "locked")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

		if free, err := freeEntry(dir, "base"); err == nil {
			t.Errorf("an impossible inspection must produce an error; got %q", free)
		}
	})
}

// M-4. The fallback is reserved for EXDEV. The positive direction was
// tested, not the negative: falling back on the slightest error would turn
// a failure into a silent move to a location the user does not expect.
func TestRemoveOnlyFallsBackOnAnotherFilesystem(t *testing.T) {
	_, worktreePath, target := prepareWorktree(t)
	// The primary trash is a FILE: MkdirAll fails with ENOTDIR, not EXDEV,
	// and on the SAME filesystem as the fallback.
	if err := os.MkdirAll(target.DenHome, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(target.DenHome, "trash"), "not a directory\n")

	_, err := Remove(context.Background(), NewGit(), withForce(target, true))
	if err == nil {
		t.Fatal("an unusable trash must produce an error")
	}
	if _, err := os.Stat(worktreePath); err != nil {
		t.Errorf("the worktree must be INTACT: %v", err)
	}
	if names := readEntries(t, filepath.Join(target.Root, ".trash")); len(names) != 0 {
		t.Errorf("the fallback must not be attempted outside EXDEV; got: %v", names)
	}
}

// M-6. blobHash deduces the algorithm from the length of the index's hash.
// The function was tested, not the end to end: nothing proved that a repo
// really in SHA-256 goes through Remove.
func TestRemoveAcceptsAnIntactSymlinkInASha256Repo(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "api")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := tolerantGit(t, repo, "init", "-q", "-b", "main", "--object-format=sha256", "."); err != nil {
		t.Skipf("git cannot create a SHA-256 repo here: %v", err)
	}
	git(t, repo, "config", "user.email", "test@example.test")
	git(t, repo, "config", "user.name", "Test")
	git(t, repo, "commit", "--allow-empty", "-m", "initial")

	target := Target{
		DenHome: t.TempDir(), Layout: "central", Root: t.TempDir(),
		Nest: "api.feat12", Worktree: "feat12", RepoPath: repo,
	}
	worktreePath, err := Ensure(context.Background(), NewGit(), target.Layout, target.Root, wtName(target.Worktree), target.RepoPath)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	writeFile(t, filepath.Join(worktreePath, "target.txt"), "CONTENT\n")
	if err := os.Symlink("target.txt", filepath.Join(worktreePath, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	git(t, worktreePath, "add", ".")
	git(t, worktreePath, "commit", "-m", "target and link")
	git(t, worktreePath, "update-index", "--skip-worktree", "link.txt")

	// Setup: this really is a SHA-256 repo, hence 64-character hashes.
	// Without this check, the test would pass on a SHA-1 repo without
	// exercising anything new.
	index, err := indexHashes(context.Background(), NewGit(), worktreePath)
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := index["link.txt"]; !ok || len(e.hash) != 64 {
		t.Fatalf("setup: expected a 64-character hash, got %+v", index["link.txt"])
	}

	if _, err := Remove(context.Background(), NewGit(), target); err != nil {
		t.Fatalf("an intact symlink in a SHA-256 repo must not block removal: %v", err)
	}
}

// M-10. markedFiles' comment asserts that a marked gitlink is refused even
// though `git status` reports nothing. Verified by hand until now, held by
// nothing.
func TestRemoveRefusesAMarkedSubmodule(t *testing.T) {
	base := t.TempDir()
	sub := filepath.Join(base, "sub")
	createRepo(t, sub)
	writeFile(t, filepath.Join(sub, "f.txt"), "submodule content\n")
	git(t, sub, "add", "f.txt")
	git(t, sub, "commit", "-m", "content")

	repo := filepath.Join(base, "api")
	createRepo(t, repo)
	// protocol.file.allow: git refuses submodules over file:// by default
	// (CVE-2022-39253). The repo stays local, no network access.
	if _, err := tolerantGit(t, repo, "-c", "protocol.file.allow=always",
		"submodule", "add", "-q", sub, "sm"); err != nil {
		t.Skipf("submodules unavailable here: %v", err)
	}
	git(t, repo, "commit", "-m", "add the submodule")

	target := Target{
		DenHome: t.TempDir(), Layout: "central", Root: t.TempDir(),
		Nest: "api.feat12", Worktree: "feat12", RepoPath: repo,
	}
	worktreePath, err := Ensure(context.Background(), NewGit(), target.Layout, target.Root, wtName(target.Worktree), target.RepoPath)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	git(t, worktreePath, "-c", "protocol.file.allow=always", "submodule", "update", "--init", "-q")
	git(t, worktreePath, "update-index", "--skip-worktree", "sm")

	// git reports nothing: that is what makes den's refusal observable.
	if status := git(t, worktreePath, "status", "--porcelain"); status != "" {
		t.Fatalf("setup: git should report nothing, got %q", status)
	}

	_, err = Remove(context.Background(), NewGit(), target)
	if err == nil {
		t.Fatal("a marked gitlink must be refused: den does not know what it carries")
	}
	if !strings.Contains(err.Error(), "sm") {
		t.Errorf("the message must name sm; got: %v", err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// tolerantGit returns the error instead of aborting: for commands whose
// failure is itself information (no origin/HEAD, no upstream).
func tolerantGit(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// rawGit does NOT trim the output: column X of `git status --porcelain` is
// a space for anything that only happened in the working tree, and
// trimming it makes " R" indistinguishable from "R ".
func rawGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func branchOf(t *testing.T, dir string) string {
	t.Helper()
	return git(t, dir, "rev-parse", "--abbrev-ref", "HEAD")
}

func switchTo(t *testing.T, dir, branch string) {
	t.Helper()
	git(t, dir, "checkout", "-b", branch)
}

// A repo WITH NO COMMIT AT ALL must produce den's own diagnostic, not
// git's contradictory message. Found by exercising the assembled binary:
// `den api -w feat1` against a virgin `git init` used to produce
//
//	den: creating worktree "feat1" of /.../monorepo: git worktree add
//	     --no-track -b feat1 /.../worktrees/feat1/monorepo (in /.../monorepo):
//	     No possible source branch, inferring '--orphan'
//	     fatal: options '--orphan' and '--track' cannot be used together
//
// — accurate from git's point of view, unactionable from den's: it never
// says the repo has no commit, which is the one thing to fix.
//
// TWO configurations, because the first version of this test only covered
// a virgin `git init`, and the message it validated — "the repo has no
// commit" — is FALSE on the second: an orphan branch still has a commit
// elsewhere, only HEAD fails to resolve. A confidently wrong diagnostic
// sending the user looking for an empty repo they do not have is exactly
// the class of defect this fix removes.
func TestEnsureRefusesAnUnresolvableHeadTruthfully(t *testing.T) {
	cases := []struct {
		name string
		// prepare returns the path of a repo whose HEAD does not resolve.
		prepare func(t *testing.T) string
	}{
		{
			name: "empty repo",
			prepare: func(t *testing.T) string {
				repo := filepath.Join(t.TempDir(), "empty")
				if err := os.MkdirAll(repo, 0o755); err != nil {
					t.Fatal(err)
				}
				git(t, repo, "init", "-q", "-b", "main")
				return repo
			},
		},
		{
			name: "orphan branch in a repo that has commits",
			prepare: func(t *testing.T) string {
				repo := testRepo(t, "orphan")
				git(t, repo, "checkout", "-q", "--orphan", "new")
				// Verified, not assumed: without this the sub-case could
				// drift toward a genuinely empty repo and prove nothing
				// new.
				if n := strings.TrimSpace(git(t, repo, "rev-list", "--all", "--count")); n == "0" {
					t.Fatalf("this case requires a repo WITH commits; rev-list counts %s", n)
				}
				return repo
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := c.prepare(t)
			root := t.TempDir()

			_, err := Ensure(context.Background(), NewGit(), "central", root, wtName("feat1"), repo)
			if err == nil {
				t.Fatal("an unresolvable HEAD cannot carry a worktree: refusal expected")
			}
			msg := err.Error()
			// THE cause, named exactly: it is HEAD that does not resolve.
			if !strings.Contains(msg, "HEAD") {
				t.Errorf("the message must name HEAD, the one thing true of both cases; got: %s", msg)
			}
			// The offending repo: a nest may mount several.
			if !strings.Contains(msg, repo) {
				t.Errorf("the message must name the offending repo; got: %s", msg)
			}
			// The hedge that must survive: the message must not claim
			// unconditionally that the repo is empty. It must mention the
			// orphan-branch case too, since that is what makes it true on
			// both cases.
			if !strings.Contains(msg, "orphan branch") {
				t.Errorf("the message must hedge with the orphan-branch case, not assert the repo is empty; got: %s", msg)
			}
			// And especially NOT git's own jargon, which sends the user on
			// a false trail.
			for _, forbidden := range []string{"--orphan", "--track"} {
				if strings.Contains(msg, forbidden) {
					t.Errorf("the message must not expose %q: the user never typed it, "+
						"and it is not what they should fix; got: %s", forbidden, msg)
				}
			}
		})
	}
}

// The counterpart, and it matters: a repo WITH a commit must still produce
// a worktree. Without it, a too-broad guard — refusing every repo — would
// pass the previous test while breaking normal use.
func TestEnsureStillCreatesTheWorktreeForARepoWithACommit(t *testing.T) {
	repo := testRepo(t, "with-commit")
	root := t.TempDir()

	worktreePath, err := Ensure(context.Background(), NewGit(), "central", root, wtName("feat1"), repo)
	if err != nil {
		t.Fatalf("a repo with a commit must accept a worktree: %v", err)
	}
	if _, err := os.Stat(worktreePath); err != nil {
		t.Errorf("the worktree announced at %s does not exist: %v", worktreePath, err)
	}
}

// CommonGitDir is what `den <nest> -w` mounts next to the worktree: the
// repo's `.git`, without its working tree. The nominal case is an ordinary
// repo.
func TestCommonGitDirReturnsTheRepoGit(t *testing.T) {
	repo := testRepo(t, "api")

	common, err := CommonGitDir(context.Background(), NewGit(), repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if expected := filepath.Join(repo, ".git"); !samePath(common, expected) {
		t.Errorf("common = %q, expected %q", common, expected)
	}
}

// The case that forbids a plain `filepath.Join(repo, ".git")`: when the
// nest's repo is ITSELF a linked worktree, its `.git` is a FILE pointing at
// `<primary>/.git/worktrees/<name>`. Mounting it would give neither the
// objects nor the refs — exactly the failure round-1 fixed, just moved one
// level. Only git can climb back to the common directory, hence the call
// to rev-parse.
func TestCommonGitDirClimbsFromALinkedWorktree(t *testing.T) {
	primary := testRepo(t, "api")
	linked := filepath.Join(t.TempDir(), "api-feat")
	git(t, primary, "worktree", "add", "-b", "feat", linked)

	common, err := CommonGitDir(context.Background(), NewGit(), linked)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if expected := filepath.Join(primary, ".git"); !samePath(common, expected) {
		t.Errorf("common = %q, expected %q — the linked worktree's common git "+
			"directory is the primary repo's, not its own .git file", common, expected)
	}
}

// A directory that is not a repo must exit with an error naming the path:
// den composes that result into a `sbx create` argument, where an empty or
// wrong path would become a silent mount.
func TestCommonGitDirRefusesANonRepo(t *testing.T) {
	dir := t.TempDir()

	_, err := CommonGitDir(context.Background(), NewGit(), dir)
	if err == nil {
		t.Fatal("a non-versioned directory must exit with an error, got nil")
	}
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("error = %q, expected the offending directory's path", err.Error())
	}
}

// wtName is the case where the two names coincide: a worktree whose name is
// ALREADY a valid sandbox-name component. A test helper, not an exported
// constructor, on purpose — in production the directory name always comes
// from config.FlattenSandboxComponent, and offering a shortcut that assumes
// "branch = directory" would invite flattening the branch.
func wtName(s string) Name { return Name{Dir: s, Branch: s} }
