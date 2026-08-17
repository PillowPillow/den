package cli

import (
	"strings"
	"testing"
)

// shellSplit undoes shellQuote: it splits a proposed command line into the
// argv a shell would hand den.
//
// It replaces strings.Fields as the replay mechanism, and it has to: Fields
// splits on the very space the quoting protects, so it would declare the
// CORRECTED remedy broken. It is written here, tested here, and used by every
// replay below — a wrong splitter makes all of them vacuous.
//
// Do NOT "simplify" this away by having remedyLine return a []string for the
// test to replay. The property under test is that THE STRING den prints is
// legal when a human types it; replaying an argv bypasses the join and reopens
// exactly this hole.
func shellSplit(t *testing.T, line string) []string {
	t.Helper()
	var out []string
	var cur strings.Builder
	inWord, inQuote := false, false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case inQuote && c == '\'':
			inQuote = false
		case inQuote:
			cur.WriteByte(c)
		// The '\'' idiom (close, escape, reopen) puts a literal quote OUTSIDE the
		// quoted region: a backslash there escapes the next byte rather than
		// starting a new word. Without this case the escaped quote at i+1 is read
		// as an unmatched open, and the scan runs off the end of the line looking
		// for a close that the next real quote already consumed — reproduced: the
		// "an inner single quote" case below faults "unterminated quote" without
		// it.
		case c == '\\' && i+1 < len(line):
			i++
			cur.WriteByte(line[i])
			inWord = true
		case c == '\'':
			inQuote, inWord = true, true
		case c == ' ':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	if inQuote {
		t.Fatalf("unterminated quote in %q", line)
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out
}

func TestShellSplitUndoesTheQuotingRule(t *testing.T) {
	for _, tc := range []struct {
		name string
		line string
		want []string
	}{
		{"plain words", "den exec api true", []string{"den", "exec", "api", "true"}},
		{"a quoted space", "den exec --workdir '/tmp/hot fix' api true",
			[]string{"den", "exec", "--workdir", "/tmp/hot fix", "api", "true"}},
		{"an empty word", "den exec '' true", []string{"den", "exec", "", "true"}},
		// The '\'' idiom: close the quote, an escaped quote, reopen.
		{"an inner single quote", `den exec 'it'\''s' true`,
			[]string{"den", "exec", "it's", "true"}},
		{"a glob character survives quoting", "den up --repo '/dev/proj-*' api",
			[]string{"den", "up", "--repo", "/dev/proj-*", "api"}},
		// Pins the escape case's placement AFTER the two inQuote cases: a
		// backslash inside an open quote must stay a plain byte (shellQuote
		// single-quotes any token containing one, per shellSafe), not trigger
		// the outside-quote escape and eat the following byte.
		{"a backslash inside a quoted token", `den exec '/tmp/a\b' true`,
			[]string{"den", "exec", `/tmp/a\b`, "true"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := shellSplit(t, tc.line)
			if len(got) != len(tc.want) {
				t.Fatalf("split = %q, want %q", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("split = %q, want %q", got, tc.want)
				}
			}
		})
	}
}

// The two verdicts spawn.Spawn's step 0 owns, quoted here so every test in this
// class asserts the SAME bytes the spawn path prints. They carry no command-path
// prefix, and that is deliberate: they are spawn's verdict, not the validator's,
// and `den up -T api` has printed them prefix-free since 2026-08-16.
const (
	detachRefusal = "--detach and a command contradict each other — drop one: --detach spawns " +
		"without entering the sandbox, and `den run` runs a command inside it — " +
		"use `den up --detach <nest>`"
	spawnNoTTYRefusal = "-T asks for no terminal and no command asks for a shell, which needs one — " +
		"give a command with `den run -T <nest> <cmd>`, or drop -T"
)

// #76: den must never propose a line the target always refuses.
//
// Every row here proposed such a line until this test existed, and each one
// terminated at den's own refusal one round trip later. The flag is REGISTERED
// and always refused on the target — never cobra's `unknown flag` — so nothing
// was silently lost; what was lost is the property that makes a refusal
// answerable, and TestUp/RunRemediesAreThemselvesLegal could not see it: both
// replay through validateArgs, and the verdict lived past RunE.
//
// The answer is that the validator asks spawn's judge FIRST, so the refused flag
// is named instead of being carried into a proposal. The refusal therefore has
// NO "write `…`" half — there is no legal line carrying everything the user
// typed, and inventing one means either dropping their flag (the silent
// normalization spec §2 forbids) or inventing a command they never gave.
//
// The name check still outranks this one: `den up -T` answers "a nest expected".
// What is MISSING outranks what contradicts, which is enterArgs's own ordering.
func TestValidatorsRefuseAnAlwaysRefusedFlagInsteadOfProposingItBack(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
		want string
	}{
		// Proposed `den up --no-tty=true --repo /dev/hotfix api`.
		{"a no-tty and a second positional", []string{"up", "-T", "api", "/dev/hotfix"}, spawnNoTTYRefusal},
		// Proposed `den up --no-tty=true api`.
		{"a no-tty and a useless separator", []string{"up", "-T", "api", "--"}, spawnNoTTYRefusal},
		// The LIFTED half, and the one a name-keyed filter cannot reach: pflag eats
		// the leading `--`, so `-iT` never reaches the FlagSet and arrives fused in
		// s.flags. Proposed `den up -iT api`.
		{"a no-tty inside a lifted cluster", []string{"up", "--", "-iT", "api"}, spawnNoTTYRefusal},
		{"a no-tty lifted alone", []string{"up", "--", "-T", "api"}, spawnNoTTYRefusal},
		// The CROSS-COMMAND shape: --detach is legal on `den up`, and only the
		// `den run` line this branch proposes refuses it. Proposed
		// `den run --detach=true api go test`.
		{"a detach on a line that names a command", []string{"up", "--detach", "api", "--", "go", "test"},
			detachRefusal},
		// `den run --detach api` answered "no command given" and proposed
		// `den run --detach=true api go test`, refused in turn. The detach verdict
		// outranks the missing command because its own remedy — `den up --detach
		// <nest>` — is a complete legal line, and the missing-command one is not.
		{"a detach on den run", []string{"run", "--detach", "api"}, detachRefusal},
		{"a detach on den run with a command", []string{"run", "--detach", "api", "go", "test"},
			detachRefusal},
		// Lifted on `run` too, through the leading separator pflag eats.
		{"a detach lifted on den run", []string{"run", "--", "--detach", "api", "go", "test"},
			detachRefusal},
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
				t.Errorf("no line can carry everything typed, so none must be proposed; got %q",
					err.Error())
			}
		})
	}
}

