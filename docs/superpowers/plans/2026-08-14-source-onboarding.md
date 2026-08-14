# Source Onboarding Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a declarative, resumable `den init --source <url>` onboarding flow and publish the Digitaleo source as its first complete manifest.

**Architecture:** `internal/source` owns source manifests, source-scoped personal configuration, receipts, Git acquisition, and exact-version guards. A new `internal/converge` package owns typed answers, repository discovery, resource inspection, deterministic plans, application, readiness, and status aggregation. Cobra commands only collect flags and terminal input, then invoke the shared service; the existing legacy source path remains intact when `den-source.yaml` is absent.

**Tech Stack:** Go 1.26, Cobra, `gopkg.in/yaml.v3`, `golang.org/x/mod/semver` v0.38.0, `golang.org/x/term` v0.45.0, existing `worktree.Git` and `sbx.Runner` adapters, temporary Git repositories, Go table-driven tests.

## Global Constraints

- Every configuration key introduced by this delivery uses `snake_case`; strict YAML decoding rejects unknown keys.
- The manifest is strictly declarative. It cannot name shell hooks, commands, or external resource implementations.
- `den` and `sbx` are prerequisites. The flow verifies their versions and never installs or updates them.
- A source configuration stores one exact SemVer target version. Ordinary commands never contact the remote.
- `den init --source` configures every declared resource and inspects every exported nest. It never selects or spawns nests.
- Working repositories are detected and mapped but never cloned. Missing required repositories produce `partially_ready`, not an installation failure.
- MCP variables and credentials remain outside this delivery.
- Credential values never enter plans, logs, errors, personal configuration, receipts, or test failure messages.
- The final status vocabulary is `ready`, `partially_ready`, `blocked`, and `unknown`; individual nests use `ready` and `not_ready`.
- `den init` without `--source` and legacy sources without `den-source.yaml` retain their current behavior.
- Commits that touch `/Users/polochon/Development/Pillow/den` and `/Users/polochon/Development/Digitaleo/digitaleo-den-env` stay separate.

## File Structure

### Den repository

- `internal/source/manifest.go`: strict `den-source.yaml` model, SemVer parsing, compatibility, export, and resource validation.
- `internal/source/personal.go`: `source-config/<name>.yaml` model, path helpers, private atomic read/write.
- `internal/source/receipt.go`: `state/sources/<name>.yaml` model, applying/final receipts, private atomic read/write.
- `internal/source/version.go`: exact-version comparison and checkout/configuration/receipt consistency guard.
- `internal/source/candidate.go`: temporary acquisition and fetched-candidate worktree operations without premature checkout changes.
- `internal/converge/answers.go`: strict answer-file model and transient credential references.
- `internal/converge/discovery.go`: Git remote normalization, direct-child discovery, and candidate classification.
- `internal/converge/model.go`: statuses, observations, plan actions, readiness, and redacted render model.
- `internal/converge/sbx.go`: typed `sbx` credential, policy, and version inspection/application adapter.
- `internal/converge/build.go`: adapter over the existing build planner/executor for manifest-declared stacks.
- `internal/converge/service.go`: shared inspect/plan/apply/verify orchestration and commit ordering.
- `internal/converge/render.go`: stable text plan and status rendering.
- `internal/cli/answers.go`: interactive and answer-file collection plus confirmation.
- `internal/cli/init.go`: source-aware flags and delegation while preserving the existing no-source path.
- `internal/cli/source.go`: manifested add/configure/update/status delegation plus legacy dispatch.
- `internal/config/config.go`, `internal/config/validate.go`: optional `defaults.stack`.
- `internal/nest/resolve.go`: source-scoped mapping injection and contextual missing-stack error.
- `internal/lint/lint.go`: manifest-aware lint entry point and explicit export validation.
- `internal/doctor/doctor.go`, `internal/cli/doctor.go`: manifested-source checks and `unknown` handling.
- `examples/den-home-source/config.yaml`: embedded minimal source-aware global configuration.
- `README.md`: generic source onboarding, answer-file, status, update, and manual migration documentation.

Each new production file gets a sibling `_test.go`. Cross-component flows live in `internal/converge/service_test.go` and CLI wiring remains in `internal/cli/*_test.go`.

### Digitaleo source repository

- `den-source.yaml`: version `1.0.0`, compatibility, explicit exports, credentials, machine policies, and required build.
- `testdata/onboarding-answers.yaml`: non-secret acceptance answers using `from_env`.
- `README.md`: one-command onboarding and manual migration instructions.

---

### Task 1: Make the global default stack optional

**Files:**
- Modify: `internal/config/validate.go`
- Modify: `internal/config/validate_test.go`
- Modify: `internal/nest/resolve.go`
- Modify: `internal/nest/resolve_test.go`
- Create: `examples/den-home-source/config.yaml`
- Modify: `embed.go`
- Modify: `embed_test.go`

**Interfaces:**
- Consumes: existing `config.Global`, `nest.Nest`, and `config.Stacks.Get`.
- Produces: `den.SourceAwareDenHome fs.FS`; `defaults.stack` may be empty, while `nest.Resolve` still returns a contextual error when both the nest stack and global default are empty.

- [x] **Step 1: Write failing validation and resolution tests**

```go
func TestValidateAllowsEmptyDefaultStack(t *testing.T) {
	g := validGlobal(t)
	g.Defaults.Stack = ""
	for _, err := range g.Validate() {
		if strings.Contains(err.Error(), "defaults.stack: required") {
			t.Fatalf("empty defaults.stack was rejected: %v", err)
		}
	}
}

func TestResolveNamesMissingNestAndGlobalStack(t *testing.T) {
	g := validGlobal(t)
	g.Defaults.Stack = ""
	_, err := Resolve(t.TempDir(), g, config.Stacks{Healthy: map[string]*config.Stack{}}, &Nest{Name: "api"}, Options{})
	if err == nil || !strings.Contains(err.Error(), `nest "api": no stack is configured`) {
		t.Fatalf("Resolve error = %v", err)
	}
}
```

- [x] **Step 2: Run the focused tests and verify the old requirement fails them**

Run: `go test ./internal/config ./internal/nest -run 'TestValidateAllowsEmptyDefaultStack|TestResolveNamesMissingNestAndGlobalStack'`

Expected: FAIL because validation requires `defaults.stack`, then because resolution calls `stacks.Get("")`.

- [x] **Step 3: Remove only the global requirement and add the contextual resolution guard**

