# Team Stack Sources Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** den installs, updates and validates team-shared stack/nest repositories (git clones under `~/.den/sources/<n>/`), addressed as `<source>:<name>`, with personal repo-key mapping and a CI-friendly `den lint`.

**Architecture:** A *source* is a git clone with the den-home partial layout (`stacks/ lib/ kits/ nests/`), managed by a new `internal/source` package through the existing `worktree.Git` interface. A new `internal/lint` package validates any checkout by reusing `config.LoadStacks` / `nest.ListNests` against an arbitrary root. Reference plumbing (`corp:devx`) is resolved at the CLI/spawn boundary into `(root, bare name)` pairs so `nest.Resolve` and `build` keep working on bare names within one root.

**Tech Stack:** Go, cobra, yaml.v3 (strict), git CLI behind `worktree.Git`.

**Spec:** `docs/superpowers/specs/2026-08-04-stack-sources-design.md` — read it before starting any task.

## Global Constraints

- No test opens a socket, calls `t.Parallel()`, or depends on the network. Git tests use `file://` remotes built in `t.TempDir()`.
- Every package whose tests run real git calls `worktree.NeutralizeGitEnvironment()` in `TestMain` (copy the shape of `internal/cli/main_test.go`).
- Strict YAML everywhere: new fields go through `config.DecodeYAMLStrict`, never a second decoder.
- Errors name the file to fix and the remedy. den refuses, never normalizes in silence.
- Comments follow house style: long "why" at decision sites, English only.
- Run `make test && make lint && make typecheck` before every commit.
- Branch: work happens on `feat/stack-sources` (already created, spec committed).
- Source name charset = sandbox component charset (`config.ValidateSandboxComponent`), kind string `"source"`.
- Inside a source, `stack:`/`parent:` references are BARE (no `:`); prefixed refs exist only on the personal side (CLI args, `defaults.stack`, local nests).

---

### Task 1: Source references and source-name validation (`internal/config`)

**Files:**
- Create: `internal/config/ref.go`
- Test: `internal/config/ref_test.go`

**Interfaces:**
- Produces: `config.SplitSourceRef(ref string) (source, name string)` — cuts on the FIRST `:`; `("", ref)` when none.
- Produces: `config.ValidateSourceName(name string) error` — wraps `ValidateSandboxComponent("source", name)` plus `ValidateName("source", name)`.

- [ ] **Step 1: Write the failing tests**

```go
// internal/config/ref_test.go
package config

import "testing"

func TestSplitSourceRef(t *testing.T) {
	tests := []struct {
		ref, source, name string
	}{
		{"corp:devx", "corp", "devx"},
		{"devx", "", "devx"},
		{"corp:a:b", "corp", "a:b"}, // first colon only; the remainder fails validation downstream
		{":devx", "", "devx"},       // empty source = local, same as no prefix
		{"corp:", "corp", ""},       // empty name; caller's name validation refuses it
		{"", "", ""},
	}
	for _, tt := range tests {
		s, n := SplitSourceRef(tt.ref)
		if s != tt.source || n != tt.name {
			t.Errorf("SplitSourceRef(%q) = (%q, %q), want (%q, %q)", tt.ref, s, n, tt.source, tt.name)
		}
	}
}

func TestValidateSourceName(t *testing.T) {
	if err := ValidateSourceName("corp"); err != nil {
		t.Errorf("ValidateSourceName(corp): %v", err)
	}
	for _, bad := range []string{"", "co.rp", "-corp", "co/rp", "..", "co:rp"} {
		if err := ValidateSourceName(bad); err == nil {
			t.Errorf("ValidateSourceName(%q): expected an error", bad)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/ -run 'TestSplitSourceRef|TestValidateSourceName' -count=1`
Expected: FAIL (undefined: SplitSourceRef, ValidateSourceName)

- [ ] **Step 3: Implement**

```go
// internal/config/ref.go
package config

import "strings"

// SourceRefSeparator splits "<source>:<name>". ":" and not "/": a source
// object is NOT addressable as a relative path from the den home, and a
// path-looking name would suggest it is. A plain YAML scalar carries ":"
// unquoted as long as no space follows, so `stack: corp:devx` stays writable
// as-is (spec 2026-08-04 §2.3).
const SourceRefSeparator = ":"

// SplitSourceRef splits a reference on its FIRST separator. ("", ref) when
// there is none: a bare name is a local object. An empty source component
// (":devx") collapses to local rather than erroring here — validation of the
// PARTS belongs to the callers, which know whether they hold a source name, a
// stack name or a nest name and can say so in the message.
func SplitSourceRef(ref string) (source, name string) {
	before, after, found := strings.Cut(ref, SourceRefSeparator)
	if !found {
		return "", ref
	}
	return before, after
}

// ValidateSourceName rejects names that cannot designate a directory under
// <denHome>/sources/. Both guards, in ValidateName-first order like LoadNest:
// the path-escape intent ("../..") reads better than a charset complaint. The
// sandbox charset then applies because a source name becomes the PREFIX of
// flattened sandbox names ("corp:api" → sandbox "corp-api") — a character sbx
// refuses would only surface at spawn time, far from the `den source add`
// that accepted it.
func ValidateSourceName(name string) error {
	if err := ValidateName("source", name); err != nil {
		return err
	}
	return ValidateSandboxComponent("source", name)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/ -run 'TestSplitSourceRef|TestValidateSourceName' -count=1`
Expected: PASS

- [ ] **Step 5: Full checks, commit**

```bash
make test && make lint && make typecheck
git add internal/config/ref.go internal/config/ref_test.go
git commit -m "feat(config): source references — SplitSourceRef and ValidateSourceName"
```

---

### Task 2: Personal repo-key mapping in config.yaml (`Global.Repos`)

**Files:**
- Modify: `internal/config/config.go` (struct `Global`, `LoadGlobalUnvalidated`)
- Modify: `internal/config/validate.go` (`Validate`)
- Test: `internal/config/config_test.go`, `internal/config/validate_test.go`

**Interfaces:**
- Produces: `Global.Repos map[string]string` (yaml `repos:`), values tilde-expanded at load. Task 3's `nest.Resolve` consumes it.

- [ ] **Step 1: Write the failing tests**

