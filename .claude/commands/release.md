---
description: Cut a release — read the commits, write the changelog, tag, and stop before the push
argument-hint: "[major|minor|patch|vX.Y.Z]"
allowed-tools: Bash(git:*), Bash(make:*), Bash(gh:*), Read, Edit, Write
---

Cut a den release. `$ARGUMENTS` optionally forces the bump (`major`, `minor`, `patch`) or
names the exact tag (`vX.Y.Z`); with no argument you derive it from the commits.

One text is written once and lands in three places: the `CHANGELOG.md` section, the
annotated tag's body, and — through `release.header: {{ .TagBody }}` in `.goreleaser.yaml` —
the GitHub release body. Never write those three by hand from three drafts; write the
section, and derive the tag body from it.

**The push is the point of no return**: it triggers goreleaser, which publishes a public
release and bumps the cask in `PillowPillow/homebrew-tap`. Everything before it is local and
undoable. Stop at step 7 and wait.

## 1. Preflight — refuse, don't repair

Run these and stop with the failing one named if any is not satisfied:

```bash
git fetch origin --tags                              # the two checks below read stale refs without it
git rev-parse --abbrev-ref HEAD                      # must be main
git status --porcelain --untracked-files=no          # must be empty
git rev-list --left-right --count origin/main...HEAD # must be 0	0
git describe --tags --abbrev=0                       # the previous tag
```

`--untracked-files=no` is deliberate: this repo carries untracked directories (`claudedocs/`)
that have nothing to do with a release, and a plain `--porcelain` would refuse every run.

If `git status` shows tracked modifications, stop: a release commit must carry the changelog
and nothing else.

Then the gates, in this order, all three green:

```bash
make lint && make typecheck && make test
```

`release.yml` re-runs the same three on the tagged commit, so a red gate here is a release
that fails in CI after the tag is public — which is the one state that cannot be cleaned up.

One check runs late, once step 3 has named the version, but still **before** the commit at
step 5 — prove the tag is free on both sides:

```bash
git rev-parse -q --verify refs/tags/vX.Y.Z   # must be empty
git ls-remote --tags origin vX.Y.Z           # must be empty
```

A tag that already exists fails `git tag -a` at step 6, with the changelog commit already on
`main` — the awkward half-state this check exists to avoid.

## 2. Read the commits

```bash
git log --format='%H%n%s%n%b%n---' <previous-tag>..HEAD
```

The repo squash-merges PRs, so this is roughly one entry per PR, subject shaped
`type(scope): summary (#NN)`.

## 3. Decide the version

From the subjects, against the previous tag:

| Found | Bump |
|---|---|
| any `feat` | minor |
| only `fix` / `perf` | patch |
| only `docs`, `test`, `chore`, `build`, `refactor` | patch |
| a breaking change | **major — never inferred** |

An explicit `$ARGUMENTS` always wins over the table.

**Never bump major on your own.** `!` and `BREAKING CHANGE:` live in the commit body, and a
squash merge can drop the body entirely — so their absence proves nothing. A major release
requires the caller to pass `major` or `vX.Y.Z`. If you read a breaking change in the
commits and the argument does not ask for a major, say so and stop.

Announce the version and the evidence for it (which commit made it a minor) before writing
anything.

## 4. Write the CHANGELOG section

`CHANGELOG.md`, newest version on top, in **English** (repo convention: user-facing text is
English, only `docs/superpowers/` is French):

```markdown
## vX.Y.Z — YYYY-MM-DD

### Added
- `den ports --json` prints the published pairs as a machine-readable list.

### Fixed
- `den init`'s doctor hint only carries `--den-home` when it names a different target.
```

Use only the headings you need, in this order: `Added`, `Changed`, `Fixed`, `Removed`.
Date from the environment, not from memory.

What a line is:

- **One line per user-visible change**, not per commit. Three commits refining one feature
  are one line. A commit nobody can observe from outside — a test, a refactor, an internal
  comment — gets **no line at all**. `CHANGELOG.md` is not a reformatted `git log`; the log
  is already in the repo.
