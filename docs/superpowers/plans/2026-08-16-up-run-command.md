# `den up` / `den run` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `den spawn <nest> [repo...] [-- <cmd>]` with `den up <nest>` and `den run <nest> <cmd> [args...]`, moving ad-hoc repos behind a repeatable `--repo` and deleting `--` from the family.

**Architecture:** The remedy builder that `den exec` owns today (`execFlags` / `execShape` / `execRewrite` / `execFlagOf` / `execLine`) is rewritten first, on `exec` alone, in three commits: a derived flag table plus a hand-written token classifier, then a FlagSet re-read so a remedy never drops a flag cobra already consumed, then shell quoting so a path with a space produces a legal line. Only then are `up` and `run` born, sharing a `spawnNest` body the way `den exec` and `den shell` share `enterSandbox`, and `den spawn` is deleted in the same commit — the existing suite is the only oracle of the old behaviour, and it stops being one the moment two successors exist.

**Tech Stack:** Go 1.26, cobra v1.10.2, pflag v1.0.9, `sbx.Fake`, table-driven tests, Taskfile (`task check`).

**Spec:** `docs/superpowers/specs/2026-08-16-up-run-command-design.md`

## Global Constraints

Every task's requirements implicitly include this section.

- No test calls `t.Parallel()`, opens a socket, or spawns a process. `sbx.Fake` (`internal/sbx/fake.go`, a production file) is the double.
- `worktree.NeutralizeGitEnvironment()` stays called in `internal/cli`'s `TestMain`. Do not remove it.
- Goldens under `internal/*/testdata/*.golden` are compared by hand. **There is no `-update` flag** — edit them manually.
- Run `task check` (lint » typecheck » test, fail-fast) before every commit. `gofmt` is enforced, not advisory (`task lint` runs `test -z "$(gofmt -l .)"`).
- Tests run with `-count=1`; a plain `go test` can pass stale.
- Code, comments and user-facing messages are **English**. The spec and handoffs under `docs/superpowers/` are French.
- The dominant comment style is a long "why" at the decision site, naming what was rejected and what regression the choice prevents. Terse code visibly does not match.
- Assertions on refusal messages compare the **entire** message, never `strings.Contains` on a fragment. Slice 1 shipped a dead remedy because `strings.Contains(err, "-T")` did not look at the half that had rotted.
- `internal/spawn` must never import `internal/ports`; `internal/cli` must import none of `net`, `hash/fnv`, `os/exec`. Locked by `internal/ports/hermeticity_test.go`. `os` and `github.com/spf13/pflag` are **not** on that list and are used by this plan.
- Errors name the file to fix and the remedy. den refuses rather than normalizing in silence (spec 2026-07-27 §2).
- `.claude/worktrees/` is a shadow copy of the tree. Exclude it from every grep (`--exclude-dir=worktrees`) or counts double.

---

## File Structure

| File | Responsibility after this plan |
|---|---|
| `internal/cli/remedy.go` (**new**) | The remedy builder, shared by `exec`, `up`, `run`, `nest show`: the derived flag table (`denFlags`), the hand-written classifier (`classifyToken`), the shape (`execShape`, `execRewrite`), the FlagSet re-read (`readBackFlags`), shell quoting (`shellQuote`), and the line renderer (`remedyLine`). Moved out of `exec.go` because it stops belonging to `exec`. |
| `internal/cli/exec.go` | `enterArgs` (the `<name> <cmd>` validator, shared with `run`), `enterOptions`, `enterSandbox`, `newExecCmd`, `warnEmptyAgentOnReentry`. Loses `execFlags`, `execShape`, `execRewrite`, `execFlagOf`, `execLine`, `spawnArgs`. |
| `internal/cli/up.go` (**new**) | `newUpCmd`, `upArgs` (the four-branch validator, shared with `den nest show`), `registerSpawnFlags`, `spawnNest`. |
| `internal/cli/run.go` (**new**) | `newRunCmd`, `warnFirstCommandTokenIsADirectory`. |
| `internal/cli/spawn.go` | **deleted**. |
| `internal/cli/remedy_test.go` (**new**) | `shellSplit` (the replay splitter) and its own test; classifier and builder tests. |
| `internal/cli/up_test.go`, `internal/cli/run_test.go` (**new**) | `spawn_test.go` split by command. |
| `internal/cli/spawn_test.go` | **deleted**. |

`spawnNest` and `registerSpawnFlags` live in `up.go` rather than a third file: they are two short functions with one reader each, and `enterSandbox` — the model they copy — lives in `exec.go` beside its first caller too.

---

## Task 1: The flag table is derived, the classifier is hand-written

Spec §5 "La TABLE devient dérivée ; le classifieur reste écrit à la main", §8 commit 1.

**Files:**
- Create: `internal/cli/remedy.go`
- Modify: `internal/cli/exec.go` (delete lines 16-137 — `execFlag`, `execFlags`, `execShape`, `execRewrite`, `execFlagOf`, `execLine` — and update the one call site at line 172)
- Test: none. **This task edits no test file.** That is the gate.

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `type denFlag struct{ name string; takes bool }`
  - `func denFlags(cmd *cobra.Command) (long map[string]denFlag, short map[byte]denFlag)`
  - `func classifyToken(tok string, long map[string]denFlag, short map[byte]denFlag) (names []string, consumes bool, placeholder string, ok bool)`
  - `type execShape struct { flags []string; name string; command []string; sawDash bool; haveName bool; lifted map[string]bool }`
  - `func execRewrite(cmd *cobra.Command, args []string) execShape`
  - `func execLine(path string, s execShape, command []string) string` — unchanged signature this task; replaced in Task 2.

### Why this is the first commit, and why it edits no test

`execFlags` lists four flags by hand. `den run` carries fourteen spellings plus every `--x=value` form, and a hand list of fourteen desynchronizes at the first flag added, in silence. But the walk gives den the **names**, not a way to recognize a **token**: `execFlagOf` compares the pre-`=` segment to a flag name, which holds today only because `exec`'s single shorthand `-T` takes no value and is therefore a whole token. It misses every short spelling `run` introduces (`-wfeat`, `-w=feat`, `-iT`, `-Ti`, `-iTwfeat`, `-iT=true`), each of which would become the nest name or the first word of the command and reach the VM as `bash: -wfeat: command not found`.

On `exec` alone the derivation is provably conservative: `exec`'s derived table is `{workdir, no-tty/-T, den-home}` plus the named `help` exclusion — exactly the four hand-written entries. So the gate is falsifiable: **`internal/cli`'s suite goes green with zero test-file edits.** A red test here means the classifier changed `exec`'s behaviour. That is the finding; it is never an expectation to update.

Two placeholder strings legitimately change — `<dir>` → `<workdir>` and `<path>` → `<den-home>` — because the placeholder becomes `"<" + f.Name + ">"`. Step 1 proves no test asserts them.

- [ ] **Step 1: Prove no test asserts the placeholders that change**

Run:
```bash
grep -rn --exclude-dir=worktrees -E '<dir>|<path>' internal/cli/*_test.go internal/cli/testdata/
```
Expected: **no output**. If anything matches, stop and report — the zero-edit gate below is void and the plan needs revising before you continue.

- [ ] **Step 2: Create `internal/cli/remedy.go` with the derived table**

```go
package cli

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// denFlag is one flag den owns, as the DERIVED table carries it: the long
// name, and whether it takes a value.
//
// takes comes from NoOptDefVal being empty, which is pflag's own encoding of
// "this flag needs a value" — the exact fact the hand-written `placeholder`
// field encoded until 2026-08-16, and the one a hand list gets wrong first.
type denFlag struct {
	name  string
	takes bool
}

// denFlags walks the command's merged FlagSet and returns the two lookups the
// classifier reads: long names, and shorthand letters.
//
// The walk is the point: it sees den's own flags, the ROOT's persistent
// --den-home (cobra merges it into the same set), and every shorthand — so a
// flag added tomorrow is refused in first-command position without anyone
// editing a list. The list it replaces held four entries; `den run` needs
// fourteen, and a fourteen-entry list desynchronizes in silence.
//
// --help and -h are excluded BY NAME, and the reason is stronger than "the old
// list omitted them". The walk's contents are PATH-DEPENDENT (measured
// 2026-08-16): --help is present under Execute(), ABSENT under Find +
// ParseFlags, because cobra adds it in InitDefaultHelpFlag during execute().
// The named exclusion is what makes den behave identically on the production
// path and on the test path. Without it, `den exec api --help` — which must ask
// the program inside the sandbox for its help, as `docker compose exec` does —
// would be refused in production and accepted under a validator test.
//
// Returning early on the name also drops the shorthand: there is no `-h` row.
func denFlags(cmd *cobra.Command) (map[string]denFlag, map[byte]denFlag) {
	long := make(map[string]denFlag)
	short := make(map[byte]denFlag)
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Name == "help" {
			return
		}
		d := denFlag{name: f.Name, takes: f.NoOptDefVal == ""}
		long[f.Name] = d
		if f.Shorthand != "" {
			short[f.Shorthand[0]] = d
		}
	})
	return long, short
}
```

- [ ] **Step 3: Add the hand-written classifier to `remedy.go`**

```go
// classifyToken answers what ONE token of the positional tail is.
//
// It is written by hand, and that is the 2026-08-16 measurement's verdict: a
// derived NAME SET cannot classify a token. pflag's short-cluster rules do not
// follow from the names, and the naive reading inverts two of them (measured on
// pflag v1.0.9, replayed on a real den binary):
//
//   - `-iT=true` sets BOTH -i and -T. In parseSingleShortArg the test
//     `shorthands[1] == '='` runs BEFORE the NoOptDefVal test, so the `=` binds
//     to the letter PRECEDING it and TERMINATES the token, whatever that
//     letter's arity.
//   - `-wi feat` sets worktree="i". A letter that takes a value SWALLOWS the
//     rest of the cluster; `feat` stays positional.
//
// Returns:
//   - ok == false: the token is not den's. The scan ends here — everything from
//     this token on is the nest name or the command, verbatim.
//   - ok == true, consumes == false: den's, and its value (if any) is inside
//     the token.
//   - ok == true, consumes == true: den's, and its value is the NEXT token.
//     placeholder is what to write when there is no next token.
//
// names lists every den flag the token sets, which the caller needs for the
// duplicate rule of Task 2 — a cluster sets several.
//
// `--` is NOT handled here: the caller strips it first (it is the separator,
// not a flag), and `--` would otherwise fall into the long branch with an empty
// name and be misreported as "not den's".
//
// An UNKNOWN letter disqualifies the WHOLE cluster, deliberately. That is the
// measured status quo: `den exec api -Tv go build` is accepted today and stays
// accepted, `-Tv` reaching the VM. It is also the only right answer — `-Tv` is
// not a legal den spelling, so den cannot lift it into a remedy.
func classifyToken(tok string, long map[string]denFlag, short map[byte]denFlag) ([]string, bool, string, bool) {
	if len(tok) < 2 || tok[0] != '-' {
		return nil, false, "", false // "", "-", "api", "/tmp/x", "go"
	}
	if strings.HasPrefix(tok, "--") {
		name, _, hasEq := strings.Cut(tok[2:], "=")
		f, known := long[name]
		if !known {
			return nil, false, "", false // --nope, --help
		}
		if f.takes && !hasEq {
			return []string{f.name}, true, "<" + f.name + ">", true
		}
		return []string{f.name}, false, "", true
	}
	body := tok[1:]
	var names []string
	for i := 0; i < len(body); i++ {
		f, known := short[body[i]]
		if !known {
			return nil, false, "", false
		}
		names = append(names, f.name)
		// This test FIRST, whatever f.takes says: pflag binds the `=` to the
		// preceding letter and ends the token there.
		if i+1 < len(body) && body[i+1] == '=' {
			return names, false, "", true
		}
		if !f.takes {
			continue // a boolean; walk on to the next letter (-iT, -Ti)
		}
		if i+1 < len(body) {
			return names, false, "", true // attached value: -wfeat, -wi, -wT
		}
		return names, true, "<" + f.name + ">", true // -w feat
	}
	// Every letter was a known boolean: -iT, -Ti.
	return names, false, "", true
}
```

- [ ] **Step 4: Move `execShape` and `execRewrite` into `remedy.go`, reading the table**

Move the `execShape` type and its comment verbatim from `exec.go:57-70`, then add the `lifted` field and rewrite `execRewrite`:

```go
// execShape is the LEGAL command line den reads out of a refused one: the
// sandbox or nest name, den's own flags lifted to where they belong, and the
// command.
//
// It exists because every refusal ends in "write `…`", and a remedy that is
// itself refused costs the user a second round trip. Building the shape ONCE
// and phrasing the refusal from it is what makes the proposal answerable:
// TestExecRemediesAreThemselvesLegal feeds every remedy back through the
// validator and requires nil.
type execShape struct {
	flags   []string // den's own, value included, in the order they were typed
	name    string   // the sandbox or nest; "" when the line names none
	command []string // what runs inside the sandbox; empty when none was given
	sawDash bool     // a `--` was dropped from the line
	// haveName separates "no name yet" from "the name IS the empty string", and
	// the string's own emptiness cannot: `den exec "$SANDBOX" -T go build` with
	// the variable unset hands over an empty first token, and a scan that kept
	// looking would have taken `go` for the sandbox and proposed
	// `den exec -T go build` — a legal line naming a sandbox the user never
	// typed, which is worse than no proposal. It ends up in the branch that
	// proposes nothing.
	haveName bool
	// lifted names the den flags found in the TAIL. Task 2's re-read consults
	// it: a flag typed after the name is the last one typed, so it wins over the
	// same flag read back from the FlagSet.
	lifted map[string]bool
}

// execRewrite reads the positionals cobra handed over and returns that shape.
//
// One left-to-right pass, and the same rule on both sides of the name: a `--`
// is dropped, a den flag is lifted (with its value), and the FIRST token that
// is neither ends the scan — everything from there is the command, verbatim,
// its own flags included. Both sides, because the name is not always first:
// pflag eats a LEADING `--` before its interspersed check (measured 2026-08-14,
// cobra v1.10.2), so `den exec -- -T api go build` arrives as
// ["-T","api","go","build"].
//
// cmd is a parameter rather than a package-level table because the table is
// DERIVED from it: `den exec`, `den run` and `den nest show` do not own the
// same flags, and each must classify against its own.
func execRewrite(cmd *cobra.Command, args []string) execShape {
	long, short := denFlags(cmd)
	s := execShape{lifted: map[string]bool{}}
	for i := 0; i < len(args); i++ {
		tok := args[i]
		if tok == "--" {
			s.sawDash = true
			continue
		}
		names, consumes, placeholder, ok := classifyToken(tok, long, short)
		if !ok {
			if !s.haveName {
				s.name, s.haveName = tok, true
				continue
			}
			s.command = args[i:]
			return s
		}
		for _, n := range names {
			s.lifted[n] = true
		}
		s.flags = append(s.flags, tok)
		if consumes {
			if i+1 < len(args) {
				i++
				s.flags = append(s.flags, args[i])
			} else {
				s.flags = append(s.flags, placeholder)
			}
		}
	}
	return s
}
```

- [ ] **Step 5: Move `execLine` into `remedy.go` unchanged**

```go
// execLine spells a shape back as a command line, for the "write `…`" half of
// every refusal. den's flags first, then the name, then the command — the order
// the contract requires, which is the whole point of proposing it.
func execLine(path string, s execShape, command []string) string {
	parts := make([]string, 0, len(s.flags)+len(command)+2)
	parts = append(parts, path)
	parts = append(parts, s.flags...)
	parts = append(parts, s.name)
	return strings.Join(append(parts, command...), " ")
}
```

- [ ] **Step 6: Delete the moved code from `exec.go` and fix the call site**

Delete `exec.go` lines 16-137 (`execFlag` through `execLine`, comments included). In `execArgs`, line 172, change:

```go
	s := execRewrite(args)
```
to:
```go
	s := execRewrite(cmd, args)
```

Then remove `"strings"` from `exec.go`'s imports if nothing else in the file uses it.

Run: `task typecheck`
Expected: PASS. If `strings` is reported unused, drop the import; if it is reported missing, put it back.

- [ ] **Step 7: The gate — the whole suite, zero test edits**

Run:
```bash
task check
git status --porcelain
```
Expected: `task check` PASS, and `git status` shows **only** `internal/cli/remedy.go` (new) and `internal/cli/exec.go` (modified). No `_test.go` file may appear.

If a test is red: do **not** update its expectation. The classifier changed `exec`'s behaviour, which is exactly what this gate exists to catch. Report the failing assertion and stop.

- [ ] **Step 8: Commit**

```bash
git add internal/cli/remedy.go internal/cli/exec.go
git commit -m "refactor(cli): derive the flag table, hand-write the token classifier

The hand list of four flags becomes a walk of the command's merged FlagSet;
the classifier that reads it is written by hand, because a name set cannot
classify a token (-iT=true sets both, -wi swallows the cluster). exec's
derived table equals its old hand list, so its suite passes unedited."
```

---

## Task 2: A remedy carries the flags cobra already consumed (defect F)

Spec §5 "Le constructeur de remèdes porte deux défauts PRÉEXISTANTS" (F), §8 commit 2.

