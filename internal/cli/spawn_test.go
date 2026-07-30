package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/nest"
	"github.com/PillowPillow/den/internal/policy"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/spawn"
	"github.com/PillowPillow/den/internal/worktree"
	"github.com/spf13/cobra"
)

// The root becoming the spawn command, existing subcommands must especially
// not be swallowed as nest names.
//
// DEN_HOME is pinned to an empty directory in EVERY test going through run():
// if the root captured a token it should not, the spawn would run against the
// machine's REAL ~/.den — and the real `sbx`.
func TestSubcommandsStayPriority(t *testing.T) {
	t.Setenv("DEN_HOME", t.TempDir())

	out, err := run(t, "version")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(out, "den ") {
		t.Errorf("`den version` must stay the version command; got: %q", out)
	}
}

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
func TestDenNestRoutesToTheSpawn(t *testing.T) {
	t.Setenv("DEN_HOME", t.TempDir())
	dir := t.TempDir()

	if _, err := run(t, "api", "--den-home", dir); err == nil {
		t.Fatal("an empty den home must fail the spawn")
	} else if !strings.Contains(err.Error(), filepath.Join(dir, "config.yaml")) {
		t.Errorf("the spawn must consult the given --den-home; got: %v", err)
	}
}

// Without the flag, resolving the den home must go through config.Home
// (hence DEN_HOME, then ~/.den). This case is what distinguishes "we call
// config.Home" from "we pass the flag's raw value": raw, it is "" and the
// spawn would read a "config.yaml" relative to cwd.
func TestDenNestWithoutFlagGoesThroughDenHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEN_HOME", dir)

	if _, err := run(t, "api"); err == nil {
		t.Fatal("an empty den home must fail the spawn")
	} else if !strings.Contains(err.Error(), filepath.Join(dir, "config.yaml")) {
		t.Errorf("the spawn must resolve the den home through DEN_HOME; got: %v", err)
	}
}

// runSpawn runs the spawn command on a given den home, with injected access.
// Same reason as runDoctor: without injection, the flag-to-spawn.Options
// wiring is unverifiable anywhere, and any test reaching `sbx create` would
// try to run the real binary.
func runSpawn(t *testing.T, home string, deps spawn.Deps, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "den", SilenceUsage: true, SilenceErrors: true}
	configureSpawn(root, &home, deps)
	return executeCmd(t, root, args...)
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
	// Out stays nil: configureSpawn overwrites it on every run with
	// cmd.OutOrStdout(), and Spawn falls back to io.Discard if it is missing.
	return f, spawn.Deps{
		Sbx:    f,
		Git:    worktree.NewGit(),
		Policy: policy.DefaultOptions(),
	}
}

// Every flag of `den <nest>` must reach spawn.Options.
//
// The wiring is precisely what nobody tests, and an unwired flag is SILENT:
// `den api -w feat` would create a sandbox "api" on the repo's main checkout,
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
// runSpawn does not fit the D1 tests: it builds a BARE root, with no subcommand,
// and D1's suggestion reads precisely off root.Commands(). Against that root, the
// absence of a suggestion would be true by construction — the test would pass
// proving nothing.
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
	out, err := executeCmd(t, NewRootCmdWith(deps), append(args, "--den-home", home)...)
	return f, out, err
}

