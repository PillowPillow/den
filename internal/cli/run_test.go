package cli

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/spawn"
	"github.com/spf13/cobra"
)

// runRun runs `den run` on a given den home, with injected access. runUp's twin
// (up_test.go), and it exists for the same reason: without injection the
// flag-to-spawn.Options wiring is unverifiable, and any test reaching
// `sbx create` would try to run the real binary.
func runRun(t *testing.T, home string, deps spawn.Deps, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "den", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newRunCmd(&home, deps))
	return executeCmd(t, root, append([]string{"run"}, args...)...)
}

// Mirrors TestExecPutsItsOwnChatterOnStderrWithoutATty (exec_test.go):
// `den run -T api go build > out.txt` must not let den's own log join the file
// the command owns. Before the I1 fix, spawn.Spawn wrote every line it says to
// d.Out regardless of a terminal, so this reaches `sbx exec` through Pipe with
// den's chatter already on stdout and FAILS against the old code.
//
// Uses executeCmdSeparateStreams directly (not runRun, which merges streams via
// executeCmd) because the whole point is telling the two apart.
func TestRunPutsItsOwnChatterOnStderrWithoutATty(t *testing.T) {
	home := denHomeSpawnable(t)
	_, d := fakeSpawnDeps()
	d.IsTTY = func() bool { return false }

	root := &cobra.Command{Use: "den", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newRunCmd(&home, d))
	stdout, stderr, err := executeCmdSeparateStreams(t, root, "run", "api", "true")
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
// to `tty` would still pass TestRunPutsItsOwnChatterOnStderrWithoutATty above
// only by accident; this test would catch it.
func TestRunKeepsItsChatterOnStdoutWithATty(t *testing.T) {
	home := denHomeSpawnable(t)
	_, d := fakeSpawnDeps()
	d.IsTTY = func() bool { return true }

	root := &cobra.Command{Use: "den", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newRunCmd(&home, d))
	stdout, stderr, err := executeCmdSeparateStreams(t, root, "run", "api", "true")
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

// The command REACHES spawn.Options, and --detach is refused against it. Two
// properties in one line, because the contradiction is the only thing an
// unwired o.Command can be proven by: unwired, `--detach` alone is a perfectly
// ordinary spawn and nothing is refused.
//
// The message is asserted WHOLE, and its remedy must be `den up --detach` — the
// command that detaches. It used to promise "a command after `--`", a shape
// this family no longer has.
func TestRunRefusesDetach(t *testing.T) {
	home := denHomeSpawnable(t)
	_, d := fakeSpawnDeps()
	root := &cobra.Command{Use: "den", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newRunCmd(&home, d))
	_, err := executeCmd(t, root, "run", "--detach", "api", "true")
	if err == nil {
		t.Fatal("--detach with a command must be refused")
	}
	const want = "--detach and a command contradict each other — drop one: --detach spawns " +
		"without entering the sandbox, and `den run` runs a command inside it — " +
		"use `den up --detach <nest>`"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}

// -T/--no-tty must reach spawn.Options, and `den run` is the ONLY command that
// carries a WORKING one — `den up` registers it to refuse it, so
// TestUpRefusesNoTTY proves the refusal and nothing about the wiring. Registered
// per-command by design (registerSpawnFlags holds only the shared flags), which
// means this arrow has no other test behind it: `BoolVarP(&o.Detach, "no-tty",
// "T", …)` here would compile and pass every other case in this file.
//
// Proven by the DIFFERENCE with and without the flag, the idiom
// TestDetachReachesSpawnOptions uses, with the probe answering TRUE — the one
// case -T exists to override. Wired, `tty` is false and the command goes through
// Pipe; unwired, it goes through Attach.
//
// The cost of the silent version, measured by this repo already: `sbx exec -t`
// with no terminal behind it DISCARDS the command's output while still returning
// its status (2026-08-10 on v0.38.0, spec §14.0). `den run -T api go build | tee
// log` would come back empty and green.
func TestNoTTYReachesRunOptions(t *testing.T) {
	for _, name := range []string{"-T", "--no-tty"} {
		t.Run(name, func(t *testing.T) {
			home := denHomeSpawnable(t)

			fWith, dWith := fakeSpawnDeps()
			dWith.IsTTY = func() bool { return true }
			if _, err := runRun(t, home, dWith, name, "api", "true"); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(fWith.Attaches) != 0 {
				t.Errorf("%s must suppress the terminal; attaches: %v", name, fWith.Attaches)
			}
			if !fWith.HasPiped("exec") {
				t.Errorf("%s must send the command through Pipe; pipes: %v", name, fWith.Pipes)
			}

			fWithout, dWithout := fakeSpawnDeps()
			dWithout.IsTTY = func() bool { return true }
			if _, err := runRun(t, home, dWithout, "api", "true"); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(fWithout.Attaches) != 1 {
				t.Errorf("without %s, a terminal must be allocated; attaches: %v",
					name, fWithout.Attaches)
			}
		})
	}
}

// The command is a COMMAND, never a repo to mount. Proven by an INVALID value,
// the idiom up_test.go uses for the mirror property
// (TestRepoFlagReachesSpawnOptions): a path that does not exist fails the spawn
// WHEN IT IS READ AS A REPO, so a `den run` naming one must not fail on it.
//
// Weaker than it was before 2026-08-16, and deliberately kept: `den run` has no
// positional-repo syntax left for the command to be confused with, so the only
// regression it can still catch is a RunE that fed args[1:] to o.Repos.
func TestRunDoesNotMountTheCommandAsARepo(t *testing.T) {
	_, d := fakeSpawnDeps()
	missing := filepath.Join(t.TempDir(), "gone")

	_, err := runRun(t, denHomeSpawnable(t), d, "api", missing)
	if err != nil && strings.Contains(err.Error(), missing) {
		t.Errorf("what follows the nest name is a command, not a repo to mount: %v", err)
	}
}

// Every short spelling the exact-name comparison missed before 2026-08-16. Each
// of these, judged "not den's", became the nest name or the first word of the
// command and reached the VM as `bash: -wfeat: command not found`. The rows the
// old comparison missed ARE the test.
func TestRunRefusesDenFlagsAfterTheNestName(t *testing.T) {
	for _, argv := range [][]string{
		{"run", "api", "--workdir", "/srv", "go", "build"},
		{"run", "api", "--workdir=/srv", "go", "build"},
		{"run", "api", "-w", "feat", "go", "build"},
		{"run", "api", "-wfeat", "go", "build"},
		{"run", "api", "-w=feat", "go", "build"},
		{"run", "api", "-iT", "go", "build"},
		{"run", "api", "-Ti", "go", "build"},
		{"run", "api", "-iTw", "feat", "go", "build"},
		{"run", "api", "-iTwfeat", "go", "build"},
		{"run", "api", "-iT=true", "go", "build"},
		{"run", "api", "--repo", "/a", "go", "build"},
		{"run", "api", "--", "go", "build"},
	} {
		t.Run(strings.Join(argv[2:], " "), func(t *testing.T) {
			if err := validateArgs(t, argv...); err == nil {
				t.Fatalf("%v must be refused", argv)
			}
		})
	}
}

// The value travels WITH the cluster instead of being abandoned: `-iTw feat`
// consumes the next token, and the lifted line must carry it.
func TestRunLiftsAShorthandClusterWithItsValue(t *testing.T) {
	err := validateArgs(t, "run", "api", "-iTw", "feat", "go", "test")
	if err == nil {
		t.Fatal("a cluster after the nest name must be refused")
	}
	const want = "den run: den's flags go before the nest name — " +
		"write `den run -iTw feat api go test`"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}

// pflag's order rule, which the naive reading inverts: the `=` binds to the
// PRECEDING letter and ends the token, whatever that letter's arity. `-iT=true`
// sets both booleans; `-wi feat` sets worktree="i" and leaves `feat` positional.
//
// The third row is what that last sentence COSTS the remedy, and it is measured
// rather than assumed: `-wi` carries its value inside the token, so the cluster
// consumes nothing, and `feat` is the first word of the command. The proposed
// line therefore reads `den run -wi api feat go test` — worktree="i", nest api,
// command `feat go test`, which is exactly what the refused line meant. Moving
// `feat` left of `api`, as a reading that assumed the cluster consumed it would
// do, would silently make `feat` the nest.
func TestRunTreatsAnEqualsInsideAClusterAsTheValue(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want string
	}{
		{[]string{"run", "api", "-iT=true", "go", "test"},
			"den run: den's flags go before the nest name — write `den run -iT=true api go test`"},
		{[]string{"run", "api", "-Ti=false", "go", "test"},
			"den run: den's flags go before the nest name — write `den run -Ti=false api go test`"},
		{[]string{"run", "api", "-wi", "feat", "go", "test"},
			"den run: den's flags go before the nest name — write `den run -wi api feat go test`"},
	} {
		t.Run(tc.argv[2], func(t *testing.T) {
			err := validateArgs(t, tc.argv...)
			if err == nil {
				t.Fatalf("%v must be refused", tc.argv)
			}
			if err.Error() != tc.want {
				t.Errorf("message = %q, want %q", err.Error(), tc.want)
			}
		})
	}
}

// The command's OWN flags pass through untouched — what SetInterspersed(false)
// buys, and the guard against a classifier that overreaches.
func TestRunPassesTheCommandsOwnFlagsThrough(t *testing.T) {
	if err := validateArgs(t, "run", "api", "go", "test", "-v", "-run", "TestX"); err != nil {
		t.Errorf("the command's own flags must pass through; got %v", err)
	}
}

// An unknown LETTER disqualifies the whole cluster, and `-Tv` reaches the VM.
// That is the measured status quo of `den exec`, and the only right answer:
// `-Tv` is not a legal den spelling, so den cannot lift it into a remedy.
func TestRunPassesUnknownClustersToTheSandbox(t *testing.T) {
	if err := validateArgs(t, "run", "api", "-Tv", "go", "build"); err != nil {
		t.Errorf("an unknown cluster must reach the sandbox; got %v", err)
	}
}

// MUST go through a real Execute(): --help is ABSENT from the walk under
// Find+ParseFlags and PRESENT under Execute(), because cobra adds it in
// InitDefaultHelpFlag. A test built on validateArgs is blind to this
// regression.
//
// Asserted on the TAIL of a piped call rather than on a whole prefix, unlike
// TestExecPassesHelpToTheSandbox (exec_test.go): there the workspace is
// scripted, here `den run` creates the sandbox and StartDir picks the workdir
// out of a temporary directory this test cannot spell.
func TestRunPassesHelpToTheSandbox(t *testing.T) {
	home := denHomeSpawnable(t)
	f, d := fakeSpawnDeps()
	root := &cobra.Command{Use: "den", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newRunCmd(&home, d))
	if _, err := executeCmd(t, root, "run", "api", "mytool", "--help"); err != nil {
		t.Fatalf("--help past the nest name belongs to the command; got %v", err)
	}
	var reached bool
	for _, call := range f.Pipes {
		if len(call) >= 2 && slices.Equal(call[len(call)-2:], []string{"mytool", "--help"}) {
			reached = true
		}
	}
	if !reached {
		t.Errorf("`mytool --help` must reach the sandbox verbatim; pipes = %v", f.Pipes)
	}
}

// The persistent --den-home is in the derived table without being listed
// anywhere: left out, `den run api --den-home /tmp true` sent a program
// literally named `--den-home` into the sandbox.
func TestDerivedFlagSetSeesThePersistentDenHome(t *testing.T) {
	err := validateArgs(t, "run", "api", "--den-home", "/tmp", "true")
	if err == nil {
		t.Fatal("--den-home after the nest name must be refused")
	}
	const want = "den run: den's flags go before the nest name — " +
		"write `den run --den-home /tmp api true`"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}

// Two --repo read back off the FlagSet must come out as two PAIRS, in typing
// order. Value.String() renders a stringArray as "[/a,/b]", which reparses as
// one bogus path named exactly that — a syntactically legal remedy that is
// semantically false, so the replay below cannot catch it. SliceValue.GetSlice
// is the only correct read.
func TestRunRemediesCarryEveryRepoTypedBeforeTheNestName(t *testing.T) {
	err := validateArgs(t, "run", "--repo", "/b", "--repo", "/a", "api")
	if err == nil {
		t.Fatal("a run with no command must be refused")
	}
	const want = "den run: no command given — write `den run --repo /b --repo /a api go test`, " +
		"or `den up api` for a shell"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	if strings.Contains(err.Error(), "[/b,/a]") {
		t.Errorf("Value.String()'s slice form must never appear; got %q", err.Error())
	}
}

// The duplicate rule: the flag typed AFTER the name wins, because it is the last
// one typed and the one den is teaching the user to move.
func TestRunRemedyPrefersTheFlagTypedAfterTheName(t *testing.T) {
	err := validateArgs(t, "run", "--workdir", "/a", "api", "--workdir", "/b", "go", "test")
	if err == nil {
		t.Fatal("a flag after the nest name must be refused")
	}
	const want = "den run: den's flags go before the nest name — " +
		"write `den run --workdir /b api go test`"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}

func TestRunRemedyQuotesAValueContainingASpace(t *testing.T) {
	err := validateArgs(t, "run", "api", "--repo", "/tmp/hot fix", "go", "test")
	if err == nil {
		t.Fatal("a flag after the nest name must be refused")
	}
	const want = "den run: den's flags go before the nest name — " +
		"write `den run --repo '/tmp/hot fix' api go test`"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}

// The twin of slice 1's hard-won property: a remedy den proposes is itself
// accepted by den. Replayed through shellSplit, never strings.Fields.
func TestRunRemediesAreThemselvesLegal(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
		want string
	}{
		{"a flag after the name", []string{"run", "api", "-T", "go", "build"},
			"den run -T api go build"},
		{"a separator", []string{"run", "api", "--", "go", "build"},
			"den run api go build"},
		{"a leading separator", []string{"run", "--", "api", "go", "build"},
			"den run api go build"},
		{"a repo after the name", []string{"run", "api", "--repo", "/a", "go", "build"},
			"den run --repo /a api go build"},
		{"a repo with a space", []string{"run", "api", "--repo", "/tmp/hot fix", "go", "build"},
			"den run --repo '/tmp/hot fix' api go build"},
		{"a cluster with a value", []string{"run", "api", "-iTw", "feat", "go", "build"},
			"den run -iTw feat api go build"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateArgs(t, tc.argv...)
			if err == nil {
				t.Fatalf("%v must be refused", tc.argv)
			}
			got := remedyOf(t, err.Error())
			if got != tc.want {
				t.Errorf("remedy = %q, want %q (full message: %q)", got, tc.want, err.Error())
			}
			replay := shellSplit(t, got)[1:] // drop "den"
			if err := validateArgs(t, replay...); err != nil {
				t.Errorf("the remedy %q is refused in turn: %v", got, err)
			}
		})
	}
}

