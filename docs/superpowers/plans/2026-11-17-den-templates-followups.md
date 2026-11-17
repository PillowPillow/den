# den templates — follow-ups after the 2026-11-16 plan

Plan `2026-11-16-den-templates.md` shipped on `v2` (`a9efef7..b106847`); the conversion (Task 12) ran on
2026-11-16/17. The final whole-branch review triaged everything below as "can wait". This file keeps the list once
the plan's scratch workspace is gone. Each item names the place in the code it was found.

## Open decision

- A child template (`from: <sibling>`) inherits neither the sibling's kits nor its `network.allow` at `den up`
  (same as the old `bases:`; the spec says nothing). If a child is expected to get the sibling's egress, add the
  inheritance in `nest.Resolve`.

## Parked by the final review

- The child/sibling remedy can loop when a `den.yaml` declares the sibling name at another source. The remedy text
  could say "point the declaration at the child's source, or rename".
- A child built from a kept copy, with a repo sibling that has no record while the remote moved: the sibling is
  built and recorded, then the child refuses with `--pull`. It converges with `--pull`.
- README: "`--pull` re-clones both" overstates; an up-to-date sibling is skipped.
- Missing tests: repo child convergence, and the propagation of `Pull` after a fresh clone.
- An in-root kit link that points outside its kit dir gets no read-only overlay, and `hashCited` hashes inner
  links by target string only (pre-existing).

## Deferred concerns

- `DefinitionHash` does not follow `from:` into a sibling for notices: a `dir` sibling's drift gives no notice on a
  nest that uses the child.
- The copy's `.git` that `checkTemplate` reads is not overlaid. It matters only if a nest mounts `<denHome>/gen`
  read-write. The S6e guard test is gone; the Task 8c error suffix is the only remaining signal.
- A `from:` sibling's build files are not overlaid (pre-existing gap).

## Deferred minors

`internal/config` (definition.go)
- An empty kit/step/include entry (`""` or `"."`) resolves to the root and is accepted; the repo refuses rather
  than normalizes elsewhere.
- `den-template.yaml` itself may be a symlink out of the location (§5.2 lists only kits, includes, steps).
- A two-member `from:` cycle is reported twice.
- `resolveExisting` fails open (lexical path) on a symlink loop at the 40-hop cap.
- `resolveExisting` joins a relative dangling target to the lexical `Dir(cur)`, and `Join` cleans `..` lexically:
  a `..` through an in-root dir link plus a dangling link could be misjudged (no exploit built).
- Tests: the escape-table test does not assert the field/index named; no test of an in-root symlink (accepted) or
  of a symlink in includes/steps.

`internal/hub`
- `next` is followed to any host or scheme; no response-size cap (`io.LimitReader`).
- The deadline test uses the unexported `client{timeout: 50ms}`; the 5 s wiring of `NewWith` is unasserted.

`internal/template`
- `CompleteImage` accepts any value with a `/` (`/`, `a/`, spaces); sbx rejects it first, and the error then lacks
  the `den.yaml` names.
- A template name with a leading dot (`.staging-web`) collides with `StagingDir("web")`; `ValidateName` allows it.
- `Clone` has no `BatchMode` and no deadline: an ssh passphrase prompt can appear during `den build`.
- `Trust` does a read-modify-write without a lock. `DefinitionHash` ignores file mode (`chmod +x` on a step).
- `-c core.sshCommand` set by den overrides the user's git config.
- open.go:73 returns the `LoadDefinitions` error bare (no `den build` remedy); `short("")` renders `( -> x)`.
- Tests: setup errors ignored (copy_test.go:57,141,189; record_test.go:64-65; notice_test.go:84); record_test.go:48
  dereferences nil on a Stat failure; the real-git `RemoteHead` test puts the duplicate branch/tag and the decoy on
  one commit, so only the fake test pins the order; the inner-symlink `Readlink` branch is unasserted; `spyGit.RunWithInput`
  records no dir; open_test.go:97 asserts twice.

`internal/build`
- A step error after a fresh clone names the staging path, which is deleted afterwards. A repo→dir switch leaves
  an unused copy.
- build.go:229: an empty sibling digest gets the remedy `--force`, which does not rebuild (unreachable today).
- build.go:166: the copy swap does RemoveAll then Rename, so a failed Rename loses the old copy; a WriteRecord
  failure leaves the new copy beside the old record.
- `NoTemplateError{Nest: <template name>}` misleads; `DescribeSource` lacks a doc comment; sibling errors are not
  wrapped.
- sbx tags (`<name>:v2`) and records are shared by every den home on the machine and by every declaration of the
  same template name.
- Tests missing: a sibling in a repo location, a failing sibling build, a record present but its tag missing.

`internal/instance` / `internal/nest`
- prepare.go:272: a `Head` failure on a non-git copy has no remedy (wrap in `NotBuilt`).
- After a template rebuild, the warning says "<den.yaml> changed since …", which names the wrong file.
- `Resolved` with `TemplateErr` is only hash-safe; safety relies on `Prepare` being the sole caller.
- The `Up` hint fires on any attach error of a just-created image instance (a Claude non-zero exit or Ctrl-C gets
  a false "could not start the image").
- `Exec` that creates an instance and then fails `sbx exec` (S6e) gets no hint.
- Tests: `TestCreateWritesAKitPerInstance` never re-reads A's kit after B's create;
  `TestCreateRefusesAnAdminDirOutsideTheCommonGitDir` was weakened (`len==3` → no env create); no test for a
  `den-template.yaml` path crossing a symlink below a mount root; no test that `Create` writes `Template`/`Tag`;
  the exec path is not asserted to make zero `ls-remote` calls; the notice deadline test hangs instead of failing
  on a no-timeout regression; exec tests live in instance/prepare_test.go rather than cli.

Docs
- Long unwrapped lines in CLAUDE.md (Config, sbx env paragraphs) and the README Coexistence section.
- The README definition snippet (template `node`, network blocks) differs from the shipped example.

## Observed during the conversion

- `sbx template rm` needs `--force` without a tty.
- The env create of `den build pillow` downloaded three new layers for `claude-code-docker` while the recorded
  root digest stayed `sha256:4a94d914…`; whether sbx re-pulls a moved tag is still the plan's open fog-of-war item.
- When the nests moved from `den-env` to `dev-home`, the first `den up` from `dev-home` rewrote every entry of
  `state/v2/nests.yaml` to the new file; no manual edit was needed.
