# PR #59 review fixes — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the six verified findings of the `/code-review` run on PR #59
(`ci/prove-ldflags-stamp`). The PR added two build asserts to `ci.yml` that prove the
`-X …/internal/cli.Version=` stamp reached the linker. The review found the proof does not
cover the tag path (`release.yml`) nor the binaries users actually install
(`.goreleaser.yaml`), and that several of the added comments claim things the code does not do.

**Architecture:** The two asserts move into a **reusable workflow**
(`.github/workflows/gates.yml`, `on: workflow_call:`) called by both `ci.yml` and
`release.yml`, so a tagged commit runs the same gates as a PR and there is one copy to
maintain. A goreleaser snapshot build joins them, so the release ldflags line is verified by
the linker itself rather than by a text comparison. The comments are then made true against
the file's final state.

**Tech stack:** GitHub Actions YAML, `Taskfile.yml` (go-task v3), `goreleaser` v2, Go 1.x.

## Global Constraints

- **Comment density is the house style, and in these files the comment IS the deliverable.**
  The dominant style is a long "why" comment at the decision site, naming what was rejected
  and which regression the choice prevents. Terse code visibly does not match. Read the
  surrounding comments in `ci.yml`, `Taskfile.yml` and `.goreleaser.yaml` before writing.
- **A comment must not claim more than the code does.** Every one of findings 3–6 is a comment
  that overstates. Do not repair one overstatement by writing another. When a guarantee is
  unenforced, say it is unenforced.
- **No duplication between `ci.yml` and `release.yml`.** `Taskfile.yml`'s `check:` comment
  states the doctrine verbatim: *"Naming it removes the duplication and, with it, the chance
  the two workflows drift apart."* The reusable workflow is that doctrine applied to the
  workflow layer.
- **Do not fold the build asserts into `task check`.** `check` is what a local dev runs before
  every commit. `Taskfile.yml`'s `VERSION` comment documents that an eager `git describe` under
  an unrelated task is a defect they deliberately fixed by scoping the var to its one consumer.
- **`task check` (lint » typecheck » test, fail-fast) must pass before every commit.** `gofmt`
  is enforced, not advisory. This plan touches no Go code, so `check` should be green
  unchanged — run it anyway.
- **Code, comments and user-facing messages are English.**
- **Writes under `.github/` may raise a permission prompt** (a global settings rule). Make the
  edit and let the human approve it; do not route around it.
- **`/den`, `/dist/` and `.superpowers` are already git-ignored** — build artifacts from these
  steps leave no untracked file behind.

## Findings this plan closes

| # | File:line | Finding |
|---|---|---|
| 1 | ci.yml:75 | Asserts live in `ci.yml`'s `checks` job only; `release.yml`'s `test` job still runs bare `task check`, so a tagged commit is never subjected to the stamp proof. |
| 2 | ci.yml:75 | Proof covers `task build` only; `.goreleaser.yaml`'s ldflags stamp the shipped binaries, and the Go linker drops a wrong `-X` symbol silently. |
| 3 | ci.yml:11 | Header cites `internal/ports/hermeticity_test.go` as the guard keeping the `den version` path fork-free. That test scans **direct** imports of `internal/cli` only; the property that actually holds is laziness in `SystemDeps()`, which no test locks. |
| 4 | ci.yml:15 | Two references dangle on text the same diff deleted: "the stronger claim" and "the same shallow tagless clone". Line 14 is left ragged mid-reflow. |
| 5 | ci.yml:94 | The "plain go build still answers dev" assert has no positive floor: `den dev` is emitted by BOTH branches of `resolveVersion`, so the assert goes vacuous if the runner stops stamping VCS info. |
| 6 | ci.yml:73 | The comment claims the `git describe` assert is "Symmetric under every outcome". `Taskfile.yml:30` has a `2>/dev/null \|\| echo dev` rescue the CI line lacks, and the step dies under the runner's default `bash -e` before printing its own diagnostic. |

Human decisions already taken (do not revisit):

- Finding 1 → **reusable workflow**, not a Taskfile target and not duplicated steps.
- Finding 2 → **goreleaser snapshot build in CI**, not a static symbol-match check. Reason: a
  text comparison proves the two files agree with each other, not that either names a symbol
  the linker accepts. Only building and running catches a symbol both files rename wrongly.

---

## Task 1: Extract the gates into a reusable workflow

**Files:** `.github/workflows/gates.yml` (new), `.github/workflows/ci.yml`,
`.github/workflows/release.yml`

Closes finding 1.

- [ ] Create `.github/workflows/gates.yml` with `name: gates` and `on: workflow_call:`,
      holding a single job that is the **exact current content** of `ci.yml`'s `checks` job:
      `actions/checkout@v7`, `actions/setup-go@v7` with `go-version-file: go.mod`,
      `arduino/setup-task@v3` with `version: 3.x` and `repo-token: ${{ secrets.GITHUB_TOKEN }}`,
      `- run: task check`, then the two existing assert steps.