```go
stackName := n.Stack
if stackName == "" {
	stackName = g.Defaults.Stack
}
if stackName == "" {
	return nil, fmt.Errorf(
		"nest %q: no stack is configured — add `stack:` to %s or set `defaults.stack` in %s",
		n.Name, FilePath(denHome, n.Name), config.GlobalPath(denHome))
}
```

Keep every other `Global.Validate` check unchanged.

- [x] **Step 4: Embed and test a minimal source-aware home**

Create `examples/den-home-source/config.yaml` with the same shipped Claude agent, default agent, SSH, worktree layout, and baseline egress as `examples/den-home/config.yaml`, but omit `defaults.stack`, `repos`, `nests/`, and `stacks/`. Add a second `//go:embed` variable in `embed.go` and assert that its only file is a loadable `config.yaml`.

- [x] **Step 5: Run package and full tests**

Run: `go test ./internal/config ./internal/nest ./...`

Expected: PASS.

- [x] **Step 6: Commit**

```bash
git add internal/config internal/nest examples/den-home-source embed.go embed_test.go
git commit -m "feat(init): support source-aware global config"
```

### Task 2: Add the strict source manifest contract

**Files:**
- Create: `internal/source/manifest.go`
- Create: `internal/source/manifest_test.go`
- Modify: `go.mod`
- Modify: `go.sum`
- Modify: `internal/lint/lint.go`
- Modify: `internal/lint/lint_test.go`
- Modify: `internal/cli/lint_test.go`

**Interfaces:**
- Consumes: `config.DecodeYAMLStrict`, `config.LoadStacks`, `nest.LoadNest`, and source-root confinement rules already used by `lint.Run`.
- Produces: `source.LoadManifest(root string) (*Manifest, error)`, `source.ValidateManifest(root string, m *Manifest) []error`, `source.CheckCompatibility(m *Manifest, denVersion, sbxVersion string) error`, and `source.ManifestPath(root string) string`.

- [ ] **Step 1: Write failing decode tests for snake_case and closed resource types**

```go
func TestLoadManifestRejectsCamelCase(t *testing.T) {
	root := writeManifest(t, `schemaVersion: 1
kind: source
metadata: {name: dg, version: 1.0.0}
`)
	_, err := LoadManifest(root)
	if err == nil || !strings.Contains(err.Error(), "field schemaVersion not found") {
		t.Fatalf("LoadManifest error = %v", err)
	}
}

func TestLoadManifestRejectsUnknownCredentialType(t *testing.T) {
	root := validManifestTree(t)
	rewriteManifest(t, root, strings.Replace(validManifestYAML, "sbx_github", "shell_hook", 1))
	_, err := LoadManifest(root)
	if err == nil || !strings.Contains(err.Error(), `credential resource "github": type "shell_hook" is unsupported`) {
		t.Fatalf("LoadManifest error = %v", err)
	}
}
```

- [ ] **Step 2: Run the source tests and verify the loader is absent**

Run: `go test ./internal/source -run TestLoadManifest`

Expected: FAIL to compile because `LoadManifest` is undefined.

- [ ] **Step 3: Define the manifest types and strict loader**

```go
type Manifest struct {
	SchemaVersion int       `yaml:"schema_version"`
	Kind          string    `yaml:"kind"`
	Metadata      Metadata  `yaml:"metadata"`
	Requires      Requires  `yaml:"requires"`
	Exports       Exports   `yaml:"exports"`
	Inputs        Inputs    `yaml:"inputs"`
	Resources     Resources `yaml:"resources"`
}

type Export struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
}

type CredentialResource struct {
	ID          string    `yaml:"id"`
	Type        string    `yaml:"type"`
	Scope       string    `yaml:"scope"`
	Host        string    `yaml:"host"`
	Environment string    `yaml:"environment"`
	ValueFrom   ValueFrom `yaml:"value_from"`
}
```

Define constants for schema `1` and the three supported credential types: `sbx_github`, `sbx_registry`, and `sbx_http_substitution`. Accept only `kind: source`, `scope: global`, and requirements written as `>=<valid SemVer>`. Use `golang.org/x/mod/semver` v0.38.0 behind source-owned `validVersion` and `compareVersion` helpers; prepend the package-required `v` internally while preserving the manifest's unprefixed values.

- [ ] **Step 4: Add export and reference validation tests**

Cover duplicate names, absolute paths, `..` escape, symlink escape, missing files, filename/name mismatch, non-exported stack references, undeclared credential inputs, duplicate resource IDs, bad schema, bad functional version, and unmet Den/sbx floors. Each table row asserts the exact resource or export name in the error.

- [ ] **Step 5: Implement validation and make lint manifest-aware**

`lint.Run(root)` keeps its existing legacy scan when `den-source.yaml` does not exist. When the file exists, load the explicit stack and nest exports only, validate their decoded names against the export names, then run existing stack/nest shareability checks over that catalogue. Do not infer an export by scanning the directory.

- [ ] **Step 6: Run lint and source tests**

Run: `go test ./internal/source ./internal/lint ./internal/cli -run 'Manifest|Lint'`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/source/manifest.go internal/source/manifest_test.go internal/lint internal/cli/lint_test.go
git commit -m "feat(source): define declarative source manifest"
```

### Task 3: Add private source configuration and convergence receipts

**Files:**
- Create: `internal/source/personal.go`
- Create: `internal/source/personal_test.go`
- Create: `internal/source/receipt.go`
- Create: `internal/source/receipt_test.go`

**Interfaces:**
- Consumes: `config.DecodeYAMLStrict` and `config.ExpandPath`.
- Produces: `source.LoadPersonal`, `source.ExpandedRepos`, `source.WritePersonal`, `source.LoadReceipt`, `source.WriteReceipt`, `source.PersonalPath`, and `source.ReceiptPath`.

- [ ] **Step 1: Write failing round-trip, permission, and atomicity tests**

```go
func TestWritePersonalRoundTripsPrivately(t *testing.T) {
	home := t.TempDir()
	want := Personal{SchemaVersion: 1, Version: "1.0.0", Repos: map[string]string{"go-dgdev": "~/Development/go.dgdev"}}
	if err := WritePersonal(home, "dg", want); err != nil { t.Fatal(err) }
	got, err := LoadPersonal(home, "dg")
	if err != nil { t.Fatal(err) }
	if got.Version != want.Version { t.Fatalf("version = %q, want %q", got.Version, want.Version) }
	if got.Repos["go-dgdev"] != want.Repos["go-dgdev"] { t.Fatalf("repos = %#v", got.Repos) }
	info, _ := os.Stat(PersonalPath(home, "dg"))
	if info.Mode().Perm() != 0o600 { t.Fatalf("mode = %o", info.Mode().Perm()) }
}
```

Add a failure injection around rename to prove the old file remains readable when replacement fails.

- [ ] **Step 2: Run tests and verify the APIs are absent**

Run: `go test ./internal/source -run 'Personal|Receipt'`

Expected: FAIL to compile.

- [ ] **Step 3: Implement strict models and one private atomic writer**

```go
type Personal struct {
	SchemaVersion int               `yaml:"schema_version"`
	Version       string            `yaml:"version"`
	Repos         map[string]string `yaml:"repos"`
}

