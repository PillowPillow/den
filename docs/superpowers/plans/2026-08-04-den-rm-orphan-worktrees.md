# `den rm` recovers orphan worktrees Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `den rm` no longer leaves behind the worktree of a repo it was never told about — a repo passed on the command line, or one deleted from a nest's `repos:`.

**Architecture:** One new function, `worktree.Orphans`, is the inverse of `worktree.Path`: it enumerates `<worktree_root>/<wt>/*` and recovers each directory's repository from the directory's own `.git`. `cleanWorktrees` builds ONE work list — declared repos, then recovered ones — and feeds it to the EXISTING `worktree.Remove`, so `--force`, the uncommitted-changes refusal, the lock check and the trash all stay in one place. Recovery is only possible in the central layout; per-repo gets a conditionally-phrased warning instead.

**Tech Stack:** Go, cobra, real `git` driven through the `worktree.Git` interface. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-08-04-den-rm-orphan-worktrees-design.md`. Read it before Task 1 — every "why" below summarizes a decision recorded there. **Issue:** [#46](https://github.com/PillowPillow/den/issues/46).

## Global Constraints

- **Build/test/lint:** `make test` (`go test -count=1 ./...`), `make typecheck` (`go build ./...`), `make lint` (`go vet ./...` + `gofmt -l .` must be empty). `gofmt` is enforced, not advisory — run `gofmt -w` on every file touched.
- **Language:** code, comments and user-facing messages are **English**. The spec and this plan are French/English prose; do not translate code comments.
- **Comment style:** the dominant style in this repo is a long "why" comment at the decision site, naming what was rejected and what regression the choice prevents. Terse code visibly does not match. Every comment written below is part of the deliverable — copy it.
- **Errors name the file to fix and the remedy.** den refuses rather than normalizing in silence (spec §2).
- **Test hermeticity:** no test calls `t.Parallel()`, opens a socket, or spawns a process — except real `git` through `worktree.NewGit()`, which `internal/worktree`, `internal/cli` and `internal/spawn` already do; their `TestMain` calls `worktree.NeutralizeGitEnvironment()`.
- **`internal/cli` must not import `net`, `hash/fnv` or `os/exec`** (locked by `internal/ports/hermeticity_test.go`). This plan adds NO import to `internal/cli` at all — that is deliberate, and it is why enumeration lives in `internal/worktree`.
- **Branch:** create `fix/rm-orphan-worktrees` from the current HEAD of `feat/adhoc-repos` (Task 1, step 0). Its PR is based on `feat/adhoc-repos` (PR #47), which stays at its current size. Commit after every task.
- **Doctrine T13/T16, already written at `internal/cli/rm.go:75-86`:** best-effort on **resolution** (a failure to recover must never prevent destroying a live sandbox), strict on **removal** (a dirty or locked worktree stops everything).

---

### Task 1: `worktree.Orphans` — enumerate and recover

**Files:**
- Modify: `internal/worktree/worktree.go` (add after `Path`, around line 136)
- Test: `internal/worktree/worktree_test.go` (add after `TestPath`, around line 44)

**Interfaces:**
- Consumes: `Path`, `identify`, `repoDir`, `samePath`, `worktreeEntry`, `Git` — all already in `worktree.go`.
- Produces:
  - `type Orphan struct { Dir string; RepoPath string }`
  - `func Orphans(ctx context.Context, g Git, root, wt string, known []string) ([]Orphan, []error, error)`

  Task 3 consumes both. The middle return is one error per entry deliberately SKIPPED (the caller warns and continues); the last is a hard enumeration failure.

- [ ] **Step 0: Branch**

```bash
git checkout -b fix/rm-orphan-worktrees
```

- [ ] **Step 1: Write the failing tests**

Add to `internal/worktree/worktree_test.go`:

```go
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/worktree/ -run TestOrphans -count=1`
Expected: FAIL — `undefined: Orphans`.

- [ ] **Step 3: Implement `Orphans`**

Add to `internal/worktree/worktree.go`, right after `Path`:

```go
// Orphan is a worktree directory den created that the caller's list of repos
// does not account for — typically the worktree of a repo passed on the command
// line, deliberately absent from the sandbox identity (spec §4.3), or one whose
// entry was deleted from the nest's `repos:` since the spawn.
type Orphan struct {
	Dir      string // <root>/<wt>/<name>
	RepoPath string // the repository, recovered from Dir's own .git
}

