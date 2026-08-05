# Sandbox Manifest Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make den record, at sandbox creation, exactly what it mounted — so `den rm` reclaims what it actually created instead of re-deriving it from a configuration that may have moved since.

**Architecture:** A new leaf package `internal/manifest` writes one YAML file per sandbox under `<denHome>/state/sandboxes/<sandbox>.yaml`. `internal/spawn` writes it once, on the create branch, before `sbx create`. `internal/cli` reads it back in `rm` (replay instead of re-derive), `ls` (orphan line, true branch/nest display) and `doctor` (report + `--fix` reclaim). `internal/doctor` gains a **pure** orphan function only — it keeps its "No side effects, no network" invariant and never learns about `sbx`.

**Tech Stack:** Go, `gopkg.in/yaml.v3` (strict decoding), `github.com/spf13/cobra`, go-task (`Taskfile.yml`).

## Global Constraints

Every task's requirements implicitly include this section.

- **Source of truth:** `docs/superpowers/specs/2026-08-05-sandbox-manifest-design.md` (D1–D7 and the 14 proofs). It amends `docs/superpowers/specs/2026-07-27-den-cli-design.md` §3, §6, §7.
- **Runner:** `task check` (= `task lint` » `task typecheck` » `task test`, fail-fast) before every commit. `task test` is `go test -count=1 ./...`. There is **no Makefile**.
- **gofmt is enforced, not advisory** (`task lint` runs `go vet ./...` then `test -z "$(gofmt -l .)"`).
- **Language:** code, comments and user-facing messages in **English**. Only `docs/superpowers/` is French.
- **Comment style:** a long "why" comment at each decision site, naming what was rejected and which regression the choice prevents. Terse code visibly does not match this codebase.
- **Doctrine:** den refuses rather than normalizing in silence; every error names the file to fix and the remedy.
- **Doctrine T13/T16:** `den rm` must never refuse in a way that leaves the user with a live VM they can no longer destroy. Every new failure mode on the `rm` path degrades to a warning + fallback, never to a refusal.
- **Test conventions:** no `t.Parallel()`, no socket, no spawned process. Packages running real git (`cli`, `spawn`, `worktree`) already call `worktree.NeutralizeGitEnvironment()` in `TestMain` — do not remove it. `sbx.Fake` (`internal/sbx/fake.go`) is a **production** file, on purpose.
- **Goldens:** `internal/*/testdata/*.golden`, compared by hand. **There is no `-update` flag** — edit goldens manually.
- **Hermeticity:** `internal/spawn` must never import `internal/ports`; `internal/cli` must import none of `net`, `hash/fnv`, `os/exec`. Locked by `internal/ports/hermeticity_test.go`. `internal/manifest` must import none of those three either.
- **Permissions:** manifest directory `0700`, file `0600` — same as the mixin cache (`internal/agent/mixin.go`), and for the same reason: the file carries user paths.
- **`state/`, not `cache/`:** spec §3 declares `cache/` reconstructible; a command-line mount is reconstructible from nothing. `state/` is **never purged automatically**.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/manifest/manifest.go` (create) | The record: types, the single `Path` definition, `Write`, `Read`, `Remove`, `List`. File IO + serialization only, like `agent.WriteMixin`. |
| `internal/manifest/manifest_test.go` (create) | Round-trip, golden, strict decoding, hostile names, `List` skipping broken files. |
| `internal/manifest/testdata/manifest.golden` (create) | The rendered file, compared by hand. |
| `internal/worktree/worktree.go` (modify) | `Target` gains `WorktreePath`: an explicit, recorded path that wins over the `Path()` calculation. |
| `internal/spawn/spawn.go` (modify) | Builds and writes the manifest on the create branch, before `sbx create`. Reports "nest changed since creation" on the attach branch. |
| `internal/cli/rm.go` (modify) | Replays the manifest; falls back to today's derivation when it is absent or unreadable; removes the manifest when everything it lists is reclaimed. |
| `internal/cli/ls.go` (modify) | Orphan line; WORKTREE shows the branch as typed, NEST the prefixed reference. Fail-open throughout. |
| `internal/doctor/doctor.go` (modify) | `Orphans` (pure) + `OrphanCheck` (pure). No new `Deps` field, no `sbx`. |
| `internal/cli/doctor.go` (modify) | Supplies the live list, prints the orphan check, and carries the `--fix` / `--force` mutation. |
| `README.md`, spec §3 (modify) | `state/` documented as a top-level directory that is never purged. |

---

### Task 1: The `internal/manifest` package

**Files:**
- Create: `internal/manifest/manifest.go`
- Create: `internal/manifest/manifest_test.go`
- Create: `internal/manifest/testdata/manifest.golden`

**Interfaces:**
- Consumes: `sbx.ValidateSandboxName(string) error`, `config.FileError` (both already exist).
- Produces — every later task depends on exactly these names:
  - `manifest.Schema` (untyped const `1`)
  - `manifest.OriginKey`, `manifest.OriginPath`, `manifest.OriginCommandLine` (string consts `"key"`, `"path"`, `"command-line"`)
  - `manifest.Manifest{Schema int; Sandbox string; Nest Nest; Worktree *Worktree; Repos []Repo; GitDirs []string}`
  - `manifest.Nest{Ref, File string}`
  - `manifest.Worktree{Name, Branch, Layout, Root string}`
  - `manifest.Repo{Name, Origin, Key, Repo, Mount string; Worktree bool}`
  - `manifest.Dir(denHome string) string`
  - `manifest.Path(denHome, sandboxName string) (string, error)`
  - `manifest.Write(denHome string, m Manifest) error`
  - `manifest.Read(denHome, sandboxName string) (Manifest, error)`
  - `manifest.Remove(denHome, sandboxName string) error`
  - `manifest.List(denHome string) ([]Manifest, []Broken, error)` with `manifest.Broken{Path string; Err error}`

- [ ] **Step 1: Write the failing round-trip test**

Create `internal/manifest/manifest_test.go`:

```go
package manifest

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// sample is the manifest of a worktree'd source nest mounting one keyed repo
// and one repo given on the command line — the composition every later reader
// has to survive.
func sample() Manifest {
	return Manifest{
		Sandbox: "corp-api.feat12",
		Nest:    Nest{Ref: "corp:api", File: "/home/x/.den/sources/corp/nests/api.yaml"},
		Worktree: &Worktree{
			Name:   "feat12",
			Branch: "feature/12",
			Layout: "central",
			Root:   "/home/x/.den/worktrees",
		},
		Repos: []Repo{
			{
				Name:     "api",
				Origin:   OriginKey,
				Key:      "api",
				Repo:     "/home/x/dev/api",
				Mount:    "/home/x/.den/worktrees/feat12/api",
				Worktree: true,
			},
			{
				Name:   "hotfix",
				Origin: OriginCommandLine,
				Repo:   "/tmp/hotfix",
				Mount:  "/tmp/hotfix",
			},
		},
		GitDirs: []string{"/home/x/dev/api/.git"},
	}
}

