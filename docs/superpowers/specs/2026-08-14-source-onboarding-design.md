# Source onboarding and convergence

**Date:** 2026-08-14
**Status:** Validated design

## 1. Purpose

Den currently separates a personal den home from Git-backed team sources, but a new machine still
needs several manual steps before the first useful sandbox can start. The Digitaleo setup currently
requires a generic `den init`, a source installation, machine-level network policy changes, three sbx
credentials, a large repository mapping, an image build, and separate diagnostics. `den doctor` also
cannot establish whether a particular source is operational.

This design adds a strictly declarative source manifest and a shared convergence engine. Digitaleo is
the tracer bullet for a generic Den contract. The primary entry point is:

```bash
den init --source <url>
```

The command converges a fresh or existing den home. It finishes when the source infrastructure is
operational, even when some exported nests are not ready because their working repositories are not
present.

## 2. Goals

- Let a source declare its identity, functional version, compatibility, exports, and installation
  resources in `den-source.yaml`.
- Make `den init --source`, `den source add`, `den source configure`, and `den source update` use one
  convergence engine.
- Show a complete plan before mutation and request confirmation.
- Support the same flow through an interactive wizard or a non-interactive answer file.
- Keep personal source configuration separate from global Den configuration and source-controlled
  team files.
- Use exact functional source versions. Never update a source implicitly from ordinary commands.
- Detect and map existing working repositories without cloning them.
- Report each exported nest as `ready` or `not_ready`.
- Make partial application resumable and protect commands from using a half-applied source version.

## 3. Non-goals

- Install or update the `den` and `sbx` binaries. They are prerequisites.
- Execute manifest-defined shell hooks or load external resource implementations.
- Clone working repositories.
- Migrate the existing global `config.yaml.repos` mapping automatically.
- Move MCP variables or MCP credentials out of `config.yaml`.
- Add automatic source updates in this delivery.
- Provide rollback for sbx credentials, network policies, or built images.
- Turn Den into a general-purpose machine configuration manager.

## 4. Design principles

### 4.1 One owner per file

Four files or directories have distinct owners and lifecycles:

| Location | Owner | Purpose |
|---|---|---|
| `~/.den/sources/dg/den-source.yaml` | team | versioned onboarding contract |
| `~/.den/config.yaml` | user | global Den settings only |
| `~/.den/source-config/dg.yaml` | user | durable configuration for the installed `dg` source |
| `~/.den/state/sources/dg.yaml` | Den | non-editable convergence receipt |

The installed source checkout continues to own its nests, stacks, kits, and provision files.

### 4.2 Configuration keys use `snake_case`

Every key introduced by this design uses `snake_case`. Strict YAML decoding rejects unknown keys,
which also rejects accidental camelCase spellings such as `schemaVersion` or `repositoryRoots`.

Names and other domain identifiers are values, not schema keys. Existing repository identifiers such
as `go-dgdev` and `js.agentic-bank` do not need to change. Identifiers used as dynamic mapping keys,
such as credential names, use `snake_case`.

### 4.3 Closed resource vocabulary

The manifest can instantiate only resource types compiled into Den. It cannot register code, name a
shell command, or provide a hook. Each resource implementation supports the same internal lifecycle:

```text
inspect -> plan -> apply -> verify
```

This lifecycle is an internal Go interface, not an extension API exposed to the manifest.

Building a declared stack still executes that stack's existing provision files. The plan must state
this trust boundary and name the source before confirmation.

## 5. Source manifest

### 5.1 Location and identity

An onboardable source contains `den-source.yaml` at its root. The initial schema is:

```yaml
schema_version: 1
kind: source

metadata:
  name: dg
  version: 1.0.0

requires:
  den: ">=1.7.0"
  sbx: ">=0.38.0"
```

`metadata.name` is the recommended installation namespace. `--name` overrides it. A functional
version is a valid SemVer value and identifies the operational content published by the source.

`schema_version` versions the YAML contract. `metadata.version` versions the source itself. They are
independent.

### 5.2 Explicit exports

The source publishes a durable catalogue, similar to the exports of a plugin:

```yaml
exports:
  nests:
    - name: agentic-bank
      path: nests/agentic-bank.yaml
    - name: go-dgdev
      path: nests/go-dgdev.yaml
    - name: kafoutche
      path: nests/kafoutche.yaml
    - name: leo
      path: nests/leo.yaml
    - name: op-inscription
      path: nests/op-inscription.yaml

  stacks:
    - name: base
      path: stacks/base/stack.yaml
```

