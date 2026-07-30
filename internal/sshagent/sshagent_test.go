package sshagent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The three OpenSSH exit codes plus an exec failure, each mapped to the state
// den acts on. The identity count is asserted on the keys case alone — it is
// the only state where it is meaningful.
func TestDetect(t *testing.T) {
	cases := []struct {
		name           string
		stdout         string
		code           int
		err            error
		wantState      State
		wantIdentities int
	}{
		{
			name:           "one identity",
			stdout:         "256 SHA256:AAAA user@host (ED25519)\n",
			code:           0,
			wantState:      StateKeys,
			wantIdentities: 1,
		},
		{
			name:           "several identities are counted",
			stdout:         "256 SHA256:AAAA a (ED25519)\n2048 SHA256:BBBB b (RSA)\n",
			code:           0,
			wantState:      StateKeys,
			wantIdentities: 2,
		},
		{
			// exit 1: the agent answered, it just has nothing loaded.
			name:      "empty agent",
			stdout:    "The agent has no identities.\n",
			code:      1,
			wantState: StateEmpty,
		},
		{
			// exit 0 with nothing listed: the code says "keys" and the output
			// says none. Trusting the code alone would report a healthy agent
			// holding 0 identities — the exact silent forward this package
			// exists to catch. The listing wins.
			name:      "exit 0 listing no identity is empty, not healthy",
			stdout:    "",
			code:      0,
			wantState: StateEmpty,
		},
		{
			// exit 2: "could not open a connection to your authentication agent".
			name:      "unreachable agent",
			code:      2,
			wantState: StateUnreachable,
		},
		{
			// ssh-add absent from PATH: the run itself fails, no exit code to read.
			name:      "exec failure",
			err:       errors.New(`exec: "ssh-add": executable file not found in $PATH`),
			wantState: StateUnreachable,
		},
		{
			// Any other non-zero code is no better an agent than exit 2.
			name:      "unexpected exit code folds into unreachable",
			code:      255,
			wantState: StateUnreachable,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run := func() (string, int, error) { return c.stdout, c.code, c.err }
			got := Detect(run)
			if got.State != c.wantState {
				t.Errorf("state = %v, want %v", got.State, c.wantState)
			}
			if got.Identities != c.wantIdentities {
				t.Errorf("identities = %d, want %d", got.Identities, c.wantIdentities)
			}
		})
	}
}

// The zero value must be the SAFE one, like sbx.Exec's zero DrainDelay. A
// Result nobody filled in — a struct built by a caller that forgot a field, a
// probe that returned early — must read as "no usable agent", never as a
// healthy one: this check exists to warn, and a zero value meaning "keys" makes
// it fail OPEN, staying silent exactly when nothing answered.
func TestResultZeroValueIsUnreachable(t *testing.T) {
	var zero Result
	if zero.State != StateUnreachable {
		t.Errorf("Result{}.State = %v, want StateUnreachable (%v): an unfilled Result must not "+
			"pass for a healthy agent", zero.State, StateUnreachable)
	}
	if zero.Identities != 0 {
		t.Errorf("Result{}.Identities = %d, want 0", zero.Identities)
	}
}

// A blank or trailing line must never be counted as an identity: `ssh-add -l`
// ends its list with a newline, and an off-by-one here would report "2 keys"
// on a single-key agent.
func TestCountIdentitiesIgnoresBlankLines(t *testing.T) {
	if n := countIdentities("only one (ED25519)\n\n"); n != 1 {
		t.Errorf("countIdentities = %d, want 1: blank lines must not count", n)
	}
	if n := countIdentities(""); n != 0 {
		t.Errorf("countIdentities(\"\") = %d, want 0", n)
	}
}

