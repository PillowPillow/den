# Plan — fix the PR #97 review findings

Date: 2026-09-17. Branch: `feat/sbx-0.43-compat`. Spec: `docs/superpowers/specs/2026-07-27-den-cli-design.md`
(§2, §6, §12) and `docs/superpowers/specs/2026-09-16-sbx-0.43-compat.md` (§1.1, §1.4, §5).

Four tasks from the `/code-review high 97` pass. Task 1 is the only load-bearing one.

## Global Constraints

- Code, comments and user-facing messages are **English**. The spec stays French.
- `task check` (lint » typecheck » test) must pass at the end of every task. `gofmt` is enforced.
- Comment doctrine: a long "why" comment at the decision site, naming what was rejected and the
  regression the choice prevents. Match the density of the surrounding file. No comment narrates
  the diff, the review, or this plan — a reader who never saw the change must read it correctly.
- No test calls `t.Parallel()`, opens a socket, or spawns a process.
- Goldens under `internal/*/testdata/*.golden` are edited by hand; there is no `-update` flag.
- Doctrine T13/T16: `den rm` must never refuse and strand a live VM.
- An absent key is never an empty state (spec §12, and `decodeSecretList`'s own comment).
- Naming idiom: validators are `Validate` + the noun (`ValidateSandboxName`,
  `ValidateSandboxComponent`, `ValidateSourceName`). Keep it.
- Each task commits on `feat/sbx-0.43-compat` with a conventional-commit subject. One task, one
  commit, unless the task text says otherwise.

## Task 1 — separate "a name den built" from "a name sbx will create"

### The defect

`internal/sbx/name.go` applies four WHOLE-NAME rules inside `SandboxName`: `MinNameLength`,
`MaxNameLength`, the trailing-alphanumeric rule and `ReservedName`. `ValidateSandboxName` is
implemented as a round-trip through `SandboxName`, so those four rules reach every caller of the
validator — including the readers and the destroyer:

- `internal/cli/rm.go:134` (`cleanWorktrees`) RETURNS the error before `sbx rm --force` runs.
- `internal/manifest/manifest.go:137`, `internal/agent/mixin.go:255`, `internal/agent/drift.go:77`,
  `internal/policy/settle.go:144` — all read paths or argv for sandboxes that already exist.

Two of the four rules are reachable on sandboxes den itself created before this branch: `den up api
-w foo-` (a legal git branch flattening to the component `foo-`, sandbox `api.foo-`), and any
nest+branch over 63 bytes. `den rm api.foo-` then exits 1 with `cleaning up worktrees: sandbox name
"api.foo-": ends with "-"…` while the VM stays alive — the exact outcome `cleanWorktrees`' own doc
comment forbids. `den rm --keep-worktrees api.foo-` succeeds on the same name, because `rm.go:94`
swallows the same error inside `if path, err := manifest.Path(home, name); err == nil`.

`internal/spawn/spawn.go:557` calls `SandboxName` BEFORE the create-or-attach verdict, so re-attach
to an existing `api.foo-` fails too.

### The shape

Two questions, two functions, in `internal/sbx/name.go`:

1. **Structural** — is this a name den could have assembled from valid components, in canonical
   form? Owned by `SandboxName` (constructor) and `ValidateSandboxName` (predicate). It must stay
   tolerant of names an older den built and a current sbx would now refuse: every caller of the
   predicate operates on a sandbox that ALREADY exists, and refusing there strands it.
2. **Creatable** — will `sbx create` accept this name? A new exported
   `ValidateCreatableSandboxName(name string) error` carrying the four whole-name rules, called
   only where den creates.

Changes:

- Move the four whole-name refusals out of `SandboxName` into `ValidateCreatableSandboxName`,
  VERBATIM — the error strings, the remedies and their "why" comments are measured against a real
  sbx and must not be reworded or reordered.
- `ValidateCreatableSandboxName` indexes the last byte (`rune(name[len(name)-1])`). Document that
  it is only ever called on a name whose components passed `config.ValidateSandboxComponent`
  (ASCII), which is what makes the byte index exact — and have it call `ValidateSandboxName` first
  so that precondition is enforced, not assumed.
- `SandboxName` keeps component validation and assembly, and returns no whole-name refusal.
- `ValidateSandboxName` keeps its round-trip through `SandboxName` and its canonical comparison.
  Rewrite its doc comment: it answers the structural question ONLY, deliberately, because `den rm`,
  `manifest.Path`, the mixin and drift readers and `policy.Settle` all run against live sandboxes,
  and a name an older den built must stay addressable.
- `internal/sbx/argv.go:42` (`CreateArgv`): call `ValidateCreatableSandboxName` in place of
  `ValidateSandboxName`.
- `internal/build/sandbox.go:53` (`build.CreateArgv`): same substitution.
- `internal/spawn/spawn.go`: leave line 557 on `SandboxName`. Add
  `ValidateCreatableSandboxName(sandboxName)` on the CREATE branch only — immediately after
  `live := sbx.Find(boxes, sandboxName)`, guarded by `live == nil`.

### Ordering, and the test that encodes the old order

That placement is downstream of `sbx.Ls` and upstream of `worktree.Ensure`. The #96 regression is
an ORPHANED WORKTREE, and the new position still prevents it: nothing has been created when the
refusal fires. What it gives up is narrower — on a machine whose sbx is broken, `den up api -w
<64-byte-branch>` now reports sbx's `ls` error instead of the name error. That is a read, it
creates nothing, and it is the price of leaving the attach branch reachable.

`TestSpawnRefusesAWorktreeThatMakesTheSandboxNameTooLong`
(`internal/spawn/spawn_test.go:1891`) asserts `len(f.Calls) != 0` — "no sbx call should have
happened". That assertion is stricter than #96 requires and must be amended to what the issue
actually protects: no worktree on disk, and no `create` in `f.Calls`. Amend the test body and its
comment; do not delete the test.

### Tests

- `internal/sbx/name_test.go`: `ValidateSandboxName` ACCEPTS `api.foo-`, a 64-byte name, `a` and
  `default`; `ValidateCreatableSandboxName` refuses each of the four with the existing messages,
  and still refuses the structural failures (`api.`, an empty component, an illegal charset).
  Keep `TestValidateSandboxNameAcceptsTheImageOfSandboxName` as it stands.
- `internal/sbx/argv_test.go`: `CreateArgv` refuses exactly the union — the four whole-name rules
  AND the structural failures it refused before. Every case that passed before must still pass.
- `internal/build/sandbox_test.go`: `build.CreateArgv` still refuses a stack whose build sandbox
  name is not creatable.
- `internal/spawn/spawn_test.go`: the amended test above still refuses a too-long name with no
  worktree and no `create`; a NEW test asserts that a spawn whose computed name matches a LIVE
  sandbox that is not creatable (`api.foo-`) attaches instead of refusing.

## Task 2 — `den rm` degrades an unvalidatable name to a warning

Separate commit from Task 1: this closes a hole that predates the branch (`den rm` on a sandbox
created OUTSIDE den, whose name den's validator refuses, strands it today), so it must be
droppable without unpicking Task 1.

In `internal/cli/rm.go`, `cleanWorktrees`' `ValidateSandboxName` failure becomes a WARNING on
`warnW` and returns nil, so `sbx rm --force` always runs. The message names what was skipped
(worktree reclamation), why (den cannot turn this name into a host path), and the remedy. Extend
the function's doc comment: its "Never a refusal" line now holds for the name check too.

**Do not touch the dirty-worktree refusal.** That one is deliberate (`rm.go:85`: "if one is dirty
we stop BEFORE destroying the sandbox") and stays a refusal.

### Tests

`internal/cli/rm_test.go`: `den rm` on a name `ValidateSandboxName` refuses warns on stderr, runs
`sbx rm --force`, and exits 0. The existing dirty-worktree refusal test must stay green.

## Task 3 — `den doctor` must not FAIL on a source-built sbx

### The defect

`internal/cli/doctor.go:240`. `semver.IsValid("v0.43.0-dev")` is true, so the WARN branch at
`doctor.go:233` is skipped; `semver.Compare("v0.43.0-dev", "v0.43.0") < 0` sends it to FAIL, and
`failures > 0` makes `den doctor` return an error at `doctor.go:125`. Only a bare `dev` reaches the
intended WARN. The comment at `doctor.go:224` already describes this and parks it.

### The shape

Add the shared normalizer the parked comment names, in `internal/sbx/version.go`:

```go
func ReleaseVersion(observed string) (string, bool)
```

Same contract as `internal/source/manifest.go`'s `releaseVersion` — trim, prepend a missing `v`,
`ok=false` when `semver.IsValid` says no, then cut BUILD METADATA first and the prerelease second
(that order is what the SemVer grammar requires: a `-` may appear inside build metadata, never
before it). Document why the prerelease is dropped: `go build` of sbx from source answers a
`vX.Y.Z-…` prerelease that `semver.Compare` ranks below the release, and refusing there makes
`den doctor` red on a machine whose sbx is at or past the floor.

`sbxVersionCheck` then calls `sbx.ReleaseVersion(v)`: `ok=false` → the existing WARN, unchanged;
`ok=true` → compare the NORMALIZED version against `sbx.MinVersion`. Both the FAIL detail and the
OK detail keep naming the version the user typed (`v`), not the normalized one. Delete the parked
paragraph from the doc comment — it describes a defect that no longer exists.

**Own this side effect in the commit message:** the WARN branch keys on `!semver.IsValid(v)`, and
`ReleaseVersion` prepends a missing `v`, so a bare `0.43.0` moves from WARN to OK. Inert on a real
machine — `sbx version` prints the `v` (measured 2026-09-17: `sbx version: v0.43.0 79805a6`) — but
it is a behavior change.

Leave `internal/source/manifest.go` alone. Deduplicating the two is a separate change, and this
branch is a compat branch.

### Tests

`internal/cli/doctor_test.go`, one pinned verdict per input:

| `sbx version` output | verdict |
|---|---|
| `v0.43.0-dev` | OK — `den doctor` exits 0 |
| `v0.43.0-3-gabc1234` | OK |
| `v0.44.0` | OK |
| `v0.42.0` | FAIL |
| `v0.42.0-3-gabc1234` | FAIL |
| `dev` | WARN |

`internal/sbx/version_test.go`: table over `ReleaseVersion` — `"v0.43.0-dev"` → `v0.43.0,true`;
`"0.43.0"` → `v0.43.0,true`; `"v1.7.0-3-gabc+meta-x"` → `v1.7.0,true`; `"v1.7.0+meta"` →
`v1.7.0,true`; `"dev"` → `"",false`; `""` → `"",false`.

## Task 4 — `custom_secrets` gets the same absent-vs-empty guard as `secrets`

### The defect

`internal/converge/sbx.go:121`. `Secrets` is a `*[]struct` so an absent key can be told from an
empty list; `CustomSecrets` is a plain slice. If sbx renames or drops `custom_secrets`,
`decodeSecretList` returns an empty `Customs` map with no error, `CredentialPresent` answers false
for every custom credential, and converge re-applies — re-prompting for and overwriting tokens
already set. The text parser this replaced was fail-closed here: a `SCOPE TARGETS ENV` header
outside the `CUSTOM SECRETS` section hit the `default:` branch and errored.

### The shape

Make `CustomSecrets` a `*[]struct{…}` and refuse `nil` next to the existing `Secrets == nil`
refusal, with a message of the same shape naming `custom_secrets`. Fold the two into one check
rather than writing the same paragraph twice. Extend the struct's `Secrets is a POINTER` comment to
cover both keys, with the consequence spelled for custom secrets: den re-prompts for and overwrites
credentials the machine already holds.

The pointer must not fire on a healthy machine with zero custom secrets. Evidence, measured
2026-09-17 on sbx v0.43.0 `79805a6`:

```
$ sbx secret ls -g --json | jq 'to_entries | map({(.key): (.value|type)}) | add'
{ "secrets": "array", "custom_secrets": "array", "shadowed_services": "array", "env_only_count": "number" }
$ sbx secret ls -g --json | jq '{shadowed_services_len: (.shadowed_services|length)}'
{ "shadowed_services_len": 0 }
```

`shadowed_services` is empty on this machine and still serializes as `[]`, never omitted and never
`null` — sbx initializes its slices and emits them unconditionally. `custom_secrets` is the same
kind of field in the same document. The claim is therefore INDIRECT for the zero-custom-secrets
case, which this machine cannot exhibit (it holds one). Record that limit in the doc comment.

### Tests

`internal/converge/sbx_test.go`: a document with `secrets` but no `custom_secrets` is an ERROR
naming the key; `"custom_secrets": []` with `"secrets": []` decodes to an empty state with no
error; `"custom_secrets": null` is an ERROR too. Keep every existing case green.
`internal/converge/testdata/secret-ls.json` must still carry both keys.
