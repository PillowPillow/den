# PR #68 review fixes — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to
> implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the twelve verified findings of the `/code-review high` run on PR #68
(`feat/nest-instances`). The PR added `--as <label>` (a second sandbox-name component that is no
longer the worktree) and `select: prompt` (a nest with no default repo selection). One structural
cause dominates the findings: **`--as` decoupled sandbox-name component 2 from the worktree
directory**, while step 3 of spawn, `den rm`'s record replay, `den ls`'s WORKTREE column and the
drift warnings all still assume component 2 *is* the worktree. A second cluster sits in the new
selection gating on the attach branch.

**Architecture:** No new packages, no new concepts. Every fix pushes the same doctrine already
written in this repo: **what den mounted is recorded, not re-derived** (`internal/manifest`), and
**den never strands a live VM over a record it could not read** (T13/T16). The attach branch stops
computing worktree paths from today's flags and reads them from the record; `den rm` stops
reclaiming a directory another record still names; `den ls` stops treating an *unreadable* record
as *no record*; `den doctor` and `den nest show` learn that `select: prompt` makes an unmapped
optional `key:` a normal state, not a fault.

**Tech stack:** Go 1.x, `Taskfile.yml` (go-task v3), hermetic tests with `sbx.Fake`.

**Branch:** work happens on `feat/nest-instances` — the PR's own branch, currently clean. Do **not**
create a worktree, do not branch off.

## Global Constraints

- **`task check` (lint » typecheck » test, fail-fast) must pass before every commit.** `gofmt` is
  enforced, not advisory. Tests run with `-count=1` (`go test` alone can pass stale).
- **Goldens under `internal/*/testdata/*.golden` have NO `-update` flag.** Edit them by hand. Tasks
  that change user-visible output will hit this.
- **Test hermeticity is locked and non-negotiable.** No `t.Parallel()`, no socket, no process.
  Packages running real git (`cli`, `spawn`, `worktree`) call
  `worktree.NeutralizeGitEnvironment()` in `TestMain`. Every system access goes through
  `cli.Deps`.
- **Import bans are locked by `internal/ports/hermeticity_test.go`:** `internal/spawn` must not
  import `internal/ports`; `internal/cli` must not import `net`, `hash/fnv`, `os/exec`. Relevant to
  Task 1 if it reaches for liveness — it must not.
- **Comment density is the house style.** The dominant style is a long "why" comment at the
  decision site, naming what was rejected and which regression the choice prevents. Terse code
  visibly does not match. Read the comments around your edit before writing; several of them are
  *wrong after this fix* and must be corrected, not appended to.