// One assertion per GOOS branch (spec §4): macOS needs the keychain flag,
// everywhere else a bare ssh-add loads the default ~/.ssh keys.
//
// The darwin branch is asserted whole, not by Contains: den prints this string
// for the user to paste, so a command that merely mentions the flag but cannot
// run is worse than no advice at all.
func TestFixCommand(t *testing.T) {
	darwin := FixCommand("darwin")
	if darwin != "ssh-add --apple-use-keychain" {
		t.Errorf("darwin fix = %q, want %q", darwin, "ssh-add --apple-use-keychain")
	}
	if !strings.Contains(darwin, "--apple-use-keychain") {
		t.Errorf("darwin fix = %q, must use --apple-use-keychain (keys live in the login keychain)", darwin)
	}
	// ssh-add's positional argument is a private KEY FILE. A path token —
	// `~/.ssh/` above all — makes ssh-add try to read a directory as a key and
	// exit 1 without loading anything, so the fix den prints would itself be
	// broken. Guard the last token so re-adding one turns this red again.
	fields := strings.Fields(darwin)
	if last := fields[len(fields)-1]; strings.Contains(last, "/") {
		t.Errorf("darwin fix = %q ends on the path token %q: ssh-add expects a key file there, "+
			"not a path — a bare ssh-add already loads the default ~/.ssh keys", darwin, last)
	}

	for _, goos := range []string{"linux", "windows"} {
		fix := FixCommand(goos)
		if fix != "ssh-add" {
			t.Errorf("%s fix = %q, want a bare \"ssh-add\"", goos, fix)
		}
		if strings.Contains(fix, "apple") {
			t.Errorf("%s fix = %q, must not carry the macOS-only flag", goos, fix)
		}
	}
}

