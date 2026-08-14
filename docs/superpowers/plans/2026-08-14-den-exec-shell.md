# `den exec` compose-shaped, `den shell` born — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `den exec [-T] [--workdir p] <sandbox> <cmd> [args...]` requires a command and needs no `--`, and the login shell moves to a new `den shell <sandbox>`.

**Architecture:** Entirely inside `internal/cli`. `newExecCmd`'s RunE body is extracted into one unexported `enterSandbox` that both commands call, so the two doors cannot drift — the failure mode `internal/cli/exec.go:72-77` warns about. `internal/spawn` is not touched: `Command.Argv` empty ⇒ `bash -l` stays in `internal/spawn/enter.go:85`, and its callers become `den shell` and `den spawn`. The separator disappears through cobra's `Flags().SetInterspersed(false)`, the same mechanism `docker compose` uses.

**Tech Stack:** Go, cobra (`github.com/spf13/cobra`), `sbx.Fake` for every test. Task runner is `task` (`Taskfile.yml`), never `make`.

**Spec:** `docs/superpowers/specs/2026-08-14-den-exec-shell-design.md`

## Global Constraints

- **`task check`** (lint » typecheck » test, fail-fast) is what CI runs and what must pass before every commit in this plan. `task test` alone is `go test -count=1 ./...`; a plain `go test` can pass stale.
- **`gofmt` is enforced, not advisory** — `task lint` fails on any unformatted file.
- **No test calls `t.Parallel()`, opens a socket, or spawns a process.** Every test here uses `sbx.Fake`.
- **No `-update` flag exists for goldens.** `internal/cli/testdata/unknown-command.golden` is edited by hand.
- **Code, comments and user-facing messages are English.** The spec and plans under `docs/superpowers/` are French.
- **Comment density must match the surrounding file.** `internal/cli/exec.go` carries long "why" comments at each decision site, naming what was rejected. Terse code visibly does not match.
- **Errors name the file to fix or the remedy.** den refuses rather than normalizing in silence (spec §2).
- **`--workdir` is spelled long on every command in this family.** `-w` is `den spawn`'s worktree.
- The branch is `spec/den-exec-shell`, already carrying the spec commit `f41bc84`.

---

### Task 1: `den shell`, and the shared `enterSandbox`

Creating `den shell` first means the login shell has a home before `den exec` loses it. The extraction is scaffolding this task's deliverable needs, so it lands here rather than as a task of its own.

**Files:**
- Modify: `internal/cli/exec.go:105-241` — extract `enterOptions` + `enterSandbox`, leave `newExecCmd` as a thin caller
- Create: `internal/cli/shell.go`
- Create: `internal/cli/shell_test.go`
- Modify: `internal/cli/root.go:141` — one `AddCommand`
- Modify: `internal/cli/testdata/unknown-command.golden` — add the `shell` row

**Interfaces:**
- Consumes: `spawn.Enter`, `spawn.Command`, `spawn.StartDir`, `spawn.CheckFreshnessOnReentry`, `sandboxNameOf`, `liveNames`, `warnEmptyAgentOnReentry` — all already in `internal/cli`, all unchanged.
- Produces, used by Task 2:
  ```go
  type enterOptions struct {
      denHome   *string
      runner    sbx.Runner
      sshAgent  func() sshagent.Result
      goos      string
      freshness agent.GateOptions
      isTTY     func() bool
  }

  func enterSandbox(cmd *cobra.Command, ref string, command []string, tty bool, workdir string, o enterOptions) error

  func newShellCmd(denHome *string, runner sbx.Runner, sshAgent func() sshagent.Result, goos string,
      freshness agent.GateOptions, isTTY func() bool) *cobra.Command
  ```
  `tty` is a parameter, not computed inside: `den exec` derives it from the probe and `-T`, `den shell` passes `true` unconditionally.

- [ ] **Step 1: Write the failing test**

**Do not delete anything from `exec_test.go` in this task.** `TestShellRefusesNoTTY` below is the
refusal `den exec` will lose in Task 2, written here first — but `den exec` still carries its own
copy until then, and `TestExecRefusesNoTTYWithNoCommand` must stay green through Task 1. Both tests
passing after this task is the correct state, not a duplication to clean up. Step 4's gate ("the
extraction must change nothing") depends on it.

Create `internal/cli/shell_test.go`:

```go
package cli

import (
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/doctor"
	"github.com/PillowPillow/den/internal/sbx"
)

// `den shell` is the login shell `den exec` opened until 2026-08-14, moved
// verbatim: `bash -l`, a tty unconditionally, and the workdir taken from the
// workspace the VM REPORTS. The `:ro` suffix in the fixture is not decorative
// — it separates b.Workdir() (which strips it) from b.Workspaces[0] (which
// would keep it); without it both implementations pass.
func TestShellAttachesALoginShellInTheWorkdir(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api:ro","/profile"]}]}`)},
	}}

	if _, err := executeCmdWithSbx(t, f, "shell", "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasAttached("exec", "-it", "-w", "/w/api", "api", "bash", "-l") {
		t.Errorf("attaches = %v", f.Attaches)
	}
}

