---
name: troubleshoot
description: Use when something fails with den or in a den VM - an error message, a host unreachable from the VM, Permission denied (publickey), Not logged in, a stale instance - and the user wants the cause found and fixed.
---

Read ${CLAUDE_PLUGIN_ROOT}/rules.md and follow it.

Facts come from the troubleshooting tables of `${CLAUDE_PLUGIN_ROOT}/reference/setup.md#troubleshooting` and `${CLAUDE_PLUGIN_ROOT}/reference/ssh.md#troubleshooting`. Run the steps in order.

1. Get the symptom as the user saw it: the exact message, the command that produced it, and the instance name when there is one.

2. Match it. Read both tables and find the row that fits.
   - If no row of either page fits, say that the symptom is not in the troubleshooting pages and its cause is unknown. Do not invent a cause (rule 3). Offer to gather the output of the commands of step 3 and stop there.

3. Establish the cause with a command, not by guessing. Pick the one the row calls for:
   - `den version` and `sbx version`;
   - `den ls` and `sbx ls`;
   - `ssh-add -l`;
   - `sbx policy check network <host>:<port> --sandbox <instance>`.

   Say whether the output confirms the row.

4. Give the fix the page states for that row.
   - A fix that links to `../README.md#<anchor>` is given to the user as `https://github.com/PillowPillow/den#<anchor>`.
   - A token or login step goes to the user's own terminal (rule 6).

5. Apply it only when the user agrees, and only through the rules that govern the action:
   - a `den.yaml` change: minimal diff, agreement, then write (rule 5);
   - a host to add to `network.allow` or to `sbx policy`: only one the user named and has seen in the change (rule 9);
   - removing an instance: `den rm <instance>` after the user confirms, never with `--force` (rule 8);
   - `sbx rm <name>`: give it to the user for their own terminal, after they saved any work in that sandbox; never run it.

   A command that is interactive or touches a secret goes to the user's own terminal (rule 6).

6. Verify. Run again the command of step 3, or `den exec <nest> -- true`, and report the result. Give the user `den up <nest>` when the instance must be started (rule 7).
