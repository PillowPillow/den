# den

den runs Claude Code on your repos inside a Docker Sandboxes ([`sbx`](https://docs.docker.com/ai/sandboxes/))
microVM. You declare a set of repos once in a `den.yaml`; `den up <name>` creates the VM, mounts the
repos at their usual paths, and attaches Claude. `den rm` removes exactly what den created.

**Status: V2 (the second version of den, a rewrite of v1), for macOS and Linux, tested with sbx
v0.53.0.** den drives the experimental `sbx env` commands, which may change. den runs wherever sbx
runs; its day-to-day use so far is on macOS. Coming from v1: read
[docs/migrating-from-v1.md](docs/migrating-from-v1.md) **before** you upgrade.

den is not a sandbox of its own: sbx is the boundary, and den adds no isolation beyond it. den runs
Claude Code only.

## Security summary

Read this before you install. Details: [docs/security.md](docs/security.md).

- Claude runs with `--dangerously-skip-permissions`: it runs commands without asking.
- Claude trusts every folder of the VM (`CLAUDE_CODE_SANDBOXED=1`): it loads a repo's
  `.claude/settings.json` and `.claude/settings.local.json` (hooks, permissions, directories) without
  the trust dialog.
- Your repos are mounted **read-write**, `.git` included. A VM can change hooks, scripts and config
  that your host runs later.
- The VM reaches only the hosts its network policy allows: the **global sbx policy**, shared by every
  VM, plus the hosts of the instance (from your `den.yaml`, the template and the kits). den adds
  hosts; it never removes one.
- sbx's `balanced` policy allows common development hosts, GitHub and GitLab over HTTPS and SSH on
  port 443 included.
- The VM has no SSH key, but it can ask your **SSH agent** to sign with every key loaded in it. All
  VMs share the same agent. See [docs/ssh.md](docs/ssh.md).
- The Claude token stays on your host: sbx injects it into requests to `api.anthropic.com` only.
- den tells sbx to skip the commands an `sbxenv.yaml` can run on your host at create time
  (`lifecycle:` commands, command-backed MCP servers), and mounts the config files it reads read-only
  inside the VM. A layer you add can still run a command on your host (command-based secrets).
- A project `den.yaml` from someone else decides what a VM mounts and reaches: read it before you
  run `den up` on it. Run `den` from outside your repos, where a VM cannot have written a `den.yaml`.

## Prerequisites

Your host needs sbx, an sbx login, a network policy, Claude Code and git: see [docs/setup.md](docs/setup.md#prerequisites).

## Log Claude in

Claude in the VM needs credentials. Store them once in sbx; every VM then uses them.

**Recommended, with a Claude subscription (Pro, Max, Team, Enterprise):** create a long-lived token
and store it as an sbx secret for `api.anthropic.com`.

1. Run `claude setup-token` on your host and copy the token it prints.
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

**Warning: if you use den v1, read [docs/migrating-from-v1.md](docs/migrating-from-v1.md) first.**
Installing V2 replaces the v1 binary, and only v1 can remove the sandboxes v1 created.

Pick one method.

**Homebrew** (macOS, or Linux with Homebrew):

```bash
brew install --cask PillowPillow/tap/den
```

**Install script** (no Homebrew). It verifies the release checksum, refuses to install without that
check, and installs to `~/.local/bin`:

```bash
curl -fsSL https://raw.githubusercontent.com/PillowPillow/den/main/install.sh | sh
```

Put a setting on `sh`, not on `curl`: `… | DEN_VERSION=v2.0.0 sh` pins a version,
`… | DEN_INSTALL_DIR=~/bin sh` changes the destination.

**Go toolchain** (Go 1.26 or later). The path ends in `/v2`:

```bash
go install github.com/PillowPillow/den/v2/cmd/den@latest
```

The path without `/v2` installs v1.

**From source** (Go 1.26 or later and [go-task](https://taskfile.dev)):

```bash
git clone https://github.com/PillowPillow/den.git
cd den
task build
mkdir -p ~/.local/bin
ln -sf "$PWD/den" ~/.local/bin/den
```

Then check:

```bash
command -v den
den --help
```

`den --help` must list `build`, `exec`, `ls`, `rm`, `scan`, `shell`, `up` and `version` (plus `completion`
and `help`). If it lists `doctor` or `init`, your `PATH` finds den v1 first: remove that binary, or
put the directory of the V2 binary first in your `PATH`. `den version` prints `den v2.…`.

To update, run the command you installed with again: `brew upgrade --cask den`, the install script,
`go install …@latest`, or `git pull` and `task build` in the clone.

## Words den uses

| Word | Meaning |
|---|---|
| **den home** | the folder that holds den's config and state: `~/.den` (`--den-home` or `$DEN_HOME` to change it) |
| **nest** | a named set of repos and settings, declared in a `den.yaml` |
| **instance** | the sbx VM den creates from a nest, named `<nest>` (or `<nest>.<branch>` with `-w`) |
| **template** | the image a VM starts from: an official Docker sandbox image, or one you build with `den build` |
| **kit** | an sbx package of files, network rules and settings added to a VM |
| **profile** | a list of kits that brings your own Claude configuration into a VM |

## With Claude Code

```bash
claude plugin marketplace add PillowPillow/den
claude plugin install den@den
```

Then run `/den:setup` in Claude Code. The plugin serves the skills of den's latest release.

To declare the marketplace for a whole team, add this to the project's `.claude/settings.json`:

```json
{
  "extraKnownMarketplaces": {
    "den": { "source": { "source": "github", "repo": "PillowPillow/den" } }
  }
}
```

The manual path below stays available.

## First run

1. Create `~/.den/den.yaml` from the example in this repository:

   ```bash
   mkdir -p ~/.den
   curl -fsSL https://raw.githubusercontent.com/PillowPillow/den/main/examples/den-home/den.yaml -o ~/.den/den.yaml
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
   attaches Claude, in your repo. The repo is at the **same absolute path** in the VM as on your host,
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
- **Reach a server in the VM from your host:** `ports:` on the nest. See
  [docs/den-yaml.md](docs/den-yaml.md#ports).
- **Bring your Claude configuration** (`CLAUDE.md`, settings, skills): a profile. See
  [docs/den-yaml.md](docs/den-yaml.md#profiles).
- **Mount several repos, some optional:** see [docs/den-yaml.md](docs/den-yaml.md#repos).
- **Declare a repo by its git origin, and let den find the clone:** `repos:` keys in a project
  `den.yaml`, then `den scan ~/src`. See [docs/den-yaml.md](docs/den-yaml.md#repos) and
  [docs/commands.md](docs/commands.md#den-scan).
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

Symptoms and fixes for the common failures: see [docs/setup.md](docs/setup.md#troubleshooting).

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
6. Remove the binary the way you installed it: `brew uninstall --cask den`; or `rm ~/.local/bin/den`
   (install script, or the link to a clone, then delete the clone); or `rm "$(go env GOPATH)/bin/den"`
   (`go install`). Remove any `PATH` line you added to your shell's rc file.

## Documentation

| Page | Content |
|---|---|
| [docs/setup.md](docs/setup.md) | prerequisites and troubleshooting |
| [docs/den-yaml.md](docs/den-yaml.md) | every `den.yaml` key: nests, repos, network, ports, profiles |
| [docs/commands.md](docs/commands.md) | every command and option, worktrees |
| [docs/templates.md](docs/templates.md) | official images, building your own template |
| [docs/security.md](docs/security.md) | what a VM can do, what den protects, layers |
| [docs/ssh.md](docs/ssh.md) | SSH and git over SSH in the VM |
| [docs/files.md](docs/files.md) | what den writes on disk |
| [docs/migrating-from-v1.md](docs/migrating-from-v1.md) | moving from den v1 |
