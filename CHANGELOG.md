# Changelog

Every released version, newest first. Each section is written by hand and is also the body
of that version's annotated tag and of its GitHub release — one text, three places. Cut a
release with `/release`.

Lines describe what changed for someone using den. The commit history is in the repo; it is
not repeated here.

## v1.3.0 — 2026-08-05

### Changed

- **BREAKING — `den <nest>` ne spawne plus : `den spawn <nest>`.** Le spawn est une sous-commande
  comme les autres. Ses six flags (`-w`, `--agent`, `--without`, `--only`, `--detach`, `-i`) ont
  quitté le root, où `den --detach` seul les avalait en silence. Un premier argument inconnu liste
  désormais tout ce que den sait faire, et un nest homonyme d'une sous-commande — `ls`, `rm`, `sh`
  — redevient spawnable : `den spawn ls`.

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