Add to `internal/config/config_test.go` (follow the file's existing helpers for writing a config.yaml fixture — read the file first and reuse its minimal valid config text):

```go
func TestLoadGlobalRepoKeys(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, minimalConfig+`
repos:
  review-mgmt: ~/dev/review-mgmt
  front-app: /abs/front
`) // adapt: reuse THIS FILE's existing fixture helper and minimal config text
	g, err := LoadGlobal(home)
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if got := g.Repos["front-app"]; got != "/abs/front" {
		t.Errorf("front-app = %q", got)
	}
	if got := g.Repos["review-mgmt"]; strings.HasPrefix(got, "~") {
		t.Errorf("review-mgmt not expanded: %q", got)
	}
}

func TestValidateRepoKeyBlankPath(t *testing.T) {
	g := validGlobal() // adapt: reuse validate_test.go's existing valid-Global helper
	g.Repos = map[string]string{"api": "   "}
	errs := g.Validate()
	// One error naming repos.api: a blank mapping would expand to a relative
	// path and mount the wrong directory.
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "repos.api") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a repos.api error, got: %v", errs)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/ -run 'TestLoadGlobalRepoKeys|TestValidateRepoKeyBlankPath' -count=1`
Expected: FAIL (unknown field `repos` under strict decoding, then missing validation)

- [ ] **Step 3: Implement**

In `Global` add:

```go
	// Repos maps a repo KEY (used by team nests via `key:`, spec 2026-08-04
	// §2.4) to a path on THIS machine. Personal by design: it is the one part
	// of a shared nest that cannot travel.
	Repos map[string]string `yaml:"repos"`
```

In `LoadGlobalUnvalidated`, after the agents loop:

```go
	for key, p := range g.Repos {
		expanded, err := ExpandPath(p)
		if err != nil {
			return nil, fmt.Errorf("repos.%s: %w", key, err)
		}
		g.Repos[key] = expanded
	}
```

In `Validate`, after the defaults checks (iterate `slices.Sorted(maps.Keys(g.Repos))` for deterministic order):

```go
	for _, key := range slices.Sorted(maps.Keys(g.Repos)) {
		if strings.TrimSpace(g.Repos[key]) == "" {
			errs = append(errs, fmt.Errorf(
				"repos.%s: blank — this key is what a nest's `key:` resolves to; "+
					"set a real path, or remove the entry", key))
		}
	}
```

- [ ] **Step 4: Run tests, full checks, commit**

```bash
go test ./internal/config/ -count=1 && make test && make lint
git add internal/config/config.go internal/config/validate.go internal/config/config_test.go internal/config/validate_test.go
git commit -m "feat(config): personal repo-key mapping (repos:) in config.yaml"
```

---

### Task 3: `key:`/`url:` on nest repos, resolution in `nest.Resolve`

**Files:**
- Modify: `internal/nest/nest.go` (struct `Repo`, `LoadNest`)
- Modify: `internal/nest/resolve.go` (`Resolve`)
- Test: `internal/nest/nest_test.go`, `internal/nest/resolve_test.go`

**Interfaces:**
- Consumes: `Global.Repos` (Task 2).
- Produces: `Repo{Key, URL string}` yaml fields; `Repo.Name()` returns `Key` when set, else `filepath.Base(Path)`. After `Resolve`, every selected `Repo.Path` is filled (keys resolved) — `internal/spawn` keeps consuming `Resolved.Repos` unchanged.

- [ ] **Step 1: Write the failing tests**

In `internal/nest/nest_test.go` (reuse the file's existing nest-fixture helper):

```go
func TestLoadNestRepoKeyAndPathExclusive(t *testing.T) {
	home := writeNest(t, "n", `
stack: devx
repos:
  - { path: /a, key: api }
`) // adapt to the file's fixture helper
	_, err := LoadNest(home, "n")
	if err == nil || !strings.Contains(err.Error(), "path") || !strings.Contains(err.Error(), "key") {
		t.Fatalf("expected a path/key exclusivity refusal, got: %v", err)
	}
}

func TestLoadNestURLRequiresKey(t *testing.T) {
	home := writeNest(t, "n", `
stack: devx
repos:
  - { path: /a, url: git@x:y.git }
`)
	_, err := LoadNest(home, "n")
	if err == nil || !strings.Contains(err.Error(), "url") {
		t.Fatalf("expected a url-without-key refusal, got: %v", err)
	}
}

func TestRepoNameFromKey(t *testing.T) {
	r := Repo{Key: "api"}
	if r.Name() != "api" {
		t.Errorf("Name() = %q, want api", r.Name())
	}
}
```

In `internal/nest/resolve_test.go` (reuse its existing `Resolve` fixtures):

```go
func TestResolveRepoKeys(t *testing.T) {
	g := validGlobalFixture() // adapt
	g.Repos = map[string]string{"api": "/home/u/dev/api"}
	n := &Nest{Name: "n", Stack: "devx", Repos: []Repo{{Key: "api"}}}
	r, err := Resolve(denHome, g, stacksFixture(), n, Options{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.Repos[0].Path != "/home/u/dev/api" {
		t.Errorf("Path = %q", r.Repos[0].Path)
	}
}

func TestResolveRepoKeyMissing(t *testing.T) {
	g := validGlobalFixture()
	n := &Nest{Name: "n", Stack: "devx",
		Repos: []Repo{{Key: "api", URL: "git@gitlab.corp:a/api.git"}}}
	_, err := Resolve(denHome, g, stacksFixture(), n, Options{})
	if err == nil {
		t.Fatal("expected a refusal")
	}
	for _, want := range []string{"api", "repos:", "config.yaml", "git@gitlab.corp:a/api.git"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/nest/ -run 'TestLoadNestRepoKey|TestLoadNestURL|TestRepoNameFromKey|TestResolveRepoKey' -count=1`
Expected: FAIL (unknown field `key` under strict decoding, undefined fields)

- [ ] **Step 3: Implement**

`Repo` becomes:

```go
// Repo is a repository co-mounted in the sandbox. Exactly one of Path and Key
// is set (LoadNest refuses both and neither-with-url): Path is a machine
// path, Key is an indirection through the personal `repos:` mapping of
// config.yaml — the one thing that lets a TEAM nest travel between machines
// (spec 2026-08-04 §2.4).
type Repo struct {
	Path     string `yaml:"path"`
	Key      string `yaml:"key"`
	// URL is INDICATIVE only: it enriches the unmapped-key refusal with the
	// clone command the user probably wants. den never clones a work repo.
	URL      string `yaml:"url"`
	Optional bool   `yaml:"optional"`
}

// Name is the repo's short name, used by --without/--only and as the worktree
// directory component. The KEY when set — it is the shareable identity — else
// the path basename.
func (r Repo) Name() string {
	if r.Key != "" {
		return r.Key
	}
	return filepath.Base(r.Path)
}
```

In `LoadNest`, replace the repos expansion loop:

```go
	for i, r := range n.Repos {
		switch {
		case r.Path != "" && r.Key != "":
			return nil, fmt.Errorf(
				"nest %q: repo entry %d sets both `path:` and `key:` — a repo has ONE identity: "+
					"`path:` is a machine path, `key:` resolves through `repos:` in config.yaml. "+
					"Two identities is a contradiction, not a precedence den can arbitrate", name, i)
		case r.Path == "" && r.Key == "":
			return nil, fmt.Errorf(
				"nest %q: repo entry %d sets neither `path:` nor `key:` — den has nothing to mount", name, i)
		case r.URL != "" && r.Key == "":
			return nil, fmt.Errorf(
				"nest %q: repo entry %d sets `url:` without `key:` — url exists only to enrich the "+
					"unmapped-key message; on a `path:` entry it would never be read", name, i)
		}
		if r.Path != "" {
			if n.Repos[i].Path, err = config.ExpandPath(r.Path); err != nil {
				return nil, fmt.Errorf("nest %q, repo %q: %w", n.Name, r.Path, err)
			}
		}
	}
```

Note: `n.Name` is only assigned AFTER this loop today — keep using the `name`
parameter in these messages, as the existing code does for LoadStack.

In `Resolve`, before `selectRepos`, resolve keys (new unexported func in resolve.go):

```go
// resolveRepoKeys fills the Path of every key-typed repo from the personal
// mapping. Refusal BEFORE any side effect, naming the exact file and line to
// add — and the clone command when the nest declared one (spec 2026-08-04
// §2.4). denHome locates config.yaml through GlobalPath: the message and the
// reader must never disagree on where that file lives.
func resolveRepoKeys(denHome string, mapping map[string]string, repos []Repo) ([]Repo, error) {
	out := slices.Clone(repos)
	for i, r := range out {
		if r.Key == "" {
			continue
		}
		path, ok := mapping[r.Key]
		if !ok {
			hint := ""
			if r.URL != "" {
				hint = fmt.Sprintf(" (clone: %s)", r.URL)
			}
			return nil, fmt.Errorf(
				"repo key %q is not mapped on this machine — add `%s: <local path>` under `repos:` "+
					"in %s%s", r.Key, r.Key, config.GlobalPath(denHome), hint)
		}
		out[i].Path = path
	}
	return out, nil
}
```

Wire into `Resolve` between `resolveAgent` and `selectRepos`:

```go
	repos, err := resolveRepoKeys(denHome, g.Repos, n.Repos)
	if err != nil {
		return nil, fmt.Errorf("nest %q: %w", n.Name, err)
	}
	repos, err = selectRepos(repos, o.Without, o.Only)
```

(`selectRepos`'s current call reads `n.Repos` — it now reads the resolved slice.)

- [ ] **Step 4: Run tests, full checks, commit**

```bash
go test ./internal/nest/ -count=1 && make test && make lint
git add internal/nest/
git commit -m "feat(nest): shareable repos — key:/url: entries resolved through the personal repos: mapping"
```

---

### Task 4: `internal/lint` — checkout validation

**Files:**
- Create: `internal/lint/lint.go`
- Test: `internal/lint/lint_test.go` (+ fixtures built in `t.TempDir()`, not testdata: the checks need absolute roots)

**Interfaces:**
- Produces: `lint.Run(root string) []error` — empty slice means valid. Consumed by Tasks 6 (source add/update) and 8 (`den lint`).

- [ ] **Step 1: Write the failing tests**

```go
// internal/lint/lint_test.go
package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTree materializes a checkout: keys are relative paths, values file
// contents. Directories are implied.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const validStack = "image: devx:v1\nbase: claude\n"

func TestRunValidCheckout(t *testing.T) {
	root := writeTree(t, map[string]string{
		"stacks/devx/stack.yaml": validStack,
		"nests/api.yaml":         "stack: devx\nrepos:\n  - { key: api }\n",
	})
	if errs := Run(root); len(errs) != 0 {
		t.Fatalf("expected no errors, got: %v", errs)
	}
}

func TestRunMissingRoot(t *testing.T) {
	errs := Run(filepath.Join(t.TempDir(), "absent"))
	if len(errs) == 0 {
		t.Fatal("expected an error for a missing root")
	}
}

func TestRunBrokenStackYAML(t *testing.T) {
	root := writeTree(t, map[string]string{
		"stacks/devx/stack.yaml": "image: devx:v1\nbase: claude\negres: []\n", // typo → strict decode error
	})
	errs := Run(root)
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "egres") {
		t.Fatalf("expected the strict-YAML error, got: %v", errs)
	}
}

func TestRunParentCycle(t *testing.T) {
	root := writeTree(t, map[string]string{
		"stacks/a/stack.yaml":       "image: a:v1\nparent: b\nprovision:\n  steps: [./provision/x.sh]\n",
		"stacks/a/provision/x.sh":   "true\n",
		"stacks/b/stack.yaml":       "image: b:v1\nparent: a\nprovision:\n  steps: [./provision/x.sh]\n",
		"stacks/b/provision/x.sh":   "true\n",
	})
	errs := Run(root)
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "cycle") {
		t.Fatalf("expected a cycle error, got: %v", errs)
	}
}

func TestRunPathEscapesRoot(t *testing.T) {
	root := writeTree(t, map[string]string{
		"stacks/devx/stack.yaml": "image: devx:v1\nbase: claude\nprovision:\n  includes: [../../../outside.sh]\n  steps: [./provision/x.sh]\n",
		"stacks/devx/provision/x.sh": "true\n",
	})
	// The escaping file EXISTS, to prove the refusal is about confinement,
	// not existence.
	if err := os.WriteFile(filepath.Join(root, "..", "outside.sh"), []byte("true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	errs := Run(root)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "escapes") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a confinement error, got: %v", errs)
	}
}

func TestRunMissingProvisionFile(t *testing.T) {
	root := writeTree(t, map[string]string{
		"stacks/devx/stack.yaml": "image: devx:v1\nbase: claude\nprovision:\n  steps: [./provision/absent.sh]\n",
	})
	errs := Run(root)
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "absent.sh") {
		t.Fatalf("expected a missing-file error, got: %v", errs)
	}
}

func TestRunPrefixedRefInsideSource(t *testing.T) {
	root := writeTree(t, map[string]string{
		"stacks/devx/stack.yaml": validStack,
		"nests/api.yaml":         "stack: corp:devx\nrepos:\n  - { key: api }\n",
	})
	errs := Run(root)
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "bare") {
		t.Fatalf("expected a bare-reference error, got: %v", errs)
	}
}

func TestRunUnknownStackRef(t *testing.T) {
	root := writeTree(t, map[string]string{
		"stacks/devx/stack.yaml": validStack,
		"nests/api.yaml":         "stack: nope\nrepos:\n  - { key: api }\n",
	})
	errs := Run(root)
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "nope") {
		t.Fatalf("expected an unknown-stack error, got: %v", errs)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/lint/ -count=1`
Expected: FAIL (package does not exist)

- [ ] **Step 3: Implement**

```go
// Package lint validates a stacks/nests checkout — a team source repo or a
// clone of one — without touching git, sbx or the network. ONE implementation,
// three consumers (spec 2026-08-04 §5): the team repo's CI (`den lint`),
// `den source add` (post-clone) and `den source update` (pre-fast-forward,
// the fail-closed gate).
package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
)

// Run validates the checkout rooted at root and returns EVERY finding, in a
// deterministic order — a CI log must be reproducible, and showing one error
// per push when a repo has five is five pushes instead of one.
//
// An empty result means valid. The checks reuse the production loaders
// (config.LoadStacks, nest.ListNests) so lint can never accept what a spawn
// would later refuse — one judge, not two.
func Run(root string) []error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return []error{fmt.Errorf("resolving %q: %w", root, err)}
	}
	root = abs
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return []error{fmt.Errorf("%s: not a directory — `den lint` validates a checkout root", root)}
	}

	var errs []error

	stacks, err := config.LoadStacks(root)
	if err != nil {
		return append(errs, err) // structural: nothing more can be judged
	}
	for _, b := range stacks.Broken {
		errs = append(errs, fmt.Errorf("stack %q: %w", b.Name, b.Err))
	}

	for _, name := range stacks.Names() {
		s := stacks.Healthy[name]
		errs = append(errs, checkStack(root, stacks, s)...)
	}
	errs = append(errs, checkCycles(stacks)...)

	nests, broken, err := nest.ListNests(root)
	if err != nil {
		return append(errs, err)
	}
	for _, b := range broken {
		errs = append(errs, fmt.Errorf("nest %q: %w", b.Name, b.Err))
	}
	for _, n := range nests {
		errs = append(errs, checkNest(root, stacks, n)...)
	}
	return errs
}
```

`checkStack` (same file): validate parent + confinement + existence.

```go
// checkStack judges one healthy stack: its parent reference, and every path
// it declares. Paths were made absolute by LoadStack against the stack dir;
// what is checked here is that they stay INSIDE root and exist. Confinement
// is a shareability rule, not a security one: a path that escapes the
// checkout depends on the machine that receives the source, so the object is
// not distributable (spec 2026-08-04 §5).
func checkStack(root string, stacks config.Stacks, s *config.Stack) []error {
	var errs []error
	if s.Parent != "" {
		if strings.Contains(s.Parent, config.SourceRefSeparator) {
			errs = append(errs, fmt.Errorf(
				"stack %q: `parent: %s` is a prefixed reference — inside a source, references are "+
					"bare and resolve in the source itself: the install name is chosen per machine "+
					"and CI knows none", s.Name, s.Parent))
		} else if _, err := stacks.Get(s.Parent); err != nil {
			errs = append(errs, fmt.Errorf("stack %q: %w", s.Name, err))
		}
	}
	paths := slices.Concat(s.DeclaredKits(), s.Provision.Includes, s.Provision.Steps)
	for _, p := range paths {
		rel, err := filepath.Rel(root, p)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			errs = append(errs, fmt.Errorf(
				"stack %q: %s escapes the checkout — a source is self-contained: a path outside "+
					"its tree depends on the receiving machine and cannot be shared", s.Name, p))
			continue
		}
		if _, err := os.Stat(p); err != nil {
			errs = append(errs, fmt.Errorf("stack %q: %s: %w", s.Name, p, err))
		}
	}
	return errs
}

// checkCycles walks parent edges among HEALTHY stacks. Three colors are not
// needed at this scale: a walked set per start plus a global done set keeps
// it linear and the first cycle found names its members.
func checkCycles(stacks config.Stacks) []error {
	var errs []error
	done := map[string]bool{}
	for _, start := range stacks.Names() {
		if done[start] {
			continue
		}
		seen := map[string]bool{}
		var chain []string
		for cur := start; ; {
			if done[cur] {
				break
			}
			if seen[cur] {
				errs = append(errs, fmt.Errorf(
					"stack %q: `parent:` cycle (%s) — a build DAG must terminate on a `base:` stack",
					cur, strings.Join(append(chain, cur), " -> ")))
				break
			}
			seen[cur] = true
			chain = append(chain, cur)
			s, ok := stacks.Healthy[cur]
			if !ok || s.Parent == "" {
				break
			}
			cur = s.Parent
		}
		for n := range seen {
			done[n] = true
		}
	}
	slices.SortFunc(errs, func(a, b error) int { return strings.Compare(a.Error(), b.Error()) })
	return errs
}

// checkNest judges one loadable nest: its stack reference must be bare and
// resolvable in THIS checkout.
func checkNest(root string, stacks config.Stacks, n *nest.Nest) []error {
	var errs []error
	if n.Stack == "" {
		errs = append(errs, fmt.Errorf(
			"nest %q: no `stack:` — a source nest cannot fall back on the personal defaults.stack: "+
				"it must spawn identically on every machine", n.Name))
		return errs
	}
	if strings.Contains(n.Stack, config.SourceRefSeparator) {
		errs = append(errs, fmt.Errorf(
			"nest %q: `stack: %s` is a prefixed reference — inside a source, references are bare "+
				"and resolve in the source itself: the install name is chosen per machine and CI "+
				"knows none", n.Name, n.Stack))
		return errs
	}
	if _, err := stacks.Get(n.Stack); err != nil {
		errs = append(errs, fmt.Errorf("nest %q: %w", n.Name, err))
	}
	return errs
}
```

Note for the implementer: `TestRunPrefixedRefInsideSource` uses a nest with a
`key:` repo — Task 3 must be merged first (strict YAML would refuse `key:`).

- [ ] **Step 4: Run tests, full checks, commit**

```bash
go test ./internal/lint/ -count=1 && make test && make lint
git add internal/lint/
git commit -m "feat(lint): checkout validation — strict YAML, DAG, confinement, bare references"
```

---

### Task 5: `internal/source` — layout, listing, staleness (no mutations yet)

**Files:**
- Create: `internal/source/source.go`
- Test: `internal/source/source_test.go`, `internal/source/main_test.go`

**Interfaces:**
- Produces:
  - `source.Dir(denHome, name string) string` = `<denHome>/sources/<name>` — SOLE definition of the layout.
  - `source.Root(denHome) string` = `<denHome>/sources`.
  - `source.Locate(denHome, ref string) (root, source, name string, err error)` — `("corp:devx")` → `(<denHome>/sources/corp, "corp", "devx", nil)`; bare ref → `(denHome, "", ref, nil)`; validates the source name and that the source dir exists (error names `den source add`).
  - `source.LastFetch(denHome, name string) (time.Time, bool)` — mtime of `.git/FETCH_HEAD`, else `.git/HEAD` (a fresh clone has no FETCH_HEAD; its HEAD mtime is the clone time), else `(zero, false)`.
  - `source.StaleAfter = 7 * 24 * time.Hour`; `source.Stale(denHome, name string, now time.Time) bool`.
  - `source.Info{Name, URL, Head string; LastFetch time.Time; LintErrs []error}` and `source.List(ctx context.Context, git worktree.Git, denHome string) ([]Info, error)`.

- [ ] **Step 1: Write main_test.go**

```go
// internal/source/main_test.go
package source

import (
	"os"
	"testing"

	"github.com/PillowPillow/den/internal/worktree"
)

// TestMain neutralizes the machine's git configuration and the redirecting
// variables, exactly as internal/cli does: this package's tests run REAL git
// against file:// remotes built in temp dirs, and an inherited GIT_DIR has
// already made suites commit into unrelated repos.
func TestMain(m *testing.M) {
	worktree.NeutralizeGitEnvironment()
	os.Exit(m.Run())
}
```

- [ ] **Step 2: Write the failing tests**

```go
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
	if len(infos[0].LintErrs) != 0 {
		t.Errorf("valid source reported lint errors: %v", infos[0].LintErrs)
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/source/ -count=1`
Expected: FAIL (package does not exist)

- [ ] **Step 4: Implement**

```go
// Package source manages team source repositories: git clones under
// <denHome>/sources/<name>/ carrying the den-home partial layout (stacks/,
// lib/, kits/, nests/ — spec 2026-08-04). No parallel registry, same doctrine
// as the sandbox truth coming from `sbx ls`: an installed source IS a
// directory that is a git clone; the URL lives in its remote, the freshness
// in its FETCH_HEAD mtime. Git runs behind worktree.Git, injected — this
// package must stay testable against file:// remotes.
package source

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/lint"
	"github.com/PillowPillow/den/internal/worktree"
)

// Root is the SOLE definition of where sources live.
func Root(denHome string) string { return filepath.Join(denHome, "sources") }

// Dir is the SOLE definition of one source's clone directory. Every message
// that names the directory to fix must go through this (paths.go doctrine).
func Dir(denHome, name string) string { return filepath.Join(Root(denHome), name) }

// Locate resolves a reference to (root, source, bare name). A bare ref is a
// local object: root is the den home itself. The existence check happens HERE,
// once, because every caller (spawn, nest show, build) would otherwise fail
// later with a bare "not found" that never says `den source add` is the fix.
func Locate(denHome, ref string) (root, src, name string, err error) {
	src, name = config.SplitSourceRef(ref)
	if src == "" {
		return denHome, "", name, nil
	}
	if err := config.ValidateSourceName(src); err != nil {
		return "", "", "", err
	}
	dir := Dir(denHome, src)
	if fi, statErr := os.Stat(dir); statErr != nil || !fi.IsDir() {
		return "", "", "", fmt.Errorf(
			"source %q: not installed — expected %s; run `den source add <url> --name %s`",
			src, dir, src)
	}
	return dir, src, name, nil
}

// StaleAfter is the age past which the spawn hints at `den source update`.
// 7 days (spec 2026-08-04 §4): long enough that a VPN-less week of work stays
// quiet, short enough that a drifting team repo gets noticed.
const StaleAfter = 7 * 24 * time.Hour

// LastFetch reports when the source last talked to its remote: FETCH_HEAD's
// mtime, falling back on HEAD's for a fresh clone (clone writes HEAD but not
// FETCH_HEAD). ok=false when neither exists — not a git repo.
func LastFetch(denHome, name string) (time.Time, bool) {
	for _, f := range []string{"FETCH_HEAD", "HEAD"} {
		if fi, err := os.Stat(filepath.Join(Dir(denHome, name), ".git", f)); err == nil {
			return fi.ModTime(), true
		}
	}
	return time.Time{}, false
}

// Stale is the spawn-hint verdict. A source without git metadata is NOT
// stale: it is broken, and that is `den source ls`'s finding, not a freshness
// hint's.
func Stale(denHome, name string, now time.Time) bool {
	last, ok := LastFetch(denHome, name)
	return ok && now.Sub(last) > StaleAfter
}

// Info is one installed source as `den source ls` shows it.
type Info struct {
	Name      string
	URL       string
	Head      string
	LastFetch time.Time
	LintErrs  []error
}

// List reads the sources directory. A missing directory is an empty list —
// a den that never added a source is not an error. Git failures inside ONE
// source (a half-deleted clone) surface in that source's fields rather than
// failing the listing: `den source ls` is the tool that SHOWS broken sources.
func List(ctx context.Context, git worktree.Git, denHome string) ([]Info, error) {
	entries, err := os.ReadDir(Root(denHome))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", Root(denHome), err)
	}
	var out []Info
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		dir := Dir(denHome, name)
		info := Info{Name: name, LintErrs: lint.Run(dir)}
		if raw, err := git.Run(ctx, dir, "remote", "get-url", "origin"); err == nil {
			info.URL = strings.TrimSpace(string(raw))
		} else {
			info.URL = fmt.Sprintf("(unreadable: %v)", err)
		}
		if raw, err := git.Run(ctx, dir, "rev-parse", "--short", "HEAD"); err == nil {
			info.Head = strings.TrimSpace(string(raw))
		}
		info.LastFetch, _ = LastFetch(denHome, name)
		out = append(out, info)
	}
	return out, nil
}
```

- [ ] **Step 5: Run tests, full checks, commit**

```bash
go test ./internal/source/ -count=1 && make test && make lint
git add internal/source/
git commit -m "feat(source): layout, reference location, staleness and listing"
```

---

### Task 6: `source.Add` / `source.Update` / `source.Remove` (fail-closed mutations)

**Files:**
- Modify: `internal/source/source.go` (or create `internal/source/mutate.go`)
- Test: `internal/source/mutate_test.go`

**Interfaces:**
- Consumes: `lint.Run`, `worktree.Git`, fixtures from Task 5's test file (`makeSourceRepo`, `gitCmd`).
- Produces:
  - `source.Add(ctx, git worktree.Git, denHome, url, name string) (string, error)` — returns the resolved name. Empty name defaults to `path.Base(url)` minus a `.git` suffix.
  - `source.Update(ctx, git worktree.Git, denHome, name string) error`
  - `source.Remove(ctx, git worktree.Git, denHome, name string) error`

- [ ] **Step 1: Write the failing tests**

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/source/ -run 'TestAdd|TestUpdate|TestRemove' -count=1`
Expected: FAIL (undefined: Add, Update, Remove)

- [ ] **Step 3: Implement**

```go
// DefaultName derives a source name from its URL: the last path component,
// stripped of a ".git" suffix. Exported for the CLI's help text to stay
// honest about what "default" means.
func DefaultName(url string) string {
	base := path.Base(strings.TrimSuffix(strings.TrimSuffix(url, "/"), ".git"))
	return base
}

// Add clones url under sources/<name> and lints the result. An invalid
// source is REMOVED, not kept: a clone that fails lint would sit there
// half-usable, visible to Locate, and every later refusal would blame the
// wrong command. Refusing at add time names the actual fault: the repo.
func Add(ctx context.Context, git worktree.Git, denHome, url, name string) (string, error) {
	if name == "" {
		name = DefaultName(url)
	}
	if err := config.ValidateSourceName(name); err != nil {
		return "", fmt.Errorf("%w — pass `--name <legal name>`", err)
	}
	dir := Dir(denHome, name)
	if _, err := os.Stat(dir); err == nil {
		return "", fmt.Errorf(
			"source %q: already installed at %s — `den source update %s` refreshes it, "+
				"`den source rm %s` removes it", name, dir, name, name)
	}
	if err := os.MkdirAll(Root(denHome), 0o755); err != nil {
		return "", fmt.Errorf("creating %s: %w", Root(denHome), err)
	}
	// Root(denHome) as cwd: `git clone url dir` needs no repo, only a directory.
	if _, err := git.Run(ctx, Root(denHome), "clone", "--", url, dir); err != nil {
		return "", err
	}
	if errs := lint.Run(dir); len(errs) > 0 {
		// Best-effort removal: the refusal below matters more than the cleanup's
		// own error, and a leftover directory is visible in `den source ls`.
		os.RemoveAll(dir)
		return "", lintRefusal(name, url, errs)
	}
	return name, nil
}

// lintRefusal assembles lint findings into one refusal, ConfigError-shaped:
// all faults at once, so the team repo gets one report instead of one per push.
func lintRefusal(name, where string, errs []error) error {
	var b strings.Builder
	fmt.Fprintf(&b, "source %q: %s is not a valid source:", name, where)
	for _, e := range errs {
		fmt.Fprintf(&b, "\n  - %v", e)
	}
	return errors.New(b.String())
}

// Update fetches and fast-forwards — with the lint gate BETWEEN the two
// (spec 2026-08-04 §3): the fetched tree is linted in a throwaway detached
// git worktree, and an invalid upstream leaves HEAD exactly where it was.
// Fail-closed is the point: a team member who pushed a typo must not be able
// to break every colleague's next spawn.
func Update(ctx context.Context, git worktree.Git, denHome, name string) error {
	if err := config.ValidateSourceName(name); err != nil {
		return err
	}
	dir := Dir(denHome, name)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("source %q: not installed — expected %s; `den source ls` shows what is", name, dir)
	}
	// Dirty check FIRST: den never touches unpushed contributions. `git status
	// --porcelain` is empty exactly when the tree is clean, untracked included.
	status, err := git.Run(ctx, dir, "status", "--porcelain")
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(status)) > 0 {
		return fmt.Errorf(
			"source %q: the working tree at %s has local changes — commit or discard them first; "+
				"den never overwrites unpushed contributions", name, dir)
	}
	if _, err := git.Run(ctx, dir, "fetch", "origin"); err != nil {
		return err
	}
	// Lint the FETCHED tree before moving HEAD. A detached worktree is the
	// one git-native way to materialize FETCH_HEAD without touching the
	// clone's own checkout; --force on removal because the throwaway tree is
	// ours and gone either way.
	tmp, err := os.MkdirTemp("", "den-source-lint-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	probe := filepath.Join(tmp, "tree")
	if _, err := git.Run(ctx, dir, "worktree", "add", "--detach", probe, "FETCH_HEAD"); err != nil {
		return err
	}
	lintErrs := lint.Run(probe)
	if _, err := git.Run(ctx, dir, "worktree", "remove", "--force", probe); err != nil {
		return err
	}
	if len(lintErrs) > 0 {
		return fmt.Errorf("%w\nthe local clone stays on its last valid state — nothing changed",
			lintRefusal(name, "the fetched update", lintErrs))
	}
	if _, err := git.Run(ctx, dir, "merge", "--ff-only", "FETCH_HEAD"); err != nil {
		return fmt.Errorf(
			"source %q: cannot fast-forward — the team repo rewrote its history. "+
				"If you have no local work: `den source rm %s` then `den source add <url> --name %s` (%w)",
			name, name, name, err)
	}
	return nil
}

// Remove deletes the clone. The dirty refusal mirrors Update's and exists
// for the same reason; --porcelain again, untracked included: a file the
// user created is work, whether git tracks it or not.
func Remove(ctx context.Context, git worktree.Git, denHome, name string) error {
	if err := config.ValidateSourceName(name); err != nil {
		return err
	}
	dir := Dir(denHome, name)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("source %q: not installed — expected %s", name, dir)
	}
	status, err := git.Run(ctx, dir, "status", "--porcelain")
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(status)) > 0 {
		return fmt.Errorf(
			"source %q: the working tree at %s has local changes — push or discard them first; "+
				"`den source rm` never destroys unpushed contributions", name, dir)
	}
	return os.RemoveAll(dir)
}
```

Imports to add: `bytes`, `errors`, `path`.

- [ ] **Step 4: Run tests, full checks, commit**

```bash
go test ./internal/source/ -count=1 && make test && make lint
git add internal/source/
git commit -m "feat(source): add/update/rm — fail-closed update lints the fetched tree before fast-forward"
```

---

### Task 7: CLI — `den source add|update|ls|rm`

**Files:**
- Create: `internal/cli/source.go`
- Modify: `internal/cli/root.go` (one `root.AddCommand(newSourceCmd(&denHome, deps.Git))` line, placed with the others)
- Test: `internal/cli/source_test.go`

**Interfaces:**
- Consumes: `source.Add/Update/List/Remove/Dir` (Tasks 5-6), `deps.Git` (already in `Deps`).
- Produces: the `den source` command tree. Output shapes below are what the tests lock.

- [ ] **Step 1: Write the failing tests**

`internal/cli` has `TestMain` with `NeutralizeGitEnvironment` already (`main_test.go`) — reuse. Copy the fixture helpers `gitCmd`/`makeSourceRepo` shape from Task 5 into `source_test.go` (test files do not import other packages' tests). Also reuse this package's existing helper for building a root command with hand-built `Deps` (read `root_deps_test.go` first and follow its pattern — only `Git` needs to be real: `worktree.NewGit()`).

```go
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