type Receipt struct {
	SchemaVersion  int                       `yaml:"schema_version"`
	Status         SourceStatus              `yaml:"status"`
	Version        string                    `yaml:"version,omitempty"`
	PreviousVersion string                   `yaml:"previous_version,omitempty"`
	TargetVersion  string                    `yaml:"target_version,omitempty"`
	Commit         string                    `yaml:"commit,omitempty"`
	TargetCommit   string                    `yaml:"target_commit,omitempty"`
	ManifestDigest string                    `yaml:"manifest_digest"`
	AppliedAt      time.Time                 `yaml:"applied_at,omitempty"`
	Resources      ReceiptResources          `yaml:"resources"`
	Nests          map[string]ReceiptNest    `yaml:"nests"`
}

type SourceStatus string
type ResourceStatus string
type ReceiptResources struct {
	Credentials  ResourceStatus            `yaml:"credentials"`
	BuildNetwork ResourceStatus            `yaml:"build_network"`
	Stacks       map[string]ResourceStatus `yaml:"stacks"`
}
type ReceiptNest struct {
	Status       string   `yaml:"status"`
	MissingRepos []string `yaml:"missing_repos,omitempty"`
}
```

Define the closed `SourceStatus` constants `StatusReady = "ready"`, `StatusPartiallyReady = "partially_ready"`, `StatusBlocked = "blocked"`, `StatusUnknown = "unknown"`, and `StatusApplying = "applying"` in `internal/source`; define matching `ResourceReady`, `ResourceBlocked`, and `ResourceUnknown` constants for receipt components. `internal/converge` consumes them and never becomes an import of `internal/source`. Write into a sibling temporary file with `0600`, `Sync`, close, then `Rename`. Create parent directories with `0700`. Validate source names before composing paths. `LoadPersonal` preserves repo paths as authored; `ExpandedRepos` returns a separate expanded map for resolution so a later write does not replace `~` with an absolute path.

- [ ] **Step 4: Test schema refusal and secret absence**

Assert unknown keys and schema versions fail. Marshal a receipt and assert it contains none of the credential input names, environment variable names, repository roots, or supplied sentinel secret values.

- [ ] **Step 5: Run tests and commit**

Run: `go test ./internal/source`

```bash
git add internal/source/personal.go internal/source/personal_test.go internal/source/receipt.go internal/source/receipt_test.go
git commit -m "feat(source): persist personal config and receipts"
```

### Task 4: Route manifested nests through source-scoped mappings

**Files:**
- Create: `internal/source/version.go`
- Create: `internal/source/version_test.go`
- Modify: `internal/nest/resolve.go`
- Modify: `internal/nest/resolve_test.go`
- Modify: `internal/spawn/spawn.go`
- Modify: `internal/spawn/spawn_test.go`
- Modify: `internal/cli/nest.go`
- Modify: `internal/cli/nest_test.go`
- Modify: `internal/cli/build.go`
- Modify: `internal/cli/build_test.go`

**Interfaces:**
- Consumes: `source.LoadManifest`, `source.LoadPersonal`, `source.LoadReceipt`, and existing `source.Locate` results.
- Produces: `source.RequireUsable(denHome, name string) (*Active, error)` and `nest.Options.RepoMapping map[string]string`.

- [ ] **Step 1: Write failing source-scoping tests**

Create two manifested sources that both declare key `api`, map them to different directories, and assert each source nest resolves its own path. Assert a local nest still uses `config.yaml.repos`. Assert a manifested source never falls back to the global mapping.

```go
resolved, err := Resolve(home, global, stacks, sourceNest, Options{RepoMapping: map[string]string{"api": repoA}})
if err != nil { t.Fatal(err) }
if resolved.Repos[0].Path != repoA { t.Fatalf("path = %q", resolved.Repos[0].Path) }
```

- [ ] **Step 2: Write divergence-guard tests**

Cover checkout manifest version differing from `Personal.Version`, an `applying` receipt, target commit mismatch, missing final receipt, and a completely legacy source. Every manifested refusal must name `den source configure <name>` and the pending exact version.

- [ ] **Step 3: Run tests and verify current global-only behavior fails**

Run: `go test ./internal/source ./internal/nest ./internal/spawn ./internal/cli -run 'SourceScoped|RequireUsable|VersionDivergence'`

Expected: FAIL because `Resolve` reads only `g.Repos` and no version guard exists.

- [ ] **Step 4: Add mapping injection and the active-source guard**

```go
type Active struct {
	Name     string
	Root     string
	Manifest *Manifest
	Personal *Personal
	Receipt  *Receipt
}

func RequireUsable(denHome, name string) (*Active, error)
```

`RequireUsable` returns legacy mode when the manifest is absent. For a manifested source it requires checkout version, configured version, and final receipt version/commit to agree, then exposes `ExpandedRepos(Personal.Repos)` for runtime resolution. `nest.Resolve` selects `Options.RepoMapping` when non-nil; nil preserves `g.Repos` for local and legacy callers. Update spawn, nest show, and named source build to call this guard before loading source objects.

- [ ] **Step 5: Update unmapped-key diagnostics**

Pass the selected mapping path into `resolveRepoKeys`. A manifested source error points to `source.PersonalPath(home, name)`; local and legacy errors still point to `config.GlobalPath(home)`.

- [ ] **Step 6: Run affected and full tests, then commit**

Run: `go test ./internal/source ./internal/nest ./internal/spawn ./internal/cli ./...`

```bash
git add internal/source/version.go internal/source/version_test.go internal/nest internal/spawn internal/cli
git commit -m "feat(source): scope repo mappings per manifested source"
```

### Task 5: Decode answer files and collect the same typed answers interactively

**Files:**
- Create: `internal/converge/answers.go`
- Create: `internal/converge/answers_test.go`
- Create: `internal/cli/answers.go`
- Create: `internal/cli/answers_test.go`
- Modify: `internal/cli/root.go`
- Modify: `internal/cli/root_deps_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Consumes: manifest credential input declarations and `config.ExpandPath`.
- Produces: `converge.LoadAnswers(path string, getenv func(string) string) (Answers, error)`, `cli.collectInitialAnswers(...)`, and `cli.resolveRepoChoices(matches []converge.RepoMatch, answers *converge.Answers)`.

