package instance

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/sshagent"
)

// alertTimeout bounds every read behind the alerts: they inform, so a slow sbx daemon or SSH
// agent must never hold `den up` back.
const alertTimeout = 3 * time.Second

// printAlerts reports what a new VM gets from the machine rather than from den: the hosts the
// global sbx policy opens to every VM, and an SSH agent with no key. Only a create prints them,
// so that attaching to a running VM stays quiet. An error from sbx or the agent prints nothing.
func printAlerts(ctx context.Context, d Deps, instance string) {
	ctx, cancel := context.WithTimeout(ctx, alertTimeout)
	defer cancel()
	if rules, err := sbx.PolicyRules(ctx, d.Sbx); err == nil {
		for _, w := range policyWarnings(rules) {
			fmt.Fprintln(d.Err, w)
		}
	}
	sshNote(ctx, d.Sbx, d.Agent, d.Err, instance)
}

// policyWarnings names each active global network rule that sbx did not create, and each one
// that allows every host. sbx names its own rules `default-<topic>`.
func policyWarnings(rules []sbx.Rule) []string {
	var out []string
	for _, r := range rules {
		if r.Scope != "global" || r.ResourceType != "network" || r.Decision != "allow" || r.Status != "active" {
			continue
		}
		mine := strings.HasPrefix(r.ID, "default-")
		if allowsEveryHost(r.Resources) {
			remedy := "remove it with `sbx policy rm network --id " + r.ID + "`"
			if mine {
				remedy = "run `sbx policy reset` (it stops the running VMs), then `sbx policy init balanced`"
			}
			out = append(out, fmt.Sprintf("warning: the global sbx rule %s allows every host to every VM, "+
				"so no den network setting restricts anything — %s", r.ID, remedy))
			continue
		}
		if mine {
			continue
		}
		hosts := make([]string, len(r.Resources))
		for i, h := range r.Resources {
			hosts[i] = h
			if _, _, err := net.SplitHostPort(h); err != nil {
				hosts[i] += " (every port)"
			}
		}
		out = append(out, fmt.Sprintf("warning: the global sbx rule %s opens %s to every VM — "+
			"remove it with `sbx policy rm network --id %s`, and allow the host in the nest that needs it",
			r.ID, strings.Join(hosts, ", "), r.ID))
	}
	return out
}

func allowsEveryHost(resources []string) bool {
	for _, h := range resources {
		if host, _, err := net.SplitHostPort(h); err == nil {
			h = host
		}
		if h == "*" || h == "**" {
			return true
		}
	}
	return false
}

// sshDoc explains what the VM gets from the SSH agent. A URL, not a path: the binary runs
// outside the den repository.
const sshDoc = "https://github.com/PillowPillow/den/blob/v2/docs/ssh.md"

// sshNote says when the VM may get no usable SSH agent while sbx forwards one. sbx forwards the
// agent at ssh.agentSocketPath, or else the SSH_AUTH_SOCK of the process that calls it: den's.
// "May": sbx can keep serving the agent an earlier sbx call forwarded (internal/sbx/runner.go).
// It is a note, not a warning: an empty agent is the safe state.
func sshNote(ctx context.Context, r sbx.Runner, probe sshagent.Probe, w io.Writer, instance string) {
	if probe == nil {
		return
	}
	fwd, err := sbx.SSHForwarding(ctx, r)
	if err != nil || !fwd.Enabled {
		return
	}
	res := probe(ctx, fwd.Socket)
	var why string
	switch res.State {
	case sshagent.Empty:
		why = "the SSH agent at " + res.Socket + " holds no key"
	case sshagent.NoAgent:
		why = "no SSH agent answers at " + res.Socket
	case sshagent.Unset:
		why = "SSH_AUTH_SOCK is not set"
	default:
		return
	}
	fmt.Fprintf(w, "note: %s, so ssh and git over SSH may fail in %s — see %s\n", why, instance, sshDoc)
}
