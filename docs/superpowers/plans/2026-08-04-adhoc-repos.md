# Ad-hoc repos (`den <nest> [path...]`) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let `den <nest> ~/dev/a ~/dev/b` mount repositories declared nowhere in `~/.den/`, treated exactly like a `repos:` entry.

**Architecture:** One merge point. Command-line paths enter `nest.Options`, `nest.Resolve` normalizes and merges them into `Resolved.Repos` ahead of the declared ones, and nothing downstream branches — worktrees, common git dirs, workspace order, mixin and `sbx create` argv all consume the merged list unchanged. The command-line surface (variadic positionals) is opened LAST, after every refusal and warning it needs is already in place.

**Tech Stack:** Go 1.x, cobra, `gopkg.in/yaml.v3` (strict decoding). No new dependencies.

**Spec:** `docs/superpowers/specs/2026-08-04-adhoc-repos-design.md`. Read it before Task 1 — every "why" below is a summary of a decision recorded there.

## Global Constraints

- **Build/test/lint:** `make test` (`go test -count=1 ./...`), `make typecheck` (`go build ./...`), `make lint` (`go vet ./...` + `gofmt -l .` must be empty). `gofmt` is enforced, not advisory — run `gofmt -w` on every file touched.
- **Language:** code, comments and user-facing messages are **English**. The spec and this plan are French; do not translate code comments into French.
- **Comment style:** the dominant style in this repo is a long "why" comment at the decision site, naming what was rejected and what regression the choice prevents. Terse code visibly does not match. Every comment written below is part of the deliverable — copy it.
- **Errors name the file to fix and the remedy.** den refuses rather than normalizing in silence (spec §2).
- **Test hermeticity:** no test calls `t.Parallel()`, opens a socket, or spawns a process (`internal/spawn` and `internal/cli` DO run real `git` via `worktree.NewGit()` — that is pre-existing and allowed; their `TestMain` calls `worktree.NeutralizeGitEnvironment()`).
- **Goldens** live in `internal/*/testdata/*.golden`, compared by hand. **There is no `-update` flag** — edit them manually.
- **Never bypass a configured local registry.** Not applicable here (no package installs).
- **Branch:** work happens on `feat/adhoc-repos`, already created. Commit after every task.

---

### Task 1: `parseRepoArg` — one positional becomes a `Repo`