- [ ] **Step 1: Write strict answer-file tests**

```go
func TestLoadAnswersResolvesCredentialEnvironmentWithoutPersistingIt(t *testing.T) {
	path := writeAnswers(t, `repository_roots: [~/Development]
credentials:
  gitlab_token:
    from_env: GLPAT
repos:
  api: ~/Development/api
`)
	got, err := LoadAnswers(path, func(k string) string { if k == "GLPAT" { return "sentinel-secret" }; return "" })
	if err != nil { t.Fatal(err) }
	if got.Credentials["gitlab_token"].Value != "sentinel-secret" { t.Fatal("credential not resolved") }
}
```

Also reject `repositoryRoots`, literal credential values, missing environment variables, undeclared credential names, and relative roots. Repository discovery validates that an explicit repo override is a Git worktree because that layer owns the injected Git adapter.

- [ ] **Step 2: Define typed transient answers**

```go
type Answers struct {
	RepositoryRoots []string
	Credentials     map[string]CredentialAnswer
	Repos           map[string]string
}

type CredentialAnswer struct {
	FromEnv string
	Value   string `yaml:"-"`
}
```

The YAML wire struct remains private so `Value` cannot be decoded or marshaled accidentally.

- [ ] **Step 3: Add interactive equivalence tests**

Feed deterministic stdin for repository roots and missing credentials through `collectInitialAnswers`. Feed discovered name-only and ambiguous matches through `resolveRepoChoices`; this function writes confirmed choices into `Answers.Repos`. Compare the final `Answers` with the answer-file result field by field. Add a no-TTY test that refuses only when input or confirmation is required and prints `--answers` plus `--yes` as the remedy.

- [ ] **Step 4: Implement CLI collection with injected terminal dependencies**

Extend `cli.Deps` with `Getenv func(string) string` and `ReadSecret func(prompt string) (string, error)`; keep `IsTTY`. `SystemDeps.ReadSecret` uses `term.ReadPassword(int(os.Stdin.Fd()))`, while tests inject a recording function. Use `cmd.InOrStdin()` and a buffered reader for non-secret answers. Never echo credential input. The answer-file path bypasses prompts but not validation. The CLI calls `Service.Plan` once for discovery, resolves any unconfirmed `RepoMatch`, then calls `Service.Plan` again with the enriched answers; both calls are read-only.

- [ ] **Step 5: Run tests and commit**

Run: `go test ./internal/converge ./internal/cli -run 'Answers|Interactive'`

```bash
git add go.mod go.sum internal/converge/answers.go internal/converge/answers_test.go internal/cli/answers.go internal/cli/answers_test.go internal/cli/root.go internal/cli/root_deps_test.go
git commit -m "feat(init): add reusable onboarding answers"
```

### Task 6: Discover repositories without cloning them

**Files:**
- Create: `internal/converge/discovery.go`
- Create: `internal/converge/discovery_test.go`

**Interfaces:**
- Consumes: exported nests, `nest.Repo`, `Answers.RepositoryRoots`, `Answers.Repos`, and `worktree.Git`.
- Produces: `converge.DiscoverRepos(ctx, git, requirements, answers) ([]RepoMatch, error)` and `converge.CollectRepoRequirements(root string, manifest *source.Manifest) ([]RepoRequirement, error)`.

- [ ] **Step 1: Write URL normalization tests**

Cover `git@gitlab.example.com:team/api.git`, `ssh://git@gitlab.example.com/team/api.git`, and `https://gitlab.example.com/team/api.git` converging to the same `gitlab.example.com/team/api` identity. Preserve host and owner path; strip credentials, scheme, trailing slash, and `.git` only.

- [ ] **Step 2: Write discovery classification tests with temporary Git repos**

```go
func TestDiscoverReposPrefersNormalizedRemote(t *testing.T) {
	root := t.TempDir()
	repo := initRepoWithRemote(t, filepath.Join(root, "renamed-folder"), "git@gitlab.example.com:team/api.git")
	matches, err := DiscoverRepos(context.Background(), worktree.NewGit(), []RepoRequirement{{Key: "api", URL: "https://gitlab.example.com/team/api.git"}}, Answers{RepositoryRoots: []string{root}})
	if err != nil { t.Fatal(err) }
	if matches[0].Kind != MatchRemote || matches[0].Path != repo { t.Fatalf("match = %#v", matches[0]) }
}
```

Cover name-only, ambiguous, explicit override, absent, shared key across nests, conflicting URLs for one key, required, and optional behavior. Assert only direct children are scanned.

- [ ] **Step 3: Implement deterministic collection and discovery**

```go
type RepoRequirement struct {
	Key            string
	URL            string
	RequiredBy     []string
	OptionalOnly   bool
}

type RepoMatch struct {
	Requirement RepoRequirement
	Kind        MatchKind
	Path        string
	Candidates  []string
	Confirmed   bool
}
```

Sort keys, dependent nests, roots, and candidates. `MatchRemote` is confirmed automatically. `MatchName` and `MatchAmbiguous` require an answer override or interactive choice. `MatchAbsent` remains unmapped.

- [ ] **Step 4: Run tests and commit**

Run: `go test ./internal/converge -run 'Normalize|Discover|Requirements'`

```bash
git add internal/converge/discovery.go internal/converge/discovery_test.go
git commit -m "feat(init): discover existing work repositories"
```

### Task 7: Define deterministic plans, readiness, statuses, and redacted rendering

**Files:**
- Create: `internal/converge/model.go`
- Create: `internal/converge/model_test.go`
- Create: `internal/converge/render.go`
- Create: `internal/converge/render_test.go`

**Interfaces:**
- Consumes: typed manifest resources, repo matches, and resource observations.
- Produces: `converge.Plan`, `converge.EvaluateReadiness`, `converge.AggregateStatus`, `converge.RenderPlan`, and `converge.RenderStatus`; `converge.Status` aliases `source.SourceStatus`.

- [ ] **Step 1: Write status aggregation tests**

Use table cases for all resources/nests ready, missing required repo, managed resource blocked, observer failure, and missing optional repo. Assert command success is true only for `ready` and `partially_ready`.

```go
tests := []struct{ resource source.SourceStatus; nest NestStatus; want source.SourceStatus }{
	{source.StatusReady, NestReady, source.StatusReady},
	{source.StatusReady, NestNotReady, source.StatusPartiallyReady},
	{source.StatusBlocked, NestReady, source.StatusBlocked},
	{source.StatusUnknown, NestReady, source.StatusUnknown},
}
```

