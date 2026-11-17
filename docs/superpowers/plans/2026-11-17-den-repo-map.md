# Per-instance repo map Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** At every instance creation, den writes a map of the instance's repos into the VM: Claude loads it from `~/.claude/rules/den-instance.md`, scripts read it as JSON through `DEN_REPOS`.

**Architecture:** `internal/sbxenv` renders the map (pure: a `RepoMap` value to JSON and to Markdown, goldens). `internal/nest` exposes the machine's `repos:` keys. `internal/instance` refuses a user `DEN_REPOS`, builds the `RepoMap` in `Create` after the worktrees exist, writes both files under the instance's kit (`files/home/…`), and exports `DEN_REPOS` in the rendered `sbxenv.yaml`.

**Tech Stack:** Go (stdlib `encoding/json`), sbx kits (`files/home/` delivery), `task check`.

**Spec:** `docs/superpowers/specs/2026-11-17-den-repo-map-design.md`, validated by `docs/superpowers/specs/2026-11-17-den-repo-map-validation.md` (the validation record wins over the design).

## Global Constraints

- Never write `~/.claude/CLAUDE.md` or `~/.claude/settings.json` in the VM (they belong to the profile kit). den adds only `~/.claude/rules/den-instance.md` and `~/.den/repos.json`.
- The map is written only under the instance's gen dir (`<denHome>/gen/<instance>/…`), never under a mounted folder.
- The Markdown names neither Claude, nor a dev home, nor `repos.local.json`.
- `DEN_REPOS=/home/agent/.den/repos.json` (Task 1 confirms the home). JSON carries `"schema": 1`.
- den neither reads nor writes `repos.local.json`.
- No `env create` / `env rm` without `--skip-host-commands`. Every sbx measurement uses a throwaway sandbox named `den-probe-*`, created and removed by the agent; never touch `am` or any other instance.
- **Another agent edits `README.md` on `v2` in parallel.** Never stage, commit, revert or reformat `README.md` or any file you did not change; always `git add <your files>` by name, never `git add -A`/`.`.
- Code, comments and user-facing text are English; `task check` is green before every commit; goldens are edited by hand (no `-update` flag).

## Review Focus

1. A second `den up` on a Live instance must not warn "config changed": adding `DEN_REPOS` must not mutate `Resolved.Env` (it feeds `configHash`). Test in Task 5.
2. A Recorded instance recreated with another selection gets a fresh map with no stale entry (GenDir is rewritten from scratch). Test in Task 5.
3. A `--repo` ad hoc folder appears under "Mounted repos" with no key, and is not listed again under "Other repos of this machine" when a `repos:` key points at the same path. Test in Task 5.
4. A worktree found in place on another branch than the one typed shows its real branch; a detached HEAD shows `detached HEAD`. Test in Task 5.
5. Empty lists render as `- (none)` and the JSON keeps `[]`, never `null`. Test in Task 2.

---

### Task 1: Spike — does the sandbox kit deliver `files/home/`?

No product code. Outcome decides one constant in Task 5.

**Files:** none committed. Scratch only (`<scratchpad>/spike/`).