func TestSourceUpdateAllWhenNoName(t *testing.T) {
	home := t.TempDir()
	url := makeSourceRepo(t)
	// two sources installed, `den source update` bare updates both
	// (assert by running it and checking it succeeds; per-source failures
	// must name the source and not stop the loop — install one valid and one
	// with a hand-broken .git to assert the error names the broken one and
	// the valid one still updated)
	...
}
```

Write `TestSourceUpdateAllWhenNoName` fully at implementation time following the sketch: the behavior to lock is (a) no arg = update every installed source, (b) one failing source does not prevent the others from updating, (c) the combined error names each failing source.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run TestSource -count=1`
Expected: FAIL (unknown command "source")

- [ ] **Step 3: Implement `newSourceCmd`**

Follow the shape of `newNestCmd` (`internal/cli/nest.go`): a parent command with subcommands, each `RunE` resolving `config.Home(*denHome)` first.

```go
// newSourceCmd manages team source repositories (spec 2026-08-04 §3). git is
// the injected worktree.Git — the SAME injection `den rm` already receives —
// so the whole tree tests against file:// remotes.
func newSourceCmd(denHome *string, git worktree.Git) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "source",
		Short: "Manage team source repositories (stacks/nests shared over git)",
	}

	var name string
	add := &cobra.Command{
		Use:   "add <url>",
		Short: "Clone a source repository under <den home>/sources/ and validate it",
		Args:  exactlyOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}
			resolved, err := source.Add(cmd.Context(), git, home, args[0], name)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"source %q installed — its objects are addressed %s:<name> (e.g. `den %s:<nest>`)\n",
				resolved, resolved, resolved)
			return nil
		},
	}
	add.Flags().StringVar(&name, "name", "", "install name (default: the URL's basename)")
	cmd.AddCommand(add)

	// update [name]: bare = all. One broken source must not shield the
	// others from freshness — errors accumulate, ConfigError-shaped.
	// ls: one line per source: name, HEAD, last fetch (or "never"), URL, and
	// "INVALID (run `den source update <n>` after the repo is fixed)" when
	// LintErrs is non-empty.
	// rm <name>: source.Remove verbatim.
	...
	return cmd
}
```