// The refusal the spec moves here from `den exec`: it must stay identical, in
// the same words, to den spawn's (TestNoTTYReachesSpawnOptions,
// spawn_test.go). -T and --no-tty are one flag with two spellings, so both
// reach it — and on EITHER side of the sandbox name, because `den shell` does
// not set SetInterspersed(false) (see newShellCmd's comment). That is the whole
// observable difference from `den exec`, so it is pinned here.
func TestShellRefusesNoTTY(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
	}{
		{"-T before the name", []string{"shell", "-T", "api"}},
		{"-T after the name", []string{"shell", "api", "-T"}},
		{"--no-tty before the name", []string{"shell", "--no-tty", "api"}},
		{"--no-tty after the name", []string{"shell", "api", "--no-tty"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &sbx.Fake{}
			_, err := executeCmdWithSbx(t, f, tc.argv...)
			if err == nil {
				t.Fatal("-T on a login shell must be refused")
			}
			if !strings.Contains(err.Error(), "-T") {
				t.Errorf("the refusal must name the flag in play: %v", err)
			}
			if len(f.Calls) != 0 {
				t.Errorf("the refusal must land before anything is asked of sbx; calls = %v", f.Calls)
			}
		})
	}
}

// --workdir overrides the workspace the VM reported, on the shell exactly as on
// the command.
func TestShellWorkdirOverridesTheReportedWorkspace(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
	}}
	if _, err := executeCmdWithSbx(t, f, "shell", "--workdir", "/srv", "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasAttached("exec", "-it", "-w", "/srv", "api", "bash", "-l") {
		t.Errorf("attaches = %v", f.Attaches)
	}
}

// `-w` must NOT be accepted: it is den spawn's worktree, and one letter meaning
// two things across sibling commands is the collision den refuses elsewhere.
func TestShellRefusesTheShortWorkdirFlag(t *testing.T) {
	f := &sbx.Fake{}
	if _, err := executeCmdWithSbx(t, f, "shell", "-w", "/srv", "api"); err == nil {
		t.Error("-w must not be a workdir on den shell")
	}
}

// A shell takes exactly one sandbox name. A second positional is not a command
// here — `den exec` is where commands go — so it is refused rather than
// silently ignored.
func TestShellRefusesASecondPositional(t *testing.T) {
	f := &sbx.Fake{}
	if _, err := executeCmdWithSbx(t, f, "shell", "api", "bash"); err == nil {
		t.Error("den shell takes one sandbox name")
	}
}

// The §9.1 freshness gate governs `den shell` exactly as it governed the shell
// `den exec` used to open: the user is about to run that agent.
func TestShellRefusesASandboxWhoseFreshnessGateFailed(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
		"exec api cat /var/lib/den/kit-dispatch.json": {Output: []byte(`{"status":"failed"}`)},
	}}
	deps := Deps{Doctor: doctor.FakeDeps(), Sbx: f, Freshness: fakeGateOptions()}
	if _, _, err := executeCmdSeparateStreams(t, NewRootCmdWith(deps), "shell", "api"); err == nil {
		t.Fatal("a failed freshness gate must refuse the shell")
	}
}
```

The last test's `exec api cat …` key must match whatever `spawn.CheckFreshnessOnReentry` actually asks the runner. Copy the key verbatim from the existing `TestExecRefusesASandboxWhoseFreshnessGateFailed` (`internal/cli/exec_test.go:195`) rather than trusting the line above.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
go test ./internal/cli/ -run 'TestShell' -count=1 -v
```

Expected: FAIL — `undefined: newShellCmd` at build time, or `unknown command "shell"` once the file compiles.

- [ ] **Step 3: Extract `enterOptions` and `enterSandbox` in `internal/cli/exec.go`**

Move the whole body of `newExecCmd`'s RunE (currently `exec.go:115-229`) into a package-level function. `newExecCmd` keeps only the argv/tty decision and calls it. Nothing about the body's behavior changes in this step — the `sandboxNameOf` call, the `sbx.Ls`/`sbx.Find` lookup, `CheckAttachable`, the stdout/stderr chatter split, the stopped-sandbox line, `CheckFreshnessOnReentry`, `warnEmptyAgentOnReentry`, `os.Getwd`, `spawn.StartDir` and `spawn.Enter` all move together, in order.

