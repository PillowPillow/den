# den templates Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `bases:` and `agents:` with `templates:` and `default_template`: a template's definition lives in a `den-template.yaml` at the root of a git repo or a local folder, den builds it into an sbx template, records what it built, and `den up` uses it or an official image directly.

**Architecture:** A new package `internal/template` owns everything that has no sbx side effect: resolve a `template:`/`default_template` value to a declared template or an image, load and confine `den-template.yaml`, the local copy of a `repo:` location, the template record under `state/v2/templates/`, and the `den up` notices. `internal/build` (replaces `internal/base`) runs the build VM and writes the copy and the record only after a successful save. `nest.Resolve` carries a `*template.Resolved` instead of a base; `instance` reads it. Docker Hub and git reach the code through `cli.Deps`.

**Tech Stack:** Go 1.26, cobra, `gopkg.in/yaml.v3` (strict), `net/http` (Docker Hub, production only), Taskfile (`task check`). sbx v0.53.0.

**Spec:** `docs/superpowers/specs/2026-11-16-den-templates-design.md` (French). The validation record `docs/superpowers/specs/2026-11-16-den-templates-validation.md` wins over it on any conflict. Measurements: `docs/superpowers/specs/2026-11-12-den-v2-spikes.md`, section S8.

## Decisions this plan rests on

Settled on the validation record (39 lines): do not reopen. Settled by the user on 2026-11-16, during planning:

- **T1. The digest is required.** For a template whose root `from` is an image, the record stores the digest that `sbx template ls --json` reports (S8a). `den build` reads it right after it creates the build VM, before the first step. If the digest is empty or missing, the build stops and names the reference. `den up` always gives the den-claude kit `<repository>@<digest>`. The design's "reference only" fallback does not exist.
- **T2. No `task build` before Task 12.** `den` on PATH is a symlink to the repo's `./den` (memory note *den V2 direction*). After one `task build` of this code, the current den is gone and `den rm am` cannot read am's schema-1 record. Tasks 1–11 use `task check` and, for a manual run, `go build -o <scratchpad>/den ./cmd/den`. Task 12 step 0 removes `am` with the current den first.

Chosen by the planner (reversible; the reviewer can reject each one):

- **T3. One den-claude kit per instance and per build.** Today all instances share `gen/.kits/den-claude/`, and each create rewrites its `image:`. That works with one global image. With an image per template, nest B would rewrite the kit that instance A was created from. The kit moves to `<GenDir>/den-claude/` (instance) and `gen/.build/<name>/den-claude/` (build). `den rm` already deletes the gen dir after the VM.
- **T4. Short image names.** A value without `/` is a short name. It must match `^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$` and is completed to `docker.io/docker/sandbox-templates:<value>`. A value with `/` is a full address and is used as written. `ubuntu:22.04` is refused with "write the full address".
- **T5. Kits in `den-template.yaml`: path or reference.** An entry is a reference that sbx resolves when its first `/` component is a registry host: it contains `.` or `:` and is not `.` or `..`, or it is `localhost` (the rule of `sbx.isRegistry`). Any other entry is a path relative to `den-template.yaml` (B9), so the design's `kits/ssh-known-hosts` and `den-env`'s `./kits/ssh-known-hosts` both work. A Docker Hub shorthand kit (`acme/kit:1`) must be written `docker.io/acme/kit:1`. `den.yaml` keeps its own rule.
- **T6. Fetch = a fresh clone into a staging dir**, on the first build and on every `--pull`. No `fetch` into the live copy. A failed build deletes the staging dir; nothing else moves (B16).
- **T7. Trusted URLs** live in `<denHome>/state/v2/template-sources.yaml`, written when the user confirms (B17), before the clone. A URL declared in the global file needs no confirmation.
- **T8. No git prompt, ever.** `neutralEnvironment()` adds `GIT_TERMINAL_PROMPT=0` to every git call den makes. `git ls-remote` also gets `-c core.sshCommand=ssh -o BatchMode=yes -o ConnectTimeout=3`, and `RunWithInput` sets `cmd.WaitDelay`, so a killed git cannot hold `den up` through an ssh child.
- **T9. Tag filter = the design's rule, exactly.** Drop a tag that contains `nightly` or matches `[0-9]+\.[0-9]+\.[0-9]+`. On the 226 tags of 2026-11-16 this keeps 29 names, `latest` and `claude-code-minimal-test` included.
- **T10. `den.yaml` keeps `schema: 1`.** A lenient pre-pass reports the removed keys before the strict decode, so the error names the replacement instead of yaml's "field not found".
- **T11. Instance record schema 2** adds `template` (the value the nest resolved: a name or an image) and `tag` (the sbx template tag, empty for an image).
- **T12. Package split.** `internal/template` imports `config`, `sbx`, `worktree` and never `instance`, because `instance` imports it. `internal/build` imports both.

## Global Constraints

- sbx floor `sbx.MinVersion = "v0.53.0"`, checked once by `up`, `exec` and `build` before their first side effect.
- Strict YAML everywhere (`config.DecodeYAMLStrict`), `den-template.yaml` and the template record included.
- No value is written in den's code for the template or the image (A9, §5.1.2). The embedded `den-claude/spec.yaml` keeps an `image:` line whose value is a placeholder that den always replaces.
- Every `sbx env create` and `sbx env rm` gets `--no-input … --force --skip-host-commands`.
- Every git call goes through `cli.Deps.Git` (`worktree.Git`). Tests clone a local repo by path. No test opens a socket: Docker Hub goes through `cli.Deps.Hub`.
- No test calls `t.Parallel()` or spawns a process other than git. A package that runs real git calls `worktree.NeutralizeGitEnvironment()` in `TestMain`.
- Template tag: `<name in den-template.yaml>:v2`. Record: `<denHome>/state/v2/templates/<name>.yaml`. Copy: `<denHome>/gen/.templates/<name>/`. Staging: `<denHome>/gen/.templates/.staging-<name>/`.
- Notices go to stderr from `den up` only, never block, never come from `den exec` or `den shell`.
- Errors name the file to fix and the remedy. Code, comments and messages in English. "Why" comments at the decision site, matching the density around them.
- `task check` passes before every commit. Goldens are edited by hand. Exclude `.claude/worktrees/` from every grep.
- **No `task build` before Task 12 step 0 (T2).** Every sbx measurement uses a throwaway sandbox named `den-probe-*`, created and removed by the agent with `--skip-host-commands`. The agent never touches the instance `am`.
- Any write under `~/.den/` or in the `den-env` repo needs the user's approval at the moment it runs.

