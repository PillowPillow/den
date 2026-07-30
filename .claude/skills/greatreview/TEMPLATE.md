# PR Review — {{PR_IDENTIFIER}}

> **Read this report as an audit, not an action plan.** Each finding documents a weakness with evidence and rationale so it can be lifted into a ticket, a remediation plan, or a follow-up session. This skill does not fix code.

**Source**: {{REPO}} · {{BRANCH_OR_PR}}
**Author**: {{AUTHOR}}
**Scope**: {{N_FILES}} files, +{{ADDITIONS}}/-{{DELETIONS}} LOC
**Themes audited**: {{THEMES_AUDITED — from themeOutcomes[].theme, never hard-coded: a subset run must not claim full coverage}}
**Raw findings → Validated**: {{RAW_COUNT}} → {{VALIDATED_COUNT}}
**Reviewed on**: {{DATE}}

---

## Verdict

> **{{Approve & merge | Request changes ({{N}} findings) | Block — Critical issues}}**

{{One-sentence justification tied to the findings below, including the band tally — e.g. "Request changes: 2 fix-now / 3 normal / 1 defer." State as a conclusion, not an instruction — the user decides whether to act.}}

---

## Phase 0 — Overview

{{1-3 lines on what the PR does in plain terms. Same content the built-in `/review` would produce: what changed, why, key files. Do not include opinions here — opinions live in the findings section.}}

**Files touched**:
- `{{path/to/file/a.ext}}` (+{{N}} −{{M}})
- `{{path/to/file/b.ext}}` (+{{N}} −{{M}})

---

## Validated findings

> Findings are grouped by **fix-worthiness band** (`Score = Impact × Opportunity`), then severity. Each is *portable*: the **Why this is a weakness** and **Impact if uncorrected** fields explain the rationale so the finding still makes sense when read outside this report.

### fix-now (band)

#### Finding {{N}}: {{short title}}
- **Band**: fix-now · **Score**: Impact {{I}} × Opportunity {{O}} = {{S}} {{(or "unscored — scorer failed, manual triage" if no score)}}
- **Severity**: {{Critical|High|Medium|Low}}
- **File:line**: `{{path:NN-MM}}`
- **Claim**: {{one sentence describing what is weak}}
- **Evidence**:
  ```
  {{concrete excerpt, grep output, or trace — quoted verbatim}}
  ```
  {{optional 1-2 lines tying the excerpt to the claim}}
- **Why this is a weakness**: {{principle violated and why it matters — cite a project rule, framework norm, or clear engineering principle}}
- **Impact if uncorrected**: {{what concretely breaks, degrades, or surprises later}}
- **Suggested fix**: {{one-line change}}
- **Score rationale**: impact — {{impactWhy}}; opportunity — {{opportunityWhy}}

### normal (band)

{{same card shape; repeat per finding}}

### defer (band)

{{same card shape; repeat per finding}}

### unscored (band — scorer failed, manual triage)

{{same card shape; only present if a scorer died}}

> Omit any band heading with no findings. If no validated findings at all, write: **No validated weaknesses across the themes audited.**

---

## Rebutted claims (transparency)

> Phase 1 surfaced these candidate findings; Phase 2 (skeptic validators) invalidated them. Listed for audit trail — they should NOT be acted on.

- **{{theme}}** — "{{one-line claim verbatim}}"
  *Verdict: INVALID.* {{validator's one-sentence counter-evidence}}
- **{{theme}}** — "{{one-line claim verbatim}}"
  *Verdict: EXAGGERATED → downgraded to Low (kept above).* {{reason}}

> If no claims were rebutted, write: **No claims rebutted — Phase 1 findings all survived validation.** If Phase 1 produced no findings at all, write: **No raw findings produced — nothing to rebut.**

---

## Residual risk (acknowledged, non-blocking)

> Items the PR author or this review explicitly flagged as deferred. They are NOT validated weaknesses but are worth tracking separately so they aren't lost.

- {{e.g., "Observability follow-up: PR defers a `Log::warning` when the new branch fires. Open a tracking issue."}}
- {{e.g., "Schema-level constraint suggested but out of scope here — separate migration PR."}}

> If none, omit this section.

---

## Phase 1 theme outcomes

| Theme | Raw findings | Validated | Notes |
|---|---|---|---|
| {{Theme}} | {{N}} | {{M}} | {{e.g., "branch order verified, null-coercion safe" — or "agent failed"}} |

> One row per `themeOutcomes[]` entry — only the themes actually run.

---

## How this report was produced

- **Phase 0**: Same fetch + focus-area scan as Claude Code's built-in `/review`, run in the main context.
- **Phase 1**: 6 theme-specialist agents in parallel (greatreview workflow), ≤5 findings each, empty findings explicitly allowed.
- **Phase 2**: 1 skeptic validator per raw finding, briefed to disprove. Verdicts: CONFIRMED / EXAGGERATED / INVALID.
- **Phase 2.5**: 1 cheap haiku `scorer` per validated finding — Impact × Opportunity (1–5 each); band + severity floor computed in JS.
- **Phase 3**: This report. INVALID dropped, EXAGGERATED downgraded.

Total agents spawned: {{TOTAL_AGENTS}}.

---

*Output is intentionally portable — copy any section into a ticket, doc, or follow-up plan. The review skill does not modify code or open follow-up PRs on its own.*