```go
// enterOptions carries what every door into a live sandbox needs. It exists so
// `den exec` and `den shell` cannot drift: #60's own comment (below) says two
// spellings of one door is the failure mode, and after 2026-08-14 there really
// ARE two commands — so the shared body is what keeps that from being true of
// the behaviour as well as the name.
type enterOptions struct {
	denHome   *string
	runner    sbx.Runner
	sshAgent  func() sshagent.Result
	goos      string
	freshness agent.GateOptions
	isTTY     func() bool
}

// enterSandbox is the body `den exec` and `den shell` share: find the sandbox,
// check it is attachable, hold the §9.1 gate, warn about an empty ssh agent,
// then hand `spawn.Enter` a command and a workdir.
//
// tty is a PARAMETER rather than computed here, and that is the one thing the
// two callers genuinely disagree about: `den exec` lets the probe and -T decide,
// because `sbx exec -t` with no terminal behind it silently DISCARDS the
// command's output while still returning its status (measured 2026-08-10 on
// v0.38.0, spec §14.0). `den shell` passes true unconditionally — a login shell
// without a terminal is worth nothing, which is why it refuses -T instead.
//
// command EMPTY means the login shell, and the default is NOT applied here: it
// lives in spawn.Command.Argv (internal/spawn/enter.go:85), one layer down,
// where `den spawn` reads it too.
func enterSandbox(cmd *cobra.Command, ref string, command []string, tty bool,
	workdir string, o enterOptions) error {

	// The SANDBOX name is the flattened reference: ":" is not in sbx's
	// `--name` charset, so a nest loaded from a source never spawns under its
	// prefixed name (spawn.go) — the live VM this command must find is already
	// "corp-api", not "corp:api".
	name, err := sandboxNameOf(ref)
	if err != nil {
		return err
	}
	boxes, err := sbx.Ls(cmd.Context(), o.runner)
	if err != nil {
		return err
	}
	b := sbx.Find(boxes, name)
	if b == nil {
		names := liveNames(boxes)
		if len(names) == 0 {
			return fmt.Errorf("sandbox %q not found — no sandbox is running", name)
		}
		return fmt.Errorf("sandbox %q not found (live: %v)", name, names)
	}
	// Same guard as `den spawn`, through the same helper: both paths end in an
	// `sbx exec`. A STOPPED VM passes, `sbx exec` restarts it.
	if err := b.CheckAttachable(); err != nil {
		return err
	}
	// den's OWN lines go to stderr as soon as a caller might be piping stdout:
	// `den exec -T api go build | tee log` must carry the command's output and
	// nothing else. On the interactive path they stay on stdout, where `den sh`
	// always put them.
	chatter := cmd.ErrOrStderr()
	if tty {
		chatter = cmd.OutOrStdout()
	}
	stopped := b.IsStopped()
	if stopped {
		fmt.Fprintf(chatter,
			"sandbox %s is stopped: it restarts on attach (its state is kept)\n", b.Name)
	}
	// The §9.1 gate WAITS here even with no terminal. Under `--detach` den reads
	// once because nobody is at a prompt; `den exec <sb> <cmd>` fits neither
	// case — nobody is at a prompt AND the command being run is very often the
	// agent itself. The gate's reason ("you are about to run that agent") is
	// more true here, not less.
	if err := spawn.CheckFreshnessOnReentry(
		cmd.Context(), o.runner, chatter, b.Name, stopped, o.freshness); err != nil {
		return err
	}
	// Before the attach, never after: once the shell has the terminal, a
	// warning printed behind it is a line the user scrolls past on the way out.
	warnEmptyAgentOnReentry(cmd, o.denHome, o.sshAgent, o.goos)
	// The workdir comes from the workspaces REPORTED BY THE VM, never from a
	// path recomputed from the config. Which of them, and how --workdir and the
	// cwd rank, is spawn.StartDir's verdict — `den spawn` calls the same judge,
	// so the sibling commands cannot open a shell in two different places (#69).
	//
	// The cwd is read HERE, at the caller's edge: the judge stays pure, and an
	// unreadable working directory is not an error.
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "" // StartDir skips its cwd rule on "" and falls back.
	}
	return spawn.Enter(cmd.Context(), o.runner, b.Name, spawn.Command{
		Argv:    command,
		Workdir: spawn.StartDir(workdir, cwd, b.Workspaces),
		TTY:     tty,
	})
}
```

`newExecCmd`'s RunE becomes, with its existing comments on the tty decision kept in place:

```go
RunE: func(cmd *cobra.Command, args []string) error {
	var command []string
	if dash := cmd.ArgsLenAtDash(); dash >= 0 {
		command = args[dash:]
	}
	tty := len(command) == 0 || (!noTTY && isTTY != nil && isTTY())
	if noTTY && len(command) == 0 {
		return fmt.Errorf(
			"-T asks for no terminal and no command asks for a shell, which needs one — " +
				"give a command after `--`, or drop -T")
	}
	return enterSandbox(cmd, args[0], command, tty, workdir, enterOptions{
		denHome: denHome, runner: runner, sshAgent: sshAgent,
		goos: goos, freshness: freshness, isTTY: isTTY,
	})
},
```

- [ ] **Step 4: Run the whole suite — the extraction must change nothing**

```bash
task test
```

Expected: PASS, every existing test, unchanged. Only the `TestShell*` tests fail (still no `shell` command). If any pre-existing test fails here, the extraction moved something; do not proceed.

- [ ] **Step 5: Create `internal/cli/shell.go`**

