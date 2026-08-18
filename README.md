# den

Generic CLI for driving `sbx` sandboxes: one command to start a multi-project
microVM, without retyping mixin, kits and policy by hand.

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

## Bootstrapping

```bash
sbx policy init balanced   # once per machine — see below
den init
$EDITOR ~/.den/nests/example.yaml
den doctor
```

**`sbx policy init` is a one-time step den does not perform for you.** sbx answers no policy
command — and starts no sandbox — until a global network policy exists, so on a machine that never
ran it every den command touching the network fails: `den source add` refuses before it asks
anything ("den could not observe this machine"), and `den up` fails in the settle loop. den does not
choose the profile for you, because `allow-all`, `balanced` and `deny-all` are a machine-wide
security posture: `balanced` allows typical development traffic (AI services, package registries),
`deny-all` blocks everything until you allow hosts one by one. `sbx policy reset` is the only way
back. `den doctor` reports the missing policy as `[FAIL] sbx policy`.

`den init` copies the shipped example into the den home, refusing only on the presence of
`config.yaml` itself (`already initialized: <path>`) — a home with no `config.yaml` counts as
uninitialized no matter what else is sitting in it, and every shipped file is then written
unconditionally, overwriting a same-named file already on disk. Step 2 above is why: the shipped
`~/.den/nests/example.yaml` points at `~/dev/my-project`, and
skipping the edit is what makes the first `den doctor` fail on "repo not found" — that diagnostic
goes away once the nest points at a real repository. `sbx` missing is a second, machine-dependent
one — only shown if it is not already installed.

`~/.den/` is the single source of truth. The `DEN_HOME` variable (or the `--den-home` flag) lets
you use a different one — that is what makes `den` testable and scriptable.

## Available commands

