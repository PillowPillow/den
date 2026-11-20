# Rules for every den skill

1. Write the den home as `${DEN_HOME:-$HOME/.den}` in every command; never hard-code `~/.den` in a command.
2. Check versions before acting:
   - Run `den version` (it prints `den vX.Y.Z`) and read `${CLAUDE_PLUGIN_ROOT}/.claude-plugin/plugin.json`.
   - If `den` is absent, send the user to `/den:setup` (except inside `den:setup`).
   - If `den --help` lists `doctor` or `init`, this is den v1: stop and say so ([setup troubleshooting](${CLAUDE_PLUGIN_ROOT}/reference/setup.md#troubleshooting)).
   - If the den and plugin major or minor versions differ, say so, give the command that updates the older one, and continue.
   - Update den with the link `https://github.com/PillowPillow/den#install`.
   - Update the plugin with `claude plugin marketplace update den`, then `claude plugin update den@den`.
3. Read the page you need under `${CLAUDE_PLUGIN_ROOT}/reference/` when you act; state no den fact that is absent from those pages.
4. Verify by a command what a command establishes: `sbx version`, `sbx policy ls`, the filtered `sbx secret ls --json` check of den:setup, `den ls`, `sbx ls`.
5. Edit `den.yaml` minimally: show the diff, get the user's agreement, then write; never rewrite the whole file.
6. Never read, print or write the token or any secret value; give an interactive command, or one that touches a secret, to the user for their own terminal, then verify the result. Never run `sbx secret ls` without `--json` and a `jq` filter that drops every `secret` field: its table prints part of each secret.
7. Never run `den up`: create and verify with `den exec <nest> -- true` in the background, then give the user `den up <nest>`.
8. Run `den rm` only after the user confirms, and never with `--force`.
9. Add a host to `network.allow` or to `sbx policy` only if the user named it and has seen the change.
10. If a project `den.yaml` is found from the current folder, show it (mounted repos, open hosts, template, kits) before any creation; see `${CLAUDE_PLUGIN_ROOT}/reference/security.md`.
11. Never touch `config.yaml`, `stacks/`, `nests/` or `state/sandboxes/` in the den home (v1).
12. Announce only skills that exist: `/den:setup`, `/den:profile` (create a profile), `/den:nest`, `/den:troubleshoot`.