Write `update`/`ls`/`rm` fully at implementation time in the same shape as `add` (home resolution, one `source.X` call, output on OutOrStdout). `ls` prints `(none)` on an empty list, like the other listing commands — read `newLsCmd` for the exact convention first.

- [ ] **Step 4: Run tests, full checks, commit**

```bash
go test ./internal/cli/ -run TestSource -count=1 && make test && make lint
git add internal/cli/source.go internal/cli/source_test.go internal/cli/root.go
git commit -m "feat(cli): den source add/update/ls/rm"
```

---

### Task 8: CLI — `den lint <path>`

**Files:**
- Create: `internal/cli/lint.go`
- Modify: `internal/cli/root.go` (`root.AddCommand(newLintCmd())` — no denHome: lint targets an arbitrary checkout, never the den home implicitly)
- Test: `internal/cli/lint_test.go`

**Interfaces:**
- Consumes: `lint.Run` (Task 4).
- Produces: `den lint <path>` — prints `ok` and exits 0 on a valid checkout; prints every finding and returns an error (exit 1) otherwise.

- [ ] **Step 1: Write the failing tests**

```go
func TestLintValidCheckout(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "stacks", "devx", "stack.yaml"),
		"image: devx:v1\nbase: claude\n") // reuse/add this package's file helper
	cmd := NewRootCmdWith(Deps{})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"lint", root})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("lint: %v", err)
	}
	if !strings.Contains(out.String(), "ok") {
		t.Errorf("output = %q", out.String())
	}
}

func TestLintInvalidCheckoutFails(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "stacks", "devx", "stack.yaml"),
		"image: devx:v1\nbase: claude\negres: []\n")
	cmd := NewRootCmdWith(Deps{})
	cmd.SetArgs([]string{"lint", root})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "egres") {
		t.Fatalf("expected the lint failure, got: %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run TestLint -count=1`
