# Changelog

Every released version, newest first. Each section is written by hand and is also the body
of that version's annotated tag and of its GitHub release — one text, three places. Cut a
release with `/release`.

Lines describe what changed for someone using den. The commit history is in the repo; it is
not repeated here.

## v2.3.0 — 2026-11-24

### Added
- `resources: {cpus, memory}` (global top level or nest, the nest wins key by key) sets the VM size of
  an instance, instead of sbx's default of every host CPU and half the host memory. den checks only the
  format at load, naming the file: `cpus` is a plain decimal whole number of 1 or more, `memory` an
  integer then `m` (MiB) or `g` (GiB), such as `8g`; sbx checks the bounds. A changed `resources:` gives
  the usual drift warning on a running instance; it applies after `den rm`, then `den up`.
- A nest with `resources:` needs sbx v0.54.0 or later to create an instance: on an older sbx, `den up`
  refuses before it creates anything, instead of failing inside sbx. A running instance still attaches.

## v2.2.0 — 2026-11-24

### Added
- `add_dirs` (global top level or nest, the nest wins, default `false`): `den up` gives Claude every
  other mounted repo with `--add-dir`, so Claude loads their `CLAUDE.md` and skills, not just the
  start repo's. It applies at the next `den up`, without `den rm`.
- `auto_trust` (global top level or nest, the nest wins, default `true`): `false` brings Claude's
  trust dialog back in the VM.

### Fixed
- Claude in a den VM now honours a repo's `permissions.additionalDirectories`: den records Claude's
  trust for each mounted repo and the start folder, where it used to skip the trust dialog without
  recording trust, so Claude ignored those folders. An instance created before this release gets the
  fix after `den rm`, then `den up`.

## v2.1.0 — 2026-11-22

### Added
- A Claude Code plugin, `den`, from this repo's marketplace: `claude plugin marketplace add PillowPillow/den`,
  then `claude plugin install den@den`. Each release pins the plugin to its own tag, so an install gets the
  skills of the last release, not `main`.
- `/den:setup` installs and checks den, sbx, the network policy, the Claude token and a first nest.
- `/den:profile` builds a profile kit from the host's `~/.claude` (or declares an existing kit folder),
  reviewing `settings.json` key by key.
- `/den:nest` adds or changes a nest in a `den.yaml` and validates it with `den exec`.
- `/den:troubleshoot` finds and fixes the cause of a den or VM failure.
- The skills never print the Claude token, a secret or a settings value: `sbx secret ls` is only read
  through `--json` and a `jq` filter that drops every secret field.

### Changed
- Prerequisites and troubleshooting move from the README to `docs/setup.md`.

## v2.0.0 — 2026-11-20