// The round-trip is the property the whole feature rests on: rm replays what
// spawn wrote, and a field lost between the two would silently stop a worktree
// from being reclaimed. Modelled on TestReadMixinRereadsWhatWriteMixinWrote.
func TestReadRereadsWhatWriteWrote(t *testing.T) {
	denHome := t.TempDir()
	want := sample()
	if err := Write(denHome, want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Read(denHome, want.Sandbox)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	want.Schema = Schema // Write stamps it; the caller does not
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round-trip lost data:\n got %#v\nwant %#v", got, want)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/manifest/ -run TestReadRereadsWhatWriteWrote -count=1`
Expected: FAIL — the package does not build (`undefined: Manifest`, `undefined: Write`).

- [ ] **Step 3: Write the package**

Create `internal/manifest/manifest.go`:

```go
// Package manifest records what den ACTUALLY mounted when it created a
// sandbox, so no later reader has to re-derive it.
//
// Everything den does after a spawn — reclaiming worktrees at `den rm`,
// naming the branch in `den ls`, spotting a sandbox whose VM is gone — used
// to be deduced from today's configuration. That deduction is only right for
// as long as the configuration has not moved: a `repos:` line edited, a
// `worktree_root` relocated, a nest deleted, or a repo mounted from the
// command line (declared in no file at all) each make it aim somewhere else,
// silently. Creation is an event; this package is its trace.
//
// On the HOST, not in the VM (spec 2026-08-05 D1): the file has to be readable
// exactly when the VM is not — a sandbox that no longer boots, a stopped one,
// or one sbx has already lost. Worktrees are host artifacts anyway.
//
// Under state/, not cache/: spec §3 declares cache/ reconstructible, and a
// command-line mount is reconstructible from nothing. A future `den clean`
// emptying cache/ would erase the only trace of a worktree carrying
// uncommitted work. state/ is never purged automatically.
package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/sbx"
	"gopkg.in/yaml.v3"
)

// Schema is the version stamped into every manifest den writes. Read refuses
// anything else rather than decoding it optimistically: a field that changed
// MEANING between versions would send `worktree.Remove` at a directory the
// writer never created. Every reader treats that refusal as "no usable
// manifest" and falls back, so a newer den's file never blocks an older one.
const Schema = 1

// Origins of a repo, in the manifest's own vocabulary. They are recorded
// rather than recomputed because they are exactly what cannot be recovered
// later: OriginCommandLine names a mount that appears in no file on disk, and
// OriginKey names one whose path came from a personal mapping that may since
// have changed or vanished.
const (
	OriginKey         = "key"
	OriginPath        = "path"
	OriginCommandLine = "command-line"
)

// Manifest is one sandbox's creation record.
//
// Deliberately NO timestamp: den injects its clocks (Freshness, Policy), and
// a field here would thread a clock through all of Spawn for something no
// reader consults.
type Manifest struct {
	Schema  int      `yaml:"schema"`
	Sandbox string   `yaml:"sandbox"`
	Nest    Nest     `yaml:"nest"`
	// A POINTER, so a spawn without -w renders no `worktree:` block at all
	// rather than a block of empty strings that reads like a worktree named "".
	Worktree *Worktree `yaml:"worktree,omitempty"`
	Repos    []Repo    `yaml:"repos"`
	GitDirs  []string  `yaml:"git_dirs,omitempty"`
}

// Nest records BOTH spellings, because they answer different questions: Ref is
// what the user typed (`corp:api`) and is the only form `den ls` can print
// back without lying, File is what was actually read and is what an "it
// changed since creation" comparison has to name.
type Nest struct {
	Ref  string `yaml:"ref"`
	File string `yaml:"file"`
}

// Worktree keeps the branch as TYPED next to the flattened component.
// Flattening is lossy — `-w feature/12` creates the sandbox `api.feat-12` —
// and the manifest is the only place the original survives once the spawn is
// over.
//
// Layout and Root are recorded even though Repo.Mount already carries the
// final path: worktree.Remove needs them for the trash fallback location and
// for the parent-directory cleanup, and re-reading them from config.yaml at
// rm time is the very dependency this file exists to sever.
type Worktree struct {
	Name   string `yaml:"name"`
	Branch string `yaml:"branch"`
	Layout string `yaml:"layout"`
	Root   string `yaml:"root"`
}

// Repo is one mounted repository.
//
// Mount is the path `sbx create` REALLY received; Repo is the repository it
// was derived from. Worktree says whether den created Mount — and therefore
// whether `den rm` may reclaim it. A repo mounted as-is (Worktree false) is
// the user's own working directory: den never touches it.
type Repo struct {
	Name     string `yaml:"name"`
	Origin   string `yaml:"origin"`
	Key      string `yaml:"key,omitempty"`
	Repo     string `yaml:"repo"`
	Mount    string `yaml:"mount"`
	Worktree bool   `yaml:"worktree"`
}

// Broken is a manifest file List could not decode. Named and returned rather
// than dropped: a caller that silently skipped it would report a sandbox as
// having no record when it has an unreadable one — two very different things
// for the user holding the leftover directories.
type Broken struct {
	Path string
	Err  error
}

// Dir and Path are the SOLE definition of where a manifest lives. Writing and
// reading must agree: had they composed the path separately and diverged, Read
// would forever return os.ErrNotExist and every reader would silently take its
// fallback path — a feature that is off, everywhere, with nothing failing.
// The same trap mixinDir/mixinPath documents, locked the same way, by
// TestReadRereadsWhatWriteWrote.
func Dir(denHome string) string {
	return filepath.Join(denHome, "state", "sandboxes")
}

// Path validates the name BEFORE composing a path with it. sbx.SplitName is
// deliberately total and validates nothing (it also serves sandboxes created
// outside den), and filepath.Join CLEANS a ".." into a real traversal instead
// of rejecting it — so `den rm` on a hostile name listed by `sbx ls` would
// otherwise read and delete a file outside state/. Defense in depth: Spawn
// already refuses these names upstream via sbx.SandboxName.
func Path(denHome, sandboxName string) (string, error) {
	if err := sbx.ValidateSandboxName(sandboxName); err != nil {
		return "", err
	}
	return filepath.Join(Dir(denHome), sandboxName+".yaml"), nil
}

// Write materializes the manifest. It stamps Schema itself, so no caller can
// write a file claiming a version this package does not produce.
//
// 0700/0600, like the mixin cache: the file lists every path den mounted, and
// nothing justifies making that readable by every account on the machine.
func Write(denHome string, m Manifest) error {
	path, err := Path(denHome, m.Sandbox)
	if err != nil {
		return fmt.Errorf("writing manifest: %w", err)
	}
	m.Schema = Schema
	content, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("rendering the manifest of %s: %w", m.Sandbox, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// Read decodes one manifest. STRICT (KnownFields), like every other decode in
// den: an unknown key is a load error, never a silence — the reason spec §12
// gives for the rule everywhere else applies here too, since a mistyped
// `worktre:` would leave rm with nothing to reclaim and no way to know.
//
// The error wraps os.ErrNotExist through config.FileError, so callers can tell
// "no manifest at all" (the legacy sandbox case, worth a mention) from
// "unreadable manifest" (worth a warning naming the file).
func Read(denHome, sandboxName string) (Manifest, error) {
	path, err := Path(denHome, sandboxName)
	if err != nil {
		return Manifest{}, fmt.Errorf("reading manifest: %w", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("reading manifest %s: %w", path, &config.FileError{Err: err})
	}
	m, err := decode(content)
	if err != nil {
		return Manifest{}, fmt.Errorf("reading manifest %s: %w", path, err)
	}
	return m, nil
}

func decode(content []byte) (Manifest, error) {
	dec := yaml.NewDecoder(bytes.NewReader(content))
	dec.KnownFields(true)
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, err
	}
	if m.Schema != Schema {
		return Manifest{}, fmt.Errorf(
			"schema %d, but this den only understands schema %d — the file was written by "+
				"another version of den", m.Schema, Schema)
	}
	return m, nil
}

// Remove deletes the manifest. An already-absent file is NOT an error: rm
// removes it after reclaiming what it listed, and failing there would refuse a
// `den rm` that did everything it was asked (doctrine T13/T16).
func Remove(denHome, sandboxName string) error {
	path, err := Path(denHome, sandboxName)
	if err != nil {
		return fmt.Errorf("removing manifest: %w", err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing %s: %w", path, err)
	}
	return nil
}

// List reads every manifest, for the orphan scan.
//
// A MISSING directory is not an error: a den home that has never spawned has
// no state/, and `den ls` must not report a problem over it.
//
// Results are sorted by sandbox name — os.ReadDir is already lexical, but the
// callers render them to a terminal and a golden cannot tolerate depending on
// that implementation detail.
func List(denHome string) ([]Manifest, []Broken, error) {
	entries, err := os.ReadDir(Dir(denHome))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", Dir(denHome), err)
	}
	var out []Manifest
	var broken []Broken
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(Dir(denHome), e.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			broken = append(broken, Broken{Path: path, Err: err})
			continue
		}
		m, err := decode(content)
		if err != nil {
			broken = append(broken, Broken{Path: path, Err: err})
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Sandbox < out[j].Sandbox })
	sort.Slice(broken, func(i, j int) bool { return broken[i].Path < broken[j].Path })
	return out, broken, nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/manifest/ -run TestReadRereadsWhatWriteWrote -count=1`
Expected: PASS

- [ ] **Step 5: Add the golden and the remaining package tests**

Create `internal/manifest/testdata/manifest.golden` by running the golden test once and reading the diff it prints — **write the file by hand**, there is no `-update` in this repo. The expected content is:

```yaml
schema: 1
sandbox: corp-api.feat12
nest:
    ref: corp:api
    file: /home/x/.den/sources/corp/nests/api.yaml
worktree:
    name: feat12
    branch: feature/12
    layout: central
    root: /home/x/.den/worktrees
repos:
    - name: api
      origin: key
      key: api
      repo: /home/x/dev/api
      mount: /home/x/.den/worktrees/feat12/api
      worktree: true
    - name: hotfix
      origin: command-line
      repo: /tmp/hotfix
      mount: /tmp/hotfix
      worktree: false
git_dirs:
    - /home/x/dev/api/.git
```

(yaml.v3's default indent is 4 spaces. If the produced bytes differ, trust the produced bytes and correct the golden — the golden records what den emits, it does not prescribe it.)

Append to `internal/manifest/manifest_test.go`:

```go
// The golden is what a human reads when a `den rm` misbehaves. It is compared
// by hand: this repo has no -update flag, on purpose.
func TestWriteRendersTheGoldenFile(t *testing.T) {
	denHome := t.TempDir()
	if err := Write(denHome, sample()); err != nil {
		t.Fatal(err)
	}
	path, err := Path(denHome, "corp-api.feat12")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "manifest.golden"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("rendered manifest differs from testdata/manifest.golden\n got:\n%s\nwant:\n%s", got, want)
	}
}

// A spawn without -w must render NO worktree block: a block of empty strings
// would read as a worktree literally named "", and worktree.Remove's own guard
// against that name exists because it erases the worktree root's parent.
func TestWriteOmitsTheWorktreeBlockWithoutAWorktree(t *testing.T) {
	denHome := t.TempDir()
	m := sample()
	m.Sandbox = "api"
	m.Worktree = nil
	m.Repos = []Repo{{Name: "api", Origin: OriginPath, Repo: "/dev/api", Mount: "/dev/api"}}
	if err := Write(denHome, m); err != nil {
		t.Fatal(err)
	}
	path, _ := Path(denHome, "api")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "worktree:\n") {
		t.Errorf("no worktree block expected:\n%s", content)
	}
}

// Strict decoding, like every other den decode: a mistyped key must be a load
// error, not a silence that leaves rm with nothing to reclaim.
func TestReadRefusesAnUnknownKey(t *testing.T) {
	denHome := t.TempDir()
	if err := os.MkdirAll(Dir(denHome), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(Dir(denHome), "api.yaml")
	if err := os.WriteFile(path, []byte("schema: 1\nsandbox: api\nworktre: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(denHome, "api"); err == nil {
		t.Fatal("an unknown key must be refused")
	} else if !strings.Contains(err.Error(), path) {
		t.Errorf("the message must name the file; got: %v", err)
	}
}

// A manifest from another den must not be decoded optimistically: a field
// whose MEANING changed would send worktree.Remove at a directory this writer
// never created. Readers treat the refusal as "no usable manifest".
func TestReadRefusesAnotherSchema(t *testing.T) {
	denHome := t.TempDir()
	if err := os.MkdirAll(Dir(denHome), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(Dir(denHome), "api.yaml"),
		[]byte("schema: 2\nsandbox: api\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(denHome, "api"); err == nil {
		t.Fatal("another schema must be refused")
	}
}

// Absence is distinguishable from corruption, because the two get different
// messages from rm: a mention for a legacy sandbox, a warning naming the file
// for a corrupt one.
func TestReadReportsAbsenceAsNotExist(t *testing.T) {
	_, err := Read(t.TempDir(), "api")
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("absence must surface as os.ErrNotExist; got: %v", err)
	}
}

// The name becomes a path, so it is validated before it is joined. Without
// this, a name `sbx ls` reports but den would never create escapes state/.
func TestPathRefusesAHostileName(t *testing.T) {
	if _, err := Path("/den", "api/../../evade"); err == nil {
		t.Fatal("a name that is not a legal sandbox name must be refused")
	}
}

// List is the orphan scan's input: one bad file must not hide every good one,
// and a den home that never spawned has no state/ at all.
func TestListSkipsBrokenFilesAndToleratesNoStateDir(t *testing.T) {
	empty, broken, err := List(t.TempDir())
	if err != nil || len(empty) != 0 || len(broken) != 0 {
		t.Fatalf("a den home without state/ must list nothing: %v %v %v", empty, broken, err)
	}

	denHome := t.TempDir()
	if err := Write(denHome, sample()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(Dir(denHome), "bad.yaml"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	good, bad, err := List(denHome)
	if err != nil {
		t.Fatal(err)
	}
	if len(good) != 1 || good[0].Sandbox != "corp-api.feat12" {
		t.Errorf("the readable manifest must survive its broken neighbour: %#v", good)
	}
	if len(bad) != 1 || !strings.HasSuffix(bad[0].Path, "bad.yaml") {
		t.Errorf("the broken file must be named: %#v", bad)
	}
}

// Removing what is already gone is not a failure: rm calls this after
// reclaiming everything, and refusing there would fail a `den rm` that did its
// whole job (doctrine T13/T16).
func TestRemoveToleratesAnAbsentFile(t *testing.T) {
	if err := Remove(t.TempDir(), "api"); err != nil {
		t.Errorf("removing an absent manifest must not fail: %v", err)
	}
}
```

Add `"strings"` to the test file's imports.

- [ ] **Step 6: Run the package suite**

Run: `go test ./internal/manifest/ -count=1`
Expected: PASS (7 tests). If the golden mismatches, correct `testdata/manifest.golden` by hand from the printed `got:` block.

- [ ] **Step 7: Full check and commit**

Run: `task check`
Expected: PASS

```bash
git add internal/manifest/
git commit -m "feat(manifest): den records what it mounted, once, at creation"
```

---

### Task 2: `worktree.Target` accepts a recorded path

**Files:**
- Modify: `internal/worktree/worktree.go:253-291` (`Target`, `Remove`)
- Test: `internal/worktree/worktree_test.go`

**Interfaces:**
- Consumes: nothing from Task 1.
- Produces: `worktree.Target.WorktreePath string` — when non-empty, `Remove` uses it verbatim instead of calling `Path(Layout, Root, Worktree, RepoPath)`. Task 4 and Task 7 both set it from `manifest.Repo.Mount`.

- [ ] **Step 1: Write the failing test**

Append to `internal/worktree/worktree_test.go`:

```go
// The recorded path WINS over the calculation. This is what severs rm's
// dependency on a configuration that may have moved: a worktree_root edited
// between the spawn and the rm made Path() aim at a directory that never
// existed, and the real one stayed on disk with no message about it.
func TestRemoveUsesTheRecordedPathOverTheCalculatedOne(t *testing.T) {
	denHome := t.TempDir()
	repo := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)

	// Created under the root in force at spawn time...
	spawnRoot := filepath.Join(t.TempDir(), "worktrees-then")
	created, err := Ensure(context.Background(), NewGit(), "central", spawnRoot,
		Name{Dir: "feat12", Branch: "feat12"}, repo)
	if err != nil {
		t.Fatal(err)
	}

	// ...and removed while config.yaml now names a different root.
	dest, err := Remove(context.Background(), NewGit(), Target{
		DenHome:      denHome,
		Layout:       "central",
		Root:         filepath.Join(t.TempDir(), "worktrees-now"),
		Nest:         "api.feat12",
		Worktree:     "feat12",
		RepoPath:     repo,
		WorktreePath: created,
	})
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if dest == "" {
		t.Fatal("the recorded worktree must be found and moved, not reported as already gone")
	}
	if _, err := os.Stat(created); !os.IsNotExist(err) {
		t.Errorf("%s must have left its place: %v", created, err)
	}
}
```

(`createTestRepo` already exists in this package's tests. If its name differs there, use the local equivalent — check the top of `worktree_test.go`.)

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/worktree/ -run TestRemoveUsesTheRecordedPathOverTheCalculatedOne -count=1`
Expected: FAIL — `unknown field WorktreePath in struct literal`.

- [ ] **Step 3: Add the field and honour it**

In `internal/worktree/worktree.go`, add to `Target` (after `RepoPath`, before `Force`):

```go
	// WorktreePath is the directory as RECORDED at creation
	// (internal/manifest). When set it WINS over the Path() calculation
	// below, and that is the whole point: Path() re-derives from the layout
	// and root in force TODAY, so a worktree_root moved since the spawn made
	// Remove aim at a directory that never existed while the real one stayed
	// on disk, silently.
	//
	// The calculation survives for callers that have no record — a sandbox
	// created before manifests existed, or one created outside den. Layout,
	// Root and Worktree stay REQUIRED even with WorktreePath set: the trash
	// fallback location (fallbackTrash) and the parent-directory cleanup
	// (removeParentDir) read them, and neither is derivable from the final
	// path alone.
	WorktreePath string
```

Replace the first line of `Remove`:

```go
func Remove(ctx context.Context, g Git, c Target) (string, error) {
	repoPath, worktreePath, force := c.RepoPath, c.WorktreePath, c.Force
	if worktreePath == "" {
		worktreePath = Path(c.Layout, c.Root, c.Worktree, c.RepoPath)
	}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/worktree/ -count=1`
Expected: PASS — the whole package, since every existing caller leaves `WorktreePath` empty and keeps the calculated path.

- [ ] **Step 5: Commit**

```bash
git add internal/worktree/
git commit -m "feat(worktree): Remove honours a recorded path over the recalculated one"
```

---

### Task 3: spawn writes the manifest

**Files:**
- Modify: `internal/spawn/spawn.go` (create branch, around line 652)
- Test: `internal/spawn/spawn_test.go`

**Interfaces:**
- Consumes: Task 1's `manifest.Manifest`, `manifest.Write`, `manifest.Origin*`.
- Produces: nothing new exported. The manifest file itself is the contract Tasks 4–10 read.

- [ ] **Step 1: Write the failing test**

Append to `internal/spawn/spawn_test.go`:

```go
// What spawn mounted is what the manifest says — including the repo given on
// the command line, which is declared in no file at all and which no later
// re-derivation could ever find.
func TestSpawnWritesTheManifestOfWhatItMounted(t *testing.T) {
	denHome := t.TempDir()
	// … build the same fixture the neighbouring create-branch tests use
	// (writeConfig/writeStack/writeNest + a real git repo), spawn with
	// -w feature/12 and one positional repo.

	m, err := manifest.Read(denHome, "api.feature-12")
	if err != nil {
		t.Fatalf("the manifest must exist after a create: %v", err)
	}
	if m.Worktree == nil || m.Worktree.Branch != "feature/12" {
		t.Errorf("the branch as typed must survive flattening: %#v", m.Worktree)
	}
	var adhoc *manifest.Repo
	for i := range m.Repos {
		if m.Repos[i].Origin == manifest.OriginCommandLine {
			adhoc = &m.Repos[i]
		}
	}
	if adhoc == nil {
		t.Fatalf("the command-line repo must be recorded: %#v", m.Repos)
	}
	if !adhoc.Worktree {
		t.Errorf("under -w, den created this repo's worktree too: %#v", adhoc)
	}
}

// The manifest describes what THIS VM received at its create. Rewriting it on
// attach would destroy that reference — the same doctrine, and the same
// regression, as TestSpawnDoesNotRewriteTheMixinOfALiveSandbox.
func TestSpawnDoesNotRewriteTheManifestOfALiveSandbox(t *testing.T) {
	// spawn once against a fake reporting no sandbox, capture the file's
	// bytes, then spawn again against a fake reporting it live, and assert
	// the bytes are unchanged.
}
```

Model both bodies on the existing `TestSpawnMountsCommandLineRepos` and `TestSpawnDoesNotRewriteTheMixinOfALiveSandbox` in the same file — reuse their fixture helpers verbatim rather than inventing new ones.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/spawn/ -run TestSpawnWritesTheManifest -count=1`
Expected: FAIL — `reading manifest …: no such file or directory`.

- [ ] **Step 3: Build the manifest and write it on the create branch**

Add a helper at the bottom of `internal/spawn/spawn.go`:

```go
// manifestOf assembles the creation record from what spawn just did — NOT
// from what the configuration says. mounts is workspaces[:len(r.Repos)], the
// slice step 3 filled one entry per repo, in declaration order: those are the
// paths `sbx create` is about to receive, worktrees included, and recording
// anything else would put the file straight back into the business of
// re-deriving that it exists to end.
func manifestOf(sandboxName, nestRef, nestFile string, wt worktree.Name,
	r *nest.Resolved, mounts, gitDirs []string) manifest.Manifest {

	m := manifest.Manifest{
		Sandbox: sandboxName,
		Nest:    manifest.Nest{Ref: nestRef, File: nestFile},
		Repos:   make([]manifest.Repo, 0, len(r.Repos)),
		GitDirs: gitDirs,
	}
	if wt.Dir != "" {
		m.Worktree = &manifest.Worktree{
			Name:   wt.Dir,
			Branch: wt.Branch,
			Layout: r.WorktreeLayout,
			Root:   r.WorktreeRoot,
		}
	}
	for i, repo := range r.Repos {
		// The three origins are exclusive and ordered: AdHoc first, because a
		// positional never carries a key, and Key before the plain path,
		// because a key entry HAS a path by now (Resolve filled it) and would
		// otherwise be indistinguishable from a declared `path:`.
		origin := manifest.OriginPath
		switch {
		case repo.AdHoc:
			origin = manifest.OriginCommandLine
		case repo.Key != "":
			origin = manifest.OriginKey
		}
		m.Repos = append(m.Repos, manifest.Repo{
			Name:   repo.Name(),
			Origin: origin,
			Key:    repo.Key,
			Repo:   repo.Path,
			Mount:  mounts[i],
			// den created this directory iff it spawned under -w. That single
			// bit is what `den rm` consults before touching anything: a repo
			// mounted as-is is the user's own working directory.
			Worktree: wt.Dir != "",
		})
	}
	return m
}
```

Then, in the `else` (create) branch, **before** `agent.WriteMixin`:

```go
		// The creation record, written BEFORE `sbx create` (spec 2026-08-05
		// D3). The worktrees already exist at this point — step 3 created
		// them — so a `sbx create` that fails leaves directories on disk, and
		// this is the only position where that case still leaves a trace of
		// them. The accepted corollary is that a manifest can exist with no
		// sandbox; `den ls` and `den doctor` are what make that state
		// addressable.
		//
		// A write failure REFUSES, here, rather than being warned about: den
		// has just printed the path of every worktree it created
		// (`worktree %s: %s` above), so the refusal names them and the user
		// is not additionally left with a VM to destroy.
		if err := manifest.Write(r.DenHome, manifestOf(
			sandboxName, o.Nest, nest.FilePath(nestRoot, bareNest),
			worktreeName, r, workspaces[:len(r.Repos)], gitDirs,
		)); err != nil {
			return err
		}
```

Add `"github.com/PillowPillow/den/internal/manifest"` to the imports.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/spawn/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/spawn/
git commit -m "feat(spawn): the create branch leaves a record of what it mounted"
```

---

### Task 4: `den rm` replays the manifest

**Files:**
- Modify: `internal/cli/rm.go`
- Test: `internal/cli/rm_test.go`

**Interfaces:**
- Consumes: `manifest.Read`, `manifest.Remove`, `manifest.Path`, `worktree.Target.WorktreePath`.
- Produces: `cli.cleanFromManifest(ctx, home string, m manifest.Manifest, g worktree.Git, force bool, out io.Writer) error` — used by Task 7's `--fix` as well.

- [ ] **Step 1: Write the failing tests**

Append to `internal/cli/rm_test.go` (fixture style copied from `TestRmKeepWorktreesLeavesDiskUntouched`):

```go
// writeManifest drops a creation record for a sandbox, the way spawn would
// have. Tests that need one build it here rather than running a spawn: the rm
// path must be assertable on a record whose configuration no longer exists.
func writeManifest(t *testing.T, denHome string, m manifest.Manifest) {
	t.Helper()
	if err := manifest.Write(denHome, m); err != nil {
		t.Fatal(err)
	}
}

// Proof 3 — the hole this feature exists to close. A repo mounted from the
// command line is declared in NO file: before the manifest, its worktree was
// never reclaimed and nothing said so.
func TestRmReclaimsACommandLineRepoWorktree(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
	repo := filepath.Join(t.TempDir(), "hotfix")
	createTestRepo(t, repo)
	wt := createWorktree(t, repo, filepath.Join(denHome, "worktrees", "feat12", "hotfix"), "feat12")

	writeManifest(t, denHome, manifest.Manifest{
		Sandbox:  "api.feat12",
		Nest:     manifest.Nest{Ref: "api", File: filepath.Join(denHome, "nests", "api.yaml")},
		Worktree: &manifest.Worktree{Name: "feat12", Branch: "feat12", Layout: "central", Root: filepath.Join(denHome, "worktrees")},
		Repos: []manifest.Repo{{
			Name: "hotfix", Origin: manifest.OriginCommandLine,
			Repo: repo, Mount: wt, Worktree: true,
		}},
	})
	f := &sbx.Fake{Responses: lsWith("api.feat12")}

	if _, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("the command-line worktree must be reclaimed: %v", err)
	}
	if _, err := manifest.Read(denHome, "api.feat12"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the manifest must be removed once everything it lists is reclaimed: %v", err)
	}
}

// Proof 8 — a repo mounted as-is is the user's own working directory. den does
// not dispose of it, and `worktree: false` is the bit that says so.
func TestRmNeverTouchesARepoMountedAsIs(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
	repo := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)

	writeManifest(t, denHome, manifest.Manifest{
		Sandbox: "api",
		Nest:    manifest.Nest{Ref: "api", File: filepath.Join(denHome, "nests", "api.yaml")},
		Repos: []manifest.Repo{{
			Name: "api", Origin: manifest.OriginPath, Repo: repo, Mount: repo, Worktree: false,
		}},
	})
	f := &sbx.Fake{Responses: lsWith("api")}

	if _, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(repo); err != nil {
		t.Errorf("a repo mounted as-is must survive rm: %v", err)
	}
}

// Proof 4 — the nest is gone, and it no longer matters: the record does not
// need it.
func TestRmCleansUpEvenWhenTheNestWasDeleted(t *testing.T) {
	// same shape, with NO nests/api.yaml written at all, and an assertion
	// that the worktree left its place and no "nest unreadable" warning was
	// printed.
}

// Proof 5 — worktree_root moved in config.yaml between the spawn and the rm.
// The recorded mount still names the real directory.
func TestRmReclaimsTheOriginalDirectoryAfterWorktreeRootMoved(t *testing.T) {
	// write config.yaml with worktree_root: <somewhere-else>, record a mount
	// under the ORIGINAL root, assert the original directory is reclaimed.
}

// Proof 6 — a key unmapped since the spawn. rm.go used to abandon the
// directory with a warning; the record carries the path itself.
func TestRmReclaimsAWorktreeWhoseKeyIsNoLongerMapped(t *testing.T) {
	// config.yaml with an EMPTY repos: mapping, manifest entry with
	// Origin: OriginKey, Key: "api" and a real Mount. Assert reclaim, and that
	// stderr says nothing about an unmapped key.
}

// Proof 7 — --without at spawn. The record holds the repos this spawn really
// mounted, so a repo it excluded is not reclaimed by association.
func TestRmDoesNotReclaimARepoTheSpawnExcluded(t *testing.T) {
	// nest declaring two repos, manifest listing only one, assert the second
	// repo's would-be worktree directory (created by the test) survives.
}

// --keep-worktrees keeps the RECORD too: the directories survive, and doctor
// must still be able to find them.
func TestRmKeepWorktreesKeepsTheManifest(t *testing.T) {
	// as TestRmKeepWorktreesLeavesDiskUntouched, plus a manifest.Read that
	// must still succeed afterwards, and stdout naming the record's path.
}
```

Add a `createWorktree(t, repo, path, branch string) string` helper next to `createTestRepo` if the file has none — it should call `worktree.Ensure` with a real `worktree.NewGit()`, exactly as the existing dirty-worktree tests build their fixtures.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/cli/ -run 'TestRmReclaims|TestRmNeverTouchesARepo|TestRmCleansUpEven|TestRmDoesNot|TestRmKeepWorktreesKeeps' -count=1`
Expected: FAIL — the worktrees survive, and `manifest.Read` still succeeds after rm.

- [ ] **Step 3: Replay the manifest in rm.go**

Rename today's `cleanWorktrees` body to `cleanWorktreesLegacy` (unchanged), and introduce the manifest path:

```go
// cleanWorktrees reclaims what den created for this sandbox.
//
// It REPLAYS the creation record (internal/manifest) rather than re-deriving
// it: the derivation assumed today's configuration still describes yesterday's
// spawn, and it aimed elsewhere the moment that stopped being true — a
// `worktree_root` moved, a key unmapped, a nest deleted, a `--without` at
// spawn, or a repo given on the command line that is declared in no file at
// all and was simply never reclaimed.
//
// Without a usable record it falls back on that derivation, saying so
// (cleanWorktreesLegacy). Never a refusal: a `den rm` that refuses leaves the
// user with a live VM they can no longer destroy (doctrine T13/T16).
func cleanWorktrees(ctx context.Context, home, ref, sandboxName string, g worktree.Git, force bool, out, warnW io.Writer) error {
	if err := sbx.ValidateSandboxName(sandboxName); err != nil {
		return fmt.Errorf("cleaning up worktrees: %w", err)
	}
	m, err := manifest.Read(home, sandboxName)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// Legacy sandbox: created before den kept records, or outside den.
		// Mentioned, not warned about — it is an ordinary state, and this
		// path dies on its own as old sandboxes disappear.
		fmt.Fprintf(warnW, "sandbox %s has no creation record: falling back on the nest and "+
			"config.yaml to locate its worktrees, which is only accurate if neither changed "+
			"since the spawn\n", sandboxName)
		return cleanWorktreesLegacy(ctx, home, ref, sandboxName, g, force, out, warnW)
	case err != nil:
		fmt.Fprintf(warnW, "%v — falling back on the nest and config.yaml to locate the "+
			"worktrees of %s\n", err, sandboxName)
		return cleanWorktreesLegacy(ctx, home, ref, sandboxName, g, force, out, warnW)
	}
	return cleanFromManifest(ctx, home, m, g, force, out)
}

// cleanFromManifest reclaims exactly the directories den recorded creating.
// Shared with `den doctor --fix`, which reclaims the same set for a sandbox
// whose VM is already gone — one body, so the two can never disagree on what
// den is allowed to move.
func cleanFromManifest(ctx context.Context, home string, m manifest.Manifest, g worktree.Git, force bool, out io.Writer) error {
	for _, r := range m.Repos {
		// The one bit that matters: den only ever reclaims what it created.
		// A repo mounted as-is is the user's own working directory.
		if !r.Worktree {
			continue
		}
		// Layout, Root and the worktree NAME still come from the record, not
		// from config.yaml: worktree.Remove needs them for its trash fallback
		// and its parent-directory cleanup, and reading them from today's
		// config would reintroduce the drift the recorded Mount just removed.
		var layout, root, wt string
		if m.Worktree != nil {
			layout, root, wt = m.Worktree.Layout, m.Worktree.Root, m.Worktree.Name
		}
		// One deadline PER repo, not one for the whole loop: a broken repo
		// must not eat the budget of the next ones.
		repoCtx, cancel := context.WithTimeout(ctx, gitProbeTimeout)
		dest, err := worktree.Remove(repoCtx, g, worktree.Target{
			DenHome:      home,
			Layout:       layout,
			Root:         root,
			Nest:         m.Sandbox,
			Worktree:     wt,
			RepoPath:     r.Repo,
			WorktreePath: r.Mount,
			Force:        force,
		})
		cancel()
		if err != nil {
			// The record SURVIVES a failed reclaim, deliberately: it is the
			// only trace of the directories still on disk, and deleting it
			// here would strand them permanently.
			return err
		}
		if dest != "" {
			fmt.Fprintf(out, "worktree moved to trash: %s\n", dest)
		}
	}
	// Removed only now, when everything it listed is reclaimed.
	return manifest.Remove(home, m.Sandbox)
}
```

In `newRmCmd`, the `--keep-worktrees` branch must say what it keeps:

```go
			if keepWorktrees {
				// The record survives with the directories. Without it the
				// worktrees would be unreclaimable by anything but hand — and
				// `den doctor` is precisely what will offer to finish the job.
				if path, err := manifest.Path(home, name); err == nil {
					if _, err := os.Stat(path); err == nil {
						fmt.Fprintf(out, "worktrees kept, and so is their record (%s): "+
							"`den doctor` will report them, `den doctor --fix` reclaims them\n", path)
					}
				}
			} else if err := cleanWorktrees(...); err != nil {
				return err
			}
```

Note the ordering constraint that does not change: worktrees are reclaimed BEFORE `sbx rm`, so a dirty one stops everything while the VM still exists.

Add `errors`, `os` and the `manifest` import to `rm.go`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/cli/ -count=1`
Expected: PASS — including every pre-existing rm test, which now runs through the legacy fallback and sees one extra warning line on stderr. Adjust only assertions that compare stderr *exactly*; do not weaken assertions on stdout.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/
git commit -m "feat(rm): den reclaims what it recorded creating, not what config implies today"
```

---

### Task 5: the fallback is proven, both ways

**Files:**
- Test: `internal/cli/rm_test.go`
- Modify: `internal/cli/rm.go` only if a proof fails.

**Interfaces:**
- Consumes: Task 4's `cleanWorktrees` / `cleanWorktreesLegacy`.
- Produces: nothing.

- [ ] **Step 1: Write the failing tests**

```go
// Proof 9 — a sandbox from before this feature. The old derivation still
// runs, and the user is told the answer is only as good as an unchanged
// configuration.
func TestRmFallsBackAndSaysSoWithoutAManifest(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	repo := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)
	writeNest(t, denHome, "api", "stack: devx\nrepos:\n  - { path: "+repo+" }\n")
	wt := createWorktree(t, repo, filepath.Join(denHome, "worktrees", "feat12", "api"), "feat12")
	f := &sbx.Fake{Responses: lsWith("api.feat12")}

	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("the legacy derivation must still reclaim the worktree: %v", err)
	}
	if !strings.Contains(out, "no creation record") {
		t.Errorf("the fallback must be announced; got: %s", out)
	}
}

// Proof 10 — a corrupt record must never block. The file is named, the
// derivation takes over, and above all the VM is destroyed: a `den rm` that
// refuses leaves a live sandbox nobody can remove (doctrine T13/T16).
func TestRmWarnsAndStillDestroysOnACorruptManifest(t *testing.T) {
	// same fixture, plus a state/sandboxes/api.feat12.yaml containing "{".
	// Assert: the file's path appears in the output, the worktree is
	// reclaimed through the derivation, and f.HasCalled("rm", "--force",
	// "api.feat12").
}
```

`executeCmdWithSbx` returns combined output in this package — check its signature at the top of `root_test.go` and read stderr the same way the neighbouring warning assertions do.

- [ ] **Step 2: Run the tests to verify they fail (or pass for the right reason)**

Run: `go test ./internal/cli/ -run 'TestRmFallsBack|TestRmWarnsAndStill' -count=1`
Expected: FAIL on the message assertions if the wording drifted; PASS on the reclaim assertions, since Task 4 already routes both cases to the derivation. A test that passes immediately is still worth keeping — it is what stops a later refactor from turning either case into a refusal.

- [ ] **Step 3: Fix the wording if needed**

Only if Step 2 failed: align the two `fmt.Fprintf(warnW, …)` messages in `cleanWorktrees` with what the tests assert. Do not weaken the tests to match the code.

- [ ] **Step 4: Run and commit**

Run: `task check`

```bash
git add internal/cli/
git commit -m "test(rm): a missing or corrupt record degrades, and never strands a live VM"
```

---

### Task 6: `doctor.Orphans` — the pure verdict

**Files:**
- Modify: `internal/doctor/doctor.go`
- Test: `internal/doctor/doctor_test.go`

**Interfaces:**
- Consumes: `manifest.Manifest`.
- Produces:
  - `doctor.LiveSandboxes{Known bool; Names []string}`
  - `doctor.Orphan{Sandbox string; Worktrees []string}`
  - `doctor.Orphans(live LiveSandboxes, manifests []manifest.Manifest) []Orphan`
  - `doctor.OrphanCheck(live LiveSandboxes, manifests []manifest.Manifest) Check`

- [ ] **Step 1: Write the failing test**

```go
// A record with no live sandbox is an orphan: `sbx rm` run outside den, a
// failed boot, or a `den rm --keep-worktrees`. Only the directories den
// created are named — a repo mounted as-is belongs to the user.
func TestOrphansNamesRecordsWithoutALiveSandbox(t *testing.T) {
	live := doctor.LiveSandboxes{Known: true, Names: []string{"web"}}
	ms := []manifest.Manifest{
		{Sandbox: "api.feat12", Repos: []manifest.Repo{
			{Name: "api", Mount: "/wt/feat12/api", Worktree: true},
			{Name: "hotfix", Mount: "/tmp/hotfix", Worktree: false},
		}},
		{Sandbox: "web", Repos: []manifest.Repo{{Name: "web", Mount: "/wt/web", Worktree: true}}},
	}
	got := doctor.Orphans(live, ms)
	if len(got) != 1 || got[0].Sandbox != "api.feat12" {
		t.Fatalf("only the sandbox with no live VM is an orphan: %#v", got)
	}
	if !reflect.DeepEqual(got[0].Worktrees, []string{"/wt/feat12/api"}) {
		t.Errorf("only den-created directories are named: %#v", got[0].Worktrees)
	}
}

// Proof 14 — with sbx absent the live list is UNKNOWN, and every healthy
// sandbox would look like an orphan. The check is skipped and says so; the sbx
// line above it already carries the real problem.
func TestOrphanCheckIsSkippedWhenTheLiveListIsUnknown(t *testing.T) {
	c := doctor.OrphanCheck(doctor.LiveSandboxes{}, []manifest.Manifest{{Sandbox: "api"}})
	if c.Blocking() || c.Level != doctor.LevelOK {
		t.Errorf("an unknown live list must not accuse anyone: %#v", c)
	}
	if !strings.Contains(c.Detail, "skipped") {
		t.Errorf("the skip must be visible: %q", c.Detail)
	}
}

// An orphan is a warning, never a failure: leftover directories are not a
// broken installation, and turning `den doctor` red over them would train the
// user to ignore it.
func TestOrphanCheckWarnsAndNamesTheRemedy(t *testing.T) {
	c := doctor.OrphanCheck(
		doctor.LiveSandboxes{Known: true},
		[]manifest.Manifest{{Sandbox: "api.feat12", Repos: []manifest.Repo{
			{Mount: "/wt/feat12/api", Worktree: true}}}})
	if c.Level != doctor.LevelWarning {
		t.Errorf("orphans warn, they do not fail: %#v", c)
	}
	if !strings.Contains(c.Detail, "den doctor --fix") {
		t.Errorf("the remedy must be named: %q", c.Detail)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/doctor/ -run Orphan -count=1`
Expected: FAIL — `undefined: doctor.Orphans`.

- [ ] **Step 3: Implement, purely**

Append to `internal/doctor/doctor.go`:

```go
// LiveSandboxes answers "which sandboxes are alive" — INCLUDING the case
// where the question could not be asked. A bare []string cannot hold that:
// empty would mean "none alive", and every healthy sandbox would then be
// reported as an orphan the moment sbx is missing from PATH.
//
// This package never asks the question itself. internal/cli owns deps.Sbx and
// answers it, exactly as it owns the mutation in `--fix`: doctor stays what
// its package comment says it is — no side effects, no network.
type LiveSandboxes struct {
	Known bool
	Names []string
}

// Orphan is a creation record whose sandbox is gone, with the directories den
// created for it and never reclaimed.
type Orphan struct {
	Sandbox   string
	Worktrees []string
}

// Orphans is a PURE function: given the live list and the records read off
// disk, it says which records no longer have a VM. Deliberately no IO — that
// is what lets `den ls`, `den doctor` and `den doctor --fix` share one verdict
// instead of three that could disagree about what den is allowed to move.
//
// An unknown live list yields NOTHING, rather than everything: see
// LiveSandboxes.
func Orphans(live LiveSandboxes, manifests []manifest.Manifest) []Orphan {
	if !live.Known {
		return nil
	}
	alive := make(map[string]bool, len(live.Names))
	for _, n := range live.Names {
		alive[n] = true
	}
	var out []Orphan
	for _, m := range manifests {
		if alive[m.Sandbox] {
			continue
		}
		o := Orphan{Sandbox: m.Sandbox}
		for _, r := range m.Repos {
			// Only what den created. A repo mounted as-is is the user's own
			// working directory and has no business in a cleanup list.
			if r.Worktree {
				o.Worktrees = append(o.Worktrees, r.Mount)
			}
		}
		out = append(out, o)
	}
	return out
}

// OrphanCheck renders the verdict as a diagnostic. A WARNING, not a failure:
// leftover directories are a legitimate state — a `den rm --keep-worktrees`
// produces one on purpose — and a `den doctor` that exits non-zero over them
// teaches the user to stop reading it.
func OrphanCheck(live LiveSandboxes, manifests []manifest.Manifest) Check {
	if !live.Known {
		return Check{Name: "orphans", Level: LevelOK,
			Detail: "skipped: den could not list live sandboxes, so a record without a VM " +
				"cannot be told apart from a healthy one — see the sbx line above"}
	}
	orphans := Orphans(live, manifests)
	if len(orphans) == 0 {
		return Check{Name: "orphans", Level: LevelOK, Detail: "none"}
	}
	var parts []string
	for _, o := range orphans {
		parts = append(parts, fmt.Sprintf("%s (%d worktree(s))", o.Sandbox, len(o.Worktrees)))
	}
	return Check{Name: "orphans", Level: LevelWarning, Detail: fmt.Sprintf(
		"%s: recorded by den but no live sandbox — the worktrees are still on disk; "+
			"reclaim them with `den doctor --fix` (add --force if one carries uncommitted changes)",
		strings.Join(parts, ", "))}
}
```

Add `"github.com/PillowPillow/den/internal/manifest"` to the imports.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/doctor/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/doctor/
git commit -m "feat(doctor): a pure verdict on records whose sandbox is gone"
```

---

### Task 7: `den doctor` reports orphans and `--fix` reclaims them

**Files:**
- Modify: `internal/cli/doctor.go`
- Modify: `internal/cli/root.go:120` (the `newDoctorCmd` call)
- Test: `internal/cli/doctor_test.go`

**Interfaces:**
- Consumes: `doctor.LiveSandboxes`, `doctor.Orphans`, `doctor.OrphanCheck`, `manifest.List`, `manifest.Remove`, Task 4's `cleanFromManifest`.
- Produces: `newDoctorCmd(denHome *string, deps doctor.Deps, runner sbx.Runner, g worktree.Git) *cobra.Command`.

- [ ] **Step 1: Write the failing tests**

```go
// Proof 12 — a `den rm --keep-worktrees` leaves a record on purpose, and
// doctor is what makes it addressable.
func TestDoctorReportsAnOrphanRecord(t *testing.T) {
	// denHome with minimalConfig + a manifest for "api.feat12", sbx.Fake
	// listing NO sandbox. Assert the output holds "[warn]", "api.feat12" and
	// "den doctor --fix", and that the command still exits 0.
}

// --fix sends the orphaned worktrees to the trash and only then drops the
// record. Same strictness as rm: den never deletes, it moves.
func TestDoctorFixReclaimsOrphanedWorktrees(t *testing.T) {
	// real repo + real worktree recorded in the manifest, sbx.Fake listing
	// nothing. Assert the directory is gone, the trash entry is announced,
	// and manifest.Read now returns os.ErrNotExist.
}

// Proof 13 — a dirty worktree stops --fix, exactly as it stops rm. --force is
// the same consent, with the same effect: the trash, never deletion.
func TestDoctorFixRefusesADirtyWorktreeUnlessForced(t *testing.T) {
	// write an uncommitted file in the worktree; assert `doctor --fix` errors
	// and leaves BOTH the directory and the record in place, then that
	// `doctor --fix --force` moves it to the trash.
}

// Proof 14, end to end: sbx unreachable means the live list is unknown, and
// den must not accuse a healthy sandbox.
func TestDoctorSkipsTheOrphanCheckWhenSbxCannotAnswer(t *testing.T) {
	// sbx.Fake scripted to fail `ls --json`; assert the output contains
	// "skipped" and never names the sandbox.
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/cli/ -run TestDoctor -count=1`
Expected: FAIL — `unknown flag: --fix`.

- [ ] **Step 3: Wire the command**

In `internal/cli/doctor.go`:

```go
func newDoctorCmd(denHome *string, deps doctor.Deps, runner sbx.Runner, g worktree.Git) *cobra.Command {
	var fix, force bool

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose den's configuration and environment",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			…
			checks := doctor.Run(home, deps)

			// The live list is read HERE, not in internal/doctor: that package
			// promises "no side effects, no network" in its very first line and
			// never runs sbx. cli already owns deps.Sbx — and already carries
			// the mutation for `den rm` — so the boundary stays exactly where
			// it was.
			//
			// An error is NOT a failure: it means the answer is unknown, which
			// LiveSandboxes models explicitly so that a missing sbx skips the
			// check instead of reporting every healthy sandbox as an orphan.
			live := doctor.LiveSandboxes{}
			if boxes, err := sbx.Ls(cmd.Context(), runner); err == nil {
				live.Known = true
				live.Names = liveNames(boxes)
			}
			manifests, broken, err := manifest.List(home)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "reading creation records: %v\n", err)
			}
			for _, b := range broken {
				fmt.Fprintf(cmd.ErrOrStderr(), "creation record %s unreadable: %v\n", b.Path, b.Err)
			}
			checks = append(checks, doctor.OrphanCheck(live, manifests))

			… // existing rendering loop, unchanged

			// --fix runs AFTER the report: the user sees what den is about to
			// touch on the same screen, and a diagnostic that mutates before
			// printing would be unreadable when it fails halfway.
			if fix {
				if err := reclaimOrphans(cmd.Context(), home, doctor.Orphans(live, manifests),
					manifests, g, force, cmd.OutOrStdout()); err != nil {
					return err
				}
			}
			…
		},
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "reclaim the worktrees of sandboxes that no longer exist")
	cmd.Flags().BoolVar(&force, "force", false,
		"reclaim them even when they carry uncommitted changes")
	return cmd
}

