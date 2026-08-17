# Changelog

Every released version, newest first. Each section is written by hand and is also the body
of that version's annotated tag and of its GitHub release — one text, three places. Cut a
release with `/release`.

Lines describe what changed for someone using den. The commit history is in the repo; it is
not repeated here.

## Unreleased

### Changed
- **Breaking:** `den spawn` is gone, with no alias. `den up <nest>` creates or re-attaches and
  opens a shell; `den run <nest> <cmd> [args...]` does the same and runs a command instead,
  exiting with its status. Ad-hoc repos move from trailing positionals to a repeatable
  `--repo <path>` — a shell glob expands before den sees it, so `den spawn scratch ~/dev/proj-*`
  becomes `repos=(~/dev/proj-*); den up --repo=${^repos} scratch` (zsh) or a loop building
  `--repo` pairs (portable). `--` leaves the family: `den run`'s own flags sit left of the nest
  name, and everything from the command on is passed through verbatim, the way `den exec`
  already worked.

## v1.7.0 — 2026-08-16

### Added
- Declarative sources. A team source carrying `den-source.yaml` at its root declares what it needs
  from a machine — credentials, build egress, stack builds, repositories — and `den init --source
  <url>` converges all of it in one command. den prints the plan and applies nothing before you
  confirm it; `--yes` is the answer of someone who has read one before, and with no terminal and no
  `--yes` den prints the plan, applies nothing and exits zero. The resource vocabulary is closed
  (`sbx_github`, `sbx_registry`, `sbx_http_substitution`): a manifest names no shell command, no
  hook and no plugin, and an unknown type is refused rather than ignored. A source without
  `den-source.yaml` behaves exactly as before.
- `den source configure <name>` reconverges an installed declarative source without contacting its
  remote — it maps a repo cloned since, or finishes an interrupted run. `den source status [name]`
  reports what the source needs against what this machine has, and exits non-zero on `blocked` and
  `unknown` only. `den doctor` reports every declarative source with the same verdict.
- `--answers <file>` supplies the answers of one run — where to look for working repos, credentials
  by environment variable **name**, explicit repo mappings — so onboarding runs with no terminal.
  den stores none of it, and a literal credential value in that file is refused. A repository is
  mapped only on a fact: you named the directory, or exactly one directory under the roots carries
  the declared remote. den never clones.
- `den shell <name>` opens the login shell in a live sandbox. `-T` is refused on it by name, since
  a login shell needs a terminal.
- `--as <label>` names the instance, so several sandboxes of one nest run side by side:
  `den spawn api --as analyse-a`. It renames the sandbox only — a worktree directory keeps the name
  `-w` gave it, or two nests spawned `--as x` would collide on it.
- `select: prompt` on a nest declares no default selection: its repos are chosen at spawn time from
  a checklist that opens without `-i` and starts empty. `--only` states the set outright from a
  script or CI, `--without` is refused for want of a default to subtract from, and an unmapped
  **optional** key costs nothing — `den doctor` stays green and `den nest show` lists the key with
  the `repos:` line to add. What you decline is recorded, so re-attaching does not reopen the
  checklist.

### Changed
- **`den exec` takes the compose shape, and this breaks the old spelling.** It now requires a
  command and no longer accepts `--`: write `den exec api go build`, not
  `den exec api -- go build`. den's own flags — `-T`, `--workdir`, `--den-home` — go **before** the
  sandbox name; everything after it is the command, verbatim, its own flags included, so
  `den exec api --help` asks the program inside the sandbox rather than den. The login shell that
  `den exec <name>` opened with no command is now `den shell <name>`. Every refusal proposes a line
  den itself accepts.
- `den source add` and `den source update` converge a **declarative** source, where they used to
  clone and fast-forward only. `add` on an existing den home now runs the same plan-and-confirm as
  `den init --source`; `update` converges to the version the source *publishes* — a greater
  `metadata.version` gets a full plan and a confirmation, the same version on a new commit is
  reported and applied to nothing, and a lower version is refused with the checkout untouched. A
  source without `den-source.yaml` is unaffected on both commands.
