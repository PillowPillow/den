# den

Generic CLI for driving `sbx` sandboxes: one command to start a multi-project
microVM, without retyping mixin, kits and policy by hand.

## Installation

```bash
go build -o den ./cmd/den
```

## Bootstrapping

```bash
cp -R examples/den-home ~/.den
$EDITOR ~/.den/config.yaml
./den doctor
```

On the first `den doctor`, two diagnostics are expected until you adapt the config: `sbx` missing
if it is not installed, and the example repo `~/dev/my-project` not found. Both go away once
`~/.den/config.yaml` and `~/.den/nests/example.yaml` are adjusted.

`~/.den/` is the single source of truth. The `DEN_HOME` variable (or the `--den-home` flag) lets
you use a different one — that is what makes `den` testable and scriptable.

## Available commands

| Command | Role |
|---|---|
| `den <nest>` | spawn-or-attach: creates the nest's microVM if it does not exist, attaches to it otherwise |
| `den ls` | lists live sandboxes, with their nest and worktree |
| `den sh <name>` | opens a shell in an existing sandbox |
| `den ports <name>` | publishes the nest's declared ports into that sandbox and prints where they land on the host |
| `den rm <name>` | destroys a sandbox and cleans up the worktrees den created (the agent profile persists) |
| `den build [<stack>]` | builds stack images in `parent` order, by running each stack's own `build.sh` |
| `den nest ls` | lists the declared nests |
| `den nest show <n>` | shows a fully resolved nest (stack, agent, egress, repos) |
| `den doctor` | diagnoses the configuration and the environment |
| `den version` | binary version |

Options of `den <nest>`:

| Option | Effect |
|---|---|
| `-w`, `--worktree <branch>` | propagates a worktree of that name across **all** the nest's repos, and suffixes the sandbox name (`api.feat12`) |
| `--detach` | prepares the sandbox without attaching a shell |
| `--only <repo,...>` | keep only these optional repos (required repos stay mounted) |
| `--without <repo,...>` | exclude these optional repos |
| `-i`, `--interactive` | pick the optional repos from a checklist; refused together with `--only`/`--without`, and outside a terminal (a pipe or CI must use those two) |
| `--agent <name>` | overrides `defaults.agent` |

`-w` takes a **branch** name, and a branch name often contains a `/`.
The branch keeps the name as typed — that is the name in `git log` and in the PR — while the
sandbox name and the worktree directory take a flattened form: `den api -w feature/123` works on
branch `feature/123` in a sandbox `api.feature-123`. That is the name it appears under in
`den ls`, and the one `den sh` and `den rm` expect.

So den accepts any name it can **name**; git remains the sole judge of what is a legal **ref**.
`-w 'a..b'` passes naming (sandbox `api.a--b`) and it is `git worktree add` that refuses,
before any sandbox is created.

Options of `den rm`: `--keep-worktrees` (keep the worktrees), `--force` (delete them even if they
carry uncommitted changes; without it, den refuses **before** touching the VM).

A stopped sandbox — which `sbx` does on its own after a few minutes of inactivity — is not a
failure: `den <nest>` and `den sh` pick it back up, with its state.

## Ports

Ports are **not** published at spawn. `den ports <name>` does it, on demand, and takes a **sandbox**
name — like `den sh` and `den rm` — because a port is published into a live VM.

Each nest gets a deterministic window of 10 host ports, `base = 9000 + hash(<nest>) % 900 * 10`,
overridable with `ports.base`. The window is seeded by the **nest**, never by the sandbox, so
`api` and `api.feat12` share the same base and a bookmarked URL keeps working across worktrees. A
declared port takes `base + <its declaration index>`; declaration order is what assigns the number,
so the table is printed in that order and never sorted.

```yaml
# ~/.den/nests/web.yaml
ports:
  base: 9100          # optional — otherwise hashed from the nest name
  publish:
    - { name: vite, container: 5173, open: true }
    - { name: api,  container: 3000 }
    - { name: cdp,  container: 9223, loopback_lock: true }
```

```
$ den ports web.feat123
nest: web   sandbox: web.feat123   window: 9100-9109 (canonical)
  NAME  CONTAINER  URL
  vite  5173       http://127.0.0.1:9100  [opened]
  api   3000       http://127.0.0.1:9101
  cdp   9223       http://127.0.0.1:9102  [loopback-locked]
  remote?  ssh -L 9100:127.0.0.1:9100 you@$(hostname)
```

- `open: true` opens that URL in a browser once the table is printed. A browser that fails to start
  is a warning on stderr, not a failed command — the ports are published either way.