Den does not infer exports by scanning the source tree. Files that exist but are not exported remain
implementation details of the source.

`den lint` verifies:

- export names are unique within their category;
- paths are relative, confined to the source, and point to readable files;
- each exported name agrees with the decoded file;
- every exported nest resolves its stack through the source's exported stack catalogue;
- every resource refers only to declared inputs, exports, and supported resource types;
- schema and functional versions are valid.

### 5.3 Declarative resources

The Digitaleo tracer bullet needs a closed set of credential, machine-policy, and stack-build
resources. A representative manifest is:

```yaml
inputs:
  credentials:
    gitlab_token:
      prompt: GitLab personal access token

resources:
  credentials:
    - id: github
      type: sbx_github
      scope: global

    - id: gitlab-registry
      type: sbx_registry
      scope: global
      host: gitlab.digitaleo.com:4567
      value_from:
        credential: gitlab_token

    - id: gitlab-http
      type: sbx_http_substitution
      scope: global
      host: gitlab.digitaleo.com
      environment: GITLAB_TOKEN
      value_from:
        credential: gitlab_token

  build_network:
    allow:
      - cdn.playwright.dev
      - acli.atlassian.com

  builds:
    - stack: base
```

The exact Go types may split these groups internally, but the YAML remains finite and schema-driven.
The first implementation supports only the resource shapes required by the Digitaleo manifest.

Credential inputs are transient. Den reads them only when inspection shows that a resource needs
application. Den never writes a credential value into personal configuration, state, a plan, a log,
or an error.

## 6. Personal source configuration

`~/.den/source-config/<installation-name>.yaml` contains only data needed by later Den commands:

```yaml
schema_version: 1
version: 1.0.0

repos:
  go-dgdev: ~/Development/Digitaleo/go.dgdev
  js.agentic-bank: ~/Development/Digitaleo/js.agentic-bank
```

`version` is an exact target version, not a range or update channel. Repository mappings are scoped
to this source installation. The same key may map differently in another source.

Source resolution uses this mapping for manifested sources. The legacy global `config.yaml.repos`
mapping remains supported for local nests and legacy sources, but it is not consulted for a
manifested source.

There is no automatic migration. If an existing user already has source mappings under
`config.yaml.repos`, the command reports them as legacy and prints the exact destination file and
YAML block to copy. It never copies or removes those entries.

Personal source configuration is written atomically with private permissions.

## 7. Answer files and repository discovery

### 7.1 Temporary answers

An answer file supplies one execution's inputs. It is not copied into the den home:

```yaml
repository_roots:
  - ~/Development/Digitaleo
  - ~/Development/Kampn

credentials:
  gitlab_token:
    from_env: GLPAT

repos:
  js.agentic-bank: ~/Development/Digitaleo/js.agentic-bank
```

`repos` is optional and lets a non-interactive run resolve an ambiguous or non-standard directory.
Credential values may come from environment references. A literal credential in an answer file is
rejected in the first schema so the recommended automation path cannot silently persist a secret.

`--answers <path>` replaces the interactive collector. `--yes` authorizes application of the
printed plan. Without a terminal or `--yes`, Den prints the plan and exits without mutation when
confirmation is required. Tests inject an answer provider and confirmation, but exercise the same
planner and applier as the interactive command.

### 7.2 Discovery

Den collects repository keys and URLs from every exported nest. `repository_roots` tells Den where
to search for this execution. It is deliberately absent from `source-config/dg.yaml` and the receipt.

For each direct child of each root, Den:

1. detects whether the directory is a Git repository;
2. normalizes its configured remotes and the declared repository URL;
3. treats an exact normalized remote match as a strong candidate;
4. otherwise compares the directory name with the repository URL basename and repository key;
5. reports ambiguous or name-only matches for confirmation.

The plan confirmation accepts unambiguous remote matches. A name-only or ambiguous match requires an
explicit interactive choice or an entry under `repos` in the answer file. Den writes only confirmed
mappings.

Den does not clone a missing repository. A missing required repository makes each dependent nest
`not_ready`. A missing optional repository does not make a nest `not_ready`.

## 8. Global initialization

`den init --source <url>` works with both a fresh and an existing den home.

On a fresh home it writes a source-aware minimal `config.yaml`: the shipped default Claude agent,
the default agent selection, SSH defaults, worktree layout, and baseline egress. It does not create
the placeholder `nests/example.yaml` or local `stacks/devx/stack.yaml`.

