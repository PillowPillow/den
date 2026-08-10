# What `mifunedev/openharness` does that den does not — and what den should take (2026-08-10)

Written after reading the openharness tree at `main` on 2026-08-10: `README.md`,
`harness.yaml.example`, `.oh/docs/roadmap.md`, `.oh/docs/oh-directory-layout.md`,
`.oh/docs/harness-manifest.md`, `.oh/docs/security-considerations.md`, `.oh/crons/heartbeat.md`,
`.oh/crons/autopilot.md`, `.oh/skills.lock`. Every claim about openharness below is quoted from
those files (primary source), not from a summary.

den's side is read from `README.md`, the command table, the open issues (#50, #56, #57, #58) and
`docs/superpowers/handoffs/HANDOFF.md` §8.

---

## 1. The two products are not the same product

| | den | openharness |
|---|---|---|
| Isolation unit | `sbx` microVM, one per nest+worktree | Docker container, one per project |
| Identity | the sandbox name `<nest>[.<worktree>]` | the compose project name (`sandbox.name`) |
| Config | cascade global ← stack ← nest ← flags, strict YAML, refuses on unknown key | one flat `harness.yaml`, parsed by `awk` — "flat two-level section/key only … no yq" |
| Agent | a registry (`--agent`, `config_dir`, `env:`, `update:`), one shared profile per agent | 4–9 CLIs baked into the image, one dotdir each (`.claude`, `.codex`, `.pi`, `.hermes`) |
| Sharing | team sources (git repos of stacks/nests), validated by one linter | "clone-and-own": fork the whole harness, keep `upstream` as a remote |
| Stance | interactive-first; the README states the blast radius is "enough to let the agent run unprompted, **not enough to treat it as unattended**" | lights-out: cron + Slack + remote VM, "an unattended software factory" |

openharness's own roadmap (`.oh/docs/roadmap.md`) is **not** about sandboxes. It is a
primitive-taxonomy migration — collapsing skills / agents / hooks / rules / identity into three
portable primitives. That is prompt-pack organization. den mounts a profile directory and lets the
agent own its content; that whole problem is outside den's scope, and their unsolved problems are
not den's.

So the harvest below is narrow on purpose. Most of openharness is content den deliberately does not
own.

---

## 2. The one idea that is a doctrine change, not a feature

openharness's cron format, verbatim from `.oh/crons/autopilot.md`:

```yaml
---
id: autopilot
schedule: "5 * * * *"
timezone: America/Denver
enabled: false
overlap: false
catchup: false
tmux: true
worktree: true
agent: pi
preflight: .oh/skills/autopilot/autopilot-caps.sh
repo: mifunedev/openharness
description: Hourly autopilot — issue-queue-first harness-infra improvements in an isolated-worktree Pi tmux Advisor session
---
```

Five design points in that header are worth more than the feature itself:

1. `worktree: true` — the runtime creates an isolated worktree per fire (`$CRON_WORKTREE`), "the
   shared root checkout is never touched, so the run can never dirty it or be skipped for overlap".
2. `overlap: false` / `catchup: false` — a missed fire is not replayed, a running fire is not
   doubled.
3. `preflight:` — a **deterministic shell gate** that runs before the agent is spawned. Caps are
   enforced by that script ("logs `SKIPPED-CAP-*` … and spawns no session on a capped hour"), not by
   asking the model to behave.
4. Explicit caps: "at most 6 open `autopilot` PRs created per UTC day AND 10 total open at any
   time … **Never auto-merge**."
5. `enabled: false` by default in the tracked file.

den cannot adopt this as "a scheduler feature". The README takes the opposite position in writing,
and for stated reasons: repos are mounted read-write from the host at the same absolute path,
`ssh.mode: agent-forward` puts the user's push access inside the VM, and `egress:` **widens** the
host policy and cannot narrow it. Every one of those is fine while a human is at the prompt and is a
different object when nobody is.

The choice is binary and must be made before anything is planned:

- **(A) Keep interactive-first.** Then crons, the Slack gateway and the tunnel all drop, and this
  document's roadmap is section 4 only.
- **(B) Take the unattended position.** Then it is bought with guardrails, enumerated up front:
  - a scheduled fire runs in its own worktree, never a shared checkout (`-w` already gives den
    exactly this, and the sandbox name already carries it);
  - a scheduled fire does not hold the user's push access — `ssh.mode` must have a third value
    (none / a scoped token) and the fire must refuse to start under `agent-forward`;
  - a deterministic preflight gate, in den (Go), not in a prompt;
  - caps recorded in `state/`, never auto-merge, never push to a protected branch;
  - a journal per fire that `den doctor` can read back.
  - `egress:` narrows nothing — so the guard is at the agent and at git, not at the network. Say it
    in the doc rather than let a reader assume the microVM covers it.

