---
name: setup
description: Use when installing den for the first time, or when den, sbx or the Claude token is not set up yet (sbx missing or signed out, no network policy, no stored token, no nest), or when the user asks to get den working on this machine.
---

Read ${CLAUDE_PLUGIN_ROOT}/rules.md and follow it.

Facts come from ${CLAUDE_PLUGIN_ROOT}/reference/setup.md. Run the steps in order.

1. Check the prerequisites. Read the Prerequisites section of `${CLAUDE_PLUGIN_ROOT}/reference/setup.md#prerequisites` and run each "Check" command:
   - the platform;
   - `sbx version` (v0.53.0 or later);
   - `sbx ls`;
   - `sbx policy ls`, then `sbx policy check network example.com:443`, which must answer `Denied`;
   - `claude --version`;
   - `git --version`.

   For each missing item, give the user the "Install" command from that page, then run its check again.

   If no policy exists, propose `sbx policy init balanced`, say it applies to every VM on this machine, and run it only after the user agrees.

2. Check den. Run `command -v den`, then apply the version rule.
   - If den is absent, give the user `https://github.com/PillowPillow/den#install`, wait, then check again.

3. Check the token. Run `sbx secret ls --json | jq '{custom: [.custom_secrets[]? | select(any(.targets[]?; . == "api.anthropic.com")) | {scope, env, targets}], services: [.secrets[]? | {scope, name, type}]}'`.
   - The token is stored when `custom` is non-empty. If `custom` is empty, give the user `https://github.com/PillowPillow/den#log-claude-in`, wait, then check again with the same command.

4. Find the nest. Look for a `den.yaml` walking up from the current folder.
   - If you find one, show it (rule 10) and offer its nests.
   - Otherwise, ask the user which repos to mount, then create or edit `${DEN_HOME:-$HOME/.den}/den.yaml` (rule 5) with `schema: 1`, `default_template: claude-code-docker` when no template is declared, and the nest.

5. Create the nest. Run `den exec <nest> -- true` in the background (the first pull takes several minutes). When it succeeds, give the user `den up <nest>`.

6. Offer `/den:profile`.