- Everything binds `127.0.0.1`, **never** `0.0.0.0`. `loopback_lock: true` marks an endpoint that
  must never leave the loopback (an unauthenticated CDP/Playwright socket, typically).
- Remote access is the printed `ssh -L` line, never a LAN bind. `you@$(hostname)` is literal: paste
  it in your own shell, where it expands on the machine that runs the tunnel.
- **Re-running the command re-reads the same table.** den first reads what that sandbox already
  publishes and reuses the window it is on, publishing only what is missing. Without that step the
  scan finds den's own previous publication holding the canonical block, calls it busy, and binds
  ten more host ports — which is what it used to do, on every run.
- If the canonical window really is taken, den moves the **whole** block to the next free one and
  warns on stderr that these addresses hold for this instance only. It does not say *who* holds it:
  a bound port names no owner, and den only knows it is not this sandbox. `sbx ports <name>` lists
  what a sandbox publishes; `lsof -nP -iTCP:<port> -sTCP:LISTEN` names a process.
- `--add H:C` (repeatable) publishes a pair the nest does not declare. Re-running an identical
  `--add` succeeds and changes nothing. A nest that declares no port prints no window and scans
  nothing — only the added pairs are published.
- A **stopped** sandbox is started first, and said so on stderr: publishing a port needs a live
  endpoint in the VM, and `sbx ports` — unlike `sbx exec` — does not restart one.

The table goes to stdout, every warning to stderr: what a pipe reads is the table alone.

## Egress

A nest's `egress:` is a **widening** of the machine's policy, never a narrowing.

den cannot make a sandbox *less* connected than the host's own `sbx` policy already permits — on a
stock machine that baseline is broad (measured: 197 rules, including filesystem read and write
allow-all, and wildcards like `**.github.com:443` or `**.amazonaws.com:443`, so most package
registries, cloud endpoints and AI services are reachable whether or not a nest declares them).

What `egress:` genuinely buys is everything **outside** that baseline — a project database at
`10.22.11.54:27017`, an internal host — and that part is enforced, scoped to the sandbox. Measured
both ways: the same host is allowed for the nest that declared it and denied for the one that did
not (`No matching allow rule (default deny)`).

Treat `egress:` as *access you would not otherwise have*, not as a sandbox firewall.

## Agent freshness

The agent is updated **at boot**, not baked into the image, through the `update:` command of the
registry. den watches that gate: it reads the sandbox's kit-startup journal and **refuses to open a
sandbox whose agent it knows was never updated**, quoting the failing line.

Where it waits is a trade-off, and den takes the two sides differently: attaching a shell, it waits
for the verdict (tens of seconds on a fresh spawn) because you are about to run that agent; under
`--detach` it reads once and moves on with a note, because nobody is at a prompt and the next attach
catches it.

## Building images

A stack names an image and, optionally, a `parent:`. That is the whole DAG.

```
den build                 # every declared stack, in dependency order
den build dgdevx          # devx first if its image is missing, then dgdevx
den build dgdevx --force  # rebuild devx too
```

- Each stack is built by its own `stacks/<name>/build.sh`, which den runs **unchanged**, from the
  stack directory, with your environment. den orders the builds; it does not rewrite them, and
  `versions.lock` stays whatever the scripts maintain.
- The **target** is always rebuilt — you named it. Only its ancestors are skipped when their image
  is already there, and every skip is printed, with the `--force` that overrides it.
- "Already there" is read from `sbx template ls --json`, which is also why `den build` (everything)
  and `--force` never call it: those forms rebuild by definition.
- A stack with **no `build.sh`** is not something den builds: its `image:` is one sbx pulls. It is
  skipped and named, never a refusal — unless you ask for it by name, which den does refuse rather
  than answer with a skip line that reads as success.
- A `parent:` cycle is refused with the whole cycle spelled out (`a → b → a`), a `parent:` that does
  not exist names the file to fix, and every `build.sh` is checked to exist **before the first one
  runs** — a four-minute base image should not be built to reach a refusal den could make instantly.

`den <nest>` uses the same inventory: if the stack has a `build.sh` and its image was never built,
den stops and tells you to run `den build <stack>`. Without that check the failure surfaces as
sbx's own `403 Forbidden: pull failed for image "X"` — sbx treats an unknown template as a registry
pull, so the message talks about authorization rather than about a missing build. A stack with no
`build.sh` is left alone: its `image:` may well be one sbx can pull, and den has no build to
suggest.

## Design

`docs/superpowers/specs/2026-07-27-den-cli-design.md`.
