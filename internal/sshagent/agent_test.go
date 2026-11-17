package sshagent

import "testing"

func TestStateOfReadsTheExitStatusOfSshAddL(t *testing.T) {
	for code, want := range map[int]State{0: Keys, 1: Empty, 2: NoAgent, 127: Unknown, -1: Unknown} {
		if got := stateOf(code); got != want {
			t.Errorf("exit %d: got %v, want %v", code, got, want)
		}
	}
}

func TestCheckReportsAnUnsetSSHAuthSock(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	if got, want := Check(t.Context(), ""), (Result{State: Unset}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
