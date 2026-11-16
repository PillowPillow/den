# den

den drives [`sbx`](https://docs.docker.com/ai/sandboxes/) microVMs: one command creates (or
re-attaches to) a sandbox that holds your repos, runs Claude Code with your profile kits, and keeps the
egress allowlist, kits and ports you declared once in a `den.yaml`.

den does not replace sbx. It writes the `sbxenv.yaml` files sbx reads, calls `sbx env create` and
`sbx run`, and remembers what it created so `den rm` can undo exactly that.

**This is den V2.** It needs `sbx` v0.53.0 or later and a signed-in sbx (`sbx login`). V2 and the
v1 CLI can share `~/.den` until you cut over — see [Coexistence with v1](#coexistence-with-v1).

## Installation

Homebrew (macOS):

```bash
brew install --cask PillowPillow/tap/den
```

Or with a Go toolchain, from anywhere:

```bash
go install github.com/PillowPillow/den/cmd/den@latest
```

No Homebrew (Linux distros, WSL, macOS without brew):

```bash
curl -fsSL https://raw.githubusercontent.com/PillowPillow/den/main/install.sh | sh
```

The script detects OS and architecture, verifies the release checksum (and
refuses to install without verification), and installs to `~/.local/bin`.
Pin a version or change the destination by placing the assignment on `sh`
(each pipeline command gets its own environment, so a prefix on `curl` never
reaches the script):

```bash
curl -fsSL https://raw.githubusercontent.com/PillowPillow/den/main/install.sh | DEN_VERSION=v1.0.1 sh
curl -fsSL https://raw.githubusercontent.com/PillowPillow/den/main/install.sh | DEN_INSTALL_DIR=~/bin sh
```

Prebuilt archives also sit on the
[releases page](https://github.com/PillowPillow/den/releases) if you'd rather
not pipe a script into your shell.

From a checkout — the build runs through [go-task](https://taskfile.dev), so it comes first:

```bash
brew install go-task/tap/go-task   # or, with a Go toolchain already there:
go install github.com/go-task/task/v3/cmd/task@latest

task build
```

Every path stamps the version into the binary, so `den version` names the code it runs — the
release tag (`v1.0.0`) via Homebrew, `go install …/cmd/den@v1.0.0` and releases (including
`install.sh`), and where you stand relative to it (`v1.0.0-3-gabc1234-dirty`) via `task build`.
Building from a checkout without the runner — a plain `go build`, or a `go install ./cmd/den` —
is what names nothing: it answers `dev`, the documented tell that the build skipped `task`.

## Updating

| Installed with | Update with |
|---|---|
| Homebrew | `brew upgrade --cask den` |
| `go install` | `go install github.com/PillowPillow/den/cmd/den@latest` |
| `install.sh`, or a release archive | re-run the same `curl ... \| sh`, which updates in place |
| `task build` from a checkout | `git pull && task build` |

V2 has no `den update`. Until V2 is released, build it from the `v2` branch with `task build`; the
published releases are still v1.

## Quick start

The repository ships a den home you can copy and edit: [`examples/den-home/den.yaml`](examples/den-home/den.yaml).

```bash
mkdir -p ~/.den
cp examples/den-home/den.yaml ~/.den/den.yaml      # then edit the repo paths
mkdir -p ~/.den/kits/perso                         # your profile kit, see "Profiles"
printf 'schemaVersion: "2"\nkind: mixin\nname: perso\nversion: 1.0.0\n' > ~/.den/kits/perso/spec.yaml

den build example     # once: provision the base image and save it as the sbx template `example:v2`
den up demo           # create the instance `demo` if needed, then run Claude in it
```

Every command takes `--den-home <dir>` (or `$DEN_HOME`) to use another directory than `~/.den`.

## The `den.yaml` reference

There are two kinds of file, with the same name and the same strict schema:

- the **global file**, `<denHome>/den.yaml`: your machine. It is the only one that may carry
  `worktrees`, `repos`, `env`, `network`, `profiles` and `agents`;
- a **project file**, `den.yaml` in a project folder: bases and nests that travel with the project.
  den finds it by walking up from the current directory. A project file carrying a global-only key
  is refused.

Decoding is strict: an unknown key, a wrong `schema`, or a missing required field is a load error
that names the file and every problem, so a typo never silently empties an allowlist. Relative
paths in a file are relative to that file's folder; `~` is expanded.

```yaml
schema: 1                      # required, must be 1

# --- global file only -------------------------------------------------------
worktrees:
  layout: central              # central (default) | per-repo
  root: ~/.den/wt              # default <denHome>/wt; used by `central`
repos:                         # keys a nest can refer to
  api: ~/src/api
  web: ~/src/web
env:                           # reaches every instance
  TZ: Europe/Paris
network:
  allow:                       # egress for every instance
    - api.anthropic.com
profiles:                      # a profile is a named list of kits, see "Profiles"
  perso: [~/.den/kits/perso]
agents:                        # optional; `claude` is the only key
  claude:
    image: docker.io/docker/sandbox-templates@sha256:<64 hex>   # must pin a digest; default: the pin embedded in den

# --- both kinds of file -----------------------------------------------------
profile: perso                 # default profile of every nest of this file
bases:
  node:
    image: node:v2             # required: the sbx template `den build node` saves and a nest starts from
    from: ""                   # template the build VM starts from; empty = the pinned den-claude image
    kits: []                   # default kits of every nest on this base
    network:
      allow: []                # default egress of every nest on this base
    build:
      includes: [lib/common.sh]   # shell prepended to every step
      steps: [steps/10-node.sh]   # one script per `sbx exec`, in order
      network:
        allow: [registry.npmjs.org]   # egress of the build VM only

nests:
  api:
    base: node                 # required; looked up in the nest's own file, then in the global file
    profile: perso             # optional; wins over the file's `profile`
    repos:                     # required, at least one
      - key: api               # a key of the global `repos`
      - path: ~/src/api-docs   # or a host path; exactly one of key and path
        optional: true         # dropped with --without, kept alone with --only, asked with -i
    kits: []                   # added after the base's kits
    network:
      allow: []                # added after the global and the base's
    env: {}                    # overrides the global env, key by key
    ports:
      - {name: web, sandbox: 3000, host: 0}   # host 0 = an ephemeral host port
    start: api                 # where Claude starts, see below
    worktrees: {layout: central, root: ~/wt}  # overrides the global setting for this nest
    layers: []                 # extra sbxenv.yaml files, see "Layers"
```

### Bases

A base is an image recipe. `den build <base>` produces the sbx template named by `image`; a nest
refuses to start, naming `den build <base>`, while that template does not exist. Image tags
should end in `:v2` (see [Coexistence](#coexistence-with-v1)).

`from` names the template the build VM starts from. Left empty, the build starts from the
`docker.io/docker/sandbox-templates:claude-code-docker` image that den pins by digest, so every
`den build` starts from the same Claude Code. `agents.claude.image` in the global file replaces that
pin (a digest is required: a tag or an image without `@sha256:<64 hex>` is a load error).

The image must keep the VM alive: sbx runs the image's own `ENTRYPOINT` as the VM's first process,
with its keep-alive command as arguments, and the kit's `entrypoint` only launches Claude when you
attach. The `claude-code-docker` images use `tini --`. `docker.io/sbx/claude` uses `claude`, which
exits within seconds and stops the VM, so every `den build` step and every `den exec` dies with
status 137: do not use it. Check an image with
`docker image inspect --format '{{json .Config.Entrypoint}}' <image>`.

To move to a newer Claude image: run
`docker buildx imagetools inspect docker.io/docker/sandbox-templates:claude-code-docker`, copy the
index digest into `agents.claude.image` as `docker.io/docker/sandbox-templates@sha256:<digest>`, then
`den build <base>` for each base. An instance that already exists keeps its old image; `den up` on
it warns that the config changed (the image is part of the config hash), and `den rm` then `den up`
recreates it on the new one. A build step that runs `claude update` also moves a base to a newer
Claude Code, without changing the image.

A kit is a directory holding `spec.yaml` (a path starting with `./`, `../`, `/` or `~`, checked
before any side effect) or a reference sbx resolves itself, passed through verbatim.

`kits` and `network.allow` on a base are defaults that every nest on it receives before its own.
`build.network.allow` applies to the build VM only.

### Nests

- **Name.** A nest name becomes the first part of the sandbox name, so it must be a legal sandbox
  name component. Two files declaring the same nest name are refused; den remembers which file owns
  a name in `<denHome>/state/v2/nests.yaml`, so a project nest keeps working from any directory
  once den has seen it.
- **Repos.** Each repo is mounted in the VM at the same path it has on the host. Two repos with the
  same folder name are refused.
- **Merge order.** Egress is global, then base, then nest. Kits are base, then nest, then the profile's. Env is global,
  then nest (the nest wins).
- **Start.** `start` is `<repo>[/<sub folder>]` (the repo's folder name), or `.` for the project
  folder (project files only). Empty means the first repo. `--workdir <vm path>` overrides it for
  one command.
- **Workspace.** The VM's primary workspace is a per-instance *run dir*:
  `<project>/.den/run/<instance>/` for a project nest (den adds `.den/.gitignore`), or
  `<denHome>/run/<instance>/` for a global nest. It holds only the generated `sbxenv.yaml`. The run
  dir is den's and disposable: `den rm` deletes it without checking it for changes, so keep
  nothing there. With `start: .`, the workspace is the project folder instead, which is the only
  way a VM reaches your project's `den.yaml` and scripts; den then mounts every file it reads from
  that folder (the `den.yaml`, a local kit, a layer) read-only, so the VM reads them but cannot
  rewrite them (see [Layers](#layers)).
- **Ports.** Published through the sandbox at create time. den prints sbx's port table after each
  create; run `sbx ports <instance>` to see it again.
- **Worktrees.** See [Worktrees](#worktrees).

## Profiles

A profile is a named list of kits, defined only in the global file and named by a top-level
`profile:` (the default of every nest of that file) or by a nest's own `profile:` (which wins):

```yaml
# ~/.den/den.yaml
profiles:
  perso: [~/.den/kits/perso]
profile: perso
```

A profile kit is an ordinary local kit (`kind: mixin`) that carries your Claude configuration as
files copied into the VM's home. Its `spec.yaml` is required, and the smallest one is:

```yaml
schemaVersion: "2"
kind: mixin
name: perso
version: 1.0.0
```

```
~/.den/kits/perso/
  spec.yaml                      # the spec above
  files/home/.claude/CLAUDE.md
  files/home/.claude/settings.json
  files/home/.claude/output-styles/
  files/home/.claude/agents/
  files/home/.zshrc
```

Profile kits come after the base's and the nest's kits. A profile the machine does not define is
skipped with a warning, never refused. Files under `files/home` are copied verbatim into
`/home/agent` when the VM is created, so recreate the instance after an edit. Skills travel
through `sbx skills import` or `files/home/.claude/skills/`. Ship `settings.json` from one profile
kit per VM: which kit wins on a collision is unverified. A `settings.json` that sets the
permission mode (the Claude Code settings key `"permissions": {"defaultMode": "bypassPermissions"}`,
from the Claude Code settings schema) makes a `claude` typed inside `den shell` behave like the
attached one; den's own launch already passes `--dangerously-skip-permissions`.

## Commands

All commands accept `--den-home <dir>`. den passes sbx's global `--no-input` to `env create`,
`env rm`, `rm`, `exec` and `ports`. `den build`'s `stop` and `template save` do not get it, nor do
the interactive attaches (`sbx run` for `den up`, `sbx exec -it` for `den shell`).

### `den up <nest> [flags]`

Creates the instance if it does not exist, then attaches Claude to it (`sbx run`, starting in the
`start` folder). Without a nest name it lists the nests it can see.

| Flag | Effect |
|---|---|
| `-w, --worktree <branch>` | run on a git worktree of every repo, on that branch |
| `--only a,b` / `--without a,b` | keep only / drop these optional repos |
| `-i, --interactive` | pick the optional repos from a checklist (needs a terminal) |
| `--repo <path>` | also mount this folder (repeatable, in the order typed); the instance keeps it until `den rm` |
| `--workdir <vm path>` | start Claude in this folder instead of `start`, this time only (the instance keeps `start`) |
| `--detach` | create or start the instance, leave its VM running, and do not attach |

The instance is named `<nest>`, or `<nest>.<branch>` with `-w`; slashes in the branch are
flattened (`feature/123` becomes `demo.feature-123`) while git keeps the branch as typed. That
name is the VM's name and the handle of every other command.

Every refusal den can decide from the config, the flags, its records and `sbx ls` comes before the
first file, worktree or sbx call that changes state: unknown nest, missing repo,
missing image (`den build`), a missing local kit or layer, a missing `build.includes` or
`build.steps` file under a mounted folder, a file den reads through a symlink inside a mounted
folder, `-i` without a terminal, an sbx older than v0.53.0.

On an instance that already exists den attaches and changes nothing. If the nest's config changed
since the instance was created, it warns and tells you to `den rm` then `den up`; a selection flag
(`--only`, `--without`, `-i`) on a running instance is ignored with a warning for the same reason.
If a sandbox of that name exists but den V2 has no record of it (a v1 VM, say), den refuses and
names `sbx rm <name>`; it never adopts what it did not create.

### `den exec <nest> [flags] -- <cmd> [args...]`

Same creation as `den up`, then runs one command in the start folder and returns its exit code.
The `--` is mandatory. It never asks (`-i` is refused) and writes its own messages to stderr, so
stdout carries only the command's output.

```bash
den exec demo -- git status
den exec demo -w feature/123 -- bash -lc 'echo ok'
```

### `den shell [<instance>] [--workdir <vm path>]`

Opens `bash -l` in an instance. Without a name, den picks the instance whose repos contain the
current directory, then the only running one, and otherwise lists the candidates.

### `den ls`

Lists the instances V2 recorded and the state of their VM (`missing` when the VM is gone):

```
INSTANCE  NEST  BRANCH   STATUS   PROJECT
demo      demo           running  /path/to/project
```

An unreadable record is reported on stderr, never hidden.

### `den rm <instance> [--force]`

Removes an instance den created: its VM, the worktrees den made, the run dir and generated files,
then the record, in that order. It acts only on an instance with a readable V2 record, and only on
worktrees the record marks as den-created; a repo den found in place is never touched.

There is no trash. `den rm` therefore refuses, before removing anything (the VM included), a
worktree with modified, untracked or ignored files, and a detached HEAD that holds commits no
branch, remote branch or tag contains. The refusal names them and `--force`, which discards them.
It also refuses to remove a worktree another instance mounts (two nests sharing a repo and a `-w`
branch share it; remove the other instance first), and, while a record under `state/v2/` is
unreadable, any den-created worktree at all.

`den rm` removes the VM through `sbx env rm` only when every file the create read still has the
hash den recorded; otherwise, since a VM may have rewritten it, through `sbx rm --force`.

### `den build <base>`

Provisions a throwaway VM named `<base>-build` on `from` (or the pinned den-claude image), runs each
`build.steps` script in it through `bash -lc` (with `build.includes` prepended to each), stops it,
saves it as the template `image`, and removes the VM. Scripts are read before anything is created,
so a missing one refuses instantly. A leftover `<base>-build` sandbox makes `den build` refuse
rather than remove a VM it did not create. The VM's scratch workspace is `<denHome>/build/<base>/`.

### `den version`

Prints the version stamped at build time; `den --version` and `den -v` print the same line. A plain
`go build` answers `den dev`; use `task build`.

## Worktrees

`-w <branch>` makes a git worktree of every selected repo on that branch (creating the branch when
it does not exist) and mounts it in the VM at the repo's usual path, so paths inside the VM do not
change. With `layout: central` the worktrees live under `<root>/<flattened branch>/<repo>`; with
`per-repo`, under `<repo>/.den/<flattened branch>`.

den locks each worktree it creates (`git worktree lock`), mounts the repo's common git dir at
`/den-git/<repo>`, and mounts a read-only `.git` file that points at it. A second
`den up demo -w feature/123` attaches to the same instance. Repos must be the root of a git
repository for `-w`.

## Layers

`layers:` lists extra `sbxenv.yaml` files stacked after the one den generates. This is how a nest
reaches sbx fields den does not model (`secrets:`, `mcp:`, `sandboxOptions.memory`, ...). den never
parses them; sbx merges them.

```yaml
nests:
  api:
    layers: [~/.den/layers/api-secrets.yaml]
```

sbx acts on a layer at the next `create` and `rm`, so a layer the VM could rewrite would hand the
VM host-side actions. A layer may still live in a folder the VM can write (the workspace, a repo or
its worktree): den then mounts it read-only over that folder, at the path where the VM sees it. The
same goes for every other file den or sbx reads for the instance: the `den.yaml` files the nest
resolves from, its local kits, and its base's `build.includes` and `build.steps`. den checks both
the written and the symlink-resolved paths, and refuses a file whose written path goes through a
symlink inside a mounted folder (the VM could replace that link): reference its real path instead. The read-only overlay holds only inside the instance that reads the
file: any other VM that mounts the same folder read-write (another nest sharing the repo, or a v1 VM)
can still write it.
The host enforces the read-only mount even against root in the VM. What the VM can still do is
create a *new* file beside them, a `den.yaml` in a sub folder for one, which den would read if you
ran it from there. Independently, den passes
`--force --skip-host-commands` to every `sbx env create` and `sbx env rm`, so no `lifecycle:`
command or command-backed MCP server in a layer ever runs on the host.

## Where things live

| Path | Content | Owner |
|---|---|---|
| `<denHome>/den.yaml` | global config | you |
| `<project>/den.yaml` | project bases and nests | you |
| `<project>/.den/run/<instance>/` or `<denHome>/run/<instance>/` | the generated `sbxenv.yaml` (the VM's workspace) | den |
| `<denHome>/gen/<instance>/` | generated egress kit and worktree `.git` files | den |
| `<denHome>/gen/.kits/den-claude/` | the shared den-claude agent kit | den |
| `<denHome>/gen/.build/<base>/` | generated files of a build | den |
| `<denHome>/build/<base>/` | the build VM's scratch workspace | den |
| `<denHome>/wt/` | default worktree root | den |
| `<denHome>/state/v2/instances/<instance>.yaml` | the record `ls`, `shell` and `rm` replay | den |
| `<denHome>/state/v2/nests.yaml` | which file declares which nest | den |

den writes the files under a run dir and never reads them back: what it mounted is what the record
says, not what a VM may have edited since.

## Coexistence with v1

V2 uses only paths v1 never reads or writes: `den.yaml`, `run/`, `gen/`, `build/`,
`state/v2/` and `wt/`. v1 keeps `config.yaml`, `stacks/`, `nests/`, `state/sandboxes/`,
`worktrees/`, `trash/` and the `:v1` image tags. V2 base images use `:v2` tags (`node:v2`), so
`den build` never overwrites a v1 template.

V2 does not list, adopt or remove a v1 sandbox, and refuses (naming `sbx rm`) to create an instance
whose name a v1 VM already holds. The v1 binary and the V2 one are different builds of the same
`den` command; keep the one you are not using under another name until you cut over.

## Design

The V2 decisions are in `docs/superpowers/plans/2026-11-12-den-v2-0.md`; the v1 spec,
`docs/superpowers/specs/2026-07-27-den-cli-design.md`, is historical.
