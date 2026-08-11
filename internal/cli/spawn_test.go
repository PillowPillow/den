package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/policy"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/spawn"
	"github.com/PillowPillow/den/internal/worktree"
	"github.com/spf13/cobra"
)

func TestDenWithNoArgumentPrintsHelp(t *testing.T) {
	t.Setenv("DEN_HOME", t.TempDir())

	out, err := run(t)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "nest") {
		t.Errorf("`den` alone must print the help; got: %q", out)
	}
}

// The end-to-end wiring: args[0] becomes the nest, and --den-home is indeed
// the one the spawn consults. An empty den home fails at the very first step
// (reading config.yaml), which is enough to name the consulted directory
// without ever calling `sbx` — absent from this machine.
func TestSpawnRoutesToTheSpawn(t *testing.T) {
	t.Setenv("DEN_HOME", t.TempDir())
	dir := t.TempDir()

	if _, err := run(t, "spawn", "api", "--den-home", dir); err == nil {
		t.Fatal("an empty den home must fail the spawn")
	} else if !strings.Contains(err.Error(), filepath.Join(dir, "config.yaml")) {
		t.Errorf("the spawn must consult the given --den-home; got: %v", err)
	}
}

// Without the flag, resolving the den home must go through config.Home
// (hence DEN_HOME, then ~/.den). This case is what distinguishes "we call
// config.Home" from "we pass the flag's raw value": raw, it is "" and the
// spawn would read a "config.yaml" relative to cwd.
func TestSpawnWithoutFlagGoesThroughDenHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEN_HOME", dir)

	if _, err := run(t, "spawn", "api"); err == nil {
		t.Fatal("an empty den home must fail the spawn")
	} else if !strings.Contains(err.Error(), filepath.Join(dir, "config.yaml")) {
		t.Errorf("the spawn must resolve the den home through DEN_HOME; got: %v", err)
	}
}

// runSpawn runs the spawn command on a given den home, with injected access.
// Same reason as runDoctor: without injection, the flag-to-spawn.Options
// wiring is unverifiable anywhere, and any test reaching `sbx create` would
// try to run the real binary.
//
// The tree is BARE — the spawn and nothing else. Tests that need den's real
// command list (the refusal, the suggestion) go through run() (NewRootCmd)
// in root_test.go instead — TestUnknownFirstArgumentListsTheCommands and
// TestUnknownFirstArgumentSuggestsTheCloseCommand.
func runSpawn(t *testing.T, home string, deps spawn.Deps, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "den", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newSpawnCmd(&home, deps))
	return executeCmd(t, root, append([]string{"spawn"}, args...)...)
}

// denHomeSpawnable: a minimal den home on which a complete spawn succeeds.
//
// No git repo: no test in this file creates a worktree. No `egress:` either,
// which short-circuits the settle-loop (policy.Settle returns nil on an empty
// allowlist) — these tests are about flag wiring, not the loop, already
// locked in internal/spawn. Without this, a probe that failed to pass would
// make the suite really sleep for 60s.
func denHomeSpawnable(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "api")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	return denHomeFor(t, repo)
}

// denHomeFor writes a valid den home whose single nest points at the given repo.
// The repo's SHAPE is the caller's decision — a bare directory or a real git
// repository — and the only thing that varies between the fixtures.
func denHomeFor(t *testing.T, repo string) string {
	t.Helper()
	dir := t.TempDir()
	writeUnder(t, dir, "config.yaml", `agents:
  claude:
    config_dir: `+filepath.Join(dir, "agents", "claude")+`
    update: "claude update"
defaults:
  agent: claude
  stack: devx
`)
	writeUnder(t, dir, "stacks/devx/stack.yaml", "image: devx:v1\n")
	writeUnder(t, dir, "nests/api.yaml", "stack: devx\nrepos:\n  - { path: "+repo+" }\n")
	return dir
}

// emptySbxFake: an empty sandbox list plus a permissive policy verdict.
func emptySbxFake() *sbx.Fake {
	return &sbx.Fake{
		Responses: map[string]sbx.Response{
			"ls --json": {Output: []byte(`{"sandboxes":[]}`)},
		},
		Default: sbx.Response{Output: []byte(`{"allowed": true}`)},
	}
}