// Orphans enumerates the worktree directories of wt under root that `known`
// does not account for, recovering each one's repository from its own `.git`.
// It is the INVERSE of Path, and the reason den can clean up what it never
// stored: den keeps no state beyond the sandbox name, but the directory itself
// remembers which repository it belongs to.
//
// CENTRAL LAYOUT ONLY. In per-repo, Path puts the worktree at <repo>/.den/<wt>:
// without the repo path there is no directory to list, so there is nothing to
// enumerate — the caller warns instead.
//
// Three returns, because three outcomes are genuinely different:
//   - the entries den may remove;
//   - one error per entry deliberately SKIPPED. Not a failure: an entry den
//     cannot vouch for is left alone, and the caller warns. Removing on a
//     guess would be worse than the leftover this exists to clear;
//   - a hard error, only when the enumeration ITSELF fails. An absent
//     <root>/<wt> is the nominal "no worktree" case, not a failure.
func Orphans(ctx context.Context, g Git, root, wt string, known []string) ([]Orphan, []error, error) {
	// Path("central", root, "", repo) is <root>/<repo>, so an empty wt would
	// enumerate worktree_root ITSELF and offer the worktrees of every other
	// nest for removal. Same refusal as removeParentDir's, for the same reason.
	if wt == "" {
		return nil, nil, nil
	}

	parent := filepath.Join(root, wt)
	entries, err := os.ReadDir(parent)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil // no worktree directory: nothing to recover
		}
		return nil, nil, fmt.Errorf("listing %s: %w", parent, err)
	}

	var found []Orphan
	var skipped []error
	// os.ReadDir returns its entries SORTED, which is what keeps the caller's
	// output — and its goldens — stable from one run to the next.
	for _, e := range entries {
		dir := filepath.Join(parent, e.Name())
		if accountedFor(root, wt, dir, known) {
			continue // the caller already handles this one: silent, not skipped
		}
		orphan, reason := recoverOrphan(ctx, g, root, wt, dir, e.IsDir())
		if reason != nil {
			skipped = append(skipped, reason)
			continue
		}
		found = append(found, orphan)
	}
	return found, skipped, nil
}

// accountedFor asks the question through Path — the function that PLACED the
// directory — rather than by comparing basenames: the comparison is then exact
// by construction, and it keeps following Path if the layout ever changes.
func accountedFor(root, wt, dir string, known []string) bool {
	for _, repo := range known {
		if samePath(Path("central", root, wt, repo), dir) {
			return true
		}
	}
	return false
}

