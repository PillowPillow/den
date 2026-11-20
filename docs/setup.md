# Host setup and troubleshooting

## Prerequisites

Your **host** is the machine you run den on. It must be one that sbx supports
([sbx requirements](https://docs.docker.com/ai/sandboxes/install/)):

- **macOS** 14 (Sonoma) or later, Apple silicon. Check: `sw_vers -productVersion` and `uname -m`
  (`arm64`).
- **Linux**: Ubuntu 24.04 or later, amd64 or arm64, with KVM turned on and your user in the `kvm`
  group. Check: `lsmod | grep kvm` and `id -nG` (lists `kvm`).

| Need | Install | Check |
|---|---|---|
| sbx v0.53.0 or later | macOS: `brew install docker/tap/sbx`; Ubuntu: see the [sbx install page](https://docs.docker.com/ai/sandboxes/install/) | `sbx version` |
| sbx signed in (Docker account) | `sbx login` | `sbx ls` prints a table, not an error |
| sbx global network policy | `sbx policy init balanced` (once) | `sbx policy ls` lists `local-policy` |
| Claude Code on your host, for the token | [Claude Code setup](https://code.claude.com/docs/en/setup) | `claude --version` |
| git | macOS: `xcode-select --install`; Ubuntu: `sudo apt install git` | `git --version` |

`sbx policy init` is a one-time setup that picks a preset, a starting rule set for every VM. If
`sbx policy ls` already lists `local-policy`, check the preset: `sbx policy check network
example.com:443` must answer `Denied`. `Allowed` means `allow-all`, and then no den setting restricts
the network ([docs/security.md](security.md#what-the-vm-can-reach)).

## Troubleshooting

| Symptom | Fix |
|---|---|
| den answers with commands such as `doctor`, or messages that the README does not describe | your `PATH` finds den v1: remove it or put the V2 binary's directory first, see [Install](../README.md#install) |
| `go install github.com/PillowPillow/den/cmd/den@latest` installs v1 | that path is v1's: use `github.com/PillowPillow/den/v2/cmd/den@latest` |
| `den up`, `den ls` or another command fails with an sbx error about signing in | sbx is not signed in: `sbx login` |
| `sbx <version> is older than v0.53.0` | upgrade sbx: `brew upgrade docker/tap/sbx` on macOS, `sudo apt install --only-upgrade docker-sbx` on Ubuntu |
| Claude in the VM says `Not logged in · Please run /login` | no token stored: see [Log Claude in](../README.md#log-claude-in) |
| `nest "<nest>" has no template` | add `default_template: claude-code-docker` to `~/.den/den.yaml` |
| a host is unreachable from the VM | `sbx policy check network <host>:<port> --sandbox <instance>`, then add the host to the nest's `network.allow` ([docs/den-yaml.md](den-yaml.md#network)) |
| `den-claude: the image has no jq` | the template's image lacks `jq`: see [docs/templates.md](templates.md#the-image-must-provide-jq) |
| every `den exec` or build step dies with status 137 | the image's entrypoint (its first process) exits: see [docs/templates.md](templates.md#the-image-must-keep-the-vm-alive) |
| `a sandbox named "<name>" exists but den V2 did not create it` | an sbx VM with that name exists (a v1 VM, say): save its work, then `sbx rm <name>` |
| `Permission denied (publickey)` in the VM | your key is not loaded in the SSH agent: see [docs/ssh.md](ssh.md#troubleshooting) |
| `den up` warns that a file changed since the instance was created | the instance keeps its old config: `den rm <instance>`, then `den up` again |
| `repo … is not the root of a git repository, and -w makes a worktree of every repo` | drop `-w`, or point the repo at the root of its git repository ([docs/commands.md](commands.md#worktrees)) |