Recommendation: **(B), but sequenced** — the tracer bullet in §4 is useful under (A) too, so it can
be built before the decision is taken. Do not build the scheduler before the decision.

---

## 3. Where each borrowed idea lands against den's locked invariants

Any item below that touches time, sockets or processes has one landing zone, because
`internal/ports/hermeticity_test.go` locks the import graph: `internal/cli` imports none of `net`,
`hash/fnv`, `os/exec`, and `internal/spawn` never imports `internal/ports`. A scheduler, a tunnel or
a messenger bridge is therefore **a new package behind a `cli.Deps` interface**, exactly like
`Scanner`, `Open` and `SSHAgent`. Two more constraints apply to every item: a new config block is a
strict-YAML decoder change in the cascade (`KnownFields(true)`, not an additive comment), and
anything holding per-sandbox metadata keys off the sandbox name or the manifest, because `sbx
create` has no `--label`.

---

## 4. Adopt — no doctrine change needed

### 4.1 `den exec <name> -- <cmd>` (the tracer bullet)

den has `den sh` (interactive) and nothing else. openharness's whole automation surface rests on
being able to fire a command into the sandbox non-interactively. `den exec` is neutral on its own —
it serves CI, scripts and a human in a hurry — and it is the single primitive every phase-3 item
needs. Build it first regardless of the §2 decision.

Landing: `internal/sbx` already owns process invocation; `cli` calls the shared runner. No new
dependency.

### 4.2 Agent registry entries beyond Claude

den already has the right mechanism (`--agent`, `agents.<name>.config_dir`, `env:`, `update:`);
openharness proves the demand — it ships Claude Code, Codex, Pi, Hermes, OpenCode, DeepAgents, Grok.
den ships one populated entry. Adding `codex` and `pi` entries is config plus docs, and it is what
open issue **#50** ("should a spawn mount all profiles, and what does `--agent` then mean?") is
really about: with one agent the question is theoretical, with three it is forced.

Note the confirmation in openharness's favour of den's existing choice: it persists provider auth in
Docker **named volumes** so a rebuild does not force a re-login. den's mounted profile directory,
untouched by `den rm`, already is that — no work needed.

### 4.3 Guard hooks in the shipped agent profile

The README recommends `"defaultMode": "bypassPermissions"` and `"sandbox": {"enabled": false}` in
`~/.den/agents/claude/settings.json`, and is honest about the trade. openharness runs the same
configuration and then re-asserts the line with `PreToolUse` hooks, on the stated grounds that
"deny-list rules alone are skipped under bypass mode, so the hooks re-assert them" — plus a
destructive-command guard that parses the shell semantically rather than by regex.

den ships an agent profile scaffold via `den init`. Shipping two small guards in it
(secret-path reads, bulk env dumps) costs little and closes the honesty gap the README currently
leaves open. Copy their framing too, not just the scripts: their security page labels every line
**ENFORCED** (a mechanism holds) or **RECOMMENDED** (doctrine only), and states plainly that
pattern-based guards "are not a complete defense against an adversary". That is den's register.

### 4.4 Pin a team source

`.oh/skills.lock` pins each vendored skill by `source`, `commit` and `sha256` checksum. den's
sources are floating clones: `den source update` fast-forwards, and nothing lets a team say "this
machine is on this commit" or notices that two machines are not.

Minimum viable version, no lockfile format needed at first: `den source ls` already prints HEAD —
add a `pin:` to the source record and make `den source update` refuse to move past it without
`--to`. Owner directory: `state/` (it records what this machine installed; it is not a cache and is
never purged).

### 4.5 Split the README

`README.md` is 32 KB and now covers install, spawn, ports, egress, agent profile, freshness, build,
sources and lint. openharness moved its docs site out to a separate repo and kept a short README
plus a GitHub-readable `docs/` index with one line per page. den does not need a site; it needs the
index. Low risk, and it is the cheapest thing on this list.

---

## 5. Adopt with a probe first — the attach surface

openharness publishes sshd into the container behind one config block:

```yaml
ssh:
  # enabled: false             # SANDBOX_SSH — run sshd for direct container SSH
  # port: 2222                 # SANDBOX_SSH_PORT — host loopback port published for SSH
  # password_auth: false       # SANDBOX_SSH_PASSWORD_AUTH — allow password login
```

The point is not SSH, it is **attaching an editor** (their docs list VS Code / Remote-SSH as first-
class). den's only door is `den sh`. This sits directly next to open issues **#57** (`ssh.mode:
mount` — does the stock image ship a non-empty `/home/agent/.ssh`?) and **#58** (a host absent from
`known_hosts` blocks every non-interactive caller in the VM) — and #58 is a hard blocker for
anything unattended, since a scheduled fire is by definition a non-interactive caller.

Do not plan this from the README. It needs a real `sbx` smoke first (can a sandbox publish a port
back to the host loopback for sshd, and what does the stock image contain), recorded in spec §14
with its date like every other `sbx` fact.

Same category, same reason to hold: openharness ships a **prebuilt image** (`sandbox.image:
ghcr.io/mifunedev/openharness:latest`, `pull_policy`) so a team member skips the build. den's team
sources ship stack *definitions*, and every member then runs `den build` locally. Distributing the
built artifact would close that, but it depends on whether `sbx` can take an image from a registry —
unverified, and unverifiable from this repo.

---

## 6. Reject, with the reason

- **The `.oh/` primitive pack** (`skills/`, `agents/`, `hooks/`, `prompts/`, `evals/`, `memory/`,
  `context/`). That is agent content. den mounts a profile and stays out of it — and the profile
  already carries personal skills into every sandbox. Taking this on would make den an agent
  framework.
- **Clone-and-own distribution.** openharness asks the user to fork the framework and keep
  `upstream` as a remote. den's sources solve the same problem without forking the tool, and with a
  linter that refuses what a spawn would later refuse. den's answer is better; do not trade it.
- **`harness.yaml` as a model.** It is awk-parsed, flat, two levels, every key commented out. den's
  cascade with `KnownFields(true)` exists precisely because a silent `egres:` typo empties an
  allowlist. Do not soften it.
- **Compose overlays as sidecar services** (a project database beside the sandbox). Real need,
  wrong shape for den: there is no compose under `sbx`, so it would become stack provision steps
  plus lifecycle den does not own. Leave it to `egress:` pointing at a host-run service until
  someone asks twice.

---

## 7. Proposed roadmap

Ordered by blocking edges, not by appetite.

**Phase 1 — v1.5, no doctrine change.** Independent of each other; ship in any order.

1. `den exec <name> -- <cmd>` (§4.1) — the tracer bullet, and a prerequisite for phase 3.
2. Agent registry entries for `codex` and `pi`, which forces and closes **#50** (§4.2).
3. Guard hooks + an ENFORCED/RECOMMENDED security page (§4.3).
4. `pin:` on a team source, `den source update` refuses to cross it (§4.4).
5. README split into a docs index (§4.5).

**Phase 2 — v1.6, attach surface.** Blocked by a real `sbx` smoke.

6. The smoke itself: sshd/port-publish behaviour and the stock image's `.ssh`, answering **#57**;
   result to spec §14 with its date.
7. `known_hosts` for non-interactive callers, **#58** — also a phase-3 prerequisite.
8. Editor attach, if and only if the smoke says it is possible (§5).

**Phase 3 — v2, requires the §2 decision to be taken first.** Strictly sequential.

9. `ssh.mode: none` (or a scoped token), so a fire can run without the user's push access.
10. A one-shot unattended fire: `den run <nest> -w <branch> -- <agent cmd>` — own worktree,
    deterministic preflight gate in Go, caps in `state/`, a journal per fire, never auto-merge.
    Built on 9 and on `den exec`.
11. The scheduler over it (`schedule:`, `overlap:`, `catchup:`, `enabled: false` by default), in a
    new package behind a `cli.Deps` interface — `internal/cli` may not import `os/exec` or `net`.
12. Remote host (`sbx` on a cloud VM over SSH), then only after that a messenger bridge. Both are
    net/exec surfaces; same landing rule.

Everything in phase 3 is worthless without step 9, and step 11 without step 10 is a scheduler firing
an unbounded agent. That ordering is the whole content of this section.

---

## 8. What this review did not settle

- Whether `sbx` can publish a port for sshd or pull a template from a registry. Both are §5, both
  need a bench, and this repo attests `sbx` behaviour — it does not extrapolate it.
- Whether the §2 decision is (A) or (B). That is a human call; nothing here should be built past
  phase 1 until it is made.