- [ ] **Step 2: Write deterministic rendering and redaction tests**

Build plans in different map insertion orders and assert byte-identical output. Seed credential values with `sentinel-secret` and assert neither that string nor its environment variable name occurs in rendered plan, status, errors, or marshaled receipt. The visible value is exactly `<redacted>`. Construct a `ResourceError` and assert it names resource ID, observed state, expected state, remaining action, and exact resume command.

- [ ] **Step 3: Implement the closed plan model**

```go
type Action string
type Status = source.SourceStatus
const (
	ActionUnchanged Action = "unchanged"
	ActionCreate    Action = "create"
	ActionUpdate    Action = "update"
	ActionBlocked   Action = "blocked"
)

type ResourcePlan struct {
	ID       string
	Kind     string
	Action   Action
	Observed string
	Expected string
	Resume   string
}

type ResourceError struct {
	Resource  string
	Observed  string
	Expected  string
	Remaining string
	Resume    string
	Cause     error
}

func (e *ResourceError) Error() string
func (e *ResourceError) Unwrap() error

type Plan struct {
	Source          string
	Version         string
	TrustBoundary   string
	Resources       []ResourcePlan
	RepoMatches     []RepoMatch
	Nests           map[string]NestReadiness
	Status          Status
}

func (p *Plan) UnconfirmedRepoMatches() []RepoMatch
```

Construct all slices in stable manifest order, then key order where the manifest has maps. `RenderPlan` names the source and stack provision files before confirmation.

- [ ] **Step 4: Run tests and commit**

Run: `go test ./internal/converge -run 'Status|Readiness|Render|Redact'`

```bash
git add internal/converge/model.go internal/converge/model_test.go internal/converge/render.go internal/converge/render_test.go
git commit -m "feat(init): model deterministic convergence plans"
```

### Task 8: Implement typed sbx and build resource adapters

**Files:**
- Create: `internal/converge/sbx.go`
- Create: `internal/converge/sbx_test.go`
- Create: `internal/converge/testdata/secret-ls.txt`
- Create: `internal/converge/testdata/policy-ls.json`
- Create: `internal/converge/build.go`
- Create: `internal/converge/build_test.go`
- Modify: `internal/sbx/runner.go`
- Modify: `internal/sbx/runner_test.go`
- Modify: `internal/sbx/fake.go`
- Modify: `internal/sbx/fake_test.go`

**Interfaces:**
- Consumes: `sbx.Runner`, `build.Chain`, `build.Plan`, `build.Execute`, source resources, and transient answers.
- Produces: `converge.ResourceDriver` implementations for credentials, build-network policies, and stack builds.

- [ ] **Step 1: Capture and sanitize sbx inspection fixtures**

Before Task 8 implementation, bring in the factual commit from `prototype/sbx-inspection-contract`. That spike ran `sbx secret ls -g` and `sbx policy ls --type network --source local --decision allow --json` on a working sbx profile. Its fixtures contain only synthetic masked secret rows and a sanitized successful policy JSON shape. Docker's [official credentials documentation](https://docs.docker.com/ai/sandboxes/security/credentials/) confirms the public `SCOPE TYPE NAME SECRET` columns. Do not commit real masked secret fragments, usernames, hosts, policy IDs, or keychain errors.

Restricted execution that denies macOS Keychain access makes both observers fail with exit code 1 and keychain error `-50`; the same commands succeed outside that boundary. Add a synthetic non-zero observer-failure test and classify the resource as `unknown`. Do not classify an observer failure as an absent credential or policy.

- [ ] **Step 2: Write parser and version tests**

Assert `sbx version` parses `sbx version: v0.38.0 <commit>`. Assert the first secret table distinguishes service `github` and registry host rows from `SCOPE TYPE NAME SECRET`; both `(stored)` and `(oauth configured)` mean that a service credential is present. Assert the optional `CUSTOM SECRETS` table matches custom credentials from `SCOPE TARGETS ENV PLACEHOLDER SECRET`. Never read the masked value or custom placeholder. Assert policy JSON matches exact strings in `rules[].resources`. Any unexpected format returns an observation error instead of guessing.

- [ ] **Step 3: Define the driver lifecycle and fake it**

```go
type ResourceDriver interface {
	Inspect(context.Context) (Observation, error)
	Plan(Observation) ResourcePlan
	Apply(context.Context, Answers, io.Writer) error
	Verify(context.Context) (Observation, error)
}
```

Add a companion interface without changing the existing `sbx.Runner`, so specialized secret I/O does not force unrelated test runners to implement new methods:

```go
type SecretRunner interface {
	RunInput(ctx context.Context, input []byte, args ...string) ([]byte, error)
	RunSensitive(ctx context.Context, redactedIndexes []int, args ...string) ([]byte, error)
}
```

`*sbx.Exec` and `*sbx.Fake` implement it. Extend `sbx.Fake` to record stdin-safe invocations separately from displayed argv. Test helpers may store the sentinel secret internally, but failure formatting must redact it.

- [ ] **Step 4: Implement credential and policy commands**

Use these established commands:

```text
sbx secret set -g github
sbx secret set -g --registry <host> --password-stdin
sbx secret set-custom -g --host <host> --env <environment> --value <secret>
sbx policy allow network <host>
```

Never place a registry password in argv; pipe it through `SecretRunner.RunInput`. The installed v0.38.0 CLI requires `set-custom --value`; call it through `RunSensitive` with the value argument index redacted from `ExecError.Error` and fake call displays. Verify after every mutation.

- [ ] **Step 5: Wrap existing stack build behavior**

Resolve only manifest-exported stacks named by `resources.builds`. Reuse `build.Chain`, `build.Plan`, and `build.Execute`; do not shell out to `den build`. A first install and a greater functional source version rebuild each declared target even when its image exists. A configure of the already-finalized exact version is `unchanged`; an applying receipt may skip a completed build only after image verification succeeds. Apply declared builds in manifest order.

- [ ] **Step 6: Run adapter tests and commit**

Run: `go test ./internal/sbx ./internal/converge -run 'Sbx|Credential|Policy|BuildDriver'`

```bash
git add internal/sbx/runner.go internal/sbx/runner_test.go internal/sbx/fake.go internal/sbx/fake_test.go internal/converge/sbx.go internal/converge/sbx_test.go internal/converge/build.go internal/converge/build_test.go internal/converge/testdata
git commit -m "feat(init): converge declared sbx resources"
```

### Task 9: Orchestrate inspect, plan, apply, verify, and final commit ordering