// reclaimOrphans sends each orphan's worktrees to the trash, through the same
// body `den rm` uses (cleanFromManifest): one definition of what den is
// allowed to move, so the two commands can never diverge on it.
//
// The first refusal stops the loop and is returned. That is deliberate: a
// dirty worktree is the user's uncommitted work, and continuing past it would
// bury the one message they need under the reclaim lines of the next sandbox.
func reclaimOrphans(ctx context.Context, home string, orphans []doctor.Orphan,
	manifests []manifest.Manifest, g worktree.Git, force bool, out io.Writer) error {

	byName := make(map[string]manifest.Manifest, len(manifests))
	for _, m := range manifests {
		byName[m.Sandbox] = m
	}
	for _, o := range orphans {
		fmt.Fprintf(out, "\nreclaiming %s...\n", o.Sandbox)
		if err := cleanFromManifest(ctx, home, byName[o.Sandbox], g, force, out); err != nil {
			return err
		}
	}
	return nil
}
```

Update `internal/cli/root.go:120`:

```go
	root.AddCommand(newDoctorCmd(&denHome, deps.Doctor, deps.Sbx, deps.Git))
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/cli/ -count=1`
Expected: PASS. Existing doctor tests gain one `[ok  ] orphans` line — update their expected output rather than loosening their assertions.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/
git commit -m "feat(doctor): orphaned records are reported, and --fix reclaims them"
```