func fakeSpawnDeps() (*sbx.Fake, spawn.Deps) {
	f := emptySbxFake()
	// Built FIELD BY FIELD, not from a `spawn.SystemDeps()` whose Sbx would
	// then be overwritten: that constructor no longer exists (see
	// internal/spawn/spawn.go), because it built a second `sbx.NewExec("")` —
	// the wiring root_deps_test.go forbids — and its godoc claimed a
	// production role it never had.
	//
	// Git is the REAL one (worktree.NewGit), as before: safe for the tests
	// that use this helper because none of them passes `-w`, the spawn's only
	// path that consults Git. Whoever wants to prove Git's injection supplies
	// a fake explicitly (root_deps_test.go).
	//
	// Out stays nil: newSpawnCmd overwrites it on every run with
	// cmd.OutOrStdout(), and Spawn falls back to io.Discard if it is missing.
	return f, spawn.Deps{
		Sbx:       f,
		Git:       worktree.NewGit(),
		Policy:    policy.DefaultOptions(),
		Freshness: instantGate(),
	}
}

// Every flag of `den spawn` must reach spawn.Options.
//
// The wiring is precisely what nobody tests, and an unwired flag is SILENT:
// `den spawn api -w feat` would create a sandbox "api" on the repo's main checkout,
// without a worktree, and the user would only discover it by looking at their
// branch from inside the VM.
//
// Each flag is exercised with an INVALID value: that is what produces,
// without sbx, a message that depends on the passed value — hence proof it
// crossed the wiring. Unwired, the flag falls back to its zero value, the
// spawn succeeds, and there is no error at all anymore.
func TestFlagsReachSpawnOptions(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		expected string
	}{
		// "+wip", not "feature/123": since F4, a "/" is FLATTENED and therefore
		// accepted, it would no longer produce any error. What is left is what
		// flattening does not fix — a first character that is not
		// alphanumeric. "+" rather than "-": pflag would take "-wip" for a
		// flag before den ever sees anything.
		{"-w", []string{"api", "-w", "+wip"}, `worktree "+wip"`},
		{"--worktree", []string{"api", "--worktree", "+wip"}, `worktree "+wip"`},
		{"--agent", []string{"api", "--agent", "unknown"}, `agent "unknown"`},
		{"--without", []string{"api", "--without", "unknown"}, `--without: repo "unknown"`},
		{"--only", []string{"api", "--only", "unknown"}, `--only: repo "unknown"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, d := fakeSpawnDeps()

			_, err := runSpawn(t, denHomeSpawnable(t), d, c.args...)
			if err == nil {
				t.Fatalf("%s with an invalid value must fail the spawn", c.name)
			}
			if !strings.Contains(err.Error(), c.expected) {
				t.Errorf("%s does not reach spawn.Options (expected %q); got: %v", c.name, c.expected, err)
			}
			if len(f.Calls) != 0 {
				t.Errorf("no sbx call must have happened; calls: %v", f.Calls)
			}
		})
	}
}

// runFullRoot runs the REAL command tree (every registered subcommand) on a
// given den home, with a fake sbx.Runner. The Fake is returned so the caller can
// assert the ABSENCE of a call as much as its presence.
//
// runSpawn does not fit the tests through here: its tree carries no
// --den-home flag (home is a direct pointer, not a registered flag) and no
// sibling commands — it exists for flag-wiring tests that inject spawn.Deps
// by hand and stop at the first spawn.Options error. The tests below drive
// den through --den-home like a real invocation, and
// TestANestHomonymOfASubcommandSpawnsNormally specifically needs a real `ls`
// subcommand registered alongside the spawn to prove the collision no longer
// happens — against runSpawn's bare root there is no subcommand to collide
// with, so the property would hold true by construction of the stub, not by
// anything den does.
//
// deps.Git and deps.Policy stay those of SystemDeps(), which is safe HERE for
// a precise reason: the den homes built above declare no `egress:` (the
// settle-loop returns nil on an empty allowlist, so no 60s wait), and the tests
// that pass `-w` use denHomeHostile, whose repo is a real git repository.
//
// deps.SSHAgent is NOT safe left real, and this is the helper where it showed:
// these tests reach the spawn's preflight, whose empty-agent warning probes the
// agent — with SSH_AUTH_SOCK set in the environment, `go test ./internal/cli/`
// forked `ssh-add -l` against the developer's own agent (measured with a fake
// ssh-add on PATH). Nil is what the warning reads as "nothing to ask", and no
// test through here is about the warning: root_deps_test.go injects its own
// counting probe for that.
func runFullRoot(t *testing.T, home string, args ...string) (*sbx.Fake, string, error) {
	t.Helper()
	f := emptySbxFake()
	deps := SystemDeps()
	deps.Sbx = f
	deps.SSHAgent = nil
	// deps.Freshness is NOT safe left real either, and for the same shape of
	// reason as SSHAgent: the §9.1 gate polls the kit journal with a real
	// time.Sleep, and a fake sbx never answers anything a journal can be read
	// out of — so every test through here would stand and wait out the gate's
	// full ninety-second budget before warning. instantGate keeps the budget
	// and moves the clock only when the loop sleeps.
	deps.Freshness = instantGate()
	out, err := executeCmd(t, NewRootCmdWith(deps), append(args, "--den-home", home)...)
	return f, out, err
}

// A nest that carries a SUBCOMMAND'S OWN NAME spawns. Not a lookalike — the
// name itself.
//
// Until 2026-08-05 this was impossible: cobra routed `den ls` to the
// subcommand before the argument reached the root's RunE, so a nest named `ls`
// was unreachable for life. den knew, and warned about it in `den nest ls`
// (warnAboutShadowedNests, deleted with this change) — a warning is not a fix.
// Making the spawn a subcommand removes the collision instead of commenting
// on it, and this test is what says so.
func TestANestHomonymOfASubcommandSpawnsNormally(t *testing.T) {
	home := denHomeSpawnable(t)
	if err := os.WriteFile(filepath.Join(home, "nests", "ls.yaml"),
		[]byte("stack: devx\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f, _, err := runFullRoot(t, home, "spawn", "ls")
	if err != nil {
		t.Fatalf("a nest named \"ls\" must spawn; got: %v", err)
	}
	// Without this assertion, a spawn that did nothing at all would pass: it
	// must be sandbox "ls" that actually spawns.
	//
	// A positional check on call[2], not strings.Contains(joined, "ls"): the
	// old check matched "ls" anywhere in the whole argv, TMPDIR paths
	// included, so it happened to discriminate today but would go silently
	// tautological the moment a mount path or test name contained "ls".
	// CreateArgv (internal/sbx/argv.go) fixes the shape — call[0] "create",
	// call[1] "--name", call[2] the sandbox name — so that is the position
	// this test actually needs to pin.
	var created bool
	for _, call := range f.Calls {
		if len(call) > 2 && call[0] == "create" && call[2] == "ls" {
			created = true
		}
	}
	if !created {
		t.Errorf("no `create` for sandbox \"ls\"; calls: %v", f.Calls)
	}
	if len(f.Attaches) != 1 {
		t.Errorf("the spawn must attach; attaches: %v", f.Attaches)
	}
}

// --detach is the only flag whose value has no observable effect before the
// very end of the sequence: it is proven by the DIFFERENCE with the same
// spawn without the flag. Asserting the mere absence of an attach would prove
// nothing — a broken spawn would produce it just as well.
func TestDetachReachesSpawnOptions(t *testing.T) {
	home := denHomeSpawnable(t)

	fWith, dWith := fakeSpawnDeps()
	if _, err := runSpawn(t, home, dWith, "api", "--detach"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fWith.Attaches) != 0 {
		t.Errorf("--detach must not attach; attaches: %v", fWith.Attaches)
	}

	fWithout, dWithout := fakeSpawnDeps()
	if _, err := runSpawn(t, home, dWithout, "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fWithout.Attaches) != 1 {
		t.Errorf("without --detach, an attach must happen; attaches: %v", fWithout.Attaches)
	}
}

// --workdir must reach spawn.Options, proven the same way --detach is: by the
// DIFFERENCE with and without the flag. Unwired, o.Workdir stays "" and the
// attach directory would be whatever the spawn computed on its own — "/custom"
// would never appear on either run.
func TestWorkdirReachesSpawnOptions(t *testing.T) {
	home := denHomeSpawnable(t)

	fWith, dWith := fakeSpawnDeps()
	if _, err := runSpawn(t, home, dWith, "api", "--workdir", "/custom"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fWith.HasAttached("exec", "-it", "-w", "/custom", "api", "bash", "-l") {
		t.Errorf("--workdir must reach the attach's -w; attaches: %v", fWith.Attaches)
	}

	fWithout, dWithout := fakeSpawnDeps()
	if _, err := runSpawn(t, home, dWithout, "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fWithout.HasAttached("exec", "-it", "-w", "/custom", "api", "bash", "-l") {
		t.Errorf("without --workdir, \"/custom\" must not appear; attaches: %v", fWithout.Attaches)
	}
}

// -T/--no-tty must reach spawn.Options. Proven by the contradiction it is the
// only thing that can raise, the same idiom as -i and --detach above: with no
// command, -T asks for something a login shell cannot give up, and
// spawn.Spawn refuses it (mirroring `den exec`, internal/cli/exec.go).
// Unwired, o.NoTTY would stay false and this spawn would succeed instead.
func TestNoTTYReachesSpawnOptions(t *testing.T) {
	for _, name := range []string{"-T", "--no-tty"} {
		t.Run(name, func(t *testing.T) {
			_, d := fakeSpawnDeps()

			_, err := runSpawn(t, denHomeSpawnable(t), d, "api", name)
			if err == nil {
				t.Fatal("-T with no command must be refused: the flag did not reach spawn.Options")
			}
			if !strings.Contains(err.Error(), "-T") {
				t.Errorf("the refusal must name the flag in play: %v", err)
			}
		})
	}
}

// Mirrors TestExecPutsItsOwnChatterOnStderrWithoutATty (exec_test.go):
// `den spawn api -T -- go build > out.txt` must not let den's own log join
// the file the command owns. Before the I1 fix, spawn.Spawn wrote every line
// it says to d.Out regardless of a terminal, so this reaches `sbx exec`
// through Pipe with den's chatter already on stdout and FAILS against the
// old code.
//
// Uses executeCmdSeparateStreams directly (not runSpawn, which merges
// streams via executeCmd) because the whole point is telling the two apart.
func TestSpawnPutsItsOwnChatterOnStderrWithoutATty(t *testing.T) {
	home := denHomeSpawnable(t)
	_, d := fakeSpawnDeps()
	d.IsTTY = func() bool { return false }

	root := &cobra.Command{Use: "den", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newSpawnCmd(&home, d))
	stdout, stderr, err := executeCmdSeparateStreams(t, root, "spawn", "api", "--", "true")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stdout != "" {
		t.Errorf("stdout must belong to the command alone; got %q", stdout)
	}
	if !strings.Contains(stderr, "creating sandbox") {
		t.Errorf("den's own chatter must land on stderr; got %q", stderr)
	}
}

// The interactive path keeps saying it on stdout, as it always has (mirrors
// TestExecKeepsItsChatterOnStdoutWithATty, exec_test.go): nothing is piped
// there under a terminal, and moving it would change a surface #60 does not
// touch. Guards the other direction of the split — inverting spawn.go's `!tty`
// to `tty` would still pass TestSpawnPutsItsOwnChatterOnStderrWithoutATty's
// sibling above only by accident; this test would catch it.
func TestSpawnKeepsItsChatterOnStdoutWithATty(t *testing.T) {
	home := denHomeSpawnable(t)
	_, d := fakeSpawnDeps()
	d.IsTTY = func() bool { return true }

	root := &cobra.Command{Use: "den", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newSpawnCmd(&home, d))
	stdout, stderr, err := executeCmdSeparateStreams(t, root, "spawn", "api", "--", "true")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "creating sandbox") {
		t.Errorf("den's own chatter must stay on stdout under a tty; got %q", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr must stay empty on the tty path; got %q", stderr)
	}
}

// denHomeWithOptionalRepo: a spawnable den home whose nest declares one
// required repo and one optional one — the shape `-i` exists for.
func denHomeWithOptionalRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	api := filepath.Join(root, "api")
	docs := filepath.Join(root, "docs")
	for _, p := range []string{api, docs} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dir := denHomeFor(t, api)
	writeUnder(t, dir, "nests/api.yaml",
		"stack: devx\nrepos:\n  - { path: "+api+" }\n  - { path: "+docs+", optional: true }\n")
	return dir
}

// runSpawnWithInput is runSpawn with a stdin the test controls: `-i` is the
// only flag whose behavior depends on what cobra hands down as the command's
// input.
func runSpawnWithInput(t *testing.T, home string, deps spawn.Deps, input string, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "den", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newSpawnCmd(&home, deps))
	root.SetIn(strings.NewReader(input))
	return executeCmd(t, root, append([]string{"spawn"}, args...)...)
}

// -i must reach spawn.Options. Proven by the contradiction it is the only
// thing that can raise: unwired, the flag falls back to false and the spawn
// succeeds on `--only docs` alone.
func TestInteractiveFlagReachesSpawnOptions(t *testing.T) {
	for _, name := range []string{"-i", "--interactive"} {
		t.Run(name, func(t *testing.T) {
			_, d := fakeSpawnDeps()

			_, err := runSpawn(t, denHomeWithOptionalRepo(t), d, "api", name, "--only", "docs")
			if err == nil {
				t.Fatal("-i together with --only must be refused: the flag did not reach spawn.Options")
			}
			if !strings.Contains(err.Error(), "--only") {
				t.Errorf("the refusal must name the flag in play: %v", err)
			}
		})
	}
}

// The checklist reads the COMMAND's input, not os.Stdin: that is what makes it
// scriptable in a test, and what makes cobra's SetIn meaningful here.
func TestInteractiveReadsTheCommandInput(t *testing.T) {
	f, d := fakeSpawnDeps()
	d.IsTTY = func() bool { return true }

	out, err := runSpawnWithInput(t, denHomeWithOptionalRepo(t), d, "1\n\n", "api", "-i")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "docs") {
		t.Errorf("the checklist must be printed on the command's output:\n%s", out)
	}
	// "docs" unchecked: it must not be among the workspaces of the create.
	for _, call := range f.Calls {
		for _, arg := range call {
			if strings.HasSuffix(arg, "/docs") {
				t.Errorf("the unchecked repo must not be mounted; call: %v", call)
			}
		}
	}
}

// Without an injected probe, `-i` refuses instead of reading a stream nobody
// types into — the wiring tests build Deps by hand and must owe nothing to the
// machine running them.
func TestInteractiveRefusesWithoutATerminalProbe(t *testing.T) {
	_, d := fakeSpawnDeps()

	_, err := runSpawnWithInput(t, denHomeWithOptionalRepo(t), d, "\n", "api", "-i")
	if err == nil {
		t.Fatal("-i without a terminal probe must be refused")
	}
	if !strings.Contains(err.Error(), "--without") {
		t.Errorf("the refusal must name the non-interactive equivalents: %v", err)
	}
}

func TestPositionalsReachSpawnOptionsAsRepos(t *testing.T) {
	// Same shape as TestFlagsReachSpawnOptions: an INVALID value proves the
	// wiring, because an unwired argument is silent — the paths would simply
	// vanish and the spawn would succeed mounting nothing extra.
	f, d := fakeSpawnDeps()
	missing := filepath.Join(t.TempDir(), "gone")

	_, err := runSpawn(t, denHomeSpawnable(t), d, "api", missing)
	if err == nil {
		t.Fatal("a positional naming a path that does not exist must fail the spawn")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("positionals do not reach spawn.Options (expected %q); got: %v", missing, err)
	}
	if !strings.Contains(err.Error(), "command line") {
		t.Errorf("error = %q, expected it to name the command line as the place to fix", err)
	}
	if len(f.Calls) != 0 {
		t.Errorf("no sbx call must have happened; calls: %v", f.Calls)
	}
}

func TestSeveralPositionalsAllReachSpawnOptions(t *testing.T) {
	// The SECOND path is the invalid one: with `args[1:2]` instead of
	// `args[1:]`, or with only the first positional read, this passes silently.
	f, d := fakeSpawnDeps()
	missing := filepath.Join(t.TempDir(), "gone")
	present := t.TempDir()

	_, err := runSpawn(t, denHomeSpawnable(t), d, "api", present, missing)
	if err == nil {
		t.Fatal("expected the spawn to fail on the second positional")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("only the first positional seems to reach spawn.Options; got: %v", err)
	}
	if len(f.Calls) != 0 {
		t.Errorf("no sbx call must have happened; calls: %v", f.Calls)
	}
}

// Everything after `--` is the command; everything before it past the nest is
// still repos. Proven by an INVALID value, the idiom this file already uses
// for wiring (TestPositionalsReachSpawnOptionsAsRepos): a path that does not
// exist fails the spawn WHEN IT IS READ AS A REPO. Placed after `--`, it must
// not be read as one, so the spawn must not fail naming it.
func TestSpawnDoesNotMountTheCommandAsARepo(t *testing.T) {
	_, d := fakeSpawnDeps()
	missing := filepath.Join(t.TempDir(), "gone")

	_, err := runSpawn(t, denHomeSpawnable(t), d, "api", "--", missing)
	if err != nil && strings.Contains(err.Error(), missing) {
		t.Errorf("what follows `--` is a command, not a repo to mount: %v", err)
	}
}

// The other half, positive: the command REACHES spawn.Options. Proven by the
// contradiction it is the only thing that can raise — unwired, o.Command stays
// empty and `--detach` alone is a perfectly ordinary spawn.
func TestCommandReachesSpawnOptions(t *testing.T) {
	_, d := fakeSpawnDeps()

	_, err := runSpawn(t, denHomeSpawnable(t), d, "api", "--detach", "--", "go", "test")
	if err == nil {
		t.Fatal("--detach with a command must be refused: the command did not reach spawn.Options")
	}
	if !strings.Contains(err.Error(), "--detach") {
		t.Errorf("the refusal must name the flag in play: %v", err)
	}
}
