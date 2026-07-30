# macOS verification of PR #3 (branch `claude/issue-2-cqe7m6`, commit 2a82488)

Everything the Linux sandbox could not run has now been run on real hardware.

**Machine under test**: macOS 26.5.2 (build 25F84), `OpenSSH_10.2p1, LibreSSL 3.3.6`,
Apple `/usr/bin/ssh-add`, `git version 2.50.1 (Apple Git-155)`, `sbx` v0.35.0.

**Method note**: the user's live launchd agent was never wiped. `ssh-add -D` was
not run against it at any point. All empty/one-key/two-key states were staged in
a throwaway `ssh-agent -a /tmp/den-probe.sock`, and `den doctor` was driven with
`--den-home` pointing at a scratch directory, so `~/.den` was never touched. The
one test that required the real agent (§6, propagation into a live VM) added a
single key and removed it again; the agent's before and after listings are
identical and are recorded below.

---

## §1 — `-race`: clean

```
CGO_ENABLED=1 go test -race -count=1 ./...
Go test: 704 passed in 11 packages
```

No data race, no failure. The 50 ms injected bound in the `sshagent` timeout test
did not flake; re-run under `-count=3 -race` for good measure:

```
ok  github.com/PillowPillow/den/internal/sshagent  6.531s
```

The bound stands as written. Nothing to raise.

## §2 — F1: the central hypothesis holds, with one caveat that must travel with it

`ssh-add --apple-use-keychain`, no argument, in an empty agent:

```
Identity added: /Users/polochon/.ssh/id_rsa (/Users/polochon/.ssh/id_rsa)
ssh-add -l  →  exit 0, 1 identity
```

The flag loads the default `~/.ssh` keys. F1 is correct.

Made airtight by an A/B in the same agent, which is the measurement that
discriminates "the flag preserves the default-file list" from "the flag happened
to load something":

| form | files loaded |
|---|---|
| `ssh-add` (Linux branch) | `/Users/polochon/.ssh/id_rsa` |
| `ssh-add --apple-use-keychain` (darwin branch) | `/Users/polochon/.ssh/id_rsa` |

Identical. `--apple-use-keychain` changes *passphrase storage*, not the
default-file list. The two branches of `FixCommand` are behaviourally the same
command plus a keychain flag, which is exactly what the godoc at
`sshagent.go:221-226` claims.

**The caveat.** On this machine the only default-named key is `~/.ssh/id_rsa`
(2048-bit RSA, comment `Polochon@DESKTOP-UAPJ1TD`). The keys that carry real
traffic are not default-named:

- `~/.ssh/digitaleo_id_ed25519` — `odep*`/`bweb*`/`oweb*` hosts
- `~/.ssh/id_ed25519_liftia` — `github-liftia`
- `sbx-devbox@PillowMacBook`, `SHA256:XEx6gCH0HfxTCX+PM5xDfOmyKQ/bEiB0rUgu4R9ElkI`
  — the only key in the live agent, and it matches **no** `.pub` file in `~/.ssh`

So on this box F1 passes (exit 0, "1 identity") while loading a key that is not
the one most pushes need. The handoff files the non-standard-name gap under
"pre-existing, merits its own decision"; on this configuration it is not a tail
case, it is the dominant one. A user here is told to run a command that succeeds,
reports an identity, and can still leave `git push` broken.

This does **not** invalidate F1 — the remedy remains strictly better than the
proven-broken `ssh-add --apple-use-keychain ~/.ssh/`. It means "§2 verified" must
never ship without this paragraph attached.

**macOS version reserve**: not closed by measurement. macOS 26.5.2 is far past the
Monterey rename, so `--apple-use-keychain` vs `-K` is moot *here*; old-macOS
behaviour cannot be measured on this box. It stays a decision, not a verification.

## §3 — ssh-add exit codes on Apple OpenSSH 10.2: the godoc sentence is now measured

The claim at `sshagent.go:67-74` was asserted, never measured. Measured now:

| situation | exit | maps to |
|---|---|---|
| agent reachable, empty | **1** | `StateEmpty` |
| `SSH_AUTH_SOCK=/nonexistent/x.sock` | **2** | `StateUnreachable` |
| `SSH_AUTH_SOCK` unset | **2** | `StateUnreachable` |
| 1 identity | **0** | `StateKeys` |
| 2 identities | **0** | `StateKeys` |