## Review Focus

1. **`den up` with the remote unreachable or asking for a credential** (offline, VPN down, ssh key with a passphrase and no agent). Expected: no prompt on the tty, no message, `den up` continues within ~3 s. Tests in Task 5 (argv carries `BatchMode=yes`), Task 8b (`den up` returns within the deadline) and Task 1 (`GIT_TERMINAL_PROMPT=0`).
2. **A location that tries to reach the host.** A `den-template.yaml` path through `..`, an absolute path, `~`, or a symlink that leaves the location; a `repo:` URL starting with `-`. Expected: a load error naming the file and the field; git always receives `--` before the URL. Tests in Tasks 2 (URL), 3 (paths) and 5 (`--`).
3. **A build that fails halfway** (a step exits 1, the digest is unreadable, Ctrl-C). Expected: the previous copy, record and sbx template stay; the staging dir and the build VM are removed. Test in Task 7.
4. **A schema-1 instance record left behind after the upgrade.** Expected: `den rm` of another instance with a worktree refuses, names the file, and says `sbx rm <instance>` then delete the file. Test in Task 9.
5. **A Live instance whose template was rebuilt.** Expected: `den up` attaches and warns that the config changed (`den rm` then `den up`); it never refuses. Test in Task 8a.

---

## File Structure

```
internal/sbx/template.go               MODIFY  Template.Digest; two v0.35.0 comments corrected
internal/worktree/worktree.go          MODIFY  GIT_TERMINAL_PROMPT=0, cmd.WaitDelay
internal/config/schema.go, load.go     MODIFY  templates:, default_template, nests.<n>.template; bases/agents/base removed
internal/config/definition.go          NEW     den-template.yaml: schema, strict load, confinement, from-cycle check
internal/hub/                          NEW     Catalog interface, real client, ShortNames, Describe, Fake
internal/template/choose.go            NEW     value → declared template or image (A6, B1, B3, B29, B30, B4)
internal/template/record.go            NEW     template record + trusted URLs
internal/template/copy.go              NEW     clone, HEAD, ls-remote, definition hash
internal/template/open.go              NEW     Open: record + source + definition → *Resolved
internal/template/notice.go            NEW     den up notices (A8, B20, B21)
internal/build/                        NEW     replaces internal/base (build.go, provision.go moved)
internal/nest/resolve.go               MODIFY  Resolved.Template replaces Base*, AgentImage; FindBase deleted
internal/instance/{prepare,create}.go  MODIFY  checkTemplate, per-instance kit, overlays, configHash, notices
internal/registry/registry.go          MODIFY  schema 2, template + tag, schema-1 remedy
internal/sbxenv/kits.go, den-claude/   MODIFY  DenClaudeKit(image) (error on empty), placeholder image
internal/cli/{root,build,up,exec}.go   MODIFY  Deps.Hub, build flags, no-template explanation
README.md, CLAUDE.md, examples/        MODIFY  template vocabulary, den home layout, example den-template.yaml
```

---

### Task 1: sbx digest and git hardening (measurement S8 → code)

The measurement of design §5.4 ran on 2026-11-16 (spikes S8a, S8b). This task turns S8a into code and adds T8's git environment.

**Files:**
- Modify: `internal/sbx/template.go`, `internal/sbx/template_test.go`, `internal/worktree/worktree.go`, `internal/worktree/worktree_test.go`

**Interfaces:**
- Produces: `sbx.Template.Digest string` (`json:"digest"`). `sbx.ImageWithDigest(ref, digest string) string` returns `<NormalizeImageRef(ref) repository>@<digest>`.

- [ ] **Step 1: Write the failing tests.**
  - `TestTemplatesReadsTheDigest`: Fake answers `template ls --json` with the S8a row (`"repository":"docker.io/docker/sandbox-templates","tag":"claude-code-docker","digest":"sha256:4a94d9144a70df744fd9247e0c365a14cfb97024afab8db7597c8ba3e0637642"`). Assert `FindTemplate(list, "docker.io/docker/sandbox-templates:claude-code-docker").Digest` equals that digest.
  - `TestImageWithDigest`: `ImageWithDigest("docker.io/docker/sandbox-templates:claude-code-docker", "sha256:4a94…")` == `"docker.io/docker/sandbox-templates@sha256:4a94…"` (full 64 hex); `ImageWithDigest("devx:v1", d)` == `"docker.io/library/devx@"+d`.
  - `TestNeutralEnvironmentDisablesPrompts` (package `worktree`): `neutralEnvironment()` contains `GIT_TERMINAL_PROMPT=0` exactly once, even when the parent environment sets it to `1`.