- **A comment must not claim more than the code does.** Two findings here are comments that already
  overstate (`internal/cli/ls.go`'s "only when there is no record at all"). Do not repair one
  overstatement by writing another.
- **Errors name the file to fix and the remedy.** Messages naming the global config go through
  `config.GlobalPath(denHome)` — never the literal string `config.yaml`
  (`internal/config/paths.go:54-58` states this as doctrine, with no carve-out).
- **Code, comments and user-facing messages are English.** The spec and handoffs under
  `docs/superpowers/` are French.
- **Strict YAML everywhere** (`KnownFields(true)`): an unknown key is a load error, never a silence.
- **den refuses rather than normalizing in silence** (spec §2).

## Findings this plan closes

| # | File:line | Finding | Task |
|---|---|---|---|
| 1 | spawn.go:367 | `--as` lets two sandboxes of one nest own the same worktree; `den rm` on one moves the other's LIVE worktree to the trash. | T1 |
| 2 | spawn.go:781 | A different `-w` on a live `--as` instance takes the attach branch but step 3 still runs `worktree.Ensure`: worktrees and branches are created that nothing mounts and no den command reclaims. | T2 |
| 4 | spawn.go:935 | Re-attaching `--as` without repeating `-w` prints "is not mounted" per repo and advises `den rm` on a healthy VM. | T2 |
| 8 | spawn.go:540 | `--without` on a `select: prompt` nest silences the checklist and resolves the MAXIMAL set — the default such a nest declares it has not got. | T3 |
| 9 | spawn.go:540 | `--without` on a prompt-nest ATTACH skips the record rebuild and refuses over an unmapped key the bare attach handles. | T3 |
| 3 | spawn.go:547 | Live sandbox + unreadable/absent record + unmapped optional `key:` → den refuses to attach to a running VM, naming no remedy. | T3 |
| 6 | spawn.go:548 | `reportUnrebuiltSelection` fires on nests that declare no optional repo, naming a remedy `nest.Resolve` refuses. | T4 |
| 7 | spawn.go:545 | `-i` on a live sandbox of an ordinary nest is discarded with no checklist and no explanation. | T4 |
| 5 | ls.go:121 | An UNREADABLE record makes `den ls` print an `--as` label under WORKTREE as if it were a branch — what the comment above the switch claims is avoided. | T5 |
| 10 | nest.go:100 | `den doctor` and `den nest show` do not consult `n.Select`, so the flagship `select: prompt` nest reports as broken (doctor exits 1 on a correctly configured machine). | T6 |
| 15 | interactive.go:246 | `unmappedNote` hard-codes `config.yaml` instead of `config.GlobalPath(denHome)`: under `DEN_HOME` it names a file the user cannot find. | T6 |
| 14 | CLAUDE.md:27 | `<nest>[.<worktree>]` identity notation is what this PR invalidates; README's twin sentence was updated in this PR, CLAUDE.md was not. | T7 |

## Human decisions already taken (do not revisit)

- **Finding 8 → REFUSE.** `--without` on a `select: prompt` nest is refused, mirroring the existing
  `-i` + selection-flag refusal at step 0. Rationale: there is no default set to subtract from. The
  error names `--only` as the scriptable spelling. README's "`--only`/`--without` … make the same
  selection from a script or CI" is corrected in the same task — it holds for `--only` alone.
  Consequence for finding 9: a prompt nest + `--without` never reaches the attach branch at all, so
  the skipped-rebuild path dies with it. The implementer must **verify** that claim, not assume it.
- **Finding 3 → KEEP REFUSING, better error.** den still refuses the attach; the refusal names
  `--without <key>` as the remedy. Not the tolerant path — the human ruled explicitly.
- **Finding 7 → PRINT THE LINE for every nest.** The checklist stays shut on a live sandbox (its
  mounts cannot change — decision 6). What moves is the explanation, out of the
  `n.PromptsForRepos()` guard, so `-i` is never silently ignored.
  `TestInteractiveDoesNotPromptWhenAttaching` asserts that silence deliberately today: update the
  test to assert the line, and correct the comment that records the old decision.

---

## Task 1: `den rm` must not reclaim a worktree another record still names

**Finding 1.** `den spawn api -w feature/123` then `den spawn api -w feature/123 --as reco` produce
two sandboxes (`api.feature-123`, `api.reco`) whose records both carry the SAME worktree mount —
`worktree.Ensure` is idempotent on the same branch (`internal/worktree/worktree.go:191`), so the
second spawn reuses the directory and records it with `Worktree: true`. `den rm api.reco` then runs
`cleanFromManifest` → `worktree.Remove` on that path: the directory is moved to den's trash and
`git worktree remove` unregisters it, while `api.feature-123` is still running and mounting it.

**Fix:** in `cleanFromManifest` (`internal/cli/rm.go:152`), before removing a repo's worktree, read
the other records (`manifest.List`) and skip the removal when another record — any sandbox but this
one — names the same `Mount`. Say so on stdout, naming the sandbox that still holds it. The record
of the sandbox being removed is still deleted at the end: den is removing THIS sandbox, not
disowning the directory.

**Deliberately NOT liveness.** Do not call `sbx ls` from `internal/cli/rm.go`: `den rm` must never
refuse or hang over a probe, and the record is the authority here by doctrine. A stale record whose
sandbox is gone is an accepted state (`den doctor` is what makes it addressable) — it costs one
un-reclaimed directory, where the alternative costs a live VM its workspace.

`cleanFromManifest` is shared with `den doctor --fix`; the same guard must therefore hold for both,
which is the reason it lives in that one body.

- [ ] Read `internal/cli/rm.go` (whole file) and `internal/manifest/manifest.go`'s `List`/`Broken`.
- [ ] Add the guard in `cleanFromManifest`. A `List` error must not make `den rm` refuse (T13/T16):
      on error, log nothing and proceed as today — name that choice in the comment.
- [ ] Broken records count as "still names it" only if they can be read; they cannot, so they are
      out of scope here. State it in the comment rather than leaving the reader to wonder.
- [ ] Test: two records sharing one mount, `den rm` on the second leaves the directory in place,
      removes the record, and prints the line naming the other sandbox.
- [ ] Test: the single-record case still reclaims exactly as before (regression floor).
- [ ] `task check`, then commit.

## Task 2: the attach branch stops re-deriving worktree paths

**Findings 2 and 4 are one change.** On the attach branch (`live != nil`):

- step 3 still calls `worktree.Ensure` whenever `o.Worktree != ""`, which CREATES a git worktree
  per repo plus the branch, for a sandbox that mounts none of it. The manifest is not rewritten on
  the attach branch, so `den rm` reclaims nothing: the directories and branches are orphaned with
  no den command that can reclaim them (finding 2).
- when `-w` is NOT repeated, `workspaces` holds the raw repo paths while `live.Workspaces` holds
  the recorded worktree paths, so `reportUnmountedRepos` prints "is not mounted" per repo and
  advises `den rm` on a healthy VM (finding 4).

**Fix:** on the attach branch, den computes the expected mounts, it does not create them.

- Replace `worktree.Ensure` with the pure `worktree.Path(layout, root, wt, repoPath)`
  (`internal/worktree/worktree.go:131`) when `live != nil`.
- Take the worktree name from the RECORD (`recorded.Worktree.Name`, and `Layout`/`Root` from the
  record too), not from `o.Worktree` — "what den mounted is recorded, not re-derived". With no
  readable record, fall back to today's derivation from the flags and keep the existing muting.
- The `worktree %s: %s` progress line describes creation; it must not print on the attach branch.

Verify with a test that a `-w` passed to a live `--as` instance creates NOTHING on disk, and that
re-attaching an `--as` sandbox without `-w` prints no unmounted-repo warning and no `den rm`
advice.

- [ ] Read `internal/spawn/spawn.go` step 3 (~line 770-830) and the attach branch (~900-980).
- [ ] Confirm `worktree.Path`'s signature and that `manifest.Worktree` carries `Name`, `Layout`,
      `Root` (it does — verify before relying on it).
- [ ] Implement; correct the step-3 comment, which currently describes one code path where there
      are now two.
- [ ] Test: live `api.reco` + `-w brand/new` → no directory created, no branch created, attach
      succeeds.
- [ ] Test: `--as` sandbox created with `-w`, re-attached without `-w` → no "is not mounted" lines,
      no `den rm` advice.
- [ ] Test: the ordinary `-w` create path is unchanged (regression floor).
- [ ] `task check`, then commit.

## Task 3: selection gating — refuse `--without` on a prompt nest, and name the remedy

Three findings, one decision site: the `selectionOpen` expression at `internal/spawn/spawn.go:540`
and the switch below it.

**Finding 8 (ruled: refuse).** At step 0, beside the existing `-i` + selection-flag refusal, refuse
`--without` on a `select: prompt` nest. The message says the nest has no default selection to
subtract from and names `--only` as the scriptable spelling. `--only` stays accepted: it states the
set outright, which is exactly what a prompt nest asks for.

**Finding 9.** With finding 8 refused, verify — do not assume — that no path remains where a prompt
nest reaches the attach branch with `selectionOpen == false` and a rebuild it needed. Record the
verification in the comment. If a path does remain, close it.

**Finding 3 (ruled: keep refusing, better error).** When the attach branch cannot rebuild the
selection (`recordedErr != nil`) and `nest.Resolve` then dies on an unmapped optional `key:`, the
user gets `repo key "crm" is not mapped on this machine` with a remedy pointing at `config.yaml` —
correct for a create, wrong here, where the sandbox is already running and the repo simply is not
part of it. den must name `--without crm` as the remedy for THIS case. Keep the refusal.

The natural shape is for spawn to wrap the `nest.Resolve` error on the attach branch when
`selectionUnknown` is set, rather than to teach `internal/nest` about liveness — `nest.Resolve`
stays pure and knows nothing of sandboxes. Use `errors.As`/a typed error if `internal/nest` already
exports one; check before inventing.

- [ ] Read `internal/spawn/spawn.go:430-560` in full, including the long comment block: it states
      the invariant this task edits, and it must come out TRUE, not merely appended to.
- [ ] Read `internal/nest/resolve.go`'s `resolveRepoKeys` refusal.
- [ ] Implement the three points above.
- [ ] Test: `--without` on a `select: prompt` nest is refused, message names `--only`.
- [ ] Test: `--only` on a `select: prompt` nest still works, on both create and attach.
- [ ] Test: live sandbox, unreadable record, unmapped optional key → refusal names `--without <key>`.
- [ ] Correct README's "`--only`/`--without` … from a script or CI" sentence to match the refusal.
- [ ] `task check`, then commit.

## Task 4: the attach-branch messages

**Depends on Task 3** — its guards sit in the region Task 3 rewrites. Do not start before Task 3 is
committed.

**Finding 6.** `reportUnrebuiltSelection` fires on nests that declare no optional repo at all: the
diagnostic describes a problem that cannot exist there, and the remedy it names (`--only`/
`--without`) is one `nest.Resolve` refuses ("is a required repo of this nest, it cannot be
removed"). Guard it on the nest actually having an optional repo. The create branch already knows
the right wording for that shape (`interactiveWithout`: "nest api declares no optional repo:
nothing to choose, every repo is mounted") — reuse the concept, do not duplicate the string.

**Finding 7 (ruled: print the line for every nest).** Move the "its mounts come from its creation …
to run a different set alongside it, spawn `--as <label>`" explanation out of the
`n.PromptsForRepos()` guard, so a discarded `-i` is never silent. The recorded-repos line stays
conditional on there being a record. Print the explanation when the user's request would otherwise
be silently dropped — that is `-i` (or a prompt nest) on a live sandbox — not on every attach.

`TestInteractiveDoesNotPromptWhenAttaching` asserts today's silence deliberately and says so in a
comment: update BOTH the assertion and the comment; a stale comment claiming a decision that was
reversed is worse than none.

**Handed over by Task 2 (its review raised the first as Important — it must not be lost):**

- **A refusal message Task 2 falsified.** Step 2bis (`internal/spawn/spawn.go:~651`) probes
  `worktree.CommonGitDir` for every repo whenever `-w` is given, attach branch included, and can
  refuse the whole spawn with "`-w` propagates a worktree to every repo of the spawn, and X is not a
  git repository". Since Task 2, `-w` propagates NOTHING on the attach branch: den refuses to attach
  to a healthy live VM citing a consequence that cannot happen, which brushes T13/T16. Fix: on
  `live != nil`, scope the probe to the no-record fallback that still needs it, or reword the refusal
  to what it actually protects.
- **`-w` is now silently ignored on a live sandbox with a record.** `den spawn api --as reco -w other`
  attaches and says nothing about the flag it did not honour — the silence spec §2 forbids. Print the
  line here, on the surface this task owns.

- [ ] Read the attach branch's message block and `reportUnrebuiltSelection` / `interactiveWithout`.
- [ ] Close the two items handed over by Task 2 above.
- [ ] Add the optional-repo guard (a helper on `*nest.Nest` if none exists — check first).
- [ ] Move the explanation out of the `PromptsForRepos` guard, keeping the recorded-repos line
      inside the record's own branch.
- [ ] Update `TestInteractiveDoesNotPromptWhenAttaching` and its comment.
- [ ] Test: nest with no optional repo, live, no record, `-i` → no unrebuilt-selection line.
- [ ] Test: ordinary nest, live, `-i` → the explanation line is printed.
- [ ] `task check`, then commit. Goldens are hand-edited if any move.

## Task 5: `den ls` — an unreadable record is not "no record"

**Finding 5.** `manifest.List` returns broken records in its second result and `den ls` discards it
(`manifests, _, mErr`). A sandbox created with `--as` whose record is corrupt — or written by a
NEWER den, which `manifest.Read` refuses by design — is therefore absent from `recorded`, so
`hasRecord` is false and the switch sets `wt = instance`: `den ls` prints the LABEL under WORKTREE,
and the user greps for a branch that exists in no repository. The comment above the switch claims
precisely this cannot happen ("applies ONLY when there is no record at all").

**Fix:** consume the broken list. A sandbox with an unreadable record renders WORKTREE as unknown
(`?`), never as the label — den does not know, and saying so is the house doctrine. Correct the
comment: a record that cannot be read is not "no record at all".

Check whether `den ls` should also mention the unreadable record elsewhere in its output; if den
already has a wording for that state (`den doctor` has one), reuse it rather than inventing a
second dialect. Do not make `den ls` refuse.

- [ ] Read `internal/cli/ls.go` (whole file) and `manifest.List`/`Broken`.
- [ ] Implement, correct the comment.
- [ ] Test: broken record + `--as` sandbox → WORKTREE is `?`, INSTANCE is the label.
- [ ] Test: no record at all → today's fallback is unchanged (regression floor).
- [ ] `task check`, then commit. Goldens are hand-edited.

## Task 6: `den doctor` and `den nest show` learn `select: prompt`, and the checklist names the real config path

**Finding 10.** `select: prompt` makes an unmapped optional `key:` repo a NORMAL state — the
checklist merely annotates it. But `internal/doctor/doctor.go` (~433-446) walks nests without
consulting `n.Select` and emits one FAIL per unmapped key, so `den doctor` exits 1 on a correctly
configured machine and the real problems drown. `den nest show`, documented as "the dry-run of
`den spawn`" (`internal/cli/nest.go:174`), calls `nest.Resolve` with no selection and refuses on
the first unmapped key — the dry-run of the new mode cannot be run on the nests the mode exists
for.

**Fix:** on a `select: prompt` nest, an unmapped optional `key:` is reported as information, not as
a failure — doctor stays green, `den nest show` renders the nest and annotates the unmapped keys
instead of refusing. On a `select: all` nest nothing changes: there the repo IS meant to be
mounted, and an unmapped key is a real fault.

**Finding 15.** `unmappedNote` (`internal/spawn/interactive.go:246`) hard-codes the string
`config.yaml`. Under `DEN_HOME` that names a file the user cannot find. Use
`config.GlobalPath(denHome)`, like `resolveRepoKeys` and `den doctor` do. `denHome` is a parameter
of `Spawn` and in scope at the single call site; thread it through `interactiveWithout`.

**Handed over by Task 3:** `den nest show --without` on a `select: prompt` nest is still accepted
(`internal/cli/nest.go:199` calls `nest.Resolve` directly, never through `Spawn`), while `den spawn`
now refuses it — one flag, two verdicts. Close that here: `den nest show` is documented as the
dry-run of `den spawn`, so it must refuse what a spawn refuses, with the same wording.

- [ ] Read `internal/doctor/doctor.go`'s nest walk, `internal/cli/nest.go`'s show path, and
      `internal/spawn/interactive.go`.
- [ ] Close the `den nest show --without` divergence handed over by Task 3.
- [ ] Implement all three; keep the wording family shared with `resolveRepoKeys`.
- [ ] Test: `select: prompt` nest with unmapped keys → `den doctor` exits 0 and reports them as
      information; `den nest show` renders and annotates.
- [ ] Test: `select: all` nest with an unmapped key → still a FAIL, still a refusal (regression
      floor).
- [ ] Test: the checklist annotation names `<denHome>/config.yaml`.
- [ ] `task check`, then commit. Goldens are hand-edited.

## Task 7: the two documents the shipped code contradicts

**Finding 14.** `CLAUDE.md:27` still states **"Identity is the sandbox name `<nest>[.<worktree>]`"**
and (line 29) describes component 2 as the flattened branch. That is exactly the reading this PR
removes: `--as` fills it with an arbitrary label, and `sbx.Sandbox.Worktree()` no longer exists
(renamed `Instance()`, whose own comment says "It is NOT 'the worktree'"). README's twin sentence
was updated in this PR; CLAUDE.md is absent from the 18 changed files. It is the one document the
repo tells every session to trust first, and the wrong inference it encodes is the one
`internal/cli/ls.go` and `internal/cli/rm.go` were both patched to stop making.

**Spec, "Limites assumées" §1.** The spec claims a second spawn on one branch dies; the code
shipped here disproves it (`worktree.Ensure` is idempotent on the same branch and returns the
existing directory). Correct the spec to say what the code does, and — now that Task 1 has shipped
— what `den rm` does about it.

- [ ] Update `CLAUDE.md`'s identity paragraph: component 2 is the INSTANCE (`--as` label, or the
      flattened branch of `-w`, or empty). Keep the paragraph's job — naming what keys off the
      sandbox name — intact.
- [ ] Correct the spec's "Limites assumées" §1 (French — the spec is French).
- [ ] Re-read README against the shipped code for the same class of claim; fix what Task 3 did not
      already correct.
- [ ] **Task 6 already edited two README paragraphs — do NOT re-derive or rewrite them:** the
      `select:` section's "a key that is not mapped costs nothing as long as you do not select its
      repo" claim (now stating what `den doctor` and `den nest show` do, and what stays a failure),
      and the `den nest show` flags paragraph (now stating that `--without` is refused there exactly
      as on a spawn). Both document behaviour Task 6 introduced. Read them, leave them.
- [ ] **Handed over by Task 4:** `README.md:558` is falsified by finding 7's ruling. It reads "`-i`
      on an ordinary live nest rebuilds from the record the same way, *silently — no explanation
      line, since the selection was never a nest-wide default to begin with*". den now prints the
      explanation there. The spec sample at
      `docs/superpowers/specs/2026-08-11-nest-instances-design.md:160-161` stays correct — leave it.
- [ ] **Handed over by Task 3's review:** `README.md:562` is half false. In the paragraph that opens
      on "Re-attaching to a live `select: prompt` sandbox" (`README.md:556`) it still says den names
      "`--only`/`--without` to pick a set explicitly"; on a prompting nest den now names `--only`
      alone, `--without` being refused there (`internal/spawn/spawn.go:~2076`).
- [ ] **Handed over by Task 6's review — two overclaiming clauses, one each:**
      `internal/nest/resolve.go:28-29` says `TolerateUnmappedOptional` is "the one field of this
      struct that is not a flag", which is false (`Repos` are command-line positionals, `Cwd` is a
      derived parameter and says so): drop the clause, keep "internal/spawn must never set it".
      README's new `select:` paragraph says an unmapped optional key "is reported, not failed"
      without scoping the `--only` refusal the same commit added — naming that key on `--only` still
      refuses in `den nest show`. One clause fixes it.
- [ ] `task check` (this task touches one Go comment and prose only — run it anyway), then commit.

## Task 8: `den rm`'s guard must see the records it cannot decode

**Found by Task 5, ruled by the human.** Task 1 made `cleanFromManifest` skip a worktree another
RECORD still names, but it reads `manifest.List`'s decodable half only. A sibling sandbox whose
record den could not decode — typically one written by a NEWER den, which `manifest.Read` refuses by
design — is invisible to that guard, so `den rm` can still move a live sibling's worktree to the
trash. The narrowest, most common case is also the recoverable one: a newer den's file is still
valid YAML, and only its `schema` is unknown.

**Ruling (do not revisit):** lax mount scan with a cautious fallback.

- For every `manifest.Broken` entry, re-read the file loosely for `repos[].mount` ALONE — no
  `KnownFields`, no schema check, nothing else consumed. Those mounts join the set the guard
  consults.
- A file that will not parse at all counts as an **unknown sharer**: den reclaims no worktree for
  that `den rm`, names the file it could not read, and removes nothing on disk. The sandbox and its
  own record still go — `den rm` never refuses (T13/T16).
- Strict decoding stays the rule everywhere else (spec §12). This is one deliberate lax reader, for
  one field, in the one place where guessing wrong destroys a live VM's workspace. Say exactly that
  at the decision site, and say what it does NOT do: it never writes, never deletes, never repairs.

- [ ] Implement the lax mount scan in `internal/manifest` (a named function, so the laxity has one
      site and one comment) and consume it in `cleanFromManifest`.
- [ ] Test: a sibling record with `schema: 9999` sharing the mount → the worktree survives, and the
      message names the sandbox that still holds it.
- [ ] Test: a sibling record that is not YAML at all → nothing is reclaimed, the message names the
      file, the sandbox and its own record are still removed.
- [ ] Test: no broken records → Task 1's behaviour is unchanged (regression floor).
- [ ] `task check`, then commit.

## Task 9: the legacy reclaim path needs the same guard

**Found by Task 8, in flight.** Tasks 1 and 8 put the shared-worktree guard in `cleanFromManifest`
only. When THIS sandbox's own record is absent or undecodable, `cleanWorktrees`
(`internal/cli/rm.go:~134-143`) falls back to `cleanWorktreesLegacy`, which derives the worktree from
the sandbox name and config and calls `worktree.Remove` **without consulting any other record** — so
a shared worktree can still be trashed on that branch. Same failure as finding 1, same data loss, one
branch over; it is reachable precisely because `--as` made two sandboxes of one nest able to share a
tree, which is what this PR introduced.

- [ ] Give `cleanWorktreesLegacy` the same holder check `cleanFromManifest` uses — including Task 8's
      lax mounts and its unknown-sharer hold-back. One helper, consulted by both paths, so the two
      can never disagree about what den may move.
- [ ] The legacy path derives a path den is not sure about in the first place; keep that
      already-documented tolerance intact (guessing a directory that does not exist costs nothing)
      and do not turn a miss into a refusal.
- [ ] Test: legacy path (no record for the sandbox being removed) + a sibling record naming the same
      derived mount → the worktree survives and the message names the holder.
- [ ] Test: legacy path with no sibling → today's reclaim is unchanged (regression floor).
- [ ] **Handed over by Task 8's review, same file, fix while you are in it:** `claim`
      (`internal/cli/rm.go:~310-318`) guards an empty mount but not an empty derived sandbox name.
      `manifest.List` admits any entry ending in `.yaml`, including a file named exactly `.yaml`;
      the trim then yields `""`, `holders[mount] = ""` is stored, and the guard's own `holder != ""`
      test reads it as "no holder" — so a broken record that DOES name a live mount silently fails
      to protect it, and (first-wins) its empty entry masks another broken record's claim on the
      same mount. Count such an entry as an unknown sharer: a file den never wrote is exactly what
      the cautious path is for. Also: the comment at `rm.go:~253-255` says "two branches below" for
      a rule that is **above** it (`rm.go:~228-230`) — one word.
- [ ] `task check`, then commit.

## Verification

- `task check` green at every commit.
- The twelve findings above are closed, each by a test that fails without the fix, except the
  documentation-only ones (14, and the spec correction).
- No new `t.Parallel()`, socket, process, or banned import.
