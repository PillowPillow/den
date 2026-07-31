# Milestone plan to close v1 (state as of 2026-07-31)

Written from the open issues (#4, #8, #9, #10), the spec, and the state of the
tree at `74072dd`. It says what is actually done, what each remaining issue
really costs, and in which order they can be executed — including the one
ordering constraint that is not written in any issue.

---

## 1. Where the repo really stands

| Scope | State |
|---|---|
| Plan 1 — foundations & inspection | shipped |
| Plan 2 — spawn (`den <nest>`, `ls`, `sh`, `rm`, `-i`) | shipped, smoke-tested against real `sbx` v0.35.0 (smoke #1, macOS verification) |
| Plan 3 — ports (`internal/ports`, `den ports`) | shipped and merged (#5, #6), **never run against a real `sbx`** |
| Plan 4 — build DAG (`den build`) | not started (#8) |
| Version / tag | none — no git tag, `Version = "dev"` (#10) |

`go test ./...` → **816 tests green in 12 packages**. That number says nothing
about ports against a real `sbx`: the suite is hermetic by design and never
invokes `sbx`.

## 2. What each open issue actually costs

- **#4 (docs)** — its part 1 is **already done**: the five documents it asks to
  commit are tracked and the tree is clean. What remains is the spec amendment,
  and it is **larger than the issue body says** (see §4).
- **#9 (smoke #2)** — requires the user's machine and a real `sbx`. No agent can
  produce it. It is the only source of truth for ports, `--only/--without`,
  `-i`, `--agent`, and an interactive `den sh`.
- **#8 (build DAG)** — the only remaining issue an agent can implement. But its
  own body flags that "if its image is missing" rests on **no attested `sbx`
  command** (§14.0 lists none that inspects images) and that the real refusal
  text must be measured against a real `sbx` first. **#8 is therefore blocked on
  a measurement only #9 can produce.**
- **#10 (v1.0.0)** — mechanical, but explicitly last: it presupposes #8 and #9.

## 3. The ordering constraint nobody wrote down

#8 cannot be implemented honestly before #9 runs, because the "image absent"
message it must fix was only ever observed against a **fake** `sbx`. So smoke #2
must carry two extra probes that #8 needs:

1. the **real** wording of `sbx create` refusing an unknown `--template`;
2. whether **any** `sbx` surface lists or inspects images (`sbx --help`, `sbx
   ls --help`, and anything the CLI exposes) — the answer decides whether "build
   the ancestors only if their image is missing" is implementable at all, or
   whether `den build` must always rebuild.

If the answer to (2) is "nothing", #8 shrinks: no image-presence check, and
`--force` loses its meaning. That is a scope decision, not an implementation
detail — hence measuring first.

## 4. Two items no issue owns today

- **The README lies right now.** It still says `den ports` is "Not shipped yet"
  and its command table has no `den ports` row, while `internal/cli/ports.go` is
  merged on `main`. #10 defers the README pass to last; this particular lie is
  live today and is the exact bug class of F6 (smoke #1). Fix it now, not at
  tagging time.
- **`HANDOFF.md` is stale and no issue owns it.** Dated 2026-07-28, it is the
  designated entry point ("read this file in full"), and it claims Plan 2 is
  written-but-not-executed, ports "to be written", 34 commits and nothing
  pushed. Everything in that TL;DR is now false. It needs its own issue.

Two more spec drifts, unlisted by #4:

- **§14.0** still carries "(copied here from plan 2, which is not a tracked
  file)" — plan 2 is tracked since. The parenthesis has to go.
- **§8 (ports model)** predates what shipped: it writes `den ports <nest>` where
  the command takes a **sandbox** name (`internal/cli/ports.go:39`), and says
  nothing of the two contracts #6 settled — no phantom window on a portless
  nest, and the honest `--add` abort contract.

## 5. Proposed milestones

**M3 — "repo truth + smoke #2"** (the next milestone)

1. README: add the `den ports` row, drop "Not shipped yet" for ports. (small,
   pulled forward from #10)
2. New issue: rewrite `HANDOFF.md` on the real state.
3. #4, widened: §14.1 (A4, A10, A11, kit order, `{"running"}` whitelist), §14.0
   parenthesis, **§8 ports drift**, French test names.
4. #9 smoke #2 on the user's machine, **carrying the two #8 probes of §3**, plus
   the F5 point (a global `local-policy` allowing 197 hosts means `egress:`
   documents intent rather than confining anything — must be written into the
   README and the spec).

Ordering inside M3: run #4 **after** the smoke, not before. §14.1 is what the
smoke amends; editing it first means editing it twice. #4's claim of primacy
("the others rest on the spec") holds for #8 and #10, not for #9 — a smoke
operator reads the issue body, not §14.1.

**M4 — "build + v1.0.0"**: #8 informed by the probes, then #10 (build line with
`-ldflags`, README final pass, `v1.0.0` tag).

## 6. What is deliberately not in v1

Unchanged from spec §1: autonomous flow (`den agent` / `den review`), remote kit
sync, agent-plugin snapshots, distribution registry/CI. #10 says it too, in its
"what this issue does NOT do".
