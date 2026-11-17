// Package sshagent asks a host SSH agent whether it holds a key.
package sshagent

import (
	"context"
	"errors"
	"os"
	"os/exec"
)

// State is what an SSH agent answers to `ssh-add -l`.
type State int

const (
	Keys    State = iota // the agent holds at least one key
	Empty                // the agent answers and holds no key
	NoAgent              // no agent answers at the socket
	Unknown              // ssh-add is missing, timed out or answered something else
)

// Probe answers the State of the agent at socket, or at the caller's SSH_AUTH_SOCK when socket
// is empty.
type Probe func(ctx context.Context, socket string) State

// Check runs `ssh-add -l` and reads only its exit status. Its output names each key, and the
// comment of a key often holds an email address: den never reads it.
func Check(ctx context.Context, socket string) State {
	cmd := exec.CommandContext(ctx, "ssh-add", "-l")
	if socket != "" {
		cmd.Env = append(os.Environ(), "SSH_AUTH_SOCK="+socket)
	}
	err := cmd.Run()
	if err == nil {
		return Keys
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return stateOf(exit.ExitCode())
	}
	return Unknown
}

// stateOf maps the exit status of `ssh-add -l` (man ssh-add, EXIT STATUS): 1 when the agent
// holds no key, 2 when ssh-add cannot contact an agent.
func stateOf(code int) State {
	switch code {
	case 0:
		return Keys
	case 1:
		return Empty
	case 2:
		return NoAgent
	}
	return Unknown
}
