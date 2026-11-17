# Commands

den has seven commands. Every command takes `--den-home <dir>` (or `$DEN_HOME`) to use another den
home (the folder that holds den's config and state) than `~/.den`.

| Command | What it does |
|---|---|
| [`den up <nest>`](#den-up) | create the nest's instance if needed, then run Claude in it |
| [`den exec <nest> -- <cmd>`](#den-exec) | create the instance if needed, run one command, return its exit code |
| [`den shell [<instance>]`](#den-shell) | open a `bash -l` shell in an existing instance |
| [`den ls`](#den-ls) | list the instances den created |
| [`den rm <instance>`](#den-rm) | remove an instance: its VM, the worktrees den made, its files |
| [`den build <template>`](#den-build) | build a template, see [templates.md](templates.md) |
| [`den version`](#den-version) | print den's version |

A **nest** is what you declare in a `den.yaml` ([den-yaml.md](den-yaml.md)). An **instance** is the
sbx sandbox, a microVM, that den creates from it. The instance is named `<nest>`, or
`<nest>.<branch>` with `-w`. That name is also the sbx sandbox name, so `sbx` commands accept it too.

## `den up`

```bash
den up <nest> [-w <branch>] [--only a,b | --without a,b | -i] [--repo <path>]... [--workdir <vm path>] [--detach]
```

Creates the instance if it does not exist, then attaches Claude to it, in the nest's
[start folder](den-yaml.md#start-folder). When you quit Claude, run the same `den up` command again
to attach again (with the same `-w <branch>` for a worktree instance).

| Flag | Effect |
|---|---|
| `-w, --worktree <branch>` | run on a git worktree of every repo, on that branch, see [Worktrees](#worktrees) |
| `--only a,b` | mount only these optional repos |
| `--without a,b` | do not mount these optional repos |
| `-i, --interactive` | pick the optional repos from a checklist (needs a terminal) |
| `--repo <path>` | also mount this folder (repeatable); the VM keeps it for its whole life |
| `--workdir <vm path>` | start Claude in this folder, this time only |
| `--detach` | create or start the instance, leave it running, and do not attach |

Optional repos are described in [den-yaml.md](den-yaml.md#optional-repos).

Without a nest name, `den up` fails and lists the nests it can see.

**Before it changes anything**, den checks what it can: an unknown nest, a missing repo, a template
that is not built, no template named anywhere, a missing local [kit](den-yaml.md#every-key) or
[layer](security.md#layers), `-i` without a terminal, an sbx older than v0.53.0. A refusal names the
file or the command that fixes it.

**After it creates the VM**, den prints two kinds of alerts on stderr. They do not stop `den up`:

- `warning: the global sbx rule <id> opens <host> to every VM` for each rule of the global sbx
  policy that sbx did not create, and `allows every host` for a rule that allows every host (as
  the `allow-all` preset does). See [security.md](security.md#what-the-vm-can-reach).
- `note: the SSH agent at <socket> holds no key`, `note: no SSH agent answers at <socket>` or
  `note: SSH_AUTH_SOCK is not set`, when sbx forwards the SSH agent to the VM and the VM may not
  be able to use it. See [ssh.md](ssh.md#check).

den prints them only when it creates the VM, never when it attaches to an existing one. den waits
at most 3 seconds for these checks in total; a check that fails or does not finish in time prints
nothing.

**On an instance that already exists**, den attaches and changes nothing. If the nest's config
changed since the instance was created, den warns and tells you to run `den rm <instance>` then
`den up` again. `--only`, `--without`, `-i` and `--repo` on an existing instance are ignored with a
warning for the same reason.

**A VM den did not create.** If an sbx sandbox with the instance's name exists but den has no record
of it, den refuses and names `sbx rm <name>`. den never adopts a VM it did not create.

`sbx stop <instance>` stops a VM without removing it; `den up` starts it again.

## `den exec`

```bash
den exec <nest> [-w <branch>] [--only a,b | --without a,b] [--repo <path>]... [--workdir <vm path>] -- <cmd> [args...]
```

Creates the instance like `den up` if needed, then runs one command in the start folder and returns
its exit code. The `--` is required. `den exec` never asks a question (`-i` is refused). It writes
its own messages to stderr, so stdout carries only the command's output. No terminal is allocated.
Unverified: whether your stdin reaches the command.

```bash
den exec demo -- git status
den exec demo -w feature/123 -- bash -lc 'npm test'
den exec demo -- cat package.json > package.copy.json
```

- `den exec demo -- cat package.json > package.copy.json` writes a file that holds only the command's output.

When den itself fails (a refusal, an sbx error), the exit code is 1, the same as a command that
exits 1: read stderr to tell them apart.

## `den shell`

```bash
den shell [<instance>] [--workdir <vm path>]
```

Opens `bash -l` in an instance that already exists; it never creates one. Without a name, den picks
the instance whose repos contain the current directory, then the only running instance; otherwise
it lists the candidates and asks you to name one.

`claude` typed in this shell runs without `--dangerously-skip-permissions`, unlike the Claude that
`den up` attaches. See [den-yaml.md](den-yaml.md#profiles).

## `den ls`

Lists the instances den recorded and the state of their VM:

```
INSTANCE  NEST  BRANCH   STATUS   PROJECT
demo      demo           running  /path/to/project
```

- `STATUS` is sbx's state of the VM (`running`, `stopped`), or `missing` when the record exists but
  the VM is gone; the same `den up` command creates it again.
- `PROJECT` is the project folder of a nest from a project `den.yaml`; it is empty for a nest of the
  global file.

An unreadable record is reported on stderr, never hidden. `den ls` does not list sbx sandboxes that
den did not create: `sbx ls` lists them all.

## `den rm`

```bash
den rm <instance> [--force]
```

Removes an instance den created, in this order: its VM, the worktrees den made, the folders den
generated for it (its run dir and generated kits, see [files.md](files.md)), then its record (the
file in which den notes what it created). It acts only on an instance den has a record of. It
removes only the worktrees den created; a repo or worktree den found in place is never touched.

**Why `den rm` refuses.** There is no trash, so `den rm` refuses, before it removes anything (the VM
included):

| Refusal | What to do |
|---|---|
| a worktree den created holds modified or untracked files, or ignored files (an ignored folder such as `node_modules/` does not count: it is deleted) | commit or copy them, or `--force` to discard them |
| a worktree holds commits that no branch, remote branch or tag contains (a detached HEAD: git is on a commit, not a branch) | `git branch <name>` in the worktree, or `--force` to discard them |
| another instance mounts the same worktree (two nests that share a repo and a `-w` branch) | remove the other instance first |
| a worktree is locked by something other than den | unlock it with `git worktree unlock`; `--force` does not override this |
| the instance's record is unreadable, or another record is unreadable while this instance has worktrees den created | fix or delete the file it names |
| the record names a run dir or a generated folder that is not where den places it | fix or delete the record it names |
| no record of the instance | `den ls` lists what den knows; `sbx rm <name>` removes a VM den did not create |

The refusal names the files or commits concerned (the first five, then a count). `--force` discards
modified, untracked and ignored files and unreachable commits.

## `den build`

```bash
den build <template> [--pull] [--yes] [--force]
```

Builds a template declared in a `den.yaml`. See [templates.md](templates.md#den-build).

## `den version`

Prints the version stamped at build time. `den --version` and `den -v` print the same line.
`task build` stamps the `git describe` of the checkout. A plain `go build` answers `den dev`.

## Worktrees

`-w <branch>` runs the instance on a git worktree of every repo, on that branch. When the branch does
not exist, den creates it from `origin/HEAD`, or from the current HEAD when there is no
`origin/HEAD`, without tracking. git keeps the branch name as you typed it. Each worktree is mounted
in the VM at the repo's usual path, so paths in the VM do not change.

```bash
den up demo -w feature/123
den up demo
```

- `den up demo -w feature/123` runs the instance `demo.feature-123`.
- `den up demo` runs the instance `demo`, on the repos as they are.

The two instances run side by side. A second `den up demo -w feature/123` attaches to the same
instance. In the instance name, every character other than a letter, a digit or `-` becomes `-`. Two
branches that give the same name (`feat/x` and `feat.x`) are refused for the second one.

Where the worktrees live is set by `worktrees:` in the global file (`~/.den/den.yaml`), or per nest:

| `layout` | Worktree of the repo at `<repo path>` on branch `<branch>` |
|---|---|
| `central` (default) | `<root>/<branch>/<repo folder name>`, with `root` default `~/.den/wt` |
| `per-repo` | `<repo path>/.den/<branch>` |

`<branch>` here is the flattened branch (`feature-123`).

- Every repo of the instance, `--repo` folders included, must be the **root of a git repository**.
  A folder inside a repository, or a folder that is not in one, is refused with `-w`: drop `-w`, or
  fix the repo path.
- A worktree that already exists at that path on another branch is refused.
- den locks each worktree it creates (`git worktree lock`). A worktree that already exists at that
  path on the right branch is used as found and left unlocked; `den rm` never removes it.
- The VM sees the repo's git directory at `/den-git/<repo folder name>`, read-write, so it can
  commit. The worktree's `.git` file is read-only. See
  [security.md](security.md#what-the-vm-can-write).
- `den rm demo.feature-123` removes the worktrees den made, after the checks in
  [`den rm`](#den-rm). The branch stays.
