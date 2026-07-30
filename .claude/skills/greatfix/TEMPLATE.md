# Remediation Report — {{SOURCE_ID}}

> **greatfix applied these fixes in the working tree and verified them — it did NOT commit, push, or create a branch.** Review the diff below and commit what you accept. Each fix carries an independent skeptic review and an integration sweep; treat any "Needs attention" item as your call to keep, amend, or drop.

**Repo**: {{REPO}} · **Branch**: `{{BRANCH}}` · **Diff base**: `{{BASE_REF}}`
**Already dirty before the run**: {{PRE_DIRTY_FILES — "none", or the files whose changes are NOT greatfix's}}
**Source of findings**: {{SOURCE — e.g. "greatreview of MR 99"}}
**Planned → Fixed**: {{ACTIONABLE_COUNT}} actionable → {{FIXED_COUNT}} fixed ({{FAILED_COUNT}} failed, {{CNR_COUNT}} could-not-reproduce, {{SKIPPED_COUNT}} skipped)
**Verification**: `{{TEST_CMD}}` · `{{TYPECHECK_CMD}}` · `{{LINT_CMD}}`

---

## Verdict

> **{{Ready to commit (N fixes) | Needs attention before commit}}**

{{One-sentence justification: e.g. "All N fixes are green, reviewers accept, integration clean — ready to commit." OR "M of N green, but finding Fx's reviewer flags an unresolved regression — see below." State it as a conclusion; the user decides.}}

---

## Fixes

> One card per actionable finding, ordered as applied. The **Review** line is the independent skeptic's verdict on that fix.

### ✅ {{Fixed}} — Finding {{id}}: {{title}}
- **Decision**: {{tdd | apply}} · **Status**: fixed · **Score**: {{S}} ({{band}})
- **Location**: `{{location}}`
- **Change**: {{changeSummary}}
- **Files touched**: {{filesTouched}}
- **TDD evidence** (tdd only): red `{{redConfirmed}}` → green `{{greenConfirmed}}` · test: {{testAdded}}
- **Checks**: test `{{pass}}` · typecheck `{{pass}}` · lint `{{pass}}`
- **Review**: resolved **{{yes/partial/no}}** · test-meaningful **{{yes/no/na}}** · regression **{{none/possible/introduced}}** · compliant **{{yes/minor/no}}** — {{recommendation}}
  - {{evidence bullet (file:line)}}
  - {{evidence bullet}}
- **Notes**: {{notes, if any}}

{{repeat for each fixed finding}}

### ⚠️ {{Needs attention}} — Finding {{id}}: {{title}}
> Use this heading for fixes the reviewer did NOT fully accept (resolved≠yes, regression=introduced, or compliant=no), or for `failed` / `could-not-reproduce` outcomes.
- **Status**: {{failed | could-not-reproduce | fixed-but-flagged}}
- **What happened**: {{for failed: what blocked it + that edits were reverted; for could-not-reproduce: the red test never failed, finding may be invalid, no source edit made; for flagged: the reviewer's concern}}
- **Review** (if reviewed): {{verdict line + key evidence}}
- **Recommendation**: {{what the user should do}}

{{repeat as needed}}

---

## Integration sweep

- **Full suite green**: {{yes | no | unknown}} ({{ran `test && typecheck && lint`}})
- **Cross-fix interactions**: {{list, or "none — fixes are independent"}}
- **New weaknesses introduced by the fixes**: {{list, or "none"}}
- **Overall**: {{clean | concerns}} — {{recommendation}}

> If no fixes succeeded, write: **No fixes applied — nothing to integrate.**

---

## Deliberately not fixed (skipped)

> Items the plan or the user chose not to fix this run. Listed so nothing is silently dropped.

- **{{id}}** — {{title}} ({{score · band}}): {{reason — note "defer band" when the score, not the user, deprioritized it}}

> If nothing was skipped, omit this section.

---

## What to review

```
git diff {{BASE_REF}} --stat
```

{{paste the --stat output}}

Nothing has been committed. `git diff {{BASE_REF}}` shows the full change; commit the hunks you accept.

---

## How this report was produced

- **Phase 0**: findings normalized + repo context and diff base resolved, in the main context. Fixes ran on the current branch (no branch created unless requested).
- **Plan stage** (1 agent): classified each finding tdd / apply / skip and ordered them; you trimmed the plan.
- **Execute stage**: {{N}} fix agents (sequential, reproduce-first where behavioral, self-reverting on failure) → {{M}} skeptic reviewers (parallel, read-only) → 1 integration sweep.

Total agents spawned: {{TOTAL_AGENTS}}.

---

*greatfix mutates the working tree and verifies; it never commits, pushes, creates branches, or weakens tests. The remediation arm of greatreview.*