// The `den up` half of the same property, and it lives here because the builder
// is shared: `den up` proposes `den run` lines, so a remedy of its own that the
// other command refused would be invisible to the loop above.
func TestUpRemediesAreThemselvesLegal(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
		want string
	}{
		{"a useless separator", []string{"up", "--", "api"}, "den up api"},
		{"a trailing separator", []string{"up", "api", "--"}, "den up api"},
		{"a repo across the separator", []string{"up", "--repo", "/a", "--", "api"},
			"den up --repo /a api"},
		{"a second positional", []string{"up", "api", "/dev/hotfix"},
			"den up --repo /dev/hotfix api"},
		{"a command", []string{"up", "api", "--", "go", "test"}, "den run api go test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateArgs(t, tc.argv...)
			if err == nil {
				t.Fatalf("%v must be refused", tc.argv)
			}
			got := remedyOf(t, err.Error())
			if got != tc.want {
				t.Errorf("remedy = %q, want %q (full message: %q)", got, tc.want, err.Error())
			}
			replay := shellSplit(t, got)[1:] // drop "den"
			if err := validateArgs(t, replay...); err != nil {
				t.Errorf("the remedy %q is refused in turn: %v", got, err)
			}
		})
	}
}

// `den down` is a compose reflex, and cobra catches nothing on its own.
// SuggestFor on `rm` is what puts the real gesture in front of the user. The
// field must widen nothing else: `den dwon` still suggests nothing.
func TestDownSuggestsRm(t *testing.T) {
	t.Setenv("DEN_HOME", t.TempDir())

	_, err := run(t, "down", "api")
	if err == nil {
		t.Fatal("`den down` is not a command")
	}
	if !strings.Contains(err.Error(), "did you mean `den rm`?") {
		t.Errorf("`den down` must point at `den rm`; got %q", err.Error())
	}
	_, err = run(t, "dwon")
	if err == nil {
		t.Fatal("`den dwon` is not a command")
	}
	if strings.Contains(err.Error(), "did you mean") {
		t.Errorf("SuggestFor must widen nothing else; got %q", err.Error())
	}
}