---

### Task 8: `den ls` signals orphans

**Files:**
- Modify: `internal/cli/ls.go`
- Test: `internal/cli/ls_test.go`

**Interfaces:**
- Consumes: `manifest.List`, `doctor.Orphans`, `doctor.LiveSandboxes`.
- Produces: nothing new exported.

- [ ] **Step 1: Write the failing tests**

```go
// Proof 11 — `sbx create` failed after the worktrees existed. The record is
// the only trace, and `den ls` is where the user looks.
func TestLsSignalsAnOrphanedRecord(t *testing.T) {
	// manifest for "api.feat12" with one worktree: true; sbx.Fake listing
	// only "web". Assert the output names api.feat12 AND the mount path.
}

// `den ls` is the command you type when everything is broken. It must never
// fail over its own extra: fail-open, strictly.
func TestLsSurvivesACorruptRecord(t *testing.T) {
	// a state/sandboxes/x.yaml containing "{" plus one live sandbox; assert
	// no error, and that the live sandbox is still listed.
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/cli/ -run TestLs -count=1`
Expected: FAIL — the orphan line is absent.

- [ ] **Step 3: Add the line**

In `internal/cli/ls.go`, after `w.Flush()` (and BEFORE the `len(boxes) == 0` early return is reached — move that return so the orphan scan still runs when nothing is live, since "no live sandbox but four recorded worktrees" is exactly the state worth reporting):

