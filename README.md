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
- If the canonical window is already taken, den moves the **whole** block to the next free one and
  warns on stderr that these addresses hold for this instance only. It does not say *who* holds the
  canonical window: a bound port names no owner. It may be another instance of the nest, or an
  earlier `den ports` run of this same sandbox — `sbx ports <name>` settles it.
- `--add H:C` (repeatable) publishes a pair the nest does not declare. Added pairs are never
  scanned, so re-running the command re-reads the same table instead of fighting its own
  publication. A nest that declares no port prints no window and scans nothing — only the added
  pairs are published.

The table goes to stdout, every warning to stderr: what a pipe reads is the table alone.

Not shipped yet: `den build` (the image DAG). See `docs/superpowers/plans/`.

## Design

`docs/superpowers/specs/2026-07-27-den-cli-design.md`.