// recoverOrphan decides whether dir is a worktree den may remove, and of which
// repository. It returns a REASON, never an error to abort on.
//
// Deriving the repository FROM the directory makes checkOwnership a tautology:
// we would ask git whose the directory is, then assert that it is theirs. The
// guard that makes "den only removes directories it placed itself" true has to
// be rebuilt here, out of the checks below — each one closes a case where the
// tautology would have said yes.
func recoverOrphan(ctx context.Context, g Git, root, wt, dir string, isDir bool) (Orphan, error) {
	if !isDir {
		return Orphan{}, fmt.Errorf("%s is not a directory: left in place", dir)
	}

	wtRoot, common, err := identify(ctx, g, dir)
	if err != nil {
		return Orphan{}, fmt.Errorf(
			"%s is not a git worktree, or its repository is gone: left in place (%v)", dir, err)
	}

	// git answers for the first repository found walking UP: without this, a
	// plain directory the user parked under worktree_root would pass for a
	// worktree of whatever repository happens to contain worktree_root.
	if !samePath(wtRoot, dir) {
		return Orphan{}, fmt.Errorf(
			"%s is not the root of a git worktree — git answers for %s: left in place", dir, wtRoot)
	}

	repoPath := repoDir(common)

	// dir IS the main worktree of its own repository: someone cloned a repo
	// under worktree_root. Sending it to the trash would be far worse than the
	// leftover this enumeration exists to clear.
	if samePath(repoPath, dir) {
		return Orphan{}, fmt.Errorf(
			"%s is a repository itself, not a worktree den created: left in place", dir)
	}

	// Remove RECOMPUTES the path from RepoPath (see Target) — deliberately, so
	// that no caller can send it anywhere. When the repo is ITSELF a linked
	// worktree, repoDir walks up to the MAIN worktree, whose basename may
	// differ from the directory we just enumerated. Remove would then stat a
	// path that does not exist, take the "already gone" branch, and report
	// success while the directory stays on disk. Say so instead of no-oping.
	if !samePath(Path("central", root, wt, repoPath), dir) {
		return Orphan{}, fmt.Errorf(
			"%s belongs to %s, whose directory name differs — den would clean up elsewhere: "+
				"remove %s by hand", dir, repoPath, dir)
	}

	// The strongest available proof that this path really is a worktree OF that
	// repository, and not a directory that merely looks like one.
	registered, _, err := worktreeEntry(ctx, g, repoPath, dir)
	if err != nil {
		return Orphan{}, fmt.Errorf("%s left in place: %v", dir, err)
	}
	if !registered {
		return Orphan{}, fmt.Errorf(
			"%s is not a registered worktree of %s: left in place", dir, repoPath)
	}

	return Orphan{Dir: dir, RepoPath: repoPath}, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/worktree/ -run TestOrphans -count=1`
Expected: PASS (4 tests).

- [ ] **Step 5: Lint and commit**

```bash
gofmt -w internal/worktree/worktree.go internal/worktree/worktree_test.go
make lint && make test
git add internal/worktree/worktree.go internal/worktree/worktree_test.go
git commit -m "feat(worktree): Orphans recovers a worktree from its own .git"
```

---

### Task 2: the guards that replace `checkOwnership`

No production code changes: Task 1 wrote the guards, this task proves each one. Two of these cases are the ones a passing suite would otherwise miss — one eats a directory, the other lies.

**Files:**
- Test: `internal/worktree/worktree_test.go` (after the Task 1 tests)

**Interfaces:**
- Consumes: `Orphans`, `Orphan`, `Ensure`, `Path`, `samePath`, and the test helpers `testRepo`, `createRepo`, `git`, `wtName` — all already present.
- Produces: nothing consumed later.

- [ ] **Step 1: Write the failing tests**

```go
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
```

- [ ] **Step 2: Run the tests**

Run: `go test ./internal/worktree/ -run TestOrphans -count=1 -v`
Expected: PASS (10 tests). If one FAILS, the corresponding guard in `recoverOrphan` is wrong — fix `worktree.go`, not the test.

- [ ] **Step 3: Commit**

```bash
gofmt -w internal/worktree/worktree_test.go
make lint && make test
git add internal/worktree/worktree_test.go
git commit -m "test(worktree): the six guards that replace checkOwnership for a recovered repo"
```

---

### Task 3: `cleanWorktrees` builds one work list

**Files:**
- Modify: `internal/cli/rm.go:75-153` (the whole `cleanWorktrees` function and its godoc)
- Test: `internal/cli/rm_test.go`

**Interfaces:**
- Consumes: `worktree.Orphans`, `worktree.Orphan` (Task 1), `worktree.Target`, `worktree.Remove`, `gitProbeTimeout` (`rm.go:23`).
- Produces: no new exported symbol. Task 4 and Task 5 edit the same function.

- [ ] **Step 1: Write the failing tests**

Add to `internal/cli/rm_test.go`:

```go
// Issue #46. `den api -w feat ~/dev/hotfix` gives hotfix a worktree, but a
// positional is deliberately NOT part of the sandbox identity, so `den rm`
// cannot find it in the nest. It is recovered from the directory instead.
//
// This is ALSO the ORDERING test, and the only one: it holds one declared repo
// AND one orphan, so an implementation that removed before enumerating would
// see removeParentDir empty <root>/<wt> and lose the orphan. Do not "simplify"
// it down to a single repo — that silently drops the coverage.
func TestRmCleansUpAWorktreeNoRepoDeclares(t *testing.T) {
	denHome := t.TempDir()
	root := filepath.Join(denHome, "worktrees")
	writeConfig(t, denHome, minimalConfig+"worktree_layout: central\nworktree_root: "+root+"\n")
	writeStack(t, denHome, "devx", "image: devx:v1\n")

	declared := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, declared)
	writeNest(t, denHome, "api", "stack: devx\nrepos:\n  - { path: "+declared+" }\n")

	// The ad-hoc repo: mounted on the command line at spawn time, declared
	// nowhere.
	adhoc := filepath.Join(t.TempDir(), "hotfix")
	createTestRepo(t, adhoc)

	var paths []string
	for _, repo := range []string{declared, adhoc} {
		p, err := worktree.Ensure(context.Background(), worktree.NewGit(),
			"central", root, worktree.Name{Dir: "feat12", Branch: "feat12"}, repo)
		if err != nil {
			t.Fatalf("preparing the worktree of %s: %v", repo, err)
		}
		paths = append(paths, p)
	}

	f := &sbx.Fake{Responses: lsWith("api.feat12")}
	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, p := range paths {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s must have been moved to the trash; stat: %v", p, err)
		}
	}
	// executeCmdWithSbx MERGES stdout and stderr, so this count sees warnings
	// too: no later task may introduce a warning containing "moved to trash"
	// (Task 5's per-repo message deliberately does not).
	if strings.Count(out, "moved to trash") != 2 {
		t.Errorf("both worktrees must be announced; got:\n%s", out)
	}
	// The registration must be gone too, otherwise `git branch -d feat12` in
	// the user's own repository still refuses with "already checked out".
	if reg := gitOutput(t, adhoc, "worktree", "list", "--porcelain"); strings.Contains(reg, "feat12") {
		t.Errorf("the registration survives in %s:\n%s", adhoc, reg)
	}
}