Every code lands where the three-state model expects. `StateEmpty` is reachable
on macOS — the failure mode feared in §3 (exit 1 not being produced, making den
call a healthy agent a dead socket) does not occur. F13's count-based guard is
belt-and-braces here, not load-bearing.

## §5 — `den doctor`, all four states on real darwin

Driven against a scratch den home with `ssh.mode: agent-forward`. These came from
**two separate invocation batches**, not one sequence — recorded here as they were
actually produced, batch by batch, rather than assembled into a tidy block.

Batch A — temp agent holding `id_rsa` + `id_ed25519_liftia`, then the two
degraded environments:

```
[ok  ] ssh.mode  agent-forward, SSH_AUTH_SOCK=/tmp/den-probe.sock (2 identities)

[warn] ssh.mode  agent-forward, but SSH_AUTH_SOCK=/nonexistent/x.sock points at an
       unreachable agent (dead socket, no agent running, or ssh-add absent from
       PATH): … load a key with `ssh-add --apple-use-keychain`, or set `ssh.mode`
       to "mount" in …

[warn] ssh.mode  agent-forward, but SSH_AUTH_SOCK is absent or empty in den's
       environment: … start an agent (`eval $(ssh-agent)` then `ssh-add`), or set
       `ssh.mode` to "mount" in …
```

Batch B — same temp socket, emptied with `ssh-add -D`, then a single key re-added:

```
[warn] ssh.mode  agent-forward, but the agent at SSH_AUTH_SOCK=/tmp/den-probe.sock
       holds no identity: sandboxes inherit an empty agent and are denied SSH
       access (publickey), so `git push` fails from the VM far from the cause —
       load a key with `ssh-add --apple-use-keychain`

[ok  ] ssh.mode  agent-forward, SSH_AUTH_SOCK=/tmp/den-probe.sock (1 identity)
```

All four states match the handoff table. **F11's GOOS injection reaches `SystemDeps`**:
the printed remedy is the darwin form, `ssh-add --apple-use-keychain`, not bare
`ssh-add`. `doctor.go:90` needs no attention.

Singular/plural is correct: `(1 identity)` / `(2 identities)`.

### New finding, not in the handoff

`doctor.go:288-296`, the `socket == ""` branch, hardcodes the bare form in its
remedy text — ``start an agent (`eval $(ssh-agent)` then `ssh-add`)`` — while the
`StateEmpty` and `StateUnreachable` branches both call
`sshagent.FixCommand(d.goos())`. On darwin, one of three warning branches prints
the non-keychain form. Low severity: bare `ssh-add` does load the same default
files (measured in §2), it just prompts for a passphrase instead of reading the
keychain. But it is the same OS-branching inconsistency F1 and F11 exist to
remove, surviving in the one branch nobody templated. Deliberately not fixed —
outside this handoff's scope, and it deserves its own finding rather than a quiet
patch.

## §6 — `den <nest>` on macOS

Run against the live sandbox `den.review-sshagent-issue` (nest `den`, den home
`~/.den-dogfood`) with an empty agent, streams captured separately:

```
stderr: warning: ssh.mode agent-forward, but the forwarded SSH agent holds no
        identity — this sandbox is denied SSH access (publickey) and `git push`
        fails from inside it; run `ssh-add --apple-use-keychain` on the host (the
        forwarded socket is a live proxy, so the key takes effect without
        respawning den)

stdout `warning:` count = 0
stderr `warning:` count = 1
```

The remedy is the darwin form, and the warning goes to stderr.

**But this is weaker than "F14's stream discipline holds", and the doc must not
claim more.** This run died at step 3 (worktree guard) before reaching the fork,
so stdout was *entirely empty* — it never received the spawn's own log either.
The grep therefore cannot distinguish "warnings are correctly routed away from
stdout" from "stdout was never written to at all". What is proven: the empty-agent
warning is emitted, on stderr, on darwin, with the darwin remedy. What is **not**
proven on this machine: that during a *completed* attach — where stdout does carry
`sandbox … already live: attaching` — no `warning:` leaks into it.

