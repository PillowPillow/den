package instance

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/sshagent"
)

func globalAllow(id string, resources ...string) sbx.Rule {
	return sbx.Rule{ID: id, Scope: "global", ResourceType: "network", Decision: "allow", Status: "active", Resources: resources}
}

func TestPolicyWarningsNameEachGlobalRuleAddedByHand(t *testing.T) {
	inactive := globalAllow("c3", "inactive.example.com:443")
	inactive.Status = "inactive"
	deny := globalAllow("d4", "deny.example.com")
	deny.Decision = "deny"
	scoped := globalAllow("e5", "kit.example.com")
	scoped.Scope = "sandbox:am"
	files := globalAllow("f6", "/tmp")
	files.ResourceType = "filesystem"
	got := policyWarnings([]sbx.Rule{
		globalAllow("default-ai-services", "api.example.com:443"),
		globalAllow("a1", "*.example.com"),
		globalAllow("b2", "cdn.example.com:443", "b.example.com"),
		inactive, deny, scoped, files,
	})
	want := []string{
		"warning: the global sbx rule a1 opens *.example.com (every port) to every VM — " +
			"remove it with `sbx policy rm network --id a1`, and allow the host in the nest that needs it",
		"warning: the global sbx rule b2 opens cdn.example.com:443, b.example.com (every port) to every VM — " +
			"remove it with `sbx policy rm network --id b2`, and allow the host in the nest that needs it",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

func TestPolicyWarningsFlagARuleThatAllowsEveryHost(t *testing.T) {
	got := policyWarnings([]sbx.Rule{
		globalAllow("default-allow-all", "**"),
		globalAllow("a1", "*:443", "x.example.com"),
	})
	want := []string{
		"warning: the global sbx rule default-allow-all allows every host to every VM, so no den network setting restricts anything — " +
			"run `sbx policy reset` (it stops the running VMs), then `sbx policy init balanced`",
		"warning: the global sbx rule a1 allows every host to every VM, so no den network setting restricts anything — " +
			"remove it with `sbx policy rm network --id a1`",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

const handRuleJSON = `{"rules":[{"id":"a1","scope":"global","resource_type":"network","decision":"allow","status":"active","resources":["*.example.com"]}]}`

const forwardingOn = `[{"key":"ssh.agentForwardingEnabled","value":true},{"key":"ssh.agentSocketPath","value":""}]`

func imageNest(t *testing.T, f *fixture) {
	t.Helper()
	f.project(t, map[string]string{"am": "template: claude-code-docker\n" + repos(repoEntry(f.repo(t, "api"), false))})
}

// upDetached runs `den up am --detach` with probe as the SSH agent and returns stderr.
func upDetached(t *testing.T, f *fixture, probe sshagent.Probe) string {
	t.Helper()
	var stderr strings.Builder
	d := f.deps()
	d.Err = &stderr
	d.Agent = probe
	if err := Up(context.Background(), d, Options{Nest: "am"}, true); err != nil {
		t.Fatal(err)
	}
	return stderr.String()
}

func agentAt(state sshagent.State, sockets *[]string) sshagent.Probe {
	return func(_ context.Context, socket string) sshagent.State {
		if sockets != nil {
			*sockets = append(*sockets, socket)
		}
		return state
	}
}

func TestUpPrintsTheAlertsWhenItCreatesTheVM(t *testing.T) {
	f := newFixture(t)
	imageNest(t, f)
	f.respond("policy ls --json", handRuleJSON)
	f.respond("settings ls --json", forwardingOn)
	stderr := upDetached(t, f, agentAt(sshagent.Empty, nil))
	for _, want := range []string{
		"warning: the global sbx rule a1 opens *.example.com (every port) to every VM",
		"note: your SSH agent holds no key, so ssh and git over SSH fail in am — see docs/ssh.md\n",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr %q lacks %q", stderr, want)
		}
	}
}

func TestUpNamesTheSocketNoAgentAnswersAt(t *testing.T) {
	f := newFixture(t)
	imageNest(t, f)
	f.respond("settings ls --json", `[{"key":"ssh.agentForwardingEnabled","value":true},{"key":"ssh.agentSocketPath","value":"/run/agent.sock"}]`)
	var sockets []string
	stderr := upDetached(t, f, agentAt(sshagent.NoAgent, &sockets))
	if !reflect.DeepEqual(sockets, []string{"/run/agent.sock"}) {
		t.Errorf("probed %q, want the socket of ssh.agentSocketPath", sockets)
	}
	if want := "note: no SSH agent answers at /run/agent.sock, so ssh and git over SSH fail in am — see docs/ssh.md\n"; !strings.Contains(stderr, want) {
		t.Errorf("stderr %q lacks %q", stderr, want)
	}
}

func TestUpNamesSSHAuthSockWhenNoAgentAnswers(t *testing.T) {
	f := newFixture(t)
	imageNest(t, f)
	f.respond("settings ls --json", forwardingOn)
	stderr := upDetached(t, f, agentAt(sshagent.NoAgent, nil))
	if want := "note: no SSH agent answers at $SSH_AUTH_SOCK, so ssh and git over SSH fail in am — see docs/ssh.md\n"; !strings.Contains(stderr, want) {
		t.Errorf("stderr %q lacks %q", stderr, want)
	}
}

func TestUpSaysNothingOfAnAgentThatHoldsAKey(t *testing.T) {
	f := newFixture(t)
	imageNest(t, f)
	f.respond("settings ls --json", forwardingOn)
	if stderr := upDetached(t, f, agentAt(sshagent.Keys, nil)); strings.Contains(stderr, "SSH") {
		t.Errorf("stderr %q", stderr)
	}
}

func TestUpSkipsTheAgentWhenForwardingIsOff(t *testing.T) {
	f := newFixture(t)
	imageNest(t, f)
	f.respond("settings ls --json", `[{"key":"ssh.agentForwardingEnabled","value":false},{"key":"ssh.agentSocketPath","value":""}]`)
	var sockets []string
	if stderr := upDetached(t, f, agentAt(sshagent.Empty, &sockets)); strings.Contains(stderr, "SSH") || len(sockets) != 0 {
		t.Errorf("stderr %q, probed %q", stderr, sockets)
	}
}

func TestUpStaysSilentWhenSbxCannotAnswer(t *testing.T) {
	f := newFixture(t)
	imageNest(t, f)
	f.fake.Responses["policy ls --json"] = sbx.Response{Err: errors.New("daemon down")}
	f.fake.Responses["settings ls --json"] = sbx.Response{Err: errors.New("daemon down")}
	var sockets []string
	if stderr := upDetached(t, f, agentAt(sshagent.Empty, &sockets)); stderr != "" || len(sockets) != 0 {
		t.Errorf("stderr %q, probed %q", stderr, sockets)
	}
}

func TestUpOnALiveInstancePrintsNoAlert(t *testing.T) {
	f := newFixture(t)
	imageNest(t, f)
	mustCreate(t, f, mustPrepare(t, f, Options{Nest: "am"}))
	f.live("am")
	f.respond("policy ls --json", handRuleJSON)
	f.respond("settings ls --json", forwardingOn)
	f.fake.Calls = nil
	if stderr := upDetached(t, f, agentAt(sshagent.Empty, nil)); stderr != "" {
		t.Errorf("stderr %q", stderr)
	}
	if f.fake.HasCalled("policy", "ls", "--json") || f.fake.HasCalled("settings", "ls", "--json") {
		t.Errorf("a live instance read the machine: %v", f.fake.Calls)
	}
}