```go
package cli

import (
	"fmt"

	"github.com/PillowPillow/den/internal/agent"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/sshagent"
	"github.com/spf13/cobra"
)

// newShellCmd opens a login shell in an already live sandbox.
//
// It is `den exec <name>` with no command, as that form behaved until
// 2026-08-14 — and before that, `den sh`. The spec
// 2026-08-14-den-exec-shell-design.md says why it became a command of its own:
// `docker compose exec` REQUIRES a command, and den claimed to be modelled on
// it while keeping a shell as the default argument. Requiring the command on
// `den exec` leaves the login shell with nowhere to live; this is where it
// lives.
//
// It is NOT a second door. The body is enterSandbox, shared with `den exec`
// byte for byte, and `bash -l` itself is not spelled here at all — an empty
// spawn.Command.Argv means it (internal/spawn/enter.go:85), one layer down,
// where `den spawn` reads the same default.
//
// -T is REGISTERED and always refused, rather than left unknown. A named
// refusal beats cobra's `unknown flag: -T`, which is the argument #60 already
// made for this flag; do not "simplify" it away. Its message is identical, in
// the same words, to `den spawn`'s (internal/spawn/spawn.go:249) — one
// contradiction must not read as two rules (spec §2).
//
// The parameters are `den exec`'s, and pointers/injection follow the same
// reasons: denHome is a POINTER because --den-home is a persistent flag whose
// value only exists once cobra has parsed it; goos, freshness and isTTY are
// threaded from the wiring site so the suite never reads the real machine.
func newShellCmd(denHome *string, runner sbx.Runner, sshAgent func() sshagent.Result, goos string,
	freshness agent.GateOptions, isTTY func() bool) *cobra.Command {

	var workdir string
	var noTTY bool

	cmd := &cobra.Command{
		Use:   "shell <name>",
		Short: "Open a login shell in an existing sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if noTTY {
				return fmt.Errorf(
					"-T asks for no terminal and no command asks for a shell, which needs one — " +
						"give a command after `--`, or drop -T")
			}
			// Argv nil ⇒ `bash -l`. The tty is unconditional: the refusal above
			// is the only way to reach this line without one.
			return enterSandbox(cmd, args[0], nil, true, workdir, enterOptions{
				denHome: denHome, runner: runner, sshAgent: sshAgent,
				goos: goos, freshness: freshness, isTTY: isTTY,
			})
		},
	}

	cmd.Flags().StringVar(&workdir, "workdir", "",
		"working directory for the shell (default: the directory you ran den from, when the sandbox mounts it; otherwise the first workspace it reports)")
	cmd.Flags().BoolVarP(&noTTY, "no-tty", "T", false,
		"refused here — a login shell needs a terminal; use `den exec` for a command")
	// NO SetInterspersed(false) here, unlike `den exec`, and that is a decision
	// rather than an omission: `den shell` takes no command, so it has no second
	// side for a flag to belong to, and interspersing costs nothing. It buys one
	// thing — `den shell api -T` reaches the -T refusal, which names the flag and
	// the remedy. Under SetInterspersed(false) that same line would be refused by
	// ExactArgs as "accepts 1 arg(s), received 2", which names neither.
	return cmd
}
```

The `-T` message is copied byte for byte from `internal/spawn/spawn.go:249`. Do not reword it, including the trailing "give a command after `--`, or drop -T" — Task 2 revisits that wording on **both** sites at once, or on neither.

- [ ] **Step 6: Wire it in `internal/cli/root.go`**

Immediately after the `newExecCmd` line (`root.go:141`):

```go
// `den shell` gets exactly what `den exec` gets, and for the same reasons: it
// is the same door with `bash -l` as its argument. Wiring it from the same
// fields is what makes "one door, two spellings" false — enterSandbox is
// shared, and so is everything injected into it.
root.AddCommand(newShellCmd(&denHome, deps.Sbx, deps.SSHAgent, runtime.GOOS, deps.Freshness, deps.IsTTY))
```

- [ ] **Step 7: Run the shell tests**

```bash
go test ./internal/cli/ -run 'TestShell' -count=1 -v
```

Expected: PASS, all six.

- [ ] **Step 8: Fix the golden by hand**

`task test` now fails on `internal/cli/testdata/unknown-command.golden` — the listing comes from `root.Commands()`, so `shell` appeared in it. Read the diff the test prints, then add this row to the file, in the alphabetical position cobra prints it (between `rm` and `source`), matching the existing two-space indent and column alignment:

```
  shell       Open a login shell in an existing sandbox
```

There is no `-update` flag. Edit the file.

- [ ] **Step 9: Run the full check**

```bash
task check
```

Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add internal/cli/shell.go internal/cli/shell_test.go internal/cli/exec.go \
        internal/cli/root.go internal/cli/testdata/unknown-command.golden
git commit -m "feat: den shell opens the login shell, on a body shared with den exec

Extracts newExecCmd's RunE into enterSandbox so the two commands cannot
drift. No behaviour change to den exec in this commit."
```

---

### Task 2: `den exec` requires a command, and drops `--`

**Files:**
- Modify: `internal/cli/exec.go` — `execArgs` replaced, `SetInterspersed(false)`, the `-T`-without-command refusal removed, `Use` and `Short` updated
- Modify: `internal/cli/exec_test.go` — 3 tests invert, 1 is deleted, 2 move out to `shell_test.go`, ~9 rewrite their argv, 3 are new
- Modify: `internal/cli/shell_test.go` — receives the two shell tests moving out of `exec_test.go`
- Modify: `internal/cli/testdata/unknown-command.golden` — `exec`'s `Short` loses "or open a shell"

**Interfaces:**
- Consumes: `enterSandbox`, `enterOptions` from Task 1 — signatures exactly as printed there.
- Produces: nothing later tasks call. Task 3 is documentation only.

- [ ] **Step 1: Write the failing tests**

In `internal/cli/exec_test.go`, **replace** these four tests wholesale (delete the old body and its comment, write the new one in its place):

```go
// A command needs no separator. `den exec api go test` had two readings in the
// old comment here — a sandbox `api` running `go test`, or three sandbox names
// — but the second was never reachable: execArgs refused anything but exactly
// one name before `--`. The real ambiguity was FLAGS, and
// Flags().SetInterspersed(false) closes it the way docker compose does.
func TestExecRunsACommandWithoutTheDoubleDash(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
	}}
	deps := Deps{Doctor: doctor.FakeDeps(), Sbx: f, Freshness: fakeGateOptions(), IsTTY: func() bool { return false }}

	if _, _, err := executeCmdSeparateStreams(t, NewRootCmdWith(deps),
		"exec", "api", "go", "test", "./..."); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasPiped("exec", "-w", "/w/api", "api", "go", "test", "./...") {
		t.Errorf("pipes = %v", f.Pipes)
	}
	if len(f.Attaches) != 0 {
		t.Errorf("a command must not open a shell; attaches = %v", f.Attaches)
	}
}