**Files:**
- Create: `internal/converge/service.go`
- Create: `internal/converge/service_test.go`
- Create: `internal/source/candidate.go`
- Create: `internal/source/candidate_test.go`

**Interfaces:**
- Consumes: all preceding source/converge APIs, `worktree.Git`, `sbx.Runner`, and a clock.
- Produces: `converge.Service.Plan`, `converge.Service.Apply`, and `source.AcquireCandidate`.

- [ ] **Step 1: Write a no-mutation planning test**

Snapshot the temporary den home, source remote HEAD, fake sbx calls, and output before `Service.Plan`. Assert all remain unchanged except read-only Git/sbx inspection calls. Assert the plan contains every credential, policy, build, exported nest, and repo key. For ambiguous repositories, assert `UnconfirmedRepoMatches` returns them and a second plan with enriched `Answers.Repos` becomes fully confirmable.

- [ ] **Step 2: Write application ordering and failure tests**

Assert order: fresh global config when needed, applying receipt, credentials, policies, builds, repository discovery/readiness, source install/checkout, personal config, final receipt. Inject a failure at each managed resource. Assert the final personal version remains previous, an `applying` receipt records completed resources, and a second call skips resources whose `Verify` returns ready.

- [ ] **Step 3: Define service requests and results**

```go
type Request struct {
	Mode       Mode
	DenHome    string
	URL        string
	Name       string
	Answers    Answers
	Candidate  *source.Candidate
	DenVersion string
	FreshGlobalConfig []byte
}

type Mode string
const (
	ModeInit Mode = "init"
	ModeAdd Mode = "add"
	ModeConfigure Mode = "configure"
	ModeUpdate Mode = "update"
)

type DriverFactory interface {
	ForManifest(m *source.Manifest, answers Answers) ([]ResourceDriver, error)
}

type Result struct {
	Status   source.SourceStatus
	Personal source.Personal
	Receipt  source.Receipt
}

type Service struct {
	Git     worktree.Git
	Sbx     sbx.Runner
	Secrets sbx.SecretRunner
	Now     func() time.Time
	Drivers DriverFactory
}

func (s Service) Plan(ctx context.Context, req Request) (*Plan, error)
func (s Service) Apply(ctx context.Context, req Request, plan *Plan, out, errOut io.Writer) (*Result, error)
```

Modes are `init`, `add`, `configure`, and `update`. `configure` uses only the installed checkout. `init` and `add` may use a temporary clone. `update` uses a fetched detached candidate.

- [ ] **Step 4: Implement temporary acquisition and namespace arbitration**

Normalize source URLs before comparing ownership. `--name` wins; otherwise use `metadata.name`. Reject a namespace owned by another URL. A fresh clone stays temporary until managed resources verify. Install by atomic directory rename where the filesystem permits it; otherwise clone into a sibling temporary directory so rename remains same-filesystem.

- [ ] **Step 5: Implement final commit marker semantics**

After confirmation, write the fresh minimal global config first when needed, then write the `applying` receipt before the first managed resource mutation. Compute `manifest_digest` as lowercase `sha256:<hex>` over the exact `den-source.yaml` bytes; obtain commit from Git and `applied_at` from `Service.Now`. After verification, install/advance the checkout, write the new personal configuration atomically, then write the final receipt last. Missing repos update only confirmed `Personal.Repos` entries and yield `partially_ready`.

- [ ] **Step 6: Run convergence tests and commit**

Run: `go test ./internal/source ./internal/converge`

```bash
git add internal/source/candidate.go internal/source/candidate_test.go internal/converge/service.go internal/converge/service_test.go
git commit -m "feat(init): add resumable convergence service"
```

### Task 10: Wire init, add, configure, confirmation, and manual migration diagnostics

**Files:**
- Modify: `internal/cli/init.go`
- Modify: `internal/cli/init_test.go`
- Modify: `internal/deninit/deninit.go`
- Modify: `internal/deninit/deninit_test.go`
- Modify: `internal/cli/source.go`
- Modify: `internal/cli/source_test.go`
- Modify: `internal/cli/root.go`

**Interfaces:**
- Consumes: `converge.Service`, typed answers, rendering, existing `deninit.Run`, and legacy `source.Add`.
- Produces: `den init --source`, manifested `den source add`, and `den source configure`.

- [ ] **Step 1: Write CLI wiring tests**

Cover:

```text
den init --source file://<remote> --answers <file> --yes --den-home <tmp>
den source add file://<remote> --answers <file> --yes --den-home <tmp>
den source configure dg --answers <file> --yes --den-home <tmp>
```

Assert `--name` overrides `metadata.name`. Assert a no-TTY invocation without `--yes` prints the complete plan, exits without mutation, and tells the user to rerun with `--yes`. Assert interactive rejection leaves all files and sbx calls unchanged.

- [ ] **Step 2: Preserve existing init and legacy source tests**

Run the existing `TestInit*` and legacy source add/update cases unchanged before editing. Add explicit regression tests proving `den init` still creates the embedded example and a source without `den-source.yaml` still follows `source.Add`.

- [ ] **Step 3: Add source-aware initialization flags**

```go
type convergenceFlags struct {
	Source  string
	Name    string
	Answers string
	Yes     bool
}
```

Add `DenVersion func() string` to `cli.Deps`; `SystemDeps` returns `displayVersion`, and tests inject exact versions. When `--source` is empty, call the existing embedded-example path. Otherwise prepare `den.SourceAwareDenHome` in memory when `config.yaml` is absent, preserve an existing global config byte-for-byte, collect initial answers, calculate discovery, resolve unconfirmed repo matches, recalculate and print the final plan, confirm, then let `Service.Apply` write the prepared global config. Planning and rejected confirmation write nothing.

- [ ] **Step 4: Dispatch manifested and legacy source add**

Probe the candidate for `den-source.yaml` before mutation. Manifested sources use convergence. Legacy sources call the current `source.Add`. Add `source configure <name>` only for manifested sources and return a clear manifest-required error for legacy sources.

- [ ] **Step 5: Print manual legacy mapping instructions**

If keys needed by the manifested source exist under global `config.yaml.repos`, print an exact YAML block under the destination `source.PersonalPath(home, name)`. Do not copy or remove it. A test compares global `config.yaml` before and after byte-for-byte.

- [ ] **Step 6: Run CLI/full tests and commit**

Run: `go test ./internal/cli ./internal/deninit ./...`

```bash
git add internal/cli internal/deninit
git commit -m "feat(init): wire source onboarding commands"
```

### Task 11: Add explicit exact-version updates and configure-based resume

