---
name: profile
description: Use when the user wants to bring their own Claude configuration (CLAUDE.md, skills, agents, output styles, settings.json) into den VMs, by creating a profile from the host's ~/.claude, or by declaring as a profile a kit folder that already exists under the den home, such as kits/pro.
---

Read ${CLAUDE_PLUGIN_ROOT}/rules.md and follow it.

Facts come from ${CLAUDE_PLUGIN_ROOT}/reference/den-yaml.md#profiles. Run the steps in order.

1. Name the profile.
   - Read `${CLAUDE_PLUGIN_ROOT}/reference/den-yaml.md#profiles`.
   - Ask the user for the profile name.
   - Read the `profiles:` key of `${DEN_HOME:-$HOME/.den}/den.yaml`. If the name is already defined there, stop and ask the user whether to replace that entry; do not go on without their agreement.
   - If a folder `${DEN_HOME:-$HOME/.den}/kits/<name>` already exists and holds a `spec.yaml`, it is a kit: declare it as it is, without copying anything, and go to step 6.

2. Choose the files to bring. List `~/.claude/` of the host (`ls -A ~/.claude`). Propose one by one, and copy only what the user accepts into `${DEN_HOME:-$HOME/.den}/kits/<name>/files/home/.claude/`:
   - `CLAUDE.md`;
   - `skills/`;
   - `agents/`;
   - `output-styles/`;
   - `settings.json`, through step 3 and step 4 only (never a plain copy).

   Never copy `~/.claude.json`, `.credentials.json`, `projects/`, `history.jsonl`, `todos/`, `statsig/`, `shell-snapshots/`, `plugins/`, nor any other state or history file, even if the user asks for "everything".

3. Go through `settings.json` key by key with the user, without printing a secret. If `jq` is absent from the host, do not copy `settings.json` and tell the user why: `env` cannot be filtered without it.
   - First list the key names and value types only: `jq 'del(.env) | map_values(type)' ~/.claude/settings.json` (step 4 covers `env`).
   - For a key not on the lists below, show its shape first: `jq '.<key> | if type=="object" then map_values(type) elif type=="array" then length else type end' ~/.claude/settings.json`. For `permissions`: `jq '.permissions | map_values(if type=="array" then length else type end)' ~/.claude/settings.json`.
   - Show the full value of a key only when the user asks for that key, after warning that `permissions` rules and other strings can embed a token: `jq '.["<key>"]' ~/.claude/settings.json`. A shown value stays in the transcript, so never repeat a secret from it.
   - Never print the value of a key on the command or path lists below. Show only its type and that it is removed by default. If the user wants to keep one, they read its value in their own terminal (rule 6).
   - General rule: any value that is a command run on the host, or an absolute path of the host, is shown to the user and removed by default, because the VM has neither the host's programs nor its folders.
   - Keys known to hold such a value, checked against the Claude Code settings reference (https://code.claude.com/docs/en/settings-reference):
     - commands: `hooks`, `statusLine`, `apiKeyHelper`, `awsAuthRefresh`, `awsCredentialExport`, `gcpAuthRefresh`, `otelHeadersHelper`, `fileSuggestion`, `sshHostCommands`;
     - executables: `processWrapper`, `policyHelper`, `sandbox` (its `bwrapPath`, `socatPath`, `ripgrep`, `excludedCommands` and `filesystem` paths);
     - host folders: `permissions.additionalDirectories` when an entry is an absolute path, `autoMemoryDirectory`, `plansDirectory`, `worktree`;
     - file paths: `claudeMdExcludes`.
   - Apply the same rule to any other key whose value is a host command or an absolute host path, including permission rules that name one.
   - `enabledPlugins` and `extraKnownMarketplaces`: remove them by default. Tell the user what was observed: in a trial in a VM, Claude Code did not install the plugin these keys declare during a session of about two minutes, although the network allowed it; the cause is not established. The user may still choose to keep them.
   - Keep the other keys the user accepts.

4. Filter `env` without ever showing a value.
   - Never read `settings.json` with the Read tool, and never print an `env` value.
   - List the names only: `jq '(.env // {}) | keys' ~/.claude/settings.json`. With no `env` or no name, skip the question.
   - Create the folder first: `mkdir -p "${DEN_HOME:-$HOME/.den}/kits/<name>/files/home/.claude"`.
   - Write the filtered file with `jq`, in one command, deleting the keys removed in step 3 and keeping only the chosen `env` names. With no name kept, delete `env` entirely. Write to a temporary file and move it into place only when `jq` succeeds. Pass the lists as arguments, for example:

     ```
     K="${DEN_HOME:-$HOME/.den}/kits/<name>/files/home/.claude/settings.json"
     jq --argjson keep '["NAME_A","NAME_B"]' \
        'del(.hooks, .statusLine) | if .env then .env |= with_entries(select(.key as $k | $keep | index($k))) else . end | if .env == {} then del(.env) else . end' \
        ~/.claude/settings.json > "$K.tmp" && mv "$K.tmp" "$K"
     ```

   - Check the result on the same file with `jq 'del(.env) | map_values(type)' ...` and `jq '(.env // {}) | keys' ...`, never by reading it raw.
   - Ship `settings.json` from one kit only (see `${CLAUDE_PLUGIN_ROOT}/reference/den-yaml.md#profiles`).

5. Write `${DEN_HOME:-$HOME/.den}/kits/<name>/spec.yaml`, the smallest kit:

   ```yaml
   schemaVersion: "2"
   kind: mixin
   name: <name>
   version: 1.0.0
   ```

6. Declare the profile (rule 5).
   - Add `profiles.<name>: [<den home>/kits/<name>]` to `${DEN_HOME:-$HOME/.den}/den.yaml`, with the den home written as a path (`~/.den/kits/<name>` when `DEN_HOME` is unset).
   - Show the diff and get the user's agreement before writing.
   - Offer `profile: <name>` as the default profile of the file, and add it only if the user agrees. A nest of a project file ignores it: name the profile in that project file.

7. Tell the user which instances must be recreated. A kit is copied into a VM when it is created, so an existing VM keeps its old configuration.
   - Run `den ls` and name the instances that use the nests concerned.
   - To recreate one: `den rm <instance>`, then `den up <nest>`.
   - Run `den rm` only after the user confirms (rule 8).