// Zero positionals after the sandbox name has no reading left: the login shell
// moved to `den shell` on 2026-08-14. The message is the only place a user
// learns that, short of mistyping a command name, so the test pins it.
func TestExecRefusesASandboxWithNoCommand(t *testing.T) {
	f := &sbx.Fake{}
	_, err := executeCmdWithSbx(t, f, "exec", "api")
	if err == nil {
		t.Fatal("den exec with no command must be refused")
	}
	if !strings.Contains(err.Error(), "den shell api") {
		t.Errorf("the refusal must name `den shell api`; got %q", err.Error())
	}
	if len(f.Calls) != 0 {
		t.Errorf("the refusal must land before anything is asked of sbx; calls = %v", f.Calls)
	}
}

// With no sandbox name at all there is nothing to name in a remedy, so the
// refusal names the usage line instead.
func TestExecRefusesWithNoArgumentAtAll(t *testing.T) {
	f := &sbx.Fake{}
	_, err := executeCmdWithSbx(t, f, "exec")
	if err == nil {
		t.Fatal("den exec with no argument must be refused")
	}
	if len(f.Calls) != 0 {
		t.Errorf("the refusal must land before anything is asked of sbx; calls = %v", f.Calls)
	}
}

// den's own flags belong LEFT of the sandbox name — SetInterspersed(false)
// stops parsing at the first positional, so `-T` after it would reach the VM
// as a program named `-T` and fail with `bash: -T: command not found`, an error
// that names nothing the user can fix. `--` is in the same closed set: cobra no
// longer consumes it (measured 2026-08-14), so it too would reach the VM.
//
// The set is CLOSED on purpose — `-T`, `--no-tty`, `--workdir`, `--workdir=…`,
// `--` — so the refusal cannot swallow a legitimate command. `--help` is NOT in
// it: it passes through to the sandbox, like compose (TestExecPassesHelpToTheSandbox).
func TestExecRefusesItsOwnFlagsAfterTheSandboxName(t *testing.T) {
	for _, tc := range []struct{ name, arg, want string }{
		{"-T", "-T", "before the sandbox name"},
		{"--no-tty", "--no-tty", "before the sandbox name"},
		{"--workdir", "--workdir", "before the sandbox name"},
		{"--workdir=", "--workdir=/srv", "before the sandbox name"},
		{"double dash", "--", "is not needed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &sbx.Fake{}
			_, err := executeCmdWithSbx(t, f, "exec", "api", tc.arg, "go", "build")
			if err == nil {
				t.Fatalf("%q in first command position must be refused", tc.arg)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal must say %q; got %q", tc.want, err.Error())
			}
			if len(f.Calls) != 0 {
				t.Errorf("the refusal must land before anything is asked of sbx; calls = %v", f.Calls)
			}
		})
	}
}
```

**Delete** `TestExecRefusesNoTTYWithNoCommand` (`exec_test.go:753`) entirely — Task 1 already moved it to `shell_test.go` as `TestShellRefusesNoTTY`.

**Add** these three, which pin measurements that are easy to break without noticing:

```go
// The command's OWN flags pass through untouched. This is what
// SetInterspersed(false) buys, and it is the reason `--` could be dropped at
// all — measured on cobra in den's real command tree, 2026-08-14.
func TestExecPassesTheCommandsOwnFlagsThrough(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
	}}
	deps := Deps{Doctor: doctor.FakeDeps(), Sbx: f, Freshness: fakeGateOptions(), IsTTY: func() bool { return false }}

	if _, _, err := executeCmdSeparateStreams(t, NewRootCmdWith(deps),
		"exec", "api", "go", "test", "-v", "-run", "TestX"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasPiped("exec", "-w", "/w/api", "api", "go", "test", "-v", "-run", "TestX") {
		t.Errorf("pipes = %v", f.Pipes)
	}
}

