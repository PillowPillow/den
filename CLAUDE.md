# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`den` is a Go CLI that drives `sbx` microVM sandboxes (V2, branch `v2`): it resolves a **nest**
from a `den.yaml`, generates the `sbxenv.yaml` files `sbx env create` reads, runs Claude Code in the
VM through a `den-claude` agent kit, and records what it created so `den rm` can undo exactly that.
Seven commands: `up`, `exec`, `shell`, `ls`, `rm`, `build`, `version`. `~/.den/` is the single
source of truth; `DEN_HOME` / `--den-home` redirects it, which is what makes the whole suite
hermetic. The sbx floor is **v0.53.0** (`sbx.MinVersion`), checked once by `up`, `exec` and `build`
before their first side effect.

## Commands

```bash
task check       # lint » typecheck » test, fail-fast — what CI runs, and what to run before a commit
task test        # go test -count=1 ./...   (-count=1 defeats the cache — plain `go test` can pass stale)
task typecheck   # go build ./...
task lint        # go vet ./... then test -z "$(gofmt -l .)"   — gofmt is enforced, not advisory
task build       # go build -ldflags "-X .../cli.Version=$(git describe --tags --always --dirty)" -o den ./cmd/den
                 # the ONLY documented way to build: a plain `go build` answers `den dev`

go test ./internal/instance/ -run TestCreatePassesNoInputForceAndSkipHostCommands -count=1   # single test
```

## Architecture

**Identity is the instance name** `<nest>[.<flattened branch>]`, which is also the sandbox name.
`sbx.SandboxName` / `sbx.SplitName` own the format; `-w` flattens a branch into a legal component
(`feature/123` becomes `api.feature-123`) while git keeps the branch as typed. The record under
`state/v2/` keeps the real branch, so nothing re-derives it from the name.

**Config**: `den.yaml` has two kinds, one strict schema (`internal/config`): the global file in the
den home (the only one allowed `worktrees`, `repos`, `env`, `network`, `profiles`) and a project file found by
walking up from the cwd; both may carry `templates` and `default_template` (a nest without `template:` takes its own file's
`default_template`, then the global one). Decoding is `KnownFields(true)` everywhere: an unknown key is a load error
naming the file, never a silence (a silent `network:` typo would empty an allowlist). `nest.Find`
locates the declaring file (project, then `state/v2/nests.yaml`, then global); `nest.Resolve`
merges global, template and nest into a `*nest.Resolved`. A nest name declared by two live files is
refused. A **template** is a nest's starting point: a value is a declared `templates.<key>` (project masks global; `repo`/`dir` locates a folder with a `den-template.yaml`, parsed by `config.LoadDefinitions`, paths confined to it) or an official image (a short name completes to `docker.io/docker/sandbox-templates:<name>`). No default exists in code: with none, den refuses and lists Docker Hub's images (`internal/hub`). The `bases` and `agents` keys and `nests.<n>.base` are refused with their replacement named. A **profile** is a named kit list (`profiles:`, global file only) that `nest.Resolve` appends to
`Kits` after the template's and the nest's; an undefined one is a warning, not a refusal.