**Files:**
- Modify: `internal/source/candidate.go`
- Modify: `internal/source/candidate_test.go`
- Modify: `internal/source/mutate.go`
- Modify: `internal/source/mutate_test.go`
- Modify: `internal/converge/service.go`
- Modify: `internal/converge/service_test.go`
- Modify: `internal/cli/source.go`
- Modify: `internal/cli/source_test.go`

**Interfaces:**
- Consumes: existing dirty/unpushed guards, fetched candidate, SemVer comparison, convergence plan/apply, and receipt resume state.
- Produces: manifested `den source update <name>` with exact-version semantics; legacy `source.Update` remains unchanged.

- [ ] **Step 1: Write equal, greater, downgrade, dirty, and unpushed tests**

Use temporary remotes with successive commits and manifests. Assert:

- same version/same commit prints unchanged;
- same version/new commit warns and leaves checkout unchanged;
- greater version prints a full plan and requires confirmation;
- lower version refuses before checkout mutation;
- dirty or locally unique commits refuse before fetch/application;
- `source update` without a name continues to aggregate per-source failures; it dispatches legacy sources to the old fast-forward path and manifested sources to independent plan/confirmation cycles in sorted source-name order.

- [ ] **Step 2: Write partial-update resume tests**

Fail the second policy after the first credential verifies. Assert checkout may point at the target while `Personal.Version` stays previous, receipt is `applying`, source consumers refuse, and `source configure dg` completes without fetching or reapplying the verified credential.

- [ ] **Step 3: Split legacy fast-forward from manifested candidate fetch**

Keep `source.Update` as the legacy implementation. Add a fetched detached worktree API that returns candidate root, commit, manifest, and cleanup function without moving the installed checkout:

```go
type Candidate struct {
	Root     string
	Commit   string
	Manifest *Manifest
}

func (c *Candidate) Close() error
```

Use the checked-out branch's `@{u}` and preserve current dirty/upstream/ahead safety messages.

- [ ] **Step 4: Apply the candidate only after confirmation**

Compare `Candidate.Manifest.Metadata.Version` against `Personal.Version`. Build the full plan from candidate content. After confirmation, move checkout to candidate commit through a fast-forward, apply/verify, then finalize personal config and receipt. Do not fetch from configure/status/spawn/build.

- [ ] **Step 5: Run source/convergence/CLI tests and commit**

Run: `go test ./internal/source ./internal/converge ./internal/cli -run 'Update|Resume|Divergence'`

```bash
git add internal/source internal/converge/service.go internal/converge/service_test.go internal/cli/source.go internal/cli/source_test.go
git commit -m "feat(source): update exact source versions explicitly"
```

### Task 12: Add source status and doctor integration

**Files:**
- Modify: `internal/converge/service.go`
- Modify: `internal/converge/service_test.go`
- Modify: `internal/cli/source.go`
- Modify: `internal/cli/source_test.go`
- Modify: `internal/doctor/doctor.go`
- Modify: `internal/doctor/doctor_test.go`
- Modify: `internal/cli/doctor.go`
- Modify: `internal/cli/doctor_test.go`

**Interfaces:**
- Consumes: read-only resource inspectors, readiness evaluator, receipt, and renderers.
- Produces: `Service.Status(ctx, denHome, name)`, `den source status [name]`, and aggregated doctor checks.

- [ ] **Step 1: Write source status tests**

Assert one-name and all-source modes, stable ordering, no Git fetch calls, missing repo detail with URL/dependent nests/remedy, and non-zero exit for `blocked`/`unknown` only. Assert `partially_ready` exits zero.

- [ ] **Step 2: Write doctor regression for unavailable sbx**

Make the injected runner return the observed keychain/daemon error. Assert doctor contains an `unknown` source check, exits non-zero, and never prints `all good`. Add divergence and partially-ready cases.

- [ ] **Step 3: Implement read-only observation**

`Service.Status` loads installed manifest/config/receipt, runs resource `Inspect` only, reevaluates exported nests, and never calls `Apply`, `fetch`, or a mutating sbx command. `source status` uses `RenderStatus`.

- [ ] **Step 4: Extend doctor dependencies and levels**

> Divergence recorded in Task 1: `internal/doctor/doctor.go:405-413` still falls back on
> `g.Defaults.Stack` per nest and calls `stacks.Get("")` when both are empty, so a source-aware home
> with a stackless local nest is diagnosed as `stack "" not found` instead of "no stack is
> configured". `nest.Resolve` now refuses that case with both file paths named. Align doctor with
> that judge here, with its own test.


Add the same injected `sbx.Runner` and Git reader already available at CLI wiring. Map `ready` to OK, `partially_ready` to warning, and `blocked`/`unknown` to fail. Report receipt/checkout/config divergence as fail.

- [ ] **Step 5: Run tests and commit**

Run: `go test ./internal/doctor ./internal/converge ./internal/cli -run 'Doctor|SourceStatus|Unknown'`

```bash
git add internal/converge internal/doctor internal/cli/source.go internal/cli/source_test.go internal/cli/doctor.go internal/cli/doctor_test.go
git commit -m "feat(doctor): report manifested source readiness"
```

### Task 13: Lock the complete Den acceptance flow

**Files:**
- Create: `internal/converge/acceptance_test.go`
- Modify: `README.md`
- Modify: `CHANGELOG.md`

**Interfaces:**
- Consumes: public Cobra commands through `cli.NewRootCmdWith` and all fake adapters.
- Produces: one tracer-bullet acceptance test for fresh/existing init, configure, status, update, and resume.

- [ ] **Step 1: Add the fresh-home acceptance test**

Build a temporary source remote with manifest, stack, nests, and provision files. Create temporary present repos with normalized remotes. Invoke the real root command with an answer file and `--yes`. Assert sbx command order, build order, installed source, no example nest/stack, source-scoped mappings, final receipt, redacted output, and `ready` status.

- [ ] **Step 2: Add partial and existing-home acceptance cases**

Omit one required repo and one optional repo. Assert installation succeeds as `partially_ready` and names only the required one. Start from a non-default global config and assert it remains byte-identical. Add the missing repo, run configure, and assert transition to `ready` without remote access.

- [ ] **Step 3: Add update/resume acceptance cases**

Publish `1.1.0`, reject confirmation once, then accept and inject a mid-apply failure. Assert exact version remains `1.0.0`, source consumers refuse, configure resumes, and the final receipt/config converge on `1.1.0`.

- [ ] **Step 4: Add the opt-in real-source acceptance entry point**

