# Authoring a den source

This document is for the person who **publishes** a team source — the git repo teammates install
with `den source add`. The consumer side (installing, updating, addressing `<source>:<name>`,
repo keys, the convergence plan) is in [README § Team sources](../README.md#team-sources); this
one only says what the repo itself must contain and what `den lint` judges before a push.

A source is a plain git repo. den adds no ritual on top: you commit and push it like any other
repo, and the only thing that makes it a source is its layout.

## Layout

```
<source repo>/
├── den-source.yaml          # optional — the onboarding contract (see below)
├── nests/
│   └── backend.yaml         # nest "backend"  → addressed corp:backend
├── stacks/
│   └── base/
│       ├── stack.yaml       # stack "base"    → addressed corp:base
│       └── provision/
│           ├── 00-apt.sh
│           └── 10-gh.sh
├── lib/
│   └── common.sh            # shared shell, reached from a stack as ../../lib/common.sh
└── kits/
    └── node/                # kit directories a stack declares via kit:/kits:
```

Same layout as a den home, minus the personal half. Two consequences:

- **A source never carries `config.yaml`.** den does not read one there — personal settings
  (`repos:` mappings, `defaults.stack`, ports) belong to each machine's `~/.den/config.yaml` and
  never travel through a source. A `config.yaml` committed in a source repo is dead weight, not a
  refusal.
- **A source is self-contained.** Every path a stack declares (`kit:`, `kits:`,
  `provision.includes:`, `provision.steps:`) must exist inside the checkout and resolve inside it —
  including through symlinks. A path escaping the tree is refused by `den lint`: it depends on the
  machine that receives the clone.

`den` names objects by location, not by a field: a nest is `nests/<name>.yaml`, a stack is
`stacks/<name>/stack.yaml`. Rename the file, and you rename the object.

## What a shared nest may declare

```yaml
# nests/backend.yaml
stack: base                  # REQUIRED, and bare — never `corp:base`
select: all                  # all (default) | prompt
repos:
  - { key: review-mgmt }
  - { key: front-app, optional: true, url: git@gitlab.corp:front/app.git }
```

- **`stack:` is required.** A source nest cannot fall back on the installer's personal
  `defaults.stack` — it must spawn identically on every machine.
- **References inside a source are bare.** `stack:` on a nest and `parent:` on a stack resolve
  within that source alone. A prefixed reference (`corp:base`) is a lint refusal: the install name
  is chosen per machine (`den source add --name`), and the source's own CI knows none.
- **`repos:` entries carry `key:`, never `path:`.** *Any* `path:` is refused — absolute or
  relative. A work repo lives outside the checkout, so no path written in the source names it on a
  colleague's machine. Each teammate maps the key on their own side (in `~/.den/config.yaml`, or in
  `~/.den/source-config/<name>.yaml` for a manifested source).
- **`url:`** only enriches the "key is not mapped" message. den never clones on the user's behalf,
  and `url:` without `key:` is refused — on a `path:` entry it would never be read.
- **`select:`** accepts `all`, `prompt`, or nothing (empty means `all`, so no existing nest changes
  meaning). Any other value is refused, naming both modes.
- **`optional: true` plus `select: prompt`** is what makes a thirty-repo nest usable: an unmapped
  key costs nothing as long as its repo is not selected. See README § Repo keys.

## What `den lint` judges

```bash
den lint .        # what your CI runs
```

It reads no den home and touches no git, no `sbx` and no network — the argument is a filesystem
path, nothing more. It reports **every** finding at once, so one push fixes the whole repo.

The same code runs on the installer's machine: `den source add` lints the fresh clone (and
**deletes** it on a finding), and `den source update` lints the fetched tree *before*
fast-forwarding. One judge, so lint can never accept what a spawn would later refuse.

What it checks:

| Object | Rule |
|---|---|
| every YAML | strict decoding — an unknown key is an error, never a silence |
| stack | legal name, `image:` present, `parent:` bare, resolvable and acyclic |
| stack | `kit:`/`kits:`/`provision.includes:`/`provision.steps:` written relative, existing, confined to the checkout |
| nest | `stack:` present, bare, and resolvable |
| nest | no `path:` under `repos:` — declare `key:` |
| repo entry | exactly one of `path:`/`key:`, and `url:` only beside `key:` |
| nest | `select:` is `all`, `prompt` or empty |

The last three rows, plus a stack's illegal name and a missing `image:`, surface as the load error
on the offending file — which can end the report early on that one file rather than join the
itemized list.

## The onboarding contract — `den-source.yaml`

Optional. Without it the source stays **legacy**: `den source add` clones and lints it,
`den source update` fast-forwards it, and den converges nothing on the machine. With it, den knows
what the source needs from a machine (credentials, egress, images) and converges it in one command
— see README § Declarative sources for what the installer sees.

```yaml
schema_version: 1
kind: source
metadata:
  name: corp                 # a RECOMMENDATION — `den source add --name` overrides it
  version: 1.4.0
requires:                    # floors; den installs and downgrades no binary
  den: ">=1.7.0"
  sbx: ">=0.38.0"
exports:                     # explicit: a file not listed here stays internal
  nests:
    - { name: backend, path: nests/backend.yaml }
  stacks:
    - { name: base, path: stacks/base/stack.yaml }
inputs:
  credentials:
    gitlab_token: { prompt: "GitLab personal access token" }
resources:
  credentials:
    - { id: github, type: sbx_github, scope: global }
    - { id: gitlab_registry, type: sbx_registry, scope: global, host: registry.corp:443,
        value_from: { credential: gitlab_token } }
    - { id: gitlab_http, type: sbx_http_substitution, scope: global, host: gitlab.corp,
        environment: GITLAB_TOKEN, value_from: { credential: gitlab_token } }
  build_network:
    allow: [registry.corp, proxy.corp]
  builds:
    - { stack: base }
```