```go
// reportOrphans names the sandboxes den recorded creating that no longer have
// a VM, and the directories they left behind.
//
// FAIL-OPEN, strictly: `den ls` is the command a user types when everything
// else is broken, and it must never fail over its own extra. A record that
// cannot be read is skipped in silence here — `den doctor` is the command that
// names it, and it is the one with a remedy to offer.
//
// The comparison is free: this command already holds both the live list and
// den home.
func reportOrphans(out io.Writer, home string, boxes []sbx.Sandbox) {
	manifests, _, err := manifest.List(home)
	if err != nil {
		return
	}
	orphans := doctor.Orphans(doctor.LiveSandboxes{Known: true, Names: liveNames(boxes)}, manifests)
	if len(orphans) == 0 {
		return
	}
	fmt.Fprintln(out)
	for _, o := range orphans {
		if len(o.Worktrees) == 0 {
			// The record outlived its sandbox but den created nothing for it:
			// worth naming, since `den doctor --fix` will drop the record, but
			// there is no directory to point at.
			fmt.Fprintf(out, "orphan: %s — no live sandbox (nothing left on disk)\n", o.Sandbox)
			continue
		}
		fmt.Fprintf(out, "orphan: %s — no live sandbox, worktrees still on disk: %s\n",
			o.Sandbox, strings.Join(o.Worktrees, ", "))
	}
	fmt.Fprintln(out, "reclaim them with `den doctor --fix`")
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/cli/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/cli/
git commit -m "feat(ls): a record without a sandbox is named, not silently kept"
```