- The shell and the command start in the directory you ran `den` from, when the sandbox mounts it;
  the first workspace stays the fallback. `den spawn api` from `~/dev/api/internal/spawn` opened
  `~/dev/api` and you typed `cd` back every time.
- `den ls` gains an INSTANCE column, and its WORKTREE column reads the sandbox's creation record
  instead of deriving a branch from the sandbox name — under `--as` that name component is a label,
  not a branch, and guessing printed one that never existed.

### Fixed
- den allocates a terminal only when there is a real terminal. The probe accepted any character
  device, so `den exec < /dev/null` with a terminal on stdout still asked for a pty; the sandbox
  then discarded the command's output while reporting success. den now asks the kernel for the
  descriptor's termios attributes.

## v1.6.0 — 2026-08-11

### Added
- `den exec <name> -- <cmd> [args...]` runs one command in a live sandbox and exits with that
  command's own status. den had exactly one door into a sandbox and it was interactive, so a CI
  step, a task target or a script had to drive a shell — or bypass den, losing the sandbox name
  resolution, the agent freshness gate and the ssh-agent warning den owns.
- `-T` on `den exec` never allocates a terminal, for pipes and CI; it is refused with no command
  after `--`, because a shell needs one. `--workdir <path>` sets the directory the command runs
  in, defaulting to the first workspace the sandbox reports.
- `den spawn <nest> -- <cmd> [args...]` does the same on the way in: create or re-attach, run the
  command, exit with its status. It takes the same `-T` and `--workdir`, and is refused together
  with `--detach` — `--detach` spawns without entering the sandbox, and a command has to run
  inside it.

### Changed
- den allocates a terminal only when stdin **and** stdout are both terminals. The old check read
  stdin alone, and `< /dev/null` — the canonical CI and cron stdin — passed it; the sandbox then
  discarded the command's output while still returning a success status. Consequence on an
  existing flag: `den spawn -i`'s checklist now takes its clean refusal whenever stdout is
  redirected, even with a real terminal on stdin.

### Fixed
- `den spawn <nest> -T -- <cmd> > out.txt` no longer writes den's own progress lines into the file
  the command owns. That chatter goes to stderr, as it already did on `den exec`.

### Removed
- `den sh` is gone. Use `den exec <name>`, which opens the same shell when no command is given. No
  alias was kept, so `den sh api` prints den's unknown-command listing — the listing that follows
  it names every command, `exec` included.

## v1.5.0 — 2026-08-10

### Added
- On attach, `den spawn` warns when the live sandbox is missing a mount that your config now
  declares — a `mounts:` entry, or the directory behind `ssh.mode: mount`. den never reapplies
  anything to a running VM, so the warning is the whole remedy: recreate the sandbox when you
  want the new mount. The check reads what the VM actually has, in one direction only — a mount
  you *removed* from the config is not reported, because a live workspace list cannot tell a
  removal from a moved worktree.
- A mount that flipped between `ro: true` and `ro: false` now names both sides instead of
  claiming the mount is absent, and the warning header names the block you wrote — `mounts:`
  or `ssh.dir`.

### Fixed
- `den spawn` works again on sbx v0.38.0, which renamed `caps:` to `permissions:` and
  `commands:` to `setup:` inside `schemaVersion: 2`. The old spelling is a hard refusal at
  `sbx create`, before any VM exists, so every spawn died on den's own generated mixin with
  `field commands not found in type spec.specFileV2`.
- A `repos:` entry written with a trailing slash (`api: ~/dev/api/`) no longer prints two
  permanent falsehoods on every attach — that the repo is not mounted when it is, and a
  moved-start line naming the directory the shell already starts in. sbx normalizes the paths
  it echoes back; den now normalizes both sides of that comparison.
- A leading space in `ssh.dir` (`dir: " ~/.ssh_sbx"`) no longer defeats `~` expansion in
  silence, which left the literal string as the host path and mounted a directory that does
  not exist.