The unit tests do not close that gap either. `TestSpawnWarnsOnTheAttachBranchWhenTheForwardedAgentIsEmpty`
(`spawn_test.go:1613`) does assert a completed attach, but captures only `errBuf`;
it never inspects stdout. The one stdout assertion,
`spawn_test.go:1717`, is in `TestSpawnDoesNotWarnWhenTheForwardedAgentHasKeys` —
the has-keys case, where no warning exists to leak. So "no warning on stdout while
a warning is being emitted and stdout is in use" is asserted by nothing, in the
suite or here. It is cheap to add and worth its own finding.

**On the create/attach question**: the two branches need not be tested
separately. `warnEmptySSHAgent` is step **2bis** of the preflight
(`spawn.go:218`), before worktrees (step 3) and before the spawn-or-attach fork
(step 6). It fires on the path common to both branches, by construction. The run
above proves it empirically: the warning was emitted, and only *then* did den
fail on an unrelated worktree/branch guard at step 3.

### New finding: `den sh` never warns at all

`warnEmptySSHAgent` has exactly one call site — `spawn.go:225`, inside the
`den <nest>` preflight. `den sh <name>` does not go through it: `sh.go:40` calls
`spawn.Attach` directly. So re-entering a sandbox with `den sh` emits **no**
empty-agent warning, ever, on any OS.

`spawn.go:186-189` already documents that `den sh` "skips all of this: it only
calls spawn.Attach and reads neither config nor kits" — so this is a known
property for config and kits. Whether it was *intended* for the agent warning is
a separate question, and the argument that put the warning on the attach branch
in the first place applies here verbatim: the forwarded socket is a live proxy,
so an empty agent is just as true, and just as silent, when re-entering via
`den sh`. It is also a frequent re-entry surface. Reported, not patched — the
call site is one line, but whether `den sh` should read config at all is a design
question this handoff has no mandate to settle.

Incidental: that guard fired because
`~/.den-dogfood/worktrees/review-sshagent-issue/den` is currently on branch
`greatfix/sshagent-review`, not `review-sshagent-issue`. Pre-existing state of the
dogfood tree, unrelated to this PR, left alone.

### The `spawn.go:480` claim — proven, and it needed the real VM

"the forwarded socket is a live proxy, so the key takes effect without respawning
den" is printed to users and had no test. Measured against the running sandbox:

```
host agent   : sbx-devbox…XEx6g
VM agent     : sbx-devbox…XEx6g          ← same fingerprint, live, not a snapshot

ssh-add ~/.ssh/id_ed25519_liftia   (on host, VM already running, no respawn)
VM agent     : sbx-devbox…XEx6g
               polochon@…    …8Xclh      ← appeared inside the running VM

ssh-add -d ~/.ssh/id_ed25519_liftia
host agent   : sbx-devbox…XEx6g          ← identical to the starting state
VM agent     : sbx-devbox…XEx6g          ← removal propagated too
```

The sentence den prints is true. Propagation works in both directions without
recreating the sandbox.

## §7 — timeout under the macOS scheduler

Per the handoff, trusted to the test rather than reproduced by hand.
`go test -race -count=3 ./internal/sshagent/` passes; the `WaitDelay` /
orphan-descendant test does not flake on darwin.

---

## What remains unverified

- **Old macOS** (< 12, `-K` instead of `--apple-use-keychain`). Cannot be measured
  on this box. Still a decision: ignore, or branch on OS version.
- **Non-standard key names.** Measured as a real gap on this machine (§2), not
  fixed. Needs its own decision.
- **A completed `sbx create`, and a completed attach, were not run.** No new VM
  was spawned: the warning provably precedes the create/attach fork, so a create
  would have been a heavyweight side effect for no additional signal about the
  warning itself. The cost is the stdout-cleanliness gap described in §6 — the one
  claim in this document that rests on structure and a partial run rather than on
  a full end-to-end observation.
- **`den sh`'s missing warning** (§6) is reported, not fixed, and has no test.
- The three items the handoff already listed as out of scope (PR description
  pointing at a non-existent spec file, the stale French test name quoted in the
  spec at line 62, and `spawn.go`'s missing `default:` arm) are untouched.

## State at handoff

Nothing in the repository was modified. No commit, no push. The working tree
carries only the pre-existing untracked handoff/plan docs plus this file. The
user's `~/.den`, `~/.den-dogfood`, `~/.ssh` and live launchd agent are all in the
state they started in; the throwaway agent and the temporary `den` binary were
removed and no `ssh-agent` process of mine survives.