---

### Task 9: `den ls` shows the branch as typed and the prefixed nest

**Files:**
- Modify: `internal/cli/ls.go`
- Test: `internal/cli/ls_test.go`

**Interfaces:**
- Consumes: `manifest.List`.
- Produces: nothing new exported.

- [ ] **Step 1: Write the failing test**

```go
// Flattening is lossy: `-w feature/12` becomes the sandbox api.feat-12, and
// before the record there was nowhere left to read "feature/12" from. The NEST
// column has the same problem in reverse — a source nest spawns as "corp-api"
// while the user only ever typed "corp:api".
func TestLsShowsTheBranchAsTypedAndThePrefixedNest(t *testing.T) {
	// manifest for the LIVE sandbox "corp-api.feat-12" with
	// Nest.Ref "corp:api" and Worktree.Branch "feature/12".
	// Assert both strings appear in the table.
}

// Fail-open: with no record den shows exactly what it showed before.
func TestLsFallsBackToTheSandboxNameWithoutARecord(t *testing.T) {
	// live "api.feat12", no manifest at all; assert "feat12" is displayed and
	// no error is returned.
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/cli/ -run TestLsShows -count=1`
Expected: FAIL — the table shows `feat-12` and `corp-api`.

- [ ] **Step 3: Read the record for display**

