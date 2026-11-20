---
name: nest
description: Use when the user wants to add a nest or change one in a den.yaml - repos to mount (by path or origin), optional repos, allowed hosts, ports, template or profile - or asks why a nest does not see a repo.
---

Read ${CLAUDE_PLUGIN_ROOT}/rules.md and follow it.

Facts come from ${CLAUDE_PLUGIN_ROOT}/reference/den-yaml.md. Run the steps in order.

1. Read `${CLAUDE_PLUGIN_ROOT}/reference/den-yaml.md`.

2. Choose the file. Read `${CLAUDE_PLUGIN_ROOT}/reference/den-yaml.md#project-file` and decide with the user between the project file and `${DEN_HOME:-$HOME/.den}/den.yaml`. If a project `den.yaml` is found from the current folder, show it first (rule 10).

3. Collect the change, one item at a time, reading the section of the page that governs it:
   - repos, by path or by origin: `${CLAUDE_PLUGIN_ROOT}/reference/den-yaml.md#repos`;
   - optional repos: `${CLAUDE_PLUGIN_ROOT}/reference/den-yaml.md#optional-repos`;
   - `network.allow`: only a host the user named, after they have seen the change (rule 9);
   - `ports`;
   - `template`: `${CLAUDE_PLUGIN_ROOT}/reference/templates.md#choosing-a-template-for-a-nest`;
   - `profile`.

4. Write the change (rule 5). Show the diff of the smallest edit, keep the comments of the file, get the user's agreement, then write.

5. Check for an existing instance. Run `den ls`.
   - If the nest has an instance, read `${CLAUDE_PLUGIN_ROOT}/reference/den-yaml.md#changing-a-nest` and tell the user what it says: the change reaches the VM only when the instance is recreated.
   - Propose `den rm <instance>` then `den up <nest>`; run `den rm` only after the user confirms, never with `--force` (rule 8).

6. Validate. Run `den exec <nest> -- true` (in the background if it creates the VM). When it succeeds, give the user `den up <nest>` (rule 7).