// An orphan is not a licence to delete work: the uncommitted-changes refusal
// applies to a RECOVERED entry exactly as to a declared one — which is why
// orphans go through the same worktree.Remove and not a second removal path.
func TestRmRefusesADirtyOrphanWorktree(t *testing.T) {
	denHome := t.TempDir()
	root := filepath.Join(denHome, "worktrees")
	writeConfig(t, denHome, minimalConfig+"worktree_layout: central\nworktree_root: "+root+"\n")
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	writeNest(t, denHome, "api", "stack: devx\nrepos: []\n")

	adhoc := filepath.Join(t.TempDir(), "hotfix")
	createTestRepo(t, adhoc)
	p, err := worktree.Ensure(context.Background(), worktree.NewGit(),
		"central", root, worktree.Name{Dir: "feat12", Branch: "feat12"}, adhoc)
	if err != nil {
		t.Fatalf("preparing the worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(p, "wip.txt"), []byte("work in progress"), 0o644); err != nil {
		t.Fatal(err)
	}

	f := &sbx.Fake{Responses: lsWith("api.feat12")}
	_, err = executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err == nil {
		t.Fatal("a dirty recovered worktree must stop den rm, like a declared one")
	}
	if !strings.Contains(err.Error(), "wip.txt") {
		t.Errorf("error = %q, expected the uncommitted file to be named", err.Error())
	}
	if _, statErr := os.Stat(p); statErr != nil {
		t.Errorf("the worktree must still be there: %v", statErr)
	}
	if f.HasCalled("rm", "--force", "api.feat12") {
		t.Errorf("the sandbox must NOT be destroyed while work is at stake; calls: %v", f.Calls)
	}
}

// ...and --force applies to a recovered entry too: the same flag, on the same
// code path.
func TestRmForceRemovesADirtyOrphanWorktree(t *testing.T) {
	denHome := t.TempDir()
	root := filepath.Join(denHome, "worktrees")
	writeConfig(t, denHome, minimalConfig+"worktree_layout: central\nworktree_root: "+root+"\n")
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	writeNest(t, denHome, "api", "stack: devx\nrepos: []\n")

	adhoc := filepath.Join(t.TempDir(), "hotfix")
	createTestRepo(t, adhoc)
	p, err := worktree.Ensure(context.Background(), worktree.NewGit(),
		"central", root, worktree.Name{Dir: "feat12", Branch: "feat12"}, adhoc)
	if err != nil {
		t.Fatalf("preparing the worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(p, "wip.txt"), []byte("work in progress"), 0o644); err != nil {
		t.Fatal(err)
	}

	f := &sbx.Fake{Responses: lsWith("api.feat12")}
	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12", "--force")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(p); !os.IsNotExist(statErr) {
		t.Errorf("--force must move the recovered worktree; stat: %v", statErr)
	}
	// den never deletes: the user must be told where their work went.
	if !strings.Contains(out, "moved to trash") {
		t.Errorf("the trash path must be announced; got:\n%s", out)
	}
}

// Best-effort on RESOLUTION (doctrine T13/T16): an orphan whose repository has
// been deleted since the spawn cannot be recovered. den says so and carries on
// — refusing here would leave the user with a live VM they can no longer
// destroy, over a directory.
func TestRmWarnsAboutAnUnrecoverableOrphanAndStillDestroys(t *testing.T) {
	denHome := t.TempDir()
	root := filepath.Join(denHome, "worktrees")
	writeConfig(t, denHome, minimalConfig+"worktree_layout: central\nworktree_root: "+root+"\n")
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	writeNest(t, denHome, "api", "stack: devx\nrepos: []\n")

	// A directory that is not a git worktree at all: recovery is impossible.
	stray := filepath.Join(root, "feat12", "hotfix")
	if err := os.MkdirAll(stray, 0o755); err != nil {
		t.Fatal(err)
	}

	f := &sbx.Fake{Responses: lsWith("api.feat12")}
	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("an unrecoverable orphan must not fail den rm: %v", err)
	}
	if !strings.Contains(out, stray) {
		t.Errorf("the warning must name the directory left behind; got:\n%s", out)
	}
	if _, statErr := os.Stat(stray); statErr != nil {
		t.Errorf("den must not have touched %s: %v", stray, statErr)
	}
	if !f.HasCalled("rm", "--force", "api.feat12") {
		t.Errorf("the sandbox must still be destroyed; calls: %v", f.Calls)
	}
}
```

Add this helper next to `createTestRepo` in `rm_test.go` (the file has no output-returning git helper yet):

```go
// gitOutput runs git in dir and returns its combined output, for assertions on
// what the repository still knows.
func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/cli/ -run 'TestRmCleansUpAWorktreeNoRepoDeclares|TestRmRefusesADirtyOrphan|TestRmForceRemovesADirtyOrphan|TestRmWarnsAboutAnUnrecoverableOrphan' -count=1`
Expected: FAIL — the orphan worktree is still on disk, nothing is announced, no warning.

- [ ] **Step 3: Rewrite `cleanWorktrees`**

Replace `internal/cli/rm.go:75-153` with:

```go
// cleanWorktrees removes, through worktree.Remove, the worktrees den created
// for this sandbox. Best-effort on RESOLUTION (a nest deleted from
// ~/.den/nests, an orphan whose repo is gone, must not prevent destroying a
// live sandbox); strict on REMOVAL (a dirty worktree stops everything — see
// worktree.Remove).
//
// TWO sources feed one work list, because the nest is not the whole truth:
// Spawn gives a worktree to EVERY repo it mounts, including those passed on the
// command line, which are deliberately absent from the sandbox identity and
// therefore from n.Repos (issue #46). worktree.Orphans recovers those from the
// directories themselves. The declared list still comes first: it needs no git
// probing to be trusted.
//
// The enumeration runs BEFORE the first Remove, and that ordering is load-
// bearing: removeParentDir deletes <root>/<wt> as soon as it empties, so a
// removal loop running first could delete the very directory to be enumerated.
//
// out carries the success messages, warnW the best-effort warnings: a script
// reading only stdout must not see a successful `den rm` hiding a warning.
//
// worktree.Remove never deletes a worktree, it moves it to den_home's trash and
// returns the path of the entry it created. That path is the ONLY trace the
// user gets of where their work went, so it is announced for every worktree
// actually moved.
func cleanWorktrees(ctx context.Context, home, sandboxName string, g worktree.Git, force bool, out, warnW io.Writer) error {
	nestName, wt := sbx.SplitName(sandboxName)
	if wt == "" {
		return nil // no worktree: nothing to clean up
	}

	// sbx.Ls validates NOTHING of what it reads: a sandbox listed by `sbx ls`
	// may have been created outside den, with a name sbx accepts but den would
	// refuse ("api../../evade"). Without this check that name travels as-is to
	// worktree.Path and sends Remove outside worktree_root.
	if err := sbx.ValidateSandboxName(sandboxName); err != nil {
		return fmt.Errorf("cleaning up worktrees: %w", err)
	}

	// A command validates what it USES: here exactly WorktreeLayout and
	// WorktreeRoot, both read below to build worktree.Remove's Target.
	//
	// The validating LoadGlobal would be wrong here: it would fail `den rm` on
	// a bad `agents.claude.update` — unrelated to worktrees — and leave the
	// user with a live VM they can no longer destroy (doctrine T13/T16). But
	// validating nothing would be just as wrong: LoadGlobalUnvalidated only
	// defaults the EMPTY layout, so a `centrl` survives and den would clean up
	// somewhere else SILENTLY. Hence the explicit check on those two fields.
	gl, err := config.LoadGlobalUnvalidated(home)
	if err != nil {
		return err
	}
	if errs := gl.ValidateWorktree(); len(errs) > 0 {
		return fmt.Errorf("cleaning up worktrees: %w", config.ConfigError(home, errs))
	}

	var known []string
	if n, err := nest.LoadNest(home, nestName); err != nil {
		// Best-effort on RESOLUTION: an unreadable nest must not prevent
		// destroying a live sandbox (doctrine T13/T16). It no longer prevents
		// CLEANING UP either — the central-layout enumeration needs no declared
		// repo, so `known` simply stays nil and the worktrees are recovered from
		// their own directories. The message therefore says where den looked,
		// not what it gave up on.
		where := filepath.Join(gl.WorktreeRoot, wt)
		if gl.WorktreeLayout == "per-repo" {
			// Nothing is enumerable there: without a repo path there is no
			// directory to list (see worktree.Orphans).
			where = fmt.Sprintf("every repo of the nest, under <repo>/.den/%s", wt)
		}
		fmt.Fprintf(warnW, "nest %q unreadable: den read no repo from it and looked under %s instead: %v\n",
			nestName, where, err)
	} else {
		for _, repo := range n.Repos {
			known = append(known, repo.Path)
		}
	}

	base := worktree.Target{
		DenHome:  home,
		Layout:   gl.WorktreeLayout,
		Root:     gl.WorktreeRoot,
		Nest:     sandboxName,
		Worktree: wt,
		Force:    force,
	}
	targets := make([]worktree.Target, 0, len(known))
	for _, path := range known {
		t := base
		t.RepoPath = path
		targets = append(targets, t)
	}
	targets = append(targets, recoveredTargets(ctx, base, g, known, warnW)...)

	for _, t := range targets {
		// One deadline PER repo, not one for the whole loop: a broken repo must
		// not eat the budget of the next repos of the same nest.
		repoCtx, cancel := context.WithTimeout(ctx, gitProbeTimeout)
		dest, err := worktree.Remove(repoCtx, g, t)
		cancel()
		if err != nil {
			return err
		}
		if dest == "" {
			continue // the directory was already gone: nothing to announce
		}
		fmt.Fprintf(out, "worktree moved to trash: %s\n", dest)
	}
	return nil
}