**Files:**
- Modify: `internal/nest/nest.go` (add the `AdHoc` field to `Repo`)
- Modify: `internal/nest/repos.go` (add `parseRepoArg`, `parseRepoArgs`, `origin`; change `checkUniqueNames`'s signature)
- Modify: `internal/nest/nest.go` (`LoadNest`'s call to `checkUniqueNames`)
- Test: `internal/nest/repos_test.go`

**Interfaces:**
- Consumes: `config.ExpandPath(p string) (string, error)` — expands `~` and **only** `~`; it does not absolutize.
- Produces:
  - `nest.Repo` gains `AdHoc bool \`yaml:"-"\``
  - `parseRepoArg(cwd, raw string) (Repo, error)` — unexported
  - `parseRepoArgs(cwd string, raws []string) ([]Repo, error)` — unexported
  - `origin(r Repo) string` — unexported
  - `checkUniqueNames(repos []Repo, scope string) error` — signature CHANGED, gains `scope`

---

- [ ] **Step 1: Write the failing tests**

Append to `internal/nest/repos_test.go`:

```go
func TestParseRepoArgNormalizes(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory: the tilde cases cannot be asserted")
	}
	cases := []struct {
		name     string
		cwd, raw string
		expected string
	}{
		{"absolute path travels as-is", "/work", "/dev/api", "/dev/api"},
		{"tilde is expanded", "/work", "~/dev/api", filepath.Join(home, "dev", "api")},
		{"dot is the working directory", "/work/api", ".", "/work/api"},
		{"relative path resolves against cwd", "/work", "sub/api", "/work/sub/api"},
		{"parent traversal resolves too", "/work/api", "../front", "/work/front"},
		{"redundant separators are cleaned", "/work", "/dev//api/", "/dev/api"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseRepoArg(c.cwd, c.raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Path != c.expected {
				t.Errorf("Path = %q, expected %q", got.Path, c.expected)
			}
			if !got.AdHoc {
				t.Error("AdHoc = false: a repo built from the command line must carry its origin, " +
					"it is what decides which place the \"not found\" error tells the user to fix")
			}
			if got.Optional {
				t.Error("Optional = true: a repo typed on the command line was asked for explicitly, " +
					"--without/--only never address it")
			}
		})
	}
}

func TestParseRepoArgRefusesReadOnlySuffix(t *testing.T) {
	// The path EXISTS as far as this function is concerned: the point is that
	// the refusal must talk about `:ro`, not about a missing directory. sbx
	// accepts the suffix, so a user who writes it is asking for something den
	// deliberately does not do.
	_, err := parseRepoArg("/work", "/dev/api:ro")
	if err == nil {
		t.Fatal("expected a refusal for the `:ro` suffix")
	}
	if !strings.Contains(err.Error(), ":ro") {
		t.Errorf("error = %q, expected it to name `:ro` — otherwise the user reads "+
			"\"no such path\" about a directory that exists", err)
	}
}

func TestParseRepoArgRefusesEmpty(t *testing.T) {
	_, err := parseRepoArg("/work", "   ")
	if err == nil {
		t.Fatal("expected a refusal for an empty path")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error = %q, expected it to say the path is empty", err)
	}
}

func TestParseRepoArgsRefusesAMissingWorkingDirectory(t *testing.T) {
	// A wiring defect, not a user error: nest.Options.Cwd unset while Repos is
	// not. Falling back on the process's cwd would be a silent retreat to
	// exactly the system access the parameter exists to remove, and it would
	// only show up at runtime, on the wrong path.
	_, err := parseRepoArgs("", []string{"/dev/api"})
	if err == nil {
		t.Fatal("expected a refusal when Cwd is unset")
	}
	if !strings.Contains(err.Error(), "Cwd") {
		t.Errorf("error = %q, expected it to name the unset field", err)
	}
}

func TestParseRepoArgsEmptyInputYieldsNothing(t *testing.T) {
	// The nominal case for every nest spawned without positionals: no Cwd is
	// required, and nothing is added.
	got, err := parseRepoArgs("", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, expected nothing", got)
	}
}

func TestCheckUniqueNamesNamesTheCommandLineOrigin(t *testing.T) {
	err := checkUniqueNames([]Repo{
		{Path: "/tmp/scratch/api", AdHoc: true},
		{Path: "/dev/api"},
	}, "spawn")
	if err == nil {
		t.Fatal("expected a refusal: both repos have the short name \"api\"")
	}
	if !strings.Contains(err.Error(), "command line") {
		t.Errorf("error = %q, expected it to say which of the two came from the command line — "+
			"only that one is fixable by retyping", err)
	}
	if !strings.Contains(err.Error(), "/dev/api") {
		t.Errorf("error = %q, expected it to name the declared path too", err)
	}
}
```

Add `"os"`, `"path/filepath"` to the imports of `internal/nest/repos_test.go` (it already imports `slices`, `strings`, `testing`).

Also update the ONE existing assertion that pins `checkUniqueNames`'s old signature. Find it:

```bash
grep -n "checkUniqueNames" internal/nest/*_test.go
```

Every call becomes `checkUniqueNames(<repos>, "nest")`, and its expected message is unchanged.

- [ ] **Step 2: Run the tests and verify they fail**

Run: `go test ./internal/nest/ -run 'TestParseRepo|TestCheckUniqueNames' -count=1`
Expected: compilation failure — `undefined: parseRepoArg`, `undefined: parseRepoArgs`, and `too many arguments in call to checkUniqueNames`.

- [ ] **Step 3: Add `AdHoc` to `Repo`**

In `internal/nest/nest.go`, replace the `Repo` type:

```go
// Repo is a repository co-mounted in the sandbox.
//
// AdHoc records where the entry came from, and it is not cosmetic: it decides
// which place a "repo not found" tells the user to correct — a `repos:` line in
// a yaml file, or the command they just typed. Sending someone to edit a nest
// file over a path they typed by hand is the kind of wrong remedy den exists to
// avoid (spec §2).
//
// `yaml:"-"` states the intent rather than leaving it to a side effect of the
// decoder: strict decoding (KnownFields(true)) would already reject a hand
// written `adhoc:`, but that is a property of the loader, not of this type.
type Repo struct {
	Path     string `yaml:"path"`
	Optional bool   `yaml:"optional"`
	AdHoc    bool   `yaml:"-"`
}
```

- [ ] **Step 4: Write `parseRepoArg`, `parseRepoArgs` and `origin`, and reshape `checkUniqueNames`**

In `internal/nest/repos.go`, replace `checkUniqueNames` and add the three new functions:

```go
// checkUniqueNames rejects two repos sharing a basename within one list.
// The basename IS a repo's identity: --without/--only address it by this
// name, and it becomes a path (worktree_root/<wt>/<repo>) and the position of
// a `sbx create` argument. Two homonyms would make all three
// ambiguous — and `--without api` would silently drop two of them. This is a
// configuration that cannot be honored: a hard error, not a surprise.
//
// scope names WHAT the list is, because the two callers judge different things:
// LoadNest judges a single file (the fix is in the yaml), Resolve judges the
// merged spawn, where the collision may be between a file and the command line
// and only the second is fixable by retyping. origin() carries the other half.
func checkUniqueNames(repos []Repo, scope string) error {
	seen := make(map[string]Repo, len(repos))
	for _, r := range repos {
		if previous, ok := seen[r.Name()]; ok {
			return fmt.Errorf(
				"two repos share the short name %q (%s and %s) — this name is used by --without/--only "+
					"and by the worktree path, it must be unique within the %s",
				r.Name(), origin(previous), origin(r), scope)
		}
		seen[r.Name()] = r
	}
	return nil
}

// origin names where a repo came from, for the collision message.
//
// A declared repo shows its path ALONE, which keeps LoadNest's message exactly
// what it was: at load time the command line does not exist, and mentioning it
// would send the user to correct something that had no part in the collision.
func origin(r Repo) string {
	if r.AdHoc {
		return r.Path + " (command line)"
	}
	return r.Path
}

// parseRepoArgs turns the command line's positionals into repos.
//
// cwd is REQUIRED as soon as there is one positional, and its absence is an
// error rather than a fallback on the process's working directory: that
// fallback would be a silent retreat to exactly the system access the parameter
// exists to keep out of this package, and it would only show itself at runtime,
// on the wrong path.
func parseRepoArgs(cwd string, raws []string) ([]Repo, error) {
	if len(raws) == 0 {
		return nil, nil
	}
	if cwd == "" {
		return nil, fmt.Errorf(
			"%d repo(s) given on the command line but nest.Options.Cwd is unset, so a relative "+
				"path has nothing to resolve against — this is a wiring defect in den, please report it",
			len(raws))
	}
	out := make([]Repo, 0, len(raws))
	for _, raw := range raws {
		r, err := parseRepoArg(cwd, raw)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// parseRepoArg normalizes ONE positional into a Repo.
//
// The order of the three steps is the whole point:
//
//  1. `:ro` is refused FIRST, before any path handling. sbx accepts the suffix
//     on a workspace, so someone who writes it is asking for something den
//     deliberately does not do — and letting the string through would answer
//     "no such path" about a directory that exists.
//  2. ExpandPath, like `repos:`, `ssh.dir` and `config_dir`. It handles the
//     tilde and NOTHING else.
//  3. absolutize against cwd. This step, and only this step, is what makes
//     `den scratch .` work: sbx.checkWorkspace rejects every relative path,
//     because it would resolve against a working directory nothing guarantees
//     by the time sbx uses it.
func parseRepoArg(cwd, raw string) (Repo, error) {
	if strings.TrimSpace(raw) == "" {
		return Repo{}, fmt.Errorf(
			"empty repo path on the command line — `sbx create` would receive an empty positional, " +
				"which mounts nothing")
	}
	if strings.HasSuffix(raw, ":ro") {
		return Repo{}, fmt.Errorf(
			"repo %q: `:ro` is not supported — a repo given on the command line is mounted "+
				"writable, like a declared `repos:` entry", raw)
	}
	expanded, err := config.ExpandPath(raw)
	if err != nil {
		return Repo{}, err
	}
	if !filepath.IsAbs(expanded) {
		expanded = filepath.Join(cwd, expanded)
	}
	return Repo{Path: filepath.Clean(expanded), AdHoc: true}, nil
}
```

Update `internal/nest/repos.go`'s imports: it currently has `fmt`, `slices`, `strings`; add `path/filepath` and `github.com/PillowPillow/den/internal/config`.

- [ ] **Step 5: Fix `LoadNest`'s call site**

In `internal/nest/nest.go`, the call inside `LoadNest`:

```go
	// After expansion: two differently-written paths can converge.
	if err := checkUniqueNames(n.Repos, "nest"); err != nil {
		return nil, fmt.Errorf("nest %q: %w", n.Name, err)
	}
```

- [ ] **Step 6: Run the tests and verify they pass**

Run: `go test ./internal/nest/ -count=1`
Expected: PASS, including the pre-existing `checkUniqueNames` tests with their message unchanged.

- [ ] **Step 7: Lint and commit**

```bash
gofmt -w internal/nest/
make lint && go test ./internal/nest/ -count=1
git add internal/nest/
git commit -m "feat(nest): parseRepoArg — un positionnel devient un Repo

\`:ro\` refusé avant tout traitement de chemin (sinon l'utilisateur lit \"no
such path\" sur un dossier qui existe), tilde puis absolutisation contre un cwd
passé en paramètre — c'est cette dernière étape qui fait marcher \`den scratch .\`,
et le paramètre qui garde internal/nest pur. checkUniqueNames prend le scope
qu'il juge: LoadNest garde son message, la fusion nommera la ligne de commande."
```

---

### Task 2: fusion in `nest.Resolve`

**Files:**
- Modify: `internal/nest/resolve.go` (`Options` gains two fields; `Resolve` merges)
- Test: `internal/nest/resolve_test.go`

**Interfaces:**
- Consumes: `parseRepoArgs(cwd string, raws []string) ([]Repo, error)`, `checkUniqueNames(repos []Repo, scope string) error`, `selectRepos(repos []Repo, without, only []string) ([]Repo, error)` (unchanged).
- Produces: `nest.Options` gains `Repos []string` and `Cwd string`. `Resolved.Repos` is now `[positionals...] ++ selectRepos(declared)`.

---

- [ ] **Step 1: Write the failing tests**

Append to `internal/nest/resolve_test.go`:

```go
func TestResolvePutsCommandLineReposFirst(t *testing.T) {
	// Workspaces[0] decides where the attached shell starts
	// (sbx.Sandbox.Workdir). "I am mounting X on the fly" means "I have come to
	// work in X", so a positional wins over the nest's own first repo.
	r, err := Resolve("/d", globalTest(), stacksTest(), nestTest(), Options{
		Repos: []string{"/tmp/hotfix"},
		Cwd:   "/work",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []string{"/tmp/hotfix", "/dev/api", "/dev/front"}
	if got := paths(r.Repos); !slices.Equal(got, expected) {
		t.Errorf("Repos = %v, expected %v — the positional comes first", got, expected)
	}
	if !r.Repos[0].AdHoc {
		t.Error("Repos[0].AdHoc = false: the origin must survive the merge")
	}
	if r.Repos[1].AdHoc {
		t.Error("Repos[1].AdHoc = true: a declared repo must not be reported as ad-hoc")
	}
}

func TestResolveWithoutStillFiltersDeclaredRepos(t *testing.T) {
	// --without/--only keep addressing the declared list ONLY. A repo given on
	// the command line is removed by not typing it.
	r, err := Resolve("/d", globalTest(), stacksTest(), nestTest(), Options{
		Repos:   []string{"/tmp/hotfix"},
		Cwd:     "/work",
		Without: []string{"front"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []string{"/tmp/hotfix", "/dev/api"}
	if got := paths(r.Repos); !slices.Equal(got, expected) {
		t.Errorf("Repos = %v, expected %v", got, expected)
	}
}

func TestResolveRefusesWithoutNamingACommandLineRepo(t *testing.T) {
	_, err := Resolve("/d", globalTest(), stacksTest(), nestTest(), Options{
		Repos:   []string{"/tmp/hotfix"},
		Cwd:     "/work",
		Without: []string{"hotfix"},
	})
	if err == nil {
		t.Fatal("expected a refusal: --without does not address a repo given on the command line")
	}
	if !strings.Contains(err.Error(), "hotfix") {
		t.Errorf("error = %q, expected it to name the repo", err)
	}
}

func TestResolveRefusesABasenameCollisionWithTheCommandLine(t *testing.T) {
	// nestTest declares /dev/api. A positional whose basename is also "api"
	// makes --without, the worktree path and the sbx positional ambiguous at
	// once: a hard error, not a last-one-wins.
	_, err := Resolve("/d", globalTest(), stacksTest(), nestTest(), Options{
		Repos: []string{"/tmp/scratch/api"},
		Cwd:   "/work",
	})
	if err == nil {
		t.Fatal("expected a refusal on the duplicated short name \"api\"")
	}
	if !strings.Contains(err.Error(), "command line") {
		t.Errorf("error = %q, expected it to point at the fixable half", err)
	}
}

func TestResolveWithoutCommandLineReposIsUnchanged(t *testing.T) {
	// The nominal path: no positional, no Cwd, and the declared list is exactly
	// what it was before this feature existed.
	r, err := Resolve("/d", globalTest(), stacksTest(), nestTest(), Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := paths(r.Repos); !slices.Equal(got, []string{"/dev/api", "/dev/front"}) {
		t.Errorf("Repos = %v", got)
	}
}

// paths is the Path projection, so a failure prints what a reader recognizes
// rather than a wall of struct literals.
func paths(rs []Repo) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Path)
	}
	return out
}
```

Add `"slices"` and `"strings"` to `internal/nest/resolve_test.go`'s imports if absent.

- [ ] **Step 2: Run the tests and verify they fail**

Run: `go test ./internal/nest/ -run TestResolve -count=1`
Expected: compilation failure — `unknown field Repos in struct literal of type Options`.

- [ ] **Step 3: Extend `Options`**

In `internal/nest/resolve.go`:

```go
// Options carries the overrides coming from CLI flags (the cascade's last level).
type Options struct {
	Agent   string   // --agent
	Without []string // --without
	Only    []string // --only
	// Repos are the repositories given as positionals on the command line, raw:
	// tilde unexpanded, possibly relative. They are additive to the nest's
	// `repos:`, and they are NOT addressable by --without/--only — a repo typed
	// by hand is removed by not typing it.
	Repos []string
	// Cwd resolves the relative entries of Repos. A parameter, not an
	// os.Getwd() inside this package: the resolution stays pure, so `den
	// scratch .` is assertable without a test having to chdir, and the one
	// system call lives with the other world access in internal/spawn.
	Cwd string
}
```

- [ ] **Step 4: Merge inside `Resolve`**

In `internal/nest/resolve.go`, replace the `selectRepos` block:

```go
	repos, err := selectRepos(n.Repos, o.Without, o.Only)
	if err != nil {
		return nil, fmt.Errorf("nest %q: %w", n.Name, err)
	}
	adhoc, err := parseRepoArgs(o.Cwd, o.Repos)
	if err != nil {
		return nil, fmt.Errorf("nest %q: %w", n.Name, err)
	}
	// Positionals FIRST, declared repos after: internal/spawn turns this list
	// into `sbx create`'s workspaces in order, and sbx.Sandbox.Workdir — the
	// directory the attached shell starts in — is Workspaces[0]. The gesture
	// "I am mounting X on the fly" means "I have come to work in X".
	//
	// This is the merge point of the whole feature, and the reason nothing
	// downstream needs a branch: from here on a repo given on the command line
	// IS a repo. It gets a worktree under -w, its common git dir mounted, its
	// place in the argv — by construction, not by repetition.
	repos = append(adhoc, repos...)
	// Re-checked on the MERGED list: LoadNest only ever saw the file. A
	// positional colliding with a declared basename makes --without, the
	// worktree path and the sbx positional ambiguous at once.
	if err := checkUniqueNames(repos, "spawn"); err != nil {
		return nil, fmt.Errorf("nest %q: %w", n.Name, err)
	}
```

- [ ] **Step 5: Run the tests and verify they pass**

Run: `go test ./internal/nest/ -count=1`
Expected: PASS.

- [ ] **Step 6: Lint and commit**

```bash
gofmt -w internal/nest/
make lint && go test ./internal/nest/ -count=1
git add internal/nest/
git commit -m "feat(nest): Resolve fusionne les repos de la ligne de commande

Les positionnels passent DEVANT les déclarés: internal/spawn transforme cette
liste en workspaces dans l'ordre, et Workspaces[0] décide du répertoire de
démarrage du shell. Point de fusion unique — rien en aval n'a de branche à
ajouter, un repo à la volée EST un repo. checkUniqueNames rejoué sur la liste
fusionnée: LoadNest n'a jamais vu que le fichier."
```

---

### Task 3: spawn plumbing — positionals reach `sbx create`

**Files:**
- Modify: `internal/spawn/spawn.go` (`Options` gains `Repos`; `Spawn` reads the cwd and passes it; step 2's "repo not found" branches on origin)
- Test: `internal/spawn/spawn_test.go`

**Interfaces:**
- Consumes: `nest.Options{Repos []string, Cwd string}`, `nest.Repo.AdHoc bool`.
- Produces: `spawn.Options` gains `Repos []string` — the raw positionals, in command-line order.

---

- [ ] **Step 1: Write the failing tests**

Append to `internal/spawn/spawn_test.go`. `denTest` builds a den home whose nest `api` declares one repo; `fakeDeps`, `callStartingWith` and `workspacesOf` are existing helpers in that file.

```go
func TestSpawnMountsCommandLineRepos(t *testing.T) {
	denHome, repo := denTest(t)
	hotfix := filepath.Join(t.TempDir(), "hotfix")
	createRepo(t, hotfix)
	f, d := fakeDeps()

	if err := Spawn(context.Background(), denHome,
		Options{Nest: "api", Repos: []string{hotfix}}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := workspacesOf(callStartingWith(f, "create"))
	expected := []string{hotfix, repo, filepath.Join(denHome, "agents", "claude")}
	if !slices.Equal(got, expected) {
		t.Errorf("workspaces = %v, expected %v — the positional comes first, because "+
			"Workspaces[0] is where the attached shell starts", got, expected)
	}
}

func TestSpawnMountsSeveralCommandLineReposInOrder(t *testing.T) {
	// A nest with NO `repos:` at all: the headline case. Without the
	// positionals its only workspace would be the agent profile, which is a
	// useless place to land.
	denHome, _ := denTest(t)
	write(t, filepath.Join(denHome, "nests", "scratch.yaml"), "stack: devx\n")
	a := filepath.Join(t.TempDir(), "a")
	b := filepath.Join(t.TempDir(), "b")
	createRepo(t, a)
	createRepo(t, b)
	f, d := fakeDeps()

	if err := Spawn(context.Background(), denHome,
		Options{Nest: "scratch", Repos: []string{a, b}}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := workspacesOf(callStartingWith(f, "create"))
	expected := []string{a, b, filepath.Join(denHome, "agents", "claude")}
	if !slices.Equal(got, expected) {
		t.Errorf("workspaces = %v, expected %v", got, expected)
	}
}

func TestSpawnRefusesAMissingCommandLineRepoWithoutBlamingTheNestFile(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := fakeDeps()

	err := Spawn(context.Background(), denHome,
		Options{Nest: "api", Repos: []string{filepath.Join(t.TempDir(), "gone")}}, d)
	if err == nil {
		t.Fatal("expected a refusal on a path that does not exist")
	}
	if !strings.Contains(err.Error(), "command line") {
		t.Errorf("error = %q, expected it to name the command line — sending the user to edit "+
			"nests/api.yaml over a path they typed by hand is the wrong remedy", err)
	}
	if strings.Contains(err.Error(), "repos:") {
		t.Errorf("error = %q, expected it NOT to quote `repos:`", err)
	}
	if callStartingWith(f, "create") != nil {
		t.Error("a sandbox was created despite the refusal: everything rejectable from config "+
			"alone must be rejected before the first side effect")
	}
}

func TestSpawnStillBlamesTheNestFileForADeclaredRepo(t *testing.T) {
	// The counterpart, so "branches on origin" cannot degrade into "always says
	// command line".
	denHome, _ := denTest(t)
	write(t, filepath.Join(denHome, "nests", "api.yaml"),
		"stack: devx\nrepos:\n  - { path: "+filepath.Join(t.TempDir(), "gone")+" }\n")
	_, d := fakeDeps()

	err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !strings.Contains(err.Error(), "repos:") {
		t.Errorf("error = %q, expected it to send the user to `repos:`", err)
	}
}
```

- [ ] **Step 2: Run the tests and verify they fail**

Run: `go test ./internal/spawn/ -run 'TestSpawnMounts|TestSpawnRefusesAMissingCommandLine|TestSpawnStillBlames' -count=1`
Expected: compilation failure — `unknown field Repos in struct literal of type Options`.

- [ ] **Step 3: Extend `spawn.Options`**

In `internal/spawn/spawn.go`, add to `Options`:

```go
	// Repos are the repositories given as positionals: `den <nest> ~/dev/a`.
	// Raw — tilde unexpanded, possibly relative; nest.Resolve normalizes them.
	// Additive to the nest's `repos:`, and placed AHEAD of them, so the first
	// one becomes the directory the attached shell starts in.
	Repos []string
```

- [ ] **Step 4: Read the cwd and pass it through**

In `internal/spawn/spawn.go`, replace the `nest.Resolve` call at step 1:

```go
	// The working directory is read HERE, once, and handed to internal/nest,
	// which stays pure: `den scratch .` is then assertable without a test having
	// to chdir. os.Getwd is world access, like the os.Stat probes at step 2 —
	// this side of the boundary is where it belongs.
	//
	// Read only when there IS a positional: a spawn with none must not fail
	// because the process sits in a deleted directory.
	cwd := ""
	if len(o.Repos) > 0 {
		if cwd, err = os.Getwd(); err != nil {
			return fmt.Errorf(
				"reading the working directory, needed to resolve the repos given on "+
					"the command line: %w", err)
		}
	}
	r, err := nest.Resolve(denHome, g, stacks, n, nest.Options{
		Agent: o.Agent, Without: without, Only: o.Only, Repos: o.Repos, Cwd: cwd,
	})
	if err != nil {
		return err
	}
```

- [ ] **Step 5: Branch step 2's refusal on the repo's origin**

In `internal/spawn/spawn.go`, replace the existence loop at step 2:

```go
	// 2. All repos must exist before any create (spec §11).
	for _, repo := range r.Repos {
		if _, err := os.Stat(repo.Path); err != nil {
			// The remedy follows the ORIGIN. Sending someone to edit
			// nests/<n>.yaml over a path they typed by hand names a file that
			// has nothing to do with the failure — the wrong remedy is worse
			// than a bare error, because it is followed.
			if repo.AdHoc {
				return fmt.Errorf(
					"repo not found: %s — given on the command line", repo.Path)
			}
			return fmt.Errorf(
				"nest %q: repo not found: %s — fix `repos:` in %s",
				o.Nest, repo.Path, nest.FilePath(denHome, o.Nest))
		}
	}
```

- [ ] **Step 6: Run the tests and verify they pass**

Run: `go test ./internal/spawn/ -count=1`
Expected: PASS, `TestSpawnAddsNoWorkspaceOutsideMountMode` included — it is the witness that no workspace leaked elsewhere.

- [ ] **Step 7: Lint and commit**

```bash
gofmt -w internal/spawn/
make lint && go test ./internal/spawn/ -count=1
git add internal/spawn/
git commit -m "feat(spawn): les positionnels atteignent \`sbx create\`

Le cwd est lu ici, une fois, et passé à internal/nest qui reste pur. Le refus
\"repo not found\" suit désormais l'ORIGINE: envoyer quelqu'un éditer
nests/<n>.yaml pour un chemin tapé à la main nomme un fichier qui n'y est pour
rien, et un mauvais remède est pire qu'une erreur nue — il est suivi."
```

---

### Task 4: git pre-flight under `-w`, before the first side effect

**Files:**
- Modify: `internal/spawn/spawn.go` (probe at step 2, reuse at step 3)
- Test: `internal/spawn/spawn_test.go`

**Interfaces:**
- Consumes: `worktree.CommonGitDir(ctx context.Context, g worktree.Git, repoPath string) (string, error)`.
- Produces: nothing new exported. Step 3 stops calling `worktree.CommonGitDir` and reads `commonDirs[repo.Path]`.

**Why this task exists:** `worktree.Ensure` calls `checkRepo` (`internal/worktree/worktree.go:579`), which only `os.Stat`s. A non-git directory is not caught until `git worktree add` fails — at step 3, **after** the worktrees of the repos before it were created. One orphaned worktree per repo, left to clean up by hand. That is the exact regression `Spawn`'s ordering exists to prevent, and it predates this feature: a declared `repos:` entry pointing at a non-git directory has it too. Positionals make it reachable in one keystroke.

---

- [ ] **Step 1: Write the failing tests**

Append to `internal/spawn/spawn_test.go`:

```go
func TestSpawnRefusesANonGitRepoUnderWorktreeBeforeCreatingAnything(t *testing.T) {
	denHome, repo := denTest(t)
	data := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	f, d := fakeDeps()

	err := Spawn(context.Background(), denHome,
		Options{Nest: "api", Worktree: "feat", Repos: []string{data}}, d)
	if err == nil {
		t.Fatal("expected a refusal: -w propagates a worktree to every repo, and data is not one")
	}
	if !strings.Contains(err.Error(), data) {
		t.Errorf("error = %q, expected it to name the offending path", err)
	}
	if !strings.Contains(err.Error(), "-w") {
		t.Errorf("error = %q, expected it to name the flag that made this fatal", err)
	}

	// The assertion that actually proves "before the first side effect": no
	// worktree exists for the nest's OWN repo, which is processed first and
	// would already have one if the refusal came from step 3.
	//
	// The message alone proves nothing — it would read identically after the
	// damage was done.
	wt := filepath.Join(denHome, "worktrees", "feat", filepath.Base(repo))
	if _, statErr := os.Stat(wt); statErr == nil {
		t.Errorf("%s exists: a worktree was created before the refusal, which is the orphan "+
			"this ordering exists to prevent", wt)
	}
	if callStartingWith(f, "create") != nil {
		t.Error("a sandbox was created despite the refusal")
	}
}

func TestSpawnGivesACommandLineRepoAWorktreeAndItsGitDir(t *testing.T) {
	denHome, repo := denTest(t)
	hotfix := filepath.Join(t.TempDir(), "hotfix")
	createRepo(t, hotfix)
	f, d := fakeDeps()

	if err := Spawn(context.Background(), denHome,
		Options{Nest: "api", Worktree: "feat", Repos: []string{hotfix}}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := workspacesOf(callStartingWith(f, "create"))
	expected := []string{
		filepath.Join(denHome, "worktrees", "feat", "hotfix"),
		filepath.Join(denHome, "worktrees", "feat", filepath.Base(repo)),
		filepath.Join(hotfix, ".git"),
		filepath.Join(repo, ".git"),
		filepath.Join(denHome, "agents", "claude"),
	}
	if !slices.Equal(got, expected) {
		t.Errorf("workspaces = %v, expected %v — a repo given on the command line gets the "+
			"SAME treatment as a declared one: worktree, then its common git dir", got, expected)
	}
}
```

The expected paths are exact, not approximate: `writeConfig` (`internal/spawn/spawn_test.go:87`) writes `worktree_layout: central` and `worktree_root: <denHome>/worktrees`, and `worktree.Path` (`internal/worktree/worktree.go:131`) joins `root/<wt>/<basename>` in that layout. `denTest`'s repo basename is `api`.

- [ ] **Step 2: Run the tests and verify they fail**

Run: `go test ./internal/spawn/ -run 'TestSpawnRefusesANonGit|TestSpawnGivesACommandLineRepo' -count=1`
Expected: FAIL. The first fails with git's raw `fatal: not a git repository` (or a passing spawn), and a worktree for the nest's own repo exists.

- [ ] **Step 3: Probe git-ness at step 2**

In `internal/spawn/spawn.go`, immediately AFTER the existence loop of step 2 and BEFORE the `ssh.dir` check, insert:

```go
	// 2bis. Under -w, git-ness is decided HERE, before any worktree exists.
	//
	// worktree.Ensure only os.Stat's its repo (worktree.checkRepo): a non-git
	// directory is not caught until `git worktree add` fails, at step 3, AFTER
	// the worktrees of the repos ahead of it were created — one orphaned
	// worktree per repo, left for the user to clean up by hand. That is the
	// regression this function's ordering exists to prevent, and it predates
	// ad-hoc repos: a declared `repos:` entry pointing at a non-git directory
	// has it too. Positionals just made it reachable in one keystroke.
	//
	// CommonGitDir is a pure read AND is exactly the value step 3 needs, so the
	// result is kept and reused there rather than asked of git twice.
	//
	// Keyed by repo.Path rather than by rank: two entries can name the same
	// repository (a clone and one of its worktrees), the case step 3 already
	// dedups on gitDirs. Keyed by path, the alias falls on the same probe and
	// the reuse does not reintroduce the call it removes.
	commonDirs := make(map[string]string, len(r.Repos))
	if o.Worktree != "" {
		for _, repo := range r.Repos {
			if _, known := commonDirs[repo.Path]; known {
				continue
			}
			commonDir, err := worktree.CommonGitDir(ctx, d.Git, repo.Path)
			if err != nil {
				return fmt.Errorf(
					"%w — `-w` propagates a worktree to every repo of the spawn, and %s is not a "+
						"git repository: drop `-w`, or drop that path", err, repo.Path)
			}
			commonDirs[repo.Path] = commonDir
		}
	}
```

- [ ] **Step 4: Reuse the probe at step 3**

In `internal/spawn/spawn.go`, inside the worktree loop of step 3, replace the `worktree.CommonGitDir` call:

```go
			// Read from the step-2 probe, never asked again: git already
			// answered this, and asking twice would let the two answers differ
			// under a concurrent checkout.
			commonDir := commonDirs[repo.Path]
			if !slices.Contains(gitDirs, commonDir) {
				gitDirs = append(gitDirs, commonDir)
			}
```

The `commonDir, err := worktree.CommonGitDir(...)` line and its `if err != nil` block are deleted. Keep the long comment above them — it explains why the common git dir is mounted at all, and why writable.

- [ ] **Step 5: Run the tests and verify they pass**

Run: `go test ./internal/spawn/ ./internal/worktree/ -count=1`
Expected: PASS.

- [ ] **Step 6: Lint and commit**

```bash
gofmt -w internal/spawn/
make lint && go test ./... -count=1
git add internal/spawn/
git commit -m "fix(spawn): la git-ité est jugée avant le premier worktree

worktree.Ensure ne fait qu'un os.Stat: un dossier non-git n'était découvert
qu'au \`git worktree add\`, APRÈS que les worktrees des repos précédents aient
été créés — un orphelin par repo, à nettoyer à la main. Le bug précède les
repos à la volée; les positionnels le rendent atteignable en une frappe.
CommonGitDir sonde au §2 et son résultat est réutilisé au §3, clé sur le
chemin pour que deux entrées visant le même dépôt retombent sur une sonde."
```

---

### Task 5: the attach branch warns about both halves

**Files:**
- Modify: `internal/spawn/spawn.go` (add `reportUnmountedRepos`, call it in the live branch)
- Test: `internal/spawn/spawn_test.go`

**Interfaces:**
- Consumes: `sbx.Sandbox.Workspaces []string`, `sbx.Sandbox.Workdir() string`.
- Produces: `reportUnmountedRepos(out io.Writer, sandboxName, workdir string, mounted, expected []string)` — unexported.

**Why both halves:** positionals put the first one at `Workspaces[0]`, so `den api ~/dev/hotfix` promises "I am going to work in hotfix". On a live sandbox neither the mount nor the start directory can move — `sbx create` takes workspaces as positionals and den reapplies nothing to a running VM. Naming only the mount leaves the user expecting to at least land there. Warn, never refuse: same doctrine as `reportDrift`.

---

- [ ] **Step 1: Write the failing tests**

Append to `internal/spawn/spawn_test.go`. The live-sandbox JSON below is the exact shape the drift tests already use (`internal/spawn/spawn_test.go:282`) — there is no helper wrapping it, the literal is written inline there too.

```go
func TestSpawnWarnsThatALiveSandboxMountsNeitherTheNewRepoNorMovesTheShell(t *testing.T) {
	denHome, repo := denTest(t)
	hotfix := filepath.Join(t.TempDir(), "hotfix")
	createRepo(t, hotfix)

	// The sandbox is live, created WITHOUT hotfix: exactly the day-2 case.
	f, d := fakeDeps()
	f.Responses["ls --json"] = sbx.Response{Output: []byte(
		`{"sandboxes":[{"name":"api","status":"running","workspaces":["` + repo + `"]}]}`)}
	var out bytes.Buffer
	d.Out = &out

	if err := Spawn(context.Background(), denHome,
		Options{Nest: "api", Repos: []string{hotfix}, Detach: true}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if callStartingWith(f, "create") != nil {
		t.Fatal("a live sandbox must be attached, never re-created")
	}
	log := out.String()
	if !strings.Contains(log, hotfix) {
		t.Errorf("log = %q, expected it to name the repo that is not mounted", log)
	}
	if !strings.Contains(log, repo) {
		t.Errorf("log = %q, expected it to name the directory the shell starts in instead — "+
			"naming only the mount leaves the user expecting to at least land in what they typed",
			log)
	}
	if !strings.Contains(log, "den rm api") {
		t.Errorf("log = %q, expected the remedy", log)
	}
}

func TestSpawnDoesNotWarnWhenTheLiveSandboxMountsEverything(t *testing.T) {
	// A permanent warning stops being read: silence is the contract when the
	// VM already carries what was asked for.
	denHome, repo := denTest(t)
	f, d := fakeDeps()
	f.Responses["ls --json"] = sbx.Response{Output: []byte(
		`{"sandboxes":[{"name":"api","status":"running","workspaces":["` + repo + `"]}]}`)}
	var out bytes.Buffer
	d.Out = &out

	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Detach: true}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out.String(), "does not mount") {
		t.Errorf("log = %q, expected no unmounted-repo warning", out.String())
	}
}

func TestSpawnTreatsTwoDifferentPositionalSetsAsTheSameSandbox(t *testing.T) {
	// Decision 7 of the spec: positionals are NOT part of the identity.
	// `den scratch ~/dev/a` and `den scratch ~/dev/b` both name the sandbox
	// "scratch"; the second attaches the first and mounts nothing new. This is
	// the likeliest way to get bitten by a scratch nest, and the warning is the
	// only signal.
	denHome, _ := denTest(t)
	write(t, filepath.Join(denHome, "nests", "scratch.yaml"), "stack: devx\n")
	a := filepath.Join(t.TempDir(), "a")
	b := filepath.Join(t.TempDir(), "b")
	createRepo(t, a)
	createRepo(t, b)

	f, d := fakeDeps()
	f.Responses["ls --json"] = sbx.Response{Output: []byte(
		`{"sandboxes":[{"name":"scratch","status":"running","workspaces":["` + a + `"]}]}`)}
	var out bytes.Buffer
	d.Out = &out

	if err := Spawn(context.Background(), denHome,
		Options{Nest: "scratch", Repos: []string{b}, Detach: true}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callStartingWith(f, "create") != nil {
		t.Fatal("a second sandbox was created: positionals do not compose the identity, " +
			"which is <nest>[.<worktree>] and nothing else")
	}
	if !strings.Contains(out.String(), b) {
		t.Errorf("log = %q, expected it to name %s, the repo that is not mounted", out.String(), b)
	}
}
```

- [ ] **Step 2: Run the tests and verify they fail**

Run: `go test ./internal/spawn/ -run 'TestSpawnWarnsThatALiveSandbox|TestSpawnTreatsTwoDifferent' -count=1`
Expected: FAIL — the log names neither `hotfix` nor the start directory.

- [ ] **Step 3: Write `reportUnmountedRepos`**

In `internal/spawn/spawn.go`, immediately after `reportMissingGitDirs`:

```go
// reportUnmountedRepos warns that the sandbox does not mount every repo this
// command asked for — and that the shell will not start in them either.
//
// BOTH halves, deliberately. Positionals put the first repo at Workspaces[0],
// so `den api ~/dev/hotfix` promises "I have come to work in hotfix". On a live
// sandbox neither promise can be kept: `sbx create` takes workspaces as
// positionals, and den reapplies NOTHING to a running VM. Naming only the mount
// would leave the user expecting to at least land where they typed.
//
// Warn, never refuse, and never recreate: the same doctrine as reportDrift.
// Refusing would break a `den <nest>` that worked yesterday over a path added
// today, and recreating would destroy work in progress in the VM.
//
// This also covers a case that was silent before: a `repos:` entry added to the
// yaml after the sandbox was created. The mixin drift comparison cannot see it —
// workspaces are argv, not mixin content.
//
// workdir may be empty (a VM that mounts nothing): the line is then omitted
// rather than naming a directory den would be inventing.
func reportUnmountedRepos(out io.Writer, sandboxName, workdir string, mounted, expected []string) {
	present := make(map[string]bool, len(mounted))
	for _, w := range mounted {
		// The ":ro" suffix is a mount option, not part of the path — same
		// treatment as Sandbox.Workdir.
		present[strings.TrimSuffix(w, ":ro")] = true
	}
	var missing []string
	for _, p := range expected {
		if !present[p] {
			missing = append(missing, p)
		}
	}
	if len(missing) == 0 {
		return // a permanent warning stops being read
	}
	fmt.Fprintf(out,
		"warning: sandbox %s does not mount every repo this command asked for — mounts and "+
			"start directory are both fixed at create time:\n", sandboxName)
	for _, p := range missing {
		fmt.Fprintf(out, "  - %s is not mounted\n", p)
	}
	if workdir != "" {
		fmt.Fprintf(out, "  the shell starts in %s, as it did at create time\n", workdir)
	}
	fmt.Fprintf(out, "  `den rm %s` then relaunch to change either.\n", sandboxName)
}
```

- [ ] **Step 4: Call it in the live branch**

In `internal/spawn/spawn.go`, in the `if live != nil` branch, right after the existing `reportMissingGitDirs` call:

```go
		// The repos are the FIRST len(r.Repos) workspaces — step 3 appends
		// exactly one per repo, before the git dirs, the agent profile and
		// ssh.dir. Slicing there rather than recomputing keeps the comparison on
		// the paths the VM would actually have received, worktrees included.
		reportUnmountedRepos(d.Out, sandboxName, workdir, live.Workspaces, workspaces[:len(r.Repos)])
```

`workdir` has already been reassigned to `live.Workdir()` three lines above; leave that assignment where it is.

- [ ] **Step 5: Run the tests and verify they pass**

Run: `go test ./internal/spawn/ -count=1`
Expected: PASS.

- [ ] **Step 6: Lint and commit**

```bash
gofmt -w internal/spawn/
make lint && go test ./... -count=1
git add internal/spawn/
git commit -m "feat(spawn): l'attach dit les deux moitiés qu'il ne peut pas tenir

Un positionnel place son repo en Workspaces[0], donc \`den api ~/dev/hotfix\`
promet \"je viens travailler dans hotfix\". Sur une VM vivante ni le montage ni
le répertoire de départ ne bougent — sbx create prend les workspaces en
positionnels et den ne réapplique rien. Ne nommer que le montage laisserait
croire qu'on va au moins atterrir là. Avertir, jamais refuser (doctrine de
reportDrift). Couvre au passage un \`repos:\` ajouté au yaml après le create,
que la drift de mixin ne peut pas voir: les workspaces sont de l'argv."
```

---

### Task 6: the command line opens — variadic positionals

**Files:**
- Modify: `internal/cli/spawn.go` (`root.Use`, `root.Args`, wiring `args[1:]`)
- Modify: `internal/cli/root.go` (add `atLeastOneArg`)
- Modify: `internal/cli/nest.go` (`nest show` takes paths; `writeResolution` marks their origin)
- Test: `internal/cli/spawn_test.go`, `internal/cli/nest_test.go`

**Interfaces:**
- Consumes: `spawn.Options{Repos []string}`, `nest.Options{Repos []string, Cwd string}`.
- Produces: `atLeastOneArg cobra.PositionalArgs` in `internal/cli/root.go`.

---

- [ ] **Step 1: Write the failing tests**

Append to `internal/cli/spawn_test.go`. `runSpawn(t, home, deps, args...)` builds a bare root through `configureSpawn` and returns its output and error; `denHomeSpawnable(t)` writes a den home whose nest `api` declares one existing (non-git) repo and no `egress:`; `fakeSpawnDeps()` returns the `*sbx.Fake` alongside the `spawn.Deps`. `runFullRoot(t, home, args...)` runs the REAL command tree — `TestATypoOnASubcommandIsSuggested` uses it, because the suggestion reads off `root.Commands()` and a bare root would make its absence true by construction.

```go
func TestPositionalsReachSpawnOptionsAsRepos(t *testing.T) {
	// Same shape as TestFlagsReachSpawnOptions: an INVALID value proves the
	// wiring, because an unwired argument is silent — the paths would simply
	// vanish and the spawn would succeed mounting nothing extra.
	f, d := fakeSpawnDeps()
	missing := filepath.Join(t.TempDir(), "gone")

	_, err := runSpawn(t, denHomeSpawnable(t), d, "api", missing)
	if err == nil {
		t.Fatal("a positional naming a path that does not exist must fail the spawn")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("positionals do not reach spawn.Options (expected %q); got: %v", missing, err)
	}
	if !strings.Contains(err.Error(), "command line") {
		t.Errorf("error = %q, expected it to name the command line as the place to fix", err)
	}
	if len(f.Calls) != 0 {
		t.Errorf("no sbx call must have happened; calls: %v", f.Calls)
	}
}

func TestSeveralPositionalsAllReachSpawnOptions(t *testing.T) {
	// The SECOND path is the invalid one: with `args[1:2]` instead of
	// `args[1:]`, or with only the first positional read, this passes silently.
	f, d := fakeSpawnDeps()
	missing := filepath.Join(t.TempDir(), "gone")
	present := t.TempDir()

	_, err := runSpawn(t, denHomeSpawnable(t), d, "api", present, missing)
	if err == nil {
		t.Fatal("expected the spawn to fail on the second positional")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("only the first positional seems to reach spawn.Options; got: %v", err)
	}
	if len(f.Calls) != 0 {
		t.Errorf("no sbx call must have happened; calls: %v", f.Calls)
	}
}

func TestATypoOnASubcommandIsStillSuggestedWithPositionals(t *testing.T) {
	// `den doctr /dev/a` must keep suggesting `den doctor`. The suggestion is
	// pinned to nest.NestNotFoundError, not to the argument count — and this
	// test is what keeps that true now that extra arguments are legal.
	_, _, err := runFullRoot(t, denHomeSpawnable(t), "doctr", "/dev/a")
	if err == nil {
		t.Fatal("expected a failure: there is no nest named doctr")
	}
	if !strings.Contains(err.Error(), "den doctor") {
		t.Errorf("error = %q, expected it to suggest `den doctor`", err)
	}
}
```

Append to `internal/cli/nest_test.go`. `testDenHome(t)` writes a den home (its nest `api` declares `/dev/api`) and pins `DEN_HOME`; `run(t, args...)` executes the real root against it.

```go
func TestNestShowResolvesCommandLineRepos(t *testing.T) {
	testDenHome(t)
	out, err := run(t, "nest", "show", "api", "/dev/hotfix")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// `den nest show` is the dry-run of a spawn: it must list what would be
	// mounted, and say which entries came from the command line — "required"
	// and "optional" describe a `repos:` declaration, which this is not.
	if !strings.Contains(out, "/dev/hotfix (command line)") {
		t.Errorf("output = %q, expected the ad-hoc repo listed with its origin", out)
	}
	if !strings.Contains(out, "/dev/api (required)") {
		t.Errorf("output = %q, expected the declared repo to keep its own wording", out)
	}
}
```

- [ ] **Step 2: Run the tests and verify they fail**

Run: `go test ./internal/cli/ -run 'TestPositionalsReach|TestSeveralPositionals|TestATypoOnASubcommandIsStillSuggested|TestNestShowResolves' -count=1`
Expected: FAIL — the spawn tests fail because `atMostOneArg` rejects the extra argument with "at most one argument expected", and `nest show` fails the same way through `exactlyOneArg`.

- [ ] **Step 3: Add `atLeastOneArg`**

In `internal/cli/root.go`, in the validators var block:

```go
var (
	noArgs        = argsBetween(0, 0)
	exactlyOneArg = argsBetween(1, 1)
	atMostOneArg  = argsBetween(0, 1)
	// atLeastOneArg has no upper bound because the arguments past the first are
	// repos, and nothing caps how many a spawn may mount. argsBetween's
	// "too many" branch is therefore unreachable through it — only the
	// "one argument expected: none received" wording is ever produced.
	atLeastOneArg = argsBetween(1, math.MaxInt)
)
```

Add `"math"` to `internal/cli/root.go`'s imports.

- [ ] **Step 4: Open the root's argument list**

In `internal/cli/spawn.go`, inside `configureSpawn`:

```go
	root.Use = "den <nest> [repo...]"
	// ArbitraryArgs, not a bounded validator: args[0] is the nest and args[1:]
	// are repos, of which there is no reason to allow only N.
	//
	// What matters is that Args stays NON-NIL: setting it at all is what
	// disables cobra's legacyArgs ("unknown command"), and that switch is what
	// makes a nest name acceptable in first position. Going back to nil here
	// would break the whole `den <nest>` form.
	root.Args = cobra.ArbitraryArgs
```

and in `RunE`, right after `o.Nest = args[0]`:

```go
		// Raw: nest.Resolve expands the tilde and absolutizes against the
		// working directory, which internal/spawn reads. Doing it here would put
		// path resolution on the cobra side of the boundary, where no test of
		// the cascade could reach it.
		o.Repos = args[1:]
```

- [ ] **Step 5: Let `den nest show` take the same paths**

In `internal/cli/nest.go`, in `newNestShowCmd`:

```go
		Use:   "show <nest> [repo...]",
		Short: "Show a fully resolved nest",
		Args:  atLeastOneArg,
```

and in its `RunE`, after `n, err := nest.LoadNest(home, args[0])` succeeds:

```go
			// The dry-run of `den <nest> [repo...]`: same resolution, no side
			// effect. Reading the working directory here mirrors internal/spawn
			// — internal/nest never reads it itself.
			opts.Repos = args[1:]
			if len(opts.Repos) > 0 {
				if opts.Cwd, err = os.Getwd(); err != nil {
					return fmt.Errorf(
						"reading the working directory, needed to resolve the repos given on "+
							"the command line: %w", err)
				}
			}
```

Add `"os"` to `internal/cli/nest.go`'s imports.

- [ ] **Step 6: Mark the origin in `writeResolution`**

In `internal/cli/nest.go`, in `writeResolution`'s repo loop:

```go
	fmt.Fprintln(w, "repos:")
	for _, repo := range r.Repos {
		// A repo given on the command line is neither required nor optional —
		// those words describe a `repos:` declaration and --without/--only,
		// which never address it. Naming its origin instead is what makes this
		// listing a usable dry-run.
		status := "required"
		switch {
		case repo.AdHoc:
			status = "command line"
		case repo.Optional:
			status = "optional"
		}
		fmt.Fprintf(w, "  - %s (%s)\n", repo.Path, status)
	}
```

- [ ] **Step 7: Run the whole suite and verify it passes**

Run: `go test ./... -count=1`
Expected: PASS. If a `den nest show` golden under `internal/cli/testdata/` covers the repo lines, update it BY HAND — there is no `-update` flag.

- [ ] **Step 8: Lint and commit**

```bash
gofmt -w internal/cli/
make lint && go test ./... -count=1
git add internal/cli/
git commit -m "feat(cli): \`den <nest> [repo...]\` — les positionnels s'ouvrent

La surface arrive en dernier, après que tous les refus et l'avertissement
qu'elle réclame sont en place. Args reste NON-NIL (c'est lui qui désactive le
legacyArgs de cobra, donc la forme \`den <nest>\` elle-même), les chemins
passent bruts à nest.Resolve, et \`den nest show <nest> [repo...]\` devient le
dry-run, qui nomme l'origine de chaque repo."
```

---

### Task 7: documentation

**Files:**
- Modify: `docs/superpowers/specs/2026-07-27-den-cli-design.md` (§4.3, §5, §6)
- Modify: `README.md`

**Interfaces:** none.

**Why this task is not optional:** `CLAUDE.md` records that README and spec diverging is now a bug in one of them, not a phase. Both move in the same change.

---

- [ ] **Step 1: Update the CLI spec's §5 command table**

In `docs/superpowers/specs/2026-07-27-den-cli-design.md`, in the §5 table, the spawn row's command cell becomes:

```
`den <nest> [path...] [-w <wt>] [--without r] [--only r] [-i] [--agent a] [--detach]`
```

and its role cell gains, after "**spawn-or-attach** + shell":

```
 ; les `path...` sont des repos montés à la volée, additifs aux `repos:` du nest et placés devant eux
```

The `den nest show` row becomes `` `den nest ls` / `den nest show <n> [path...]` ``.

- [ ] **Step 2: Update §4.3**

In the same file, immediately after the §4.3 yaml block, before "**Règles de fusion**", insert:

```markdown
**`repos:` est facultatif.** Un nest qui n'en déclare aucun reste un objet spawnable complet — il
porte sa stack, son egress, son env, ses ports et ses profils agents — et reçoit ses dépôts en
positionnels : `den scratch ~/dev/a ~/dev/b`. Les positionnels sont additifs et passent **devant**
les `repos:` déclarés, parce que `Workspaces[0]` décide du répertoire où démarre le shell attaché.
Ils n'entrent pas dans l'identité : `den scratch ~/dev/a` et `den scratch ~/dev/b` visent la même
sandbox `scratch`. Détail : `2026-08-04-adhoc-repos-design.md`.
```

- [ ] **Step 3: Update §6**

In the same file, in the §6 spawn data flow, at the repo-selection step, append:

```markdown
La sélection produit `[positionnels…] ++ selectRepos(déclarés)`, dédoublonnée par basename sur la
liste **fusionnée**. Sous `-w`, la git-ité de chaque repo est sondée à ce moment — avant tout effet
de bord — et le common git dir obtenu est réutilisé plus bas plutôt que redemandé à git.
```

- [ ] **Step 4: Update the README**

In `README.md`, in the command table, the spawn row's command cell becomes `den <nest> [path...]`, and add to its description: "les chemins supplémentaires sont montés à la volée".

Then, in the spawn section, add:

````markdown
### Monter un dépôt à la volée

Un dépôt n'a pas besoin d'être déclaré pour entrer dans la sandbox : les chemins qui suivent le nom
du nest sont montés comme des `repos:`, worktree compris.

```bash
den scratch ~/dev/a ~/dev/b     # un nest sans `repos:` — les deux dépôts viennent de la ligne
den api ~/dev/hotfix            # additif : les repos d'api PLUS hotfix
den scratch .                   # le répertoire courant
den api -w feat/x ~/dev/hotfix  # -w propage un worktree sur hotfix comme sur les repos d'api
den nest show scratch ~/dev/a   # ce qui serait monté, sans rien créer
```

Le premier dépôt de la ligne devient le répertoire où démarre le shell. Les montages sont figés à la
création de la sandbox : sur une sandbox déjà vivante, `den` prévient qu'il ne monte pas le nouveau
chemin et n'y démarre pas non plus — `den rm <nom>` puis relance pour changer l'un ou l'autre.

`:ro` n'est pas accepté : un dépôt monté à la volée l'est en écriture, comme un `repos:` déclaré.
````

- [ ] **Step 5: Verify nothing else claims the old surface**

```bash
grep -rn "atMostOneArg\|den <nest>" README.md docs/ --include='*.md'
```

Every hit describing the spawn command's argument list must show `[path...]`. Fix any that do not.

- [ ] **Step 6: Commit**

```bash
git add README.md docs/
git commit -m "docs: \`den <nest> [path...]\` dans la spec CLI et le README

README et spec divergents sont un bug, pas une phase (CLAUDE.md): §4.3 dit que
\`repos:\` est facultatif, §5 porte la nouvelle ligne de commande, §6 décrit la
fusion et le pré-vol git."
```

- [ ] **Step 7: Full verification**

```bash
make lint && make typecheck && make test
```

Expected: everything green.

---

## Ce que ce plan ne fait PAS

Repris de la section « Hors scope » de la spec, pour qu'aucune tâche ne dérive :

- `:ro` sur un positionnel — refusé avec un message, pas implémenté ;
- un `den run` sans fichier nest — amendement du §2, à chiffrer séparément ;
- l'adressage des positionnels par `--without` / `--only` ;
- `den nest add-repo` ou toute écriture dans la config : le point de la feature est de ne PAS écrire.