**Layout on disk** (all under the den home, none of it read by v1, see the coexistence note below):
`den.yaml`, `run/<instance>/` (global nests; project nests use
`<project>/.den/run/<instance>/`), `gen/<instance>/` (egress kit, worktree `.git` files),
`gen/.templates/<name>/` (a repo template's copy; `.staging-<name>/` is a build's clone), `gen/.build/<name>/` (build files), `build/<name>/`
(the build VM's scratch workspace), `wt/` (default worktree root) and `state/v2/` (the registry).
The leading dot on `.templates` and `.build` is deliberate: no sandbox name can start with one, so
`den rm <name>` can never own them.

**Everything runs through `sbx env`.** `internal/sbxenv` renders `sbxenv.yaml` (pure, deterministic)
plus the two kits den generates: `den-claude` (embedded, `kind: sandbox`, image pinned to the
image from the template record (`<repository>@<digest>`) or from the image reference, never from code (the image's ENTRYPOINT must keep the VM alive: sbx runs it as PID 1, and `docker.io/sbx/claude`'s `claude` exits and stops the VM, S6e), no `credentials` block so the subscription OAuth token keeps
working) and a per-instance network mixin carrying the egress allowlist; each instance (`<gen dir>/den-claude/`) and each build (`gen/.build/<name>/den-claude/`) has its own den-claude kit, so a rebuild of one template never rewrites another instance's. `internal/instance`
(`Prepare` then `Create`/`Attach`/`Exec`/`Remove`) is the orchestration: `Prepare` makes every
refusal decidable from config, flags, registry and `sbx ls` *before the first side effect*;
`Create` writes worktrees, generated files and the record, then calls
`sbx --no-input env create --force --skip-host-commands`. Among the generated files is the instance's
repo map (`repoMapOf`, rendered by `sbxenv.RenderRepoMap*`), written under the den-claude kit's
`files/home/` so sbx copies it to `~/.claude/rules/den-instance.md` and `~/.den/repos.json` in the VM,
never under a mount; `envOf` exports `DEN_REPOS` on a copy of `Resolved.Env`, which feeds `configHash`. **No `env create` or `env rm` may lack
`--skip-host-commands`**: it keeps a `lifecycle:` command or a command-backed MCP server that a VM
could have written from ever running on the host. Likewise every file den or sbx reads for the
instance (its `den.yaml` files, local kits, layers, the template's `build.includes`/`build.steps`) that
lies under a read-write mount (lexical or symlink-resolved) is mounted read-only over it at the path
the VM sees (`Plan.Overlays`, appended after every other mount), since sbx acts on layers at every
create and rm and a kit carries the egress allowlist and credentials. One whose written path crosses
a symlink below the mount root is refused instead: sbx cannot overlay a symlink's path (S6c).
Accepted residual: the VM can still create a *new* file beside them, and the overlay holds only inside
the instance that reads the file: any other VM mounting the same folder read-write (another nest sharing
the repo, or a v1 VM) can still write it.
`den build <template> [--pull] [--yes] [--force]` (`internal/build`) clones (`gen/.templates/.staging-<name>/`, then `gen/.templates/<name>/`) or reads the location, provisions a VM, saves the sbx template `<name>:v2` and writes `state/v2/templates/<name>.yaml` (the root image digest is required); URLs the user trusted live in `state/v2/template-sources.yaml`. `den up` never clones or builds: a missing template refuses with the `den build` remedy, and `den up` alone prints stderr notices (newer remote commit, `dir` drift; 3 s bound, silent on error). Building a child builds its `from:` sibling first when the sibling is missing or stale (a `dir` sibling whose definition hash moved, a `repo` sibling built from another commit than the child's checkout); a sibling's files count in the child's definition hash, and the child's `--force` never reaches the sibling. Instance records are schema 2 (`template`, `tag`); a schema-1 record is refused with `sbx rm <instance>` then delete the file. `den up` attaches Claude (`sbx run --name <i> --workdir <start>`); `den shell` is the way to `bash -l`.

**What den created is recorded, not re-derived.** `internal/registry` writes
`<denHome>/state/v2/instances/<instance>.yaml` (schema-stamped, atomic, refused when its `instance`
differs from its file name) *before* `sbx env create`. `up` distinguishes Live (record and VM),
Recorded (record, no VM: recreate) and Foreign (VM, no record: refuse, never adopt). `den rm` acts
only on a readable record, removes only worktrees the record marks den-created, deletes only run
and gen dirs at the paths den places, and goes VM first, record last, so no orphan VM is left.
`state/` is never purged.

**Worktrees** (`internal/worktree`): `-w` makes one per repo, locked with `git worktree lock` when den
created it (one found in place stays unlocked), mounted at the main clone path with the common git
dir at `/den-git/<repo>` and a read-only generated `.git`.
There is no trash: `den rm` refuses (VM included) a worktree with modified, untracked or ignored
files, or a detached HEAD holding commits no ref contains, unless `--force`. A failed `Create` rolls
back the worktrees that call made, without force.

**Every system access is injected through `cli.Deps`** (`internal/cli/root.go`): one `sbx.Runner`
shared by all commands, plus `Git`, `Hub` (`hub.Catalog`, Docker Hub's official images), `Agent` (`sshagent.Probe`, runs `ssh-add -l`), `Prompt`, `IsTTY`, `Getwd`, `Out`, `Err`. Hard-wiring any of them
breaks the suite's hermeticity. Read helpers (`sbx.Ls`, `Templates`, `Version`, `PolicyRules`, `SSHForwarding`) keep sbx's plain
argv; `env create`, `env rm`, `rm`, `exec` and `ports` get `--no-input`; `den build`'s `stop` and
`template save` do not, nor do interactive attaches.
`den exec` requires `--` and sends its own messages to stderr, so stdout is the command's alone.

**Coexistence with v1** (until cutover): V2 touches only the paths above and `:v2` image tags; v1 owns
`config.yaml`, `stacks/`, `nests/`, `state/sandboxes/`, `worktrees/`, `trash/`, `sources/` and
`:v1` tags. V2 never reads or writes those.

## Test conventions

- No test calls `t.Parallel()`, opens a socket, or spawns a process other than git. Keep it that way.
- Packages running real git (`worktree`, `instance`) call `worktree.NeutralizeGitEnvironment()` in
  `TestMain`. Without it the suite has actually committed into unrelated repos via an inherited
  `GIT_DIR`/`GIT_WORK_TREE`. `internal/worktree/hermeticity_test.go` fails when a package that runs
  git lacks it.
- `sbx.Fake` lives in `internal/sbx/fake.go` — a production file on purpose, since `cli`, `instance`
  and `build` all need it. Don't move it into `_test.go`.
- Goldens live in `internal/*/testdata/*.golden` and are compared by hand; **there is no `-update`
  flag**, edit them manually.

## Conventions

- Code, comments and user-facing messages are **English**; the spec under `docs/superpowers/` is
  **French**.
- The dominant style is a long "why" comment at the decision site — what was rejected and what
  regression the choice prevents. Terse code visibly does not match; match the density around you.
- Errors name the file to fix and the remedy (`fix `repos:` in <path>`, "run `den rm <instance>`"). den
  refuses rather than normalizing in silence.

## Stale artifacts — don't trust these

- The V2 decisions are in `docs/superpowers/plans/2026-11-12-den-v2-0.md` (P1 to P11), the templates design `2026-11-16-den-templates-design.md`, plan `2026-11-16-den-templates.md` and validation `2026-11-16-den-templates-validation.md` and the
  scope in `docs/superpowers/specs/2026-11-12-den-v2-scope-design.md`; the sbx measurements are in
  `2026-11-12-den-v2-spikes.md`. Everything else under `docs/superpowers/` (the v1 spec
  `2026-07-27-den-cli-design.md` included) and everything under `.superpowers/sdd/` is historical
  and never rewritten: each document describes the state on its own date. Read them as v1.
- v1 vocabulary that no longer exists: `config.yaml`, stacks, `stack.yaml`, team sources
  (`den source`, `den lint`), `den spawn`, `den ports`, `den doctor`, `den update`, `den init`, the
  worktree trash, the SSH-agent key mount and the egress settle loop. A few v1 words survive in
  `internal/sbx` error strings (`den doctor`); there is no such command in V2.
- The v1 binary on a machine answers `den` too. `den version` names the code it runs; a plain
  `go build` answers `den dev`, so build with `task build`.
- sbx is installed (`/opt/homebrew/bin/sbx`). V2 requires **v0.53.0 or later** and reads only the
  surface of `sbx env`, `sbx ls --json`, `sbx template`, `sbx run`, `sbx exec`, `sbx ports`, and, for the
  alerts of `den up`, `sbx policy ls --json` and `sbx settings ls --json`. An
  sbx that is not signed in (`sbx login`) fails every `sbx ls` den makes.
- `.claude/worktrees/` may hold a full shadow copy of the tree. Exclude it from greps or every
  search returns doubled hits.
- There is no `Makefile`: the runner is `Taskfile.yml`. Dated plans under `docs/superpowers/` say
  `make lint && make test`; read it as `task check`.