**Files:**
- Modify: `internal/cli/remedy.go` (add `readBackFlags`, rewrite `execLine`)
- Modify: `internal/cli/exec.go` (`execArgs`'s comment at the `ArgsLenAtDash() == 0` branch, lines 199-202)
- Test: `internal/cli/exec_test.go` (invert one row, add one test)

**Interfaces:**
- Consumes: `execShape` (with `lifted`), `execRewrite`, `denFlags` from Task 1.
- Produces:
  - `func readBackFlags(cmd *cobra.Command, lifted map[string]bool) []string`
  - `func remedyLine(source *cobra.Command, target string, s execShape, command []string) string` — replaces `execLine`. `source` is the command that PARSED the line; `target` is the command path the remedy names, which is not always source's.

### The defect, measured on today's binary

```
$ den exec --workdir /srv -- api go build
den exec: `--` is not needed, and a sandbox name must come first — write `den exec api go build`
                                                                        ↑ --workdir /srv gone
```

`execRewrite` reads only `args`, and pflag consumed `--workdir` before the validator ran. `exec.go:199-202` blesses the omission — "the flag is one cobra has honoured" — and that is false in its own terms: cobra honoured it on an invocation that is REFUSED and never runs. The remedy is a line the user retypes; without `--workdir` they silently get a different directory. With `--repo` the same defect loses a **mount**.

`Value.String()` is forbidden for `--repo`, `--only` and `--without`: measured, a `stringArray` holding `/a` and `/tmp/hot fix` renders `[/a,/tmp/hot fix]`, which reparses as one bogus path literally named that — syntactically legal, so it passes both `strings.Contains` and the replay validator, and semantically wrong. `pflag.SliceValue.GetSlice()` plus **one `--repo <v>` pair per element** is the only correct spelling, and it keeps the typing order §2 c.1 depends on.

- [ ] **Step 1: Write the failing test — the flag survives the separator**

Add to `internal/cli/exec_test.go`:

```go
// The remedy must carry a flag pflag consumed before the validator ran.
// Measured on the 2026-08-16 binary, the proposal dropped `--workdir /srv`
// entirely: a line the user retypes, silently landing them in another
// directory. exec.go called the omission a decision on the grounds that "cobra
// has honoured the flag" — it honoured it on an invocation that is REFUSED and
// never runs.
func TestExecRemediesCarryFlagsTypedBeforeTheSeparator(t *testing.T) {
	err := validateArgs(t, "exec", "--workdir", "/srv", "--", "api", "go", "build")
	if err == nil {
		t.Fatal("a leading separator must be refused")
	}
	const want = "den exec: `--` is not needed, and a sandbox name must come first — " +
		"write `den exec --workdir /srv api go build`"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/cli/ -run TestExecRemediesCarryFlagsTypedBeforeTheSeparator -count=1 -v`
Expected: FAIL, the got message missing `--workdir /srv`.

- [ ] **Step 3: Add `readBackFlags` to `remedy.go`**

```go
// readBackFlags spells back the den flags cobra ALREADY CONSUMED, so a remedy
// never drops one. It is the second source of the builder; execRewrite's lift
// out of the positional tail is the first.
//
// Four rules, written down because they will otherwise be guessed:
//
//   - ORDER: read-back flags first, in VisitAll's order — which is LEXICAL,
//     SortFlags defaulting to true (measured) — then the flags lifted from the
//     tail, in typing order. Order between DISTINCT flags is indifferent to
//     pflag; inside --repo, the slice's order IS the typing order, and
//     2026-08-04 depends on it (the repo order decides sbx's argv, hence
//     mounts[0], hence StartDir's rule 3).
//   - SCALAR DUPLICATES (`den run --workdir /a api --workdir /b go test`): the
//     LIFTED one wins and the read-back one is dropped. It is the last one
//     typed, and the one den is teaching the user to move. On `up`, where
//     interspersing is on, nothing is ever lifted and the conflict cannot arise.
//   - SLICE DUPLICATES: --repo, --only and --without are all SliceValue, and
//     the scalar rule does NOT apply to them. Both origins are emitted —
//     read-back first (necessarily typed left of the name), then lifted — one
//     `--<name> <value>` pair per element. Value.String() is FORBIDDEN here:
//     measured, a stringArray holding "/a" and "/tmp/hot fix" renders
//     "[/a,/tmp/hot fix]", which reparses as one bogus path named exactly that
//     — a syntactically legal remedy that is semantically false, so the replay
//     test cannot catch it.
//   - BOOLEANS: Changed says a boolean was TYPED, never how. A read-back
//     boolean is therefore written CANONICALLY with its value — `--detach=false`
//     — and never bare: emitting `--detach` for a `--detach=false` would flip
//     the value, and the remedy could then trigger a contradiction the original
//     line did not (`--detach=true` wakes spawn.go's refusal). The bare form
//     stays what the USER types; it is not what den PROPOSES.
//
// NoOptDefVal != "" is the boolean test rather than a type assertion on
// *pflag.boolValue, which is unexported. den owns no flag with an optional
// value, so the two are the same set here; a flag with one would need its own
// branch.
func readBackFlags(cmd *cobra.Command, lifted map[string]bool) []string {
	var out []string
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Name == "help" || !f.Changed {
			return
		}
		if sv, ok := f.Value.(pflag.SliceValue); ok {
			for _, v := range sv.GetSlice() {
				out = append(out, "--"+f.Name, v)
			}
			return
		}
		if lifted[f.Name] {
			return
		}
		if f.NoOptDefVal != "" {
			out = append(out, "--"+f.Name+"="+f.Value.String())
			return
		}
		out = append(out, "--"+f.Name, f.Value.String())
	})
	return out
}
```

- [ ] **Step 4: Replace `execLine` with `remedyLine`**

In `remedy.go`, delete `execLine` and write:

```go
// remedyLine spells a shape back as a command line, for the "write `…`" half of
// every refusal. den's flags first, then the name, then the command — the order
// the contract requires, which is the whole point of proposing it.
//
// TWO commands, not one, and that is what the inter-command remedies need:
// `den run api ~/dev/hotfix` proposes a `den up …` line, and `den up api -- go
// test` proposes a `den run …` line. source is the command that PARSED the
// line — it owns the derived table and the Changed values — and target is the
// path the proposal names. A builder taking only cmd.CommandPath() cannot write
// those lines; one taking only a target path has no FlagSet to read back from.
func remedyLine(source *cobra.Command, target string, s execShape, command []string) string {
	parts := make([]string, 0, len(s.flags)+len(command)+4)
	parts = append(parts, readBackFlags(source, s.lifted)...)
	parts = append(parts, s.flags...)
	parts = append(parts, s.name)
	parts = append(parts, command...)
	// target is NOT one of the parts: it carries a space ("den nest show"), and
	// Task 3's quoting would wrap it in quotes. It is den's own prefix, never a
	// shell word the user supplies.
	return target + " " + strings.Join(parts, " ")
}
```

- [ ] **Step 5: Update the five call sites in `execArgs`**

In `internal/cli/exec.go`, replace every `execLine(path, s, X)` with `remedyLine(cmd, path, s, X)`. Then rewrite the comment at the `ArgsLenAtDash() == 0` branch (lines 194-204), which currently blesses the defect:

```go
	case cmd.ArgsLenAtDash() == 0:
		// The one shape SetInterspersed(false) does not neutralize, and the only
		// reason this validator consults ArgsLenAtDash at all: pflag ate the
		// separator, so `--` is not in args and s.sawDash cannot see it.
		//
		// A den flag typed before that leading `--` was ALSO eaten, by the flag
		// parser this time. It used to be absent from the line proposed here,
		// and the omission was written down as a decision — "the flag is one
		// cobra has honoured". False in its own terms: cobra honoured it on an
		// invocation that is REFUSED and never runs, so the user retypes a line
		// missing their --workdir and lands somewhere else in silence. Since
		// 2026-08-16 remedyLine reads those flags back off the FlagSet.
		return fmt.Errorf("%s: `--` is not needed, and a sandbox name must come first — write `%s`",
			path, remedyLine(cmd, path, s, s.command))
```

- [ ] **Step 6: Invert the test row that encoded the defect**

In `internal/cli/exec_test.go`, `TestExecRemediesAreThemselvesLegal`, replace the row at lines 863-869 (the `"a flag before a leading separator"` case and the comment above it) with:

```go
		// This row was inverted on 2026-08-16. It used to expect
		// `den exec api go build` and carried a comment calling the dropped `-T`
		// an accepted omission. The remedy is a line the user RETYPES, so a
		// dropped flag is a silently different run — and with --repo it would be
		// a dropped mount.
		//
		// `--no-tty=true`, not `-T`: readBackFlags spells a read-back boolean
		// canonically with its value, and it has no shorthand path — it emits
		// "--" + f.Name. The bare form stays what the USER types.
		//
		// The replay holds: pflag parses `--no-tty=true` left of the first
		// positional, so s.flags is empty and enterArgs returns nil.
		{"a flag before a leading separator",
			[]string{"exec", "-T", "--", "api", "go", "build"}, "den exec --no-tty=true api go build"},
```

- [ ] **Step 7: Run the suite**

Run: `task test`
Expected: PASS. `TestExecRemediesCarryFlagsTypedBeforeTheSeparator` green; every other row of `TestExecRemediesAreThemselvesLegal` still green, its replay included.

- [ ] **Step 8: Commit**

```bash
git add internal/cli/remedy.go internal/cli/exec.go internal/cli/exec_test.go
git commit -m "fix(cli): a remedy carries the flags pflag already consumed

den exec --workdir /srv -- api go build proposed a line without --workdir:
retyped, it lands the user in another directory. The builder gains a second
source, the FlagSet read-back, with the slice/scalar/boolean rules written
down. The test row that blessed the omission is inverted."
```

---

## Task 3: A remedy quotes what a shell would split (defect G)

Spec §5 defect (G), §8 commit 3.

**Files:**
- Modify: `internal/cli/remedy.go` (add `shellQuote`, apply it in `remedyLine`)
- Create: `internal/cli/remedy_test.go` (`shellSplit` and its own test)
- Modify: `internal/cli/exec_test.go` (replay through `shellSplit`, two new rows)

**Interfaces:**
- Consumes: `remedyLine` from Task 2.
- Produces:
  - `func shellQuote(tok string) string`
  - `func shellSplit(t *testing.T, line string) []string` (test-only, in `remedy_test.go`)

### The defect, measured

```
$ den exec api --workdir "/tmp/hot fix" true
den exec: den's flags go before the sandbox name — write `den exec --workdir /tmp/hot fix api true`
```

Replayed, that binds `--workdir=/tmp/hot` and leaves positionals `[fix api true]`: sandbox `fix`, command `api true`. Legal, and absurd. Paths with spaces are real input (`internal/nest/repos_test.go:137`), and `--repo` makes them common. **The replay test cannot catch it**: `TestExecRemediesAreThemselvesLegal` replays with `strings.Fields`, which splits on the space exactly as the bug does, so the wrong line loops cleanly.

`shellSplit` must therefore land **before** the replay swaps to it, with its own test. A wrong splitter makes every replay assertion in this package vacuous.

- [ ] **Step 1: Write `shellSplit` and its test first**

Create `internal/cli/remedy_test.go`:

```go
package cli

import (
	"strings"
	"testing"
)

// shellSplit undoes shellQuote: it splits a proposed command line into the
// argv a shell would hand den.
//
// It replaces strings.Fields as the replay mechanism, and it has to: Fields
// splits on the very space the quoting protects, so it would declare the
// CORRECTED remedy broken. It is written here, tested here, and used by every
// replay below — a wrong splitter makes all of them vacuous.
//
// Do NOT "simplify" this away by having remedyLine return a []string for the
// test to replay. The property under test is that THE STRING den prints is
// legal when a human types it; replaying an argv bypasses the join and reopens
// exactly this hole.
func shellSplit(t *testing.T, line string) []string {
	t.Helper()
	var out []string
	var cur strings.Builder
	inWord, inQuote := false, false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case inQuote && c == '\'':
			inQuote = false
		case inQuote:
			cur.WriteByte(c)
		case c == '\'':
			inQuote, inWord = true, true
		case c == ' ':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	if inQuote {
		t.Fatalf("unterminated quote in %q", line)
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out
}

func TestShellSplitUndoesTheQuotingRule(t *testing.T) {
	for _, tc := range []struct {
		name string
		line string
		want []string
	}{
		{"plain words", "den exec api true", []string{"den", "exec", "api", "true"}},
		{"a quoted space", "den exec --workdir '/tmp/hot fix' api true",
			[]string{"den", "exec", "--workdir", "/tmp/hot fix", "api", "true"}},
		{"an empty word", "den exec '' true", []string{"den", "exec", "", "true"}},
		// The '\'' idiom: close the quote, an escaped quote, reopen.
		{"an inner single quote", `den exec 'it'\''s' true`,
			[]string{"den", "exec", "it's", "true"}},
		{"a glob character survives quoting", "den up --repo '/dev/proj-*' api",
			[]string{"den", "up", "--repo", "/dev/proj-*", "api"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := shellSplit(t, tc.line)
			if len(got) != len(tc.want) {
				t.Fatalf("split = %q, want %q", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("split = %q, want %q", got, tc.want)
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run it to verify the helper is correct**

Run: `go test ./internal/cli/ -run TestShellSplitUndoesTheQuotingRule -count=1 -v`
Expected: **PASS**, and that is not a broken red-green step. `shellSplit` is a test helper with no production counterpart, written in the same step as its test precisely because everything downstream depends on it being right: a wrong splitter makes every replay assertion in this package vacuous. If it fails, fix the helper here, before any production code exists to blame.

- [ ] **Step 3: Write the failing production test**

Add to `internal/cli/exec_test.go`:

```go
// A path with a space must come back quoted, or the proposed line reparses into
// something else entirely: `--workdir /tmp/hot fix api true` binds
// --workdir=/tmp/hot and leaves sandbox `fix`, command `api true`. Legal, and
// absurd. The assertion is on the WHOLE message: the half that rots is never
// the half a Contains happens to look at.
func TestExecRemedyQuotesAValueContainingASpace(t *testing.T) {
	err := validateArgs(t, "exec", "api", "--workdir", "/tmp/hot fix", "true")
	if err == nil {
		t.Fatal("a flag after the sandbox name must be refused")
	}
	const want = "den exec: den's flags go before the sandbox name — " +
		"write `den exec --workdir '/tmp/hot fix' api true`"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}
```

- [ ] **Step 4: Run it to verify it fails**

Run: `go test ./internal/cli/ -run TestExecRemedyQuotesAValueContainingASpace -count=1 -v`
Expected: FAIL, the got line carrying `/tmp/hot fix` unquoted.

- [ ] **Step 5: Add `shellQuote` to `remedy.go` and apply it**

```go
// shellSafe is the character set a token may carry bare. Everything else — a
// space above all, but also a `$`, a `*`, a backtick — forces quoting.
const shellSafe = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ" +
	"0123456789_@%+=:,./-"

// shellQuote renders one token as a shell word.
//
// SINGLE quotes, which is what makes this safe rather than merely tidy: POSIX
// single quotes interpolate nothing, so a path carrying `$`, `*`, a backtick or
// a backslash survives verbatim. An inner single quote is spelled '\'' — close,
// escape, reopen — the only way out of a single-quoted string.
//
// An empty token becomes '' rather than vanishing: `den exec "$SANDBOX" …` with
// the variable unset must not silently lose a word from the line den echoes.
//
// A non-ASCII rune is not in shellSafe, so it is quoted. Harmless, and the
// conservative direction: over-quoting produces a longer legal line,
// under-quoting produces a different command.
func shellQuote(tok string) string {
	if tok == "" {
		return "''"
	}
	for _, r := range tok {
		if !strings.ContainsRune(shellSafe, r) {
			return "'" + strings.ReplaceAll(tok, "'", `'\''`) + "'"
		}
	}
	return tok
}
```

In `remedyLine`, quote every part before joining (the target path stays bare — it is den's own prefix, and it carries a space in `den nest show`):

```go
	for i, p := range parts {
		parts[i] = shellQuote(p)
	}
	return target + " " + strings.Join(parts, " ")
```

- [ ] **Step 6: Run it to verify it passes**

Run: `go test ./internal/cli/ -run TestExecRemedyQuotesAValueContainingASpace -count=1 -v`
Expected: PASS.

- [ ] **Step 7: Swap the replay mechanism**

In `internal/cli/exec_test.go`, `TestExecRemediesAreThemselvesLegal`, replace:

```go
			replay := strings.Fields(got)[1:] // drop "den"
```
with:
```go
			// shellSplit, not strings.Fields: Fields splits on the space the
			// quoting rule protects, so it would declare the CORRECTED remedy
			// broken — and, before 2026-08-16, looped cleanly on a line that
			// reparsed into a different sandbox. See remedy_test.go.
			replay := shellSplit(t, got)[1:] // drop "den"
```

Add two rows to the table:

```go
		{"a value with a space after the name",
			[]string{"exec", "api", "--workdir", "/tmp/hot fix", "true"},
			"den exec --workdir '/tmp/hot fix' api true"},
		{"an empty value after the name",
			[]string{"exec", "api", "--workdir", "", "true"},
			"den exec --workdir '' api true"},
```

- [ ] **Step 8: Run the whole suite**

Run: `task check`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/cli/remedy.go internal/cli/remedy_test.go internal/cli/exec_test.go
git commit -m "fix(cli): a remedy quotes what a shell would split

den exec api --workdir '/tmp/hot fix' true proposed an unquoted line that
reparses into sandbox 'fix' running 'api true'. The replay test could not see
it: strings.Fields splits exactly like the bug. shellQuote lands with a
shellSplit replay and its own test."
```

---

## Task 4: `den up` and `den run` are born; `den spawn` is deleted

Spec §3, §4, §5 (`upArgs`, the run refusals), §6, §7, §8.

**This task is atomic and it is large.** It cannot be split: with `spawn` still registered the golden and the command listing sit in a half state, and the existing suite — which is the only coverage of the spawn path — has to land on `up` or `run` in one move. Of the 17 `"spawn"` argv tokens (measured 2026-08-16: `spawn_test.go` 7, `hostile_test.go` 5, `root_deps_test.go` 4, `root_test.go` 1), the two typing `"--", "true"` can only go to `run`; the rest carry no command and can only go to `up`.

**Files:**
- Create: `internal/cli/up.go`, `internal/cli/run.go`, `internal/cli/up_test.go`, `internal/cli/run_test.go`
- Delete: `internal/cli/spawn.go`, `internal/cli/spawn_test.go`
- Modify: `internal/cli/exec.go` (`execArgs` → `enterArgs`, delete `spawnArgs` at 215-225, comment at 433-434)
- Modify: `internal/cli/root.go` (two `AddCommand`, migration line at 397, delete `atLeastOneArg` at 262-269 and the `math` import, `SuggestFor` on `newRmCmd`)
- Modify: `internal/cli/rm.go` (`SuggestFor: []string{"down"}`)
- Modify: `internal/spawn/spawn.go` (the two remedy strings, lines 233 and 257)
- Modify: `internal/cli/testdata/unknown-command.golden` (by hand)
- Modify: `internal/cli/root_test.go`, `internal/cli/root_deps_test.go`, `internal/cli/hostile_test.go`, `internal/cli/exec_test.go`
- Test: the four `_test.go` files above plus the two new ones

**Interfaces:**
- Consumes: `execShape`, `execRewrite`, `remedyLine`, `readBackFlags`, `shellQuote` (Tasks 1-3); `shellSplit` (Task 3, test-side).
- Produces:
  - `func spawnNest(cmd *cobra.Command, denHome *string, o spawn.Options, deps spawn.Deps) error`
  - `func registerSpawnFlags(cmd *cobra.Command, o *spawn.Options)`
  - `func upArgs(cmd *cobra.Command, args []string) error` — also `den nest show`'s validator (Task 6)
  - `func enterArgs(cmd *cobra.Command, args []string, noun, shellCmd string) error` — `den exec`'s and `den run`'s
  - `func newUpCmd(denHome *string, deps spawn.Deps) *cobra.Command`
  - `func newRunCmd(denHome *string, deps spawn.Deps) *cobra.Command`
  - `func (s execShape) addFlag(name, value string) execShape`

### The four strings that must agree in three places

`Use:` and `Short:` surface in `den help`, in `cmd.UseLine()` inside the arity error `root_test.go` freezes, and verbatim in `unknown-command.golden`. Pinned here, once:

| | `Use:` | `Short:` |
|---|---|---|
| `up` | `up <nest>` | `Spawn or attach a nest's sandbox, then open a shell` |
| `run` | `run <nest> <cmd> [args...]` | `Spawn or attach a nest's sandbox, then run a command` |

Golden padding does not move: cobra pads to `max(11, longest name)`, the longest name is `completion` (10), so it stays 11 — same column as today.

- [ ] **Step 1: Write `internal/cli/up.go`**

```go
package cli

import (
	"fmt"
	"strings"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/spawn"
	"github.com/spf13/cobra"
)

// spawnNest is the body `den up` and `den run` share: resolve --den-home, point
// spawn's streams at THIS invocation, call spawn.Spawn.
//
// It is enterSandbox's twin (exec.go), and its virtue is the same: nothing is
// guessed from the command. Two spellings of one door is the failure mode #60
// named, and a shared body is what keeps that false of the BEHAVIOUR as well as
// of the name. What stays each command's own fits in two lines — `up` sets
// o.Nest and leaves o.Command empty (an empty Argv means `bash -l`, one layer
// down in spawn.Command); `run` sets o.Nest and o.Command = args[1:].
//
// Out, Err and In are decided HERE, at run time, because they alone depend on
// the command and hence on a test's SetOut/SetErr/SetIn. The terminal probe
// stays in deps — it describes the machine, not the command. Out set here is
// not the last word on it: on a non-tty command spawn.Spawn aliases it to Err
// itself, so den's own log never joins a pipe the command owns.
func spawnNest(cmd *cobra.Command, denHome *string, o spawn.Options, deps spawn.Deps) error {
	home, err := config.Home(*denHome)
	if err != nil {
		return err
	}
	d := deps
	d.Out = cmd.OutOrStdout()
	d.Err = cmd.ErrOrStderr()
	d.In = cmd.InOrStdin()
	return spawn.Spawn(cmd.Context(), home, o, d)
}

// registerSpawnFlags registers the flags `den up` and `den run` share.
//
// Shared rather than written twice: a flag added to one and forgotten on the
// other is silent — cobra reports nothing, and the flag simply reaches the VM
// as a command token on the command that lacks it. --detach and -T are NOT here
// because the two commands spell them differently: each registers the one that
// works and the one that is refused, with its own help text.
//
// --repo is a StringArrayVar, never a StringSliceVar, and that is measured:
// StringSlice splits on commas and a path may contain one. --only and --without
// keep StringSliceVar, and this slice does not change them — their comma
// limitation on a repo named `a,b` is pre-existing and has its own subject.
func registerSpawnFlags(cmd *cobra.Command, o *spawn.Options) {
	cmd.Flags().StringVarP(&o.Worktree, "worktree", "w", "", "worktree to propagate across all repos")
	cmd.Flags().StringVar(&o.Instance, "as", "",
		"name this instance, to run several sandboxes of one nest side by side")
	cmd.Flags().StringVar(&o.Agent, "agent", "", "agent to use (default: defaults.agent)")
	cmd.Flags().StringSliceVar(&o.Without, "without", nil, "exclude these optional repos")
	cmd.Flags().StringSliceVar(&o.Only, "only", nil, "keep only these optional repos")
	cmd.Flags().BoolVarP(&o.Interactive, "interactive", "i", false,
		"pick the nest's optional repos from a checklist (contradicts --only/--without)")
	cmd.Flags().StringVar(&o.Workdir, "workdir", "",
		"working directory for the command (default: the directory you ran den from, when the sandbox mounts it; otherwise the first workspace it reports)")
	cmd.Flags().StringArrayVar(&o.Repos, "repo", nil,
		"mount this repository too, ad hoc (repeatable; the order you type is the order den mounts)")
}

// addFlag appends a flag den's proposed line needs but the user never typed.
//
// Two remedies cannot be built without it, and both are inter-command: `den up`
// turning a stray positional into `--repo <path>`, and `den run`'s warning
// turning the first command token into one. Written as a method on the shape so
// those lines come out of the SHARED builder — a Sprintf at the call site is
// outside TestRunRemediesAreThemselvesLegal, hence free to rot the way slice
// 1's did.
func (s execShape) addFlag(name, value string) execShape {
	s.flags = append(s.flags, "--"+name, value)
	return s
}

// upArgs is `den up`'s validator, and `den nest show`'s.
//
// exactlyOneArg does NOT fit, and this is the one place this slice ADDS a
// message rather than moving one. The gesture the break makes most likely is
// finger memory — `den up api ~/dev/hotfix` — and under exactlyOneArg the user
// reads "exactly one argument expected, 2 received, starting with
// "~/dev/hotfix" — usage: …", which names neither --repo nor what changed.
//
// FOUR branches, and their ORDER is the subject. The command tail is args[1:]
// once the nest is identified, NEVER args[dash:]: on `up -- api`, dash is 0
// while `api` is the nest, so indexing by dash would swap the two.
// ArgsLenAtDash() says WHETHER a `--` was typed, it does not cut.
//
// pflag terminates its parse on `--` whatever SetInterspersed says, and `--`
// NEVER appears in args — only ArgsLenAtDash reveals it (measured 2026-08-16:
// `up -- api` → args ["api"], dash 0; `up api --` → args ["api"], dash 1;
// `up api -- go test` → args ["api","go","test"], dash 1). A validator counting
// positionals is therefore blind, and `den up api -- go test` reaches it as
// three positionals — whence the remedy `den up --repo go --repo test api`,
// legal, replayable, and proposing to mount two directories named `go` and
// `test` when the user meant `den run api go test`.
//
// A validator NEVER writes to a stream. `den run`'s directory warning lives in
// its RunE for that reason (run.go): first-defect-wins means a printing
// validator would staple advice under a line already refused for something else.
func upArgs(cmd *cobra.Command, args []string) error {
	path := cmd.CommandPath()
	if len(args) == 0 {
		return fmt.Errorf("%s: a nest expected — usage: %s", path, cmd.UseLine())
	}
	// Branch 2 is the DISCRIMINANT and runs before the repo branch: the user
	// wrote a separator, which in the old grammar meant "a command follows".
	// That reading beats the repo one.
	if cmd.ArgsLenAtDash() >= 0 {
		s := execRewrite(cmd, args)
		if len(args) > 1 {
			// A `run` typed `up`. The remedy names `den run`, NOT --repo:
			// `go test` is a command, and proposing to mount it as two
			// directories is the absurdity this branch exists to prevent.
			//
			// Reached from `den nest show` too, and accepted there rather than
			// special-cased: `den nest show api -- foo` proposes `den run api
			// foo`, which reads oddly from a dry-run but is the honest answer —
			// the user typed a command, and commands go to `den run`. Naming
			// `den nest show api` instead would drop `foo` in silence, which is
			// the normalization §2 refuses.
			return fmt.Errorf("%s: %s takes no command — write `%s`",
				path, path, remedyLine(cmd, "den run", s, args[1:]))
		}
		// The separator is merely useless: `up -- api`, `up api --`. The remedy
		// still carries every flag pflag consumed — `up --repo /a -- api` must
		// come back as `den up --repo /a api`, not `den up api`, or the mount
		// vanishes in silence.
		return fmt.Errorf("%s: `--` is not needed — write `%s`",
			path, remedyLine(cmd, path, s, nil))
	}
	if cmd.Flags().Changed("repo") && len(args) > 1 {
		// den cannot say WHICH positional is the nest, and it says so instead of
		// guessing. No remedy line is built here, deliberately: building one from
		// the positionals proposes the wrong nest.
		//
		// The likeliest cause is a shell pattern — --repo cannot take a glob, the
		// shell expands before den sees anything, --repo binds the first match and
		// the rest arrive as positionals. But Changed("repo") does NOT prove an
		// expansion: `den up --repo /a api /b` satisfies it with no pattern at
		// all. So the message states the FACT and names both exits, and claims no
		// cause. No os.Stat either: the trigger is a fact about the command line,
		// not a hypothesis about the disk.
		return fmt.Errorf(
			"%s: --repo was given and %d arguments remain, so den cannot tell which one is the nest\n"+
				"  — if a shell pattern expanded, quote it or repeat --repo once per path\n"+
				"  — if these are ad-hoc repos, repeat --repo once per path\n"+
				"  (the arguments were %s)",
			path, len(args), strings.Join(args, ", "))
	}
	if len(args) > 1 {
		// Finger memory, the case this validator exists for. The extra
		// positionals ARE repos, so they come back as --repo pairs through the
		// shared builder — never re-joined by hand, so this line enters
		// TestRunRemediesAreThemselvesLegal like every refusal.
		s := execRewrite(cmd, args)
		for _, p := range args[1:] {
			s = s.addFlag("repo", p)
		}
		return fmt.Errorf("%s: extra arguments — ad-hoc repos go behind --repo now — write `%s`",
			path, remedyLine(cmd, path, s, nil))
	}
	return nil
}

// newUpCmd builds `den up <nest>`: create-or-attach, then a login shell.
//
// It is `den spawn <nest>` of 2026-08-15, minus the ad-hoc repos' spelling and
// minus `--`. The name is compose's, and the 2026-08-05 objection to it — "up
// lies about the semantics, this is a spawn-OR-attach, not a start" — rests on
// a false premise, measured 2026-08-16 on Docker Compose v5.3.1: `docker
// compose up` on live containers neither recreates nor restarts them. It is a
// create-or-attach too.
//
// NO SetInterspersed(false), and that is a decision. shell.go:93-100 holds the
// argument word for word: `up` takes no command, so no flag has a possible
// second owner, and interspersing buys one thing — `den up api -T` reaches -T's
// NAMED refusal instead of being refused for its ARITY by a message naming
// neither the flag nor the way out.
func newUpCmd(denHome *string, deps spawn.Deps) *cobra.Command {
	var o spawn.Options

	cmd := &cobra.Command{
		Use:   "up <nest>",
		Short: "Spawn or attach a nest's sandbox, then open a shell",
		Args:  upArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// o.Command stays empty: an empty spawn.Command.Argv IS `bash -l`,
			// one layer down (internal/spawn/enter.go), where `den run` reads
			// the same default.
			o.Nest = args[0]
			return spawnNest(cmd, denHome, o, deps)
		},
	}

	registerSpawnFlags(cmd, &o)
	cmd.Flags().BoolVar(&o.Detach, "detach", false, "do not open a shell after the sandbox is up")
	// REGISTERED and always refused, like -T on `den shell`: a named refusal
	// beats cobra's `unknown flag: -T`. The refusal itself is NOT here — it is
	// spawn.go's existing Detach×Command / NoTTY×no-command contradiction, at
	// step 0 of Spawn, before a single config file is read. A second check on
	// the cobra side would be two sources for one verdict, which is what
	// enterOptions refused in slice 1.
	cmd.Flags().BoolVarP(&o.NoTTY, "no-tty", "T", false,
		"refused here — `den up` opens a login shell, which needs a terminal; use `den run -T <nest> <cmd>`")
	return cmd
}
```

- [ ] **Step 2: Write `internal/cli/run.go` (without the warning — Task 5 adds it)**

```go
package cli

import (
	"github.com/PillowPillow/den/internal/spawn"
	"github.com/spf13/cobra"
)

// newRunCmd builds `den run <nest> <cmd> [args...]`: create-or-attach, then the
// command. It is `den spawn <nest> -- <cmd>` of 2026-08-15, without the
// separator.
//
// It is NOT compose's ephemeral `run`. `docker compose run` builds a throwaway
// container beside the project that --rm deletes on exit; den has no such
// object. `den run` enters THE nest's sandbox, creates it if absent, and leaves
// it alive. Named here so a compose reader does not discover it by use.
//
// SetInterspersed(false), unlike `den up`: everything after the nest name is
// the command, verbatim, its own flags included. Without it, `den run api go
// test -v` dies on "unknown shorthand flag: 'v'". The consequence is the
// contract's break — den's own flags sit LEFT of the nest name — and enterArgs
// refuses the wrong order by name rather than letting `-T` reach the VM as
// `bash: -T: command not found`.
func newRunCmd(denHome *string, deps spawn.Deps) *cobra.Command {
	var o spawn.Options

	cmd := &cobra.Command{
		Use:   "run <nest> <cmd> [args...]",
		Short: "Spawn or attach a nest's sandbox, then run a command",
		Args: func(cmd *cobra.Command, args []string) error {
			return enterArgs(cmd, args, "nest", "den up")
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// enterArgs has refused every other shape, so args[1:] is a real
			// command. No ArgsLenAtDash: under SetInterspersed(false) it is
			// always -1 past the nest name, and the one shape where it is not —
			// a leading `--` — enterArgs already refused.
			o.Nest = args[0]
			o.Command = args[1:]
			return spawnNest(cmd, denHome, o, deps)
		},
	}

	registerSpawnFlags(cmd, &o)
	// REGISTERED and always refused; see newUpCmd for why the refusal is not
	// spelled here but in spawn.go's step 0.
	cmd.Flags().BoolVar(&o.Detach, "detach", false,
		"refused here — `den run` runs a command inside the sandbox; use `den up --detach <nest>`")
	cmd.Flags().BoolVarP(&o.NoTTY, "no-tty", "T", false,
		"do not allocate a terminal (for pipes and CI)")
	cmd.Flags().SetInterspersed(false)
	return cmd
}
```

- [ ] **Step 3: Generalize `execArgs` into `enterArgs` and delete `spawnArgs`**

In `internal/cli/exec.go`, rename `execArgs` to `enterArgs`, add the two parameters, and thread the noun through every message. Add above the existing comment block:

```go
// enterArgs validates `<name> <cmd> [args...]` — `den exec`'s shape and
// `den run`'s, from ONE function.
//
// noun is what the first positional is called ("sandbox" for exec, which takes
// the full sandbox name `den ls` prints; "nest" for run, which takes a nest
// reference and has no name until -w and --as have spoken). shellCmd is the
// sibling that opens a shell without a command: `den shell` for exec, `den up`
// for run.
//
// One function rather than two, because these are the two commands this slice
// exists to reconcile: `den exec api go test` and `den run api go test` must
// read identically, and a second copy is where they would drift apart.
func enterArgs(cmd *cobra.Command, args []string, noun, shellCmd string) error {
	s := execRewrite(cmd, args)
	path := cmd.CommandPath()
	switch {
	case s.name == "":
		return fmt.Errorf("%s: a %s name and a command expected, none received — usage: %s",
			path, noun, cmd.UseLine())
	case len(s.command) == 0:
		return fmt.Errorf("%s: no command given — write `%s`, or `%s %s` for a shell",
			path, remedyLine(cmd, path, s, []string{"go", "test"}), shellCmd, s.name)
	case cmd.ArgsLenAtDash() == 0:
		return fmt.Errorf("%s: `--` is not needed, and a %s name must come first — write `%s`",
			path, noun, remedyLine(cmd, path, s, s.command))
	case s.sawDash:
		return fmt.Errorf("%s: `--` is not needed — write `%s`",
			path, remedyLine(cmd, path, s, s.command))
	case len(s.flags) > 0:
		return fmt.Errorf("%s: den's flags go before the %s name — write `%s`",
			path, noun, remedyLine(cmd, path, s, s.command))
	}
	return nil
}
```

Keep every existing comment inside the switch, verbatim. In `newExecCmd`, set:

```go
		Args: func(cmd *cobra.Command, args []string) error {
			return enterArgs(cmd, args, "sandbox", "den shell")
		},
```

Delete `spawnArgs` entirely (`exec.go:215-225`). It goes with `--`, and its string "a nest must be named before `--`" is one of the 11 user-facing outputs.

At `exec.go:433-434`, the comment on `--no-tty` says `-w` "is `den spawn`'s worktree". Rewrite to `` `den up` / `den run` ``.

- [ ] **Step 4: Rewrite the two remedy strings in `internal/spawn/spawn.go`**

Both refusals stay where they are — step 0 of `Spawn`, before `config.LoadGlobal` at line 261 — so `den up api -T` refuses without reading a single config file and a broken nest cannot steal the message. Only the strings change; no logic moves to `internal/cli`.

Line 231-234:
```go
	if o.Detach && len(o.Command) > 0 {
		return fmt.Errorf(
			"--detach and a command contradict each other — drop one: --detach spawns " +
				"without entering the sandbox, and `den run` runs a command inside it — " +
				"use `den up --detach <nest>`")
	}
```

Line 254-258:
```go
	if o.NoTTY && len(o.Command) == 0 {
		return fmt.Errorf(
			"-T asks for no terminal and no command asks for a shell, which needs one — " +
				"give a command with `den run -T <nest> <cmd>`, or drop -T")
	}
```

Also rewrite the comment above the second refusal (lines 244-253): it promises `den spawn` "still takes" a command after `--`. That form is gone. Replace the last paragraph with: the remedy each command names is the one that exists ON it — `den up` sends the user to `den run`, `den shell` sends them to `den exec`, and neither mentions a separator den refuses.

- [ ] **Step 5: Wire the root and delete the dead validator**

In `internal/cli/root.go`, replace the single `newSpawnCmd` AddCommand (lines 169-190) with:

```go
	// `den up` and `den run` are ASSEMBLED here from the very fields newLsCmd
	// just got: deps.Sbx is the single source. Out/Err/In are left unset —
	// spawnNest overwrites them on every run from the command itself, the only
	// way to follow a test's SetOut.
	//
	// ONE Deps value for both, deliberately: two literals would be two places to
	// keep in sync, and a field wired on one command and forgotten on the other
	// is silent.
	spawnDeps := spawn.Deps{
		Sbx:       deps.Sbx,
		Git:       deps.Git,
		Policy:    deps.Policy,
		Freshness: deps.Freshness,
		SSHAgent:  deps.SSHAgent,
		IsTTY:     deps.IsTTY,
		// The real OS, named at the wiring site like every other system access.
		GOOS: runtime.GOOS,
		// The real clock for the source-staleness hint (spawn.Deps.Now): nil is
		// what the package's own tests want, but a live den wiring this to
		// nothing would silently drop the hint for every user, forever.
		Now: time.Now,
	}
	root.AddCommand(newUpCmd(&denHome, spawnDeps))
	root.AddCommand(newRunCmd(&denHome, spawnDeps))
```

Delete `atLeastOneArg` and its comment (lines 265-269) — both callers are gone — and remove `"math"` from the imports (measured: it has no other user in this package).

Rewrite the migration line at line 397:

```go
	b.WriteString("\n\n`den <nest>` and `den spawn <nest>` no longer spawn: " +
		"use `den up <nest>`, or `den run <nest> <cmd>`.")
```

The line stays STATIC — it does not read the den home, for the 2026-08-05 reason: a fallible config read on the CLI's most banal error path. `spawn`→`up` is edit distance 5 and `spawn`→`run` is 4, both above `SuggestionsMinimumDistance = 2`, so cobra will suggest nothing on `den spawn api`; this line is the whole migration.

- [ ] **Step 6: `SuggestFor: []string{"down"}` on `newRmCmd`**

In `internal/cli/rm.go`, add the field to the `&cobra.Command{…}` literal:

```go
		// A compose-shaped surface trains the fingers to type `den down`, and
		// cobra catches nothing on its own: `down` is edit distance 3 from `run`
		// and 4 from everything else, above SuggestionsMinimumDistance, and it
		// prefixes no command name — the other route into SuggestionsFor.
		// Verified 2026-08-16 on the real tree: `den down` printed the command
		// list with no "did you mean" at all.
		//
		// A field on `rm`, not a line in the migration message: that message
		// carries what den REMOVED (`den <nest>`, `den spawn <nest>`), and a verb
		// den never had is not part of it. Measured on cobra v1.10.2 with den's
		// tree: SuggestionsFor("down") returns [rm], and SuggestionsFor("dwon")
		// still returns [] — the field widens nothing else.
		//
		// The cost, named: `den rm` destroys without confirming (`sbx rm
		// --force`), so den suggests a destruction to someone who mistyped a
		// verb. It only SUGGESTS — the line runs nothing — and rm's Short
		// ("Destroy a sandbox (the agent profile persists)") prints a few lines
		// below in the same listing.
		SuggestFor: []string{"down"},
```

- [ ] **Step 7: Delete `spawn.go` and rewrite the golden by hand**

```bash
git rm internal/cli/spawn.go
```

Rewrite `internal/cli/testdata/unknown-command.golden` by hand (there is no `-update`): drop the `spawn` row, insert `run` between `rm` and `shell` and `up` between `source` and `version` (cobra sorts by name), rewrite the migration line. The result, in full:

```
unknown command "api"

Commands:
  build       Build stack images, in dependency order
  completion  Generate the autocompletion script for the specified shell
  doctor      Diagnose den's configuration and environment
  exec        Run a command in an existing sandbox
  help        Help about any command
  init        Create a den home from the shipped example
  lint        Validate a source checkout (stacks, nests, references, confinement)
  ls          List live sandboxes
  nest        Inspect the declared nests
  ports       Show where a sandbox's declared ports land on the host
  rm          Destroy a sandbox (the agent profile persists)
  run         Spawn or attach a nest's sandbox, then run a command
  shell       Open a login shell in an existing sandbox
  source      Manage team source repositories (stacks/nests shared over git)
  up          Spawn or attach a nest's sandbox, then open a shell
  version     Print den's version

`den <nest>` and `den spawn <nest>` no longer spawn: use `den up <nest>`, or `den run <nest> <cmd>`.
Run `den help <command>` for details.
```

- [ ] **Step 8: Split `spawn_test.go` into `up_test.go` and `run_test.go`**

```bash
git mv internal/cli/spawn_test.go internal/cli/up_test.go
```

Then, in `up_test.go`:

- Rename the helpers: `runSpawn` → `runUp`, `runSpawnWithInput` → `runUpWithInput`. Both build a bare root with `newSpawnCmd`; change to `newUpCmd` and change the prefixed token from `"spawn"` to `"up"`.
- Replace every `newSpawnCmd(&home, …)` with `newUpCmd(&home, …)` and every `"spawn"` argv token with `"up"`.
- Rewrite ad-hoc-repo tests from positionals to `--repo`. Add one that pins the typing order:

```go
// The order the user types --repo is the order den mounts, and 2026-08-04
// depends on it: the repo order decides sbx's argv, hence mounts[0], hence
// StartDir's third rule (reached when the spawn runs from outside every mount).
// A repeatable flag keeps it as faithfully as a positional did — StringArrayVar
// appends.
func TestUpKeepsTheOrderOfRepeatedRepoFlags(t *testing.T) {
	home := denHomeSpawnable(t)
	f, d := fakeSpawnDeps()
	if _, err := runUp(t, home, d, "--repo", "/dev/b", "--repo", "/dev/a", "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Assert on the sbx create argv the Fake recorded: /dev/b must precede
	// /dev/a. Read f.Calls and pin the exact positions — the helper the other
	// repo tests in this file already use.
	_ = f
}
```

Fill that assertion using whatever the neighbouring repo tests in this file already assert against (`f.Calls` / `f.HasPiped`); match their idiom rather than inventing one.

- Move the two tests typing `"--", "true"` (today at lines 384 and 409, `TestSpawnPutsItsOwnChatterOnStderrWithoutATty` and `TestSpawnKeepsItsChatterOnStdoutWithATty`) into a new `internal/cli/run_test.go`, renamed to `TestRunPutsItsOwnChatterOnStderrWithoutATty` / `TestRunKeepsItsChatterOnStdoutWithATty`, built on `newRunCmd`, with argv `"run", "api", "true"` — no separator.
- `TestSpawnRefusesNoTTYWithNoCommand` stays in `up_test.go`, renamed `TestUpRefusesNoTTY`, and asserts the **whole** message, which must name `den run`:

```go
func TestUpRefusesNoTTY(t *testing.T) {
	home := denHomeSpawnable(t)
	_, d := fakeSpawnDeps()
	_, err := runUp(t, home, d, "-T", "api")
	if err == nil {
		t.Fatal("-T with no command must be refused")
	}
	const want = "-T asks for no terminal and no command asks for a shell, which needs one — " +
		"give a command with `den run -T <nest> <cmd>`, or drop -T"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}
```

- `TestSpawnRefusesDetachWithACommand` MOVES to `run_test.go` as `TestRunRefusesDetach`, whole message, naming `den up --detach`:

```go
func TestRunRefusesDetach(t *testing.T) {
	home := denHomeSpawnable(t)
	_, d := fakeSpawnDeps()
	root := &cobra.Command{Use: "den", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newRunCmd(&home, d))
	_, err := executeCmd(t, root, "run", "--detach", "api", "true")
	if err == nil {
		t.Fatal("--detach with a command must be refused")
	}
	const want = "--detach and a command contradict each other — drop one: --detach spawns " +
		"without entering the sandbox, and `den run` runs a command inside it — " +
		"use `den up --detach <nest>`"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}
```

- [ ] **Step 9: Fix the three mechanical test files**

- `internal/cli/hostile_test.go`: 5 `"spawn"` argv tokens (lines 67, 104, 150, 225, 248) → `"up"`.
- `internal/cli/root_deps_test.go`: 4 `"spawn"` argv tokens (lines 96, 127, 164, 223) → `"up"`; and two `t.Fatalf` strings quoting `den spawn api --detach` (lines 165, 225) → `den up api --detach`.

  All nine go to `up`, and that is checked rather than assumed (2026-08-16):

  ```bash
  grep -n -A3 -F '"spawn"' internal/cli/hostile_test.go internal/cli/root_deps_test.go \
    | grep -E '"--"|"-T"|"--detach"'
  ```

  answers three lines, all `root_deps_test.go`, all typing `"--detach"` with no
  command after it — legal on `up`, refused on `run`. **No `"--"` anywhere**, so
  none of the nine is a `run`. Re-run that grep before editing: a line typing
  `"--"` could only go to `run`, and renaming it to `"up"` would leave a test
  that passes while covering nothing.
- `internal/cli/root_test.go`:
  - lines 206 and 246: text, mechanical.
  - line 287-288: the table row freezing the whole usage line splits in two:

```go
		{
			"up, missing argument",
			[]string{"up"},
			"den up: a nest expected — usage: den up <nest> [flags]",
		},
		{
			"run, no command",
			[]string{"run", "api"},
			"den run: no command given — write `den run api go test`, or `den up api` for a shell",
		},
```

  The spec §8 calls this split `TestSpawnWithoutANestNamesTheUsageLine`. **No function by that name exists** (checked 2026-08-16, `grep -rn` over `internal/`): it is this table ROW, and the two halves above are the whole split. Do not go looking for a test to move.

  Update the comment above `TestWrongArgumentCountNamesTheUsageLine` (lines 280-283): `den spawn` was the site exercising the unbounded-maximum wording; with `atLeastOneArg` gone, no site does, and the sentence must say so instead of naming a deleted command.
  - line 416: `strings.Contains(got, "`den spawn <nest>`")` — rewrite AND widen to the whole migration line, which is the pattern §8 condemns:

```go
	const migration = "`den <nest>` and `den spawn <nest>` no longer spawn: " +
		"use `den up <nest>`, or `den run <nest> <cmd>`."
	if !strings.Contains(got, migration) {
		t.Errorf("the listing must carry the whole migration line; got %q", got)
	}
```

- [ ] **Step 10: Write the new validator tests in `up_test.go`**

```go
// `den up api -- go test` is a `run` typed `up`. The remedy must name `den run`
// — not --repo, and not "extra arguments": `go test` is a command, and
// proposing to mount two directories named `go` and `test` is the absurdity the
// branch ORDER exists to prevent. `--` never appears in args; only
// ArgsLenAtDash reveals it, so a validator counting positionals sees three.
func TestUpRefusesADoubleDashWithACommandByNamingRun(t *testing.T) {
	err := validateArgs(t, "up", "api", "--", "go", "test")
	if err == nil {
		t.Fatal("a command after `--` must be refused")
	}
	const want = "den up: den up takes no command — write `den run api go test`"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	if strings.Contains(err.Error(), "--repo") || strings.Contains(err.Error(), "extra arguments") {
		t.Errorf("the command reading must beat the repo reading; got %q", err.Error())
	}
}

// The two shapes where the separator carries nothing: dash == 0 and
// dash == len(args).
func TestUpRefusesAUselessDoubleDash(t *testing.T) {
	for _, argv := range [][]string{{"up", "--", "api"}, {"up", "api", "--"}} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			err := validateArgs(t, argv...)
			if err == nil {
				t.Fatalf("%v must be refused", argv)
			}
			const want = "den up: `--` is not needed — write `den up api`"
			if err.Error() != want {
				t.Errorf("message = %q, want %q", err.Error(), want)
			}
		})
	}
}

// The branch-2 remedy must carry a flag pflag consumed before the separator, or
// the mount vanishes from the line the user retypes. This is defect (F) seen
// from `up`.
func TestUpRemedyCarriesTheRepoAcrossADoubleDash(t *testing.T) {
	err := validateArgs(t, "up", "--repo", "/a", "--", "api")
	if err == nil {
		t.Fatal("a useless separator must be refused")
	}
	const want = "den up: `--` is not needed — write `den up --repo /a api`"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}

// Finger memory: the gesture the break makes most likely. The message must name
// --repo and what changed, which is exactly what exactlyOneArg's "2 received"
// does not.
func TestUpNamesTheRepoFlagOnASecondPositional(t *testing.T) {
	err := validateArgs(t, "up", "api", "/dev/hotfix")
	if err == nil {
		t.Fatal("a second positional must be refused")
	}
	const want = "den up: extra arguments — ad-hoc repos go behind --repo now — " +
		"write `den up --repo /dev/hotfix api`"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}

// A shell pattern: --repo binds the first match, the rest arrive as positionals,
// and den cannot say which one is the nest. It must NOT build a remedy from
// them — branch 4 would take /dev/proj-b for the nest and propose mounting the
// real one.
func TestUpRefusesToGuessTheNestWhenRepoAndPositionalsCollide(t *testing.T) {
	err := validateArgs(t, "up", "--repo", "/dev/proj-a", "/dev/proj-b", "/dev/proj-c", "scratch")
	if err == nil {
		t.Fatal("--repo plus several positionals must be refused")
	}
	const want = "den up: --repo was given and 3 arguments remain, so den cannot tell which one is the nest\n" +
		"  — if a shell pattern expanded, quote it or repeat --repo once per path\n" +
		"  — if these are ad-hoc repos, repeat --repo once per path\n" +
		"  (the arguments were /dev/proj-b, /dev/proj-c, scratch)"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	if strings.Contains(err.Error(), "write `") {
		t.Errorf("den must not propose a line built from the positionals; got %q", err.Error())
	}
}

// The counter-example that made the message above get rewritten:
// Changed("repo") does NOT prove a pattern expanded. Here the user simply gave
// one repo to the flag and left another positional, and the message must claim
// no cause.
func TestUpDoesNotClaimAGlobWhenRepoWasSimplyGivenTwice(t *testing.T) {
	err := validateArgs(t, "up", "--repo", "/a", "api", "/b")
	if err == nil {
		t.Fatal("--repo plus two positionals must be refused")
	}
	if strings.Contains(err.Error(), "expanded to several") {
		t.Errorf("den must not invent a cause; got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "cannot tell which one is the nest") {
		t.Errorf("the message must state the fact; got %q", err.Error())
	}
}

// Interspersing is ON for `den up`, and this is what it buys: `den up api -T`
// reaches -T's NAMED refusal instead of an arity error naming neither the flag
// nor the way out. It runs through a real Execute() because the refusal lives in
// spawn.Spawn, past RunE.
func TestUpKeepsInterspersedFlags(t *testing.T) {
	home := denHomeSpawnable(t)
	_, d := fakeSpawnDeps()
	_, err := runUp(t, home, d, "api", "-T")
	if err == nil {
		t.Fatal("-T after the nest name must still reach its refusal")
	}
	if !strings.Contains(err.Error(), "drop -T") {
		t.Errorf("the named -T refusal must fire, not an arity error; got %q", err.Error())
	}
}
```

- [ ] **Step 11: Write the new classifier and builder tests in `run_test.go`**

```go
// Every short spelling the exact-name comparison missed before 2026-08-16. Each
// of these, judged "not den's", became the nest name or the first word of the
// command and reached the VM as `bash: -wfeat: command not found`. The rows the
// old comparison missed ARE the test.
func TestRunRefusesDenFlagsAfterTheNestName(t *testing.T) {
	for _, argv := range [][]string{
		{"run", "api", "--workdir", "/srv", "go", "build"},
		{"run", "api", "--workdir=/srv", "go", "build"},
		{"run", "api", "-w", "feat", "go", "build"},
		{"run", "api", "-wfeat", "go", "build"},
		{"run", "api", "-w=feat", "go", "build"},
		{"run", "api", "-iT", "go", "build"},
		{"run", "api", "-Ti", "go", "build"},
		{"run", "api", "-iTw", "feat", "go", "build"},
		{"run", "api", "-iTwfeat", "go", "build"},
		{"run", "api", "-iT=true", "go", "build"},
		{"run", "api", "--repo", "/a", "go", "build"},
		{"run", "api", "--", "go", "build"},
	} {
		t.Run(strings.Join(argv[2:], " "), func(t *testing.T) {
			if err := validateArgs(t, argv...); err == nil {
				t.Fatalf("%v must be refused", argv)
			}
		})
	}
}

// The value travels WITH the cluster instead of being abandoned: `-iTw feat`
// consumes the next token, and the lifted line must carry it.
func TestRunLiftsAShorthandClusterWithItsValue(t *testing.T) {
	err := validateArgs(t, "run", "api", "-iTw", "feat", "go", "test")
	if err == nil {
		t.Fatal("a cluster after the nest name must be refused")
	}
	const want = "den run: den's flags go before the nest name — " +
		"write `den run -iTw feat api go test`"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}

// pflag's order rule, which the naive reading inverts: the `=` binds to the
// PRECEDING letter and ends the token, whatever that letter's arity. `-iT=true`
// sets both booleans; `-wi feat` sets worktree="i" and leaves `feat` positional.
func TestRunTreatsAnEqualsInsideAClusterAsTheValue(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want string
	}{
		{[]string{"run", "api", "-iT=true", "go", "test"},
			"den run: den's flags go before the nest name — write `den run -iT=true api go test`"},
		{[]string{"run", "api", "-Ti=false", "go", "test"},
			"den run: den's flags go before the nest name — write `den run -Ti=false api go test`"},
		{[]string{"run", "api", "-wi", "feat", "go", "test"},
			"den run: den's flags go before the nest name — write `den run -wi feat api go test`"},
	} {
		t.Run(tc.argv[2], func(t *testing.T) {
			err := validateArgs(t, tc.argv...)
			if err == nil {
				t.Fatalf("%v must be refused", tc.argv)
			}
			if err.Error() != tc.want {
				t.Errorf("message = %q, want %q", err.Error(), tc.want)
			}
		})
	}
}

// The command's OWN flags pass through untouched — what SetInterspersed(false)
// buys, and the guard against a classifier that overreaches.
func TestRunPassesTheCommandsOwnFlagsThrough(t *testing.T) {
	if err := validateArgs(t, "run", "api", "go", "test", "-v", "-run", "TestX"); err != nil {
		t.Errorf("the command's own flags must pass through; got %v", err)
	}
}

// An unknown LETTER disqualifies the whole cluster, and `-Tv` reaches the VM.
// That is the measured status quo of `den exec`, and the only right answer:
// `-Tv` is not a legal den spelling, so den cannot lift it into a remedy.
func TestRunPassesUnknownClustersToTheSandbox(t *testing.T) {
	if err := validateArgs(t, "run", "api", "-Tv", "go", "build"); err != nil {
		t.Errorf("an unknown cluster must reach the sandbox; got %v", err)
	}
}

// MUST go through a real Execute(): --help is ABSENT from the walk under
// Find+ParseFlags and PRESENT under Execute(), because cobra adds it in
// InitDefaultHelpFlag. A test built on validateArgs is blind to this
// regression.
func TestRunPassesHelpToTheSandbox(t *testing.T) {
	home := denHomeSpawnable(t)
	f, d := fakeSpawnDeps()
	root := &cobra.Command{Use: "den", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newRunCmd(&home, d))
	if _, err := executeCmd(t, root, "run", "api", "mytool", "--help"); err != nil {
		t.Fatalf("--help past the nest name belongs to the command; got %v", err)
	}
	// Assert the Fake saw `mytool --help` reach the sandbox, in the idiom
	// TestExecPassesHelpToTheSandbox (exec_test.go) already uses.
	_ = f
}

// The persistent --den-home is in the derived table without being listed
// anywhere: left out, `den run api --den-home /tmp true` sent a program
// literally named `--den-home` into the sandbox.
func TestDerivedFlagSetSeesThePersistentDenHome(t *testing.T) {
	err := validateArgs(t, "run", "api", "--den-home", "/tmp", "true")
	if err == nil {
		t.Fatal("--den-home after the nest name must be refused")
	}
	const want = "den run: den's flags go before the nest name — " +
		"write `den run --den-home /tmp api true`"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}

// Two --repo read back off the FlagSet must come out as two PAIRS, in typing
// order. Value.String() renders a stringArray as "[/a,/b]", which reparses as
// one bogus path named exactly that — a syntactically legal remedy that is
// semantically false, so the replay below cannot catch it. SliceValue.GetSlice
// is the only correct read.
func TestRunRemediesCarryEveryRepoTypedBeforeTheNestName(t *testing.T) {
	err := validateArgs(t, "run", "--repo", "/b", "--repo", "/a", "api")
	if err == nil {
		t.Fatal("a run with no command must be refused")
	}
	const want = "den run: no command given — write `den run --repo /b --repo /a api go test`, " +
		"or `den up api` for a shell"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	if strings.Contains(err.Error(), "[/b,/a]") {
		t.Errorf("Value.String()'s slice form must never appear; got %q", err.Error())
	}
}

// The duplicate rule: the flag typed AFTER the name wins, because it is the last
// one typed and the one den is teaching the user to move.
func TestRunRemedyPrefersTheFlagTypedAfterTheName(t *testing.T) {
	err := validateArgs(t, "run", "--workdir", "/a", "api", "--workdir", "/b", "go", "test")
	if err == nil {
		t.Fatal("a flag after the nest name must be refused")
	}
	const want = "den run: den's flags go before the nest name — " +
		"write `den run --workdir /b api go test`"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}

func TestRunRemedyQuotesAValueContainingASpace(t *testing.T) {
	err := validateArgs(t, "run", "api", "--repo", "/tmp/hot fix", "go", "test")
	if err == nil {
		t.Fatal("a flag after the nest name must be refused")
	}
	const want = "den run: den's flags go before the nest name — " +
		"write `den run --repo '/tmp/hot fix' api go test`"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}

// The twin of slice 1's hard-won property: a remedy den proposes is itself
// accepted by den. Replayed through shellSplit, never strings.Fields.
func TestRunRemediesAreThemselvesLegal(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
		want string
	}{
		{"a flag after the name", []string{"run", "api", "-T", "go", "build"},
			"den run -T api go build"},
		{"a separator", []string{"run", "api", "--", "go", "build"},
			"den run api go build"},
		{"a leading separator", []string{"run", "--", "api", "go", "build"},
			"den run api go build"},
		{"a repo after the name", []string{"run", "api", "--repo", "/a", "go", "build"},
			"den run --repo /a api go build"},
		{"a repo with a space", []string{"run", "api", "--repo", "/tmp/hot fix", "go", "build"},
			"den run --repo '/tmp/hot fix' api go build"},
		{"a cluster with a value", []string{"run", "api", "-iTw", "feat", "go", "build"},
			"den run -iTw feat api go build"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateArgs(t, tc.argv...)
			if err == nil {
				t.Fatalf("%v must be refused", tc.argv)
			}
			got := remedyOf(t, err.Error())
			if got != tc.want {
				t.Errorf("remedy = %q, want %q (full message: %q)", got, tc.want, err.Error())
			}
			replay := shellSplit(t, got)[1:] // drop "den"
			if err := validateArgs(t, replay...); err != nil {
				t.Errorf("the remedy %q is refused in turn: %v", got, err)
			}
		})
	}
}

// StringArrayVar, not StringSliceVar: StringSlice splits on commas and a path
// may contain one. Invisible on reading; the observable is spawn.Options.Repos
// as the Fake receives it, so this goes through a real Execute().
func TestRepoFlagDoesNotSplitOnComma(t *testing.T) {
	home := denHomeSpawnable(t)
	f, d := fakeSpawnDeps()
	root := &cobra.Command{Use: "den", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newUpCmd(&home, d))
	if _, err := executeCmd(t, root, "up", "--repo", "/dev/a,b", "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Assert the Fake saw ONE mount, "/dev/a,b", never two. Use the same
	// f.Calls idiom the repo tests in up_test.go use.
	_ = f
}

// The persistent --den-home is still read LEFT of the subcommand, as it was
// under `den spawn`. The observable is the den home actually read, so this goes
// through a real Execute().
func TestUpStillReadsDenHomeBeforeTheSubcommand(t *testing.T) {
	home := denHomeSpawnable(t)
	f, d := fakeSpawnDeps()
	root := NewRootCmdWith(Deps{ /* wire deps.Sbx to f, mirroring root_deps_test.go */ })
	_ = root
	_ = f
	_ = d
	_ = home
	// `den --den-home <home> up api` must succeed against that home, and the
	// same line with a bogus --den-home must fail on it. Build it in the idiom
	// root_deps_test.go already uses for the persistent flag.
}
```

The three tests whose assertion body is left as a directive (`TestUpKeepsTheOrderOfRepeatedRepoFlags`, `TestRunPassesHelpToTheSandbox`, `TestRepoFlagDoesNotSplitOnComma`, `TestUpStillReadsDenHomeBeforeTheSubcommand`) must be completed against the helpers that already exist in this package — `f.Calls`, `f.HasPiped`, `fakeSpawnDeps`, `denHomeSpawnable`, `NewRootCmdWith`. Do not invent a new fake or a new helper; read the neighbouring test and match it.

Also add to `run_test.go`:

```go
// `den down` is a compose reflex, and cobra catches nothing on its own.
// SuggestFor on `rm` is what puts the real gesture in front of the user. The
// field must widen nothing else: `den dwon` still suggests nothing.
func TestDownSuggestsRm(t *testing.T) {
	got, err := run(t, "down", "api")
	if err == nil {
		t.Fatal("`den down` is not a command")
	}
	if !strings.Contains(err.Error(), "did you mean `den rm`?") {
		t.Errorf("`den down` must point at `den rm`; got %q", err.Error())
	}
	_, err = run(t, "dwon")
	if err == nil {
		t.Fatal("`den dwon` is not a command")
	}
	if strings.Contains(err.Error(), "did you mean") {
		t.Errorf("SuggestFor must widen nothing else; got %q", err.Error())
	}
}
```

- [ ] **Step 12: Run the whole suite**

Run: `task check`
Expected: PASS. If `unknown-command.golden` fails, compare column by column — the padding is cobra's own and must not have moved.

- [ ] **Step 13: Prove `den spawn` is gone from the binary's surface**

Run:
```bash
task build && ./den spawn api; ./den down api; ./den up; ./den run api
```
Expected, in order: the unknown-command listing carrying the new migration line; the listing carrying "did you mean `den rm`?"; `den up: a nest expected — usage: den up <nest> [flags]`; `den run: no command given — write `den run api go test`, or `den up api` for a shell`.

- [ ] **Step 14: Commit**

```bash
git add -A
git commit -m "feat!: den spawn becomes den up and den run

BREAKING CHANGE: \`den spawn\` is deleted with no alias, \`--\` leaves the
family, and ad-hoc repos move from positionals to a repeatable --repo.
\`den up <nest>\` creates-or-attaches then opens a shell; \`den run <nest>
<cmd>\` creates-or-attaches then runs the command. Two spellings of one door
is the failure mode #60 named, so the migration is a message, not a second
door. \`den rm\` gains SuggestFor \"down\"."
```

---

## Task 5: `den run` warns when the first command token is a directory

Spec §5 "Le second positionnel de `den run` — un avertissement, pas un refus".

**Files:**
- Modify: `internal/cli/run.go` (add `warnFirstCommandTokenIsADirectory`, call it from `RunE`)
- Test: `internal/cli/run_test.go`

**Interfaces:**
- Consumes: `execShape.addFlag`, `remedyLine`, `execRewrite` (Task 4).
- Produces: `func warnFirstCommandTokenIsADirectory(cmd *cobra.Command, args []string)`

### Why a warning and not a refusal

`den run api ~/dev/hotfix go test` hands the sandbox the command `~/dev/hotfix go test`, which fails inside. den cannot refuse it: `~/dev/hotfix` is a perfectly readable first command token, and "this is a program" is a legitimate reading. `den up` can name the migration because a second positional has NO other reading there; `den run` has one.

The `stat` is allowed, and the earlier draft that called it "the silent normalization §2 refuses" confused two gestures. A `stat` deciding the **behaviour** — mounting the path anyway, or stripping it from the command — is what §2 refuses. A `stat` deciding a **warning** is not: den runs the line exactly as typed, the token reaches the VM as the first word of the command, `sbx` receives it unchanged, the exit status is the command's, and den writes one more line on stderr. Spec 2026-07-27 §11 l.934 draws that line in so many words: §2 governs what den DOES with an ambiguous intention, not what it may LOOK AT. And den already `stat`s these paths — step 2 of `Spawn` loops `os.Stat` over every repo.

Nothing is injected. `internal/cli` already reads the filesystem outside `cli.Deps` (`rm.go:74`, `rm.go:597`, `reference.go:116`, `nest.go:103`, `exec.go:480-484`); `Deps` injects what touches the machine BEYOND the disk, and `hermeticity_test.go` forbids only `net`, `hash/fnv` and `os/exec`.

- [ ] **Step 1: Write the failing test**

Add to `internal/cli/run_test.go`:

```go
// The warning goes through a real Execute(): it comes out of RunE, not out of
// the validator — no validator in this repo writes to a stream, and one that did
// would staple advice under a line already refused for something else.
//
// The directory is a t.TempDir() and the test chdirs into it, because the
// normalization joins a relative path to the cwd.
func TestRunWarnsWhenTheFirstCommandTokenIsADirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.Mkdir(filepath.Join(dir, "hotfix"), 0o755); err != nil {
		t.Fatal(err)
	}
	home := denHomeSpawnable(t)
	_, d := fakeSpawnDeps()
	root := &cobra.Command{Use: "den", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newRunCmd(&home, d))
	_, stderr, err := executeCmdSeparateStreams(t, root, "run", "api", "hotfix", "go", "test")
	if err != nil {
		t.Fatalf("the line must RUN, not be refused: %v", err)
	}
	const want = "! hotfix is a directory on this host, and den is passing it to the sandbox as the " +
		"first word of the command — ad-hoc repos go behind `--repo` now — " +
		"write `den run --repo hotfix api go test`\n"
	if !strings.Contains(stderr, want) {
		t.Errorf("stderr must carry %q; got %q", want, stderr)
	}
}

// With no other command on the line, the remedy must name `den up`:
// `den run --repo hotfix api` would be refused in turn for "no command given".
// That is the dead-on-arrival remedy slice 1 paid for once.
func TestRunWarningNamesUpWhenTheLineCarriesNoOtherCommand(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.Mkdir(filepath.Join(dir, "hotfix"), 0o755); err != nil {
		t.Fatal(err)
	}
	home := denHomeSpawnable(t)
	_, d := fakeSpawnDeps()
	root := &cobra.Command{Use: "den", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newRunCmd(&home, d))
	_, stderr, err := executeCmdSeparateStreams(t, root, "run", "api", "hotfix")
	if err != nil {
		t.Fatalf("the line must RUN, not be refused: %v", err)
	}
	if !strings.Contains(stderr, "write `den up --repo hotfix api`") {
		t.Errorf("the remedy must name `den up`; got %q", stderr)
	}
}

// A file is not a directory: `den run api ./build.sh` is a legitimate command.
// And the false positive, priced honestly: a command whose first word is also
// the name of a directory in the cwd. In a repo carrying build/, `den run api
// build` prints one advisory line too many — argv, exit status and output
// unchanged. That is the whole cost.
func TestRunDoesNotWarnOnAFileOrAPlainWord(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "build.sh"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	home := denHomeSpawnable(t)
	_, d := fakeSpawnDeps()
	root := &cobra.Command{Use: "den", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newRunCmd(&home, d))
	for _, tok := range []string{"./build.sh", "go"} {
		t.Run(tok, func(t *testing.T) {
			_, stderr, err := executeCmdSeparateStreams(t, root, "run", "api", tok, "test")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if strings.Contains(stderr, "is a directory on this host") {
				t.Errorf("no warning is owed for %q; got %q", tok, stderr)
			}
		})
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/cli/ -run 'TestRunWarns|TestRunWarning|TestRunDoesNotWarn' -count=1 -v`
Expected: FAIL — no warning is printed.

- [ ] **Step 3: Implement the warning in `run.go`**

```go
// warnFirstCommandTokenIsADirectory advises, on stderr, when the first word of
// the command is a directory on this host — the shape a repo typed as a
// positional now takes.
//
// It WARNS and never refuses, and never changes what runs: the doctrine of
// 2026-08-04 at the closest precedent that exists — same feature, same command
// family — and the form reportUnmountedRepos ships (spawn.go: "Warn, never
// refuse, and never recreate"). The `!` prefix, one line, no consequence.
//
// It lives in RunE, never in the validator, for two reasons. After the
// validator, args[1] IS the first command token by construction — the line
// `den exec` already leans on. And no validator in this repo writes to a
// stream: the switch is first-defect-wins, so a printing validator would staple
// advice under a line already refused for something else. A verdict and an
// advisory leaving from the same place end up read as one.
//
// The NORMALIZATION is parseRepoArg's (internal/nest/repos.go), exactly, and
// nothing more: expand `~`, join a relative path to the cwd, Clean. NO
// filepath.Abs, NO EvalSymlinks — two reasonable implementations diverged on
// a quoted `~/repo`, on redundant components, and on a failing Getwd, so it is
// written out rather than described. Every failure is SILENT: ExpandPath fails,
// Getwd fails, stat fails whatever the reason, or it exists but is not a
// directory (`den run api ./build.sh` is a legitimate command).
//
// parseRepoArg's own entry refusals — an empty token, a `:ro` suffix — are NOT
// replayed: those refuse a REPO, and this token is not a repo yet. A `:ro` as
// the first word of a command goes to the VM like everything else, unadvised.
//
// NO git-ness test: a non-git directory was a perfectly legal ad-hoc repo
// (2026-08-04 decision 2 requires git only under -w), and that check — not the
// stat — is what would make this a second resolver.
//
// NO "looks like a path" prefilter (a leading /, ~ or .), although it would kill
// the false positive: parseRepoArg accepts a bare relative name, so
// `den run api hotfix` was a legal typing too, and the prefilter would miss it.
// A warning that fires on some spellings of an ad-hoc repo and not others is
// worse than one that occasionally fires for nothing.
//
// The proposed line carries the RAW token, as typed: proposing
// `--repo /Users/x/dev/hotfix` to someone who wrote `~/dev/hotfix` hands back a
// line they do not recognize, and parseRepoArg will redo the expansion anyway.
// The normalized path serves the stat and nothing else.
func warnFirstCommandTokenIsADirectory(cmd *cobra.Command, args []string) {
	raw := args[1]
	expanded, err := config.ExpandPath(raw)
	if err != nil {
		return
	}
	if !filepath.IsAbs(expanded) {
		cwd, err := os.Getwd()
		if err != nil {
			return
		}
		expanded = filepath.Join(cwd, expanded)
	}
	info, err := os.Stat(filepath.Clean(expanded))
	if err != nil || !info.IsDir() {
		return
	}
	// The remedy comes out of the SHARED builder, not a Sprintf: an advisory
	// proposing an illegal line costs the same round trip as a refusal that
	// does, and slice 1 paid it once. It enters TestRunRemediesAreThemselvesLegal
	// like every refusal.
	//
	// The target is `den up` when the line carries no OTHER command: with the
	// first token lifted into --repo there is nothing left to run, and
	// `den run --repo … api` would be refused in turn for "no command given".
	s := execRewrite(cmd, args).addFlag("repo", raw)
	target, tail := "den run", args[2:]
	if len(tail) == 0 {
		target = "den up"
	}
	// s.command was set by execRewrite from args[1:]; the shape is respelled
	// with the tail alone, which is what replacing the command means here.
	fmt.Fprintf(cmd.ErrOrStderr(),
		"! %s is a directory on this host, and den is passing it to the sandbox as the "+
			"first word of the command — ad-hoc repos go behind `--repo` now — write `%s`\n",
		raw, remedyLine(cmd, target, s, tail))
}
```

Call it from `newRunCmd`'s `RunE`, before `spawnNest`:

```go
			o.Nest = args[0]
			o.Command = args[1:]
			// Advisory only: the command runs exactly as typed, whatever this
			// prints. cmd.ErrOrStderr(), never stdout — `den run api go build |
			// tee log` must carry the command's output and nothing else, and
			// `den run` always has a command, so it has no interactive branch
			// handing stdout back to den.
			warnFirstCommandTokenIsADirectory(cmd, args)
			return spawnNest(cmd, denHome, o, deps)
```

Add `fmt`, `os`, `path/filepath` and `internal/config` to `run.go`'s imports.

**Note on `remedyLine` and `s.command`:** `execRewrite` fills `s.command` from the tail, and `remedyLine` never reads it — it appends the `command` PARAMETER (Task 2, step 4). So passing `tail` here replaces the command, which is the second gesture the inter-command remedies need. Do not "fix" the builder to read `s.command`; every caller in this plan passes the tail explicitly.

- [ ] **Step 4: Run them to verify they pass**

Run: `go test ./internal/cli/ -run 'TestRunWarns|TestRunWarning|TestRunDoesNotWarn' -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Add the advisory to the replay property**

In `TestRunRemediesAreThemselvesLegal` (`run_test.go`), add a second half that replays the ADVISORY line through `shellSplit` and `validateArgs`. The refusals go through `validateArgs`; the advisory comes out of a real `Execute()`, so it needs its own arm:

```go
	t.Run("the advisory line", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		if err := os.Mkdir(filepath.Join(dir, "hotfix"), 0o755); err != nil {
			t.Fatal(err)
		}
		home := denHomeSpawnable(t)
		_, d := fakeSpawnDeps()
		root := &cobra.Command{Use: "den", SilenceUsage: true, SilenceErrors: true}
		root.AddCommand(newRunCmd(&home, d))
		_, stderr, err := executeCmdSeparateStreams(t, root, "run", "api", "hotfix", "go", "test")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got := remedyOf(t, stderr)
		replay := shellSplit(t, got)[1:] // drop "den"
		if err := validateArgs(t, replay...); err != nil {
			t.Errorf("the advisory line %q is refused in turn: %v", got, err)
		}
	})
```

- [ ] **Step 6: Run the whole suite**

Run: `task check`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/cli/run.go internal/cli/run_test.go
git commit -m "feat(cli): den run warns when the first command token is a directory

den run api ~/dev/hotfix go test cannot be REFUSED — the token is a readable
first word of a command — so den runs the line as typed and writes one
advisory line, reportUnmountedRepos's doctrine and form. The proposed line
comes out of the shared builder and is replayed like every refusal; with no
other command on the line it names den up, since den run would be refused."
```

---

## Task 6: `den nest show` migrates to `--repo`

Spec §7bis "`den nest show` suit, sinon la césure du §4 est livrée".

**Files:**
- Modify: `internal/cli/nest.go` (`Use:` l. 131, `Args:` l. 133, delete `opts.Repos = args[1:]` l. 193, register the flag near l. 226-228, repoint the comments at l. 43, 90, 147, 164-165, 177, 190)
- Modify: `internal/nest/resolve.go` (l. 18-22, the doc of `Options.Repos`)
- Test: `internal/cli/nest_test.go`

**Interfaces:**
- Consumes: `upArgs` (Task 4).
- Produces: nothing new.

Leaving the dry-run on positionals ships exactly what §4 refused when it rejected the split — "an ad-hoc repo is spelled two ways between two sibling commands" — and the README shows both three lines apart. `nest.Options.Repos` is already `[]string`, so `StringArrayVar` binds without a type change, and the `if len(opts.Repos) > 0 { opts.Cwd, err = os.Getwd() }` guard reads the LENGTH, not the origin: it does not move.

**No `SetInterspersed(false)`**, and here it is not merely neutral but mandatory: three existing tests type the flag AFTER the nest name (`nest_test.go:79`, `:318`, `:358`), and under `SetInterspersed(false)` those lines would become positionals refused for their arity.

- [ ] **Step 1: Write the failing tests**

In `internal/cli/nest_test.go`, rewrite `TestNestShowResolvesCommandLineRepos` (l. 567) and `TestNestShowResolvesRelativeCommandLineRepos` (l. 591) to pass the repo behind `--repo` instead of as a positional — same assertions, same expected resolution. Then add:

```go
// The dry-run cannot be the one place the migration goes unnamed: it reuses
// `den up`'s validator, and the remedy names THIS command because it comes from
// cmd.CommandPath(), not from a hardcoded string.
func TestNestShowNamesTheRepoFlagOnASecondPositional(t *testing.T) {
	err := validateArgs(t, "nest", "show", "api", "/dev/hotfix")
	if err == nil {
		t.Fatal("a second positional must be refused")
	}
	const want = "den nest show: extra arguments — ad-hoc repos go behind --repo now — " +
		"write `den nest show --repo /dev/hotfix api`"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}

// Branch 2 fires on the dry-run too, and the remedy names `den run`. Accepted
// rather than special-cased: the user typed a command, and commands go to
// `den run`. Proposing `den nest show api` instead would drop `foo` in silence.
func TestNestShowSendsACommandToRun(t *testing.T) {
	err := validateArgs(t, "nest", "show", "api", "--", "foo")
	if err == nil {
		t.Fatal("a command after `--` must be refused")
	}
	const want = "den nest show: den nest show takes no command — write `den run api foo`"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/cli/ -run 'TestNestShow' -count=1 -v`
Expected: FAIL — `--repo` is an unknown flag, and the two new tests get the arity message.

- [ ] **Step 3: Migrate the command**

In `internal/cli/nest.go`, `newNestShowCmd`:

```go
		Use:   "show <nest>",
		Short: "Show a fully resolved nest",
		// upArgs, not exactlyOneArg: the latter would open a "too many
		// arguments" branch whose message — "exactly one argument expected, 2
		// received" — names neither --repo nor what changed. That is word for
		// word the grievance §5 raises against exactlyOneArg on `den up`, and the
		// dry-run cannot be the one place the migration goes unnamed.
		//
		// It costs NO parameter: a cobra validator already receives
		// *cobra.Command, hence cmd.CommandPath() ("den nest show") and
		// cmd.Flags() (Changed("repo"), and the derived table). The
		// inter-command TARGET is the remedy builder's need, not the
		// validator's — two distinct needs that must not be fused.
		Args:  upArgs,
```

Delete line 193 (`opts.Repos = args[1:]`) and keep the `len(opts.Repos) > 0` guard that follows. Register the flag beside the other three:

```go
	cmd.Flags().StringArrayVar(&opts.Repos, "repo", nil,
		"resolve as if this repository were mounted ad hoc (repeatable; the order you type is the order den mounts)")
```

**No `SetInterspersed(false)`** — see the paragraph above; three tests depend on it.

- [ ] **Step 4: Repoint the comments in `nest.go`**

Two of these promise message identity with functions that `up` AND `run` both reach through the shared body, so the truthful repointing names **both**:

| Line | Today | Becomes |
|---|---|---|
| 147 | "so `den nest show` and `den spawn` never resolve the SAME reference to two different nests" | `` `den nest show`, `den up` and `den run` `` |
| 164-165 | "a command `den spawn` would have rejected" (the P2 occurrence, split across a comment line break) | `` `den up` / `den run` `` |
| 177-178 | "must stay word-identical between `den nest show` and `den spawn` … or the two would resolve" | the three names, and "the two" becomes "the three" |
| 190 | "The dry-run of `den spawn <nest> [repo...]`" | `` The dry-run of `den up <nest>` / `den run <nest> <cmd>` `` |
| 43, 90 | `den spawn` in prose | `den up` / `den run` as the sentence requires |

- [ ] **Step 5: Fix the doc of `nest.Options.Repos`**

`internal/nest/resolve.go:18-22` says the repos are "given as positionals on the command line". False as of this task. Rewrite to name `--repo`, and keep the field `[]string` — `StringArrayVar` binds to it with no type change.

- [ ] **Step 6: Run the suite**

Run: `task check`
Expected: PASS, the three tests typing the flag after the nest name (`nest_test.go:79`, `:318`, `:358`) included. `internal/cli/testdata/` holds only `ports-*.golden` and `unknown-command.golden`, neither of which goes through `den nest show` — no golden moves.

- [ ] **Step 7: Commit**

```bash
git add internal/cli/nest.go internal/cli/nest_test.go internal/nest/resolve.go
git commit -m "feat(cli)!: den nest show takes ad-hoc repos behind --repo

BREAKING CHANGE: den nest show <nest> [repo...] becomes den nest show
[--repo <path>...] <nest>. Leaving the dry-run on positionals would ship the
split §4 rejected — one ad-hoc repo spelled two ways between sibling
commands. It reuses den up's validator, so the remedy names the migration
here too."
```

---

## Task 7: The documentation sweep

Spec §7 "Le balayage, reproductible", and the 11 user-facing output sites.

**Files:**
- Modify: `internal/cli/source.go` (l. 40), `internal/config/stack.go` (l. 192), `internal/spawn/interactive.go` (l. 19-20), `internal/spawn/spawn.go` (comments at 152, 157-160, 198), `internal/build/sandbox.go:15`, `internal/build/execute.go:302`, `internal/build/plan.go:176`
- Modify: `README.md`, `CHANGELOG.md`, `CLAUDE.md`
- Modify: `docs/superpowers/specs/2026-07-27-den-cli-design.md`
- Test: none new; `task check` must stay green

### The sweep is re-run, not trusted

The counts in the spec (P1 = 411, 209 to reread, 210 with `HANDOFF.md`) were measured **before** any code task. Do not copy them into the work. **Step 1 re-runs the four patterns and works from what they answer now.**

Four patterns, because one line-grep structurally cannot see three classes of occurrence in this repo: an occurrence split across a comment line break (P2), the argv token in tests (P3, which no search for "den spawn" finds), and `Use:`/`Short:`/identifiers (P4).

- [ ] **Step 1: Re-run the four patterns**

```bash
SPEC=docs/superpowers/specs/2026-08-16-up-run-command-design.md

# P1 — the written form, on one line, this spec excluded (it matches itself)
grep -rn --exclude-dir=.git --exclude-dir=worktrees -E 'den spawn|den <nest>' . | grep -v "$SPEC"

# P2 — occurrences CUT by a comment line break
grep -rn --exclude-dir=.git --exclude-dir=worktrees --include='*.go' -A1 -E '`den$' . \
  | grep -E '^\S+-[0-9]+-[[:space:]]*//[[:space:]]*spawn'

# P3 — the tests' argv token (none of which contains "den spawn")
grep -rn --exclude-dir=.git --exclude-dir=worktrees --include='*_test.go' -F '"spawn"' internal/cli

# P4 — Use:/Short:/identifiers, invisible to P1
grep -rn --exclude-dir=.git --exclude-dir=worktrees --include='*.go' \
  -E 'newSpawnCmd|spawnArgs|"spawn <nest>|Spawn or attach' .
```

Expected after Tasks 1-6: **P3 and P4 return nothing** — those are the ones that break `task check`, and Task 4 closed them. P1 and P2 still return the residue this task edits.

- [ ] **Step 2: The remaining production outputs**

Two, and both are user-facing strings rather than comments:

- `internal/cli/source.go:40`, `den source add`'s output: `den spawn %s:<nest>` → `den up %s:<nest>`.
- `internal/config/stack.go:192`, the "no `image:`" refusal: `` exact string, and `den spawn` looks for it there `` → `` `den up` ``.

- [ ] **Step 3: The Go comments that describe a deleted syntax**

Nothing breaks if these are left, and that is exactly why they must not be: they DESCRIBE a syntax that no longer exists, which is the dead documentation §6 corrects elsewhere.

- `internal/spawn/interactive.go:19-20` — a P2 occurrence, names `` `den spawn -- <cmd>` ``.
- `internal/spawn/spawn.go:152`, `157-160`, `198`.
- `internal/build/sandbox.go:15`, `internal/build/execute.go:302`, `internal/build/plan.go:176`.

`internal/sbx`, `internal/manifest`, `internal/policy`, `internal/worktree`, `internal/agent`, `internal/doctor`: comments only, no mandatory change — but re-read P1's output rather than trusting this list.

`internal/spawn/spawn_test.go`: comments only, plus the failure message at l. 4608. Those tests build `Options{Repos: …}` directly, so the field does not move and they survive intact.

- [ ] **Step 4: `README.md`**

Rewrite every line P1 returns for `README.md` (17 at the 2026-08-16 measurement — re-count from the grep): the command table (l. 81, 89), "Options of `den spawn`" (l. 98), `-w` (l. 115), instances (l. 129-130, 134), the ad-hoc repos block (l. 164-165, 168-172, 174), `den nest show` (l. 214), resuming after a stop (l. 221), `image:` (l. 397), build (l. 463, 474), sources (l. 492, 494, 542).

The ad-hoc repos block needs more than a rename: `den spawn scratch ~/dev/proj-*` mounted N repos in one keystroke, and a repeatable flag cannot take a glob — the shell expands before den sees anything, `--repo` binds the first match and the rest arrive as positionals (verified 2026-08-16; it is a property of the shell, not of pflag). Both replacement forms, verified the same day, go in the README:

```zsh
# zsh, parameter distribution — the `--repo=<val>` spelling is mandatory
repos=(~/dev/proj-*); den up --repo=${^repos} scratch
```

```bash
# portable, and the only one that survives a space in a path
repos=(); for d in ~/dev/proj-*; do repos+=(--repo "$d"); done
den up "${repos[@]}" scratch
```

- [ ] **Step 5: `CHANGELOG.md` and `CLAUDE.md`**

`CHANGELOG.md`: ONE entry under `Changed`, a break assumed with no deprecation window. The 9 existing occurrences are in **published** entries — historical, never rewritten.

`CLAUDE.md` l. 136-137: the note "`den <nest>` → `den spawn <nest>` on 2026-08-05" gains its third step. Keep it in French, like the paragraph around it.

- [ ] **Step 6: The living spec `2026-07-27-den-cli-design.md`**

Rewrite §2 (l. 61), §4.2 (l. 176), §5 (l. 218, 220, 234, 241), §6 (l. 248, 289, 343), §9.2 (l. 750, 752), §10 (l. 817), §11 (l. 926, 938), §12 (l. 984).

**§14 and §14.1 are NOT rewritten.** They are dated readings against a real `sbx`, and changing the command they quote would falsify a measurement.

- [ ] **Step 7: What is deliberately NOT rewritten**

Everything under `docs/superpowers/` except the living spec above and `HANDOFF.md`: plans, dated handoffs, decisions, dated specs. They are historical by the convention CLAUDE.md states, and each is correct **at its own date**.

The one exception inside that bucket, easy to miss: **`docs/superpowers/handoffs/HANDOFF.md` is NOT dated.** CLAUDE.md calls it living and rewritten — the dated handoffs beside it are the historical ones. Its line is rewritten. Counting by directory would have missed exactly that line.

- [ ] **Step 8: Verify the residue**

Re-run P1 and check every remaining hit is in the historical bucket. Then:

```bash
task check
```
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add -A
git commit -m "docs: den spawn becomes den up / den run across the tree

The two remaining production outputs (den source add, the no-image: refusal),
the comments in spawn/ and build/ that describe a deleted syntax, README's
17 lines including the glob workarounds, CHANGELOG, CLAUDE.md, and the living
2026-07-27 spec — except §14/§14.1, which are dated readings against a real
sbx and would be falsified by a rename. Dated docs under docs/superpowers/ are
historical and untouched; HANDOFF.md is not dated and is rewritten."
```

---

## Self-Review

**Spec coverage.** §1 problem → context only. §2 measurements → Task 1 (c, d, e), Task 4 (c.1 order test, c.2 comma test, c.3 den-home test, f), Task 2 (g/F), Task 3 (g/G). §3 names → Task 4 (no alias, `internal/spawn` keeps its name — no task renames it). §4 contract → Task 4 (`up`/`run`, `--repo`, no `--`); the four-doors table is documentation, not code, and no task deletes `shell` or `exec`. §5 flag matrix → Task 4 (`registerSpawnFlags`, the two registered-and-refused flags, `upArgs`'s four branches, `enterArgs`), Task 5 (the warning), Tasks 1-3 (the derived table, the classifier, F, G). §6 contradictions and migration → Task 4 (the two `spawn.go` strings, the migration line). §7 scope → Tasks 4, 6, 7 (`spawnNest` boundary in Task 4; the sweep in Task 7; `nest show` in Task 6). §8 tests → distributed; the harness rule (`validateArgs` for refusals, `executeCmdSeparateStreams` for anything crossing `RunE`) is applied per test; the three-commit boundary is Tasks 1-3. §9 divergences → assumed, no code. §10 out of scope → nothing built.

**Gaps found and closed while writing:** `Use:`/`Short:` for `up` and `run` are nowhere in the spec — pinned in Task 4 and quoted again in the golden. The help text for the two registered-and-refused flags is likewise absent — written in Task 4. `upArgs`'s `--` branch reached from `den nest show` is unstated in the spec — accepted in Task 6 with a why-comment and a test row. `atLeastOneArg`'s death takes the `math` import with it (measured: no other user) — Task 4 step 5.

**Placeholders.** Four assertion bodies in Task 4 step 11 are directives rather than code (`TestUpKeepsTheOrderOfRepeatedRepoFlags`, `TestRunPassesHelpToTheSandbox`, `TestRepoFlagDoesNotSplitOnComma`, `TestUpStillReadsDenHomeBeforeTheSubcommand`). That is deliberate and named at the call site: they assert against `sbx.Fake`'s recorded calls through helpers this plan does not own, and inventing a shape for them would be worse than pointing at the neighbouring test that already has one. Everything else is literal.

**Type consistency.** `execShape` gains `lifted map[string]bool` in Task 1 and it is initialized in `execRewrite`, so Task 2's `readBackFlags(cmd, s.lifted)` never reads a nil map. `execLine(path, s, command)` (Task 1) becomes `remedyLine(source, target, s, command)` (Task 2) at one moment, with all five `exec.go` call sites updated in the same step. `classifyToken` returns four values from Task 1 onward and its `names` result is unused until Task 2 — flagged there rather than added later, so the signature never changes twice. `addFlag` is a value method returning a new shape; both callers (Task 4 branch 4, Task 5) reassign.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-08-16-up-run-command.md`. Two execution options:

1. **Subagent-Driven (recommended)** — a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — tasks executed in this session with checkpoints for review.
