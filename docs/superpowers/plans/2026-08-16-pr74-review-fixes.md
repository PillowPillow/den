# PR #74 Review Fixes — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the correctness defects found by two independent code reviews of PR #74
(`feature/source-onboarding`) — one Claude pass, one Codex cross-review — without widening the
delivery's contract. Every task lands on the PR branch.

**Spec:** `docs/superpowers/specs/2026-08-14-source-onboarding-design.md`.
**Origin plan:** `docs/superpowers/plans/2026-08-14-source-onboarding.md`.

**Architecture:** No new package. The fixes touch `internal/converge` (error contract, drivers,
rendering), `internal/source` (manifest validation, usability guard), `internal/cli` (personal-config
error handling, answer collection), `internal/lint` (parent resolution) and `internal/sbx` (the
`Machine` double).

**Tech Stack:** Go 1.26, Cobra, `gopkg.in/yaml.v3`, `golang.org/x/mod/semver`, `golang.org/x/term`,
existing `worktree.Git` and `sbx.Runner` adapters, Go table-driven tests.

## Global Constraints

- **Base for every diff is a recorded commit SHA, never the branch name `main`.** Local `main`
  (`448953e`) is ahead of `origin/main` (`d796a0b`) and contains two of this PR's own docs commits.
  `git diff main...HEAD` yields the wrong range. Use `git merge-base origin/main HEAD` when a
  whole-branch range is needed.
- **`task check` must be green before any task is reported DONE**: `task lint` (go vet + `gofmt -l`
  empty), `task typecheck` (`go build ./...`), `task test` (`go test -count=1 ./...`). `gofmt` is
  enforced, not advisory.
- **Reproduce first.** Every behavioral fix begins with a test that fails against the current code.
  Where a test double hides the defect, make the double honest first, watch the tests fail, then fix
  the production code. This is the pattern commit `1c9aca8`/`d4ece41` established on this branch.
- **Strict YAML everywhere** (`KnownFields(true)`, via `config.DecodeYAMLStrict`). No new decoder
  escapes it.
- **No test calls `t.Parallel()`, opens a socket, or spawns a process.** Packages running real git
  (`cli`, `spawn`, `worktree`) call `worktree.NeutralizeGitEnvironment()` in `TestMain`.
- **Credential values never enter plans, logs, errors, personal configuration, receipts, or test
  failure messages.**
- **Code, comments and user-facing messages are English.** The dominant style is a long "why" comment
  at the decision site naming what was rejected and the regression the choice prevents. Match the
  density around you.
- **Errors name the file to fix and the remedy.** den refuses rather than normalizing in silence
  (spec §2).
- **Goldens** live in `internal/*/testdata/*.golden` and are compared by hand; there is **no
  `-update` flag**. Edit them manually.
- **Do not widen the manifest contract.** `sbx_github` keeps taking no `value_from` (see Task 4's
  ruling). No new resource type, no new credential type.
- **`sbx.Fake` (`internal/sbx/fake.go`) and `sbx.Machine` (`internal/sbx/machine.go`) are production
  files on purpose.** Do not move them into `_test.go`.

## Findings This Plan Does Not Fix

Recorded so a later reader knows they were considered, not missed:

- **Resume re-forces every image build.** `Service.forceRebuild` (`internal/converge/service.go:308`)
  decides from `personal.Version != m.Metadata.Version`, and `personal.Version` is written only at
  the end of `Apply`, so a resumed `den source configure` rebuilds images the interrupted run already
  built. Parked: the behavior is coarse but fail-safe, its comment defends the mode distinction
  deliberately, and a fix that consults the receipt to *skip* a rebuild can skip one that was needed.
  Cost of parking: wasted build time on resume, never a wrong result.
- **`sbx_github` via stdin.** Real sbx accepts `echo "$TOKEN" | sbx secret set github` (measured
  2026-08-16, `sbx secret set --help`), which would make the github credential non-interactive and
  CI-resumable like the registry one. Parked: `manifest.go:301` actively refuses `value_from` for
  this type, so adopting it widens the manifest contract — a follow-up delivery, not a review fix.

## File Structure

