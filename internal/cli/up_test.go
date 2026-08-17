package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/doctor"
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
func TestUpRoutesToTheSpawn(t *testing.T) {
	t.Setenv("DEN_HOME", t.TempDir())
	dir := t.TempDir()

	if _, err := run(t, "up", "api", "--den-home", dir); err == nil {
		t.Fatal("an empty den home must fail the spawn")
	} else if !strings.Contains(err.Error(), filepath.Join(dir, "config.yaml")) {
		t.Errorf("the spawn must consult the given --den-home; got: %v", err)
	}
}

// Without the flag, resolving the den home must go through config.Home
// (hence DEN_HOME, then ~/.den). This case is what distinguishes "we call
// config.Home" from "we pass the flag's raw value": raw, it is "" and the
// spawn would read a "config.yaml" relative to cwd.
func TestUpWithoutFlagGoesThroughDenHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEN_HOME", dir)

	if _, err := run(t, "up", "api"); err == nil {
		t.Fatal("an empty den home must fail the spawn")
	} else if !strings.Contains(err.Error(), filepath.Join(dir, "config.yaml")) {
		t.Errorf("the spawn must resolve the den home through DEN_HOME; got: %v", err)
	}
}

// runUp runs `den up` on a given den home, with injected access.
// Same reason as runDoctor: without injection, the flag-to-spawn.Options
// wiring is unverifiable anywhere, and any test reaching `sbx create` would
// try to run the real binary.
//
// The tree is BARE — `den up` and nothing else. Tests that need den's real
// command list (the refusal, the suggestion) go through run() (NewRootCmd)
// in root_test.go instead — TestUnknownFirstArgumentListsTheCommands and
// TestUnknownFirstArgumentSuggestsTheCloseCommand.
func runUp(t *testing.T, home string, deps spawn.Deps, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "den", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newUpCmd(&home, deps))
	return executeCmd(t, root, append([]string{"up"}, args...)...)
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

// spawnCreatedNothing reports whether the only thing a refused spawn asked of
// sbx is the liveness listing.
//
// spawn.Spawn reads the sandbox list BEFORE nest.Resolve, so that a live
// sandbox is never asked a repo question nothing can act on (internal/spawn,
// step 1bis). Every refusal that comes out of the cascade therefore has one
// `sbx ls` behind it. The property these assertions defend is unchanged and is
// the one spec §6 states: a refusal creates NOTHING, and a listing creates
// nothing.
//
// Duplicated from internal/spawn's own helper rather than exported: it is a
// test-only predicate about a test double, and exporting it from a production
// package to share three call sites is the wrong trade.
func spawnCreatedNothing(f *sbx.Fake) bool {
	for _, call := range f.Calls {
		if !slices.Equal(call, []string{"ls", "--json"}) {
			return false
		}
	}
	return len(f.Attaches) == 0
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
	// Out stays nil: spawnNest overwrites it on every run with
	// cmd.OutOrStdout(), and Spawn falls back to io.Discard if it is missing.
	return f, spawn.Deps{
		Sbx:       f,
		Git:       worktree.NewGit(),
		Policy:    policy.DefaultOptions(),
		Freshness: instantGate(),
	}
}

// Every flag of `den up` must reach spawn.Options.
//
// The wiring is precisely what nobody tests, and an unwired flag is SILENT:
// `den up -w feat api` would create a sandbox "api" on the repo's main checkout,
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

			_, err := runUp(t, denHomeSpawnable(t), d, c.args...)
			if err == nil {
				t.Fatalf("%s with an invalid value must fail the spawn", c.name)
			}
			if !strings.Contains(err.Error(), c.expected) {
				t.Errorf("%s does not reach spawn.Options (expected %q); got: %v", c.name, c.expected, err)
			}
			if !spawnCreatedNothing(f) {
				t.Errorf("the refusal must create nothing; calls: %v", f.Calls)
			}
		})
	}
}

