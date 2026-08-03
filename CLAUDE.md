# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`den` is a Go CLI that drives `sbx` microVM sandboxes: one command spawns (or re-attaches to) a
multi-project VM without retyping stack, kits, egress policy and worktrees by hand. `~/.den/` is the
single source of truth; `DEN_HOME` / `--den-home` redirects it, which is what makes the whole suite
hermetic.

## Commands

```bash
make test        # go test -count=1 ./...   (-count=1 defeats the cache — plain `go test` can pass stale)
make typecheck   # go build ./...
make lint        # go vet ./... && test -z "$(gofmt -l .)"   — gofmt is enforced, not advisory
go build -o den ./cmd/den
go build -ldflags "-X github.com/PillowPillow/den/internal/cli.Version=v0.1.0" -o den ./cmd/den

go test ./internal/spawn/ -run TestSpawnAddsNoWorkspaceOutsideMountMode -count=1   # single test
```

## Architecture

**Identity is the sandbox name** `<nest>[.<worktree>]`. `sbx create` has no `--label` (probed
2026-07-28), so that string is the only handle: `den ls`/`sh`/`rm`/`ports`, scoped policy, the mixin
cache and the worktree trash all key off it. `sbx.SandboxName` / `sbx.SplitName` own the format;
`-w` flattens a branch name into a legal component (`feature/123` → sandbox `api.feature-123`) while
git keeps the branch as typed.

**Config cascade**: global (`~/.den/config.yaml`) ← stack (`stacks/<n>/stack.yaml`) ← nest
(`nests/<n>.yaml`) ← flags, resolved by `nest.Resolve` into a `*nest.Resolved` that spawn consumes.
Decoding is **strict YAML everywhere** (`KnownFields(true)`): an unknown key is a load error, never a
silence. Spec §12 gives the reason — a silent `egres:` typo empties the allowlist, and the
fail-closed settle-loop then dies with no visible cause.

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

## Test conventions

- No test calls `t.Parallel()`, opens a socket, or spawns a process. Keep it that way — hermeticity
  is the reason the untestable one-liners (`ports.ListenScanner`, `ports.OpenURL`,
  `spawn.StdinIsTerminal`) are isolated behind interfaces.
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
- `README.md`'s `den build` section describes the **shipped** #8 model — a per-stack `build.sh` run
  on the host. The spec was amended on 2026-08-03: den now owns `create` → `exec` → `save`, the
  `build.sh` is gone and a stack declares `provision:` instead. Until that lands, README = what
  ships, spec = where it is going. Do not "fix" one against the other; the spec wins on intent.
- `docs/superpowers/handoffs/HANDOFF.md` says Plan 2 is unexecuted and nothing is pushed. Both are
  false (PRs #12–#14 merged). Handoffs and plans are historical; the spec
  `docs/superpowers/specs/2026-07-27-den-cli-design.md` is the source of truth.
- `.claude/worktrees/feat+spawn-interactive/` is a full shadow copy of the tree. Exclude it from
  greps or every search returns doubled hits.
