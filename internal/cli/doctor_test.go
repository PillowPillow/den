package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/doctor"
	"github.com/PillowPillow/den/internal/sshagent"
)

// runDoctor runs `den doctor` on a given den home with injected system
// access. The test must owe nothing to the machine running it: without this
// injection, the command's exit contract ("non-zero if a check fails") is
// unverifiable anywhere.
func runDoctor(t *testing.T, home string, deps doctor.Deps) (string, error) {
	t.Helper()
	cmd := newDoctorCmd(&home, deps)
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs(nil)
	err := cmd.Execute()
	return out.String(), err
}

func TestDoctorSucceedsWhenEverythingIsFine(t *testing.T) {
	home := testDenHome(t)
	out, err := runDoctor(t, home, doctor.FakeDeps())
	if err != nil {
		t.Fatalf("expected a nil exit on a healthy config, got: %v\n%s", err, out)
	}
	if !strings.Contains(out, "all good") {
		t.Errorf("output = %q, expected the final success message", out)
	}
	if strings.Contains(out, "[FAIL]") {
		t.Errorf("output = %q, expected no failure", out)
	}
}

// LA property of D2, and the reason the third level exists: a warning does
// NOT change `den doctor`'s exit code. Without it, the SSH_AUTH_SOCK check
// would turn `den doctor` red on any machine where one works locally without
// an SSH agent — a perfectly healthy configuration den has no way to
// distinguish from a faulty one.
//
// The test exercises the WHOLE command (Execute), not doctor.Run: it is cobra
// that turns the error returned by RunE into an exit code, so that is where
// the property is checked. `err == nil` is exactly what `den doctor` will
// render as an rc of 0.
func TestDoctorDoesNotFailOnAWarning(t *testing.T) {
	home := testDenHome(t)
	deps := doctor.FakeDeps()
	// No SSH agent. The fixture's config does not declare `ssh:`, so the mode
	// is the default, agent-forward — the nominal case, not a corner one.
	deps.Getenv = func(string) string { return "" }

	out, err := runDoctor(t, home, deps)
	if err != nil {
		t.Fatalf("a warning must not fail den doctor, got: %v\n%s", err, out)
	}
	if !strings.Contains(out, "[warn]") {
		t.Errorf("output = %q, expected a [warn] line", out)
	}
	if strings.Contains(out, "[FAIL]") {
		t.Errorf("output = %q, expected no failure: a missing SSH agent is not one", out)
	}
	// The warning must stay visible in the final summary: "all good" under a
	// [warn] line would read as leftover output.
	if strings.Contains(out, "all good") {
		t.Errorf("output = %q, \"all good\" contradicts the [warn] line", out)
	}
	if !strings.Contains(out, "warning") {
		t.Errorf("output = %q, the final summary must report the warning", out)
	}
	if !strings.Contains(out, "SSH_AUTH_SOCK") {
		t.Errorf("output = %q, expected the diagnostic naming SSH_AUTH_SOCK", out)
	}
}

// The socket-present blind spot, end to end: SSH_AUTH_SOCK is set but the
// agent behind it holds no key. The old check called that OK; now it is a
// [warn] line — non-blocking (rc 0), naming a fix — that `den doctor` renders
// through the whole command, not just doctor.Run.
func TestDoctorWarnsWhenTheForwardedAgentIsEmpty(t *testing.T) {
	home := testDenHome(t)
	deps := doctor.FakeDeps() // socket present (FakeSSHSocket), config default agent-forward
	deps.SSHAgent = func() sshagent.Result { return sshagent.Result{State: sshagent.StateEmpty} }

	out, err := runDoctor(t, home, deps)
	if err != nil {
		t.Fatalf("an empty agent is a warning, not a failure: %v\n%s", err, out)
	}
	if !strings.Contains(out, "[warn]") {
		t.Errorf("output = %q, expected a [warn] line for the empty agent", out)
	}
	if strings.Contains(out, "[FAIL]") {
		t.Errorf("output = %q, an empty agent must not fail den doctor", out)
	}
	if !strings.Contains(out, "ssh-add") {
		t.Errorf("output = %q, the warning must offer a concrete fix", out)
	}
}

