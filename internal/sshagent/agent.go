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
	Unset                // no socket given and SSH_AUTH_SOCK is empty
	Unknown              // ssh-add is missing, timed out or answered something else
)

// Result is the State of the agent at Socket, the socket the probe checked.
type Result struct {
	State  State
	Socket string
}

// Probe checks the agent at socket, or at the caller's SSH_AUTH_SOCK when socket is empty.
type Probe func(ctx context.Context, socket string) Result

// Check runs `ssh-add -l` and reads only its exit status. Its output names each key, and the
// comment of a key often holds an email address: den never reads it.
func Check(ctx context.Context, socket string) Result {
	if socket == "" {
		socket = os.Getenv("SSH_AUTH_SOCK")
	}
	if socket == "" {
		return Result{State: Unset}
	}
	cmd := exec.CommandContext(ctx, "ssh-add", "-l")
	cmd.Env = append(os.Environ(), "SSH_AUTH_SOCK="+socket)
	err := cmd.Run()
	if err == nil {
		return Result{Keys, socket}
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return Result{stateOf(exit.ExitCode()), socket}
	}
	return Result{Unknown, socket}
}

// stateOf maps the exit status of `ssh-add -l` (man ssh-add, EXIT STATUS): 1 when the listing
// fails, which an agent with no key causes, 2 when ssh-add cannot contact an agent.
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