- **"User" means someone running den**, not someone contributing to it. Repo tooling — CI,
  agent configuration, this command, the release plumbing itself — gets no line, however
  large the diff. A release whose whole content is tooling gets a changelog section saying
  so in one sentence, with no headings.
- **Name what the reader touches**: the command, the flag, the config key, the file. `den
  init`, `--den-home`, `egress:`, `install.sh`. Not "improved the init flow".
- **Say the change, then what it prevents**, when the reason isn't obvious from the change.
  Match the density of the repo's own comments — they name the regression, not the diff.
- No PR numbers, no hashes, no "misc", no "various improvements". The reader has the
  release page for links.

If a commit's user-visible effect isn't clear from its subject and body, read the diff
before writing its line. Guessing produces a changelog that is wrong in the one place
people trust it.

## 5. Commit

```bash
git add CHANGELOG.md
git commit -m "release: vX.Y.Z"
```

The changelog commit goes straight on `main` — that is deliberate for this one commit: it is
mechanical, generated, and already read by the caller at step 7. Feature work still goes
through a branch and a PR.

## 6. Tag

Write the tag message to a file first — subject line, blank line, then **the section body
without its `## vX.Y.Z` heading** (the heading would repeat the release title on GitHub):

```
vX.Y.Z — <the release in one clause, lowercase, no trailing period>

### Added
- …
```

```bash
git tag -a --cleanup=verbatim vX.Y.Z -F <notes-file>
```

`--cleanup=verbatim` is **mandatory, not stylistic**: git's default cleanup strips every
line starting with `#`, which silently deletes every `###` heading from the tag body — and
the tag body *is* the release page. Verified on 2026-08-04: without it, `### Added`
disappears from the tag object with no warning.

Write the notes file under the session scratchpad, not in the repo.

## 7. Show, then stop

Print, so the caller reads what the world will read:

```bash
git show --stat HEAD                                    # the changelog commit
git for-each-ref --format='%(contents:body)' refs/tags/vX.Y.Z   # the future release body
```

State plainly that pushing publishes the release and bumps the Homebrew cask, then **wait
for an explicit go**. Do not push in the same turn.

If the caller declines, undo cleanly and say so:

```bash
git tag -d vX.Y.Z
git reset --hard HEAD~1        # only ever the release commit, which nothing else touches
```

## 8. On the go: push and verify

```bash
git push origin main vX.Y.Z
gh run watch "$(gh run list --workflow=release.yml -L1 --json databaseId -q '.[0].databaseId')"
gh release view vX.Y.Z --json body -q .body     # must equal the tag body from step 7
```

`gh run watch` with no run id prompts and stalls an unattended run, hence the explicit id.
The run takes a few seconds to appear after the push — if the list comes back empty, wait
and ask again rather than assuming the workflow did not fire.

The release body comes from `release.header: {{ .TagBody }}` — goreleaser renders it in the
release pipe (`internal/pipe/release/body.go`), independently of the changelog pipe, which
`.goreleaser.yaml` disables. That path only executes during a real publish, so step 7's
output is the expectation and this check is the comparison.

If the body comes back empty or wrong, the release is already out; repair it in place
rather than retagging — the notes file here is the tag body, without the subject line, or
the release page repeats its own title:

```bash
git for-each-ref --format='%(contents:body)' refs/tags/vX.Y.Z > <body-file>
gh release edit vX.Y.Z --notes-file <body-file>
```

An **empty** body is almost never `.goreleaser.yaml`'s fault: `actions/checkout` fetches the
annotated tag object and then re-fetches the commit SHA into the same ref
(`+<sha>:refs/tags/vX.Y.Z`), leaving a lightweight tag whose `%(contents:body)` is empty —
so `{{ .TagBody }}` renders nothing. Happened on v1.1.0 (2026-08-04): a 4-byte body, repaired
by hand. The remedy is one step after checkout in `release.yml`, `git fetch --force --tags`
(`--force` is required — otherwise the fetch will not clobber the ref checkout just wrote).
Check that step is present before suspecting the template. Report whichever it was, so the
next release does not repeat it.
