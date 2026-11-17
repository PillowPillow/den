package sbx

import (
	"context"
	"encoding/json"
	"fmt"
)

// Rule is one row of `sbx policy ls --json` (measured on sbx v0.53.0). Scope is "global" for a
// rule of the global policy (`local-policy`), which applies to every VM, and "sandbox:<name>"
// for a rule only one sandbox gets. sbx names its own rules `default-<topic>`; a rule added with
// `sbx policy allow network` gets a UUID instead. Provenance does not tell them apart: both
// carry `created_via: added`.
type Rule struct {
	ID           string   `json:"id"`
	Scope        string   `json:"scope"`
	ResourceType string   `json:"resource_type"`
	Decision     string   `json:"decision"`
	Status       string   `json:"status"`
	Resources    []string `json:"resources"`
}

// PolicyRules lists every active policy rule.
func PolicyRules(ctx context.Context, r Runner) ([]Rule, error) {
	out, err := r.Run(ctx, "policy", "ls", "--json")
	if err != nil {
		return nil, err
	}
	var l struct {
		Rules []Rule `json:"rules"`
	}
	if err := json.Unmarshal(out, &l); err != nil {
		return nil, fmt.Errorf("reading `sbx policy ls --json`: %w", err)
	}
	return l.Rules, nil
}

// Forwarding is how sbx hands a host SSH agent to every VM. An empty Socket means sbx forwards
// the agent of the process that calls it (its SSH_AUTH_SOCK).
type Forwarding struct {
	Enabled bool
	Socket  string
}

// SSHForwarding reads ssh.agentForwardingEnabled and ssh.agentSocketPath.
func SSHForwarding(ctx context.Context, r Runner) (Forwarding, error) {
	out, err := r.Run(ctx, "settings", "ls", "--json")
	if err != nil {
		return Forwarding{}, err
	}
	var settings []struct {
		Key   string          `json:"key"`
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(out, &settings); err != nil {
		return Forwarding{}, fmt.Errorf("reading `sbx settings ls --json`: %w", err)
	}
	var f Forwarding
	found := 0
	for _, s := range settings {
		var dst any
		switch s.Key {
		case "ssh.agentForwardingEnabled":
			dst = &f.Enabled
		case "ssh.agentSocketPath":
			dst = &f.Socket
		default:
			continue
		}
		if err := json.Unmarshal(s.Value, dst); err != nil {
			return Forwarding{}, fmt.Errorf("reading %s in `sbx settings ls --json`: %w", s.Key, err)
		}
		found++
	}
	if found != 2 {
		return Forwarding{}, fmt.Errorf("`sbx settings ls --json` lacks ssh.agentForwardingEnabled or ssh.agentSocketPath")
	}
	return f, nil
}