```
internal/converge/service.go     # T1 error contract, T5 (read-only reference)
internal/converge/sbx.go         # T4 github credential dispatch
internal/converge/build.go       # T6 Inspect Detail
internal/converge/render.go      # T6 RenderStatus warnings
internal/converge/model.go       # T8 Plan.Changes comment
internal/cli/source.go           # T2 LoadPersonal swallow
internal/cli/answers.go          # T5 non-interactive resume
internal/source/version.go       # T3 RequireUsable drift
internal/source/candidate.go     # T8 Candidate.installed comment
internal/source/manifest.go      # T8 build_network.allow validation
internal/lint/lint.go            # T7 parent resolution
internal/sbx/machine.go          # T4 honest github double, T8 bounds check
```

---

### Task 1: Restore the error contract on the two `Apply` failure paths

**Files:** `internal/converge/service.go`, plus its tests.

Two failure paths in `Service.Apply` return errors that drop the spec §12.3 contract (what remains
applied, and the command that resumes). Both are reachable in normal operation, and the first is what
makes Task 4's defect unreadable when it fires.

**Defect 1 — verification failure with no cause.** At `internal/converge/service.go:391`:

```go
o, err := d.Verify(ctx)
if err != nil || !o.Present {
    return nil, s.failed(req, applying, applied, planned, err)
}
```

When `Verify` succeeds but reports the resource absent, `err` is `nil`. `s.failed` receives a nil
cause, so `errors.As(nil, &resErr)` is false and the synthesized `ResourceError` carries
`Observed: planned.Observed` — the state read **before** the apply — and never states that
verification is what failed.

Required behavior: when `err == nil && !o.Present`, the error says the resource was applied and then
read back absent, and its `Observed` field carries `o.Detail` (the post-apply observation), not the
pre-apply one. Keep the existing behavior when `err != nil`.

**Defect 2 — `ReadSbxState` returns a bare error.** At `internal/converge/service.go:375`:

```go
state, err := ReadSbxState(ctx, s.Sbx)
if err != nil {
    return nil, err
}
```

On `ModeUpdate` this fires **after** `fastForward` has moved the installed checkout and after the
`applying` receipt was written, so the machine is in the recoverable window the receipt marks — but
the message never names the resume command. Required behavior: the error names the resume command for
the request's mode (the same one `s.failed` and `source.RequireUsable` print) and states that the
checkout is at the target version while its resources are not yet applied.

**Defect 3 — the fast-forward failure does not name its cause.** `s.fastForward`
(`internal/converge/service.go:369`) runs `merge --ff-only`. When the remote rewrote history, it fails
identically on every retry, so `den source configure <name>` — the command den prints as the remedy —
can never clear the `applying` marker. Required behavior: a failed fast-forward produces an error that
names the rewritten-history case and says the checkout under `sources/<name>/` must be re-cloned (or
the remote fixed) before a resume can succeed. Do **not** change the ordering: the applying receipt is
written before `fastForward` on purpose, and its comment states why.

**Steps:**

- [ ] Write table-driven tests in `internal/converge` that drive `Apply` through each of the three
      paths with `sbx.Machine` and assert on the error: (a) a resource that applies and verifies
      absent, (b) `ReadSbxState` failing on `ModeUpdate`, (c) `fastForward` failing. Watch them fail.
- [ ] Assert on the `ResourceError` fields (`Resource`, `Observed`, `Expected`, `Remaining`,
      `Resume`) where one is produced, not on a substring of the rendered string alone.
- [ ] Fix the three paths.
- [ ] `task check` green.

**Verification:** the new tests pass; every pre-existing test in `internal/converge` still passes.

---

### Task 2: Stop treating a `LoadPersonal` error as "never configured"

**Files:** `internal/cli/source.go`, `internal/converge/service.go`, plus their tests.

`source.LoadPersonal` errors are swallowed at two sites with an `err == nil` guard, so a corrupt
personal file, a strict-decode rejection, or a permission error is indistinguishable from "this source
was never configured here". Only `os.ErrNotExist` means "never configured".

**Site 1 — the load-bearing one.** `internal/cli/source.go:262`:

```go
if personal, err := source.LoadPersonal(home, name); err == nil {
    configured = personal.Version
}
```

