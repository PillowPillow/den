# den

den runs Claude Code on your repos inside a Docker Sandboxes ([`sbx`](https://docs.docker.com/ai/sandboxes/))
microVM. You declare a set of repos once in a `den.yaml`; `den up <name>` creates the VM, mounts the
repos at their usual paths, and attaches Claude. `den rm` removes exactly what den created.

**Status: pre-release V2 (the second version of den, a rewrite of v1), macOS only, tested with sbx
v0.53.0.** den drives the experimental `sbx env` commands, which may change. Linux is not supported
yet.

den is not a sandbox of its own: sbx is the boundary, and den adds no isolation beyond it. den runs
Claude Code only.

## Security summary

Read this before you install. Details: [docs/security.md](docs/security.md).

- Claude runs with `--dangerously-skip-permissions`: it runs commands without asking.
- Claude trusts every folder of the VM (`CLAUDE_CODE_SANDBOXED=1`): it loads a repo's
  `.claude/settings.json` and `.claude/settings.local.json` (hooks, permissions, directories) without
  the trust dialog.
- Your repos are mounted **read-write**, `.git` included. A VM can change hooks, scripts and config
  that your Mac runs later.
- The VM reaches only the hosts its network policy allows: the **global sbx policy**, shared by every
  VM, plus the hosts of the instance (from your `den.yaml`, the template and the kits). den adds
  hosts; it never removes one.
- sbx's `balanced` policy allows common development hosts, GitHub and GitLab over HTTPS and SSH on
  port 443 included.
- The VM has no SSH key, but it can ask your **SSH agent** to sign with every key loaded in it. All
  VMs share the same agent. See [docs/ssh.md](docs/ssh.md).
- The Claude token stays on your Mac: sbx injects it into requests to `api.anthropic.com` only.
- den tells sbx to skip the commands an `sbxenv.yaml` can run on your Mac at create time
  (`lifecycle:` commands, command-backed MCP servers), and mounts the config files it reads read-only
  inside the VM. A layer you add can still run a command on your Mac (command-based secrets).
- A project `den.yaml` from someone else decides what a VM mounts and reaches: read it before you
  run `den up` on it. Run `den` from outside your repos, where a VM cannot have written a `den.yaml`.

## Prerequisites

| Need | Install | Check |
|---|---|---|
| macOS 14 (Sonoma) or later, Apple silicon (sbx requires both) | | `sw_vers -productVersion` and `uname -m` (`arm64`) |
| Homebrew, to install the tools below | [brew.sh](https://brew.sh) | `brew --version` |
| sbx v0.53.0 or later | `brew install docker/tap/sbx` | `sbx version` |
| sbx signed in (Docker account) | `sbx login` | `sbx ls` prints a table, not an error |
| sbx global network policy | `sbx policy init balanced` (once) | `sbx policy ls` lists `local-policy` |
| Claude Code on your Mac, for the token | [Claude Code setup](https://code.claude.com/docs/en/setup) | `claude --version` |
| Go 1.26 or later | `brew install go` | `go version` |
| go-task | `brew install go-task/tap/go-task` | `task --version` |
| git | `xcode-select --install` | `git --version` |

`sbx policy init` is a one-time setup that picks a preset, a starting rule set for every VM. If
`sbx policy ls` already lists `local-policy`, check the preset: `sbx policy check network
example.com:443` must answer `Denied`. `Allowed` means `allow-all`, and then no den setting restricts
the network ([docs/security.md](docs/security.md#what-the-vm-can-reach)).

## Log Claude in

Claude in the VM needs credentials. Store them once in sbx; every VM then uses them.

**Recommended, with a Claude subscription (Pro, Max, Team, Enterprise):** create a long-lived token
and store it as an sbx secret for `api.anthropic.com`.

1. Run `claude setup-token` on your Mac and copy the token it prints.
2. Store it without leaving it in your shell history. The first line waits: paste the token (it is
   not shown), then press Enter.

   ```bash
   read -rs TOKEN
   printf '%s' "$TOKEN" | sbx secret set-custom --host api.anthropic.com --env CLAUDE_CODE_OAUTH_TOKEN --value-stdin
   unset TOKEN
   ```

3. Check: `sbx secret ls` lists a custom secret for `api.anthropic.com`.

sbx gives the VM a placeholder in `CLAUDE_CODE_OAUTH_TOKEN` and replaces it with the token only in
requests to `api.anthropic.com`. The token never enters the VM.

**Last resort: `/login` in the VM.** Without a stored token, Claude asks you to log in when it
starts. That login lives in the VM only: you log in again every time the instance is created again.
Unverified: whether the `balanced` policy allows every host the `/login` flow uses.

## Install

Build V2 from the `v2` branch. Homebrew, `install.sh` and `go install …@latest` install **v1**, a
different program that also answers `den`.

```bash
git clone https://github.com/PillowPillow/den.git
cd den
git checkout v2
task build
mkdir -p ~/.local/bin
ln -sf "$PWD/den" ~/.local/bin/den
```

`task build` writes the binary `./den`; the link puts it on your `PATH` as `den`. Make sure
`~/.local/bin` is on your `PATH`: if `command -v den` prints nothing, add
`export PATH="$HOME/.local/bin:$PATH"` to `~/.zshrc` and open a new terminal. Then check:

```bash
command -v den
den --help
```

`command -v den` must print `~/.local/bin/den` (expanded). `den --help` must list `build`, `exec`,
`ls`, `rm`, `shell`, `up` and `version` (plus `completion` and `help`). If it lists `doctor` or
`init`, your `PATH` finds den v1 first (for example `/opt/homebrew/bin/den` from Homebrew). Either
remove v1 (`brew uninstall --cask den`), or put `~/.local/bin` first: add
`export PATH="$HOME/.local/bin:$PATH"` as the last `PATH` line of `~/.zshrc` and open a new
terminal. `den version` prints a line such as `den v1.13.1-94-g3468e6f`:
V2 is built on top of the last v1 tag, so trust `den --help`, not the number. To update, run
`git pull` and `task build` in the clone.

## Words den uses

| Word | Meaning |
|---|---|
| **den home** | the folder that holds den's config and state: `~/.den` (`--den-home` or `$DEN_HOME` to change it) |
| **nest** | a named set of repos and settings, declared in a `den.yaml` |
| **instance** | the sbx VM den creates from a nest, named `<nest>` (or `<nest>.<branch>` with `-w`) |
| **template** | the image a VM starts from: an official Docker sandbox image, or one you build with `den build` |
| **kit** | an sbx package of files, network rules and settings added to a VM |
| **profile** | a list of kits that brings your own Claude configuration into a VM |

## First run

1. Create `~/.den/den.yaml` from the example shipped in your clone (replace `<den clone>` with its
   path):

   ```bash
   mkdir -p ~/.den
   cp <den clone>/examples/den-home/den.yaml ~/.den/den.yaml
   ```

   It declares one nest, `demo`, on the official image `claude-code-docker`:

   ```yaml
   schema: 1

   default_template: claude-code-docker

   nests:
     demo:
       repos:
         - path: ~/src/demo   # any git repository on your machine
   ```

2. Replace `~/src/demo` with the path of a repository of yours.
3. Run:

   ```bash
   den up demo
   ```

   The first run pulls the image, which can take a few minutes. den creates the instance `demo` and
   attaches Claude, in your repo. The repo is at the **same absolute path** in the VM as on your Mac,
   and Claude's edits land directly in your files.
4. Quit Claude. The instance stays; `den up demo` attaches again.
5. Look around and clean up:

   ```bash
   den shell demo
   den ls
   den rm demo
   ```

   `den shell demo` opens a bash shell in the VM. `den ls` lists the instances den created.
   `den rm demo` removes the VM and what den created for it; your repo stays.

## Next steps

- **Work on a branch without touching your checkout:** `den up demo -w feature/x` runs on a git
  worktree (a second checkout of the same repo, on another branch). See [docs/commands.md](docs/commands.md#worktrees).
- **Open a host to a nest:** `network.allow` on the nest. See
  [docs/den-yaml.md](docs/den-yaml.md#network).
- **Reach a server in the VM from your Mac:** `ports:` on the nest. See
  [docs/den-yaml.md](docs/den-yaml.md#ports).
- **Bring your Claude configuration** (`CLAUDE.md`, settings, skills): a profile. See
  [docs/den-yaml.md](docs/den-yaml.md#profiles).
- **Mount several repos, some optional:** see [docs/den-yaml.md](docs/den-yaml.md#repos).
- **Install your tools once for every VM:** build a template. See
  [docs/templates.md](docs/templates.md#build-your-own-template).
- **Share nests with a team:** a project `den.yaml`. See
  [docs/den-yaml.md](docs/den-yaml.md#project-file).
- **Use git over SSH in the VM:** see [docs/ssh.md](docs/ssh.md).
- **Set sbx fields den does not model** (secrets, MCP servers, memory): layers, extra
  `sbxenv.yaml` files that sbx merges after den's. See
  [docs/security.md](docs/security.md#layers).

## Commands

| Command | What it does |
|---|---|
| `den up <nest> [-w <branch>]` | create the instance if needed, then attach Claude |
| `den exec <nest> -- <cmd>` | create the instance if needed, run one command, return its exit code |
| `den shell [<instance>]` | open `bash -l` in an instance |
| `den ls` | list the instances den created and the state of their VM |
| `den rm <instance> [--force]` | remove an instance: its VM, the worktrees den made, its files |
| `den build <template>` | build a template |
| `den version` | print den's version |

Every option: [docs/commands.md](docs/commands.md).

## Troubleshooting

| Symptom | Fix |
|---|---|
| den answers with commands such as `doctor`, or messages that this README does not describe | your `PATH` finds den v1: remove it or put `~/.local/bin` first, see [Install](#install) |
| `den up`, `den ls` or another command fails with an sbx error about signing in | sbx is not signed in: `sbx login` |
| `sbx <version> is older than v0.53.0` | upgrade sbx: `brew upgrade docker/tap/sbx` |
| Claude in the VM says `Not logged in · Please run /login` | no token stored: see [Log Claude in](#log-claude-in) |
| `nest "<nest>" has no template` | add `default_template: claude-code-docker` to `~/.den/den.yaml` |
| a host is unreachable from the VM | `sbx policy check network <host>:<port> --sandbox <instance>`, then add the host to the nest's `network.allow` ([docs/den-yaml.md](docs/den-yaml.md#network)) |
| `den-claude: the image has no jq` | the template's image lacks `jq`: see [docs/templates.md](docs/templates.md#the-image-must-provide-jq) |
| every `den exec` or build step dies with status 137 | the image's entrypoint (its first process) exits: see [docs/templates.md](docs/templates.md#the-image-must-keep-the-vm-alive) |
| `a sandbox named "<name>" exists but den V2 did not create it` | an sbx VM with that name exists (a v1 VM, say): save its work, then `sbx rm <name>` |
| `Permission denied (publickey)` in the VM | your key is not loaded in the SSH agent: see [docs/ssh.md](docs/ssh.md#troubleshooting) |
| `den up` warns that a file changed since the instance was created | the instance keeps its old config: `den rm <instance>`, then `den up` again |
| `repo … is not the root of a git repository, and -w makes a worktree of every repo` | drop `-w`, or point the repo at the root of its git repository ([docs/commands.md](docs/commands.md#worktrees)) |

## Uninstall

**Warning: deleting `~/.den` also deletes den v1's data, if you use v1.**

1. Remove every instance: run `den ls`, then `den rm <instance>` for each one.
2. Remove the images: `sbx template ls`, then `sbx template rm <repository>:<tag>` for each template
   you built (tag `v2`) and each official image den pulled
   (`docker.io/docker/sandbox-templates:claude-code-docker`).
3. Remove the Claude token: `sbx secret ls` shows its placeholder; run
   `sbx secret rm --placeholder <placeholder>`.
4. Remove the network rules you added by hand: `sbx policy ls --wide`, then
   `sbx policy rm network --id <rule id>`.
5. Remove the den home: `rm -rf ~/.den`. Remove the `.den/` folder of each project that has a
   `den.yaml`, and of each repo you used with the `per-repo` worktree layout.
6. Remove the binary: `rm ~/.local/bin/den`, delete the clone, and remove the `PATH` line you added
   to `~/.zshrc`.

## Documentation

| Page | Content |
|---|---|
| [docs/den-yaml.md](docs/den-yaml.md) | every `den.yaml` key: nests, repos, network, ports, profiles |
| [docs/commands.md](docs/commands.md) | every command and option, worktrees |
| [docs/templates.md](docs/templates.md) | official images, building your own template |
| [docs/security.md](docs/security.md) | what a VM can do, what den protects, layers |
| [docs/ssh.md](docs/ssh.md) | SSH and git over SSH in the VM |
| [docs/files.md](docs/files.md) | what den writes on disk |
| [docs/migrating-from-v1.md](docs/migrating-from-v1.md) | moving from den v1 |