```go
func TestDigitaleoManifestAcceptance(t *testing.T) {
	root := os.Getenv("DIGITALEO_DEN_ENV")
	if root == "" {
		t.Skip("DIGITALEO_DEN_ENV is not set")
	}
	runSourceAcceptance(t, root, SourceExpectations{
		Name: "dg", Version: "1.0.0", Nests: 5, Stacks: []string{"base"},
		Credentials: 3, BuildNetworkRules: 2,
	})
}
```

`runSourceAcceptance` copies the supplied source into a temporary Git remote and uses only fake sbx adapters plus temporary repositories. It never reads or mutates the real den home.

- [ ] **Step 5: Document generic behavior**

Update README with `den init --source`, `--answers`, `--yes`, manifest/answer examples using snake_case, status meanings, exact update behavior, trust boundary, legacy compatibility, and manual global-to-source mapping migration. State that MCP values and repo cloning remain outside the flow.

- [ ] **Step 6: Run all Den verification**

Run: `go test ./...`

Run: `go vet ./...`

Run: `go test -race ./...`

Expected: all commands PASS.

- [ ] **Step 7: Build the exact binary used by cross-repository checks**

Run: `go build -ldflags '-X github.com/PillowPillow/den/internal/cli.Version=1.7.0' -o /tmp/den-source-onboarding ./cmd/den`

Expected: `/tmp/den-source-onboarding version` prints `den 1.7.0` and exits 0.

- [ ] **Step 8: Commit**

```bash
git add internal/converge/acceptance_test.go README.md CHANGELOG.md
git commit -m "test(init): lock source onboarding flow"
```

### Task 14: Publish the Digitaleo declarative source

**Working directory:** `/Users/polochon/Development/Digitaleo/digitaleo-den-env`

**Files:**
- Create: `den-source.yaml`
- Create: `testdata/onboarding-answers.yaml`
- Modify: `README.md`

**Interfaces:**
- Consumes: Den's finalized manifest schema and `den lint`.
- Produces: Digitaleo source functional version `1.0.0`, recommended namespace `dg`, five nest exports, one stack export, and the resources currently documented as manual prerequisites.

- [ ] **Step 1: Create the real manifest**

```yaml
schema_version: 1
kind: source

metadata:
  name: dg
  version: 1.0.0

requires:
  den: ">=1.7.0"
  sbx: ">=0.38.0"

exports:
  nests:
    - { name: agentic-bank, path: nests/agentic-bank.yaml }
    - { name: go-dgdev, path: nests/go-dgdev.yaml }
    - { name: kafoutche, path: nests/kafoutche.yaml }
    - { name: leo, path: nests/leo.yaml }
    - { name: op-inscription, path: nests/op-inscription.yaml }
  stacks:
    - { name: base, path: stacks/base/stack.yaml }

inputs:
  credentials:
    gitlab_token:
      prompt: GitLab personal access token

resources:
  credentials:
    - { id: github, type: sbx_github, scope: global }
    - id: gitlab_registry
      type: sbx_registry
      scope: global
      host: gitlab.digitaleo.com:4567
      value_from: { credential: gitlab_token }
    - id: gitlab_http
      type: sbx_http_substitution
      scope: global
      host: gitlab.digitaleo.com
      environment: GITLAB_TOKEN
      value_from: { credential: gitlab_token }
  build_network:
    allow:
      - cdn.playwright.dev
      - acli.atlassian.com
  builds:
    - stack: base
```

- [ ] **Step 2: Add a safe answer-file fixture**

```yaml
repository_roots:
  - ~/Development/Digitaleo
  - ~/Development/Kampn

credentials:
  gitlab_token:
    from_env: GLPAT
```

The fixture names an environment variable but contains no secret.

- [ ] **Step 3: Validate the source with the built Den binary**

Run from the Digitaleo repository: `/tmp/den-source-onboarding lint .`

Expected: exit 0 with no lint finding.

- [ ] **Step 4: Replace the manual installation section**

Lead with:

```bash
export GLPAT='...'
den init --source <url-de-ce-repo> --answers testdata/onboarding-answers.yaml
```

Keep detailed sbx credential/policy commands in a troubleshooting section. Add exact manual migration instructions from `~/.den/config.yaml.repos` to `~/.den/source-config/dg.yaml.repos`. Explain `ready`/`partially_ready`, `den source configure dg`, and `den source update dg`.

- [ ] **Step 5: Run Digitaleo acceptance with fake sbx from the Den repository**

Run from Den with the explicit source path: `DIGITALEO_DEN_ENV=/Users/polochon/Development/Digitaleo/digitaleo-den-env go test ./internal/converge -run TestDigitaleoManifestAcceptance -v`

`TestDigitaleoManifestAcceptance`, created in Task 13, skips only when `DIGITALEO_DEN_ENV` is unset. When set, it copies that source into a temporary Git remote and drives the same fake-sbx acceptance harness as the generic fixture. It asserts the three credentials, two policies, `base` build, all five nests, source-scoped mappings, readiness, redaction, personal config, and receipt.

- [ ] **Step 6: Commit in the Digitaleo repository**

```bash
git add den-source.yaml testdata/onboarding-answers.yaml README.md
git commit -m "feat: publish declarative den source"
```

### Task 15: Run cross-repository verification and prepare review

**Files:**
- Modify only files required by verification failures attributable to this feature.

**Interfaces:**
- Consumes: both completed repositories.
- Produces: reviewable commits with no uncommitted feature changes and evidence for the complete first-time flow.

- [ ] **Step 1: Verify Den from a clean process**

Run in Den:

```bash
go test ./...
go vet ./...
go test -race ./...
git diff --check
git status --short
```

Expected: tests and vet PASS; diff check is empty; status contains no uncommitted feature file.

- [ ] **Step 2: Verify the Digitaleo source**

Run in `digitaleo-den-env`:

```bash
/tmp/den-source-onboarding lint .
git diff --check
git status --short
```

Expected: lint exits 0; diff check is empty; status contains no uncommitted feature file.

- [ ] **Step 3: Exercise a temporary end-to-end home**

Run `DIGITALEO_DEN_ENV=/Users/polochon/Development/Digitaleo/digitaleo-den-env go test ./internal/converge -run TestDigitaleoManifestAcceptance -v`. The harness uses a temporary `--den-home`, a file URL to a temporary source clone, and fake sbx adapters. Never point this verification at the real `~/.den` or mutate live sbx credentials/policies. Assert `source status`, source config, receipt, and missing-repo remedies from command output.

- [ ] **Step 4: Request code review**

Invoke `superpowers:requesting-code-review`. Review the Den commits against `docs/superpowers/specs/2026-08-14-source-onboarding-design.md`, then review the separate Digitaleo commit against the finalized Den schema. Resolve findings with focused tests and separate fix commits.