// stubSSHAdd plants an executable `ssh-add` in a fresh directory, to be put on
// the PATH ALONE so the developer's real ssh-add cannot answer instead (the
// hermeticity rule doctor/fake.go states: a test must owe nothing to the
// machine running it). The stub echoes line on stdout — nothing at all when
// line is empty, as ssh-add does when it only writes to stderr — records its
// argv in a file, and exits with code.
//
// It uses shell BUILTINS only (echo, exit, redirection): the PATH it runs under
// holds nothing but its own directory, so an external utility — /usr/bin/printf
// on a shell that doesn't build printf in — would not resolve.
func stubSSHAdd(t *testing.T, line string, code int) (dir, argvFile string) {
	t.Helper()
	dir = t.TempDir()
	argvFile = filepath.Join(dir, "argv")
	script := []string{"#!/bin/sh", fmt.Sprintf("echo \"$@\" > %q", argvFile)}
	if line != "" {
		script = append(script, fmt.Sprintf("echo %q", line))
	}
	script = append(script, fmt.Sprintf("exit %d", code))
	stub := filepath.Join(dir, "ssh-add")
	if err := os.WriteFile(stub, []byte(strings.Join(script, "\n")+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, argvFile
}

// The real WIRING, which every Detect case above bypasses by injecting a
// double. Two properties nothing else pins, both invisible to a test that
// merely accepts whatever the machine's own agent answers:
//
//   - a non-zero EXIT is data, not a run failure: it must come back as
//     (stdout, code, nil). Reported as an err instead, exit 1 — a reachable
//     agent holding no key, the very case this package exists for — would fold
//     into StateUnreachable, and den would tell someone whose agent is running
//     to go start an agent;
//   - the argv is `ssh-add -l`. A BARE `ssh-add` loads the default ~/.ssh keys
//     and can block on a passphrase prompt; a probe must never do that.
//
// t.Setenv forbids t.Parallel in this test (same note as doctor_test.go:556).
func TestSystemExecTranslatesExitCodes(t *testing.T) {
	cases := []struct {
		name           string
		line           string
		code           int
		wantState      State
		wantIdentities int
	}{
		{
			// exit 0 with a listing: the healthy agent.
			name:           "exit 0 lists identities",
			line:           "256 SHA256:AAAA user@host (ED25519)",
			code:           0,
			wantState:      StateKeys,
			wantIdentities: 1,
		},
		{
			// exit 1: reachable, nothing loaded. Must NOT read as a failure.
			name:      "exit 1 is a reachable but empty agent",
			line:      "The agent has no identities.",
			code:      1,
			wantState: StateEmpty,
		},
		{
			// exit 2, "could not open a connection...", written to stderr —
			// which SystemExec discards, so stdout comes back empty.
			name:      "exit 2 is an unreachable agent",
			code:      2,
			wantState: StateUnreachable,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, argvFile := stubSSHAdd(t, c.line, c.code)
			t.Setenv("PATH", dir)

			stdout, code, err := SystemExec()()
			if err != nil {
				t.Fatalf("SystemExec err = %v for exit %d, want nil: a non-zero exit is the "+
					"signal Detect reads, not a run failure", err, c.code)
			}
			if code != c.code {
				t.Errorf("exit code = %d, want %d", code, c.code)
			}
			want := c.line
			if want != "" {
				want += "\n" // echo's newline, as ssh-add -l ends its listing
			}
			if stdout != want {
				t.Errorf("stdout = %q, want %q", stdout, want)
			}

			argv, readErr := os.ReadFile(argvFile)
			if readErr != nil {
				t.Fatalf("the stub ssh-add did not run: %v", readErr)
			}
			if got := strings.TrimSpace(string(argv)); got != "-l" {
				t.Errorf("ssh-add was run with %q, want \"-l\": a bare ssh-add LOADS keys "+
					"and may prompt for a passphrase", got)
			}

			got := Detect(SystemExec())
			if got.State != c.wantState {
				t.Errorf("Detect(SystemExec()) state = %v, want %v", got.State, c.wantState)
			}
			if got.Identities != c.wantIdentities {
				t.Errorf("identities = %d, want %d", got.Identities, c.wantIdentities)
			}
		})
	}
}

// stubStalledSSHAdd plants an `ssh-add` that ACCEPTS the call and never
// answers, the exact shape of the failure the deadline exists for: a forwarded
// agent socket whose far end is gone connects fine, then hangs. body is the
// shell line that stalls, with %s standing for the absolute path to `sleep`.
//
// That path is resolved HERE, before the caller narrows the PATH to the stub's
// own directory (the hermeticity rule stubSSHAdd states): `sleep` is not a
// shell builtin, so on the stripped PATH the stub would fail to resolve it and
// exit AT ONCE — and a stub that returns immediately makes every duration
// assertion below pass for the wrong reason.
func stubStalledSSHAdd(t *testing.T, body string) string {
	t.Helper()
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skipf("no `sleep` on the PATH, nothing to stall with: %v", err)
	}
	dir := t.TempDir()
	script := "#!/bin/sh\n" + fmt.Sprintf(body, sleep) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "ssh-add"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A probe that never comes back is worse than the risk it reports: `ssh-add -l`
// runs on the mainline `den <nest>` path and in `den doctor`, so an agent that
// accepts the connection and stays silent used to suspend den forever on an
// ADVISORY check. Measured before the bound existed: 10.003 s for a stub that
// stalls 10 s, i.e. exactly as long as the stall lasts.
//
// TWO SCRIPT SHAPES, and the second is the one with teeth (same lesson as
// sbx's TestExecRunBoundsWaitWhenAGrandchildHoldsThePipe):
//
//   - `exec sleep 10` — ssh-add IS the sleeping process, so killing it on the
//     deadline closes stdout and the call returns;
//   - `sleep 10 & wait` — the kill hits the shell, and the orphaned `sleep`
//     keeps the inherited stdout pipe OPEN. os/exec goes on draining it, so
//     the deadline alone buys nothing: measured 10.00 s with cmd.WaitDelay
//     removed, 0.10 s with it. It is what pins WaitDelay.
//
// The bound is INJECTED at 50 ms rather than slept through at its real value:
// what needs proving here is that a bound applies at all, and the 2 s choice
// is read off the constant in the test below (sbx's effectiveDelay draws the
// same line). t.Setenv forbids t.Parallel.
func TestSystemExecBoundsAStalledSSHAdd(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"ssh-add itself never answers", "exec %s 10"},
		{"an orphaned descendant holds stdout open", "%s 10 & wait"},
	}
	const bound = 50 * time.Millisecond
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("PATH", stubStalledSSHAdd(t, c.body))

			start := time.Now()
			stdout, code, err := systemExec(bound)()
			elapsed := time.Since(start)

			if elapsed > 2*time.Second {
				t.Errorf("the probe took %v to return with a %v bound: the stub stalls for 10 s, "+
					"so nothing cuts it short and a wedged agent blocks `den <nest>` for as long "+
					"as it stalls", elapsed, bound)
			}
			// A deadline-killed process comes back as an *exec.ExitError whose
			// ExitCode() is -1. Reported as a code with err == nil, that -1
			// would be an invented answer from a conversation that never
			// happened — and the day Detect gains a case for a new code, an
			// invented one would land in it.
			if err == nil {
				t.Errorf("err = nil (stdout %q, code %d): a probe KILLED on its deadline got no "+
					"answer from the agent and must not report one", stdout, code)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty when the probe was killed", stdout)
			}
			// What the caller acts on: a timeout is "no usable agent", which
			// warns — never a healthy or empty agent, which would stay silent.
			if got := Detect(systemExec(bound)); got.State != StateUnreachable {
				t.Errorf("Detect state = %v, want StateUnreachable when the probe times out", got.State)
			}
		})
	}
}

