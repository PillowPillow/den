# Migrating from den v1

den V2 is a rewrite. It drives `sbx env` (sbx v0.53.0 or later) and shares no config format with v1.
The two can run on the same machine, with the same `~/.den`, until you switch.

In V2, a **nest** is a named set of repos declared in a `den.yaml`, and an **instance** is the sbx VM
den creates from it. See the [README](../README.md#words-den-uses).

## Which binary you run

The v1 and V2 binaries both answer `den`. Check which one is on your `PATH`:

```bash
command -v den
den --help
```

V2 lists exactly `build`, `exec`, `ls`, `rm`, `shell`, `up` and `version` (plus cobra's `completion`
and `help`). A list with `doctor`, `init` or `update` is v1. Homebrew, `install.sh` and
`go install github.com/PillowPillow/den/cmd/den@latest` install v1; V2 is built from the `v2` branch
with `task build` (see the [README](../README.md#install)). Keep the binary you are not using under
another name until you switch.

## What each version owns

| v1 only | V2 only |
|---|---|
| `~/.den/config.yaml` | `~/.den/den.yaml` |
| `~/.den/stacks/`, `~/.den/nests/` | `~/.den/run/`, `~/.den/gen/`, `~/.den/build/` |
| `~/.den/state/sandboxes/`, `~/.den/state/sources/`, `~/.den/cache/sources/` | `~/.den/state/v2/` |
| `~/.den/worktrees/`, `~/.den/trash/`, `~/.den/sources/` | `~/.den/wt/` |
| sbx templates with the tag a stack's `image:` names | sbx templates tagged `<name>:v2` |

V2 never reads or writes a v1 path in `~/.den`. Two overlaps remain:

- V2 always tags its templates `<name>:v2`. A v1 stack whose `image:` is `<name>:v2` would be
  overwritten by `den build <name>`.
- With the `per-repo` worktree layout, both versions put worktrees in `<repo>/.den/`.

## Moving a nest to V2

V2 does not list, adopt or remove a v1 sandbox. When V2 would create an instance whose name a v1 VM
already holds, it refuses and names `sbx rm <name>`.

1. Commit and push any work that lives only in the v1 VM or in its worktrees.
2. Remove the v1 sandbox with the v1 binary (`den rm`). v1 moves its worktrees to its trash; a
   branch still checked out in a v1 worktree cannot be checked out by `den up -w <branch>` until that
   worktree is gone (`git worktree list` shows it).
3. Declare the nest in `~/.den/den.yaml` ([den-yaml.md](den-yaml.md)), with a template: an
   official image such as `claude-code-docker` needs nothing more; a template of your own needs
   `den build <template>` first ([templates.md](templates.md)).
4. Run `den up <nest>` with the V2 binary.

An instance record written by an early V2 build (schema 1) is refused: run `sbx rm <instance>`, then
delete the record file the error names.

## v1 features and their V2 equivalent

| v1 | In V2 |
|---|---|
| `config.yaml`, stacks, `stack.yaml` | one `den.yaml` with nests and [templates](templates.md) |
| team sources (`den source`, `den lint`) | a template in a git repository, declared with `repo:` |
| `den up`, `den run` | `den up`, `den exec` |
| `den nest ls`, `den nest show` | `den up` without a name lists the nests |
| `den ports` | `ports:` on a nest, then `sbx ports <instance>` |
| `den doctor`, `den init`, `den update` | none; build V2 from the `v2` branch with `task build` |
| the worktree trash | none: `den rm` refuses to drop unsaved work, see [commands.md](commands.md#den-rm) |
| the SSH-agent key mount | sbx forwards your SSH agent, see [ssh.md](ssh.md) |