- [ ] **Step 1:** Build a den binary: `go build -o <scratchpad>/den ./cmd/den` (do not run `task build`: it replaces the user's `den`).
- [ ] **Step 2:** In a scratch den home (`DEN_HOME=<scratchpad>/spike/home`), write a global `den.yaml` with `schema: 1`, `default_template: claude-code-docker`, and one nest `den-probe-repomap` whose only repo is `path: <scratchpad>/spike/repo` (an empty `git init` folder). Run `<scratchpad>/den up den-probe-repomap --detach`.
- [ ] **Step 3:** Remove only the VM, keeping den's files: `sbx --no-input env rm --skip-host-commands <home>/run/den-probe-repomap/sbxenv.yaml` (then `sbx rm --force den-probe-repomap` if `sbx ls` still lists it).
- [ ] **Step 4:** Add `<home>/gen/den-probe-repomap/den-claude/files/home/.claude/rules/probe.md` (content `probe`) and `…/den-claude/files/home/.den/probe.json` (content `{}`). Recreate: `sbx --no-input env create --force --skip-host-commands <home>/run/den-probe-repomap/sbxenv.yaml`.
- [ ] **Step 5:** `sbx exec den-probe-repomap -- bash -lc 'echo $HOME; cat ~/.claude/rules/probe.md ~/.den/probe.json'`. Expected for the sandbox-kit route: `/home/agent`, `probe`, `{}`.
- [ ] **Step 6:** If the files are absent, repeat steps 3–5 with a mixin kit instead: `<home>/gen/den-probe-repomap/den-repos/spec.yaml` holding exactly
  ```yaml
  schemaVersion: "2"
  kind: mixin
  name: den-repos
  version: 1.0.0
  ```
  plus the same `files/home/…`, and add that kit dir to `kits:` in the `sbxenv.yaml` before recreating.
- [ ] **Step 7:** Clean up: `<scratchpad>/den rm den-probe-repomap` with the scratch `DEN_HOME`; `sbx ls` lists no `den-probe-*`.
- [ ] **Step 8:** Report `DELIVERY=den-claude` or `DELIVERY=den-repos` and `HOME=<value>`. If neither route delivers files, or `HOME` is not `/home/agent`, stop and report: the design needs a ruling.

### Task 2: Render the repo map (`internal/sbxenv`)

**Files:**
- Create: `internal/sbxenv/repomap.go`, `internal/sbxenv/repomap_test.go`, `internal/sbxenv/testdata/repomap.md.golden`, `internal/sbxenv/testdata/repomap.json.golden`

**Interfaces:**
- Produces:
  ```go
  const RepoMapVMPath = "/home/agent/.den/repos.json"       // value of DEN_REPOS
  const RepoMapEnv = "DEN_REPOS"
  const RepoMapRulesFile = "files/home/.claude/rules/den-instance.md" // relative to a kit dir
  const RepoMapJSONFile = "files/home/.den/repos.json"               // relative to a kit dir
  type MappedRepo struct {
      Name     string `json:"name"`
      Key      string `json:"key,omitempty"`
      Path     string `json:"path"`
      Optional bool   `json:"optional,omitempty"`
      Branch   string `json:"branch,omitempty"` // worktree only; "detached HEAD" when detached
  }
  type RepoMap struct {
      Schema         int          `json:"schema"` // always 1
      Instance       string       `json:"instance"`
      Nest           string       `json:"nest"`
      ConfigFile     string       `json:"config_file"`
      Template       string       `json:"template"`
      Start          string       `json:"start"`
      Mounted        []MappedRepo `json:"mounted"`
      NestNotMounted []MappedRepo `json:"nest_not_mounted"`
      Machine        []MappedRepo `json:"machine"`
  }
  func RenderRepoMapJSON(m RepoMap) ([]byte, error) // indented 2 spaces, trailing newline, nil slices as []
  func RenderRepoMapMarkdown(m RepoMap) []byte
  ```

- [ ] **Step 1: Write the failing tests** in `repomap_test.go`:
  - `TestRenderRepoMapMarkdownMatchesGolden`: a map with instance `oauth`, nest `oauth`, config file `/p/dev-home/den.yaml`, template `digitaleo`, start `/p/dev-home`; Mounted `oauth.messengeo.net` (key `oauth.messengeo.net`, path `/p/oauth.messengeo.net`) and `libeo` (key `libeo`, optional, branch `feature/x`); NestNotMounted `js.box` (key `js.box`, path `/p/js.box`, optional); Machine `php.flow` (`/p/php.flow`). Output equals `testdata/repomap.md.golden`.
  - `TestRenderRepoMapJSONMatchesGolden`: same map, equals `testdata/repomap.json.golden`.
  - `TestRenderRepoMapEmptyListsRenderNone`: a map with nil lists → Markdown contains `- (none)` under each of the three headings; JSON contains `"mounted": []`, `"nest_not_mounted": []`, `"machine": []`.
  - `TestRenderRepoMapMarkdownIsAgentNeutral`: Markdown output contains none of `Claude`, `claude`, `repos.local.json`, `dev home`.
- [ ] **Step 2:** `go test ./internal/sbxenv/ -run RepoMap -count=1` → FAIL (undefined).
- [ ] **Step 3:** Implement in `repomap.go`. The Markdown is exactly this copy (placeholders in `<>`; one bullet per repo; a bullet appends ` (key <key>)` when Key differs from Name, ` (optional)` when Optional, ` (worktree, branch <branch>)` when Branch is set):
  ```
  # den instance <instance>

  den wrote this file when it created this VM. It lists the repos this VM can reach.

  - Nest: `<nest>`, declared in <config_file>
  - Template: `<template>`
  - Start folder: <start>

  ## Mounted repos

  - <name> — <path>

  ## Repos of the nest not mounted

  - <name> — host path <path>

  ## Other repos of this machine

  - <name> — host path <path>

  A repo missing from "Mounted repos" is not available in this VM. Say so, and ask the human to add it: `den rm <instance>`, then `den up <nest> --repo <host path>`, or add the repo to the nest. Do not look for it elsewhere and do not clone it.

  The same data as JSON is in the file named by the environment variable `DEN_REPOS`.
  ```
  Write both goldens by hand from this copy.
- [ ] **Step 4:** `go test ./internal/sbxenv/ -count=1` → PASS.
- [ ] **Step 5:** `task check` green, then `git add internal/sbxenv/repomap.go internal/sbxenv/repomap_test.go internal/sbxenv/testdata/repomap.md.golden internal/sbxenv/testdata/repomap.json.golden && git commit -m "feat(sbxenv): render the per-instance repo map"`.

### Task 3: Expose the machine's repos (`internal/nest`)

**Files:**
- Modify: `internal/nest/resolve.go` (type `Resolved`, function `Resolve` near the `n.Repos` loop)
- Test: `internal/nest/resolve_test.go`

**Interfaces:**
- Produces: `Resolved.MachineRepos map[string]string` — every key of `repos:` in the global file, mapped to its absolute path (same `absPath(raw, global.Dir)` the nest repos use). A key whose path does not resolve is left out, never an error (only the nest's own keys may refuse).

- [ ] **Step 1: Write the failing test** `TestResolveExposesEveryMachineRepo`: a global file with `repos: {a: ~/a, b: rel/b}` and a nest using only `a`; `r.MachineRepos` equals `{"a": <home>/a, "b": <global dir>/rel/b}`.
- [ ] **Step 2:** `go test ./internal/nest/ -run MachineRepo -count=1` → FAIL.
- [ ] **Step 3:** Add the field and fill it in `Resolve`.
- [ ] **Step 4:** `go test ./internal/nest/ -count=1` → PASS.
- [ ] **Step 5:** `task check`, then commit `internal/nest/resolve.go internal/nest/resolve_test.go` with `feat(nest): expose the machine's repos: keys`.

### Task 4: Refuse a user `DEN_REPOS` (`internal/instance`)

**Files:**
- Modify: `internal/instance/prepare.go` (in `Prepare`, after the `state == Live` return and after `nest.AddRepos`)
- Test: `internal/instance/prepare_test.go`

**Interfaces:**
- Consumes: `sbxenv.RepoMapEnv` (Task 2).

- [ ] **Step 1: Write the failing tests:**
  - `TestPrepareRefusesDenReposInEnv`: global `env: {DEN_REPOS: x}`, a Recorded or absent instance → error containing `DEN_REPOS`, the global file path, and `remove it`.
  - `TestPrepareAttachesLiveDespiteDenReposInEnv`: same config, instance Live → no error (an attach is never refused).
- [ ] **Step 2:** `go test ./internal/instance/ -run DenRepos -count=1` → FAIL.
- [ ] **Step 3:** Refuse when `res.Env[sbxenv.RepoMapEnv]` is set, with: `` `DEN_REPOS` is set by den in every instance — remove it from `env:` in <res.GlobalFile> or in nest <res.Name> of <res.File.Path> ``.
- [ ] **Step 4:** `go test ./internal/instance/ -count=1` → PASS.
- [ ] **Step 5:** `task check`, commit `internal/instance/prepare.go internal/instance/prepare_test.go` with `feat(instance): refuse a user DEN_REPOS`.

### Task 5: Write the map at creation (`internal/instance`)

**Files:**
- Create: `internal/instance/repomap.go`, `internal/instance/repomap_test.go`
- Modify: `internal/instance/create.go` (`writeRecord`: keep each worktree's real branch; `writeGenerated`: write the map; `envOf`: export `DEN_REPOS`; and, only if Task 1 reported `DELIVERY=den-repos`, add the `den-repos` kit dir to `Kits` after `denClaudeKitDir(p)`)

**Interfaces:**
- Consumes: `sbxenv.RepoMap`, `MappedRepo`, `RenderRepoMapJSON`, `RenderRepoMapMarkdown`, the four constants (Task 2); `nest.Resolved.MachineRepos` (Task 3).
- Produces:
  ```go
  func repoMapOf(p *Plan, rec registry.Record, branches map[string]string) sbxenv.RepoMap
  func writeRepoMap(kitDir string, m sbxenv.RepoMap) error // writes <kitDir>/files/home/... (both files)
  func currentBranch(ctx context.Context, g worktree.Git, worktreePath string) (string, error) // `git rev-parse --abbrev-ref HEAD`; "HEAD" → "detached HEAD"
  ```
  `kitDir` is `denClaudeKitDir(p)` for `DELIVERY=den-claude`, else `filepath.Join(p.GenDir, "den-repos")` with the mixin `spec.yaml` from Task 1 step 6.

  `repoMapOf` rules: Mounted = `p.Repos` in selection order (Key, Optional from the `nest.Repo`; an ad hoc repo has no key; Branch from `branches[name]`). NestNotMounted = `p.Resolved.Repos` minus the selection, in nest order, path = host path. Machine = `MachineRepos` keys sorted, minus any key whose path is a mounted or nest-not-mounted path; Name = the key. Start = `rec.Start`; Template = `rec.Template`.

- [ ] **Step 1: Write the failing tests** in `repomap_test.go` (the package's `TestMain` already neutralizes git):
  - `TestCreateWritesTheRepoMap`: a nest with required `a`, optional `b` and `c`, created with `--without c`; global `repos:` also has `d`. After `Create`: `<GenDir>/<kit>/files/home/.den/repos.json` decodes to Mounted `[a, b]`, NestNotMounted `[c]`, Machine `[d]`; the rules file exists and contains `den rm <instance>`.
  - `TestCreateRepoMapShowsTheRealWorktreeBranch`: `-w feature/x` where the worktree path already exists on branch `other` (found in place) → Mounted entry Branch `other`; a detached worktree → `detached HEAD`.
  - `TestCreateRepoMapListsAdHocRepoOnce`: `--repo` of a folder whose path equals `repos:` key `d` → `d`'s folder is in Mounted with empty Key, and Machine has no `d`.
  - `TestRecreateRewritesTheRepoMap`: Create with `--without c`, then Create again (Recorded) without it → the second map's NestNotMounted is empty.
  - `TestEnvOfExportsDenReposWithoutMutatingResolved`: `envOf(p, nil).Env["DEN_REPOS"] == sbxenv.RepoMapVMPath` and `p.Resolved.Env` has no `DEN_REPOS` afterwards.
- [ ] **Step 2:** `go test ./internal/instance/ -run 'RepoMap|DenRepos' -count=1` → FAIL.
- [ ] **Step 3:** Implement `repomap.go`; in `writeRecord` record `currentBranch` per worktree into a `branches` map passed down; call `writeRepoMap` from `writeGenerated` (after `WriteDenClaudeKit`, so it lands in the freshly cleared GenDir); in `envOf` copy `res.Env` into a new map before adding `DEN_REPOS`.
- [ ] **Step 4:** `go test ./internal/instance/ -count=1` → PASS; existing goldens unchanged except where `DEN_REPOS` now appears in a rendered `sbxenv.yaml` (edit those goldens by hand, and only that line).
- [ ] **Step 5:** `task check`, commit the files you changed by name with `feat(instance): write the repo map at creation`.

### Task 6: End to end on sbx, and docs

**Files:**
- Modify: `CLAUDE.md` (the `internal/instance` paragraph of "Architecture": one sentence on the map, its two VM paths and `DEN_REPOS`).
- `README.md` only if `git status --porcelain README.md` is empty (the other agent has committed); otherwise skip it and report.

- [ ] **Step 1:** `go build -o <scratchpad>/den ./cmd/den` (no `task build`). Scratch `DEN_HOME`; global `den.yaml` with `repos:` keys `r1`, `r2`, `r3` (three `git init` folders under the scratchpad) and a nest `den-probe-map` with `r1` required, `r2` optional, `start: r1`, `default_template: claude-code-docker`.
- [ ] **Step 2:** `<scratchpad>/den up den-probe-map --without r2 --detach`, then:
  - `sbx exec den-probe-map -- bash -lc 'cat "$DEN_REPOS"'` → mounted `[r1]`, nest_not_mounted `[r2]`, machine `[r3]` (A3, A5).
  - `sbx exec den-probe-map -- bash -lc 'cat ~/.claude/rules/den-instance.md; ls ~/.claude'` → the rules file is there; `CLAUDE.md`/`settings.json` are absent or untouched (A4: the scratch home has no profile).
  - `<scratchpad>/den exec den-probe-map -- claude -p --dangerously-skip-permissions "Without reading any file, list the mounted repos and their paths."` → names `r1` and its path (A1). If Claude is not signed in inside the probe, report it instead of passing.
- [ ] **Step 3:** Run `<scratchpad>/den up den-probe-map` again (Live) → no "config changed" warning (Review Focus 1).
- [ ] **Step 4:** Clean up with `<scratchpad>/den rm den-probe-map`; `sbx ls` lists no `den-probe-*`.
- [ ] **Step 5:** Update `CLAUDE.md`; `task check`; commit `CLAUDE.md` by name with `docs: the per-instance repo map`.

---

## Rulings in this plan

- B3 says the real branch is "read in the instance record"; the record keeps the branch as typed (`registry.Worktree.Branch`). The plan reads the real branch with git at creation (`currentBranch`) and leaves the record unchanged, which is B3's intent.
- The design says `internal/nest` does not change (about dropped optional keys). The third list needs the machine's `repos:` keys, which `nest.Resolve` does not expose: Task 3 adds one read-only field, `MachineRepos`.
- The map is not part of `configHash`: it derives from config and selection, which the hash already covers.