// I1 — the COMBINATION nothing exercised: a warning AND a failure in the same
// diagnostic. The two neighboring tests only ever produce one at a time —
// TestDoctorDoesNotFailOnAWarning sets `Getenv → ""` on an otherwise healthy
// config, TestDoctorFailsWhenSbxIsMissing keeps the socket — and neither
// reaches the one place where the question actually arises.
//
// What this test locks, and nothing did before: the ORDER of newDoctorCmd's
// two output blocks. The "failure" block must precede the "warning" block, or
// the second returns nil before the first is ever reached. Measured before
// writing this test, with the two blocks swapped: the WHOLE suite stayed
// green (rc=0), and the binary returned 0 on a broken install while printing
// "no failure" under a [FAIL] line.
//
// A warning must therefore be exactly what it claims: without effect on the
// exit code, in BOTH directions — it does not create one (neighboring test),
// and it does not erase one (this one).
func TestDoctorFailsEvenWithAWarning(t *testing.T) {
	home := testDenHome(t)
	deps := doctor.FakeDeps()
	// The warning: no SSH agent, on a config whose SSH mode is the default
	// (agent-forward — the fixture declares no `ssh:` block).
	deps.Getenv = func(string) string { return "" }
	// The failure, INDEPENDENT of the first: sbx missing from PATH. Two
	// unrelated causes, so neither can be mistaken for an effect of the other.
	deps.LookPath = func(string) (string, error) { return "", errors.New("not found in PATH") }

	out, err := runDoctor(t, home, deps)
	if err == nil {
		t.Fatalf("a failure must STAY a failure in the presence of a warning: "+
			"`den doctor` would return 0 on a broken install; output:\n%s", out)
	}
	// The error counts ONLY failures: a warning counted as a failure would be
	// the other half of the defect.
	if !strings.Contains(err.Error(), "1 failing check(s)") {
		t.Errorf("error = %q, expected a count of failures only (1); "+
			"the warning must not be counted in it", err.Error())
	}
	// Both lines coexist: the warning does not hide the failure, and the
	// failure does not silence the warning.
	if !strings.Contains(out, "[FAIL]") {
		t.Errorf("output = %q, expected a [FAIL] line", out)
	}
	if !strings.Contains(out, "[warn]") {
		t.Errorf("output = %q, expected a [warn] line: a failure must not silence the warning", out)
	}
	// The epilogue must assert NOTHING reassuring. "no failure" under a [FAIL]
	// line is self-contradictory, and that is what the output showed with the
	// two blocks swapped.
	for _, forbidden := range []string{"no failure", "all good"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("output = %q, must not contain %q while a diagnostic is failing",
				out, forbidden)
		}
	}
}

func TestDoctorFailsWhenSbxIsMissing(t *testing.T) {
	home := testDenHome(t)
	deps := doctor.FakeDeps()
	deps.LookPath = func(string) (string, error) { return "", errors.New("not found in PATH") }

	out, err := runDoctor(t, home, deps)
	if err == nil {
		t.Fatal("expected an error: a missing sbx is a diagnostic failure")
	}
	if !strings.Contains(out, "[FAIL]") {
		t.Errorf("output = %q, expected a [FAIL] line", out)
	}
	if !strings.Contains(out, "sbx") {
		t.Errorf("output = %q, expected the sbx diagnostic", out)
	}
	// The other diagnostics must still be printed: den never stops at the
	// first problem.
	if !strings.Contains(out, "config.yaml") {
		t.Errorf("output = %q, expected the config.yaml diagnostic despite sbx failing", out)
	}
}

// The tests above build the command directly to inject their Deps, which
// leaves root.go's wiring (newDoctorCmd plugged into the root tree with
// doctor.SystemDeps()) uncovered. This one goes through NewRootCmd: it only
// asserts reachability and output, never the exit code tied to sbx.
func TestDoctorIsWiredIntoTheRootTree(t *testing.T) {
	t.Setenv("DEN_HOME", t.TempDir())
	out, err := run(t, "doctor")
	// config.yaml is missing: at least one diagnostic fails, whether sbx is
	// installed on the machine or not.
	if err == nil {
		t.Error("expected an error: config.yaml is missing from the den home")
	}
	if !strings.Contains(out, "den home:") {
		t.Errorf("output = %q, expected doctor's header", out)
	}
	if !strings.Contains(out, "config.yaml") {
		t.Errorf("output = %q, expected the config.yaml diagnostic", out)
	}
}

func TestDoctorFailsOnMissingConfig(t *testing.T) {
	out, err := runDoctor(t, t.TempDir(), doctor.FakeDeps())
	if err == nil {
		t.Error("expected an error when the config is missing")
	}
	if !strings.Contains(out, "config.yaml") {
		t.Errorf("output = %q, expected a mention of config.yaml", out)
	}
}
