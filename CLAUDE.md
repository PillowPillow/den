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
the *single* `sbx.Runner` shared by `ls`, `shell`, `ports`, `up` and `run` — structurally there is no second
runner to keep in sync. The other fields are injected because the real implementations touch the
machine: `Scanner` binds host sockets, `Open` spawns a browser, `SSHAgent` forks `ssh-add`, `IsTTY`
reads the terminal. Hard-wiring any of them breaks the suite's hermeticity.

**Spawn sequence** (`internal/spawn/spawn.go`, spec §6) is ordered on purpose: everything rejectable
from config alone (flag contradictions, sandbox name, missing repos / `ssh.dir` / kits) happens
*before the first side effect*, so a refusal never leaves an orphaned worktree; the fail-closed
settle-loop runs *before* attach, including under `--detach`. On the attach branch nothing is
reapplied to a live VM — den warns about mixin drift and missing git dirs instead of recreating.
Size is a third drift den warns about and never fixes: nothing resizes a running VM, and
`sbx ls --json` carries no size field, so `spawn.reportResourceDrift` compares the **creation
record** — by BYTES, not by spelling, or `8g` vs `8192m` would warn forever about a VM that is
right.

**`resources:` is a cascade level, not a flag passthrough** (shipped with #90). `resources: {cpus,
memory}` merges **field by field** across `config.yaml` ← `stack.yaml` ← `nests/<n>.yaml` ←
flags — a stack pinning a memory floor and a nest asking for more CPUs are two independent
statements. `cpus:` is a **pointer** on purpose: `--cpus 0` is sbx's documented *auto*, so "written
zero" must stay distinguishable from "absent". `internal/sbx/resources.go` re-implements docker's
`go-units` grammar rather than taking the dependency, because sbx refuses a too-small `--memory`
**server-side, after the image pull** — den refuses it in `nest.Resolve` instead, before the first
side effect. Keep that parser **no narrower and no wider than sbx's**: narrower refuses `2gb` /
`4G` / `2048MiB`, which work; wider lets `1bb` through for the server to refuse after the pull.
`den nest show` prints the resolved block, which is the only way to see which cascade level won.

**den compiles a nest into a `.sbxenv.yaml`; it no longer assembles an `sbx create` argv.** Spec
`docs/superpowers/specs/2026-08-24-sbx-env-positioning-design.md` (#89) — implemented, plan
`docs/superpowers/plans/2026-08-25-sbxenv-emitter.md` — settled it on maintenance surface, not
risk: an argv is not a contract, a `schemaVersion` behind a strict decoder is. `internal/sbx/env.go`
is the emitter: `EnvFile(Env) ([]byte, error)`, and `EnvSchemaVersion = "1"` is a **string** — sbx
answers `unsupported schemaVersion "2" (supported: 1)`, and an unquoted `1` round-trips as a scalar
sbx refuses. `internal/sbx/argv.go` is **deleted**. The emitted file lives at
`<denHome>/state/sandboxes/<sandbox>/.sbxenv.yaml`, mode 0600, beside den's own `manifest.yaml` in
the same directory (see below). den creates with `sbx env create <path>` and destroys with `sbx env
rm -f <path>` — `sbx env rm` does not delete the file it is handed, so den removes it itself after a
successful removal. `sbx.CheckEnvFile` refuses to hand `sbx env rm` a record it cannot vouch for —
absent (the sandbox predates the emitter), unreadable, or naming a different sandbox — and `den rm`
then refuses too, naming the escape hatch: `den rm --force <sandbox>`, which falls back to
destroying by name through `sbx rm --force` and names the commands to list and remove the secrets
scoped to that sandbox, which are left in place. The
`resources:` cascade above survives verbatim; it now emits into `sandboxOptions.cpus` /
`sandboxOptions.memory` instead of `--cpus` / `--memory` flags. `internal/build` still drives `sbx
create` — a build sandbox is a different object with its own lifecycle, a named non-objective, not
a leftover.

**Ports publish on demand only.** `internal/spawn` must never import `internal/ports`, and
`internal/cli` must import none of `net`, `hash/fnv`, `os/exec` — both invariants are locked by
`internal/ports/hermeticity_test.go`, which fails with an import-graph message if you break them.

**What den mounted is recorded, not re-derived.** `internal/manifest` writes
`<denHome>/state/sandboxes/<sandbox>/manifest.yaml` on the create branch, before `sbx env create`;
`den rm`, `den ls` and `den doctor` replay it. The directory (not a single file) is deliberate: it
is where `manifest.yaml` and the `.sbxenv.yaml` sbx consumes live side by side, so `den rm` can
remove both as one unit. `internal/manifest.LegacyPath` — the old flat
`<denHome>/state/sandboxes/<sandbox>.yaml` a pre-directory-layout den left — is still read,
**permanently**, not as a migration phase: `state/` is never purged, so every sandbox created before
this change keeps its record only there, and den converts nothing. Every reader falls back on the
old derivation when neither file is readable — `den rm` must never refuse and strand a live VM
(doctrine T13/T16), and den never deletes a record it could not read (it may belong to a newer
den). `state/` is not `cache/`: it is never purged.

**`~/.den` is the single source of truth, and `den doctor` now says where it is NOT** (#88). sbx
writes machine state den does not own — the global secret store, the MCP registry, the agent-skills
store — and `internal/doctor/undeclared.go` reports each as "present, undeclared" at **LevelOK**,
never a warning: a user who ran `sbx setup` has a correct machine, and a permanent warning stops
being read. den removes nothing it did not create; there is no `--fix` behind this check. A fourth
sbx surface must be a **row in `sbx.Stores`**, not a fourth parser. Two grades of observation, kept
apart on purpose: a surface den can enumerate is reported by identity, one it can only probe
carries a boolean. `sbx.ReadStore` matches the **negative sentinel** (`No MCP servers registered`,
`No skills found`) and treats everything else as occupied — neither command accepts `--json`, no
populated output has ever been observed, so a column parser would be a supposition. An emptiness
test would be wrong too: `sbx mcp ls` prints its gateway header with nothing registered.

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
  negative verdicts are tested against real files (`internal/spawn/isterminal_test.go` —
  `/dev/null`, a regular file, a closed file). Only "a real terminal answers true" stays untested,
  and it stays that way on purpose. The test file is tagged `darwin || linux`: the
  `!darwin && !linux` build keeps the pre-#66 `os.ModeCharDevice` heuristic, under which
  `/dev/null` answers true.
- `isTerminal` takes an `*os.File`, never a bare descriptor, and that signature is load-bearing —
  the fallback needs a `Stat`, and a `Stat` from a descriptor means `os.NewFile`, whose finalizer
  closes den's own stdin and stdout at the next GC (reproduced; `isterminal_other.go` carries the
  story). That file is the one no gate TESTS — `isterminal_test.go` is tagged `darwin || linux`
  and `task typecheck` only compiles it under `GOOS=windows`. Read its body; nothing will fail
  for you.
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
  **v0.39.0 `def8cb0`** as of 2026-08-24 — the v0.35.0 this note used to record is three releases
  behind). The spec `docs/superpowers/specs/2026-07-27-den-cli-design.md` remains the source of
  truth, and its **§14.0 → §14.5** the only place that says what a real `sbx` has actually
  answered. Three of those subsections were written the same day (2026-08-24) by three parallel
  axes that each claimed the number `14.3`; the collision is arbitrated **by issue number**, and
  the numbers below are the ones to cite: **§14.3** = `sbx mcp ls` / `sbx skills ls` (#88, neither
  accepts `--json`), **§14.4** = the real `.sbxenv.yaml` schema (#89), **§14.5** = the `--memory`
  grammar and the `--profile` probe (#90). Doc comments in `internal/sbx/store.go`,
  `store_test.go`, `machine.go` and `internal/doctor/undeclared_test.go` cite §14.3 and are
  correct. `docs/superpowers/handoffs/2026-08-24-axe3-PR-body.md` says §14.3 for the `--memory`
  grammar and means §14.5 — it carries a header note saying so, and its body is not rewritten.
  §14.2 remains the v0.39.0 surface, and it settles probes 1 and 2
  of issue #87. The `sbx setup` wizard is one-shot per machine (marker
  `~/Library/Application Support/com.docker.sandboxes/sandboxes/first-run-import.json`, keyed on
  `offeredAt` — *offered*, not accepted, so `[q]` closes it). Its gate is
  `isTerminal(stdin) && isTerminal(stdout)` — **the descriptors, not `-it`**. So `den build`, the
  §9.1 gate and the ports probe are safe (they capture stdout into a buffer), and so is CI, but
  **`den exec -T` / `den run -T` typed at a terminal HANG** on a machine that has never been
  prompted: den sends no `-it`, yet `spawn.Enter`'s Pipe branch passes the terminal's own
  descriptors through. Measured 2026-08-24, §14.2 carries the table. Do not repeat the first
  reading of that probe, which used `stdin=/dev/null` throughout and wrongly concluded the wizard
  could never reach den.
- `.claude/worktrees/feat+spawn-interactive/` is a full shadow copy of the tree. Exclude it from
  greps or every search returns doubled hits.
- Il n'y a plus de `Makefile` : le runner est `Taskfile.yml` depuis le 2026-08-04. Les plans
  datés et les handoffs sous `docs/superpowers/` disent encore `make lint && make test` — c'est
  correct **à leur date** et ils ne sont pas réécrits. Traduire en `task check` en les lisant.
- Les plans et handoffs datés sous `docs/superpowers/` disent `den <nest>` pour spawner. C'était
  vrai à leur date : la forme nue a été remplacée par `den spawn <nest>` le 2026-08-05 (spec
  `2026-08-05-spawn-command-design.md`), puis `den spawn <nest>` a lui-même été remplacé par
  `den up <nest>` / `den run <nest> <cmd>` le 2026-08-16, sans alias (spec
  `2026-08-16-up-run-command-design.md`). Traduire en lisant, comme pour `make` → `task`.
