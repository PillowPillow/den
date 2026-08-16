# sbx inspection contract spike

Date: 2026-08-14

Branch: `prototype/sbx-inspection-contract`

Installed binary:

```text
sbx version: v0.38.0 c022b14634c4bea846ca12870d1d5e97d5868b54
```

## Scope and safety

This spike observes the read-only commands required by the source onboarding convergence engine:

```text
sbx secret ls -g
sbx policy ls --type network --source local --decision allow --json
```

The committed fixtures contain synthetic names, reserved `.test` domains, synthetic IDs, and synthetic masked values. They contain no real secret fragment, username, host, policy ID, or keychain error text.

## Observations

These points come from command output produced by the installed `sbx v0.38.0` binary.

### Secret inspection

`sbx secret ls -g` has no JSON output flag. A successful invocation emits up to two text tables.

The first table starts with:

```text
SCOPE      TYPE       NAME                        SECRET
```

Observed `TYPE` values include `service` and `registry`. A service can report `(stored)` or `(oauth configured)` in the `SECRET` column. A registry reports a masked value. Den does not need the masked value.

When custom secrets exist, the same invocation adds a blank line followed by:

```text
CUSTOM SECRETS
SCOPE      TARGETS                ENV            PLACEHOLDER               SECRET
```

Den identifies a custom secret from its scope, targets, and environment variable. Den does not need the placeholder or masked value.

The sanitized representative fixture is `internal/converge/testdata/secret-ls.txt`.

### Policy inspection

The filtered policy command emits one JSON object. Its top-level key is `rules`. Each observed rule has these fields and types:

| Field | JSON type |
| --- | --- |
| `id` | string |
| `name` | string |
| `policy_id` | string |
| `scope` | string |
| `applies_to` | string |
| `resource_type` | string |
| `decision` | string |
| `resources` | array of strings |
| `origin` | string |
| `layer` | string |
| `status` | string |
| `editable` | boolean |

With `--type network --source local --decision allow`, the observed rules use:

```text
scope=global
applies_to=all
resource_type=network
decision=allow
origin=local
layer=local
status=active
editable=true
```

The sanitized representative fixture is `internal/converge/testdata/policy-ls.json`.

### Restricted execution

Both inspection commands failed with exit code 1 when the execution sandbox denied macOS Keychain access. Both commands succeeded with exit code 0 outside that sandbox, using the same binary and user profile.

Inference: the previously observed macOS keychain error `-50` is caused by the restricted execution boundary. It does not establish that the sbx profile is invalid.

## Contract for Den

1. Den invokes secret inspection as a text command and parses both tables independently.
2. Den uses `SCOPE`, `TYPE`, and `NAME` to identify service and registry credentials.
3. Den uses `SCOPE`, `TARGETS`, and `ENV` to identify custom credentials.
4. Den ignores all displayed secret values and custom placeholders.
5. Den invokes policy inspection with `--json` and compares exact strings in `rules[].resources`.
6. Den treats a non-zero inspection exit, malformed table, or incompatible JSON shape as an observation error. The resource status becomes `unknown`.
7. Den does not interpret keychain error `-50` as a missing credential or missing policy.
8. Den keeps parsing strict at the field level but accepts column-width changes and any number of data rows.

## Primary and secondary sources

- Primary source: observed output from the installed `sbx v0.38.0` binary on 2026-08-14.
- Secondary source: Docker credentials documentation, used to confirm the public `SCOPE TYPE NAME SECRET` table and the service, custom-secret, and registry concepts: <https://docs.docker.com/ai/sandboxes/security/credentials/>.
