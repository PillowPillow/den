package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PillowPillow/den/internal/agent"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/spf13/cobra"
)

// executeCmd runs a GIVEN command tree and returns its output. Separate from
// run() so tests can build several trees before executing just one.
func executeCmd(t *testing.T, cmd *cobra.Command, args ...string) (string, error) {
	t.Helper()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// executeCmdSeparateStreams runs the command tree with TWO distinct buffers
// (stdout, stderr) rather than executeCmd's single merged one. Needed to
// check a message lands on one stream and NOT the other — a distinction
// executeCmd, which deliberately merges both, cannot make.
//
// Separate function rather than a signature change to executeCmd: the latter
// has a dozen existing callers in this package, including root_deps_test.go
// and spawn_test.go whose assertions must not change — touching all of them
// for a need only a few tests have (rm_test.go, ls_test.go) would be the
// wrong trade-off.
func executeCmdSeparateStreams(t *testing.T, cmd *cobra.Command, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

// run executes a fresh root command with args and returns its output.
func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return executeCmd(t, NewRootCmd(), args...)
}

// executeCmdWithSbx runs the command tree with an injected sbx.Runner; Doctor
// and Spawn.Policy access stay those of SystemDeps() (real). deps.Git IS
// exercised for real by the `den rm` tests that go through here (rm_test.go,
// e.g. TestRmDoesNotDestroyTheSandboxWhenAWorktreeIsDirty): they do touch real
// git — it is main_test.go's TestMain that makes this hermetic to the machine
// running the suite, not this helper.
//
// No caller of THIS helper goes through newSpawnCmd's RunE (`den ls` never
// calls it). runFullRoot (spawn_test.go) does exercise it: it builds its
// accesses the same way, and explains on the spot why that is safe there — a
// den home without `egress:` (no 60s settle-loop) and without a git repo (no
// git call).
func executeCmdWithSbx(t *testing.T, r sbx.Runner, args ...string) (string, error) {
	t.Helper()
	return executeCmd(t, NewRootCmdWith(sbxDeps(t, r)), args...)
}

// sbxDeps is SystemDeps with the injected Runner and with the ONE access that
// would otherwise make a test through these helpers depend on the machine
// running it: the SSH-agent probe, left nil.
//
// Measured, not feared. `den exec` now reads ssh.mode to warn about an empty
// forwarded agent (exec.go), so a command whose test cares only about `sbx exec`
// reaches the den home and the agent behind it: under a readable DEN_HOME with
// SSH_AUTH_SOCK set, `go test -run TestExecAttachesInTheWorkdir` really did fork
// `ssh-add -l` on the developer's own agent. A nil probe is what the warning
// treats as "nothing to ask", so no test through here consults an agent — nor,
// since that check comes first, any config.yaml.
//
// DEN_HOME is deliberately NOT pinned here: ls_test.go sets it itself, and a
// t.Setenv in this helper runs AFTER the test's own and would silently replace
// the fixture it was pointing at.
//
// Tests that mean to exercise the warning inject their own probe and their own
// den home (runExecWithAgent, exec_test.go).
// The gate options are replaced too, and for the reason cli.Deps.Freshness
// exists: SystemDeps() carries time.Sleep and time.Now, and `den exec` on a
// STOPPED sandbox now waits on the §9.1 gate (exec.go). Left real, any such test
// would stand still for the full 90 s budget against a fake that answers
// nothing — the suite would look hung rather than red.
func sbxDeps(t *testing.T, r sbx.Runner) Deps {
	t.Helper()
	deps := SystemDeps()
	deps.Sbx = r
	deps.SSHAgent = nil
	deps.Freshness = fakeGateOptions()
	return deps
}

// fakeGateOptions is DefaultGateOptions' budget on an injected clock that jumps
// instead of sleeping: the wait's SHAPE is preserved (same timeout, same
// interval, so maxRounds is the real one) while the suite never blocks.
func fakeGateOptions() agent.GateOptions {
	o := agent.DefaultGateOptions()
	now := time.Now()
	o.Sleep = func(d time.Duration) { now = now.Add(d) }
	o.Now = func() time.Time { return now }
	return o
}

// executeCmdWithSbxSeparateStreams combines executeCmdWithSbx (injected
// Runner) and executeCmdSeparateStreams (distinct stdout/stderr): needed for
// tests that check a message lands on one stream and NOT the other, on a
// command that also needs an sbx.Runner (`den ls`).
func executeCmdWithSbxSeparateStreams(t *testing.T, r sbx.Runner, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return executeCmdSeparateStreams(t, NewRootCmdWith(sbxDeps(t, r)), args...)
}

func TestVersionPrintsTheVersion(t *testing.T) {
	// Version is a package variable: restoring it keeps this test from
	// contaminating the next ones with a test value.
	original := Version
	t.Cleanup(func() { Version = original })
	Version = "1.2.3"
	out, err := run(t, "version")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "1.2.3") {
		t.Errorf("output = %q, expected containing %q", out, "1.2.3")
	}
}