den V2 is a rewrite on top of sbx's `sbx env` commands. It shares no config or state with v1 and
cannot list or remove v1 sandboxes: read
[docs/migrating-from-v1.md](https://github.com/PillowPillow/den/blob/main/docs/migrating-from-v1.md)
**before** you upgrade. Requires sbx v0.53.0 or later; macOS and Linux.

### Added
- `den up <nest>` creates a nest's instance (an sbx VM) from a `den.yaml` and attaches Claude Code;
  `-w <branch>` gives each repo a locked git worktree, `--detach` leaves the instance running,
  `--repo` mounts an extra folder.
- `den exec <nest> -- <command>` runs one command in an instance, creating it if needed, and returns
  its exit code. den's own messages go to stderr, so stdout is the command's alone.
- `den shell` opens a `bash -l` in an instance; `den ls` lists the instances den created with the
  state of their VM.
- `den rm` removes exactly what den recorded: the VM first, then the worktrees den made and its own
  files. It refuses a worktree with uncommitted, untracked or ignored files, or unreachable commits,
  unless `--force`.
- Templates: a nest starts from a `templates:` entry (a folder or repo with a `den-template.yaml`)
  or an official Docker sandbox image. `den build <template>` builds and records it; `den up`
  never builds, and says when a template's definition moved.
- `repos:` keys carry their git origin, in the global and the project `den.yaml`. `den scan [folder]`
  finds the clones under a folder and records where each origin lives, so a shared project
  `den.yaml` needs no machine path. A missing repo comes with its `git clone` line.
- `profiles:` (global `den.yaml`) are named kit sets added to every nest that names them.
- Kits can ship Claude settings defaults in `~/.den/claude-settings.d/`; an existing key in the
  VM's settings keeps its value.
- Each instance gets a repo map (`~/.claude/rules/den-instance.md`, `~/.den/repos.json`,
  `DEN_REPOS`) so Claude knows where every repo is mounted.
- `den up` warns about global sbx rules added by hand and about an SSH agent the VM cannot use.
- `den --version` and `den -v`.

### Changed
- One config file, `den.yaml`, with a strict schema: an unknown key is an error naming the file,
  never a silent no-op. The global file lives in `~/.den/` (`DEN_HOME` or `--den-home` moves it);
  a project file is found by walking up from the current folder.
- Every create and remove runs through `sbx env` with `--skip-host-commands`, so no `lifecycle:`
  command or command-backed MCP server a VM could have written runs on your host. Config files den
  reads under a read-write mount are mounted read-only inside the VM.
- The Claude token stays on your host: the den-claude kit carries no credentials block, and its
  network rule opens only HTTPS to `api.anthropic.com`.
- The Go module is `github.com/PillowPillow/den/v2`: `go install` users move with
  `go install github.com/PillowPillow/den/v2/cmd/den@latest`.

### Removed
- `config.yaml`, stacks and `stack.yaml`, team sources (`den source`, `den lint`), `den spawn`,
  `den ports`, `den doctor`, `den init`, `den update`, the worktree trash, the SSH-agent key mount
  and the egress settle loop. Update with Homebrew or `install.sh`.

## v1.14.0 — 2026-11-20

### Changed
- `den update` refuses to cross a major version. den v2 is a rewrite that does not read v1's
  config and cannot list or remove v1 sandboxes, so a silent swap would leave them, and the git
  worktrees they registered, with nothing able to remove them. The refusal comes before any
  download and names the migration: commit and push your work, run `den rm` on each v1 sandbox
  with the v1 binary, then install v2 with `install.sh` or `brew upgrade --cask den`. No flag
  bypasses it.

## v1.13.1 — 2026-10-14

### Fixed
- The Homebrew cask removes the quarantine flag with a `postflight_steps` block instead of the
  Ruby `postflight` block that Homebrew 7 deprecated, so `brew upgrade den` no longer warns
  "Calling `postflight` is deprecated!" and asks you to report it to the tap. The cask needs
  Homebrew 6.0.16 or later.

## v1.13.0 — 2026-10-13

### Changed
- den requires sbx v0.48.0 or later. `den doctor` is still the single judge of that floor. On an
  older sbx, a command that lists sandboxes or templates refuses with the upgrade remedy instead of
  reading the old output: read as an empty list, it would make `den exec` or `den rm` claim that a
  live sandbox does not exist.

### Fixed
- den works with sbx v0.48.0, which returns `sbx ls --json` and `sbx template ls --json` as bare
  arrays. den v1.12.0 failed on that shape in every command that lists sandboxes or images — `den
  ls`, `den up`, `den run`, `den exec`, `den rm`, `den ports`, `den build`.
- `den doctor` reads the version of sbx v0.48.0, which prints `sbx version v0.48.0` without the
  colon. `den converge` checks a source's `requires.sbx` floor again instead of skipping it with a
  warning.

## v1.12.0 — 2026-09-17

### Added
- `den ls` shows a `LAST USED` column, from the `last_used_at` that `sbx ls --json` reports since
  sbx v0.43.0. A listing that carries no such field shows `-`.

### Changed
- den requires sbx v0.43.0 or later. `den doctor` is the single judge of that floor; the other
  commands assume the v0.43.0 surface and let sbx speak for itself when it is older.
- Every sandbox den creates is started with `--skills off`. sbx v0.43.0 mounts the host's shared
  skills store read-only into `~/.claude/skills` by default, which put a second source of skills in
  the VM next to den's agent profile.

### Fixed
- `den up`, `den run` and `den build` refuse a sandbox name sbx v0.43.0 rejects — over 63 bytes, not
  ending in an alphanumeric, or exactly `default` — before the worktree exists. A `-w` on a long
  branch or on one ending in `-` used to die at `sbx create`, with the worktree already created.
  A sandbox that already bears such a name stays addressable: `den ls`, `den rm` and an attach take
  it as before.
- `den ports` recognizes its own publications again. sbx v0.42.0 publishes a protocol-less spec as
  `tcp4` and reports that string back unchanged; den's filter only knew `tcp`, so it saw none of its
  windows on a running sandbox and republished ports that were already bound.
- `den rm` destroys a sandbox whose name it cannot validate instead of refusing it. A sandbox created
  outside den, under a name den cannot turn into a host path, exited 1 on that check before `sbx rm
  --force` ran — a live VM with no way to remove it. The check is now a warning and the destruction
  always proceeds.

## v1.11.0 — 2026-08-27

### Fixed
- `den converge` finds a repository nested under a `repository_roots` entry. den read only the
  direct children of each root, so the common `<root>/<org>/<repo>` layout reported every declared
  repository as `absent`. The walk now goes five levels deep and stops at the first checkout it
  meets, which keeps it out of `node_modules`, `vendor` and agent worktrees — those live inside a
  repository.
- A directory under a root that den cannot open is reported as a plan warning. It used to print the
  same `absent` as a missing repository, and only one of the two is answered by cloning something.

## v1.10.0 — 2026-08-21

### Fixed
- den no longer builds a sandbox name `sbx` refuses. A plus sign leaves the name charset — nobody
  had to type one, `-w feat/c++` flattened to `feat-c++` and kept it — and a name under two
  characters is refused up front, which is what `den up a` produced. `sbx` rejected both, but only
  after den had already created the worktree.
- Any den command that reads sbx survives sbx's "Update Available" banner. sbx v0.38.0 prints that
  box on stdout, behind the payload, and `den exec <nest> bash` died on it with `sbx ls: unreadable
  JSON output (invalid character 'â' after top-level value)` — den refusing to attach to a VM that
  was running, about once a day. The `--json` reads take the first top-level value and ignore what
  follows; the spawn's settle loop, which parses sbx's credential table by column offset, drops the
  banner lines before parsing.
- `den rm` judges every worktree before it moves any of them. A refusal on the second repo used to
  arrive after the first one had already gone to the trash: one directory moved, one still on disk
  with a live git registration, and no retry could clear it, since the retry hit the same refusal.
  Every refusal is now reported in one run instead of one per `den rm`. `den doctor --fix` reclaims
  the same way.
- `den rm`'s dirtiness refusals name the remedy that applies — commit, `--force`, or
  `--keep-worktrees`. An ignored file still blocks, a `.env` being exactly what one cannot get back,
  but "uncommitted changes (.DS_Store)" sent you looking for work that does not exist.

## v1.9.0 — 2026-08-19

### Added
- `den update` replaces the running binary with the latest release: it resolves the tag, verifies
  the published sha256 the way `install.sh` does, and swaps through a single atomic rename, so an
  update while den is running cannot leave a half-written file on your PATH. It refuses, naming the
  right command, when Homebrew or the go toolchain owns the binary — overwriting their file would
  leave them managing a version they no longer manage — and refuses a build from a checkout, which
  `git describe` stamps with a commit count or `-dirty`. There are no flags: `DEN_VERSION=v1.0.1`
  on `install.sh` pins a version or rolls back. `README.md` gained an `Updating` table naming the
  update command for every install path.
- `den doctor` checks the machine's sbx network policy and reports `[FAIL] sbx policy` when
  `sbx policy init <allow-all|balanced|deny-all>` has never run — the one-time step den does not
  perform for you, and without which sbx answers no policy command and starts no sandbox. doctor
  now also reads sbx once for all its source lines instead of once per source.

### Changed
- den's questions are rendered forms instead of typed lines: the `-i` repo checklist and the
  repo choice of `den init --source` / `den source add` select with the arrow keys and space, and
  a confirmation is an inline Yes/No defaulting to No. Ctrl-C now ends a prompt — the previous
  reader hung on it, leaving a question on the terminal after the signal meant to end den.

### Fixed
- Every flow that converges a source — `den init --source`, `den source add`, `den source
  configure`, `den source update` — refuses a machine den cannot observe before asking anything,
  and names `sbx policy init`. A fresh laptop used to answer every question, hand over a GitLab
  token, read a plan whose every line said `unknown`, confirm it, and only then meet sbx's own
  error about the uninitialized global network policy. `den up` and `den run` name the same step
  when the settle loop's host check is what fails.
- Those flows no longer pass sbx's deprecated `--global` to `secret set`. sbx's deprecation
  warning landed on stderr interleaved with its own `Enter secret:` prompt, which reads as den
  failing mid-plan.

## v1.8.1 — 2026-08-17

### Fixed
- `brew install --cask PillowPillow/tap/den` installs on Linux. The cask is not macOS-only —
  goreleaser emits a Linux archive beside the darwin one — but its postflight probed macOS
  `xattr`, and in cask context that probe raises instead of reporting an exit status: brew
  aborted the hook and purged a binary it had already staged correctly, with exit 127. The
  hook now asks `OS.mac?`, which runs nothing. `install.sh` was the workaround and stays
  available.

## v1.8.0 — 2026-08-17

### Added
- `den run <nest> <cmd>` warns when the first word of the command is a readable directory —
  `den run api ~/dev/hotfix go test` is a legal command line, so den runs it as typed and says
  once that the directory was not mounted, naming the `--repo` line that would mount it.

### Changed
- **Breaking:** `den spawn` is gone, with no alias. `den up <nest>` creates or re-attaches and
  opens a shell; `den run <nest> <cmd> [args...]` does the same and runs a command instead,
  exiting with its status. Ad-hoc repos move from trailing positionals to a repeatable
  `--repo <path>` — a shell glob expands before den sees it, so `den spawn scratch ~/dev/proj-*`
  becomes `repos=(~/dev/proj-*); den up --repo=${^repos} scratch` (zsh) or a loop building
  `--repo` pairs (portable). `--` leaves the family: `den run`'s own flags sit left of the nest
  name, and everything from the command on is passed through verbatim, the way `den exec`
  already worked.
- **Breaking:** `den nest show <nest> [repo...]` becomes `den nest show [--repo <path>...] <nest>`.
  The dry-run follows the family rather than keeping one ad-hoc repo spelled two ways between
  sibling commands; typing the old form prints the same migration message `den up` does.
- `den down` is not a command, and typing it now makes den suggest `den rm`.

### Fixed
- A refusal in the `den up` / `den run` / `den exec` / `den nest show` family proposes a line den
  itself accepts. Three ways it did not: the proposal dropped the flags already parsed off the
  line, so `den exec --workdir /srv -- api go build` proposed a line without `--workdir` and
  `den run --as reco -w feat api` proposed `den up api` — which attaches a *different* sandbox;
  it did not quote what a shell would split, so `den exec api --workdir '/tmp/hot fix' true`
  proposed a line reparsing into another sandbox running another command; and it could name a
  flag the target always refuses, so `den up -T api /dev/hotfix` proposed a line refused one
  round trip later.
- `den up --help` rendered `-T, --no-tty den up` instead of the flag's description. Cobra reads
  the first backquoted span of a usage string as that flag's value placeholder; `den run
  --detach` and `den shell` lost text the same way.
- `den up "$NEST" repo` with the variable unset proposed a line naming an empty nest. den now
  refuses the empty name instead. `den nest show` inherited the same slip.

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
