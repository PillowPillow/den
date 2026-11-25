# The `den.yaml` reference

A `den.yaml` declares **nests**. A nest is a named set of repos plus everything the VM needs around
them: the template it starts from, its network access, its ports, its kits. `den up <nest>` turns a
nest into an **instance**, a running sbx VM. The **den home** is the folder that holds den's config
and state, `~/.den` by default. See the [README](../README.md#words-den-uses) for the other words.

- [Global file](#global-file)
- [Project file](#project-file)
- [Rules for both files](#rules-for-both-files)
- [Every key](#every-key)
- [Nests](#nests)
- [Repos](#repos)
- [Network](#network)
- [Ports](#ports)
- [Profiles](#profiles)

## Global file

`~/.den/den.yaml` describes your machine. `--den-home <dir>` or `$DEN_HOME` moves the whole den home,
this file included. The global file is the only one that may carry `worktrees`, `env`, `network`,
`profiles`, and at its top level `auto_trust`, `add_dirs`, `sessions` and `resources`. Both kinds of file may carry `repos`, with different entries (see [Repos](#repos)).

## Project file

A `den.yaml` inside a project folder holds nests and templates that travel with the project: commit
it, and a teammate runs `den up <nest>` from a clone. den finds it by walking up from the current
directory to `/`, and takes the first `den.yaml` that is not the global file. A project file that
carries a global-only key is refused.

The repos of a teammate are rarely at the same absolute paths as yours. Three ways keep a project
file portable:

- a path relative to the project file (`path: .`, `path: ../api`);
- a `key:` that each teammate defines in their own global `repos:`;
- a `key:` that the project file declares with the repo's git `origin`: den finds the clone on each
  machine, see [Repos](#repos).

```yaml
# <project>/den.yaml, committed
schema: 1
default_template: claude-code-docker
profile: perso                 # optional; a project nest ignores the global file's `profile:`
repos:
  shared-lib: {origin: git@github.com:acme/shared-lib.git}   # den scan finds the clone
nests:
  shop:
    repos:
      - path: .                # this project
      - key: shared-lib
    network:
      allow: [registry.npmjs.org]
```

**Read a project file before you run it.** A cloned `den.yaml` decides what a VM mounts, which
kits it gets and which hosts it reaches, and its `layers:` can run a command on your host (see
[security.md](security.md#layers)). den asks before it clones a template URL from a project file,
but not before it uses the file's nests.

A nest without `template:` takes `default_template:` of its own file, then the global file's.
den writes `<project>/.den/.gitignore` so that its run dirs (see [Workspace](#workspace)) stay out of
git.

Once den has seen a project nest, it records which file declares it
(`~/.den/state/v2/nests.yaml`), and `den up <nest>` then works from any directory. Two files that
declare the same nest name are refused.

## Rules for both files

- Decoding is strict. An unknown key, a wrong `schema`, or a missing required field is a load error
  that names the file. A typo never silently empties an allowlist.
- A relative path is relative to the folder of the file that holds it. `~` is expanded.

## Every key

```yaml
schema: 1                      # required, must be 1

# --- global file only -------------------------------------------------------
# (`repos:` is in both kinds of file, with other entries: see "Repos")
worktrees:
  layout: central              # central (default) | per-repo, see docs/commands.md#worktrees
  root: ~/.den/wt              # default <den home>/wt; used by `central`
repos:                         # global file: every key has a `path`, `origin` is optional
  api: ~/src/api               # a scalar is a path
  web: {path: ~/src/web, origin: git@github.com:acme/web.git}
env:                           # environment variables of every instance
  TZ: Europe/Paris
network:
  allow:                       # hosts every instance may reach, see "Network"
    - registry.npmjs.org
profiles:                      # named kit lists, see "Profiles"
  perso: [~/.den/kits/perso]
auto_trust: true               # default true; Claude trusts the instance's folders without asking,
                               # see docs/security.md#folder-trust
add_dirs: false                # default false; true loads the other repos' CLAUDE.md and skills,
                               # see "Other repos in Claude's context"
sessions: true                 # default true; Claude's sessions stay on the host after the VM,
                               # see "Sessions"
resources: {cpus: 4, memory: 8g}   # optional; the VM size of every instance, see "Merge order"

# --- both kinds of file -----------------------------------------------------
default_template: claude-code-docker   # the template of a nest that names none
profile: perso                 # default profile of every nest of this file
templates:                     # templates you build yourself, see docs/templates.md
  node:
    dir: ~/src/den-templates   # or repo: <git URL>; exactly one of the two
    # name: node               # the name inside den-template.yaml; default the key
    # ref: main                # repo only; branch or tag

nests:
  api:
    template: node             # a declared template or an official image; default `default_template`
    profile: perso             # optional; wins over the file's `profile`
    repos:                     # required, at least one
      - key: api               # a key of `repos`, see "Repos"
      - path: ~/src/api-docs   # or a host path; exactly one of key and path
        optional: true         # see "Optional repos"
    kits: []                   # extra kits, added after the template's
    network:
      allow: []                # added to the global and the template's, see "Network"
    env: {}                    # overrides the global env, key by key
    ports:
      - {name: web, sandbox: 3000, host: 0}   # see "Ports"
    start: api                 # the folder Claude starts in, see "Start folder"
    worktrees: {layout: central, root: ~/wt}  # overrides the global setting for this nest
    layers: []                 # extra sbxenv.yaml files, see docs/security.md#layers
    auto_trust: false          # optional; wins over the global auto_trust
    add_dirs: true             # optional; wins over the global add_dirs
    sessions: false            # optional; wins over the global sessions
    resources: {memory: 16g}   # optional; wins over the global resources, key by key
```

`template:` and `templates:` are described in [templates.md](templates.md); `worktrees` and `-w`
in [commands.md](commands.md#worktrees); `layers:` in [security.md](security.md#layers).

A **kit** (`kits:`, a profile entry) is a folder that holds an sbx kit `spec.yaml`. Write a local kit
as a path that starts with `./`, `../`, `/` or `~`; den checks that it exists before it creates
anything. Any other value is a kit reference that den passes to sbx as written (for example
`docker.io/acme/kit:1`).

## Nests

### Name

The nest name becomes the first part of the VM name: letters, digits and `-`, starting with a
letter or a digit, no `.`. The instance is named `<nest>`, or `<nest>.<branch>` with `-w <branch>`,
where every character of the branch other than a letter, a digit or `-` becomes `-` (`feature/123`
gives `api.feature-123`). sbx adds limits of its own on the full name (length, reserved names);
den reports them when it checks the name.

### Repos

Each repo is mounted read-write in the VM **at the same absolute path** it has on the host, so a
path means the same file on both sides. A missing repo folder is refused before anything is created.

A repo entry of a nest is either `key: <name>` or `path: <folder>`. A **key** is declared in a
`repos:` map, and the key is the repo's name everywhere: `/den-git/<key>` in the VM under `-w`,
`start:`, `--only` and `--without`, and the repo map. A key is a legal folder name: no `/`, not `.`
or `..`, no leading dot. A `path:` entry is named by its folder name.

In every layout, two repos of one nest with the same name are refused (two `path:` entries with the
same folder name, or a key and a `path:` entry of that name), and so are two entries of one nest on
the same folder.

**Declaring keys.** Each entry says where the repo is, or how to find it:

| File | Entry | Meaning |
|---|---|---|
| project | `key: {origin: <git URL>}` | the repo is whichever clone of that URL is on the machine |
| project | `key: {path: <folder>}` | a folder, relative to the project file |
| global | `key: <folder>` or `key: {path: <folder>}` | the folder on this machine |
| global | `key: {path: <folder>, origin: <git URL>}` | the same, and the repo's identity is stated |

A project entry has exactly one of `origin` and `path`. A global entry needs a `path`; `origin` alone
is refused there. A key in both files is one repo: two different origins, or two different paths, for
the same key are refused. An `origin` starts with a letter or a digit and holds no shell character
(`$`, `` ` ``, `;`, `|`, quotes, parentheses, spaces): den prints it in the `git clone` line of a
missing repo, a line meant to be pasted. A key is refused if it holds a control or format character.

**Finding the folder.** For a key, den uses the first of:

1. the `path` in the global file;
2. the `path` in the project file;
3. the table `den scan` wrote (`~/.den/state/v2/repos.yaml`), which maps an origin to a folder.

When a project `origin` meets a global `path` without `origin`, den refuses unless a remote of that
folder has the same identity: a teammate's key must not mount a different repo of yours. A global key
with both `path` and `origin` is trusted as written.

An **identity** is `host/path` of the URL, lowercase, without scheme, user, password, port, `.git` and
trailing `/`: `git@github.com:Acme/Web.git` and `https://github.com/acme/web` are the same repo. An
SSH alias (`Host gitlab-perso` in `~/.ssh/config`) and git's `insteadOf` are not understood: the
remote then has another identity. Put the key in your global file with a `path` (and the real
`origin`), and den uses that folder.

**Keys without a folder.** `den up` lists every key that has none at once, with a `git clone` line
for each, and names `den scan`. A key counts as missing too when the table maps its origin to a folder
that is no longer a clone of it. In a terminal, `den up` then asks before it starts the instance
without them; with no terminal, and always in `den exec`, it refuses. A running instance keeps the
repos it was created with, so `den up` only warns. An optional key without a folder is dropped with a
warning.

**Folder names under `-w`.** A worktree keeps the folder name of its clone. With the `central`
layout, two repos of a nest with the same folder name would land on one worktree path: den refuses.

### `den scan`

`den scan [folder]` finds the clones for you and records them in the table `~/.den/state/v2/repos.yaml`
(`identity: path`). It never rewrites a `den.yaml`. See [commands.md](commands.md#den-scan).

### Optional repos

A repo with `optional: true` is still mounted by default. Its name is its key, or its folder name
for a `path:` entry (`api-docs` for `~/src/api-docs`). Pick per run; `--only` and `--without` take a comma-separated list
(`--without api-docs,web-docs`), cannot be combined, and refuse a name that is not optional:

| You run | Optional repos mounted |
|---|---|
| `den up api` | all of them |
| `den up api --without api-docs` | all except `api-docs` |
| `den up api --only api-docs` | only `api-docs` |
| `den up api -i` | the ones you tick in a checklist |

The non-optional repos are always mounted. A missing optional folder is refused like any repo:
leave it out with `--without`. An optional `key:` that no `repos:` defines is
skipped with a warning. The selection is fixed when the instance is created; on an existing
instance, running or stopped, a selection flag is ignored with a warning.

`--repo <path>` mounts one more folder that the nest does not declare. The VM keeps it for its
whole life.

### Start folder

`start:` is the folder Claude starts in: `<repo>` or `<repo>/<sub folder>`, where `<repo>` is the
repo's key, or its folder name for a `path:` entry. Empty means the first repo. In a project file, `start: .` means the project
folder. `--workdir <vm path>` overrides it for one command.

### Repo map

At each create, den writes the instance's repo map into the VM: the mounted repos (with their
branch under `-w`), the nest's optional repos left out, and the machine's other repos: the keys of
the global `repos:`, then the clones of the `den scan` table that no global key points to, under
their folder name. A folder that is gone is left out.
Claude loads it from `~/.claude/rules/den-instance.md` in every session. Scripts read the same map
as JSON from `$DEN_REPOS` (`/home/agent/.den/repos.json`). The map never changes for the life of the
VM, like its mounts: to add a repo, run `den rm <instance>`, then `den up <nest> --repo <path>`.
`DEN_REPOS` belongs to den: an `env:` that sets it is refused.

### Other repos in Claude's context

Claude starts in the start folder and loads the `CLAUDE.md`, skills and agents of that repo only.
The repo map tells it where the other repos are, but not what their `CLAUDE.md` says. With
`add_dirs: true`, `den up` gives Claude every other mounted repo with `--add-dir`, and Claude loads
their `CLAUDE.md` and skills too. A repo that holds the start folder is not added: Claude already
loads it. With `start: .` in a project file, every repo is added.

den passes the folders each time it attaches, after the `--workdir` of that attach. A change to
`add_dirs` therefore applies at the next `den up`, without `den rm`. `den exec`, `den shell` and
`den up --detach` start no Claude session and pass nothing. Each `CLAUDE.md` adds to the context of
every session: on a nest of many repos, keep only the ones Claude needs.

### Sessions

Claude Code keeps each session as a transcript file, and its per-project memory, under
`~/.claude/projects` in the VM. A VM's disk goes with the VM, so den keeps that folder on the host:
`<den home>/sessions/<instance>/`, one folder per instance, mounted read-write at `/den-sessions` in
the VM. At every start of the VM, den makes `~/.claude/projects` a link to it.

```
~/.den/sessions/
  api/                          # the instance
    -Users-me-src-api/          # one folder per start folder, named by Claude
      6f0c….jsonl               # one transcript per session
      memory/                   # Claude's memory for that folder
  api.feature-123/              # a worktree instance has its own
```

- `den rm` never deletes the sessions. It prints where they stay, and removes the folder only when
  it is empty.
- An instance made again under the same name mounts the same folder: `claude --continue` resumes
  the last session of the VM before it. `den up` then prints
  `<instance> loads the sessions kept in <path>`. The name is the only key: a nest that takes the
  name of a removed instance gets its sessions too.
- A `~/.claude/projects` folder that the image ships is copied into the sessions folder at the
  start of the VM, one file at a time; a file the sessions folder already holds is kept.
- `sessions: false` mounts nothing: the sessions then live on the VM's disk and go with it.
- An instance created by a den without this key has no mount. It gets one when you run
  `den rm <instance>`, then `den up <nest>`. That `den rm` deletes the sessions the VM holds: to keep
  them, copy them out first with `sbx cp <instance>:/home/agent/.claude/projects <folder>`.

The VM writes these files: read [security.md](security.md#what-the-vm-can-write) before you feed
them to a tool on your host.

### Merge order

| Setting | Order (later adds or wins) |
|---|---|
| `network.allow` | global file, then template, then nest (all added together) |
| `kits` | template, then nest, then the profile's |
| `env` | global file, then nest (the nest wins per key) |
| `auto_trust` | `true`, then global file, then nest (the last one set wins) |
| `add_dirs` | `false`, then global file, then nest (the last one set wins) |
| `sessions` | `true`, then global file, then nest (the last one set wins) |
| `resources` | sbx's default, then global file, then nest (per key: `cpus`, `memory`) |

den checks only the format of `resources`: `cpus` is a plain decimal whole number of 1 or more, `memory` an integer
then `m` (MiB) or `g` (GiB), such as `512m` or `8g`. sbx checks the bounds.

### Changing a nest

An instance keeps the config it was created with. When the nest changes, `den up` warns and tells
you to run `den rm <instance>` then `den up <nest>`. `add_dirs` is the exception: it applies at the
next `den up`.

### Workspace

sbx gives every VM one main folder, its workspace. den uses a per-instance folder that it creates and
owns, the **run dir**: `<den
home>/run/<instance>/` for a nest of the global file, `<project>/.den/run/<instance>/` for a project
nest. It holds only den's generated files, and `den rm` deletes it without checking it: keep nothing
there. With `start: .` the project folder is the workspace instead, mounted read-write as a whole.

## Network

`network.allow` lists hosts the instance may reach, on top of the global sbx policy that every VM
shares and the hosts its kits allow. It **adds** hosts: it cannot remove a host that the global sbx
policy allows. The whole policy model, and how to check and tighten it, is in
[security.md](security.md#what-the-vm-can-reach).

- Put a host in the nest that needs it. A host in the global file's `network.allow` reaches every
  nest.
- A host without a port allows **every** port: `git.example.com` opens HTTPS and SSH.
  `git.example.com:443` opens only HTTPS.
- `*.example.com` matches one label: `git.example.com`, not `a.b.example.com`. `**.example.com`
  matches any number of labels.

Check that an instance reaches a host: `sbx policy check network api.example.com:443 --sandbox
<instance>` answers `Allowed` or `Denied`.

## Ports

```yaml
ports:
  - {name: web, sandbox: 3000, host: 0}
```

`sandbox` is the port a server listens on inside the VM. `host` is the port on your host; `0` lets
sbx pick a free one. `name` is a label for you. Ports are published when the instance is created;
den prints sbx's port table after each create. Run `sbx port ls <instance>` to see it again. Two
entries with the same `sandbox` port are refused.

## Profiles

A **profile** brings your own Claude configuration (`CLAUDE.md`, `settings.json`, agents, output
styles) into a VM. It is a named list of kits, defined only in the global file:

```yaml
# ~/.den/den.yaml
profiles:
  perso: [~/.den/kits/perso]
profile: perso                 # every nest of this file uses it, unless a nest names another
```

A nest uses the `profile:` of its own file or its own `profile:`. A nest of a project file ignores
the global file's `profile:`: name the profile in the project file.

A profile kit is an ordinary local sbx kit: a folder with a `spec.yaml`, of kind `mixin` (a kit that
adds to the agent instead of defining it). The `spec.yaml` is required; the smallest one is:

```yaml
schemaVersion: "2"
kind: mixin
name: perso
version: 1.0.0
```

Files under `files/home/` are copied into the VM's home, `/home/agent`, when the instance is created:

```
~/.den/kits/perso/
  spec.yaml
  files/home/.claude/CLAUDE.md
  files/home/.claude/settings.json
  files/home/.claude/agents/
  files/home/.claude/skills/
  files/home/.zshrc
```

- The copy happens at create time: after an edit, run `den rm <instance>` then `den up <nest>`.
- Profile kits come after the template's and the nest's kits. A profile that the global file does
  not define is skipped with a warning.
- Ship `settings.json` from one kit only: which kit wins on a collision is unverified.
- den starts Claude with `--dangerously-skip-permissions`. A `claude` you type in `den shell` does
  not get that flag; a `settings.json` with `"permissions": {"defaultMode": "bypassPermissions"}`
  makes it behave the same. See [security.md](security.md).
