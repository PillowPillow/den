# Files on disk

den keeps its config and state in the **den home**, `~/.den` by default (`--den-home <dir>` or
`$DEN_HOME` moves it). It also writes in your repos and in project folders, as listed below.

Words used here: a **nest** is what you declare in a `den.yaml`; an **instance** is the sbx VM den
creates from it; a **kit** is an sbx package of files and rules added to a VM; a **template** is the
image a VM starts from. A **record** is the file in which den notes what it created. See the
[README](../README.md#words-den-uses).

## In the den home

| Path | Content | Owner |
|---|---|---|
| `~/.den/den.yaml` | the global file, see [den-yaml.md](den-yaml.md) | you |
| `~/.den/run/<instance>/` | the run dir of an instance of a global nest: the generated `sbxenv.yaml`; it is the VM's workspace | den |
| `~/.den/gen/<instance>/` | the instance's generated kits (network allowlist, `den-claude` with the repo map) and, with `-w`, the read-only `.git` files of its worktrees | den |
| `~/.den/gen/.templates/<name>/` | den's clone of a `repo` template (a build in progress uses `.staging-<name>/`) | den |
| `~/.den/gen/.build/<name>/`, `~/.den/build/<name>/` | the generated files and the scratch workspace of a template build | den |
| `~/.den/wt/` | the worktree root of the `central` layout, unless `worktrees.root` sets another | den |
| `~/.den/state/v2/instances/<instance>.yaml` | the record of an instance, read by every command that acts on it | den |
| `~/.den/state/v2/nests.yaml` | which file declares which nest | den |
| `~/.den/state/v2/templates/<name>.yaml` | the record of a built template: source, commit, definition hash, root image and its digest, tag, build time | den |
| `~/.den/state/v2/template-sources.yaml` | the template git URLs you trusted | den |

## Outside the den home

| Path | Content | Owner |
|---|---|---|
| `<project>/den.yaml` | a project file: templates and nests that travel with the project | you |
| `<project>/.den/run/<instance>/` | the run dir of an instance of a project nest; den adds `<project>/.den/.gitignore` | den |
| `<repo>/.den/<branch>/` | a worktree with `worktrees.layout: per-repo`; den adds `.den/` to the repo's `.git/info/exclude` | den |
| `<repo>/.git/worktrees/` | git's own bookkeeping for every worktree den creates or locks | git |

## Inside the VM

- Your repos appear at their host paths.
- den writes the repo map to `~/.claude/rules/den-instance.md` (in the image's home, `/home/agent` on
  `claude-code-docker`) and `/home/agent/.den/repos.json` (see [den-yaml.md](den-yaml.md#repo-map)).
- den merges the kits' `~/.den/claude-settings.d/*.json` files into `~/.claude/settings.json` at
  every start (see [templates.md](templates.md#default-claude-settings-from-a-kit)).

## Rules

- **The run dir is disposable.** `den rm` deletes it without checking it. Keep nothing there. With
  `start: .`, the VM's workspace is the project folder instead of the run dir.
- **den never reads back what it generated.** When den attaches to a running instance, what it
  mounted is what its record says, not what a VM may have edited since. When the VM is gone, den
  creates it again from the current config and rewrites the generated files.
- **`state/` is never purged.** Delete a record by hand only when den tells you to.

sbx keeps its own state (sandboxes, templates, policies, secrets) outside the den home. List it with
`sbx ls`, `sbx template ls`, `sbx policy ls` and `sbx secret ls`.