- [ ] Move the comments with the code they explain. The `setup-task` pinning comment, the two
      long assert comments — they belong beside their steps in `gates.yml`, not left behind in
      `ci.yml`. Do not rewrite them in this task (Task 3 owns their content); move them verbatim.
- [ ] Give `gates.yml` its own header comment saying what it is and why it exists: one
      definition of the gates, called by `ci.yml` on push/PR and by `release.yml` on a `v*` tag,
      so the tagged commit and the reviewed commit are subjected to the same asserts. Name the
      regression it prevents: before this, `release.yml` ran bare `task check` and a tag pushed
      onto a commit that never went through a PR shipped unproven.
- [ ] `ci.yml`: replace the `checks` job body with `uses: ./.github/workflows/gates.yml`. A
      caller job that uses `uses:` may declare **no** `runs-on` and **no** `steps`. Leave the
      `install-script` job untouched, comments included. Keep the part of `ci.yml`'s header that
      is still about `ci.yml` (triggers, the `install-script` exception); the hermeticity
      argument moves to `gates.yml` with the job it describes.
- [ ] `release.yml`: replace the `test` job body with `uses: ./.github/workflows/gates.yml`.
      Keep the job id `test` so `release.needs: test` still resolves, or rename both together.
      `release.yml`'s header currently claims the test job "re-runs the same gates as ci.yml" —
      as of this task that becomes literally true; adjust the wording to say so.
- [ ] `secrets.GITHUB_TOKEN` is available to a called workflow automatically. Do not add
      `secrets: inherit` unless you verify it is required.
- [ ] **Verify branch protection survives the conversion.** Today the status check GitHub
      reports is `checks`. Once `jobs.checks` becomes a `uses:` job, the reported name becomes
      `checks / <inner-job-id>`. If `main` requires a status check literally named `checks`,
      every PR then waits forever on a check that no longer reports. Probe it:
      `gh api repos/PillowPillow/den/branches/main/protection --jq '.required_status_checks.contexts'`
      and `gh api repos/PillowPillow/den/rulesets --jq '.[].name'`. Give the inner job a
      deliberate, stable id so the composed name is predictable. If a required context names
      `checks`, **report it to the human — do not change repo settings yourself.**
      (`release.needs: test` is unaffected: job-level `needs` resolves against a `uses:` job.)
- [ ] Verify: `task check` green. Both workflow files parse — check with a YAML parser
      (`python3 -c "import yaml,sys; yaml.safe_load(open(sys.argv[1]))" <file>`) and, if
      `actionlint` is available on the machine, run it. If neither is available, say so in the
      report rather than claiming validation you did not run.
- [ ] Commit.

## Task 2: Prove the goreleaser ldflags line, not just the Taskfile's

**Files:** `.github/workflows/gates.yml`

Closes finding 2. Depends on Task 1.

- [ ] Add a goreleaser snapshot build and an assert on the binary it produces, in the gates job,
      after the two existing asserts:
      `goreleaser/goreleaser-action@v7` with `distribution: goreleaser`, `version: "~> v2"`,
      `args: build --snapshot --single-target --clean`. Match `release.yml`'s existing
      goreleaser-action block for style.
- [ ] Assert on the produced binary's `den version`. Do **not** hard-code
      `dist/den_linux_amd64_v1/den` — the directory carries the build id and a goarch suffix
      that a config change can move. Locate it (`find dist -type f -name den`), assert exactly
      one match, run it, and refuse `den dev` and `den ` (empty stamp). Refusing those two is
      the whole point: they are what a dropped `-X` symbol produces.
- [ ] The checkout in `gates.yml` is depth-1 by default, which has no tags. goreleaser needs a
      tag; in snapshot mode it may substitute a fake one or refuse. Decide and **verify** which:
      if `fetch-depth: 0` is required, set it on the gates checkout and note in a comment that
      goreleaser's snapshot needs the tag history — the same reason `release.yml`'s release job
      already sets it. Changing the depth is allowed; it does not weaken the other two asserts
      (`task build` and the describe line read the same checkout either way, and a plain
      `go build` still reaches the `fromLocalVCS` branch).
- [ ] Comment the step in house style: why the Taskfile proof does not cover this line (the
      linker discards `-X` for a symbol that does not exist, silently, exit 0), and what the
      failure looks like if it is missing (a published v1.5.0 whose binaries answer `den dev`,
      caught only by the `install-script` smoke on a later push, after every `install.sh` and
      brew user already has it). Say also that on a `v*` tag the tree is now built twice on
      purpose — a snapshot in `gates.yml`, then the real one in `release.yml`'s `release` job —
      because that is what moves the proof of the release ldflags line to *before* publication.
- [ ] Verify: if `goreleaser` is installed on this machine, run
      `goreleaser build --snapshot --single-target --clean` and the assert locally, and quote the
      output in the report. If it is not installed, say "unverified locally: goreleaser not
      present" — do not claim a run you did not make.
- [ ] Commit.

## Task 3: Make the asserts and their comments true

**Files:** `.github/workflows/gates.yml` (and `ci.yml` if any of the text stayed there)

