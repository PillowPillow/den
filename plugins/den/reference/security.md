# Security model

den runs Claude Code **without permission prompts** inside an sbx microVM. The VM is the boundary:
den adds no isolation of its own beyond what sbx enforces. This page says what a VM can do on your
machine, what den protects, and what it accepts.

Words used here: a **nest** is what you declare in a `den.yaml`; an **instance** is the VM den
creates from it; a **kit** is an sbx package of files, network rules and settings added to a VM; a
**template** is the image a VM starts from. See the [README](../README.md#words-den-uses).

- [What the VM can do](#what-the-vm-can-do)
- [What the VM can write](#what-the-vm-can-write)
- [What the VM can reach](#what-the-vm-can-reach)
- [What the VM can sign](#what-the-vm-can-sign)
- [Secrets](#secrets)
- [What den protects](#what-den-protects)
- [Layers](#layers)
- [Templates from someone else](#templates-from-someone-else)
- [Accepted residual risks](#accepted-residual-risks)

## What the VM can do

den starts Claude with `--dangerously-skip-permissions`: Claude runs every command and edits every
file it decides to, without asking. Anything that can steer Claude can steer those commands: a
prompt, a file in a repo, a web page, a package it installs. Assume that whatever is in the VM can be
read, changed or sent to a host the VM may reach.

The VM sees only what sbx gives it: the folders den mounts, the network its policy allows, your SSH
agent, the environment variables of the nest (`env:`), and the sbx integrations that are on.
`sbx settings ls` lists the integrations; for example `browser.enabled` (on by default) lets a
process in the VM open a URL in your host browser. Turn one off with
`sbx settings set <key> false`. Ports you publish (`ports:`) expose a server of the VM on your host.

### Folder trust

With `auto_trust: true` (the default, see [den-yaml.md](den-yaml.md#every-key)), den makes Claude
trust the folders of the instance without asking. It does two things:

- It sets `CLAUDE_CODE_SANDBOXED=1`, an undocumented Claude Code switch. Claude never shows its trust
  dialog and loads a repo's `.claude/settings.json` and `.claude/settings.local.json` hooks without
  asking. The switch does not record trust.
- At every VM start, it writes the trust entry that Claude writes when you accept the dialog,
  `projects["<folder>"].hasTrustDialogAccepted: true` in `~/.claude.json`, for each mounted repo
  and for the start folder. den lists those folders in `~/.den/trust.json` at create.

Claude Code 2.1.330 ignores a project's `permissions.additionalDirectories` until that entry exists.
With the entry, Claude gets **file access** to those folders. It does not load their `CLAUDE.md`,
skills or agents: only `claude --add-dir <folder>` does that, which `add_dirs: true` turns on
([den-yaml.md](den-yaml.md#other-repos-in-claudes-context)).

With `auto_trust: false`, den does neither. Claude shows its trust dialog when you attach in a
folder it does not trust yet. Claude Code loads a repo's settings only after you accept it; this
was not measured in a den VM. `claude -p` (for example `den exec <nest> -- claude -p …`) shows no
dialog: it runs, warns that the workspace is not trusted, and ignores the project's `permissions.additionalDirectories`.

## What the VM can write

**Every repo of the nest is mounted read-write.** A VM can therefore change any file that your host
later runs or trusts:

- `.git/hooks` and `.git/config`: git on your host runs those hooks the next time you commit there.
  With `-w` the worktree's own `.git` file is read-only, but the repo's git directory is mounted
  read-write at `/den-git/<repo>` so that Claude can commit: `-w` does not remove this risk.
- any script you run on the host: `package.json` scripts, a `Taskfile.yml` or `Makefile`, `.envrc`,
  `.claude/settings.json` hooks and `CLAUDE.md` read by a Claude Code on your host.

This is an accepted risk. After a session you do not trust, review the diff of the repo **and** its
`.git/hooks` and `.git/config` before you run anything there.

The VM can also write in its run dir (deleted by `den rm`), in any folder you add with `--repo`, and,
for a nest with `start: .`, in the whole project folder.

## What the VM can reach

The VM reaches only the hosts its network policy allows. The policy has two parts:

1. **The global sbx policy** (`local-policy`), shared by every sbx VM on the machine. den recommends
   `sbx policy init balanced`, which allows a list of common development hosts (AI services, package
   registries, GitHub, ...). `allow-all` allows everything: with it, no den setting restricts
   anything. `sbx policy init` is a one-time setup; to change the preset later, run
   `sbx policy reset` (it stops the running VMs), then `sbx policy init balanced`. When `den up`
   creates a VM, it warns about each global rule that sbx did not create and about a rule that
   allows every host; it does not change the policy.
2. **The instance's rules**: the `network.allow` hosts of the global `den.yaml`, the template and the
   nest, plus the hosts that each kit of the instance allows (the `den-claude` kit allows
   `api.anthropic.com:443`; a kit from a template or a profile can add more). See
   [den-yaml.md](den-yaml.md#network).

den adds hosts; it never removes a host the global policy allows. A host without a port allows every
port.

**Check what an instance reaches:**

```bash
sbx policy check network git.example.com:22 --sandbox <instance>
sbx policy ls <instance>
```

- `sbx policy check network git.example.com:22 --sandbox <instance>` answers `Allowed` or `Denied`.
- `sbx policy ls <instance>` prints a summary of its policies.

**Which preset is active.** `sbx policy check network example.com:443` answers `Denied` with
`balanced` or `deny-all`, and `Allowed` with `allow-all` (or a rule that allows every host).

**Tighten it:**

- **Rules added by hand.** `sbx policy ls --wide` lists every rule; `den up` names the rules added
  by hand when it creates a VM. Rules that sbx created carry an id that starts with `default-`. A
  `local-policy` rule with any other id was added by hand (`sbx policy allow network <host>`) and
  opens that host to every VM, den's or not. Remove one with
  `sbx policy rm network --id <rule id>`, and put the host in the nest that needs it instead.
- **Block a host for one instance.** `sbx policy deny network <host> --sandbox <instance>`. A deny
  rule wins over every allow rule.

## What the VM can sign

The VM has no SSH key. sbx forwards your host's SSH agent to every running VM, and the VM can ask it
to sign with every key it holds. All running VMs share the same agent. The network policy limits
where a VM can use a signature. Turn forwarding off for every VM with
`sbx settings set ssh.agentForwardingEnabled false`. See [ssh.md](ssh.md).

## Secrets

The recommended way to log Claude in stores a token with `sbx secret set-custom` (see the
[README](../README.md#log-claude-in)). sbx keeps the token on your host and puts a placeholder in the
VM; the sbx proxy replaces the placeholder in requests to `api.anthropic.com`. The token never
enters the VM, but the VM can use it toward `api.anthropic.com` for as long as it runs. Any value in
`env:` does enter the VM.

## What den protects

- **No lifecycle command from a VM.** den passes `--skip-host-commands` to every `sbx env create` and
  `sbx env rm`. A `lifecycle:` command never runs on your host, and a command-backed MCP server (an
  MCP server that sbx would start as a command on your host) is disabled. This does **not** cover
  command-based secrets: see [Layers](#layers).
- **Read-only config inside the VM.** Some files decide what the next create gives a VM: the
  `den.yaml` files (den reads them), the local kits and the [layers](#layers) (sbx reads them at each
  create and remove), the template's build scripts. A kit holds a network allowlist. When one of
  these files lies in a folder the VM can write (a repo, the project folder), den mounts it read-only
  over that folder, at the path the VM sees. The host enforces this even against root in the VM.
  den refuses a file whose path goes through a symlink inside a mounted folder (the VM could replace
  the link): write its real path.
- **Records, not guesses.** den writes its record before it creates the VM, and `den rm` removes
  only that. It never adopts or removes a VM it did not create. If the generated `sbxenv.yaml` or a
  layer changed since the create, `den rm` removes the VM with `sbx rm --force` instead of
  `sbx env rm`, so sbx does not act on a file the VM may have rewritten.

## Layers

`layers:` on a nest (in either `den.yaml`) lists extra `sbxenv.yaml` files that sbx stacks after the
one den generates. This is how a nest reaches sbx fields den does not model: `secrets:`, `mcp:`,
`sandboxOptions.memory`, and so on. den never parses a layer; sbx merges it, and a later file wins
over den's.

```yaml
nests:
  api:
    layers: [~/.den/layers/api.yaml]
```

**A layer has full power over the VM.** It can add mounts, make a protected file writable again,
and declare command-based secrets, which sbx runs **on your host** at create time even with
`--skip-host-commands`. Treat a layer like a script you run: write it yourself, keep it outside your
repos (`~/.den/layers/`), and keep any helper it runs outside every folder a VM can write. A layer
inside a mounted folder is mounted read-only, like the files above. The schema of `sbxenv.yaml` is
sbx's, and `sbx env` is experimental in sbx v0.53.0.

## Templates from someone else

`den build` on a template from a git repository (`repo:`) runs that repository's scripts in a build
VM, and the template's kits and network rules then apply to every instance that uses it. den asks
before it clones a URL that only a project `den.yaml` declares. Read a template repository before
you build it, as you would read a script before you run it. See [templates.md](templates.md).

## Accepted residual risks

- A VM changes files in every repo it mounts, `.git` included, that your host later runs. See
  [What the VM can write](#what-the-vm-can-write).
- The read-only mount holds only inside the instance that reads the file. Any other VM that mounts
  the same folder read-write (another nest that shares the repo, or an sbx VM den did not create)
  can still write it.
- A VM can create a **new** `den.yaml` in a repo it mounts. den takes the first `den.yaml` it finds
  walking up from the current directory, so running `den` from inside that repo would read the
  VM's file (its repos, kits and layers). Run `den` from outside the repos, or check for a
  `den.yaml` you did not write.
- A VM can sign with every key in your SSH agent, toward every host its policy allows. See
  [ssh.md](ssh.md#accepted-residual-risks).
- A host in the global sbx policy or in the global `den.yaml` is open to every VM.
- The VM can use the Claude token toward `api.anthropic.com` while it runs.
- With `auto_trust: true`, a repo's `.claude/settings.json` runs its hooks in the VM without asking,
  and the trust entry den writes may turn on other Claude Code features that wait for trust. No one
  has listed those features for Claude Code 2.1.330. They all run inside the VM, which already runs
  Claude with `--dangerously-skip-permissions`. Set `auto_trust: false` on a nest whose repos you
  do not trust.