Expected: FAIL (unknown command "lint")

- [ ] **Step 3: Implement**

```go
// newLintCmd validates a checkout — a team source repo being developed, in
// its CI or on a laptop. Deliberately den-home-agnostic: the argument is a
// path, and lint never reads the personal configuration, so a CI runner
// needs no den home at all (spec 2026-08-04 §5).
func newLintCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "lint <path>",
		Short: "Validate a source checkout (stacks, nests, references, confinement)",
		Args:  exactlyOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			errs := lint.Run(args[0])
			if len(errs) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "ok")
				return nil
			}
			var b strings.Builder
			fmt.Fprintf(&b, "%s is not a valid source:", args[0])
			for _, e := range errs {
				fmt.Fprintf(&b, "\n  - %v", e)
			}
			return errors.New(b.String())
		},
	}
}
```

- [ ] **Step 4: Run tests, full checks, commit**

```bash
go test ./internal/cli/ -run TestLint -count=1 && make test && make lint
git add internal/cli/lint.go internal/cli/lint_test.go internal/cli/root.go
git commit -m "feat(cli): den lint <path> — CI-friendly checkout validation"
```

---

### Task 9: Spawn resolves source references (nest, stack, sandbox name, hint)

**Files:**
- Modify: `internal/spawn/spawn.go` (the load sequence around lines 161-204)
- Modify: `internal/spawn/deps.go` or wherever `spawn.Deps` lives (add `Now func() time.Time`)
- Modify: `internal/cli/root.go` (wire `Now: time.Now` into the `spawn.Deps{...}` literal)
- Test: `internal/spawn/spawn_test.go`