This requires `defaults.stack` to become optional globally. A local nest that omits `stack` fails at
resolution time with a contextual error when no global default stack exists. Source nests already
declare their stack and never use the global default.

On an existing home Den preserves every global value. It creates or updates only the source checkout,
personal source configuration, and source receipt after confirmation.

`den init` without `--source` retains its current embedded-example behavior in this delivery.

## 9. Convergence engine

### 9.1 Components

The CLI commands delegate to one application service composed of focused modules:

- **manifest loader:** strict decoding, schema validation, and compatibility checks;
- **source acquirer:** temporary clone, existing-source lookup, namespace resolution, and installation;
- **answer provider:** interactive wizard or answer file;
- **resource inspector:** read-only observation through typed Den and sbx adapters;
- **planner:** deterministic diff from observed to desired state;
- **resource applier:** typed, ordered mutations;
- **readiness evaluator:** resolution of every exported nest against confirmed repository mappings;
- **state writer:** atomic personal-configuration and receipt writes.

The planner emits `unchanged`, `create`, `update`, or `blocked` for each managed resource. It performs
no mutation. All terminal renderers consume the same plan model.

### 9.2 `den init --source`

The command executes this sequence:

1. load the existing global configuration or prepare the source-aware minimal configuration;
2. find an installed source with the same normalized URL or clone into a temporary directory;
3. decode and validate `den-source.yaml`;
4. determine the namespace from `--name` or `metadata.name`;
5. reject a namespace already owned by a different URL;
6. verify the Den and sbx versions without installing either binary;
7. load the answer file or collect temporary wizard inputs;
8. inspect every declared resource and calculate the complete plan;
9. print the plan, including the stack-provision trust boundary, and request confirmation;
10. configure required sbx credentials;
11. configure required machine network policies;
12. build every stack named by `resources.builds`;
13. inspect every exported nest and discover repositories under `repository_roots`;
14. confirm repository mappings and evaluate each nest's readiness;
15. install the source when necessary, atomically replace personal configuration, then atomically
    replace the receipt as the final commit marker;
16. print source and per-nest status with exact next actions.

A managed infrastructure resource that cannot converge fails the command. Missing working
repositories do not fail it.

### 9.3 Shared command behavior

- `den source add <url>` installs and converges a manifested source in an initialized home.
- `den source configure <name>` converges the installed exact version without contacting its remote.
  It is the command used to discover repositories added after initial setup or resume partial work.
- `den init --source <url>` performs global initialization and then the same source convergence as
  `source add`.

An existing source without `den-source.yaml` remains in legacy mode. `source add` and `source update`
retain their current clone, lint, and fast-forward behavior for such a source. `init --source`
requires a manifest because it cannot otherwise guarantee readiness.

## 10. Status and receipt

### 10.1 Status vocabulary

- `ready`: every managed infrastructure resource and every exported nest is ready;
- `partially_ready`: managed infrastructure is ready, but at least one exported nest is
  `not_ready` because a required working repository is absent;
- `blocked`: a managed dependency prevents the source from operating;
- `unknown`: Den cannot observe a required component, for example because the sbx daemon does not
  respond.

`den init --source` succeeds for `ready` and `partially_ready`. It fails for `blocked` and `unknown`.
`den source status` follows the same exit-status rule.

### 10.2 Receipt

`~/.den/state/sources/dg.yaml` is Den-managed and not user-editable. A successful receipt resembles:

```yaml
schema_version: 1
status: partially_ready
version: 1.0.0
commit: 0123456789abcdef
manifest_digest: sha256:0123456789abcdef
applied_at: 2026-08-14T12:00:00Z

resources:
  credentials: ready
  build_network: ready
  stacks:
    base: ready

nests:
  go-dgdev:
    status: ready
  leo:
    status: not_ready
    missing_repos:
      - js.agentic-bank
```

The receipt never contains credential values or temporary repository roots. It uses private
permissions and atomic replacement.

## 11. Source updates

### 11.1 No implicit remote access

Ordinary commands use the exact local version from personal source configuration. `spawn`, `build`,
`source status`, and `source configure` do not fetch, pull, or check for a newer version.

### 11.2 Explicit update flow

`den source update dg`:

1. refuses a dirty checkout or commits not reachable from a remote-tracking reference;
2. fetches without changing the checkout;
3. reads and validates the candidate manifest from the fetched commit;
4. compares its SemVer version with `source-config/dg.yaml.version`;
5. makes no checkout change when the versions are equal;
6. refuses a candidate version lower than the active version;
7. calculates and displays the full convergence plan for a greater version;
8. requests confirmation;
9. advances the checkout and applies the plan;
10. verifies resources and readiness;
11. atomically records the new exact version and final receipt.

A changed remote commit with the same functional version is not applied. Den warns that the source
changed without a version increment. Source maintainers must increment `metadata.version` for every
operational release.

### 11.3 Partial update

Personal source configuration retains the previous version until all managed resources verify. The
receipt records `status: applying`, the previous version, target version, target commit, and completed
resource results.

If application fails, Den does not rollback already applied credentials, policies, or images. A later
`den source configure dg` resumes the target convergence and skips resources that already verify.

Commands that consume the source refuse while its checkout version, active configured version, and
receipt diverge. Their error names the pending target and the configure command that resumes it.

After complete verification, Den atomically replaces the configured exact version and final receipt.
This exact-version model is also the future seam for an automatic updater: it can detect a greater
version and invoke the same planner without changing source semantics.

## 12. Diagnostics and errors

### 12.1 Source status

`den source status [name]` observes state without mutation:

```text
source: dg  version: 1.0.0  status: partially_ready

RESOURCES
credentials       ready
build_network     ready
stack:base        ready

NESTS
go-dgdev          ready
leo               not_ready   missing repo: js.agentic-bank
```

It does not contact the remote. Each missing repository diagnostic includes its key, declared URL,
dependent nests, and the `den source configure dg` remedy.

### 12.2 Doctor integration

`den doctor` aggregates global Den and sbx checks with every manifested source's receipt and current
observed state. It reports checkout/configuration/receipt divergence and per-nest readiness.

If the sbx daemon does not respond, doctor reports `unknown`; it must not print `all good`. A required
check that is `blocked` or `unknown` produces a non-zero exit status. Missing working repositories are
reported as `partially_ready` and do not make source installation fail.

### 12.3 Error contract

Every convergence error names:

- the resource;
- observed state;
- expected state;
- remaining automatic or manual action;
- the exact command to resume.

Credential values are rendered as `<redacted>` in plans, terminal output, logs, errors, and receipts.

## 13. Testing strategy

### 13.1 Unit tests

- strict YAML decoding, including rejection of unknown and non-`snake_case` keys;
- schema version, SemVer, compatibility, export, confinement, and reference validation;
- normalized Git remote comparison;
- repository discovery, ambiguity, required, and optional behavior;
- deterministic resource plans and stable rendering;
- credential redaction in every output model;
- interactive and answer-file providers producing identical typed answers;
- exact-version comparison and update decisions;
- readiness and aggregate source status.

### 13.2 Integration tests

Use temporary Git repositories, temporary den homes, and controlled sbx adapters to cover:

- fresh source-aware initialization;
- convergence with an existing global configuration without overwriting it;
- absence of automatic `config.yaml.repos` migration;
- manifested source mappings taking precedence only within that source;
- missing required repositories producing `partially_ready` without command failure;
- missing optional repositories preserving nest readiness;
- managed-resource failure producing `blocked`;
- unobservable sbx state producing `unknown`;
- partial application and configure-based resume;
- equal-version update leaving the checkout unchanged;
- greater-version update producing a plan and requiring confirmation;
- downgrade refusal;
- dirty or unpushed source refusal;
- source commands refusing version divergence;
- legacy sources retaining current add and update behavior.

### 13.3 Digitaleo acceptance fixture

The real Digitaleo manifest is exercised with a fake sbx backend and temporary repositories. An
answer file drives the non-interactive path through the same engine as the wizard. The scenario
asserts the applied credential and policy requests, stack build order, source-scoped mappings,
per-nest readiness, redacted output, personal configuration, and final receipt.

## 14. Delivery boundaries

The implementation spans two repositories but remains one vertical slice:

1. Den gains the manifest schema, source configuration, convergence engine, commands, diagnostics,
   and tests.
2. `digitaleo-den-env` gains `den-source.yaml`, a functional version, explicit exports, and the
   declarative resources needed by its current README installation steps.
3. Documentation replaces the manual Digitaleo onboarding sequence with `den init --source`, an
   answer-file example, and manual instructions for moving the existing user's repository mappings.

Automatic updates, credential storage beyond sbx, repository cloning, and generic resource plugins
remain later designs.
