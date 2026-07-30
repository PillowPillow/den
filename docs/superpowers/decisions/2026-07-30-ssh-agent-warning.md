# Decisions on the empty-SSH-agent warning (PR #3)

What the review and the macOS verification
(`../handoffs/2026-07-30-macos-verification-plan2.md`) left open, and what was
decided. One section per question, each stating the decision, the argument, and
what it costs.

---

## 1. `den sh` warns too — DECIDED, implemented

**Decision**: `den sh <name>` emits the empty-agent warning, reading the den
home for exactly one field (`ssh.mode`) and swallowing every failure to read it.

**Why**: the argument that put the warning on `den <nest>`'s attach branch holds
here word for word. The forwarded socket is a live proxy — measured on a running
VM (handoff §6, propagation in both directions without a respawn) — so a
sandbox re-entered after its agent was emptied is denied `publickey` just as
silently. `den sh` is also the cheap re-entry: a sandbox is created once and
re-entered daily, so the surface that warned was the rarer one.

**What it costs**: the invariant in `sh.go` moves from "no den home is read" to
"the den home is read, and never allowed to matter". Concretely — a den home
den cannot read, or a `config.yaml` that fails validation, silences the warning
and nothing else; no error, no missing shell
(`TestShOpensTheShellWhenTheDenHomeCannotBeRead`). `LoadGlobal`, not
`LoadGlobalUnvalidated`: the unvalidated loader stays reserved for `den doctor`,
and the consequence — an unrelated inconsistency in `config.yaml` silences this
warning — is accepted, because `doctor` is the surface that names both.

**The one deliberate divergence**: an ABSENT `SSH_AUTH_SOCK` is silent on
`den sh`, where `den <nest>` warns. A live sandbox forwards the socket it
inherited at its `sbx create`, from an environment that may be long gone, so
this shell's lack of one says nothing about the VM — and the preflight's remedy
("relaunch den, which forwards the socket at creation time") names a step
`den sh` does not have. The probe is skipped in that case too: `ssh-add -l` with
no socket answers `StateUnreachable`, whose message blames a variable the user
never set.

**Known approximation, accepted**: the probe interrogates the agent of the shell
running `den sh`, which is the same one on a stable per-user socket (macOS
launchd) and can differ from a per-shell `eval $(ssh-agent)` on Linux. Identical
to the approximation `den <nest>`'s attach branch already makes. Being wrong
costs one advisory line suggesting a harmless `ssh-add`; staying silent costs
the failure this whole PR exists to name.

## 2. macOS before 12 (`-K` instead of `--apple-use-keychain`) — DECIDED: ignore

**Decision**: no OS-version detection. `sshagent.FixCommand` keeps one darwin
form, `ssh-add --apple-use-keychain`.

**Why**: the full argument lives in `FixCommand`'s godoc, where the string is
chosen and where it cannot rot away from the code. In short — den prints a hint,
it does not run it; on macOS 11 and earlier the flag costs an "unknown option"
plus ssh-add's own usage line, which names the flag that machine wants; and
paying an `sw_vers` probe on every warning to spare that, for releases Apple no
longer updates, is the worse trade.

**Not verified, and cannot be here**: the verification machine is macOS 26.5.2,
far past the rename. Old-macOS behaviour was not measurable, which is why this
is filed as a decision rather than a verification.

## 3. Keys that are not default-named — DECIDED: widen the wording

**Decision**: every warning that quotes a load command now also names what that
command does not load. The sentence is one constant, `sshagent.KeyNameCaveat`,
appended to all six messages (three in `spawn`, three in `doctor`):

> note: only default-named keys (~/.ssh/id_*) are loaded, so a key named
> otherwise has to be passed explicitly (`ssh-add ~/.ssh/<key>`)

**The gap it answers**: bare `ssh-add` (and its keychain variant) load the
default `~/.ssh/id_*` names only. On a host whose real keys are named otherwise,
the remedy exits 0, `ssh-add -l` reports an identity, `den doctor` prints `[ok]`,
and `git push` from the sandbox stays denied. On the verification machine this
is not the tail case but the dominant one: the only default-named key is an
`id_rsa` nothing uses, the keys carrying real traffic are all named otherwise,
and the key in the live agent matches no `.pub` in `~/.ssh` at all (handoff §2).

**Not a regression of this PR** — the pre-existing `den doctor` had the same
blind spot — but the PR is what starts telling users to run the command, so it
is the PR that owes them the limit.

**A constant, not six sentences**: a caveat pasted six times gets worded six
ways, and both test suites assert the SYMBOL, so a message that drops it fails
instead of quietly reverting to the old promise
(`TestRunNamesTheNonDefaultKeyCaveatWhereverItQuotesAFix`,
`TestWarnEmptySSHAgentNamesTheNonDefaultKeyCaveatOnEveryBranch`).

**Rejected: having den find the names** — reading `~/.ssh/config`'s
`IdentityFile` entries, or globbing `~/.ssh/*.pub`. Genuinely more useful,
genuinely more surface: a new file format to parse, new failure modes, and on
the verification machine even that would have missed the live agent's key, which
has no `.pub` at all. den names the limit; it does not guess the file.

---

## Out of scope here, still open

- The PR #3 description links a spec file
  (`docs/superpowers/specs/2026-07-30-ssh-agent-vide-warn-design.md`, on branch
  `feat/warn-agent-ssh-vide`) that exists on no branch, and its "Changes"
  section still describes `FixCommand` as returning
  `ssh-add --apple-use-keychain ~/.ssh/` — the form F1 removed after measuring
  that it reads a directory as a key file and loads nothing. Editing a PR
  description is not a code change; left to the author.
- The French spec/plan documents under `docs/superpowers/` still quote Go test
  names as they were before commit 8c1841f renamed the suite to English. A
  documentation-wide rename, not part of this PR's diff.