// testDenHomeWithNest builds a minimal den home holding only a nest. `den nest
// ls` needs nothing else.
func testDenHomeWithNest(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "nests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nests", name+".yaml"), []byte("stack: devx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The --den-home flag belongs to the command INSTANCE, not the package.
//
// The interleaving is what matters: cmd2 is built BEFORE cmd1 runs. With a
// package variable, building cmd2 would reset the variable to "" (StringVar
// writes its default), then executing cmd1 would write dirA into it, and
// cmd2 — which carries no --den-home of its own — would inherit dirA instead
// of falling back to DEN_HOME. Building both trees in execution order would
// completely hide the defect.
func TestDenHomeIsScopedPerInstance(t *testing.T) {
	dirA := testDenHomeWithNest(t, "alpha")
	dirB := testDenHomeWithNest(t, "beta")
	t.Setenv("DEN_HOME", dirB)

	cmd1 := NewRootCmd()
	cmd2 := NewRootCmd() // built before cmd1 executes

	out1, err := executeCmd(t, cmd1, "nest", "ls", "--den-home", dirA)
	if err != nil {
		t.Fatalf("cmd1: unexpected error: %v", err)
	}
	if !strings.Contains(out1, "alpha") {
		t.Errorf("cmd1 = %q, expected the nest from %s", out1, dirA)
	}

	out2, err := executeCmd(t, cmd2, "nest", "ls")
	if err != nil {
		t.Fatalf("cmd2: unexpected error: %v", err)
	}
	if strings.Contains(out2, "alpha") {
		t.Errorf("cmd2 = %q: cmd1's --den-home leaked into another instance", out2)
	}
	if !strings.Contains(out2, "beta") {
		t.Errorf("cmd2 = %q, expected the nest from DEN_HOME (%s)", out2, dirB)
	}
}

// The whole refusal is a contract, so it is frozen whole.
//
// This test replaces TestUnknownFirstArgumentIsANestNotFound, which asserted
// the exact opposite — that `den doesnotexist` reported a missing nest FILE.
// That was the price of "the root IS the spawn command" (spec §11), and the
// spec of 2026-08-05 stopped paying it.
//
// A golden rather than a handful of Contains: the point is that den answers
// with EVERYTHING it can do. A test that checked three lines would go green on
// a listing that had silently lost the other eleven.
func TestUnknownFirstArgumentListsTheCommands(t *testing.T) {
	// DEN_HOME is pinned even though this path never reads it: if the refusal
	// ever regressed into a spawn, the test would otherwise run against the
	// developer's real ~/.den — and the real `sbx`.
	t.Setenv("DEN_HOME", t.TempDir())

	_, err := run(t, "api")
	if err == nil {
		t.Fatal("an unknown first argument must be refused: `den <nest>` no longer spawns")
	}

	path := filepath.Join("testdata", "unknown-command.golden")
	want, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("reading %s: %v", path, readErr)
	}
	if got := err.Error() + "\n"; got != string(want) {
		t.Errorf("the refusal is a contract.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// The suggestion, against den's REAL command list: Task 1 pins its shape on a
// throwaway tree, this pins that the root actually carries the distance.
func TestUnknownFirstArgumentSuggestsTheCloseCommand(t *testing.T) {
	t.Setenv("DEN_HOME", t.TempDir())

	_, err := run(t, "doctr")
	if err == nil {
		t.Fatal("`den doctr` must be refused")
	}
	if !strings.Contains(err.Error(), "`den doctor`") {
		t.Errorf("error = %q, expected it to suggest `den doctor`", err)
	}
}

// The flag that used to vanish. Before 2026-08-05 the six spawn flags lived on
// root.Flags(), so `den --detach` parsed cleanly, fell through to cmd.Help()
// and exited 0 — the flag silently discarded, which §2 forbids. Now the root
// does not know it.
//
// Without this test, a future PersistentFlags on the root reopens the hole
// without a sound.
func TestASpawnFlagOnTheRootIsRefused(t *testing.T) {
	t.Setenv("DEN_HOME", t.TempDir())

	for _, flag := range []string{"--detach", "-w", "--only"} {
		t.Run(flag, func(t *testing.T) {
			if _, err := run(t, flag); err == nil {
				t.Errorf("`den %s` must be refused: that flag belongs to `den spawn`", flag)
			} else if !strings.Contains(err.Error(), "unknown flag") &&
				!strings.Contains(err.Error(), "unknown shorthand flag") {
				// `-w` and `--only` take a value: a future PersistentFlags on
				// the root that merely re-registers them (without wiring)
				// would still fail here — "flag needs an argument: 'w' in
				// -w" — and an err == nil check alone would stay green
				// straight through that regression. Pinning the error to
				// "unknown flag"/"unknown shorthand flag" is what keeps this
				// test honest about refusing the flag as UNRECOGNIZED, not
				// merely as malformed.
				t.Errorf("`den %s` must be refused as UNKNOWN, not merely fail; got: %v", flag, err)
			}
		})
	}
}

// TestWrongArgumentCountNamesTheUsageLine locks argsBetween's wording across
// the package's call sites: it is the only way to notice that one site was
// missed, a wrong count everywhere but one place being indistinguishable from
// a finished job.
//
// A command with a bounded Args and no row here is the hole, not a gap in
// coverage: `den shell` shipped on 2026-08-14 with cobra.ExactArgs(1) — the one
// validator root.go says den never uses — and carried "accepts 1 arg(s),
// received 0" through a full review because no case named it. Add the row with
// the command.
//
// The root itself is not one of these sites: its Args is unknownCommand, which
// refuses on identity, not on count. `den spawn` is the site that exercises
// the "one argument expected" wording with an unbounded maximum — every
// argument past the first is a repo, and nothing caps how many a spawn may
// mount, so its "too many" branch is unreachable by design.
func TestWrongArgumentCountNamesTheUsageLine(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		expected string
	}{
		{
			"spawn, missing argument",
			[]string{"spawn"},
			"den spawn: one argument expected, none received — usage: den spawn <nest> [repo...] [-- <cmd> [args...]] [flags]",
		},
		{
			"build, too many arguments",
			[]string{"build", "a", "b"},
			`den build: at most one argument expected, 2 received, starting with "b" — usage: den build [stack] [flags]`,
		},
		{
			"version, extra argument",
			[]string{"version", "x"},
			`den version: no argument expected, "x" received — usage: den version [flags]`,
		},
		{
			"doctor, extra argument",
			[]string{"doctor", "x"},
			`den doctor: no argument expected, "x" received — usage: den doctor [flags]`,
		},
		{
			"ls, extra argument",
			[]string{"ls", "x"},
			`den ls: no argument expected, "x" received — usage: den ls [flags]`,
		},
		{
			"nest ls, extra argument",
			[]string{"nest", "ls", "x"},
			`den nest ls: no argument expected, "x" received — usage: den nest ls [flags]`,
		},
		{
			"nest show, missing argument",
			[]string{"nest", "show"},
			"den nest show: one argument expected, none received — usage: den nest show <nest> [repo...] [flags]",
		},
		{
			"exec, missing argument",
			[]string{"exec"},
			"den exec: a sandbox name and a command expected, none received — " +
				"usage: den exec <name> <cmd> [args...] [flags]",
		},
		// A sandbox name alone, NOT `exec b c`: since 2026-08-14 that pair is a
		// legal command, so it passes execArgs and reaches the real `sbx ls` —
		// this table runs through NewRootCmd(), whose runner is the machine's.
		// It really did list the developer's live sandboxes before the case was
		// changed. Every case here must refuse on the argument shape alone.
		{
			"exec, no command",
			[]string{"exec", "b"},
			"den exec: no command given — write `den exec b go test`, " +
				"or `den shell b` for a shell",
		},
		// `den shell` was born on 2026-08-14 with cobra.ExactArgs(1) and no row
		// here, which is exactly how it escaped: the wording this table locks is
		// den's, and a command that never appears in it can carry cobra's
		// "accepts 1 arg(s), received 0" through a whole review. Both directions,
		// because ExactArgs was wrong on both.
		{
			"shell, missing argument",
			[]string{"shell"},
			"den shell: one argument expected, none received — usage: den shell <name> [flags]",
		},
		{
			"shell, two arguments",
			[]string{"shell", "a", "b"},
			`den shell: exactly one argument expected, 2 received, starting with "b" — usage: den shell <name> [flags]`,
		},
		{
			"rm, missing argument",
			[]string{"rm"},
			"den rm: one argument expected, none received — usage: den rm <name> [flags]",
		},
		{
			"rm, two arguments",
			[]string{"rm", "a", "b"},
			`den rm: exactly one argument expected, 2 received, starting with "b" — usage: den rm <name> [flags]`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("DEN_HOME", t.TempDir())

			_, err := run(t, c.args...)
			if err == nil {
				t.Fatalf("%v must be rejected on argument count", c.args)
			}
			if err.Error() != c.expected {
				t.Errorf("message = %q, expected %q", err.Error(), c.expected)
			}
		})
	}
}

// testTree builds a throwaway root with two subcommands, for the tests of
// unknownCommandError below.
//
// A hand-built tree rather than NewRootCmd(): the message's SHAPE is what
// these tests pin — the padding, the order, the suggestion — and pinning it
// against den's real command list would make every future `AddCommand` break
// tests that are not about it. The real list is frozen ONCE, in the golden of
// TestUnknownFirstArgumentListsTheCommands.
func testTree() *cobra.Command {
	root := &cobra.Command{Use: "den", SilenceUsage: true, SilenceErrors: true}
	root.SuggestionsMinimumDistance = 2
	root.AddCommand(&cobra.Command{
		Use: "doctor", Short: "Diagnose", Run: func(*cobra.Command, []string) {},
	})
	root.AddCommand(&cobra.Command{
		Use: "ls", Short: "List live sandboxes", Run: func(*cobra.Command, []string) {},
	})
	return root
}

// The refusal must carry THREE things, and this test exists because any one of
// them can be dropped without the others noticing: what den did not
// understand, what den does understand, and where the bare form went.
func TestUnknownCommandErrorNamesTheArgumentAndTheCommands(t *testing.T) {
	err := unknownCommandError(testTree(), "api")
	if err == nil {
		t.Fatal("an unknown command must produce an error")
	}
	got := err.Error()

	if !strings.Contains(got, `unknown command "api"`) {
		t.Errorf("the refusal must quote what den did not understand; got:\n%s", got)
	}
	// The Short matters as much as the name: a bare list of names is a
	// vocabulary, not a contract.
	if !strings.Contains(got, "  ls          List live sandboxes") {
		t.Errorf("every command must come with its Short, cobra-padded; got:\n%s", got)
	}
	if !strings.Contains(got, "`den spawn <nest>`") {
		t.Errorf("the refusal must carry the migration line; got:\n%s", got)
	}
	// No `den: ` prefix: cmd/den/main.go already prints one, and a second
	// would read as a doubled program name.
	if strings.HasPrefix(got, "den:") {
		t.Errorf("the error value must not prefix itself with `den: `; got:\n%s", got)
	}
}

// The suggestion is what `withSuggestion` used to graft onto a nest-resolution
// failure. It moves here, where it answers the question actually asked —
// "which command did you mean" — instead of "this nest does not exist, and by
// the way".
func TestUnknownCommandErrorSuggestsACloseCommand(t *testing.T) {
	got := unknownCommandError(testTree(), "doctr").Error()

	if !strings.Contains(got, "`den doctor`") {
		t.Errorf("a one-letter typo must suggest the command; got:\n%s", got)
	}
}

// A far name suggests nothing: a suggestion offered at random teaches the
// reader to skip the line that matters.
func TestUnknownCommandErrorSuggestsNothingForAFarName(t *testing.T) {
	got := unknownCommandError(testTree(), "zzzz").Error()

	if strings.Contains(got, "did you mean") {
		t.Errorf("no suggestion must be made for a far name; got:\n%s", got)
	}
	// The list is still there: a far name is exactly when the reader needs it.
	if !strings.Contains(got, "Commands:") {
		t.Errorf("the command list must come even without a suggestion; got:\n%s", got)
	}
}

// SuggestionsMinimumDistance at 0 makes SuggestionsFor return prefix matches
// ONLY. This test is what keeps the 2026-08-05 move of that assignment
// honest: drop it while moving configureSpawn's body and `den doctr` silently
// stops suggesting anything.
func TestUnknownCommandErrorNeedsTheSuggestionDistance(t *testing.T) {
	root := testTree()
	root.SuggestionsMinimumDistance = 0

	if got := unknownCommandError(root, "doctr").Error(); strings.Contains(got, "den doctor") {
		t.Errorf("at distance 0 cobra suggests nothing: this test pins the field, not the wording; got:\n%s", got)
	}
}

// Zero argument is the ONLY case that must pass: it is `den` alone, which
// prints the help.
func TestUnknownCommandAcceptsNoArgument(t *testing.T) {
	if err := unknownCommand(testTree(), nil); err != nil {
		t.Errorf("`den` alone must be accepted, the RunE prints the help; got: %v", err)
	}
	if err := unknownCommand(testTree(), []string{"api"}); err == nil {
		t.Error("a first argument that is not a command must be refused")
	}
}