// recoveredTargets asks worktree.Orphans for the worktrees no declared repo
// accounts for, and turns each into a Target sharing the caller's base — same
// layout, same trash, same Force. That sharing is the point: an orphan is not a
// licence to delete work, so it must meet the SAME uncommitted-changes refusal.
//
// Everything here is RESOLUTION, so nothing here fails the command: a repo
// deleted since the spawn, a directory den cannot vouch for, an unreadable
// worktree_root — each becomes a warning naming what was left behind, and
// `den rm` goes on to destroy the sandbox.
func recoveredTargets(ctx context.Context, base worktree.Target, g worktree.Git, known []string, warnW io.Writer) []worktree.Target {
	// One deadline for the whole enumeration: it is a resolution step, and its
	// worst case — a repo on a dead network mount — degrades to a warning, not
	// to a `den rm` that never returns.
	scanCtx, cancel := context.WithTimeout(ctx, gitProbeTimeout)
	defer cancel()

	found, skipped, err := worktree.Orphans(scanCtx, g, base.Root, base.Worktree, known)
	if err != nil {
		fmt.Fprintf(warnW, "worktrees left behind may survive under %s: %v\n",
			filepath.Join(base.Root, base.Worktree), err)
		return nil
	}
	for _, reason := range skipped {
		fmt.Fprintf(warnW, "worktree cleanup: %v\n", reason)
	}

	targets := make([]worktree.Target, 0, len(found))
	for _, o := range found {
		t := base
		t.RepoPath = o.RepoPath
		targets = append(targets, t)
	}
	return targets
}
```

Note: `recoveredTargets` is called unconditionally here. Task 5 adds the per-repo branch — `worktree.Orphans` is central-only, and in per-repo `<root>/<wt>` normally does not exist, so it returns nothing until then.

The `LoadNest` error branch above already carries its final wording, so `TestRmUnreadableNestDoesNotPreventDestruction` (`rm_test.go:232`), which asserts on both `"unreadable"` and `<denHome>/worktrees/feat12`, stays green. Task 4 is what locks the new BEHAVIOUR of that branch.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/cli/ -count=1`
Expected: PASS, whole package — every existing `TestRm*` included. A red one here is a regression, not a deferred change.