// D1, property 1 — THE design constraint. A nest REALLY named "doctr" spawns
// normally: the suggestion must never stand in front of an object that
// exists.
//
// Refusing up front any argument close to a subcommand — the obvious fix —
// would break exactly this case: den would list an object in `den nest ls`
// that it then refuses to address. This is the defect found in T3 with
// `-api`, and this test is what prevents reintroducing it.
func TestANestHomonymOfASubcommandSpawnsNormally(t *testing.T) {
	home := denHomeSpawnable(t)
	if err := os.WriteFile(filepath.Join(home, "nests", "doctr.yaml"),
		[]byte("stack: devx\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f, _, err := runFullRoot(t, home, "doctr")
	if err != nil {
		t.Fatalf("a nest named \"doctr\" must spawn; got: %v", err)
	}
	// Without this assertion, a spawn that did nothing at all would pass: it
	// must be sandbox "doctr" that actually spawns.
	var created bool
	for _, call := range f.Calls {
		joined := strings.Join(call, " ")
		if strings.HasPrefix(joined, "create ") && strings.Contains(joined, "doctr") {
			created = true
		}
	}
	if !created {
		t.Errorf("no `create` for sandbox \"doctr\"; calls: %v", f.Calls)
	}
	if len(f.Attaches) != 1 {
		t.Errorf("the spawn must attach; attaches: %v", f.Attaches)
	}
}

// D1, property 2 — the typo. `den doctr` with no nest file must suggest
// `den doctor`, WITHOUT ceasing to name the expected file: the path is what
// lets a user who really wanted a nest understand where den looked for it.
func TestATypoOnASubcommandIsSuggested(t *testing.T) {
	home := denHomeSpawnable(t)

	_, _, err := runFullRoot(t, home, "doctr")
	if err == nil {
		t.Fatal("a nonexistent nest must fail the spawn")
	}
	if !strings.Contains(err.Error(), "doctor") {
		t.Errorf("the error must suggest the close subcommand; got: %v", err)
	}
	if !strings.Contains(err.Error(), filepath.Join(home, "nests", "doctr.yaml")) {
		t.Errorf("the error must keep naming the expected nest file; got: %v", err)
	}
}

// D1, property 3 — a FAR name suggests nothing. An absurd suggestion ("did
// you mean den doctor?" for `den zzzz`) would cost more than it is worth: it
// would make the user doubt their own nest name.
//
// The test checks the ABSENCE of the suggestion template, not the absence of
// the word "doctor": the latter would be absent even from a den that
// suggested anything at random.
func TestAFarNameSuggestsNothing(t *testing.T) {
	home := denHomeSpawnable(t)

	_, _, err := runFullRoot(t, home, "zzzz")
	if err == nil {
		t.Fatal("a nonexistent nest must fail the spawn")
	}
	if strings.Contains(err.Error(), "did you mean") {
		t.Errorf("no suggestion must be made for a far name; got: %v", err)
	}
	if !strings.Contains(err.Error(), filepath.Join(home, "nests", "zzzz.yaml")) {
		t.Errorf("the error must name the expected nest file; got: %v", err)
	}
}

// D1, property 4 — a nest that IS PRESENT but unreadable suggests nothing
// either. "doctr" does exist here: suggesting `den doctor` would send the user
// to fix a typo they did not make, instead of looking at their file.
//
// The unreadability comes from a DIRECTORY in place of the file (EISDIR), not
// a chmod 0000: the suite runs as root, where permissions block nothing and
// the test would pass exercising nothing.
func TestANestThatExistsButIsUnreadableSuggestsNothing(t *testing.T) {
	home := denHomeSpawnable(t)
	if err := os.MkdirAll(filepath.Join(home, "nests", "doctr.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, _, err := runFullRoot(t, home, "doctr")
	if err == nil {
		t.Fatal("an unreadable nest must fail the spawn")
	}
	if strings.Contains(err.Error(), "did you mean") {
		t.Errorf("a nest that EXISTS must not be redirected to a subcommand; got: %v", err)
	}
}

// The suggestion only concerns the name ACTUALLY TYPED. Today, the spawn
// sequence only loads that one nest and the case is unreachable from the
// command line: the test therefore calls withSuggestion directly, rather than
// leaving the guard unproven. The day the spawn loads a second nest (an
// `extends:`, envisioned in spec §14), that other nest's absence would say
// nothing about a command-line typo, and suggesting `den doctor` would be a
// non sequitur.
func TestTheSuggestionOnlyConcernsTheTypedName(t *testing.T) {
	root := NewRootCmd()
	failing := func(name string) error {
		return &nest.NestNotFoundError{
			Name: name,
			Path: "/den/nests/" + name + ".yaml",
			Err:  fs.ErrNotExist,
		}
	}

	// The typed name is CLOSE to `doctor` in BOTH cases: that is what isolates
	// the guard. If the typed name were far, the absence of a suggestion would
	// come from the distance rather than the guard, and the test would prove
	// nothing.
	if err := withSuggestion(root, "doctr", failing("doctr")); !strings.Contains(err.Error(), "doctor") {
		t.Errorf("the typed name is the one that failed: the suggestion must come; got: %v", err)
	}
	err := withSuggestion(root, "doctr", failing("fullstack"))
	if strings.Contains(err.Error(), "did you mean") {
		t.Errorf("the missing nest (\"fullstack\") is not the one the user typed "+
			"(\"doctr\"): no suggestion must be made; got: %v", err)
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