// probeTimeout's VALUE, checked by reading it rather than by sleeping through
// it: the bound itself is proven above, here it is the choice (same split as
// sbx's TestEffectiveDelayNeverReturnsZero, and the reason SystemExec is a
// one-line delegation to systemExec).
//
// Zero is the trap at each end. context.WithTimeout(ctx, 0) is expired on
// arrival: every probe would be killed before ssh-add could answer, and den
// would warn "no usable agent" at everyone, forwarded keys or not. Too large a
// value brings back the hang this bound removes.
func TestProbeTimeoutIsANonZeroBound(t *testing.T) {
	if probeTimeout <= 0 {
		t.Errorf("probeTimeout = %v: a non-positive deadline is already expired when the probe "+
			"starts, so every agent reads as unreachable", probeTimeout)
	}
	if probeTimeout > 10*time.Second {
		t.Errorf("probeTimeout = %v: `ssh-add -l` queries a local socket, and den blocks on it "+
			"before every spawn", probeTimeout)
	}
}

// `ssh-add` absent from the PATH is the one outcome that IS a run failure: no
// exit code was ever produced, so SystemExec must surface err — and the 0 that
// comes back beside it must never be read as "exit 0, agent healthy". Detect's
// err branch is what makes that safe.
func TestSystemExecReportsAMissingSSHAdd(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // an empty dir: no ssh-add anywhere

	stdout, _, err := SystemExec()()
	if err == nil {
		t.Fatalf("SystemExec err = nil with no ssh-add on the PATH (stdout %q): a run that never "+
			"happened must not pass for an agent's answer", stdout)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty when ssh-add could not be run", stdout)
	}
	if got := Detect(SystemExec()); got.State != StateUnreachable {
		t.Errorf("Detect state = %v, want StateUnreachable when ssh-add is absent", got.State)
	}
}

// System is the single construction cli.SystemDeps and doctor.SystemDeps both
// call, so what it owes them is the FULL probe, not merely a non-nil func: it
// must run the real ssh-add and hand its listing to Detect. A System that had
// lost either half would return the zero Result — StateUnreachable, no identity
// — while a stub agent answers with a key, i.e. den warning "no usable agent"
// at the one healthy case.
//
// The probe is BUILT BEFORE the agent exists, and that order is the second
// assertion: doctor and cli assemble their Deps at startup and call them much
// later, so a System computing its Result eagerly (`r := Detect(...); return
// func() Result { return r }` — which compiles just as well) would freeze the
// startup verdict and keep reporting an agent unlocked afterwards as dead.
//
// t.Setenv forbids t.Parallel, as in TestSystemExecTranslatesExitCodes.
func TestSystemProbesTheRealSSHAddWhenCalled(t *testing.T) {
	probe := System() // built while no ssh-add is reachable yet

	dir, argvFile := stubSSHAdd(t, "256 SHA256:AAAA user@host (ED25519)", 0)
	t.Setenv("PATH", dir)

	got := probe()
	if got.State != StateKeys || got.Identities != 1 {
		t.Errorf("System()() = %+v, want {State:%v Identities:1}: the probe must read the agent "+
			"answering at CALL time, through Detect", got, StateKeys)
	}

	argv, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("the stub ssh-add did not run: %v", err)
	}
	if s := strings.TrimSpace(string(argv)); s != "-l" {
		t.Errorf("ssh-add was run with %q, want \"-l\": a bare ssh-add LOADS keys and may prompt "+
			"for a passphrase", s)
	}
}