**Interfaces:**
- Consumes: `source.Locate`, `source.Stale`, `config.FlattenSandboxComponent`, `nest.FilePath`.
- Produces: `den corp:backend` spawns sandbox `corp-backend` from the source's nest and the source's stacks. `spawn.Deps.Now func() time.Time` — nil skips the staleness hint (hand-built test Deps owe nothing to the clock).

Behavioral contract to implement, in the existing "everything rejectable from config alone happens before the first side effect" order:

1. Split `o.Nest` with `source.Locate(denHome, o.Nest)` → `(nestRoot, srcName, bareNest)`. A missing source refuses here.
2. `nest.LoadNest(nestRoot, bareNest)` replaces the current `nest.LoadNest(denHome, o.Nest)`.
3. Stack origin: `ref := n.Stack`; if empty, `g.Defaults.Stack`. If the nest CAME from a source: a prefixed ref is a refusal ("inside a source, references are bare" — same wording as lint's), and the bare ref resolves in `nestRoot`. If the nest is local: `source.Locate(denHome, ref)` decides the root. Load `config.LoadStacks(stackRoot)` instead of `config.LoadStacks(denHome)`, then set `n.Stack` to the bare stack name before `nest.Resolve` (comment: Resolve works on bare names within one root; the caller owns reference resolution).
4. Sandbox component: local nest → `o.Nest` unchanged (current behavior). Source nest → `config.FlattenSandboxComponent("nest", o.Nest)` (`corp:backend` → `corp-backend`), and REFUSE when `nest.FilePath(denHome, flattened)` exists: a local nest with the flattened name makes attach/ls ambiguous — the message names both files and asks to rename one.
5. Staleness hint (source nests and source stacks alike, once per source): when `d.Now != nil && source.Stale(denHome, srcName, d.Now())`, print to `d.Err`: `hint: source "corp" was last fetched more than 7 days ago — den source update corp`. Never a refusal, never network.

- [ ] **Step 1: Write the failing tests**

Read `internal/spawn/spawn_test.go` first and reuse its den-home fixture helpers and its `sbx.Fake`. Then add (adapting helper names):

```go
func TestSpawnFromSourceNest(t *testing.T) {
	home := writeDenHome(t) // existing helper: valid config.yaml + local stack
	// Install a source by LAYOUT alone: spawn never runs git, so a plain
	// directory under sources/ is a valid installed source for it.
	writeFileUnder(t, home, "sources/corp/stacks/teamstack/stack.yaml",
		"image: teamstack:v1\nbase: claude\n")
	writeFileUnder(t, home, "sources/corp/nests/api.yaml",
		"stack: teamstack\nrepos:\n  - { path: "+t.TempDir()+" }\n")

	fake := sbx.NewFake(...) // existing fixture shape
	err := Spawn(ctx, home, Options{Nest: "corp:api", Detach: true}, depsWith(fake))
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	// The sandbox name is the FLATTENED reference.
	assertCreatedSandbox(t, fake, "corp-api")
}

func TestSpawnSourceNestRefusesLocalHomonym(t *testing.T) {
	// sources/corp/nests/api.yaml AND nests/corp-api.yaml both present:
	// the flattened names collide, spawn refuses BEFORE any side effect,
	// the message names both files.
}

func TestSpawnSourceNestRefusesPrefixedStackRef(t *testing.T) {
	// sources/corp/nests/api.yaml with `stack: corp:teamstack` → refusal
	// containing "bare".
}

func TestSpawnLocalNestWithSourceStack(t *testing.T) {
	// local nests/n.yaml with `stack: corp:teamstack` → spawns, image
	// teamstack:v1, sandbox name "n" (unflattened: the NEST is local).
}

func TestSpawnHintsOnStaleSource(t *testing.T) {
	// Now returns fetch-mtime + 8 days → d.Err contains "den source update corp".
	// Also: Now == nil → no hint, no panic.
}
```

Write the four sketched bodies fully at implementation time, in the file's existing style — each is the same fixture dance as `TestSpawnFromSourceNest` with one knob turned.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/spawn/ -run TestSpawn.*Source -count=1`
Expected: FAIL (source refs unrecognized: LoadNest rejects `corp:api` — ":" is not in the nest charset)

- [ ] **Step 3: Implement** per the behavioral contract above. Keep the resolution block together and comment WHY the caller (not Resolve) owns reference resolution: Resolve stays a pure cascade over one root, and the one place that knows about sources is the one that loaded them.

- [ ] **Step 4: Run tests, full checks, commit**

```bash
go test ./internal/spawn/ -count=1 && make test && make lint
git add internal/spawn/ internal/cli/root.go
git commit -m "feat(spawn): source references — corp:nest spawns from the source, flattened sandbox names, staleness hint"
```

---

### Task 10: Source references in `nest show`, `nest ls`, `build`, `sh`, `rm`, `ports`

**Files:**
- Modify: `internal/cli/nest.go`, `internal/cli/build.go`, `internal/cli/sh.go`, `internal/cli/rm.go`, `internal/cli/ports.go`
- Test: each command's existing `_test.go`

**Interfaces:**
- Consumes: `source.Locate`, `config.FlattenSandboxComponent`.

Per-command contract (each is a small edit at the existing `LoadNest`/`LoadStacks` call sites found in the grep of this plan's preparation):

- `den nest show corp:api` (`nest.go:114-122`): `source.Locate` the argument; load the nest from its root; locate the STACK ref exactly as spawn does (same bare-inside-source refusal); `LoadStacks` from the stack's root; display unchanged.
- `den nest ls` (`nest.go`): after listing local nests, iterate installed sources (`os.ReadDir(source.Root(home))`, skip non-dirs) and list each source's nests prefixed `<src>:<name>`. Broken source nests report like broken local ones, prefixed.
- `den build corp:teamstack` (`build.go:36`): `source.Locate` the argument, `config.LoadStacks` from that root, build the bare name. Bare `den build` keeps building the local stacks only — a source is built explicitly (its images are usually pulled or built by whoever maintains it).
- `den sh corp:api`, `den rm corp:api`, `den ports corp:api` (`sh.go`, `rm.go:97-117`, `ports.go:98`): the sandbox name is the FLATTENED ref (`config.FlattenSandboxComponent("nest", arg)` when the arg contains `:`, unchanged otherwise); where the command loads the nest file (rm's worktree cleanup, ports' declarations), `source.Locate` the ref and load from its root.

- [ ] **Step 1: For each command, write one failing test** in its existing `_test.go`, following that file's fixtures: a source materialized by layout (as in Task 9), the command invoked with `corp:api`, asserting (a) the right sandbox name reaches the `sbx.Fake` (sh/rm/ports), or (b) the output names the source object (`nest show`, `nest ls`, `build` plan output).

- [ ] **Step 2: Run to verify each fails, implement each call site, re-run.**

One commit per command is fine; or one commit for all five if the diffs stay small:

```bash
make test && make lint
git add internal/cli/
git commit -m "feat(cli): source references accepted by nest show/ls, build, sh, rm, ports"
```

---

### Task 11: Documentation — README, spec cross-links, CLAUDE.md

**Files:**
- Modify: `README.md` (new "Team sources" section: add/update/ls/rm, `den lint`, repo keys, the `:` addressing, the staleness hint)
- Modify: `docs/superpowers/specs/2026-07-27-den-cli-design.md` (§3 layout: add `sources/` line pointing at the 2026-08-04 spec; §11 command table: `den source …`, `den lint`)
- Modify: `CLAUDE.md` (Architecture: one paragraph on sources + the reference rule; commands table if present)

- [ ] **Step 1: Write the README section** — mirror the tone of existing sections; include a copy-pasteable session: `den source add git@gitlab.corp:dev/stacks.git --name corp` → `den corp:backend` → `den source update`. Document the repo-key mapping with the exact YAML from the spec §2.4.
- [ ] **Step 2: Update the mother spec's §3 and §11** — a one-line pointer each, not a duplicate: the 2026-08-04 spec stays the source of truth for sources.
- [ ] **Step 3: Update CLAUDE.md** — extend the "Architecture" section with: sources live in `sources/<n>/` (git clones), references are `<source>:<name>` on the personal side and BARE inside a source, `internal/source` owns git-backed management, `internal/lint` owns checkout validation.
- [ ] **Step 4: Commit**

```bash
make test && make lint
git add README.md docs/ CLAUDE.md
git commit -m "docs: team sources — README section, spec cross-links, CLAUDE.md architecture note"
```