### Field reference

| Key | Required | Rule |
|---|---|---|
| `schema_version` | yes | must be `1` — it versions the YAML **shape**, independently of `metadata.version` |
| `kind` | yes | must be `source` |
| `metadata.name` | yes | a legal source name; a recommendation only |
| `metadata.version` | yes | exact SemVer `<major>.<minor>.<patch>`, **no leading `v`**, no prerelease, no build metadata |
| `requires.den` / `requires.sbx` | no | `">=<major>.<minor>.<patch>"` only — a range is refused, since den's only answer to an unmet floor is a refusal |
| `exports.nests[]` | no | `{name, path}`; `path` must be exactly `nests/<name>.yaml` |
| `exports.stacks[]` | no | `{name, path}`; `path` must be exactly `stacks/<name>/stack.yaml` |
| `inputs.credentials.<id>.prompt` | — | the prompt shown for a transient secret. No default, no value: a secret in a versioned team file is a leaked secret |
| `resources.credentials[]` | no | see the closed vocabulary below |
| `resources.build_network.allow[]` | no | a bare host, optionally `host:port` |
| `resources.builds[]` | no | `{stack}`, and the stack must appear in `exports.stacks` |

Every key is `snake_case`. Decoding is strict, so a camelCase spelling is a load error, not a
silently empty half of the contract.

**`path:` is redundant with `name:` on purpose.** den loads both nests and stacks *by name*, so a
file published from anywhere else could never be spawned; the redundancy exists to be checked. An
export name must also be a legal sandbox name component (a nest name becomes one — `sbx create` has
no `--label`), and names are unique within their category (a stack and a nest may share a name).

### The credential vocabulary is closed

A manifest names no shell command, no hook and no plugin — it can only instantiate what den
compiles in. An unknown `type:` is refused, never ignored. `scope:` is always `global`: an
onboarding contract configures a **machine**, and a sandbox-scoped secret would have to be
reapplied by every spawn.

| `type` | `host` | `environment` | `value_from.credential` |
|---|---|---|---|
| `sbx_github` | refused | refused | refused — `sbx` collects it interactively |
| `sbx_registry` | required | refused | required |
| `sbx_http_substitution` | required | required | required |

A field the type does not use is **refused**, not ignored: writing `host:` under an `sbx_github`
entry means you expected it to do something. `value_from.credential` must name a key declared under
`inputs.credentials` — otherwise den has nothing to prompt for and an answer file has no name to
answer. `id:` is required and unique; it is the name every plan, error and receipt uses.

`value_from` can only name a declared input. It cannot name a file, a command or an environment
variable: where a value comes from on *this* machine is a personal question, answered by the wizard
or the installer's answer file, never by the team's versioned manifest.

### `build_network.allow` entries

One bare host, optionally with a port. Refused: an empty entry, leading or trailing whitespace, a
scheme (`https://…`), a path or a CIDR range (`10.0.0.0/8`). `sbx` appends `:443` to anything
without a colon, so a mangled entry would become a rule den can never match back against what it
just applied.

### Lint on a manifested source

With a contract, lint judges the **catalogue** instead of whatever the directories contain — the
teammate addresses exported names and nothing else. Two rules follow, and they are not symmetric:

- **A nest's `stack:` must resolve to an EXPORTED stack.** A stack present in `stacks/` but absent
  from `exports.stacks` is an implementation detail; a nest resolving through it would lint clean
  here and refuse on the teammate's machine.
- **A stack's `parent:` may resolve to an unexported stack.** A parent is internal composition —
  nothing on the personal side addresses it — and `den build` walks the chain through the whole
  checkout. Unexported stacks lying on an exported stack's ancestry are judged too (their own
  `stack.yaml`, their declared paths, their own `parent:`), because that is exactly what a build
  walks. An orphan stack no export reaches stays unjudged.

Plus the contract's own checks: every export names a readable file at its canonical location,
inside the checkout.

## Publishing a version

`metadata.version` is the operational content of the source — the thing installers converge on.
`schema_version` is den's YAML shape and only changes when den changes it; a source publishing
1.0.0 and later 4.2.0 of itself keeps writing `schema_version: 1`.

What `den source update` does with a fetched candidate:

| Candidate vs the machine's configured version | den |
|---|---|
| greater | plans the convergence, asks for confirmation, applies |
| equal, same commit | nothing to do |
| equal, **different commit** | reports the drift and applies **nothing** — the checkout is left alone |
| lower | refuses; the checkout is untouched |

So the rule for a publisher: **bump `metadata.version` in the same commit as any change that must
reach a machine.** A change pushed under an unchanged version is drift — den will not move the
checkout under it, because the same contract must describe the same machine state, and every
teammate's receipt still attests that version. And den converges forward only: publishing a lower
version is a fault to fix, not a rollback mechanism (the older version's resources were, by
construction, not applied to reach the newer one, and nothing says what un-applying would mean).

## Checklist before a push

1. `den lint .` reports nothing.
2. Every nest a teammate should address is listed under `exports.nests`; every stack, under
   `exports.stacks` (a manifested source only).
3. No `path:` under any nest's `repos:` — `key:` only.
4. `metadata.version` bumped if anything changed that a machine must converge.
5. No secret, token or default credential value anywhere in the tree.
