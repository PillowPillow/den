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

Not shipped yet: `den ports` (a nest's `ports:` are loaded and displayed, but nothing publishes
them) and `den build` (the image DAG). See `docs/superpowers/plans/`.

## Design

`docs/superpowers/specs/2026-07-27-den-cli-design.md`.