Closes findings 3, 4, 5 and 6. Depends on Tasks 1 and 2 — it must see the file's final state.

Read the review findings 3–6 in the table above before editing. All four live in the same
~60 lines; one coherent pass, not four patches.

**Ignore the line numbers in the findings table.** Tasks 1 and 2 moved this text into
`gates.yml` and edited the header while doing so. Locate each finding by its offending sentence,
quoted verbatim below, with `rg -n` across `.github/workflows/`:

- Finding 3 → `is why this path has no raw system access to reach`
- Finding 4 → `remains the one` / `deliberate exception on the stronger claim`, and
  `the same shallow tagless clone`
- Finding 5 → `so the depth-1` / `runner reaches the` `fromLocalVCS` `branch`
- Finding 6 → `Symmetric under every outcome`

If a sentence is gone because Task 1 or 2 already rewrote it, say so in the report with the
text that replaced it — do not invent a patch site.

- [ ] **Finding 6 — the `git describe` line (code + comment).** The step currently runs the bare
      `git describe --tags --always --dirty`, while `Taskfile.yml:30` runs it with
      `2>/dev/null || echo dev`. Under the runner's default `bash -e` the step aborts at the
      `want=` assignment when describe fails, printing git's `fatal:` and never its own
      diagnostic.
      **Do not** simply copy the Taskfile's rescue: if both sides fall back to `dev` the assert
      goes vacuous in exactly the outcome that matters — a typo'd `{{.VERSION}}` also produces
      `den dev`, so a false green. Instead, refuse that outcome explicitly and name it:
      capture describe in an `if !` guard, and on failure print a message saying `task build`
      would stamp the literal `dev` here and the assert cannot tell that apart from a skipped
      stamp. Then correct the comment: the symmetry holds under every outcome `git describe`
      can **answer**; the failure outcome is refused on purpose, and the comment must say which
      of the two expressions differs and why.
- [ ] **Finding 5 — positive floor on the plain-go-build assert.** `resolveVersion`
      (`internal/cli/version.go:28-36`) returns `dev` from two states: `fromLocalVCS` true, and
      `fromLocalVCS` false with `buildinfo == "(devel)"`. The step reads only the output string,
      so it cannot tell which branch ran, and it goes vacuous under `-buildvcs=false`, a
      `safe.directory` refusal, or any checkout without a usable `.git`. Add the floor: assert
      that `go version -m "$RUNNER_TEMP/den-plain"` actually carries a `vcs.revision` setting,
      with a message naming what its absence means. The repo already treats a missing positive
      floor as a defect — `internal/ports/hermeticity_test.go` carries two, whose comments name
      the risk ("a silently empty package would make an 'absence of X' assertion vacuous").
      Read those two for the tone. Then correct the comment that currently infers the branch
      from the `den dev` output alone: with the floor in place the inference is sound, and the
      comment should rest on the floor, not on the output string.
- [ ] **Finding 3 — the hermeticity argument in the header.** The claim "`internal/ports/
      hermeticity_test.go` … is why this path has no raw system access to reach" is false.
      `TestCliImportsNoRawPortOrSystemAccess` calls `importsOfDir` on `internal/cli` alone and
      checks **direct** imports for `net`/`hash/fnv`/`os/exec` — deliberately non-transitive, in
      contrast to `TestSpawnDoesNotTransitivelyDependOnPorts` in the same file, which walks the
      graph. `internal/cli` reaches `os/exec` transitively through `internal/sbx`,
      `internal/doctor`, `internal/sshagent` and `internal/worktree`. Read both tests and
      `internal/cli/root.go`'s `SystemDeps()` before rewriting. What actually makes `den version`
      fork-free is that `SystemDeps()` returns only func values and plain structs —
      `sbx.NewExec` returns `&Exec{Bin: bin}`, `sshagent.System()` returns a closure,
      `doctor.SystemDeps()` assigns `exec.LookPath` without calling it. Say that, and say
      plainly that **no test locks it**: a constructor made eager later (an `exec.LookPath("sbx")`
      up front for a better error) would make every `den version` fork while the cited test stays
      green. Do not replace one overstated guarantee with another.
- [ ] **Finding 4 — the two dangling references.** "`install-script` remains the one deliberate
      exception on the stronger claim" — the stronger claim ("no socket, no process, no real
      sbx") was deleted in this same PR; the surviving claim is "no socket, no real sbx", and
      `install-script` is an exception to the socket half. "Measured 2026-08-07 on the same
      shallow tagless clone" has no antecedent either: the only other measurement note says
      "Measured locally on a dirty tree", which is neither shallow nor tagless. Give each
      reference its own antecedent, or state the fact directly. Also reflow the ragged line
      (`# \`install-script\` remains the one`) — it marks where the previous edit was left
      half-reflowed. **If Task 2 changed the checkout depth, "shallow" is no longer true at
      all** — the note must describe the checkout the job actually has.
- [ ] Verify: `task check` green; both/all workflow files still parse; every claim you left in
      the comments is one you personally checked against the file it names. Quote in the report,
      per finding, the file:line you verified it against.
- [ ] Commit.