- [ ] **Step 2:** Run `go test ./internal/sbx/ ./internal/worktree/ -count=1`. Expected: FAIL.
- [ ] **Step 3:** Add the field and `ImageWithDigest`. Rewrite the `Template` doc schema line and the `IsDigestRef` paragraph that says sbx reports no digest (true for v0.35.0, false for v0.53.0, S8a). In `neutralEnvironment`, drop any `GIT_TERMINAL_PROMPT` and append `GIT_TERMINAL_PROMPT=0`. In `RunWithInput`, set `cmd.WaitDelay = time.Second`.
- [ ] **Step 4:** Run `task check` (never `task build`: it replaces the user's `den`, T2). Expected: PASS.
- [ ] **Step 5: Commit** `feat(sbx): read a template's digest; git never prompts`.

---

### Task 2: config — `templates:` and `default_template` (additive)

**Files:**
- Modify: `internal/config/schema.go`, `internal/config/load.go`, `internal/config/load_test.go`, `internal/config/testdata/`

**Interfaces:**
- Produces:

```go
type TemplateEntry struct {
    Repo string `yaml:"repo"`
    Dir  string `yaml:"dir"`  // absolute after Load (~ expanded, relative to the declaring file)
    Name string `yaml:"name"` // "" = the entry's key
    Ref  string `yaml:"ref"`  // repo only; "" = the remote's default branch
}
// File gains:   Templates map[string]TemplateEntry `yaml:"templates"`; DefaultTemplate string `yaml:"default_template"`
// Nest gains:   Template string `yaml:"template"` (optional)
func (e TemplateEntry) TemplateName(key string) string
```

This task is additive: `Bases`, `Agents` and `Nest.Base` stay, read by the old code, until Task 8a deletes them in the same commit that switches the readers. Every task keeps `task check` green.

- [ ] **Step 1: Write the failing tests** in `load_test.go`:
  - `TestLoadAcceptsTemplatesInBothFiles` (A3): the same `templates:` + `default_template` block loads in a global file and in a project file.
  - `TestLoadRefusesATemplateEntryWithoutOneLocation`: both `repo` and `dir`, then neither → `templates.x: set exactly one of repo and dir`.
  - `TestLoadRefusesRefOnADir`: `dir` + `ref` → `templates.x.ref: only a repo has a ref`.
  - `TestLoadRefusesARepoStartingWithADash`: `repo: --upload-pack=x` → `templates.x.repo: must not start with "-"`.
  - `TestLoadAnchorsATemplateDir`: `dir: ./tpl` in `/p/den.yaml` → `/p/tpl`; `dir: ~/t` → home-expanded.
  - `TestLoadAcceptsANestWithoutBase`: `nests.dev` with repos and `template: x` instead of `base:` loads (the `base: required` check goes; resolution happens in Task 4).
- [ ] **Step 2:** Run `go test ./internal/config/ -count=1`. Expected: FAIL.
- [ ] **Step 3:** Implement. Validate entry keys and `name` with `ValidateName("template", …)`; absolutize `Dir` in `absolutize`.
- [ ] **Step 4:** Run `task check` (never `task build`: it replaces the user's `den`, T2). Expected: PASS.
- [ ] **Step 5: Commit** `feat(config): templates and default_template in den.yaml`.

---

### Task 3: config — `den-template.yaml`

**Files:**
- Create: `internal/config/definition.go`, `internal/config/definition_test.go`, `internal/config/testdata/definitions/`

**Interfaces:**
- Produces:

```go
const DefinitionFileName = "den-template.yaml"
const DefinitionSchema = 1
type Definitions struct {
    Schema    int                   `yaml:"schema"`
    Templates map[string]Definition `yaml:"templates"`
}
type Definition struct {
    From    string   `yaml:"from"`
    Kits    []string `yaml:"kits"`
    Network Network  `yaml:"network"`
    Build   Build    `yaml:"build"`
}
type LoadedDefinitions struct { Path, Root string; File Definitions } // Root = dir of Path, symlinks resolved
func LoadDefinitions(root string) (*LoadedDefinitions, error)
func (l *LoadedDefinitions) IsSibling(from string) bool
```

- [ ] **Step 1: Write the failing tests:**
  - `TestLoadDefinitionsResolvesPathsUnderTheRoot`: `kits: [./kits/a, docker.io/acme/kit:1, localhost:5000/k]`, `build.includes: [lib/c.sh]`, `build.steps: [s/1.sh]` → kits `[<root>/kits/a, docker.io/acme/kit:1, localhost:5000/k]` (T5), includes and steps absolute under root.
  - `TestLoadDefinitionsReadsTheDesignExample`: the design §5.2 block verbatim (`kits: [kits/ssh-known-hosts, kits/shell-zsh, stacks/base/kit]`) → three paths under root.
  - `TestLoadDefinitionsRefusesAnEscape` (table): `kits: [../x]`, `kits: [/etc]`, `kits: [~/k]`, `build.steps: [../../s.sh]`, `build.includes: [/tmp/i.sh]`, and `kits: [./link]` where `link` is a symlink to a dir outside root → error naming `den-template.yaml` and the field, e.g. `templates.t.kits[0]: "../x" leaves <root> — a location may only cite its own files`.
  - `TestLoadDefinitionsRefusesAFromCycle`: `a.from: b`, `b.from: a` → `templates.a.from: cycle a → b → a`.
  - `TestLoadDefinitionsIsStrict`: an `image:` key → error naming the file (unknown field).
  - `TestLoadDefinitionsNamesTheMissingFile`: an empty dir → error with the path `<root>/den-template.yaml`.
- [ ] **Step 2:** Run `go test ./internal/config/ -run Definitions -count=1`. Expected: FAIL.
- [ ] **Step 3:** Implement. A path is confined when it lies under Root (a local helper in `config`, same logic as `instance.relUnder`, which `config` cannot import) for the lexical form and for the `filepath.EvalSymlinks` form (missing target: lexical check only, the build reports the missing file).
- [ ] **Step 4:** Run `task check` (never `task build`: it replaces the user's `den`, T2). Expected: PASS.
- [ ] **Step 5: Commit** `feat(config): den-template.yaml, confined to its location`.

---

### Task 4: template — choose a template or an image; Docker Hub catalog

**Files:**
- Create: `internal/template/choose.go`, `choose_test.go`; `internal/hub/hub.go`, `hub_test.go`, `fake.go`, `testdata/tags-2026-11-16.txt` (copy of `docs/superpowers/plans/2026-11-16-den-templates-hub-tags.txt`)

**Interfaces:**
- Produces:

```go
package template
const OfficialRepository = "docker.io/docker/sandbox-templates"
type Choice struct {
    Value      string               // as written
    Key        string               // templates: key; "" for an image
    Entry      config.TemplateEntry // zero for an image
    DeclaredIn *config.Loaded       // file whose templates: holds Key
    Image      string               // completed reference; "" for a template
    Origin     string               // the key to fix, e.g. "`default_template` in /h/.den/den.yaml" or "`template:` of nest \"dev\" in /p/den.yaml"
    Searched   []string             // the den.yaml files whose templates: were searched
}
func (c Choice) IsImage() bool
func (c Choice) Name() string        // Entry.TemplateName(Key)
func (c Choice) Tag() string         // Name() + ":v2"
type NoTemplateError struct{ Nest string; Files []string }
func Choose(nestFile, global *config.Loaded, nestName string) (Choice, error) // template, then nestFile default_template, then global default_template
func Lookup(first, global *config.Loaded, value string) (Choice, error)       // first.templates, then global.templates, then image
func CompleteImage(value string) (string, error)                               // T4

package hub
const TagsPage = "https://hub.docker.com/r/docker/sandbox-templates/tags"
type Catalog interface{ Tags(ctx context.Context) ([]string, error) }
func New() Catalog                       // NewWith(&http.Client{})
func NewWith(c *http.Client) Catalog     // GET …/v2/repositories/docker/sandbox-templates/tags?page_size=100, follows `next`, one 5 s deadline for all pages
func ShortNames(tags []string) []string  // T9, sorted
func Describe(ctx context.Context, c Catalog) string
type Fake struct{ Names []string; Err error }
```

- [ ] **Step 1: Write the failing tests:**
  - `TestChooseTakesTheNestThenTheDefaults`: nest `template: legacy` wins; without it the nest file's `default_template`; without that the global's.
  - `TestLookupProjectMasksGlobal` (B1): both declare `digitaleo` → `DeclaredIn` is the project file.
  - `TestLookupFallsBackToAnImage` (B29, B30): `claude-code-docker` → `Image == "docker.io/docker/sandbox-templates:claude-code-docker"`; `ghcr.io/acme/x:1` → as written; a nest `template: claude-code` works too (B3).
  - `TestLookupRefusesAValueThatIsNeither`: `ubuntu:22.04` → error naming both files and `write the full address`.
  - `TestLookupHandlesNoProjectFile`: `first == nil` (den build outside any project) → searches the global file only.
  - `TestChooseRecordsTheOrigin`: the three cases of the first test → `Origin` names `template:` of the nest, then `default_template` of the nest file, then of the global file.
  - `TestChooseRefusesWithoutAnyTemplate` (B4): no value anywhere → `*NoTemplateError` with both file paths.
  - `TestShortNamesKeepsTheDesignRule`: the fixture's 226 names → exactly `cagent claude-code claude-code-docker claude-code-minimal claude-code-minimal-test codex codex-docker copilot copilot-docker cursor-agent cursor-agent-docker devin devin-docker docker-agent docker-agent-docker droid droid-docker factory-ai factory-ai-docker gemini gemini-docker kiro kiro-docker latest opencode opencode-docker shell shell-docker ubuntu-python`.
  - `TestCatalogFollowsNext`: `NewWith` on an `http.Client` whose `Transport` is a fake `RoundTripper` (no socket) serving three pages linked by `next` → all names, in order; a transport that blocks until the request context ends → error within the deadline.
  - `TestDescribeListsOrPointsAtThePage`: `Fake{Names: …}` → `official images: claude-code, claude-code-docker, …`; `Fake{Err: …}` → `official images: see https://hub.docker.com/r/docker/sandbox-templates/tags`.
- [ ] **Step 2:** Run `go test ./internal/template/ ./internal/hub/ -count=1`. Expected: FAIL.
- [ ] **Step 3:** Implement. `NoTemplateError.Error()`: `nest %q has no template: set template: on it, or default_template in %s` (files joined with " or "). `hub.New` is the only code that opens a socket; tests reach the client through `NewWith` and a fake `RoundTripper` (Global Constraints).
- [ ] **Step 4:** Run `task check` (never `task build`: it replaces the user's `den`, T2). Expected: PASS.
- [ ] **Step 5: Commit** `feat(template): choose a template or an official image`.

---

### Task 5: template — record, trusted URLs, copy, definition hash

**Files:**
- Create: `internal/template/record.go`, `copy.go`, tests `record_test.go`, `copy_test.go` (real git; `TestMain` calls `worktree.NeutralizeGitEnvironment()`)

**Interfaces:**
- Produces:

```go
const RecordSchema = 1
type Source struct {
    Kind string `yaml:"kind"`          // "repo" | "dir"
    URL  string `yaml:"url,omitempty"`
    Ref  string `yaml:"ref,omitempty"`
    Dir  string `yaml:"dir,omitempty"`
}
type From struct{ Ref, Digest string }  // yaml: ref, digest — the root image of the from chain (T1)
type Record struct {
    Schema         int       `yaml:"schema"`
    Template       string    `yaml:"template"`
    Tag            string    `yaml:"tag"`
    Source         Source    `yaml:"source"`
    Commit         string    `yaml:"commit,omitempty"`
    DefinitionHash string    `yaml:"definition_hash"`
    From           From      `yaml:"from"`
    BuiltAt        time.Time `yaml:"built_at"`
}
func SourceOf(c Choice) Source
func ReadRecord(denHome, name string) (Record, error)   // fs.ErrNotExist through config.FileError
func WriteRecord(denHome string, r Record) error        // schema stamped, temp file + rename, 0600
func Trusted(denHome, url string) (bool, error)
func Trust(denHome, url string) error
func CopyDir(denHome, name string) string
func StagingDir(denHome, name string) string
func Clone(ctx context.Context, g worktree.Git, url, ref, dest string) (commit string, err error)
func Head(ctx context.Context, g worktree.Git, dir string) (string, error)
func RemoteHead(ctx context.Context, g worktree.Git, url, ref string) (string, error) // own 3 s deadline; picks the line named refs/tags/<ref>^{}, else refs/heads/<ref>, else refs/tags/<ref> (HEAD when ref is "")
func DefinitionHash(d *config.LoadedDefinitions, name string) (string, error)
```

- [ ] **Step 1: Write the failing tests:**
  - `TestRecordRoundTrip` + `TestReadRecordRefusesAnotherSchema` + `TestReadRecordRefusesAMismatchedName` (the registry pattern).
  - `TestTrustIsPerURL`: `Trust(a)` → `Trusted(a)` true, `Trusted(b)` false; file under `state/v2/template-sources.yaml`.
  - `TestCloneDisablesHooksAndSubmodules`: a spy `worktree.Git` wrapping the real one records argv; `Clone` of a local bare repo → argv is `-c core.hooksPath=/dev/null clone --no-recurse-submodules --single-branch --branch <ref> -- <url> <dest>` (no `--branch` when ref is ""); `commit` equals the source's HEAD.
  - `TestRemoteHeadIsBatchModeAndBounded`: spy argv starts `-c core.sshCommand=ssh -o BatchMode=yes -o ConnectTimeout=3 ls-remote -- <url> <ref or HEAD>`; a fake Git that blocks until `ctx.Done()` makes `RemoteHead` return an error in under 4 s.
  - `TestRemoteHeadPeelsAnnotatedTags` (B5): fake output `aaaa…\trefs/tags/v1`, `bbbb…\trefs/tags/v1^{}`, `cccc…\trefs/heads/x/v1` → `bbbb…` (the commit a clone of `v1` checks out, measured: `git ls-remote . 'refs/tags/*'` lists the tag object first, then `^{}`); `ref: main` with a decoy `refs/heads/x/main` → the `refs/heads/main` line.
  - `TestDefinitionHashMovesWithAnyCitedFile`: changing `den-template.yaml`, one file inside a cited kit dir, an include, or a step changes the hash; changing a file the template does not cite does not.
- [ ] **Step 2:** Run `go test ./internal/template/ -count=1`. Expected: FAIL.
- [ ] **Step 3:** Implement. `DefinitionHash` hashes `den-template.yaml`'s bytes, then each cited local kit (sorted walk: relative path + bytes), include and step.
- [ ] **Step 4:** Run `task check` (never `task build`: it replaces the user's `den`, T2). Expected: PASS.
- [ ] **Step 5: Commit** `feat(template): record, trusted sources and the local copy`.

---

### Task 6: template — `Open` and the `den up` notices

**Files:**
- Create: `internal/template/open.go`, `notice.go`, tests `open_test.go`, `notice_test.go`

**Interfaces:**
- Consumes: Tasks 3–5.
- Produces:

```go
type Resolved struct {
    Choice   Choice
    Defs     *config.LoadedDefinitions // nil for an image
    Def      config.Definition         // zero for an image
    Record   *Record                   // nil for an image
    KitImage string                    // image: c.Image; template: sbx.ImageWithDigest(Record.From.Ref, Record.From.Digest)
}
func Open(denHome string, c Choice) (*Resolved, error)
func (r *Resolved) Root() string // copy dir (repo) or Entry.Dir (dir); "" for an image
func Notices(ctx context.Context, g worktree.Git, r *Resolved) []string
```

- [ ] **Step 1: Write the failing tests:**
  - `TestOpenRefusesAnUnbuiltTemplate` (B19): no record → `template "digitaleo" is not built — run ` + "`den build digitaleo`" + ` from <project dir>` (the `from` part only when `DeclaredIn` is a project file).
  - `TestOpenRefusesAnotherSource` (B18, §5.5): record source `repo A`, choice `repo B` → error naming both sources and `den build digitaleo --force`.
  - `TestOpenRefusesAMissingCopy`: repo record present, copy dir absent → same `den build` remedy.
  - `TestOpenReadsTheDefinitionFromTheCopy`: `Def.Kits` come from `gen/.templates/<name>/den-template.yaml`; `KitImage == "docker.io/docker/sandbox-templates@sha256:4a94…"`.
  - `TestOpenOnAnImageReadsNothing`: `Open(home, imageChoice)` touches no file; `KitImage == Choice.Image`.
  - `TestNoticeOnANewerRemote` (A8): fake Git answers ls-remote `0123abcd…\tHEAD`, record commit `89abcdef…` → `template digitaleo: a newer definition is available (89abcdef -> 0123abcd); run 'den build digitaleo --pull'`.
  - `TestNoticeIsSilentOnError` (B20): ls-remote error or timeout → no notice.
  - `TestNoticeOnADriftedDir` (B21): `DefinitionHash` differs from the record → `template legacy: its definition changed since the last build; run 'den build legacy'`.
- [ ] **Step 2:** Run `go test ./internal/template/ -count=1`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `task check` (never `task build`: it replaces the user's `den`, T2). Expected: PASS.
- [ ] **Step 5: Commit** `feat(template): open a built template, notice a newer definition`.

---

### Task 7: `den build <template> [--pull] [--yes] [--force]`

**Files:**
- Create: `internal/build/build.go`, `internal/build/provision.go` (moved from `internal/base/`), tests (ported from `internal/base/*_test.go`, real git `TestMain`)
- Delete: `internal/base/`
- Create: `internal/cli/notemplate.go`
- Modify: `internal/cli/build.go`, `internal/cli/root.go` (`Deps.Hub hub.Catalog`, `SystemDeps` wires `hub.New()`)

**Interfaces:**
- Consumes: `template.Lookup`, `Open`, `Clone`, `Head`, `ReadRecord`, `WriteRecord`, `Trusted`, `Trust`, `DefinitionHash`, `sbx.Templates`, `sbx.ImageWithDigest`.
- Produces:

```go
type Deps struct { instance.Deps; IsTTY func() bool } // instance.Deps carries Prompt and Git
type Options struct { Pull, Yes, Force bool }
func Build(ctx context.Context, d Deps, value string, o Options) error
```

The build writes its den-claude kit to `gen/.build/<name>/den-claude/spec.yaml` with `sbxenv.DenClaudeKitWithImage(image)`; the image is never empty there. Task 8a changes the instance side.

Sequence (refusals first, B16 throughout): `CheckSbx` → `Lookup(project from cwd or nil, global, value)`; an image → `"<value>" is an image; it has no build — use it as template: or default_template directly` → collision check against an existing record (refuse without `--force`, B18) → location: `dir` reads in place; `repo` uses the copy when it exists and `!Pull`, otherwise checks trust (project-declared and not `Trusted` → prompt `fetch template definitions from <url>?`, `--yes` skips it, no TTY and no `--yes` → refuse naming `--yes`; then `Trust`), clears and `Clone`s into staging → `LoadDefinitions` → definition of `Name()` (missing → error naming the file) → `from`: a sibling builds first, with the same location and the same `Options`, when its record or sbx tag is missing (announce `den: building template <sibling> first (template <name> starts from it)`), then the build VM uses the sibling's tag as `Template` and its `KitImage`, and the record inherits its `From`; an image uses itself; empty uses the global `default_template` if `Lookup` makes it an image, else refuse (B11) with `*template.NoTemplateError`-style text naming `from:` and the global file → announce `den: building template <name> (<tag>)` → build VM as today (kit in `gen/.build/<name>/den-claude/`, T3) → right after `env create`, read `sbx.Templates` and the digest of the `from` image (T1; empty → stop, name the reference) → steps, `stop`, `template save <tag>` → on success only: replace the copy with the staging dir, `WriteRecord` → on any failure: remove staging, keep copy, record and template.

- [ ] **Step 1: Write the failing tests** (sbx.Fake, real git on local repos, `prompt` fake):
  - `TestBuildFromADirWritesTheRecord`: record has `tag: legacy:v2`, `source.kind: dir`, `from.digest` = the Fake's digest, `definition_hash` = `DefinitionHash`.
  - `TestBuildClonesARepoIntoItsCopy`: copy at `gen/.templates/digitaleo/` holds HEAD of the source repo; `record.commit` equals it.
  - `TestBuildWithoutPullKeepsTheCopy` / `TestBuildWithPullAdvancesIt` (B14): a new commit in the source; without `--pull` the record keeps the old commit, with `--pull` it takes the new one.
  - `TestFailedBuildLeavesThePreviousVersion` (B16, Review Focus 3): step 1 fails under `--pull` → copy, record and sbx calls show no `template save`; staging dir gone; `env rm` called.
  - `TestBuildStopsWhenTheDigestIsUnreadable` (T1): Fake's `template ls` lacks the image → error naming the reference, no step ran, VM removed.
  - `TestBuildAsksBeforeAnUnknownProjectURL` (B17): project-declared repo, no TTY, no `--yes` → refusal naming `--yes`, no clone; with `--yes` → clone and `Trusted` true; a global-declared repo never asks.
  - `TestBuildRefusesAnotherSourceWithoutForce` (B18): then `--force` builds and the record names the new source.
  - `TestBuildBuildsTheSiblingFirst` (B12): `kampn.from: digitaleo`, nothing built → two `template save` calls, `digitaleo:v2` first; kampn's build env has `template: digitaleo:v2`; both records carry the same `from`.
  - `TestBuildWithoutFromNeedsAnImageDefault` (B11): global `default_template: digitaleo` (a template) → refusal; `default_template: claude-code-docker` → den-claude kit image `docker.io/docker/sandbox-templates:claude-code-docker`.
  - `TestBuildRefusesAnImage`: `den build claude-code-docker` → the "has no build" error.
- [ ] **Step 2:** Run `go test ./internal/build/ -count=1`. Expected: FAIL.
- [ ] **Step 3:** Implement; `cli/build.go`: `Use: "build <template>"`, flags `--pull`, `--yes`, `--force`; wrap a `NoTemplateError` with `hub.Describe` (shared helper `explainNoTemplate(ctx, deps, err) error` in `internal/cli/notemplate.go`).
- [ ] **Step 4:** Run `task check` (never `task build`: it replaces the user's `den`, T2). Expected: PASS.
- [ ] **Step 5: Commit** `feat(build): den build builds a template and records it`.

---

### Task 8a: nest and instance on templates (the switch)

**Files:**
- Modify: `internal/nest/resolve.go`, `internal/nest/nest_test.go`, `internal/instance/prepare.go`, `internal/instance/create.go`, their tests, `internal/instance/testdata/overlays.golden`, `internal/sbxenv/testdata/*.golden`
- Modify: `internal/config/schema.go`, `load.go`, `load_test.go` (delete the fields Task 2 kept: `Bases`, `Base`, `Agents`, `Agent`, `Nest.Base`, `agentImagePattern`; fix every fixture that wrote them)
- Modify: `internal/sbxenv/kits.go`, `internal/sbxenv/den-claude/spec.yaml`, `internal/build/build.go` (new kit call)

**Interfaces:**
- Consumes: `template.Choose`, `template.Open`.
- Produces: `nest.Resolved.Template *template.Resolved` (replaces `BaseName`, `Base`, `BaseFile`, `AgentImage`); `nest.FindBase` deleted. `Resolved.Kits = Def.Kits + nest kits + profile kits`; `Resolved.Allow = global + Def.Network.Allow + nest`.
- Produces: `sbxenv.DenClaudeKit(image string) ([]byte, error)` (`""` → error); it replaces `DenClaudeKit()` and `DenClaudeKitWithImage`. `instance.WriteDenClaudeKit(dir, image string) error` writes `<dir>/spec.yaml`.

- [ ] **Step 1: Write the failing tests:**
  - `TestLoadRefusesRemovedKeys` (B6, T10): a file with `bases:`, `agents:` and `nests.dev.base:` → one error naming the file, with the three lines `bases: removed — declare the template in a den-template.yaml and point at it with templates.<name>.dir or .repo`, `agents: removed — set default_template to an image, e.g. claude-code-docker`, `nests.dev.base: renamed — use nests.dev.template`.
  - `TestDenClaudeKitNeedsAnImage` (A9): `DenClaudeKit("")` errors; the embedded spec's `image:` line holds no `sha256:`.
  - `TestResolveTakesKitsAndAllowFromTheDefinition` (nest).
  - `TestPrepareOnAnImageSkipsTheTemplateCheck`: nest `template: claude-code-docker` → no `template ls` call in Fake; env has no `sandboxOptions`; the instance's `den-claude/spec.yaml` has `image: docker.io/docker/sandbox-templates:claude-code-docker`.
  - `TestPrepareRefusesAMissingSbxTemplate`: record present, Fake's `template ls` lacks `digitaleo:v2` → `den build digitaleo` remedy.
  - `TestPrepareRefusesACopyAtAnotherCommit` (§5.3): `Head(copy)` ≠ `record.commit` → `den build digitaleo` remedy.
  - `TestCreateWritesAKitPerInstance` (T3): two instances on two templates → two `den-claude/spec.yaml` with two images; `gen/.kits/` is not written.
  - `TestOverlaysCoverTheDefinition`: a `dir:` location under a mounted repo → overlays list `den-template.yaml`, the kit dir, includes and steps (golden `overlays.golden` updated by hand); fix strings say `of template "legacy" in <path>/den-template.yaml`.
  - `TestConfigHashMovesWithTheTemplateBuild` (Review Focus 5): same nest, record with another `definition_hash` or `from.digest` → another hash; `ensure` on Live prints the existing "changed since" warning and attaches.
- [ ] **Step 2:** Run `go test ./internal/config/ ./internal/sbxenv/ ./internal/nest/ ./internal/instance/ -count=1`. Expected: FAIL.
- [ ] **Step 3:** Implement. Move `removedKeys(raw []byte) []string` into `config.Load`, before `DecodeYAMLStrict`: it decodes leniently (`yaml.Node` for `bases`, `agents`, `nests.*.base`). `checkImage` becomes `checkTemplate(ctx, d, res)`: an image → nothing; a template → sbx tag present, and for a repo `Head(copy) == Record.Commit`. `envOf`: `Template = Choice.Tag()` for a template, `""` for an image; kit dir `<GenDir>/den-claude`. `configHash` drops `base`, `image`, `agent_image` and adds `template` (`Choice.Value`), `tag`, `kit_image`, `definition_hash`.
- [ ] **Step 4:** Run `task check` (never `task build`: it replaces the user's `den`, T2). Expected: PASS — the whole tree compiles again.
- [ ] **Step 5: Commit** `feat(instance): nests run on templates or official images`.

---

### Task 8b: `den up` notices

**Files:**
- Modify: `internal/instance/create.go`, `internal/instance/create_test.go`, `internal/cli/exec_test.go`

**Interfaces:**
- Consumes: `template.Notices` (Task 6).

- [ ] **Step 1: Write the failing tests:**
  - `TestUpPrintsNoticesAndExecDoesNot` (B23): Fake Git answers a newer remote → `den up` stderr has the notice; `den exec` stderr and stdout do not.
  - `TestUpNoticeRespectsItsDeadline` (Review Focus 1): a fake Git whose `ls-remote` blocks until its context ends; `instance.Up` with `detach` returns in under 4 s (`time.Since`) and stderr has no notice.
- [ ] **Step 2:** Run `go test ./internal/instance/ ./internal/cli/ -count=1`. Expected: FAIL.
- [ ] **Step 3:** Implement: `instance.Up` prints `template.Notices` to `d.Err` after `ensure`, before attach or detach; `Exec` does not.
- [ ] **Step 4:** Run `task check` (never `task build`: it replaces the user's `den`, T2). Expected: PASS.
- [ ] **Step 5: Commit** `feat(instance): den up tells when a template definition moved`.

---

### Task 8c: errors that name the key to fix

**Files:**
- Modify: `internal/cli/up.go`, `internal/cli/exec.go`, `internal/cli/up_test.go`, `internal/instance/create.go`, `internal/instance/create_test.go`

**Interfaces:**
- Consumes: `explainNoTemplate` (Task 7), `Choice.Origin`, `Choice.Searched` (Task 4).

- [ ] **Step 1: Write the failing tests:**
  - `TestUpExplainsAMissingTemplate` (B4, cli): no template anywhere, `Deps.Hub = hub.Fake{Names: …}` → error lists the short names; `exec` gives the same error.
  - `TestCreateOnAnImageNamesTheKeyOnFailure` (§5.1.4, §5.4): nest on `claude-cod` (typo), Fake fails `env create` → error contains `no template is named "claude-cod" in <files searched>, and sbx could not start the image docker.io/docker/sandbox-templates:claude-cod` and `fix ` + `Choice.Origin`. Same suffix when the attach fails right after create.
- [ ] **Step 2:** Run `go test ./internal/instance/ ./internal/cli/ -count=1`. Expected: FAIL.
- [ ] **Step 3:** Implement: `up` and `exec` wrap their error with `explainNoTemplate`; `Create` and `Attach` append the suffix only when `Choice.IsImage()`.
- [ ] **Step 4:** Run `task check` (never `task build`: it replaces the user's `den`, T2). Expected: PASS.
- [ ] **Step 5: Commit** `feat(cli): name the template key to fix`.

---

### Task 9: registry schema 2

**Files:**
- Modify: `internal/registry/registry.go`, `registry_test.go`, `testdata/record.golden`, `internal/instance/create.go` (`writeRecord`), `internal/instance/remove_test.go`

**Interfaces:**
- Produces: `registry.Schema = 2`; `Record.Template string` (`yaml:"template"`), `Record.Tag string` (`yaml:"tag,omitempty"`).

- [ ] **Step 1: Write the failing tests:**
  - `TestWriteStampsSchemaTwo` (golden gains `template:` and `tag:` after `nest:`).
  - `TestReadNamesTheRemedyForSchemaOne` (B7): a schema-1 file `am.yaml` → `<path>: schema 1 was written by a den before templates; this den cannot act on it — remove the VM with ` + "`sbx rm am`" + `, then delete <path>`.
  - `TestRemoveRefusesBesideASchemaOneRecord` (Review Focus 4): `den rm x` with a worktree and a schema-1 `am.yaml` → refusal containing that remedy; nothing removed.
- [ ] **Step 2:** Run `go test ./internal/registry/ ./internal/instance/ -count=1`. Expected: FAIL.
- [ ] **Step 3:** Implement; `writeRecord` fills `Template` and `Tag` from `res.Template.Choice`.
- [ ] **Step 4:** Run `task check` (never `task build`: it replaces the user's `den`, T2). Expected: PASS.
- [ ] **Step 5: Commit** `feat(registry): instance records name their template`.

---

### Task 10: docs

**Files:**
- Modify: `README.md`, `CLAUDE.md`, `examples/den-home/den.yaml`
- Create: `examples/den-template/den-template.yaml` (generic: one template with `from: claude-code-docker`, one `./kits/…`, one step; no personal value)

- [ ] **Step 1:** Rewrite every `base`/`bases:`/`agents:` passage: vocabulary *template*, *location*, *definition*, *official image*; `den build <template> [--pull] [--yes] [--force]`; the den home layout gains `gen/.templates/`, `state/v2/templates/`, `state/v2/template-sources.yaml` and loses `gen/.kits/`; `CLAUDE.md`'s `den-claude` paragraph says the image comes from the template record or the image reference, never from code; `Deps` gains `Hub`.
- [ ] **Step 2:** `grep -rn -i "bases:\|agents\.claude\|FindBase\|base image" --exclude-dir=.claude --exclude-dir=superpowers .` returns nothing outside history.
- [ ] **Step 3:** Run `task check` (never `task build`: it replaces the user's `den`, T2). Expected: PASS.
- [ ] **Step 4: Commit** `docs: templates replace bases`.

---

### Task 11: end-to-end check on throwaway sandboxes

**Prerequisite:** Tasks 1–10 committed. No `task build` (T2): build with `go build -ldflags "-X github.com/PillowPillow/den/internal/cli.Version=$(git describe --tags --always --dirty)" -o <scratchpad>/den ./cmd/den` and run it with `--den-home <scratchpad>/home`.

- [ ] **Step 1:** In the scratch den home, write a global `den.yaml` with `default_template: claude-code-docker` and a nest `den-probe-img` on one scratch repo. Run `<scratchpad>/den up den-probe-img --detach`. Expected: created, `sbx ls` shows it running; `sbx exec den-probe-img -- claude --version` answers.
- [ ] **Step 2:** Copy `examples/den-template/` to a scratch dir, declare `templates.den-probe-tpl.dir` there, run `<scratchpad>/den build den-probe-tpl`. Expected: `sbx template ls --json` lists `den-probe-tpl:v2`; the record's `from.digest` is `sha256:4a94d914…` (or the tag's current index digest; compare with `sbx template ls --json`).
- [ ] **Step 3:** Edit a step, run `den up` on a nest using it. Expected: the B21 notice on stderr.
- [ ] **Step 4:** `<scratchpad>/den rm` both instances; `sbx template rm den-probe-tpl:v2`; delete the scratch home. Expected: `sbx ls` shows only what was there before (`am`).
- [ ] **Step 5:** Report the results in prose. No commit.

---

### Task 12: Conversion (HITL)

**Prerequisites and warnings:**
- Step 0 is irreversible: `den rm am` deletes the VM `am` and its VM-local state (`/home/agent`, docker volumes in the VM). It runs only after the user approves it at that moment.
- Every write under `~/.den/` and in `~/Development/Digitaleo/den-env` (branch `den-v2`) runs only after the user approves it at that moment.
- Never copy values from `~/.den/den.yaml` or `den-env` (host names, allowlists) into this repo.

- [ ] **Step 0 (HITL, with the current den):** Run `sbx ls`. List every instance. For each one, ask approval, then run `den rm <instance>` with the den currently on PATH. On 2026-11-16 there is one: `am` (stopped). Expected: `sbx ls` lists no den instance; `~/.den/state/v2/instances/` is empty.
- [ ] **Step 1:** `task build`. Expected: `den version` names this branch's commit.
- [ ] **Step 2 (HITL):** Create `~/.den/templates/den-template.yaml` with template `pillow` from `bases.pillow` (no `image:`; kits `kits/pillow-env`, `kits/ssh-known-hosts`; steps `steps/<script>.sh`, relative to the file), move `~/.den/bases/pillow/*.sh` to `~/.den/templates/steps/` and `~/.den/kits/{pillow-env,ssh-known-hosts}` to `~/.den/templates/kits/` (B26). In `~/.den/den.yaml`: delete `bases:`, add `templates.pillow.dir: ~/.den/templates` and `default_template: pillow`; `nests.am` loses `base:`. `~/.den/kits/perso` stays (profiles). Expected: `den up am` refuses with `den build pillow`.
- [ ] **Step 3 (HITL: writes under `~/.den/`):** `den build pillow`. Expected: record `~/.den/state/v2/templates/pillow.yaml` with `from.digest`; then `den up am` attaches Claude in `am-backend`, and `den shell am` runs `docker info` with success.
- [ ] **Step 4 (HITL):** In `den-env` (branch `den-v2`): list every kit, include and step of `bases.digitaleo` that starts with `~` or `/` or leaves the repo through `..` (on 2026-11-16 all of them start with `./`); copy each one into `den-env` first, because B13 refuses them. Then create `den-template.yaml` with template `digitaleo` from `bases.digitaleo`, paths kept relative. In its `den.yaml`: delete `bases:` and every `nests.<n>.base:` line, add `templates.digitaleo.dir: .` and `default_template: digitaleo`, so the nests still there load until step 5 moves them. Expected: from `den-env`, `den up <a dg nest>` refuses with `den build digitaleo`, which proves both files load. Commit there only with approval; do not push.
- [ ] **Step 5 (HITL, open question):** Ask the user which Digitaleo nests move to the Digitaleo dev home's `den.yaml`, and under which names (B8, design §6.3). Then write that `den.yaml` with `templates.digitaleo.repo: <den-env URL>`, `ref: den-v2`, `default_template: digitaleo` and the chosen nests, and remove them from `den-env`'s `den.yaml`.
- [ ] **Step 6 (HITL: writes under `~/.den/`):** From the dev home: `den build digitaleo`. Expected: the B17 prompt names the URL; after yes, clone, build, record with `commit`. Then `den up <one dg nest> --detach` and `den exec <it> -- claude -p 'reply OK'` → `OK`.
- [ ] **Step 7:** Report in prose: what changed under `~/.den/` and in `den-env`, what the user decided at step 5. No commit in this repo.

---

## Fog of war

- Whether `sbx template save` over an existing tag leaves a VM created from the previous snapshot intact. Task 11 step 2 builds once, and Task 12 step 3 saves over the existing `pillow:v2`; if an instance on the old snapshot breaks, stop and report.
- Whether an official image other than `claude-code-docker` keeps the VM alive and carries Claude Code (validation B4, unverified). den cannot know before start; the `den up` error names the `template`/`default_template` key.
- Whether sbx re-pulls a tag that moved on the registry at `env create`. B22 leaves the check to a later lot; T1 records what the build actually used.