- [ ] **Step 5: Lint and commit**

```bash
gofmt -w internal/cli/rm.go internal/cli/rm_test.go
make lint && make test
git add internal/cli/rm.go internal/cli/rm_test.go
git commit -m "fix(cli): den rm removes the worktree of a repo it was never told about"
```

---

### Task 4: lock the unreadable-nest upgrade

Task 3 made the behaviour possible — enumeration needs no `n.Repos`, so `known == nil` still recovers everything. This task LOCKS it, and fixes the pre-existing case the issue names: a repo deleted from `repos:` and then `den rm`. **No production code changes here**; if a step below requires one, Task 3 was implemented wrong.

**Files:**
- Test: `internal/cli/rm_test.go`

**Interfaces:**
- Consumes: `cleanWorktrees`, `recoveredTargets` (Task 3).
- Produces: nothing new.

- [ ] **Step 1: Write the failing test**

```go
// The nest yaml is gone, so den knows NO repo — and recovers the worktree
// anyway, from the directory itself. Same mechanism as issue #46's ad-hoc
// repos: the enumeration needs no declared list.
func TestRmUnreadableNestStillCleansUpUnderCentralLayout(t *testing.T) {
	denHome := t.TempDir()
	root := filepath.Join(denHome, "worktrees")
	writeConfig(t, denHome, minimalConfig+"worktree_layout: central\nworktree_root: "+root+"\n")
	// No writeNest("api", ...): the nest is absent from ~/.den/nests.

	repo := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)
	p, err := worktree.Ensure(context.Background(), worktree.NewGit(),
		"central", root, worktree.Name{Dir: "feat12", Branch: "feat12"}, repo)
	if err != nil {
		t.Fatalf("preparing the worktree: %v", err)
	}

	f := &sbx.Fake{Responses: lsWith("api.feat12")}
	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(p); !os.IsNotExist(statErr) {
		t.Errorf("%s must have been recovered and moved despite the unreadable nest; stat: %v", p, statErr)
	}
	// The failed resolution is still reported: the user must know den read no
	// nest, even though it cleaned up.
	if !strings.Contains(out, "unreadable") {
		t.Errorf("the unreadable nest must still be reported; got:\n%s", out)
	}
	if !f.HasCalled("rm", "--force", "api.feat12") {
		t.Errorf("the sandbox must still be destroyed; calls: %v", f.Calls)
	}
}
```

Also update the stale comment of the existing `TestRmUnreadableNestDoesNotPreventDestruction` (`rm_test.go:229-231` and `:245-247`) — its assertions still hold, but they no longer mean what they say. Replace the two comments with:

```go
// Best-effort on RESOLUTION: a nest removed from ~/.den/nests since the spawn
// must not prevent destroying a genuinely live sandbox — and the warning must
// say where den looked for the worktrees it could not name.
```

```go
	// Default worktree_layout/worktree_root (minimalConfig declares neither):
	// central, under <denHome>/worktrees. Nothing was ever created there in
	// this test, so there is nothing to recover — the assertion is on den
	// naming where it looked, not on an abandoned directory.
```

