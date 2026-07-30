// Package sshagent reports the state of the SSH agent reachable through
// SSH_AUTH_SOCK, so den can warn — at spawn and in `den doctor` — when
// `ssh.mode: agent-forward` would forward an agent that holds no key.
//
// The blind spot it closes: doctor already sees an ABSENT SSH_AUTH_SOCK, but
// not a socket that is present while the agent behind it is empty or dead. A
// sandbox then inherits a live-but-keyless agent and every `git push` dies on
// `Permission denied (publickey)`, with no ~/.ssh on disk to fall back to
// (that would be `ssh.mode: mount`).
package sshagent

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
)

// State is what the agent behind SSH_AUTH_SOCK looks like right now. Three
// states, not two: "reachable with keys" and "unreachable" don't cover the
// case this package exists for — a reachable agent holding NO identity, which
// forwards silently and fails only inside the VM.
type State int

const (
	// StateKeys: the agent answered and holds at least one identity. The only
	// healthy case; Result.Identities carries the count.
	StateKeys State = iota
	// StateEmpty: the agent answered but holds no identity. Forwards a live,
	// keyless agent — the exact silent failure this package warns about.
	StateEmpty
	// StateUnreachable: nothing to talk to — a dead socket, no agent running,
	// or `ssh-add` itself absent from PATH.
	StateUnreachable
)

// Result is a State plus, for StateKeys only, the number of identities the
// agent reported. Identities is 0 for the other two states.
type Result struct {
	State      State
	Identities int
}

// Exec runs `ssh-add -l` (inheriting den's environment, so SSH_AUTH_SOCK is
// carried through) and returns its stdout and exit code. err is non-nil ONLY
// when the process could not be run to completion — `ssh-add` missing from
// PATH, most concretely; a plain non-zero exit is reported through code, not
// err, because it is exactly how OpenSSH tells the three states apart.
//
// Injectable for the same reason as doctor.Deps: the three states must be
// reproducible in a test without a real agent on the machine running it.
type Exec func() (stdout string, exitCode int, err error)

// Detect maps `ssh-add -l`'s outcome onto a State, using the exit codes
// OpenSSH standardizes identically across macOS, Linux and WSL:
//
//	0            → at least one identity  (StateKeys)
//	1            → agent reachable, empty (StateEmpty)
//	2, or an     → no usable agent        (StateUnreachable)
//	exec failure
func Detect(run Exec) Result {
	stdout, code, err := run()
	if err != nil {
		// Couldn't even run ssh-add (not on PATH, say): there is no agent
		// answer to interpret, so it reads as unreachable.
		return Result{State: StateUnreachable}
	}
	switch code {
	case 0:
		return Result{State: StateKeys, Identities: countIdentities(stdout)}
	case 1:
		return Result{State: StateEmpty}
	default:
		// 2 is "could not connect"; anything else non-zero is no better an
		// agent. Both fold into unreachable rather than being invented into a
		// fourth state nobody could act on differently.
		return Result{State: StateUnreachable}
	}
}

// countIdentities counts the non-empty lines of `ssh-add -l`, which prints one
// per identity. Blank lines are skipped so a trailing newline never inflates
// the count by one.
func countIdentities(stdout string) int {
	n := 0
	for _, line := range strings.Split(stdout, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// SystemExec is the real Exec: it runs `ssh-add -l` with the inherited
// environment (cmd.Env left nil), so SSH_AUTH_SOCK reaches the child exactly
// as the sbx process would inherit it.
func SystemExec() Exec {
	return func() (string, int, error) {
		var out bytes.Buffer
		cmd := exec.Command("ssh-add", "-l")
		cmd.Stdout = &out
		// stderr is discarded: on an empty or dead agent ssh-add writes its
		// human message there, but the exit code already carries the verdict.
		err := cmd.Run()
		if err == nil {
			return out.String(), 0, nil
		}
		// A non-zero exit is not a run failure: it is the signal. Report its
		// code with err=nil so Detect reads the state, and reserve the real
		// err (ssh-add missing from PATH) for the unreachable path.
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return out.String(), exit.ExitCode(), nil
		}
		return "", 0, err
	}
}

// FixCommand returns the command the user should run, on this OS, to load
// identities into the running agent.
//
// macOS keeps keys in the login keychain and needs the flag that reads them;
// everywhere else (Linux, WSL) a bare `ssh-add` loads the default ~/.ssh keys.
// goos is a parameter, not a direct runtime.GOOS read, so each branch is
// assertable in a test regardless of where it runs.
func FixCommand(goos string) string {
	if goos == "darwin" {
		return "ssh-add --apple-use-keychain ~/.ssh/"
	}
	return "ssh-add"
}
