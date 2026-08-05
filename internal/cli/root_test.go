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
// No caller of THIS helper goes through configureSpawn's RunE (`den ls` never
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
// Measured, not feared. `den sh` now reads ssh.mode to warn about an empty
// forwarded agent (sh.go), so a command whose test cares only about `sbx exec`
// reaches the den home and the agent behind it: under a readable DEN_HOME with
// SSH_AUTH_SOCK set, `go test -run TestShAttachesInTheWorkdir` really did fork
// `ssh-add -l` on the developer's own agent. A nil probe is what the warning
// treats as "nothing to ask", so no test through here consults an agent — nor,
// since that check comes first, any config.yaml.
//
// DEN_HOME is deliberately NOT pinned here: ls_test.go sets it itself, and a
// t.Setenv in this helper runs AFTER the test's own and would silently replace
// the fixture it was pointing at.
//
// Tests that mean to exercise the warning inject their own probe and their own
// den home (runShWithAgent, sh_test.go).
// The gate options are replaced too, and for the reason cli.Deps.Freshness
// exists: SystemDeps() carries time.Sleep and time.Now, and `den sh` on a
// STOPPED sandbox now waits on the §9.1 gate (sh.go). Left real, any such test
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

// The root having become the spawn command, an unknown first argument is no
// longer "an unknown command": it is a NEST NAME, and the failure must name
// the expected file rather than talk about a command.
//
// DEN_HOME is pinned: without it, this test would only pass on machines with
// no real ~/.den, and would read the developer's actual den home elsewhere.
func TestUnknownFirstArgumentIsANestNotFound(t *testing.T) {
	dir := testDenHomeWithNest(t, "api")
	// config.yaml must exist AND be VALID, or the spawn fails one step too
	// early and the error no longer says anything about the requested nest.
	// Since D1, config.LoadGlobal rejects an inconsistent config: a file
	// reduced to `defaults:` — what the earlier version wrote — is no longer
	// enough, and it would be the missing agent registry, not the nest, that
	// gets reported.
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"),
		[]byte("agents:\n  claude:\n    config_dir: /tmp/den/claude\n    update: \"claude update\"\n"+
			"defaults:\n  agent: claude\n  stack: devx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEN_HOME", dir)

	_, err := run(t, "doesnotexist")
	if err == nil {
		t.Fatal("expected an error for an unknown nest, got nil")
	}
	if !strings.Contains(err.Error(), filepath.Join(dir, "nests", "doesnotexist.yaml")) {
		t.Errorf("the error must name the expected nest file; got: %v", err)
	}
}

// TestWrongArgumentCountNamesTheUsageLine locks argsBetween's wording across
// every one of the package's eight call sites: it is the only way to notice
// that one site was missed, a wrong count on seven sites out of eight being
// indistinguishable from a finished job.
//
// The root itself is no longer one of these sites: since the positionals
// opened (den <nest> [repo...]), root.Args is cobra.ArbitraryArgs, which never
// rejects on count — every argument past the first is a repo, and nothing
// caps how many a spawn may mount. `den build` takes its place as the table's
// exercise of the `max == 1, min == 0` branch ("at most one argument
// expected") — without a case for it, that wording is asserted nowhere in the
// package once the root stops producing it.
func TestWrongArgumentCountNamesTheUsageLine(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		expected string
	}{
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
			"sh, missing argument",
			[]string{"sh"},
			"den sh: one argument expected, none received — usage: den sh <name> [flags]",
		},
		{
			"sh, two arguments",
			[]string{"sh", "a", "b"},
			`den sh: exactly one argument expected, 2 received, starting with "b" — usage: den sh <name> [flags]`,
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
// ONLY. This test is what keeps Task 2's move of that assignment honest: drop
// it while moving configureSpawn's body and `den doctr` silently stops
// suggesting anything.
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