- [ ] **Step 2: Run the tests**

Run: `go test ./internal/cli/ -count=1`
Expected: PASS, whole package — including `TestRmUnreadableNestWritesToStderr` (`rm_test.go:329`), which checks the warning goes to stderr and not stdout.

- [ ] **Step 3: Lint and commit**

```bash
gofmt -w internal/cli/rm_test.go
make lint && make test
git add internal/cli/rm_test.go
git commit -m "test(cli): an unreadable nest no longer abandons the worktrees it cannot name"
```

---

### Task 5: the per-repo warning

`Path` puts a per-repo worktree at `<repo>/.den/<wt>`: without the repo path den has nowhere to look. den keeps no state either, so it cannot know whether a repo was ever passed on the command line. The warning is therefore unconditional and phrased conditionally — it never asserts a leftover exists.

**Files:**
- Modify: `internal/cli/rm.go` (`recoveredTargets` call site in `cleanWorktrees`)
- Test: `internal/cli/rm_test.go`

**Interfaces:**
- Consumes: `gl.WorktreeLayout`, `nestName`, `wt`.
- Produces: nothing new.

- [ ] **Step 1: Write the failing test**

```go
// In per-repo, Path puts the worktree INSIDE the user's repository and den has
// no directory to enumerate: a repo passed on the command line leaves
// <repo>/.den/<wt> behind, inside a repository the user cares about, where
// nothing gitignores it. den cannot find it — so it says so, without ever
// claiming a leftover exists (it keeps no state and cannot know).
func TestRmWarnsAboutPossibleLeftoversUnderThePerRepoLayout(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig+"worktree_layout: per-repo\n")
	writeStack(t, denHome, "devx", "image: devx:v1\n")

	repo := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)
	writeNest(t, denHome, "api", "stack: devx\nrepos:\n  - { path: "+repo+" }\n")
	if _, err := worktree.Ensure(context.Background(), worktree.NewGit(),
		"per-repo", "", worktree.Name{Dir: "feat12", Branch: "feat12"}, repo); err != nil {
		t.Fatalf("preparing the worktree: %v", err)
	}

	f := &sbx.Fake{Responses: lsWith("api.feat12")}
	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "<repo>/.den/feat12") {
		t.Errorf("the warning must name where to look; got:\n%s", out)
	}
	// Conditional, never an assertion: this teardown declared its repo and left
	// nothing behind.
	if strings.Contains(out, "was left behind") || strings.Contains(out, "survives at") {
		t.Errorf("the warning must not claim a leftover exists; got:\n%s", out)
	}
	if !f.HasCalled("rm", "--force", "api.feat12") {
		t.Errorf("the sandbox must still be destroyed; calls: %v", f.Calls)
	}
}

// The counterpart: nothing of the sort is printed under the central layout,
// where den enumerates and therefore knows.
func TestRmDoesNotWarnAboutLeftoversUnderTheCentralLayout(t *testing.T) {
	denHome := t.TempDir()
	root := filepath.Join(denHome, "worktrees")
	writeConfig(t, denHome, minimalConfig+"worktree_layout: central\nworktree_root: "+root+"\n")
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	writeNest(t, denHome, "api", "stack: devx\nrepos: []\n")

	f := &sbx.Fake{Responses: lsWith("api.feat12")}
	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, ".den/feat12") {
		t.Errorf("no per-repo warning must appear under the central layout; got:\n%s", out)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/cli/ -run 'TestRmWarnsAboutPossibleLeftovers|TestRmDoesNotWarnAboutLeftovers' -count=1`
Expected: `TestRmWarnsAboutPossibleLeftovers...` FAILS (nothing names `<repo>/.den/feat12`); the central one PASSES.

- [ ] **Step 3: Branch the recovery on the layout**

In `cleanWorktrees`, replace the single `targets = append(targets, recoveredTargets(...)...)` line with:

```go
	if gl.WorktreeLayout == "per-repo" {
		// Nothing to enumerate: Path puts the worktree at <repo>/.den/<wt>, so
		// without a repo path there is no directory to list. And den keeps no
		// state beyond the sandbox name, so it cannot know whether a repo was
		// ever passed on the command line — the warning is therefore
		// CONDITIONAL, never an assertion that something was left behind.
		// Noisy for users who never pass a positional; that is the price of not
		// lying about a state den does not have.
		fmt.Fprintf(warnW,
			"per-repo layout: den can only clean up the repos declared in nest %q — "+
				"if you passed a repo on the command line to `den %s`, look for its worktree "+
				"at <repo>/.den/%s and remove it by hand\n", nestName, nestName, wt)
	} else {
		targets = append(targets, recoveredTargets(ctx, base, g, known, warnW)...)
	}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/cli/ -count=1`