In `newLsCmd`, before the tabwriter loop, index the manifests by sandbox name (reusing the `manifest.List` call Task 8 added — call it once and pass the slice to both users), then inside the loop:

```go
			// The record, when there is one, is the ONLY place these two
			// strings survive: flattening rewrote the branch on its way into
			// the sandbox name, and the ":" of a source reference is not in
			// sbx's --name charset. Fail-open — without a record the columns
			// show what they have always shown, the flattened forms.
			if m, ok := recorded[b.Name]; ok {
				if m.Nest.Ref != "" {
					nestName = m.Nest.Ref
				}
				if m.Worktree != nil && m.Worktree.Branch != "" {
					wt = m.Worktree.Branch
				}
			}
```

The `declared[nestName]` marking must keep using the sandbox-derived name, not the reference: `declared` is keyed by the names of files under `<denHome>/nests`, and a source reference is not one of them — marking it `?` would flag every source sandbox as undeclared.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/cli/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/cli/
git commit -m "feat(ls): the branch as typed, and the nest reference the user actually wrote"
```

---

### Task 10: attach separates "the nest changed" from "the VM lacks it"

**Files:**
- Modify: `internal/spawn/spawn.go` (attach branch, around line 636)
- Test: `internal/spawn/spawn_test.go`

**Interfaces:**
- Consumes: `manifest.Read`.
- Produces: `reportNestChangedSinceCreation(out io.Writer, sandboxName string, recorded, expected []string)` (unexported).

- [ ] **Step 1: Write the failing test**

```go
// Attaching compares two different things that used to be conflated: what the
// configuration says today versus what the VM mounts (reportUnmountedRepos),
// and what the configuration says today versus what den ACTUALLY mounted at
// creation. Only the second has an honest remedy — den never remounts anything
// on a live VM, so the answer is `den rm` then respawn.
func TestAttachReportsANestChangedSinceCreation(t *testing.T) {
	// spawn once (create branch) with one repo; then add a second repo to the
	// nest and spawn again against a fake reporting the sandbox live with its
	// ORIGINAL workspaces. Assert the output says the nest changed since
	// creation and names `den rm`.
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/spawn/ -run TestAttachReportsANestChanged -count=1`
Expected: FAIL — only the existing "does not fully match" warning appears.

- [ ] **Step 3: Report it**

In the `live != nil` branch, before `reportUnmountedRepos`:

```go
			// Two DIFFERENT drifts used to arrive as one. reportUnmountedRepos
			// compares today's configuration to what the VM mounts, which
			// fires both when the VM is missing something and when the nest
			// itself was edited since — indistinguishable, and only the second
			// has a remedy den can honestly name, since nothing is ever
			// remounted on a live VM.
			//
			// Read, not required: a sandbox created before records existed has
			// none, and attaching to it must keep working exactly as before.
			if recorded, err := manifest.Read(r.DenHome, sandboxName); err == nil {
				mounts := make([]string, 0, len(recorded.Repos))
				for _, rr := range recorded.Repos {
					mounts = append(mounts, rr.Mount)
				}
				reportNestChangedSinceCreation(d.Out, sandboxName, mounts, workspaces[:len(r.Repos)])
			}
```

And the helper, next to `reportUnmountedRepos`:

```go
// reportNestChangedSinceCreation warns when the repos the configuration now
// resolves to are not the ones den mounted when it created this sandbox.
//
// The remedy is named because there is one and it is the only one: den does
// not touch a live VM's mounts, so the configuration takes effect at the next
// create, not at this attach. Silence here would let the user keep working in
// a sandbox that quietly does not match the nest they just edited.
func reportNestChangedSinceCreation(out io.Writer, sandboxName string, recorded, expected []string) {
	if slices.Equal(recorded, expected) {
		return
	}
	fmt.Fprintf(out,
		"nest changed since sandbox %s was created: it was created with %s, the configuration "+
			"now resolves to %s — a live sandbox keeps its create-time mounts, so this takes "+
			"effect after `den rm %s` and a respawn\n",
		sandboxName, strings.Join(recorded, ", "), strings.Join(expected, ", "), sandboxName)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/spawn/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/spawn/
git commit -m "feat(spawn): attach tells an edited nest apart from a VM that lacks one"
```

---

### Task 11: the documentation says `state/` exists and is never purged

**Files:**
- Modify: `docs/superpowers/specs/2026-07-27-den-cli-design.md:89` (§3 layout)
- Modify: `README.md`
- Modify: `CLAUDE.md` ("Stale artifacts" section)

**Interfaces:** none.

- [ ] **Step 1: Add `state/` to spec §3**

In the layout block, after the `cache/` lines:

```
  state/                   # trace des créations — JAMAIS purgé automatiquement
    sandboxes/<sandbox>.yaml #   ce que den a réellement monté (spec 2026-08-05)
```

And below the block, next to the `lib/` paragraph, one sentence: `state/` n'est pas un cache — un repo monté depuis la ligne de commande n'est reconstructible depuis rien, et le fichier est la seule trace d'un worktree pouvant porter du travail non commité.

- [ ] **Step 2: Document the user-visible behaviour in README.md**

Add, in the section describing `den rm` (and next to the `den doctor` rows):

- `den rm` reclaims exactly the worktrees den created for that sandbox, including repos mounted from the command line.
- `--keep-worktrees` keeps the record too, so `den doctor` can still find the directories.
- `den doctor` reports records whose sandbox is gone; `den doctor --fix` reclaims them, `--force` when one is dirty.
- `~/.den/state/` holds those records and is never purged automatically.

- [ ] **Step 3: Update the CLAUDE.md architecture note**

Add one bullet under "Architecture", after the "Ports publish on demand only" paragraph:

> **What den mounted is recorded, not re-derived.** `internal/manifest` writes `<denHome>/state/sandboxes/<sandbox>.yaml` on the create branch, before `sbx create`; `den rm`, `den ls` and `den doctor` replay it. Every reader falls back on the old derivation when the file is absent or unreadable — `den rm` must never refuse and strand a live VM (doctrine T13/T16). `state/` is not `cache/`: it is never purged.

- [ ] **Step 4: Verify and commit**

Run: `task check`

```bash
git add README.md CLAUDE.md docs/
git commit -m "docs: state/ is a first-class directory, and rm says what it reclaims"
```

---

## Self-Review

**Spec coverage.**

| Spec | Task |
|---|---|
| D1 location, one file per sandbox, strict decode, 0700/0600 | 1 |
| D2 content and field meanings, no timestamp | 1 |
| D3 written once, create branch only, before `sbx create`, failure = refusal | 3 |
| D4 rm replays; explicit path wins; `worktree: false` untouched; record removed; `--keep-worktrees` keeps it | 2, 4 |
| D5 absent/corrupt → warn + fallback, never blocking | 4, 5 |
| D6 `den ls` signals, `den doctor` reports, `--fix` reclaims, purity boundary, skip when sbx is silent | 6, 7, 8 |
| D7 `den ls` display; attach separates the two drifts | 9, 10 |
| Forme: leaf package, single `Path` definition, no `net`/`hash/fnv`/`os/exec` | 1 (+ constraint block) |
| Proofs 1–2 | 1 |
| Proofs 3–8 | 4 |
| Proofs 9–10 | 5 |
| Proof 11 | 8 |
| Proofs 12–14 | 7 |

**Known gaps, stated rather than hidden.** Tasks 3, 4 (partly), 7, 8, 9 and 10 give test *bodies* as prose for fixtures that already exist in those files (`TestSpawnMountsCommandLineRepos`, `TestRmKeepWorktreesLeavesDiskUntouched`, the doctor and ls suites). Reproducing thirty-line fixture setups five times would drift from the real helpers, which are the source of truth for how a test builds a den home in this repo. Every such step names the existing test to copy from and every assertion is spelled out in full — what is left to the implementer is the fixture, not the behaviour under test.

**Type consistency.** `manifest.Manifest.Worktree` is `*Worktree` everywhere it is read (`m.Worktree != nil` guards in Tasks 4, 6, 9). `manifest.Repo.Worktree` is the `bool` (Tasks 4, 6, 8). `worktree.Target.WorktreePath` (Task 2) is the field Tasks 4 and 7 set from `manifest.Repo.Mount`. `doctor.LiveSandboxes{Known, Names}` is built identically in Tasks 7 and 8. `cleanFromManifest` (Task 4) is the single body Task 7 calls.