| Command | Role |
|---|---|
| `den init` | creates a den home from the shipped example (`config.yaml`, `nests/example.yaml`, `stacks/devx/stack.yaml`); refuses if `config.yaml` already exists |
| `den init --source <url>` | creates a **source-aware** home instead — no example nest, no local stack — and converges everything the source declares (see [Declarative sources](#declarative-sources)) |
| `den up <nest> [--repo p...]` | spawn-or-attach: creates the nest's microVM if it does not exist, attaches to it otherwise, then opens a shell; ad-hoc repos are mounted on the fly via a repeatable `--repo` |
| `den run <nest> <cmd> [args...] [--repo p...]` | the same spawn-or-attach, running `<cmd>` instead of opening a shell; exits with the command's own status |
| `den ls` | lists live sandboxes, with their nest, instance, worktree, status and workspace count |
| `den exec <name> <cmd> [args...]` | runs one command in an existing sandbox and exits with that command's own status |
| `den shell <name>` | opens a login shell in an existing sandbox |
| `den ports <name>` | publishes the nest's declared ports into that sandbox and prints where they land on the host |
| `den rm <name>` | destroys a sandbox and cleans up the worktrees den created (the agent profile persists) |
| `den build [<stack>]` | builds stack images in `parent` order, playing each stack's `provision.steps` in a throwaway build VM |
| `den nest ls` | lists the declared nests |
| `den nest show <n>` | shows a fully resolved nest (stack, agent, egress, repos) |
| `den source add <url> [--name n]` | clones a team source under `~/.den/sources/<n>/` and validates it; refuses (and removes the clone) if invalid |
| `den source update [n]` | fetches and fast-forwards one source, or every installed source when none is named; refuses rather than overwrite local or unpushed work. A **declarative** source updates to an exact published version, after a plan and a confirmation |
| `den source configure <n>` | reconverges an installed declarative source on this machine, without contacting its remote: maps a repo cloned since, finishes an interrupted run |
| `den source status [n]` | reports what a declarative source needs and what this machine has; exits non-zero on `blocked` and `unknown` only |
| `den source ls` | lists installed sources: name, HEAD, last fetch, URL |
| `den source rm <n> [--force]` | removes an installed source; refuses on a dirty working tree or commits unreachable from any remote-tracking ref, unless `--force` |
| `den lint <path>` | validates a checkout (stacks, nests, references, path confinement) — what a team source's CI runs |
| `den doctor` | diagnoses the configuration and the environment, and reports records whose sandbox is gone; `--fix` reclaims their worktrees (`--force` if one is dirty) |
| `den version` | binary version |

Options `den up` and `den run` share:

| Option | Effect |
|---|---|
| `-w`, `--worktree <branch>` | propagates a worktree of that name across **all** the nest's repos, and suffixes the sandbox name (`api.feat12`) |
| `--repo <path>` | mount this repository too, ad hoc; repeatable, and the order you type is the order den mounts |
| `--as <label>` | name this instance, to run several sandboxes of one nest side by side |
| `--only <repo,...>` | keep only these optional repos (required repos stay mounted) |
| `--without <repo,...>` | exclude these optional repos |
| `-i`, `--interactive` | pick the optional repos from a checklist; refused together with `--only`/`--without`, and outside a terminal (a pipe or CI must use those two) |
| `--agent <name>` | overrides `defaults.agent` |
| `--workdir <path>` | working directory for the command (default: the directory you ran `den` from, when the sandbox mounts it; otherwise the first workspace it reports) |

Where the two commands diverge — each registers the other's flag only to refuse it by name:

| Option | On `den up` | On `den run` |
|---|---|---|
| `--detach` | prepares the sandbox without opening a shell | refused — `den run` runs a command inside the sandbox; use `den up --detach` |
| `-T` | refused — a login shell needs a terminal; use `den run -T` | never allocate a terminal — for pipes and CI |

`den run`'s own flags sit **before** the nest name; everything after it is the command, verbatim,
its own flags included — `den run api go test -v` passes `-v` to `go test`, the same rule
`den exec` follows. There is no `--`.

`-w` takes a **branch** name, and a branch name often contains a `/`.
The branch keeps the name as typed — that is the name in `git log` and in the PR — while the
sandbox name and the worktree directory take a flattened form: `den up api -w feature/123` works on
branch `feature/123` in a sandbox `api.feature-123`. That is the name it appears under in
`den ls`, and the one `den exec` and `den rm` expect.

So den accepts any name it can **name**; git remains the sole judge of what is a legal **ref**.
`-w 'a..b'` passes naming (sandbox `api.a--b`) and it is `git worktree add` that refuses,
before any sandbox is created.

### Instances

A sandbox is named `<nest>[.<instance>]`, and the instance is the only thing that distinguishes two
sandboxes of one nest. `-w` fills it with the flattened branch; `--as` fills it directly:

```bash
den up api --as analyse-a
den up api --as analyse-b   # same repos, two microVMs
```

`--as` renames the sandbox only. Under `-w`, the worktree directory keeps being named after the
branch, so `den up api -w feature/123 --as reco` puts the worktree exactly where `-w` alone
would have put it — a label never renames a worktree directory, since two nests spawned `--as x`
would otherwise collide on it. den never generates an instance name on its own: running a second
instance of one nest is always a deliberate `-w` or `--as`, never something den infers for you.

Two instances mounting one working tree is allowed, and den has no read-only mount to offer: two
VMs writing one git index can corrupt it. `--as` is for read-mostly concurrency — two analyses, two
agents exploring the same checkout. Two writers means two branches, hence `-w` alone: `git worktree
add` refuses a branch already checked out elsewhere, so two instances cannot share one.

Options of `den exec` and `den shell`:

| Option | Effect |
|---|---|
| `-T` | never allocate a terminal — for pipes and CI. On `den shell` it is refused: a login shell needs one |
| `--workdir <path>` | working directory (default: the directory you ran `den` from, when the sandbox mounts it; otherwise the first workspace it reports) |

den's own flags go **before** the sandbox name; everything after it is the command, verbatim, its
own flags included — `den exec -T api go test -v` passes `-v` to `go test`. This is
`docker compose exec`'s rule, and it is why no `--` is needed. `den exec api --help` asks the
program in the sandbox for its help, not den.

`--den-home` obeys the same order, although it belongs to `den` itself: `den exec --den-home ~/alt
api go build`. Past the sandbox name den stops reading its own flags entirely, so the other
spelling would send a program named `--den-home` into the VM. den refuses it by name rather than
letting the sandbox answer `bash: --den-home: command not found`.

### Mounting a repo on the fly

A nest file is still required — what becomes optional is its `repos:` block. A repo does not need
to be declared there to enter the sandbox: repeat `--repo <path>` and each one mounts like a
`repos:` entry, worktree included.

```bash
den up scratch --repo ~/dev/a --repo ~/dev/b   # a nest with no `repos:` — both repos come from the flag
den up api --repo ~/dev/hotfix                 # additive: api's repos PLUS hotfix
den up scratch --repo .                        # the current directory
den up api -w feat/x --repo ~/dev/hotfix       # -w propagates a worktree to hotfix, same as api's own repos
den nest show scratch --repo ~/dev/a           # what would be mounted, without creating anything
```

`--repo` is repeatable, never a comma list, and it cannot take a shell glob: the shell expands the
glob before den ever sees the command line, so `--repo ~/dev/proj-*` binds the first match and the
rest arrive as bare positionals. `den up` refuses them rather than guess which one is the nest.
`den run` reads the second match as the nest and the rest as the command, warns that the first word
of that command is a directory, then fails to resolve the nest. Expand the glob into repeated flags
yourself:

```zsh
# zsh, parameter distribution — the `--repo=<val>` spelling is mandatory
repos=(~/dev/proj-*); den up --repo=${^repos} scratch
```

```bash
# portable, and the only one that survives a space in a path
repos=(); for d in ~/dev/proj-*; do repos+=(--repo "$d"); done
den up "${repos[@]}" scratch
```

The first `--repo` on the command line becomes the directory where the shell starts. Mounts are
frozen at sandbox creation, so on an already-live sandbox `den` warns rather than changing anything:
it names any path it will not mount, and it says so separately when the shell will not start where
you asked — asking for a subset of what is already mounted triggers only the second. `den rm <name>`
then relaunch to change either.

`:ro` is not accepted: a repo mounted on the fly is mounted writable, like a declared `repos:` entry.
Under `-w`, it must also be a git repository, exactly like a declared one — den refuses before
creating anything otherwise.

`--without` and `--only` still address only the declared `repos:` list, so a repo given behind
`--repo` is dropped by not typing it. Naming an undeclared repo on either flag is not a no-op:
den refuses the whole spawn with `repo "<name>" unknown in this nest`.

**`den rm` reclaims the worktree of a repo given on the command line too.** At creation den writes
down what it actually mounted, under `~/.den/state/sandboxes/<sandbox>.yaml`, and the teardown
replays that record instead of re-deriving it from today's configuration. So a positional — declared
in no file at all — is reclaimed like the rest, and so is a worktree whose `worktree_root` moved,
whose repo key stopped being mapped, or whose nest was deleted since the spawn. A repo mounted
as-is, that den did not create, is never touched.

A sandbox created before the records existed (or outside den) has none: `den rm` then falls back on
the old derivation through the nest's `repos:`, saying so — that answer is only accurate if neither
the nest nor `config.yaml` changed since. Under the `per-repo` layout a leftover from such a sandbox
sits at `<repo>/.den/<wt>`, inside your own repository, and the `.den/` line den added to that
repo's `.git/info/exclude` stays too — harmless, local, never committed, but yours to remove.

Options of `den rm`: `--keep-worktrees` (keep the worktrees, and their record, so `den doctor` can
still find them), `--force` (reclaim them even if they carry uncommitted changes; without it, den
refuses **before** touching the VM).

`den doctor` reports records whose sandbox is gone — a `sbx rm` run outside den, a failed boot, a
`den rm --keep-worktrees` — as a warning naming the directories still on disk; `den doctor --fix`
reclaims them, `--force` when one is dirty. `den ls` names them too. Nothing is ever deleted: den
moves worktrees to `~/.den/trash/`.

`~/.den/state/` holds those records and is **never purged automatically** — unlike `~/.den/cache/`,
it is not reconstructible.

`den nest show` takes `--agent`, `--only`, `--without` and `--repo` — the same set `den up` and
`den run` take, and for the same reason: resolution is what they act on, so showing a nest under
them is how you read what `den up`/`den run` would do without spawning it. Including the refusals:
`--without` on a `select: prompt` nest is rejected here exactly as it is on `den up`/`den run`,
since a dry-run that accepts what the run refuses is not one. It has no `-w`: a worktree changes the
sandbox name, not the resolved nest.

A stopped sandbox — which `sbx` does on its own after a few minutes of inactivity — is not a
failure: `den up`, `den run` and `den exec` pick it back up, with its state.

## Ports

Ports are **not** published at spawn. `den ports <name>` does it, on demand, and takes a **sandbox**
name — like `den exec` and `den rm` — because a port is published into a live VM.

Each nest gets a deterministic window of 10 host ports, `base = 9000 + hash(<nest>) % 900 * 10`,
overridable with `ports.base`. The window is seeded by the **nest**, never by the sandbox, so
`api` and `api.feat12` share the same base and a bookmarked URL keeps working across worktrees. A
declared port takes `base + <its declaration index>`; declaration order is what assigns the number,
so the table is printed in that order and never sorted.

```yaml
# ~/.den/nests/web.yaml
ports:
  base: 9100          # optional — otherwise hashed from the nest name
  publish:
    - { name: vite, container: 5173, open: true }
    - { name: api,  container: 3000 }
    - { name: cdp,  container: 9223, loopback_lock: true }
```

```
$ den ports web.feat123
nest: web   sandbox: web.feat123   window: 9100-9109 (canonical)
  NAME  CONTAINER  URL
  vite  5173       http://127.0.0.1:9100  [opened]
  api   3000       http://127.0.0.1:9101
  cdp   9223       http://127.0.0.1:9102  [loopback-locked]
  remote?  ssh -L 9100:127.0.0.1:9100 you@$(hostname)
```

- `open: true` opens that URL in a browser once the table is printed. A browser that fails to start
  is a warning on stderr, not a failed command — the ports are published either way.
- Everything binds `127.0.0.1`, **never** `0.0.0.0`. `loopback_lock: true` marks an endpoint that
  must never leave the loopback (an unauthenticated CDP/Playwright socket, typically).
- Remote access is the printed `ssh -L` line, never a LAN bind. `you@$(hostname)` is literal: paste
  it in your own shell, where it expands on the machine that runs the tunnel.
- **Re-running the command re-reads the same table.** den first reads what that sandbox already
  publishes and reuses the window it is on, publishing only what is missing. Without that step the
  scan finds den's own previous publication holding the canonical block, calls it busy, and binds
  ten more host ports — which is what it used to do, on every run.
- If the canonical window really is taken, den moves the **whole** block to the next free one and
  warns on stderr that these addresses hold for this instance only. It does not say *who* holds it:
  a bound port names no owner, and den only knows it is not this sandbox. `sbx ports <name>` lists
  what a sandbox publishes; `lsof -nP -iTCP:<port> -sTCP:LISTEN` names a process.
- `--add H:C` (repeatable) publishes a pair the nest does not declare. Re-running an identical
  `--add` succeeds and changes nothing. A nest that declares no port prints no window and scans
  nothing — only the added pairs are published.
- A **stopped** sandbox is started first, and said so on stderr: publishing a port needs a live
  endpoint in the VM, and `sbx ports` — unlike `sbx exec` — does not restart one.

The table goes to stdout, every warning to stderr: what a pipe reads is the table alone.

## Egress

A nest's `egress:` is a **widening** of the machine's policy, never a narrowing.

den cannot make a sandbox *less* connected than the host's own `sbx` policy already permits — on a
stock machine that baseline is broad (measured: 197 rules, including filesystem read and write
allow-all, and wildcards like `**.github.com:443` or `**.amazonaws.com:443`, so most package
registries, cloud endpoints and AI services are reachable whether or not a nest declares them).

What `egress:` genuinely buys is everything **outside** that baseline — a project database at
`10.22.11.54:27017`, an internal host — and that part is enforced, scoped to the sandbox. Measured
both ways: the same host is allowed for the nest that declared it and denied for the one that did
not (`No matching allow rule (default deny)`).

Treat `egress:` as *access you would not otherwise have*, not as a sandbox firewall.

## Agent profile — a `.claude` dedicated to den

Every agent in the registry owns a **profile directory**, `<den home>/agents/<agent>` by default
(so `~/.den/agents/claude`). den creates it if it is missing, mounts it read-write at the same
absolute path as on the host, and exports the registry's `env:` into the sandbox — for Claude that
is `CLAUDE_CONFIG_DIR={config_dir}`, which makes that directory the agent's config root inside the
VM. `agents.<name>.config_dir` in `config.yaml` moves it globally; a nest's own `agents:` block
moves it for that nest alone (`den nest show` prints the resolved path).

The point is that it is **one profile shared by every sandbox**, and `den rm` does not touch it:
configure it once and every spawn — every nest, every worktree — starts from it. That is what makes
a den-dedicated `.claude` worth having, separate from the one your host agent uses.

Recommended `~/.den/agents/claude/settings.json`:

```json
{
  "env": {
    "CLAUDE_CODE_FORK_SUBAGENT": "1",
    "CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH": "2"
  },
  "permissions": {
    "defaultMode": "bypassPermissions"
  },
  "sandbox": {
    "enabled": false
  }
}
```

`1` and `2` are floors, not ceilings — raise them if your workflows fan out further. The two
`env:` values must stay JSON **strings**. The last two keys drop the agent's own permission prompts
and its nested sandbox, which is the trade-off worth stating plainly rather than selling: a microVM
is not a vacuum. Your repos are mounted from the host at the same absolute path, read-write —
including the `-w` worktrees that may carry uncommitted work — and `ssh.mode: agent-forward` (the
default) means the forwarded agent inside the VM holds your push access. `egress:` does not narrow
that either (see above). What the VM does buy is a blast radius bounded by *what den mounted*, which
den's own manifest replays for `den ls` / `den rm` / `den doctor` — enough to let the agent run
unprompted, not enough to treat it as unattended.

`mounts:` widens that radius, and it does so **globally**: every entry is mounted into every
sandbox, not just the one you have in mind when you add it, and — unlike your repos — it is not
in the manifest den replays, because it is re-derivable config rather than sandbox state. Pointing
a mount at a directory holding secrets (a VPN token, a `consul.secret`) exposes them to every
sandbox you spawn afterward. Legitimate, as long as it is not done unknowingly. `ssh.mode: mount`
is the same trade-off under a different name: the key sits at rest in every VM while the forwarded
agent still holds it too, so the mode **adds** reach, it does not narrow it.

`den nest show <nest>` lists what a spawn will mount — the `mounts:` entries and the
`ssh.mode: mount` sugar alike — which is the one place that enumerates a global key per nest.

`host:` must be absolute (or `~/...`, which den expands). `link:` is a **VM** path, emitted into the
sandbox's startup shell verbatim so the VM expands `$HOME`: it must be absolute or start with
`$HOME/` / `~/`, may expand nothing but `$HOME` (spelled `$HOME`, not `${HOME}`), and carries no
quote, backslash, backtick or `$(...)`. Two mounts may not claim the same `link:` — the link phase would run `ln -sfn` twice on
one path and only the last would survive, so den refuses instead of letting one entry disappear in
silence.

## Agent freshness

The agent is updated **at boot**, not baked into the image, through the `update:` command of the
registry. den watches that gate: it reads the sandbox's kit-startup journal and **refuses to open a
sandbox whose agent it knows was never updated**, quoting the failing line.

Where it waits is a trade-off, and den takes the two sides differently: attaching a shell, it waits
for the verdict (tens of seconds on a fresh spawn) because you are about to run that agent; under
`--detach` it reads once and moves on with a note, because nobody is at a prompt and the next attach
catches it.

## Building images

A stack names an image and, optionally, a `parent:`. That is the whole DAG.

```
den build                 # every declared stack, in dependency order
den build dgdevx          # devx first if its image is missing, then dgdevx
den build dgdevx --force  # rebuild devx too
```

### What a stack declares

**den owns the build sequence**; a stack only says what to run inside it. A `stacks/<name>/build.sh`
is **no longer read** — if you have one from an earlier den, see [Coming from
`build.sh`](#coming-from-buildsh) below.

```yaml
# ~/.den/stacks/devx/stack.yaml
image: devx:v1          # required — the name den saves the built image under
base: claude            # a ROOT stack: the sbx agent the build starts from
provision:
  includes:             # optional, concatenated ahead of EVERY step
    - ../../lib/common.sh
  steps:                # one `sbx exec` per entry, in order
    - ./provision/go-tools.sh
    - ./provision/gh.sh
```

```yaml
# ~/.den/stacks/dgdevx/stack.yaml
image: dgdevx:v1
parent: devx            # a DERIVED stack: it builds FROM devx's image
provision:
  steps: [./provision/glab.sh]
```

- **`image:` is required**, on every stack. It is the single name den saves into and `den up`/`den
  run` looks for, which is what keeps the two from ever disagreeing.
- **`base:` or `parent:`, never both.** `base:` names the sbx agent a root stack starts from
  (`claude`); `parent:` names another stack, and the build starts from *its* image. A stack that
  declares `provision.steps` must have exactly one of them.
- Paths in `includes:` and `steps:` are **relative to the stack directory** (`../../lib/common.sh`
  reaches `~/.den/lib/common.sh`).

For each stack, den runs: `sbx create --name <stack>-build …` → one `sbx exec … bash -lc` per
`steps:` entry → `sbx stop` → `sbx template save <stack>-build <image>` → `sbx rm --force`. The
teardown is guaranteed: it also runs when a step fails, so a failed build never leaves a VM behind
and never saves an image.

### `steps:` and `includes:`

Each step is **its own `sbx exec`**, which is what lets a failure name the script that produced it:

```
stack "devx": step 2/3 $DEN_HOME/stacks/devx/provision/gh.sh failed: E: Unable to locate package ripgrep
```

The path is absolute because `provision.steps` is resolved against the stack directory at load time,
and because the message is meant to name the file you open. `$DEN_HOME` stands in for its expanded
value here.

The price is that every step opens a **fresh shell**. Between two steps only the VM's filesystem
survives: installed packages and dropped binaries stay, but variables, functions and `cwd` die with
the process. A step that must pass a variable to a later step *writes* it (`/etc/profile.d/…`); it
does not `export` it.

`includes:` is the answer to that loss, and it is **not a script played first** — its text is
concatenated at the **head of every step**. So:

| in `includes:` | if it were "played first" | reality |
|---|---|---|
| `common::gh() { … }` | visible in step 1 only | visible in **all** steps |
| `export PATH=…` | lost from step 2 on | present in **all** steps |
| `apt-get install …` | once | **N times**, silently |

Hence the contract, which is the reason for the word: **`includes` defines, it does not act.** den
cannot verify it; a side effect placed there is replayed once per step.

Two conveniences follow from den reading the files instead of executing them: they need neither the
**executable bit** nor a **shebang**. In exchange the shell is den's choice, not the script's — den
sends `bash -lc`, the `-l` being what loads the base image's `PATH` (go, node).

A step reaches **no host file**. The only host material entering the VM is the *text* of `includes:`
and `steps:`; everything else — packages, binaries, archives — comes over the **network**, under the
nest's egress policy.

### What gets rebuilt

- The **target** is always rebuilt — you named it. Only its ancestors are skipped when their image
  is already there, and every skip is printed, with the `--force` that overrides it.
- "Already there" is read from `sbx template ls --json`, which is also why `den build` (everything)
  and `--force` never call it: those forms rebuild by definition.
- A stack with **no `provision.steps`** is not something den builds: its `image:` is one sbx pulls.
  It is skipped and named, never a refusal — unless you ask for it by name, which den does refuse
  rather than answer with a skip line that reads as success.
- A `parent:` cycle is refused with the whole cycle spelled out (`a → b → a`), a `parent:` that does
  not exist names the file to fix, and **every `includes:` and `steps:` file of the whole chain is
  read before the first `sbx create`** — a four-minute base image should not be built to reach a
  refusal den could make instantly.
- A **pre-existing `<stack>-build` sandbox is a refusal**, not a cleanup: that is a legal nest name,
  so removing it blindly could destroy a sandbox of yours. The message names `sbx rm --force`.

`den up`/`den run` uses the same inventory: if the stack declares `provision.steps` and its image was
never built, den stops and tells you to run `den build <stack>`. Without that check the failure
surfaces as sbx's own `403 Forbidden: pull failed for image "X"` — sbx treats an unknown template as
a registry pull, so the message talks about authorization rather than about a missing build. A stack
with no `provision.steps` is left alone: its `image:` may well be one sbx can pull, and den has no
build to suggest.

### Coming from `build.sh`

If your `~/.den` predates this model, every stack still has a `stacks/<name>/build.sh` and **den no
longer reads it**. Nothing breaks and nothing is deleted, but until you migrate, `den build` skips
every stack ("no `provision.steps`, nothing for den to build") and `den up`/`den run` stops warning
about missing images.

To migrate a stack: split what the script does into one file per stage under `stacks/<name>/provision/`,
list them in `steps:`, move any shared function library into `includes:`, and delete the `build.sh`
(the `sbx create`/`template save`/`trap` scaffolding it carried is now den's). Two things the old
scripts could do and steps cannot: read host files (only `includes:`/`steps:` text enters the VM),
and pick their own shell (den sends `bash -lc`). `versions.lock` is out of the model — den claims
nothing about tool versioning, and pins stay where they already are, in the scripts.

## Team sources

A **source** is a team's stacks and nests shared through a private git repo — typically reachable
only from a VPN, and unrelated to the product's own GitHub. den clones it under
`~/.den/sources/<name>/`; its objects are then addressed `<name>:<nest>`.

```bash
$ den source add git@gitlab.corp:dev/stacks.git --name corp
source "corp" installed — its objects are addressed corp:<name> (e.g. `den up corp:<nest>`)

$ den up corp:backend
# resolves the nest "backend" and its stack inside the "corp" source, then
# spawns (or attaches to) the sandbox "corp-backend"

$ den source update
source "corp" updated
```

`add` clones and lints the checkout in the same step: an invalid source is refused **and its own
clone removed**, so a bad push never leaves a half-usable directory behind. Bare `den source
update` refreshes every installed source; `den source update corp` refreshes just one. `den
source rm corp` deletes the clone; it refuses on local changes or on commits unreachable from any
remote-tracking ref (unpushed work, or a team repo that rewrote its history — see below),
`--force` deletes anyway. Contributing back is just an ordinary git workflow: edit
`~/.den/sources/corp/` directly, commit, push — den adds no ritual on top.

### Repo keys — what makes a nest shareable

A shared nest cannot carry machine-specific paths. Its `repos:` entries carry `key:` instead of
`path:`, and each person maps that key to their own local checkout:

```yaml
# sources/corp/nests/backend.yaml (inside the source repo)
stack: dgdevx        # bare reference — resolved inside the source itself, never prefixed
repos:
  - { key: review-mgmt }
  - { key: front-app, optional: true, url: git@gitlab.corp:front/app.git }
```

```yaml
# ~/.den/config.yaml (personal, per machine)
repos:
  review-mgmt: ~/dev/review-mgmt
  front-app: ~/dev/front
```

A source carrying `den-source.yaml` reads its mapping from `~/.den/source-config/<name>.yaml`
instead — see [Declarative sources](#declarative-sources). One file per source, so two teams can
use the same key for two different repositories.

An unmapped key is a refusal **before any side effect** — no clone is ever attempted on the
user's behalf:

```
repo key "review-mgmt" is not mapped on this machine — add `review-mgmt: <local path>` under
`repos:` in ~/.den/config.yaml
```

`url:` only enriches that message; it exists to tell you what to clone, never to trigger a clone
den performs itself. Local nests can use `key:` too — it is one mechanism, not a sources-only one.

Keys are resolved **after** `--without`/`--only`, so an unmapped key on an *optional* repo is
escapable: a teammate who simply does not have the front-end checkout runs `den up corp:backend
--without front-app` and spawns without it. The refusal says so itself when the repo is optional.
A key on a repo that is still selected always refuses — den never drops a repo on its own — and
a *required* one is never offered the escape, since `--without` refuses required repos outright.

`select:` on a nest decides how it picks among its optional repos:

- `all` (default — the empty value means `all`, so no existing nest changes meaning) mounts every
  optional repo unless `--without`/`--only`/`-i` says otherwise.
- `prompt` declares a nest with **no default selection**: the repos are chosen at spawn time. With a
  terminal, the checklist opens without needing `-i`, and starts empty; without one, den refuses and
  names `--only`, which states the set outright from a script or CI. `--without` is **refused** on
  such a nest, for the same reason the checklist starts empty: there is no default selection to
  subtract from. An unknown `select:` value is refused, naming both modes.

Local nests can use `select: prompt` too — it is one mechanism, not a sources-only one — and it is
what makes a thirty-repo generic nest usable without cloning thirty repositories: a key that is not
mapped in your `~/.den/config.yaml` costs nothing as long as you do not select its repo. The
checklist annotates the keys you have not mapped, naming that file.

`den doctor` and `den nest show` read the mode too, so "costs nothing" holds there as well: on a
`select: prompt` nest an unmapped **optional** key is reported, not failed — doctor stays green and
names the keys on one line per nest, and `den nest show` resolves the nest and lists them with the
`repos:` line to add — unless the key is named on `--only`, where `den nest show` refuses it exactly
as a spawn does. On a `select: all` nest, and for a **required** key on any nest, it stays a
failing check and a refusal: there the repo is meant to be mounted.

It costs nothing *tomorrow* either, which is the half that is easy to miss: what you decline is
recorded, and every later attach reads that record back instead of re-deriving a selection from a
configuration that cannot know which four of thirty repos you picked. Without that, returning to
your own sandbox would refuse on the first key you had deliberately left out.

Re-attaching to a live `select: prompt` sandbox does not reopen the checklist: den rebuilds the
selection from the sandbox's creation record, prints the repos it was created with, and names
`--as <label>` as the way to run a different set alongside it. `-i` on an ordinary live nest
rebuilds from the record the same way and prints the same two lines — `-i` does not reopen the
checklist, but it no longer drops that explanation in silence. Without a readable
record — an older den, or a sandbox created outside den — den resolves every repo the nest declares
instead, and says so, naming `--only`/`--without` to pick a set explicitly — `--only` alone on a
`select: prompt` nest, where `--without` is refused as above.

### Addressing

A source's stacks and nests are addressed `<source>:<name>` (`corp:dgdevx`, `corp:backend`); a
local object stays unprefixed. Inside a source, references (`stack:` on a nest, `parent:` on a
stack) are always **bare** and resolve within that source itself — a prefixed reference there is a
lint failure, because the install name (`--name`) is chosen per machine and the team repo's own CI
knows none.

`:` is not legal in an `sbx create --name`, so a nest loaded from a source spawns under a
flattened sandbox name: `corp:backend` becomes sandbox `corp-backend` — the same flattening `-w`
already applies to branch names. A flattening collision (a local nest already named `corp-backend`,
say) is refused at spawn, never silently renamed.

`den exec`, `den shell`, `den rm` and `den ports` take **either** spelling: the reference you typed
(`corp:backend`, `corp:backend.feat12`) or the literal name `den ls` prints (`corp-backend`,
`corp-backend.feat12`). The literal one is decoded back to its source, which is unambiguous
precisely because spawn refused both competing decompositions up front.

The one case where the flattened name stops working is an ambiguity created **after** the spawn —
you write a local nest `corp-backend.yaml`, or install a second source that also decomposes the
name. den refuses rather than guessing which nest declares the sandbox's repos, and the prefixed
spelling `corp:backend` keeps working throughout. If the source is **uninstalled** after the
spawn, the flattened name has nothing left to decode from and den can only report the missing
local nest; `den source add` it again, or destroy the sandbox with `den rm --keep-worktrees`.

<a id="declarative-sources"></a>
### Declarative sources — one-command onboarding

A source can carry a **contract**: `den-source.yaml` at its root. den then knows what the source
needs from a machine — credentials, egress, images — and can converge all of it in one command:

```bash
$ den init --source git@gitlab.corp:dev/stacks.git
source: corp  version: 1.4.0

applying this plan builds stack base, which RUNS the provision scripts of source corp
(/Users/alice/.den/cache/sources/candidate-813/checkout) — confirm only a source you trust

RESOURCES
create    credential     github
unchanged credential     gitlab_registry
          observed: configured in sbx
update    build_network  build_network
          observed: 1 of 2 hosts allowed
          expected: registry.corp, proxy.corp

REPOSITORIES
review-mgmt  map to /Users/alice/dev/review-mgmt (its remote is this repository)
front-app    not on this machine — den does not clone it; the nests needing it stay not_ready

NESTS
backend            ready

status: ready

apply this plan?                              (inline Yes/No confirmation, defaults to No)
```

`den init --source` writes a source-aware `config.yaml` — no `defaults.stack`, no example nest, no
local stack — and installs the source. On a den home that already exists it changes **nothing** in
`config.yaml`, byte for byte. The same convergence runs from `den source add <url>` on an existing
home, and from `den source configure <name>` afterwards.

Nothing is applied before the plan is confirmed. `--yes` is the answer of someone who has read one
before (a CI, a provisioning script); with no terminal and no `--yes`, den prints the plan, applies
nothing and exits **zero**.

#### What the contract declares

```yaml
# den-source.yaml, at the root of the source repo
schema_version: 1
kind: source
metadata: { name: corp, version: 1.4.0 }   # name is a recommendation; --name overrides it
requires: { den: ">=1.7.0", sbx: ">=0.38.0" }  # floors; den installs neither binary
exports:                                    # explicit: a file not exported stays internal
  nests:
    - { name: backend, path: nests/backend.yaml }
  stacks:
    - { name: base, path: stacks/base/stack.yaml }
inputs:
  credentials:                              # what a human (or an answer file) supplies
    gitlab_token: { prompt: "GitLab personal access token" }
resources:
  credentials:                              # closed vocabulary — den names no command
    - { id: github, type: sbx_github, scope: global }
    - { id: gitlab_registry, type: sbx_registry, scope: global, host: registry.corp:443,
        value_from: { credential: gitlab_token } }
    - { id: gitlab_http, type: sbx_http_substitution, scope: global, host: gitlab.corp,
        environment: GITLAB_TOKEN, value_from: { credential: gitlab_token } }
  build_network: { allow: [registry.corp, proxy.corp] }
  builds:
    - { stack: base }
```

The vocabulary is **closed**: `sbx_github`, `sbx_registry`, `sbx_http_substitution`, all `scope:
global`. A manifest names no shell command, no hook and no plugin — it can only instantiate what
den compiles in. An unknown type is refused, never ignored.

A source **without** `den-source.yaml` keeps the behavior described above, unchanged: `den source
add` clones and lints it, `den source update` fast-forwards it, and den converges nothing for it.

#### Answering without a terminal

```yaml
# answers.yaml — the answers of ONE run; den stores none of it
repository_roots:                  # where to LOOK for working repos; den never clones
  - ~/dev
credentials:
  gitlab_token:
    from_env: GITLAB_TOKEN         # the variable NAME; a literal value here is refused
repos:                             # settle a discovery den will not make alone
  front-app: ~/dev/front
```

```bash
den init --source git@gitlab.corp:dev/stacks.git --answers answers.yaml --yes
```

A credential value never travels in an argv, and never lands in a plan, a log, an error, a config
or a receipt. den maps a repository only when it is a **fact** — you named the directory, or
exactly one directory under the roots carries the declared remote. A directory merely *named* like
the repository is reported for you to confirm; nothing is mounted on a guess.

#### The four files

| File | Owner | Holds |
|---|---|---|
| `<source>/den-source.yaml` | the team, in git | the contract above |
| `~/.den/config.yaml` | you | your personal settings; never travels through a source |
| `~/.den/source-config/<name>.yaml` | you, per machine | the exact version you converged, and this source's repo mapping |
| `~/.den/state/sources/<name>.yaml` | den | the convergence receipt: what was applied, when, from which commit |

#### Statuses

`ready` · `partially_ready` (a working repository is missing — den does not clone) · `blocked` ·
`unknown` (den could not observe the machine — never reported as "absent", the two have different
remedies). Nests are `ready` or `not_ready`. `den doctor` reports every declarative source with the
same verdict, and fails on `blocked` and `unknown`.

#### Updates and resume

`den source update <name>` fetches, then converges to the version the source **publishes**. A
greater `metadata.version` gets a full plan and a confirmation; the same version on a new commit is
reported and applied to nothing (the contract did not change — the team must publish a version); a
lower version is refused, with the checkout untouched. den converges forward.

An interrupted application leaves an `applying` receipt: the previous version stays the active one,
every consumer (`den up`, `den nest show`, `den build`) refuses rather than mix a new catalogue
with old infrastructure, and `den source configure <name>` finishes the job — without fetching, and
without reapplying what it can verify is already there.

#### Coming from `repos:` in `config.yaml`

A declarative source resolves its repo keys through `~/.den/source-config/<name>.yaml` **only**. If
keys it needs are still mapped in your global `config.yaml`, den prints the block to move and
copies nothing:

```
REPOSITORY MAPPINGS TO MIGRATE
/Users/alice/.den/config.yaml no longer supplies the repositories of a manifested source. den
copies nothing — check these are the same repositories, then add them to
/Users/alice/.den/source-config/corp.yaml yourself:

repos:
  review-mgmt: /Users/alice/dev/review-mgmt
```

Two teams may use `review-mgmt` for two different repositories; den will not decide that for you.

### Fail-closed updates

`den source update` is the only thing that touches the network — a spawn never fetches, so
everything keeps working off-VPN. It fetches, then lints the fetched tree **before** fast-forwarding
(in a throwaway detached worktree, never touching the checked-out branch until the lint passes): a
typo pushed by a teammate fails that lint and the local clone stays on its last good state, unchanged.
It also refuses to fast-forward over a dirty working tree or unpushed commits — den never discards
contributions it cannot restore. If the team repo rewrote its history, fast-forwarding itself
becomes impossible; the refusal explains that `den source rm` may itself refuse (the fetch that
triggered it just orphaned those same commits) and points at `den source rm --force` followed by
`den source add` for a clone with nothing local worth keeping.

At spawn, den never fetches — it just reads the clone as it stands. If the source has not been
fetched in over 7 days, den prints a **hint**, never a refusal:

```
hint: source "corp" was last fetched more than 7 days ago — den source update corp
```

### Authoring a source

The sections above are the installer's side. The publisher's side — the repo layout a source must
have, what a shared nest may declare, the full `den-source.yaml` field reference, and when to bump
`metadata.version` — is [`docs/source-authoring.md`](docs/source-authoring.md).

### `den lint <path>`

The same validation `source add`/`source update` run: strict YAML, `parent:` resolvable and
acyclic, declared paths (`kit`, `kits`, `provision.includes`/`steps`) existing and confined to the
checkout, bare (never prefixed) internal references, a nest with no `stack:`, and a nest whose
`repos:` carries **any** `path:`. An absolute one (`/Users/alice/dev/x`, or a `~/` that expands to
one) names a directory only the authoring machine has; a relative one resolves against whatever
directory each teammate launched den from, and a work repo lives outside the checkout anyway — so
neither travels, and both are refused. Declare `key:` instead and let each teammate map it. It
reports every finding at once, not one per push.
A stack's illegal name, missing `image:`, or a repo entry with both (or neither) `path:` and
`key:` are refused too — those surface as the load error on the offending file, which can end the
report early on that one file rather than join the itemized list above. Point it at a checkout to
run it standalone, e.g. from the team repo's own CI:

```bash
den lint .
```

It reads no den home and touches no git or `sbx` — an argument is a filesystem path, nothing more.

### Known limitations

A local object and a source object that share a **bare name** (`devx` locally, `corp:devx` from a
source) address different files everywhere — but a few things downstream are keyed by the bare
name alone, and are therefore shared between them:

- The **port window** when neither nest declares `ports.base:`: it is hashed from the bare nest
  name. They don't double-bind (the scan in [Ports](#ports) above shifts the second one to the
  next free block), but one of the two loses its stable, bookmarkable URL.
- The **build scratch directory** `cache/build/<stack>` and the throwaway sandbox name
  `<stack>-build`. Harmless when the two builds run one after the other; `den build devx` and
  `den build corp:devx` running concurrently collide.
- The declared **`image:` tag**. Two stacks in different roots that declare the same `image:`
  collide in sbx's global template store: `den build corp:devx` overwrites whatever the local
  `devx` built, and the local nest then spawns from the team's image with no warning. Unlike the
  others this one is author-chosen rather than derived, so it is avoidable — give a team stack a
  distinctive tag (`corp-devx:v1`) and the collision cannot arise.

One more, and it is the collision arriving late: a local nest named `corp-backend` spawns without
any check (den only tests a flattening collision when the reference it was given carried a source
prefix), so a live `corp-backend` may have come from either nest. den refuses the flattened name
rather than guess. `corp:backend` still works if that is where the sandbox came from; if it came
from the local nest, nothing reaches it and it has to be destroyed and re-spawned once the names
no longer collide. Renaming the local nest is the lasting fix — a rename inside a source is
reverted by its next `den source update`.

## Design

`docs/superpowers/specs/2026-07-27-den-cli-design.md`.