## v1.4.0 — 2026-08-07

### Added
- `mounts:` in `~/.den/config.yaml` mounts a host directory into the sandbox and links it
  where the tool looks for it (`- host: ~/.digitaleo`, `link: $HOME/.digitaleo`, `ro: true`).
  `sbx create` takes no mount-target flag, so a mounted path lands at its host path — the link
  is what makes it reachable. The list is **global**: every entry is mounted into every
  sandbox you spawn afterward, so a directory holding secrets reaches all of them.
- `den nest show` lists what a spawn will mount — the `mounts:` entries and the
  `ssh.mode: mount` sugar alike. `mounts:` is a global key, so nothing in the nest file you
  are inspecting names it.
- `den doctor` reports a `mounts:` host, and an `ssh.dir`, that is missing on disk or is a
  file rather than a directory. den mounts directories, and a missing path mounts an empty
  one instead of your files.

### Fixed
- `ssh.mode: mount` now links the mounted directory to `$HOME/.ssh`. sbx mounts a workspace
  at its host path while `$HOME` is `/home/agent`, so the key arrived intact somewhere ssh
  never looks and the mode authenticated nothing.
- den's agent-freshness gate names the link phase's own refusal instead of reporting only
  that the agent was not updated, and it reads the whole VM startup log rather than stopping
  at the first verdict it owns.

## v1.3.1 — 2026-08-05

### Fixed
- Runtime messages no longer cite spec section numbers (`(spec §9.1)`) — the spec lives in
  `docs/superpowers/`, is written in French, and never ships with the binary, so the
  parenthesis sent the reader to a lookup that could not succeed.

### Changed
- README now documents the agent profile: `~/.den/.claude` is a den-dedicated settings
  directory (fork subagent, spawn depth, bypass permissions), separate from any profile on
  the host, with the trade-off stated plainly — repos mount RW at the same absolute path and
  `agent-forward` hands the VM your push access.

## v1.3.0 — 2026-08-05

### Changed
- **Breaking:** spawning is a subcommand — `den spawn <nest>`. The bare form `den <nest>` is
  gone, and typing it gets a refusal that lists den's commands and says where spawn went.
  Carrying spawn on the root meant `den --detach` without a nest fell through to help and
  silently swallowed the flag, no first argument could ever list the commands (every unknown
  token was a plausible nest name), and a nest named after a subcommand (`ls`, `rm`) could
  never be spawned at all — `den spawn ls` now reaches it.
- The fail-closed settle-loop after `sbx create` answers in two `sbx` calls instead of one
  per allowlisted host: on a 26-host nest, `den spawn` drops from ~20 s to ~7 s and the
  re-attach branch to ~2 s. The guarantee is unchanged — den still refuses to attach before
  the scoped egress rule is verifiably live.

## v1.2.0 — 2026-08-05

### Added
- Repos on the fly: `den <nest> [repo...]` mounts the paths typed on the command line
  alongside the nest's own `repos:`, so working on a checkout for one session no longer
  means editing `nests/<n>.yaml` and then editing it back. A positional is a repo like any
  other — `-w` gives it a worktree too — and it comes first in `sbx create`'s workspaces, so
  the attached shell starts in what you just asked for. `:ro` is refused on a positional, and
  `--without` / `--only` keep addressing only the declared list.
- Team sources: a private git repo carrying `stacks/`, `lib/`, `kits/` and `nests/` becomes a
  set of objects addressed `<source>:<name>`. `den source add <url> [--name n]` clones it
  under `~/.den/sources/<n>/` and validates it, removing the clone when it is invalid;
  `den source update [n]` fast-forwards one source or every installed one and refuses rather
  than overwrite local or unpushed work; `den source ls` lists name, HEAD, last fetch and URL;
  `den source rm <n>` refuses on a dirty tree or on commits no remote has, unless `--force`.
  Only `den source update` touches the network — a spawn never fetches.