// A value-aware verdict, and the reason a name-keyed skip set was the wrong
// mechanism: `--detach=false` is typeable, step 0 tests the VALUE, and this line
// is legal all the way through. A filter keyed on the name `detach` would delete
// it from the remedy — the silent normalization §2 forbids.
//
// Both halves are asserted, because both carry the flag.
func TestARefusedFlagSpelledFalseIsNotRefused(t *testing.T) {
	err := validateArgs(t, "run", "--detach=false", "api")
	if err == nil {
		t.Fatal("a run with no command must be refused")
	}
	const want = "den run: no command given — write `den run --detach=false api go test`, " +
		"or `den up --detach=false api` for a shell"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	// And the same for a lifted `-T=false`, where the value rides inside the
	// token: pflag binds the `=` to the letter preceding it, so this sets no-tty
	// to false and rules out nothing.
	err = validateArgs(t, "up", "--", "-T=false", "api")
	if err == nil {
		t.Fatal("a useless separator must be refused")
	}
	const wantLifted = "den up: `--` is not needed — write `den up -T=false api`"
	if err.Error() != wantLifted {
		t.Errorf("message = %q, want %q", err.Error(), wantLifted)
	}
}

// The two-half message loses the half the target refuses, and NOTHING else. That
// is not §2 normalization: no flag leaves any line — an ALTERNATIVE leaves the
// message, and the alternative is one den cannot honour.
//
// Asserted WHOLE, and it has to be: remedyOf reads the first backticked span
// only, so a replay can never see the shell half. That blindness is how the
// shell half was a Sprintf dropping every flag until 2026-08-16.
func TestTheShellHalfIsDroppedWhenTheTargetRefusesTheFlag(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
		want string
	}{
		// Proposed `den up --no-tty=true api` as the shell half, refused in turn.
		{"den run", []string{"run", "-T", "api"},
			"den run: no command given, and -T rules out a shell — " +
				"write `den run --no-tty=true api go test`"},
		// The THIRD refusal site: `den shell` refuses -T in its own RunE, not in
		// spawn's step 0. Proposed `den shell --no-tty=true api`.
		{"den exec", []string{"exec", "-T", "api"},
			"den exec: no command given, and -T rules out a shell — " +
				"write `den exec --no-tty=true api go test`"},
		// Lifted after the name, under SetInterspersed(false).
		{"den exec with a lifted no-tty", []string{"exec", "api", "-T"},
			"den exec: no command given, and -T rules out a shell — " +
				"write `den exec -T api go test`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateArgs(t, tc.argv...)
			if err == nil {
				t.Fatalf("%v must be refused", tc.argv)
			}
			if err.Error() != tc.want {
				t.Errorf("message = %q, want %q", err.Error(), tc.want)
			}
			replay := shellSplit(t, remedyOf(t, err.Error()))[1:] // drop "den"
			if err := validateArgs(t, replay...); err != nil {
				t.Errorf("the surviving half is refused in turn: %v", err)
			}
		})
	}
}

// The load-bearing half of #76's "also worth fixing alongside": validateArgs —
// the replay mechanism every remedy property runs through — must SEE the two
// contradictions spawn.Spawn's step 0 owns. It could not until 2026-08-17: the
// verdict lived past RunE, so a remedy replaying clean proved less than the
// property's name claimed.
//
// This is the test that stops the next instance. A remedy naming an
// always-refused flag now fails the replay in TestUp/RunRemediesAreThemselvesLegal
// rather than shipping.
func TestValidateArgsSeesTheContradictionsSpawnOwns(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want string
	}{
		{[]string{"up", "--no-tty=true", "api"}, spawnNoTTYRefusal},
		{[]string{"up", "-T", "api"}, spawnNoTTYRefusal},
		{[]string{"run", "--detach=true", "api", "go", "test"}, detachRefusal},
	} {
		t.Run(strings.Join(tc.argv, " "), func(t *testing.T) {
			err := validateArgs(t, tc.argv...)
			if err == nil {
				t.Fatalf("%v must be refused by the validator, not only by spawn", tc.argv)
			}
			if err.Error() != tc.want {
				t.Errorf("message = %q, want %q", err.Error(), tc.want)
			}
		})
	}
}