`configured == ""` makes `source.DecideUpdate` (`internal/source/update.go:52-57`) return
`UpdateConverge` on its **first** branch, before the downgrade refusal at `update.go:71-77` (`c < 0`)
is ever reached. So on a machine configured for `2.0.0` whose personal file is corrupt, a team publish
of `1.0.0` is accepted as a legitimate first-time install. This defeats the "den converges forward
only" rule the spec states.

**Site 2.** `internal/converge/service.go:497` (`writePersonal`):

```go
if existing, err := source.LoadPersonal(req.DenHome, req.Name); err == nil {
    maps.Copy(...)
}
```

On a corrupt personal file the existing repository mappings are silently dropped and the file is
rewritten with only the current run's confirmed repositories.

**Not a defect:** `Service.forceRebuild` (`internal/converge/service.go:308`) has the same shape, but
an error there forces an image rebuild — the conservative direction. Leave it, and add one line to its
comment saying the swallow is deliberate there and why, so the next reader does not "fix" it.

**Required behavior:** at both sites, an error that is not `os.ErrNotExist` propagates as a refusal
naming `source.PersonalPath(denHome, name)` and the remedy. Neither site silently continues.

**Steps:**

- [ ] Write a test that a corrupt personal file plus a lower published version is **refused**, not
      converged — assert against `DecideUpdate`'s result and against the command-level behavior.
      Watch it fail.
- [ ] Write a test that `writePersonal` refuses rather than dropping existing repository mappings when
      the personal file is unreadable. Watch it fail.
- [ ] Fix both sites; extend `forceRebuild`'s comment.
- [ ] `task check` green.

**Verification:** a lower version behind a corrupt personal file is refused with the checkout
untouched; repository mappings survive.

---

### Task 3: Make `RequireUsable` refuse a checkout that drifted under the same version

**Files:** `internal/source/version.go`, plus its tests.

`source.RequireUsable` (`internal/source/version.go:112-123`) compares version **strings** only:
`m.Metadata.Version`, `personal.Version`, `receipt.Version`. The `Receipt` struct carries
`TargetCommit` and `ManifestDigest` (`internal/source/receipt.go:83-85`), written by `Apply`
(`internal/converge/service.go:341-353`) for exactly this purpose, and `RequireUsable` never reads
them.

Sources are plain git clones, so a checkout can be advanced outside `den source update` — a manual
`git pull` in `sources/<name>/`. If the new commit keeps the same `metadata.version` while the
manifest content changed (different egress hosts, different images, different exported nests),
`RequireUsable` reports the machine as fully converged and every `den spawn` proceeds against
resources that were never applied.