// `den exec api --help` runs `--help` IN the sandbox: cobra does NOT intercept
// it past the first positional under SetInterspersed(false) (measured
// 2026-08-14), and compose behaves the same. The easiest behaviour here to
// lose by accident, hence a test of its own.
func TestExecPassesHelpToTheSandbox(t *testing.T) {
	for _, flag := range []string{"--help", "-h"} {
		t.Run(flag, func(t *testing.T) {
			f := &sbx.Fake{Responses: map[string]sbx.Response{
				"ls --json": {Output: []byte(
					`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
			}}
			deps := Deps{Doctor: doctor.FakeDeps(), Sbx: f, Freshness: fakeGateOptions(), IsTTY: func() bool { return false }}

			if _, _, err := executeCmdSeparateStreams(t, NewRootCmdWith(deps),
				"exec", "api", "mytool", flag); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !f.HasPiped("exec", "-w", "/w/api", "api", "mytool", flag) {
				t.Errorf("pipes = %v", f.Pipes)
			}
		})
	}
}

// SetInterspersed(false) is set on the command's own FlagSet, which cobra
// merges the root's persistent flags INTO before parsing. The merge must not
// re-arm interspersing: --den-home has to keep parsing from the left.
//
// The assertion is POSITIVE — that the sbx lookup was reached — and not merely
// "the error is not about an unknown flag". A negative assertion here passes
// whatever happens: the fake answers nothing, so the command fails on the
// lookup either way, and a re-armed FlagSet would go unnoticed.
func TestExecStillReadsDenHomeBeforeTheSubcommand(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
	}}
	deps := Deps{Doctor: doctor.FakeDeps(), Sbx: f, Freshness: fakeGateOptions(), IsTTY: func() bool { return false }}

	if _, _, err := executeCmdSeparateStreams(t, NewRootCmdWith(deps),
		"--den-home", t.TempDir(), "exec", "api", "true"); err != nil {
		t.Fatalf("--den-home must still parse before the subcommand: %v", err)
	}
	if !f.HasPiped("exec", "-w", "/w/api", "api", "true") {
		t.Errorf("the command must have run, which proves parsing got past --den-home; pipes = %v", f.Pipes)
	}
}
```

Finally, **rewrite the argv** of every remaining test that spells `"--"`: drop the `"--"` element, and move `"-T"` / `"--workdir"` to the left of `"api"`. Find them with:

```bash
grep -n '"--"' internal/cli/exec_test.go
```

At the time of writing that is `TestExecAllocatesATtyOnlyWhenDenHasOne`, `TestExecMinusTSuppressesTheTtyEvenOnATerminal` (`"exec", "-T", "api", "true"`), `TestExecWorkdirOverridesTheReportedWorkspace` (`"exec", "--workdir", "/srv", "api", "true"`), `TestExecRefusesTheShortWorkdirFlag`, `TestExecPutsItsOwnChatterOnStderrWithoutATty`, `TestExecKeepsItsChatterOnStdoutWithATty`, `TestExecPropagatesTheCommandStatus`, `TestExecStartsInTheDirectoryTheUserTypedFrom`, `TestExecWorkdirOverridesTheCwd`. Trust the grep over this list.

Tests that call `"exec", "api"` with **no** command fall into two groups. Sort each one by asking
what its assertion is about, not what its argv is.

**Group A — about `enterSandbox`, keep in `exec_test.go`:** `TestExecAttachesInTheWorkdir`,
`TestExecNeverUsesSbxRun`, `TestExecResumesAStoppedSandbox`, `TestExecRefusesASandboxThatIsNotRunning`,
`TestExecRefusesASandboxWhoseFreshnessGateFailed`, `TestExecWaitsForTheGateWhenItStartsAStoppedSandbox`,
`TestExecPollsRatherThanReadsOnceWhenItStartsAStoppedSandbox`,
`TestExecAttachesAndStaysSilentWhenTheFreshnessGatePassed`, `TestExecReadsTheFreshnessGateBeforeAttaching`,
`TestExecWarnsWhenTheForwardedAgentIsEmpty`, `TestExecDoesNotWarnWhenTheForwardedAgentHasKeys`,
`TestExecDoesNotProbeTheAgentOutsideAgentForward`, `TestExecDoesNotWarnWhenTheSSHSocketIsAbsent`,
`TestExecWithNoSandboxAtAll`, `TestExecUnknownName`, `TestExecAcceptsASourceReference`,
`TestExecAcceptsAWorktreedSourceReference`. They exercise the lookup, the gate and the warnings —
`den exec` must keep its own coverage of those. Append `"true"` to the argv and change any
`bash`/`-it` assertion to that command. Where the assertion needs the Pipe path, add
`IsTTY: func() bool { return false }` and `Freshness: fakeGateOptions()` to `Deps` and switch to
`executeCmdSeparateStreams(t, NewRootCmdWith(deps), …)`, as the sibling tests already do.

**Group B — about the SHELL, move to `shell_test.go` and rename:** two tests, and only these two.

- `TestExecAttachesWithATtyNotARun` → `TestShellAttachesWithATtyNotARun`. Its own comment says the
  point is that `f.Attaches` tells a real attach from a `Run`, locking `-it` and the full argv on
  the shell path. Bolting `true` onto it turns it into a Pipe test and makes its comment false.
  Move it whole, comment included, with `"shell", "api"` as the argv.
- `TestExecOpensTheShellWhenTheDenHomeCannotBeRead` → `TestShellOpensWhenTheDenHomeCannotBeRead`.
  The name and the assertion are both about the shell: a broken `~/.den` must never stand between
  the user and a live sandbox.

`TestShellAttachesALoginShellInTheWorkdir` from Task 1 overlaps the first of these. Keep both — one
locks the workdir, the other locks Attach-not-Run, which is the distinction `sbx.Fake.Calls`
conflates.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
go test ./internal/cli/ -run 'TestExec' -count=1
```

Expected: FAIL — the old `execArgs` refuses a command without `--`, so `TestExecRunsACommandWithoutTheDoubleDash` and the pass-through tests fail on "a command must be separated by `--`".

- [ ] **Step 3: Replace `execArgs` in `internal/cli/exec.go`**

Delete the current `execArgs` (`exec.go:16-47`), comment included, and write:

```go
// execFlagNames is the CLOSED set of tokens den refuses in first-command
// position. Closed rather than "anything starting with -", because a command's
// own flags MUST pass through: `den exec api go test -v` is the whole point of
// SetInterspersed(false), and a prefix rule would eat it.
//
// `--help` and `-h` are deliberately absent. Cobra does not intercept them past
// the first positional (measured 2026-08-14 in den's real command tree), so
// `den exec api mytool --help` asks mytool for its help, inside the sandbox —
// which is what `docker compose exec` does too.
var execFlagNames = []string{"-T", "--no-tty", "--workdir"}

// execArgs accepts a sandbox name followed by a command. The command needs no
// `--`, and that separator is now refused rather than tolerated.
//
// This reverses the 2026-08-10 contract, on purpose, and the old comment here
// argued for it with a reading that never existed: "`den exec api go test` has
// two readings — a sandbox `api` running `go test`, or three sandbox names".
// The previous execArgs refused anything but exactly one name before `--`
// (`dash != 1`), so three names was never reachable. The genuine ambiguity was
// which side owned a FLAG, and Flags().SetInterspersed(false) — set in
// newExecCmd, the same mechanism docker compose uses — closes it: cobra stops
// parsing flags at the first positional.
//
// Consequence, and it is the real break of 2026-08-14: den's own flags must sit
// LEFT of the sandbox name. `den exec -T api go build`, not `den exec api -T`.
// Refusing the wrong order by name is cheaper than letting `-T` reach the VM,
// where it fails as `bash: -T: command not found` — an error that names nothing
// the user can act on.
//
// cmd.ArgsLenAtDash() is not consulted anywhere any more: under
// SetInterspersed(false) it returns -1 in every case, and `--` arrives as an
// ordinary argument (measured 2026-08-14). That is what makes the refusal below
// an exact string comparison rather than a heuristic.
func execArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s: a sandbox name and a command expected, none received — usage: %s",
			cmd.CommandPath(), cmd.UseLine())
	}
	if len(args) == 1 {
		return fmt.Errorf(
			"%s: no command given — write `%s %s go test`, or `den shell %s` for a shell",
			cmd.CommandPath(), cmd.CommandPath(), args[0], args[0])
	}
	if args[1] == "--" {
		return fmt.Errorf("%s: `--` is not needed — write `%s %s %s`",
			cmd.CommandPath(), cmd.CommandPath(), args[0], strings.Join(args[2:], " "))
	}
	name, _, _ := strings.Cut(args[1], "=")
	if slices.Contains(execFlagNames, name) {
		return fmt.Errorf(
			"%s: den's flags go before the sandbox name — write `%s %s %s %s`",
			cmd.CommandPath(), cmd.CommandPath(), args[1], args[0], strings.Join(args[2:], " "))
	}
	return nil
}
```

Add `"slices"` to the import block. Keep `spawnArgs` (`exec.go:52-59`) exactly as it is — `den spawn` still uses `--`.

- [ ] **Step 4: Update `newExecCmd` in the same file**

Three edits inside the constructor:

```go
cmd := &cobra.Command{
	Use:   "exec <name> <cmd> [args...]",
	Short: "Run a command in an existing sandbox",
	Args:  execArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		// execArgs has refused every other shape, so args[1:] is a real
		// command. No ArgsLenAtDash: under SetInterspersed(false) it is
		// always -1 and `--` never reaches here (execArgs refuses it).
		command := args[1:]
		// The probe DECIDES, and that is not a preference: `sbx exec -t` with
		// no terminal behind it silently DISCARDS the command's output while
		// still returning its status (measured 2026-08-10 on v0.38.0, spec
		// §14.0). Passing -it in CI would lose every byte the command wrote and
		// report success. A nil probe is "no terminal", never "assume one".
		tty := !noTTY && isTTY != nil && isTTY()
		return enterSandbox(cmd, args[0], command, tty, workdir, enterOptions{
			denHome: denHome, runner: runner, sshAgent: sshAgent,
			goos: goos, freshness: freshness, isTTY: isTTY,
		})
	},
}
```

The `-T`-with-no-command refusal is **gone from this file** — with a command now mandatory, `-T` contradicts nothing here. Replace the deleted block with a pointer, so a reader of the 2026-08-10 spec finds where the invariant went:

```go
// The "-T asks for no terminal and no command asks for a shell" refusal used to
// live here. It moved to `den shell` on 2026-08-14, unchanged: `den exec` now
// requires a command, so -T contradicts nothing on this command. The spec
// 2026-08-10 pinned that message as identical byte for byte between two
// commands; the pair is now `den shell` ↔ `den spawn`, not `den exec` ↔
// `den spawn`. Change one and you must change the other (spec §2: one
// contradiction, one rule).
```

And after the flag registrations, the line that makes the whole contract work:

```go
// The mechanism that removes `--`, and the same one docker compose uses: cobra
// stops parsing flags at the first positional, so everything after the sandbox
// name is the command, verbatim, its own flags included. Without this,
// `den exec api go test -v` fails on "unknown shorthand flag: 'v'".
cmd.Flags().SetInterspersed(false)
```

Update the `--no-tty` registration comment: it currently ends "`-w` is NOT taken here: it is `den spawn`'s worktree" — keep that, it is still true.

- [ ] **Step 5: Run the exec tests**

```bash
go test ./internal/cli/ -run 'TestExec' -count=1 -v
```

Expected: PASS.

- [ ] **Step 6: Fix the golden by hand**

`exec`'s `Short` changed, so `internal/cli/testdata/unknown-command.golden` diverges again. Edit the `exec` row to:

```
  exec        Run a command in an existing sandbox
```

- [ ] **Step 7: Run the full check**

```bash
task check
```

Expected: PASS. If `internal/spawn` fails, something outside `internal/cli` was touched — revert that part.

- [ ] **Step 8: Commit**

```bash
git add internal/cli/exec.go internal/cli/exec_test.go internal/cli/testdata/unknown-command.golden
git commit -m "feat!: den exec requires a command and no longer needs \`--\`

Flags().SetInterspersed(false), the way docker compose does it. den's own
flags now go left of the sandbox name; the login shell is \`den shell\`."
```

---

### Task 3: README and CHANGELOG

**Files:**
- Modify: `README.md:83` (command table), `README.md:143-148` (options of `den exec`)
- Modify: `CHANGELOG.md` — new section at the top

**Interfaces:**
- Consumes: the final `Use`/`Short` strings from Tasks 1 and 2.
- Produces: nothing.

- [ ] **Step 1: Update the command table in `README.md`**

Replace line 83:

```markdown
| `den exec <name> <cmd> [args...]` | runs one command in an existing sandbox and exits with that command's own status |
| `den shell <name>` | opens a login shell in an existing sandbox |
```

Keep the rows in the order the table already uses (`exec` sits between `ls` and `ports`; put `shell` immediately after `exec`).

- [ ] **Step 2: Update the options block**

Replace the `Options of `den exec`:` block (README.md:143-148) with:

```markdown
Options of `den exec` and `den shell`:

| Option | Effect |
|---|---|
| `-T` | never allocate a terminal — for pipes and CI. On `den shell` it is refused: a login shell needs one |
| `--workdir <path>` | working directory (default: the directory you ran `den` from, when the sandbox mounts it; otherwise the first workspace it reports) |

den's own flags go **before** the sandbox name; everything after it is the command, verbatim, its
own flags included — `den exec -T api go test -v` passes `-v` to `go test`. This is
`docker compose exec`'s rule, and it is why no `--` is needed. `den exec api --help` asks the
program in the sandbox for its help, not den.
```

- [ ] **Step 3: Check no other README line contradicts the new contract**

```bash
grep -n 'den exec' README.md
```

Every hit spelling `den exec <name> -- <cmd>` must lose the `--`. Note line 211 (`den spawn` and `den exec` pick it back up) and line 216 (`like den exec and den rm`) are about sandbox names, not argv — leave them.

- [ ] **Step 4: Add the CHANGELOG section**

`CHANGELOG.md` has no `[Unreleased]` heading; each section is a released version, newest first, and `/release` writes the heading. Insert directly above `## v1.6.0 — 2026-08-11`:

```markdown
## Unreleased

### Changed
- `den exec <name> <cmd> [args...]` now **requires** a command and no longer takes `--`. It is
  `docker compose exec`'s contract: den's own flags go before the sandbox name, and everything
  after it is the command, verbatim, its own flags included — `den exec -T api go test -v` passes
  `-v` to `go test`. `den exec api --help` asks the program in the sandbox, not den.
- Breaking: `den exec api -T -- go build` no longer works. Write `den exec -T api go build`.

### Added
- `den shell <name>` opens the login shell `den exec` opened until now, unchanged: `bash -l`, a
  terminal, and the working directory the sandbox reports. `-T` is refused there — a login shell
  needs a terminal.
```

If `/release` does not consume an `Unreleased` heading, rename it to the version being cut at release time.

- [ ] **Step 5: Verify the whole tree**

```bash
task check
```

Expected: PASS (documentation only, but the goldens and tests must still be green).

- [ ] **Step 6: Commit**

```bash
git add README.md CHANGELOG.md
git commit -m "docs: README and CHANGELOG for den exec's new contract and den shell"
```

---

## Self-Review

**Spec coverage**

| Spec section | Task |
|---|---|
| Contrat — `den exec` requires a command | 2, steps 1/3 |
| Contrat — `--` no longer needed, `SetInterspersed(false)` | 2, steps 3/4 |
| Contrat — `den shell` runs `bash -l` | 1, steps 1/5 |
| Contrat — `-T` refused on `shell`, still registered | 1, step 5 |
| Contrat — `--workdir` spelled long on both | 1 step 5, 2 step 4 |
| Refus 1 — no command, message names `den shell` | 2, steps 1/3 |
| Refus 2 — den flag or `--` in first command position | 2, steps 1/3 |
| `den shell` is not a second door — `enterSandbox` | 1, step 3 |
| `den sh` stays dead, no alias | nothing to do; `newShellCmd` registers no `Aliases` |
| Where the `-T`-without-command invariant went | 2, step 4 (pointer comment) + 1 step 5 (the message) |
| Portée — `internal/spawn` untouched | 2, step 7 asserts it |
| Tests — the 4 that invert or move | 1 step 1 (`TestShellRefusesNoTTY`), 2 step 1 |
| Tests — the 5 new ones | 2, step 1 |
| Golden, two hand edits | 1 step 8, 2 step 6 |
| README, CHANGELOG | 3 |

Two spec items are deliberately **not** tasks: the divergences (`den spawn` keeps `--`, `--workdir` vs compose's `-w`, no `exec -d`) are stated positions, nothing to implement, and slice 2 is issue [#72](https://github.com/PillowPillow/den/issues/72).

**Placeholder scan:** none. Every code step carries the code. The three places that say "trust the grep over this list" are naming a verification command, not deferring work — the list beside each was read out of the file on 2026-08-14 and may drift before execution.

**Type consistency:** `enterOptions` fields (`denHome`, `runner`, `sshAgent`, `goos`, `freshness`, `isTTY`) and `enterSandbox`'s signature `(cmd, ref, command, tty, workdir, o)` are spelled identically in Task 1 step 3, Task 1 step 5 and Task 2 step 4. `execFlagNames` is defined once (Task 2 step 3) and referenced once. `newShellCmd`'s parameter list matches its `root.go` call site.