// runFullRoot runs the REAL command tree (every registered subcommand) on a
// given den home, with a fake sbx.Runner. The Fake is returned so the caller can
// assert the ABSENCE of a call as much as its presence.
//
// runUp does not fit the tests through here: its tree carries no
// --den-home flag (home is a direct pointer, not a registered flag) and no
// sibling commands — it exists for flag-wiring tests that inject spawn.Deps
// by hand and stop at the first spawn.Options error. The tests below drive
// den through --den-home like a real invocation, and
// TestANestHomonymOfASubcommandSpawnsNormally specifically needs a real `ls`
// subcommand registered alongside the spawn to prove the collision no longer
// happens — against runUp's bare root there is no subcommand to collide
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

	f, _, err := runFullRoot(t, home, "up", "ls")
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
	if _, err := runUp(t, home, dWith, "api", "--detach"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fWith.Attaches) != 0 {
		t.Errorf("--detach must not attach; attaches: %v", fWith.Attaches)
	}

	fWithout, dWithout := fakeSpawnDeps()
	if _, err := runUp(t, home, dWithout, "api"); err != nil {
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
	if _, err := runUp(t, home, dWith, "api", "--workdir", "/custom"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fWith.HasAttached("exec", "-it", "-w", "/custom", "api", "bash", "-l") {
		t.Errorf("--workdir must reach the attach's -w; attaches: %v", fWith.Attaches)
	}

	fWithout, dWithout := fakeSpawnDeps()
	if _, err := runUp(t, home, dWithout, "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fWithout.HasAttached("exec", "-it", "-w", "/custom", "api", "bash", "-l") {
		t.Errorf("without --workdir, \"/custom\" must not appear; attaches: %v", fWithout.Attaches)
	}
}

// -T/--no-tty must reach spawn.Options. Proven by the contradiction it is the
// only thing that can raise, the same idiom as -i and --detach above: `den up`
// opens a login shell, and -T asks it to give up the one thing that makes it
// worth opening, so spawn.Spawn refuses it. Unwired, o.NoTTY would stay false
// and this spawn would succeed instead.
//
// The message is asserted WHOLE, not with a Contains on "-T": the remedy is
// half of it, and a Contains on the flag passes on any sentence naming it —
// which is how "give a command after `--`" survived the separator it names.
// The remedy must be `den run`'s, the command form that exists on this family.
//
// BOTH spellings, because both are registered here and an unwired long name is
// as silent as an unwired short one. They produce the same refusal, so one
// `want` covers the pair.
func TestUpRefusesNoTTY(t *testing.T) {
	const want = "-T asks for no terminal and no command asks for a shell, which needs one — " +
		"give a command with `den run -T <nest> <cmd>`, or drop -T"
	for _, name := range []string{"-T", "--no-tty"} {
		t.Run(name, func(t *testing.T) {
			home := denHomeSpawnable(t)
			_, d := fakeSpawnDeps()

			_, err := runUp(t, home, d, name, "api")
			if err == nil {
				t.Fatal("-T with no command must be refused")
			}
			if err.Error() != want {
				t.Errorf("message = %q, want %q", err.Error(), want)
			}
		})
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

// runUpWithInput is runUp with a stdin the test controls: `-i` is the
// only flag whose behavior depends on what cobra hands down as the command's
// input.
func runUpWithInput(t *testing.T, home string, deps spawn.Deps, input string, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "den", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newUpCmd(&home, deps))
	root.SetIn(strings.NewReader(input))
	return executeCmd(t, root, append([]string{"up"}, args...)...)
}

// -i must reach spawn.Options. Proven by the contradiction it is the only
// thing that can raise: unwired, the flag falls back to false and the spawn
// succeeds on `--only docs` alone.
func TestInteractiveFlagReachesSpawnOptions(t *testing.T) {
	for _, name := range []string{"-i", "--interactive"} {
		t.Run(name, func(t *testing.T) {
			_, d := fakeSpawnDeps()

			_, err := runUp(t, denHomeWithOptionalRepo(t), d, "api", name, "--only", "docs")
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

	out, err := runUpWithInput(t, denHomeWithOptionalRepo(t), d, "1\n\n", "api", "-i")
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

	_, err := runUpWithInput(t, denHomeWithOptionalRepo(t), d, "\n", "api", "-i")
	if err == nil {
		t.Fatal("-i without a terminal probe must be refused")
	}
	if !strings.Contains(err.Error(), "--without") {
		t.Errorf("the refusal must name the non-interactive equivalents: %v", err)
	}
}

// --repo must reach spawn.Options. It replaces the positional repos of
// 2026-08-15, and the wiring question is the same one: an unwired flag is
// SILENT — the path would simply vanish and the spawn would succeed mounting
// nothing extra.
//
// Same shape as TestFlagsReachSpawnOptions: an INVALID value proves the wiring,
// because it is what produces, without sbx, a message that depends on the value
// passed.
func TestRepoFlagReachesSpawnOptions(t *testing.T) {
	f, d := fakeSpawnDeps()
	missing := filepath.Join(t.TempDir(), "gone")

	_, err := runUp(t, denHomeSpawnable(t), d, "--repo", missing, "api")
	if err == nil {
		t.Fatal("a --repo naming a path that does not exist must fail the spawn")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("--repo does not reach spawn.Options (expected %q); got: %v", missing, err)
	}
	if !strings.Contains(err.Error(), "command line") {
		t.Errorf("error = %q, expected it to name the command line as the place to fix", err)
	}
	if !spawnCreatedNothing(f) {
		t.Errorf("the refusal must create nothing; calls: %v", f.Calls)
	}
}

// The flag is REPEATABLE, and every occurrence reaches spawn.Options. The
// SECOND path is the invalid one: with a StringVar instead of a StringArrayVar,
// or with only the last occurrence read, this passes silently.
func TestSeveralRepoFlagsAllReachSpawnOptions(t *testing.T) {
	f, d := fakeSpawnDeps()
	missing := filepath.Join(t.TempDir(), "gone")
	present := t.TempDir()

	_, err := runUp(t, denHomeSpawnable(t), d, "--repo", present, "--repo", missing, "api")
	if err == nil {
		t.Fatal("expected the spawn to fail on the second --repo")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("only the first --repo seems to reach spawn.Options; got: %v", err)
	}
	if !spawnCreatedNothing(f) {
		t.Errorf("the refusal must create nothing; calls: %v", f.Calls)
	}
}

// The order the user types --repo is the order den mounts, and 2026-08-04
// depends on it: the repo order decides sbx's argv, hence mounts[0], hence
// StartDir's third rule (reached when the spawn runs from outside every mount).
// A repeatable flag keeps it as faithfully as a positional did — StringArrayVar
// appends.
//
// Real directories, not `/dev/b` and `/dev/a`: nest.Resolve checks that an
// ad-hoc repo EXISTS (TestRepoFlagReachesSpawnOptions is the test of that), so
// invented paths would refuse the spawn before any create argv is built.
//
// Asserted on the positions inside the create argv, the idiom
// TestANestWithNoRepoStillMountsTheAgentProfile (hostile_test.go) already uses:
// a Contains on the joined argv would hold for either order.
func TestUpKeepsTheOrderOfRepeatedRepoFlags(t *testing.T) {
	home := denHomeSpawnable(t)
	f, d := fakeSpawnDeps()
	root := t.TempDir()
	first, second := filepath.Join(root, "b"), filepath.Join(root, "a")
	for _, p := range []string{first, second} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := runUp(t, home, d, "--repo", first, "--repo", second, "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var create []string
	for _, call := range f.Calls {
		if len(call) > 0 && call[0] == "create" {
			create = call
		}
	}
	if create == nil {
		t.Fatalf("no `sbx create`; calls: %v", f.Calls)
	}
	i, j := slices.Index(create, first), slices.Index(create, second)
	if i < 0 || j < 0 {
		t.Fatalf("both repos must be mounted; create argv = %v", create)
	}
	if i > j {
		t.Errorf("--repo %s was typed first and must be mounted first; create argv = %v",
			first, create)
	}
}

// StringArrayVar, not StringSliceVar: StringSlice splits on commas and a path
// may contain one. Invisible on reading; the observable is spawn.Options.Repos
// as the Fake receives it, so this goes through a real Execute().
//
// The directory is really named `a,b` — with a StringSliceVar the spawn would
// not even reach a create, since it would refuse the two halves as two paths
// that do not exist.
func TestRepoFlagDoesNotSplitOnComma(t *testing.T) {
	home := denHomeSpawnable(t)
	f, d := fakeSpawnDeps()
	parent := t.TempDir()
	comma := filepath.Join(parent, "a,b")
	if err := os.MkdirAll(comma, 0o755); err != nil {
		t.Fatal(err)
	}

	root := &cobra.Command{Use: "den", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newUpCmd(&home, d))
	if _, err := executeCmd(t, root, "up", "--repo", comma, "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var create []string
	for _, call := range f.Calls {
		if len(call) > 0 && call[0] == "create" {
			create = call
		}
	}
	if create == nil {
		t.Fatalf("no `sbx create`; calls: %v", f.Calls)
	}
	if !slices.Contains(create, comma) {
		t.Errorf("the whole path %q must be mounted, not its halves; create argv = %v",
			comma, create)
	}
	// The split form, named explicitly: a StringSliceVar would produce a mount
	// named "<parent>/a" and another named "b".
	for _, arg := range create {
		if arg == filepath.Join(parent, "a") || arg == "b" {
			t.Errorf("the path was split on the comma; create argv = %v", create)
		}
	}
}

// `den up api -- go test` is a `run` typed `up`. The remedy must name `den run`
// — not --repo, and not "extra arguments": `go test` is a command, and
// proposing to mount two directories named `go` and `test` is the absurdity the
// branch ORDER exists to prevent. `--` never appears in args; only
// ArgsLenAtDash reveals it, so a validator counting positionals sees three.
func TestUpRefusesADoubleDashWithACommandByNamingRun(t *testing.T) {
	err := validateArgs(t, "up", "api", "--", "go", "test")
	if err == nil {
		t.Fatal("a command after `--` must be refused")
	}
	const want = "den up: den up takes no command — write `den run api go test`"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	if strings.Contains(err.Error(), "--repo") || strings.Contains(err.Error(), "extra arguments") {
		t.Errorf("the command reading must beat the repo reading; got %q", err.Error())
	}
}

// The two shapes where the separator carries nothing: dash == 0 and
// dash == len(args).
func TestUpRefusesAUselessDoubleDash(t *testing.T) {
	for _, argv := range [][]string{{"up", "--", "api"}, {"up", "api", "--"}} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			err := validateArgs(t, argv...)
			if err == nil {
				t.Fatalf("%v must be refused", argv)
			}
			const want = "den up: `--` is not needed — write `den up api`"
			if err.Error() != want {
				t.Errorf("message = %q, want %q", err.Error(), want)
			}
		})
	}
}

// The branch-2 remedy must carry a flag pflag consumed before the separator, or
// the mount vanishes from the line the user retypes. This is defect (F) seen
// from `up`.
func TestUpRemedyCarriesTheRepoAcrossADoubleDash(t *testing.T) {
	err := validateArgs(t, "up", "--repo", "/a", "--", "api")
	if err == nil {
		t.Fatal("a useless separator must be refused")
	}
	const want = "den up: `--` is not needed — write `den up --repo /a api`"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}

// Finger memory: the gesture the break makes most likely. The message must name
// --repo and what changed, which is exactly what exactlyOneArg's "2 received"
// does not.
func TestUpNamesTheRepoFlagOnASecondPositional(t *testing.T) {
	err := validateArgs(t, "up", "api", "/dev/hotfix")
	if err == nil {
		t.Fatal("a second positional must be refused")
	}
	const want = "den up: extra arguments — ad-hoc repos go behind --repo now — " +
		"write `den up --repo /dev/hotfix api`"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}

// The unset shell variable: `den up "$NEST" ~/dev/hotfix` hands den an empty
// first token, and until 2026-08-16 upArgs branched on len(args) and never on
// the shape, so it proposed a line naming a nest spelled as a quoted empty word
// — a nest the user never typed, which remedy.go calls worse than no proposal.
// `den run` refused the same shape with the usage line all along; this is the
// divergence closing.
//
// The last row is why the check sits ABOVE the separator branch rather than
// beside the old count: there the name is missing entirely, and the remedy
// spelled it as an empty word too.
//
// `den nest show` has its own row because it shares the validator: root_test.go
// makes the same argument for its own table, and a shared validator is exactly
// where one caller's coverage stops meaning anything about the other.
func TestUpRefusesAnEmptyNestRatherThanProposeOne(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
		want string
	}{
		{"an empty name and a repo", []string{"up", "", "/dev/hotfix"},
			"den up: a nest expected — usage: den up <nest> [flags]"},
		{"an empty name and a separator", []string{"up", "", "--"},
			"den up: a nest expected — usage: den up <nest> [flags]"},
		{"an empty name on nest show", []string{"nest", "show", "", "/a"},
			"den nest show: a nest expected — usage: den nest show <nest> [flags]"},
		{"no name at all behind a separator", []string{"up", "--", "--repo", "/a"},
			"den up: a nest expected — usage: den up <nest> [flags]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateArgs(t, tc.argv...)
			if err == nil {
				t.Fatalf("%v must be refused", tc.argv)
			}
			if err.Error() != tc.want {
				t.Errorf("message = %q, want %q", err.Error(), tc.want)
			}
			if strings.Contains(err.Error(), "write `") {
				t.Errorf("den must propose no line when it has no name; got %q", err.Error())
			}
		})
	}
}

// A shell pattern: --repo binds the first match, the rest arrive as positionals,
// and den cannot say which one is the nest. It must NOT build a remedy from
// them — branch 4 would take /dev/proj-b for the nest and propose mounting the
// real one.
func TestUpRefusesToGuessTheNestWhenRepoAndPositionalsCollide(t *testing.T) {
	err := validateArgs(t, "up", "--repo", "/dev/proj-a", "/dev/proj-b", "/dev/proj-c", "scratch")
	if err == nil {
		t.Fatal("--repo plus several positionals must be refused")
	}
	const want = "den up: --repo was given and 3 arguments remain, so den cannot tell which one is the nest\n" +
		"  — if a shell pattern expanded, quote it or repeat --repo once per path\n" +
		"  — if these are ad-hoc repos, repeat --repo once per path\n" +
		"  (the arguments were /dev/proj-b, /dev/proj-c, scratch)"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	if strings.Contains(err.Error(), "write `") {
		t.Errorf("den must not propose a line built from the positionals; got %q", err.Error())
	}
}

// The counter-example that made the message above get rewritten:
// Changed("repo") does NOT prove a pattern expanded. Here the user simply gave
// one repo to the flag and left another positional, and the message must claim
// no cause.
func TestUpDoesNotClaimAGlobWhenRepoWasSimplyGivenTwice(t *testing.T) {
	err := validateArgs(t, "up", "--repo", "/a", "api", "/b")
	if err == nil {
		t.Fatal("--repo plus two positionals must be refused")
	}
	if strings.Contains(err.Error(), "expanded to several") {
		t.Errorf("den must not invent a cause; got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "cannot tell which one is the nest") {
		t.Errorf("the message must state the fact; got %q", err.Error())
	}
}

// Interspersing is ON for `den up`, and this is what it buys: `den up api -T`
// reaches -T's NAMED refusal instead of an arity error naming neither the flag
// nor the way out. It runs through a real Execute() because the refusal lives in
// spawn.Spawn, past RunE.
func TestUpKeepsInterspersedFlags(t *testing.T) {
	home := denHomeSpawnable(t)
	_, d := fakeSpawnDeps()
	_, err := runUp(t, home, d, "api", "-T")
	if err == nil {
		t.Fatal("-T after the nest name must still reach its refusal")
	}
	if !strings.Contains(err.Error(), "drop -T") {
		t.Errorf("the named -T refusal must fire, not an arity error; got %q", err.Error())
	}
}

// The persistent --den-home is still read LEFT of the subcommand, as it was
// under `den spawn`. The observable is the den home actually read, so this goes
// through a real Execute(), on the REAL tree — the bare root runUp builds
// carries no --den-home at all.
//
// Both directions, because only the pair proves the flag is READ: the same line
// against a den home holding no config.yaml must fail naming it. A success
// alone would also be produced by a den home resolved from DEN_HOME, which the
// helper sets nowhere.
func TestUpStillReadsDenHomeBeforeTheSubcommand(t *testing.T) {
	home := denHomeSpawnable(t)
	f, spawnDeps := fakeSpawnDeps()
	deps := Deps{
		Doctor:    doctor.FakeDeps(),
		Sbx:       f,
		Git:       spawnDeps.Git,
		Policy:    spawnDeps.Policy,
		Freshness: spawnDeps.Freshness,
	}

	if _, err := executeCmd(t, NewRootCmdWith(deps), "--den-home", home, "up", "api", "--detach"); err != nil {
		t.Fatalf("den --den-home <home> up api: unexpected error: %v", err)
	}
	if !f.HasCalled("create", "--name", "api") {
		t.Errorf("the spawn read no den home to create from; calls: %v", f.Calls)
	}

	empty := t.TempDir()
	_, err := executeCmd(t, NewRootCmdWith(deps), "--den-home", empty, "up", "api", "--detach")
	if err == nil {
		t.Fatal("an empty den home must fail the spawn")
	}
	if !strings.Contains(err.Error(), filepath.Join(empty, "config.yaml")) {
		t.Errorf("`den up` must consult the --den-home typed left of it; got: %v", err)
	}
}