- `repos:` in `~/.den/config.yaml` maps a repo key to a path on this machine, so a nest shared
  through a source can write `key:` or `url:` instead of a path only its author has.
- `den lint <path>` validates a checkout — strict YAML, the stack DAG, bare references, and
  path confinement — which is what a team source's CI runs, and the same judge `den source
  add` and `den source update` use, so lint can never accept what a spawn would refuse.
- den records what it mounted, under `<den-home>/state/sandboxes/<sandbox>.yaml`, instead of
  re-deriving it from the nest at removal time. `den rm` and `den ls` replay that record, and
  `den doctor` reports records whose sandbox is gone — a `sbx rm` run outside den, a failed
  boot — with `--fix` reclaiming their worktrees and `--force` when one is dirty. Nothing is
  ever deleted without being named, and a sandbox created before the records existed still
  falls back on the old derivation.

### Fixed
- `den --version` answers `dev` again for a binary built with a plain `go build`. Go 1.24
  started stamping a VCS pseudo-version into build info, which den reported as though it were
  a released version — a version string nobody can check out, on a bug report naming no code.
  `go install …/cmd/den@vX` still reports its tag.

## v1.1.0 — 2026-08-04

### Added
- An install path that needs neither Homebrew nor a Go toolchain — Linux distros, WSL,
  macOS without brew:
  `curl -fsSL https://raw.githubusercontent.com/PillowPillow/den/main/install.sh | sh`.
  It picks the archive for the machine's OS and architecture, verifies its sha256 checksum
  and refuses to install when it cannot verify one, and puts `den` in `~/.local/bin`.
  `DEN_VERSION` pins a tag and `DEN_INSTALL_DIR` moves the destination; both bind to `sh`,
  not to `curl`, because each command in a pipeline gets its own environment.
- `CHANGELOG.md`, whose section for a version is also that version's release notes on
  GitHub — a release page now carries the written notes instead of a reformatted commit
  list.

### Fixed
- `den init`'s closing hint spells out `--den-home` only when it names something other than
  the default. On a plain init it repeated the path `den doctor` resolves on its own, which
  read as though the flag were required.

## v1.0.1 — 2026-08-04

### Added
- `den init` materializes a den home from the embedded example, so a first run no longer
  starts on an empty `~/.den/` and a missing-config error.
- Release archives for darwin and linux on amd64 and arm64, published on a `v*` tag, and a
  Homebrew cask in `PillowPillow/homebrew-tap`.
- The project is licensed Apache-2.0.

### Fixed
- A binary from `go install` answers its tag instead of `den dev`: the version now falls
  back to the module's build info when the linker did not stamp it.

## v1.0.0 — 2026-08-03

First release. Interactive use only — the autonomous-agent flow (`den agent`, `den review`),
remote kit sync and agent-plugin vendoring are named in the vocabulary and deliberately out
of v1. Ships against `sbx` v0.35.0, with `den build` measured end to end on 2026-08-03.

### Added
- Runtime: `den` spawns a sandbox or re-attaches to it, plus `den sh`, `den ls`,
  `den ports`, `den rm`. Several projects live in one VM. A sandbox is identified by its
  name, `<nest>[.<worktree>]`, because `sbx create` has no `--label`.
- Ports publish on demand through `den ports`, never automatically at spawn.
- `den build` orchestrates stack images as a DAG: den owns the whole sequence from
  `sbx create` to `sbx template save`, and a stack supplies only its provisioning steps
  (`provision.includes`, `provision.steps`).
- `~/.den/` is the single source of truth for stacks, nests and policy, redirectable with
  `DEN_HOME` or `--den-home`. Configuration is decoded strictly: an unknown key is a load
  error, never a silent default — a mistyped `egress:` would otherwise empty the allowlist
  and fail much later, without a visible cause.
- `den doctor` checks the host and the den home before a spawn depends on them.
- Every install path stamps the version, so `den version` names the code it runs. A plain
  `go build` answers `dev`, which is the tell that the build skipped `make`.