Expected: PASS, whole package — `TestRmRespectsThePerRepoLayout` (`rm_test.go:435`) included; it asserts on the removal, not on the absence of warnings.

- [ ] **Step 5: Lint and commit**

```bash
gofmt -w internal/cli/rm.go internal/cli/rm_test.go
make lint && make test
git add internal/cli/rm.go internal/cli/rm_test.go
git commit -m "fix(cli): den rm names what the per-repo layout hides from it"
```

---

### Task 6: documentation catches up

The limit these tasks removed is currently documented as permanent, in two places. A README that describes a bug den no longer has is worse than no note at all.

**Files:**
- Modify: `README.md` (the `den rm` paragraph)
- Modify: `docs/superpowers/specs/2026-07-27-den-cli-design.md` (§6, "Limite connue du teardown")

**Interfaces:** none — documentation only.

- [ ] **Step 1: Replace `README.md:138-152`**

That whole block — from `**`den rm` does not clean the worktree…**` down to `…but yours to remove.`, the `git worktree remove` fenced example included — becomes:

````markdown
**`den rm` cleans up the worktrees of repos it was never told about.** A positional is not part
of the sandbox identity, so den persists it nowhere — but the worktree itself remembers: each
directory carries a `.git` pointing back at its repository. Under the default `central` layout
`den rm` enumerates `worktree_root/<wt>/` and recovers what the nest's `repos:` does not explain,
so `den api -w feat ~/dev/hotfix` then `den rm api.feat` leaves neither `worktree_root/feat/hotfix`
nor its git registration behind. The same now holds for a repo deleted from `repos:` before the
teardown.

A directory den cannot vouch for is **left in place**, named in a warning: a repository parked
under `worktree_root`, a directory that is not a worktree, a repo whose directory name does not
match. den removes what it placed, not what merely looks like it.

Under the `per-repo` layout there is nothing to enumerate — the worktree lives at `<repo>/.den/<wt>`
and without the repo path den has nowhere to look. `den rm` cleans up the declared repos and warns
where to look for the rest:

```bash
git -C ~/dev/hotfix worktree remove ~/dev/hotfix/.den/feat
```

The `.den/` line den added to that repo's `.git/info/exclude` stays either way — harmless, local,
never committed, but yours to remove.
````

- [ ] **Step 2: Replace `docs/superpowers/specs/2026-07-27-den-cli-design.md:286-296`**

The paragraph "**Limite connue du teardown.**" through "…plutôt qu'un passager sur celui-ci." becomes (that spec is French):

```markdown
**Teardown des worktrees non déclarés.** `den rm` nettoie aussi le worktree d'un repo passé en
positionnel. Un positionnel ne fait pas partie de l'identité (décision 7) et den ne le persiste
nulle part — mais le répertoire, lui, se souvient : son `.git` désigne son dépôt. En layout
`central`, `den rm` énumère donc `worktree_root/<wt>/*` et récupère les entrées que les `repos:` du
nest n'expliquent pas ; le cas préexistant du repo retiré de `repos:` avant le teardown est corrigé
du même geste. Une entrée dont den ne peut pas répondre — un dépôt garé sous `worktree_root`, un
répertoire qui n'est pas un worktree, un repo dont le nom de répertoire diffère — est **laissée en
place** et nommée dans un avertissement. En `per-repo` l'énumération reste impossible faute du
chemin du repo : den nettoie les repos déclarés et avertit pour le reste, sous `<repo>/.den/<wt>`.
`--force` et le refus sur modifications non commitées s'appliquent aux entrées récupérées comme aux
autres. Mécanisme :
`docs/superpowers/specs/2026-08-04-den-rm-orphan-worktrees-design.md` ; historique : issue #46.
```

- [ ] **Step 3: Verify nothing else still claims the old limit**

```bash
grep -rn "does not clean the worktree\|ne nettoie PAS" README.md docs/superpowers/specs/ CLAUDE.md
```
Expected: no hit. `CLAUDE.md`'s "Stale artifacts" section says nothing about this limit — leave it alone.

- [ ] **Step 4: Commit**

```bash
make test
git add README.md docs/superpowers/specs/2026-07-27-den-cli-design.md
git commit -m "docs: den rm cleans up what it was never told about"
```

---

## Final verification

- [ ] `make test` — the whole suite, `-count=1`, green.
- [ ] `make lint` — `go vet` clean, `gofmt -l .` empty.
- [ ] `make typecheck`.
- [ ] `go test ./internal/ports/ -run Hermeticity -count=1` — `internal/cli` still imports no `net`, `hash/fnv` or `os/exec` (this plan adds no import to `internal/cli`).
- [ ] Manual smoke, if a real `sbx` is available:

```bash
den api -w feat ~/dev/hotfix
den rm api.feat
ls ~/.den/worktrees/feat 2>&1        # must not exist
git -C ~/dev/hotfix worktree list    # must not list feat
```

- [ ] Open the PR based on `feat/adhoc-repos`, closing #46.
