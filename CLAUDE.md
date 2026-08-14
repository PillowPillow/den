# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`den` is a Go CLI that drives `sbx` microVM sandboxes: one command spawns (or re-attaches to) a
multi-project VM without retyping stack, kits, egress policy and worktrees by hand. `~/.den/` is the
single source of truth; `DEN_HOME` / `--den-home` redirects it, which is what makes the whole suite
hermetic.

## Commands

```bash
task check       # lint » typecheck » test, fail-fast — what CI runs, and what to run before a commit
task test        # go test -count=1 ./...   (-count=1 defeats the cache — plain `go test` can pass stale)
task typecheck   # go build ./...
task lint        # go vet ./... then test -z "$(gofmt -l .)"   — gofmt is enforced, not advisory
task build       # go build -ldflags "-X .../cli.Version=$(git describe --tags --always --dirty)" -o den ./cmd/den
                 # the ONLY documented way to build: a plain `go build` answers `den dev`

go test ./internal/spawn/ -run TestSpawnAddsNoWorkspaceOutsideMountMode -count=1   # single test
```

## Architecture

**Identity is the sandbox name** `<nest>[.<instance>]`. Component 2 is the INSTANCE — the `--as`
label, or the flattened branch of `-w`, or empty — not necessarily a worktree branch
(`sbx.Sandbox.Instance()`'s own comment: "It is NOT 'the worktree'"). `sbx create` has no `--label`
(probed 2026-07-28), so that string is the only handle: `den ls`/`sh`/`rm`/`ports`, scoped policy,
the mixin cache and the worktree trash all key off it. `sbx.SandboxName` / `sbx.SplitName` own the
format; `-w` flattens a branch name into a legal component (`feature/123` → sandbox
`api.feature-123`) while git keeps the branch as typed.

**Config cascade**: global (`~/.den/config.yaml`) ← stack (`stacks/<n>/stack.yaml`) ← nest
(`nests/<n>.yaml`) ← flags, resolved by `nest.Resolve` into a `*nest.Resolved` that spawn consumes.
Decoding is **strict YAML everywhere** (`KnownFields(true)`): an unknown key is a load error, never a
silence. Spec §12 gives the reason — a silent `egres:` typo empties the allowlist, and the
fail-closed settle-loop then dies with no visible cause. Two readers escape the rule, and neither
loads config: `manifest.LaxMounts` reads `repos[].mount` alone out of a creation record the strict
decoder already refused (typically a newer den's, refused on `schema`), for its single caller
`newMountGuard` in `den rm` — there, reading nothing means guessing that no sibling mounts a
directory, and guessing wrong moves a live VM's workspace to the trash; `agent.ReadMixin` rereads
the mixin den itself generated under `cache/`.

**Every system access is injected through `cli.Deps`** (`internal/cli/root.go`), and `deps.Sbx` is
the *single* `sbx.Runner` shared by `ls`, `sh`, `ports` and spawn — structurally there is no second
runner to keep in sync. The other fields are injected because the real implementations touch the
machine: `Scanner` binds host sockets, `Open` spawns a browser, `SSHAgent` forks `ssh-add`, `IsTTY`
reads the terminal. Hard-wiring any of them breaks the suite's hermeticity.

**Spawn sequence** (`internal/spawn/spawn.go`, spec §6) is ordered on purpose: everything rejectable
from config alone (flag contradictions, sandbox name, missing repos / `ssh.dir` / kits) happens
*before the first side effect*, so a refusal never leaves an orphaned worktree; the fail-closed
settle-loop runs *before* attach, including under `--detach`. On the attach branch nothing is
reapplied to a live VM — den warns about mixin drift and missing git dirs instead of recreating.

**Ports publish on demand only.** `internal/spawn` must never import `internal/ports`, and
`internal/cli` must import none of `net`, `hash/fnv`, `os/exec` — both invariants are locked by
`internal/ports/hermeticity_test.go`, which fails with an import-graph message if you break them.

**What den mounted is recorded, not re-derived.** `internal/manifest` writes
`<denHome>/state/sandboxes/<sandbox>.yaml` on the create branch, before `sbx create`; `den rm`,
`den ls` and `den doctor` replay it. Every reader falls back on the old derivation when the file is
absent or unreadable — `den rm` must never refuse and strand a live VM (doctrine T13/T16), and den
never deletes a record it could not read (it may belong to a newer den). `state/` is not `cache/`:
it is never purged.

**Team sources** live under `sources/<n>/` (`internal/source`) — plain git clones carrying the
den-home partial layout (`stacks/`, `lib/`, `kits/`, `nests/`, no `config.yaml`: personal config
never travels through a source). References are `<source>:<name>` on the personal side (`corp:backend`,
CLI/`defaults.stack`/local nests only); **inside** a source, `stack:`/`parent:` are bare and resolve
in that source alone — a prefixed reference there is a lint refusal, because the install name is
chosen per machine and the source's own CI knows none. `internal/lint` is the single checkout
validator (confinement, DAG, bare references, spec 2026-08-04 §5) shared by `den lint`, `den source
add` (post-clone, refuses **and deletes** the clone) and `den source update` (pre-fast-forward,
fail-closed) — one judge, so lint can never accept what a spawn would later refuse.

## Test conventions

- No test calls `t.Parallel()`, opens a socket, or spawns a process. Keep it that way — hermeticity
  is the reason the untestable one-liners (`ports.ListenScanner`, `ports.OpenURL`,
  `spawn.LooksInteractive`) are isolated behind interfaces.
- `spawn.LooksInteractive` is a HALF exception since #66: it delegates to `spawn.isTerminal`, whose
  negative verdicts are tested against real descriptors (`internal/spawn/isterminal_test.go` —
  `/dev/null`, a regular file, a closed fd). Only "a real terminal answers true" stays untested,
  and it stays that way on purpose. The test file is tagged `darwin || linux`: the
  `!darwin && !linux` build keeps the pre-#66 `os.ModeCharDevice` heuristic, under which
  `/dev/null` answers true.
- Packages running real git (`cli`, `spawn`, `worktree`) call `worktree.NeutralizeGitEnvironment()`
  in `TestMain`. Without it the suite has actually committed into unrelated repos via an inherited
  `GIT_DIR`/`GIT_WORK_TREE`.
- `sbx.Fake` lives in `internal/sbx/fake.go` — a production file on purpose, since `policy`, `cli`
  and `agent` all need it. Don't move it into `_test.go`.
- Goldens live in `internal/*/testdata/*.golden` and are compared by hand; **there is no `-update`
  flag**, edit them manually.

## Conventions

- Code, comments and user-facing messages are **English**; the spec and handoffs under
  `docs/superpowers/` are **French**.
- The dominant style is a long "why" comment at the decision site — what was rejected and what
  regression the choice prevents. Terse code visibly does not match; match the density around you.
- Errors name the file to fix and the remedy (`fix `repos:` in <path>`, "run `den doctor`"). den
  refuses rather than normalizing in silence (spec §2).

## Stale artifacts — don't trust these

- `README.md` no longer lags on `den ports` nor on `den build` (both notes were themselves stale —
  the rows and the sections are there). `den build` shipped with #8.
- `README.md`'s `den build` section was rewritten on 2026-08-03 for the model den actually ships:
  den owns `create` → N × `exec` → `stop` → `template save` → `rm`, the per-stack `build.sh` is
  gone, and a stack declares `provision.includes` / `provision.steps` instead. README and spec §6
  now agree; the earlier note here said they deliberately did not, and telling you to prefer the
  spec over the README is no longer right. The spec remains the source of truth on **intent**, but
  a divergence is now a bug in one of them, not a phase.
- `docs/superpowers/handoffs/HANDOFF.md` was itself the liar this section warned about; it was
  rewritten on 2026-08-03 and is current as of the `v1.0.0` tag. The **dated** handoffs beside it
  (`2026-07-*`, `2026-08-*`) are historical and never rewritten — each describes the state on its
  own date. So is everything under `docs/superpowers/plans/` and `.superpowers/sdd/`, where several
  reports still say `sbx` is not installed on this machine. It is (`/opt/homebrew/bin/sbx`,
  v0.35.0, three real smokes). The spec `docs/superpowers/specs/2026-07-27-den-cli-design.md`
  remains the source of truth, and its §14.0/§14.1 the only place that says what a real `sbx` has
  actually answered.
- `.claude/worktrees/feat+spawn-interactive/` is a full shadow copy of the tree. Exclude it from
  greps or every search returns doubled hits.
- Il n'y a plus de `Makefile` : le runner est `Taskfile.yml` depuis le 2026-08-04. Les plans
  datés et les handoffs sous `docs/superpowers/` disent encore `make lint && make test` — c'est
  correct **à leur date** et ils ne sont pas réécrits. Traduire en `task check` en les lisant.
- Les plans et handoffs datés sous `docs/superpowers/` disent `den <nest>` pour spawner. C'était
  vrai à leur date : la forme nue a été remplacée par `den spawn <nest>` le 2026-08-05 (spec
  `2026-08-05-spawn-command-design.md`). Traduire en lisant, comme pour `make` → `task`.