**Ruling (controller, not the implementer's to revisit): compare the manifest digest, not the
commit.** `RequireUsable` runs on every `den spawn`, and it already loads the manifest for
`m.Metadata.Version`, so hashing that same content is incremental — hashing the whole checkout is not.
The digest is exactly what den converged from. Commit drift with an identical manifest is a weaker
signal, and refusing on it would also fire on any local edit under `sources/`.

**Required behavior:** after the existing `receipt.Version != personal.Version` check, compare
`receipt.ManifestDigest` against `source.ManifestDigest(root)` for the current checkout. On mismatch,
refuse with a message that says the checkout's manifest changed since this machine converged, names
the resume command, and does not claim a version disagreement (there is none). An empty
`receipt.ManifestDigest` — a receipt written by an older den — is **not** a mismatch: skip the check
and say nothing, so an existing installation does not break on upgrade.

**Steps:**

- [ ] Write tests: (a) same version, same digest → usable; (b) same version, different digest →
      refused with the drift message; (c) same version, empty receipt digest → usable, no message.
      Watch (b) fail.
- [ ] Implement the check.
- [ ] `task check` green.

**Verification:** a manual `git pull` that changes the manifest at the same version is refused; an
old receipt with no digest still works.

---

### Task 4: Hand the terminal to sbx for the github credential

**Files:** `internal/sbx/machine.go`, `internal/converge/sbx.go`, plus their tests.

`credentialDriver.Apply` (`internal/converge/sbx.go:295`) configures the github credential with:

```go
fmt.Fprintf(out, "configuring the sbx github credential (sbx will ask for it)\n")
_, err := d.runner.Run(ctx, "secret", "set", "-g", githubService)
```

`Runner.Run` is `Exec.Run` (`internal/sbx/runner.go:259`), which leaves `cmd.Stdin` nil — the child
reads `/dev/null` — and buffers stdout and stderr into `bytes.Buffer`. sbx's prompt is therefore
invisible and its read gets EOF.

**Measured, 2026-08-16, against the real sbx on this machine** (`sbx secret set --help`): `github` is
one of the interactive services, and the help text states "When SERVICE is omitted, an interactive
prompt selects it" while documenting `echo "$KEY" | sbx secret set anthropic` as the non-interactive
form. The driver's comment ("github is interactive on sbx's side") is therefore accurate; the dispatch
does not honor it.

The consequence is not a bad message. `Apply` failing sends `Service.Apply` to `s.failed`, which
leaves the `applying` receipt, and `source.RequireUsable` then refuses every `den spawn`, `den nest
show` and `den build` for that source. The remedy den prints — `den source configure <name>` — calls
the identical `Run`, so the source is permanently unusable.

**Ruling (controller): use `Attach`, do not move github to stdin.** `Runner`'s own doc reserves
`Attach` for "an interactive shell … there's nothing to capture, and capturing would break
interactivity" (`internal/sbx/runner.go:31-33`), which is this case exactly. The stdin form measured
above would also work, but `manifest.go:301` actively refuses `value_from` for `sbx_github` with the
comment "sbx collects it interactively", so adopting stdin widens the manifest contract. That is a
follow-up, not a review fix. Cost if this ruling is wrong: the github credential still cannot be
configured without a terminal, which Task 5 makes explicit rather than silent.

`Attach` returns only `error`, and the current call already discards the output (`_, err :=`), so the
substitution is type-compatible.

**Required behavior:**

1. `sbx.Machine` answers `secret set -g github` honestly: it must record which runner method delivered
   the call, so a test can assert that the github credential went through `Attach` and not `Run`. The
   double currently flips a state bit regardless of method, which is what hides the defect from every
   existing test. **Make the double honest first and watch the tests fail before touching the driver.**
2. `credentialDriver.Apply` dispatches the github case through `d.runner.Attach`.
3. The comment at `internal/converge/sbx.go:284-286` states which runner method carries the flow and
   why, in the density of the surrounding comments.

**Steps:**

- [ ] Make `sbx.Machine` distinguish the runner method for `secret set -g <service>`; run the suite and
      record which tests fail and why.
- [ ] Add a test asserting the github credential is applied through `Attach`.
- [ ] Change the dispatch; update the comment.
- [ ] `task check` green.

**Verification:** the new test fails before the driver change and passes after; no other
`internal/converge` test regresses.

---

### Task 5: Let a non-interactive resume proceed when the machine already holds the credentials

**Files:** `internal/cli/answers.go`, plus its tests.

`collectInitialAnswers` (`internal/cli/answers.go:63`) computes `MissingCredentials` from the manifest
alone and refuses when `!IsTTY`, **before** anything observes the machine. So
`den source configure <name> --yes` in CI or a provisioning script cannot clear an `applying` receipt
left by an interrupted run — even when every declared credential is already configured in sbx. The only
escape is an answer file that re-supplies secrets sbx already holds.

This collides with spec §11.3, which makes `den source configure` the command that resumes a partial
application, and with `source.RequireUsable` refusing every spawn until it does.

**Required behavior:** a credential that the machine already holds is not missing. The refusal fires
only for credentials that are genuinely absent and genuinely needed — i.e. after the same inspection
`Service.Status`/`Plan` performs (`ReadSbxState` plus each credential driver's `Inspect`), not from the
manifest alone. When credentials remain missing and there is no terminal, keep refusing, and make the
message name each missing credential and the `credentials.<name>.from_env` answer-file key that
supplies it.

Note the interaction with Task 4: after that task the `sbx_github` credential needs a real terminal to
apply. When a github credential is genuinely absent and there is no TTY, the refusal must say so
explicitly — that this credential cannot be configured without a terminal — rather than pointing at an
answer-file key that `manifest.go:301` refuses to accept for this type.

**Steps:**

- [ ] Write a test: an interrupted convergence, every credential already present in the `sbx.Machine`
      double, `IsTTY` false, `--yes` → the resume proceeds. Watch it fail.
- [ ] Write a test: a genuinely absent registry credential, `IsTTY` false → refused, message names the
      credential and its `from_env` key.
- [ ] Write a test: a genuinely absent github credential, `IsTTY` false → refused, message says a
      terminal is required.
- [ ] Implement.
- [ ] `task check` green.

**Verification:** `den source configure --yes` resumes on a machine that already holds the secrets;
refusals still fire when a credential is really missing.

---

### Task 6: Make the plan and the status tell the truth

**Files:** `internal/converge/build.go`, `internal/converge/render.go`, plus their tests and any
affected goldens.

Two independent reporting defects. Both are output-only; group them in one task.

**Defect 1 — the plan claims an image is built on the line announcing the build.**
`buildDriver.Inspect` (`internal/converge/build.go:55`) returns
`Detail: "image " + stack.Image + " is built"` unconditionally, ignoring the `has` it just computed.
`RenderPlan` prints the observed line only when `Action != ActionUnchanged` — that is, precisely when
the image is **absent**. The user confirming a plan reads `observed: image devx:v1 is built` on the
line telling them den will build it. `credentialDriver.Plan` (`internal/converge/sbx.go:262`) sets
`Observed` only under `if o.Present`, which is the asymmetry showing this is unintended.

This is on the consent path: confirming a build is confirming that the source's provision scripts run.

Required behavior: `Detail` reflects `has`. When the image is absent, it says so.

**Defect 2 — `den source status` prints `blocked` with no reason.** `Service.Status`
(`internal/converge/service.go:207-210`) appends the `RequireUsable` error to `plan.Warnings` and only
then sets `StatusBlocked`; that warning is the sole explanation for the verdict (interrupted
convergence, version disagreement, unreadable personal file). `RenderStatus`
(`internal/converge/render.go:70`) never reads `p.Warnings`, so `internal/cli/source.go:794` reports
only `<name>: blocked`. `internal/cli/doctor.go:91` (`sourceDetail`) *does* read `p.Warnings[0]` for
the same source.

Required behavior: `RenderStatus` surfaces the warnings that explain a non-`ready` status, in the same
voice `den doctor` uses. `den source status` and `den doctor` must not disagree about why a source is
blocked.

**Steps:**

- [ ] Write a test that an absent image produces a plan whose observed line does not claim the image is
      built. Watch it fail.
- [ ] Write a test that `RenderStatus` on a blocked source names the reason. Watch it fail.
- [ ] Fix both; update any affected `testdata/*.golden` by hand (there is no `-update` flag).
- [ ] `task check` green.

**Verification:** the plan text for an absent image is honest; `den source status` and `den doctor`
give the same reason for the same blocked source.

---

### Task 7: Resolve a stack's `parent:` outside the export catalogue

**Files:** `internal/lint/lint.go`, plus its tests.

`lint.RunCatalogue` (`internal/lint/lint.go:79`) builds a `config.Stacks` containing **only** the
exported stacks, then hands it to `checkStack`, which resolves `parent:` through it
(`internal/lint/lint.go:158`, `stacks.Get(s.Parent)`). A source whose exported stack layers on an
unexported base is therefore refused:

```
stack "devx": stack "base" not found in <root>/stacks (declared stacks: [devx])
```

The message is wrong on its face — it names the checkout's `stacks/` directory, where `base` visibly
exists.

The catalogue scoping is correct for its stated purpose. The comment at `internal/lint/lint.go:60-66`
justifies it for **nest → stack** resolution: "an exported nest may not resolve its `stack:` through
[an unexported stack] — the teammate who installs the source addresses exported names and nothing
else." A stack's `parent:`, by contrast, is internal composition: nothing on the personal side
addresses it. `buildDriver.Apply` (`internal/converge/build.go:88-92`) confirms this — it resolves the
chain through `config.LoadStacks`, a full directory scan, plus `build.Chain`. So den **builds** what
lint **refuses**.

This one has teeth: `source.Lint` is the gate `den source add` runs post-clone, and it **deletes the
clone** on refusal.

**Required behavior:** `parent:` resolves against the full `stacks/` scan. Nest `stack:` references
stay catalogue-scoped — do not weaken that. A `parent:` that names a stack absent from the checkout
entirely is still an error, and its message still names the directory.

Decide and state in a comment whether an unexported parent's own faults are lint findings. The
existing comment says "an unexported object's own faults are not findings: nothing published can reach
it" — an exported stack's parent *is* reachable from something published, so that sentence no longer
covers this case. Whichever way you rule, the comment must say which and why.

**Steps:**

- [ ] Write a test with `stacks/base` and `stacks/devx` (`parent: base`), catalogue `{Stacks:
      ["devx"]}` → accepted. Watch it fail.
- [ ] Write a test that a `parent:` naming a stack absent from the checkout is still refused, with a
      message naming the directory.
- [ ] Write a test that an exported nest's `stack:` naming an unexported stack is still refused.
- [ ] Fix `RunCatalogue`/`checkStack`; update the comment.
- [ ] `task check` green.

**Verification:** a layered source passes `den lint` and `den source add`; the nest-scoping rule is
unchanged.

---

### Task 8: Validate declared egress hosts, and settle two comments and a bounds check

**Files:** `internal/source/manifest.go`, `internal/source/candidate.go`, `internal/converge/model.go`,
`internal/sbx/machine.go`, plus tests.

Four small independent edits. One dispatch, one review.

**8a — `build_network.allow` entries are unvalidated.** `checkResources`
(`internal/source/manifest.go:340` region) does not validate the hosts a `build_network` resource
declares before they reach `sbx.NormalizeNetworkResource`. That function appends `:443` to anything
without a colon, so an empty string becomes `:443` and a CIDR `10.0.0.0/8` becomes `10.0.0.0/8:443`.

**Scope note:** `NormalizeNetworkResource` itself is **not** in scope. Its bracketed-IPv6, single-colon
and bare-IPv6 branches are deliberate and documented (`internal/sbx/machine.go:265-292`), and commit
`d4ece41` already locks its edges in `internal/sbx/machine_test.go`. Do not change it.

Required behavior: a manifest declaring an unusable egress entry is refused at load, naming the file
and the key — den refuses rather than normalizing in silence (spec §2). At minimum refuse: an empty or
whitespace-only entry, an entry carrying a scheme (`https://…`), and an entry carrying a path (`/`).
Write the refusals as a table-driven test.

**8b — `Candidate.installed` is written and never read.** `internal/source/candidate.go:40` documents
it as a guard: "so Install cannot run twice and Root stops pointing inside temp". It is assigned at
`candidate.go:174` and read nowhere; `Install` has no such guard. Either wire the guard the comment
describes, or delete the field and the sentence. State which you chose and why in the commit message.

**8c — `Plan.Changes()` has no caller.** `internal/converge/model.go:225` documents it as "A plan that
changes nothing needs no confirmation", but `confirm()` (`internal/cli/answers.go:149`) asks
unconditionally. Either wire it into `confirm()` or delete the method and the sentence. **If you wire
it, a plan that changes nothing must still be shown** — only the confirmation prompt is skipped.
State which you chose and why.

**8d — unchecked index in the test double.** `Machine.RunSensitive` (`internal/sbx/machine.go:170`)
walks argv and reads `args[i+1]` for `--host` and `--env` with no bounds check, so an argv ending in
either flag panics instead of failing an assertion. Unreachable from the current call site
(`internal/converge/sbx.go:314` always pairs flag and value), but `machine.go` is a production file
other packages build on. Add the bounds check; a malformed argv must produce a test-visible failure,
not a panic.

**Steps:**

- [ ] 8a: table-driven refusal test, then the validation. Watch it fail first.
- [ ] 8b, 8c: choose wire-or-delete for each; if wiring, add the test that proves the mechanism runs.
- [ ] 8d: test that a truncated argv does not panic; add the guard.
- [ ] `task check` green.

**Verification:** a manifest with an empty or scheme-carrying egress host is refused by name; no
comment in the touched files describes a mechanism that does not exist.
