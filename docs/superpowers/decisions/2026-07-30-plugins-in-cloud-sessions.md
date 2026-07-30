# Why the greatkit plugin never loads in cloud sessions

An autonomous greatship run on claude.ai/code stopped before Phase 0: the
`greatkit:greatship` skill was not installed, and invoking it returned
`Unknown skill: greatkit:greatship`. This records what the investigation found,
what was changed, and what remains blocked.

Four hypotheses were raised and tested against primary sources. Two were wrong,
two hold. They are independent, and each is on its own sufficient to produce the
observed symptom — which is why fixing one does not clear the run.

---

## 1. The marketplace source was malformed — CONFIRMED, corrected, NOT sufficient

`.claude/settings.json` declared:

```json
"greatkit": {
  "source": { "source": "url", "url": "git@gitlab.digitaleo.com:groupe/greatplugin.git" }
}
```

`url` is not a marketplace source type. The
[settings reference](https://code.claude.com/docs/en/settings#extraknownmarketplaces)
enumerates exactly five:

> * `github`: GitHub repository (uses `repo`)
> * `git`: Any git URL (uses `url`)
> * `directory`: Local filesystem path (uses `path`, for development only)
> * `hostPattern`: regex pattern to match marketplace hosts (uses `hostPattern`)
> * `settings`: inline marketplace declared directly in settings.json without a
>   separate hosted repository (uses `name` and `plugins`)

`url` *is* valid one level down, as a **plugin** source inside a
`marketplace.json` entry — a different schema the docs call out explicitly
("Marketplace sources vs plugin sources: these are different concepts"). Using
it as a marketplace source is simply not a case the parser has. The local
`known_marketplaces.json` agrees: across 15 registered marketplaces only
`github` (9), `git` (5) and `directory` (1) ever appear. And the host was stale
besides — greatkit moved to `github.com/PillowPillow/greatkit`.

**Changed to** the documented shorthand, plus the env var that forces HTTPS
cloning instead of the SSH default:

```json
"greatkit": { "source": { "source": "github", "repo": "PillowPillow/greatkit" } }
```
```json
"env": { "CLAUDE_CODE_PLUGIN_PREFER_HTTPS": "1" }
```

The `github`/`repo` shape is confirmed in three independent places in the docs:
the `extraKnownMarketplaces` worked example, the `strictKnownMarketplaces`
source-type list ("Fields: `repo` (required)"), and the plugin-authoring
examples in `marketplace.json`. The schema is settled; what is not settled is
reachability.

The env var is defence in depth, not a certainty.
[plugin-marketplaces](https://code.claude.com/docs/en/plugin-marketplaces#private-repositories)
says "GitHub `owner/repo` shorthand sources clone over SSH by default; set
`CLAUDE_CODE_PLUGIN_PREFER_HTTPS=1` to clone them over HTTPS instead" — and a
cloud container has no SSH key for github.com, so the default would fail even
with a well-formed source. But that sentence is scoped to four interactive
commands (`/plugin marketplace add`, `/plugin install`, `/plugin update`,
`/plugin marketplace update`). The declare-and-auto-install-on-trust path this
repo uses is not in that list, and no page says whether the same default
applies to it. Presumably the same clone code runs whatever triggered it, but
that is inference. Settling it needs an empirical cloud run, not another doc
read.

This also sidesteps a trap the old value carried: the cloud-environments doc
notes plugin install "requires network access to reach the marketplace source",
and a self-hosted GitLab host is not on the default **Trusted** allowlist.
github.com is.

## 2. `extraKnownMarketplaces` is managed-settings-only — WRONG

Raised because the settings reference appears to annotate the key
"(Managed settings only)". It does not. Reading the raw `settings.md` rather
than a summary shows two separate sections: a master table listing genuinely
managed-only plugin keys (`blockedMarketplaces`, `strictKnownMarketplaces`,
`allowedChannelPlugins`, …), and a `### Plugin settings` section documenting
`enabledPlugins` and `extraKnownMarketplaces` as ordinary project-scope
settings. The doc's own comparison table is explicit: `strictKnownMarketplaces`
→ "managed-settings.json only"; `extraKnownMarketplaces` → "**Any settings
file**". The config is not inert by design.

## 3. The repo's `settings.json` is ignored in cloud — WRONG

Inferred from `CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH` reading `1` at runtime
while the file declares `2`. The inference does not hold. Per
[cloud-environments](https://code.claude.com/docs/en/cloud-environments), the
repo's hooks, `.claude/rules/`, `.mcp.json`, skills, agents and commands all
load in cloud ("Part of the clone"). Only two narrow `env` carve-outs are
documented as dropped — transport variables and hosting-identity variables —
and `MAX_SUBAGENT_SPAWN_DEPTH` is in neither. Issue #78119 independently
confirms hooks from the same `settings.json` fire while the marketplace half is
skipped, so the file is read and trusted.

The `1` is real but unexplained: it matches neither the set value (`2`) nor the
current documented default (`3`). Most likely a runtime cap or a pinned CLI
version in the container. It degrades skeptic nesting depth; it does not block
a run.

## 4. The cloud runtime disables marketplace resolution — CONFIRMED, unfixed

The binding constraint. Cloud containers force-set an undocumented
`SKIP_PLUGIN_MARKETPLACE=true` (env type `cloud_default`), which short-circuits
startup marketplace resolution *before any network or credential check*.
Observed consequences: `~/.claude/plugins/known_marketplaces.json` is never
created, and `claude plugin marketplace update <name>` reports
`Available marketplaces:` — empty.

This is why §1 alone will not clear the run: the marketplace list is empty
before the declared source is ever consulted.

Evidence — no Anthropic maintainer has responded anywhere in this chain:

| Issue | Surface | State |
|---|---|---|
| [#78119](https://github.com/anthropics/claude-code/issues/78119) | plain `claude --cloud`, **not** a routine | open, `bug, has repro, area:plugins` |
| [#68264](https://github.com/anthropics/claude-code/issues/68264) | cloud routine; names the flag | closed by stale-bot, no reply |
| [#63028](https://github.com/anthropics/claude-code/issues/63028) | first-session attach race | closed by stale-bot |
| [#18088](https://github.com/anthropics/claude-code/issues/18088) | SessionStart hook bootstrap hangs | open, stale |

#78119 matters most: it is a plain interactive cloud session, so the flag is not
routine-specific. The docs meanwhile assert the feature works — "Plugins
declared in `.claude/settings.json` | Yes | Installed at session start". Docs
and runtime disagree; there is no tracked gap and no ETA.

## 5. Installation needs consent nobody can give — DOCUMENTED, unresolved

Found last, and it may make §4 redundant. The settings reference describes what
`extraKnownMarketplaces` actually does:

> **When a repository includes `extraKnownMarketplaces`**:
> 1. Team members are **prompted** to install the marketplace when they trust the folder
> 2. Team members are then **prompted** to install plugins from that marketplace
> 3. Users can skip unwanted marketplaces or plugins (stored in user settings)
> 4. Installation respects trust boundaries and **requires explicit consent**

It is not a declaration that installs; it is a declaration that *asks*. A cloud
session is headless — there is nobody to accept, and no documented way to
pre-consent. On that reading #78119 is not a bug at all but the documented
behaviour meeting an environment the mechanism was never designed for, which
would also explain the maintainer silence.

That directly contradicts the cloud-environments table ("Installed at session
start from the marketplace you declared"). Both pages are current. The
contradiction is unresolved, and it is the single question worth putting to
Anthropic on #78119.

---

## What remains blocked

**`PillowPillow/greatkit` is private.** The cloud container is scoped to `den`:
"GitHub API and release-asset requests reach only repositories attached to the
session". Cross-owner adds are refused outright ("cross-tier adds are not
supported in v1", [#76248](https://github.com/anthropics/claude-code/issues/76248));
same-owner adds are reported unreliable. And `source: "git"` clones "with the
same authentication that `git clone` would use on that machine: configured
credential helpers or SSH keys" — and a cloud container has neither for a second
private repo. The docs are explicit that a bare token is not enough: "Setting a
provider token such as `GITHUB_TOKEN` in your environment doesn't by itself
enable background authentication. Tokens take effect only through a configured
credential helper."

The documented escape is a scoped git URL rewrite, which survives even the
background pull's credential-helper blackout:

```bash
git config --global url."https://x-access-token:TOKEN@github.com/PillowPillow/greatkit".insteadOf "https://github.com/PillowPillow/greatkit"
```

That needs a read-only PAT reaching the container, and the token sits in
plaintext in the gitconfig. Two cleaner exits: make greatkit public, or
distribute through **Organization settings > Plugins** on a Team or Enterprise
plan, where "organization sync reads the marketplace repository through the
Claude GitHub App or your organization's GitHub Enterprise App" — no git
credentials involved at all, and a private plugin source is supported when it
"shares the marketplace repository's owner", which is this case.

Either way this blocks every path that fetches greatkit at session time,
independent of §4 and §5.

## Paths

Vendoring is what this branch does, because it needs nothing outside the
repository. It is not the best answer. If the cloud environment can be edited,
**setup script + seed directory** (below) is stronger: it keeps greatkit a
plugin, with one source of truth and real versioning. Vendoring is the fallback
that works when nobody can touch the environment — and the safety net if the
setup-script path turns out not to survive the flag, which nobody has tested.

**Vendor into `den/.claude/`** — chosen, and what this branch implements. Not
the best path, but the only one that depends on nothing outside the repository:
no cloud environment configuration, no credentials, no unresolved Anthropic bug.
Documented directly: repo `.claude/skills/`, `.claude/agents/`,
`.claude/commands/` are "Part of the clone", and user-scope equivalents are
answered with "Commit them to the repo's `.claude/` directory instead."
Corroborated by #78505, whose reporter observed skills/agents/rules loading in
cloud even where `settings.json` did not. Costs the plugin-distribution and
versioning story, and requires rewriting the 34 `greatkit:*` agent references —
repo-local agents resolve unprefixed, so a straight copy yields "unknown agent
type" mid-workflow.

**Marketplace declaration alone** — documented, contradicted by #78119. Now
correctly configured, so it will work if and when the flag is fixed.

**Inline `source: "settings"`** — declares the marketplace catalogue directly in
`settings.json`, no hosted marketplace repo. Removes one fetch, but the plugins
it lists "must reference external sources such as GitHub or npm", so greatkit
still has to be cloned. Solves nothing here while greatkit is private, and
inherits §4 and §5 regardless.

**Setup script + seed directory** — the first-party mechanism, and on the
evidence the one to try first. A cloud environment's **Setup script** is "a Bash
script that runs when a new cloud session starts, **before Claude Code
launches**", running as root on Ubuntu 24.04; the same environment dialog sets
environment variables. Those are exactly the two halves the documented
container recipe needs:

```bash
CLAUDE_CODE_PLUGIN_CACHE_DIR=/opt/claude-seed claude plugin marketplace add PillowPillow/greatkit || true
CLAUDE_CODE_PLUGIN_CACHE_DIR=/opt/claude-seed claude plugin install greatkit@greatkit || true
```

then `CLAUDE_CODE_PLUGIN_SEED_DIR=/opt/claude-seed` as an environment variable.
At startup Claude Code "registers marketplaces found in the seed's
`known_marketplaces.json` into the primary configuration, and uses plugin caches
found under `cache/` **in place without re-cloning** … in both interactive mode
and non-interactive mode with the `-p` flag." No runtime clone means §4 has
nothing left to short-circuit and §5 has nothing left to consent to. Two
independent first-hand reports say an explicit install in the setup script works
under the flag.

An earlier draft of this document called this path dead on the grounds that
hosted cloud does not support custom base images. That was wrong: a seed needs
only a directory on disk and an env var, and a setup script provides the first
while the environment dialog provides the second. The setup script's filesystem
snapshot persists — "Packages you install, Docker images you pull, and files you
write all carry over."

Costs: the script must exit zero (`|| true` on every install), finish in ~5
minutes, and its snapshot rebuilds roughly every 7 days or whenever the script
or the allowed-hosts list changes — so the plugin pins to whenever it last ran.
It is environment-scoped rather than committed, so each environment needs it.
And it still has to reach greatkit, which is the private-repo problem above.

One path is genuinely closed: setting `SKIP_PLUGIN_MARKETPLACE=false` via the
environment editor. `cloud_default` resets it, and per
[#63541](https://github.com/anthropics/claude-code/issues/63541) panel variables
do not reach the setup script anyway.

## A second blocker, not yet hit

greatship shells out to `gh` at three sites in `SKILL.md` — issue fetch (:37),
resume-mode PR lookup (:46), and review-comment collection (:47). `gh` is
non-functional in this container: the remote is a local proxy and `GH_TOKEN` is
rejected by github.com. All GitHub work in the failed run went through the
GitHub MCP tools instead.

This is milder than it looks, but not free. Per the boundary contract
(`SKILL.md:10`) greatship never pushes and never opens the PR — the orchestrator
owns that — and :37 says "If you were handed PRD/issue markdown, use it." So an
orchestrator that passes the issue body inline, having read it over MCP, skips
the :37 call.

:46 is the one that still fires. It is where greatship *decides* which mode it
is in, and that decision runs before it knows the answer: "the caller says so,
**or** `gh pr list --head greatship/<issue-ref>` returns a PR". The `gh` probe is
the fallback, so it executes on every run unless the caller states the mode
explicitly. The orchestrator must therefore assert fresh-vs-resume, and on
resume also pass `reviewComments` inline — otherwise :47 fires too.
