package sshagent

import (
	"errors"
	"runtime"
	"strings"
	"testing"
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
func TestFixCommand(t *testing.T) {
	darwin := FixCommand("darwin")
	if !strings.Contains(darwin, "--apple-use-keychain") {
		t.Errorf("darwin fix = %q, must use --apple-use-keychain (keys live in the login keychain)", darwin)
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

// SystemExec must run the real ssh-add without panicking and classify into one
// of the three states whatever the machine's agent looks like. It asserts no
// particular state — CI may have keys, an empty agent, or none — only that the
// wiring produces a usable Result rather than crashing.
func TestSystemExecReturnsAUsableResult(t *testing.T) {
	got := Detect(SystemExec())
	switch got.State {
	case StateKeys, StateEmpty, StateUnreachable:
		// any of the three is fine; the point is it ran.
	default:
		t.Errorf("SystemExec produced an unknown state %v on %s", got.State, runtime.GOOS)
	}
}
