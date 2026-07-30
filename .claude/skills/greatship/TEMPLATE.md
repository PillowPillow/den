# Greatship Run — {{ISSUE_REF}}

> **greatship implemented this ticket autonomously on branch `{{BRANCH}}` — committed, NOT pushed.** The orchestrator (or you) pushes and opens the PR with the body below.

**Repo**: {{REPO}} · **Branch**: `{{BRANCH}}` (from `{{BASE_REF}}`)
**Status**: {{✅ done | ⚠️ circuit_breaker ({{verdict.exitReason}})}} · **Fix rounds**: {{ROUNDS}}/{{MAX_ROUNDS}}
**Gates**: code {{✅|❌}} · functional {{✅|❌}} · security {{✅|❌}}
**Verification**: `{{TEST_CMD}}` · `{{TYPECHECK_CMD}}` · `{{LINT_CMD}}`

## Criteria

{{One line per verdict.criteria entry: [x]/[ ] **Cn** (must|should) statement (verify: type) — met/unmet/unknown}}

## Tasks

{{One line per verdict.tasks entry: **Tn** title — done (n files) | failed | skipped, plus its `note` when non-empty (a skeptic rejection and its retry)}}

{{#if blockingFindings}}## ❌ Blocking findings still open

{{bullet list: severity · theme · title · location — claim → fix}}

These are validated Critical/High weaknesses the fix loop did not close before it exited — they are why a gate line reads ❌. Also folded into `verdict.prBody`.{{/if}}

{{#if gateGaps}}## ⚠️ Gate gaps — audits that did NOT run

{{bullet list, verbatim from verdict.gateGaps}}

This is the one thing a green gate line cannot tell you: an agent died, so that part of the diff was never examined. Also folded into `verdict.prBody` so the PR carries it.{{/if}}

{{#if unvalidatedFindings}}## ⚠️ Unvalidated findings (never adjudicated)

{{bullet list: severity · theme · title · location — claim}}

A validator died on each of these, so they were neither confirmed nor disproved, and none was fixed. Also folded into `verdict.prBody`.{{/if}}

{{#if ambiguities}}## Ambiguities recorded by the analyst

{{bullet list — a human should confirm these readings}}{{/if}}

{{#if advisoryFindings}}## Advisory findings (non-blocking, validated)

{{bullet list: severity · theme · title · location — claim → fix}}{{/if}}

{{#if fixAttempts}}## Fix attempts

{{One line per attempt: round N · id — fixed | failed | could-not-reproduce (notes)}}{{/if}}

{{#if integration}}## Integration sweep

suite: {{integration.suiteGreen}} · overall: {{integration.overall}} — {{integration.recommendation}}{{/if}}

## PR body (verbatim from verdict.prBody, including anything you folded in)

{{VERDICT_PR_BODY}}

---
_Verdict JSON: `.greatship/verdict.json` · greatship {{PLUGIN_VERSION}}_
