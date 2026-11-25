// Package sbxenv generates the files den hands to `sbx env`: the sbxenv.yaml stack and the
// two kits it composes. Everything here is pure; nothing touches the disk or runs sbx.
package sbxenv

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	AgentName      = "den-claude"
	GitMountRoot   = "/den-git"
	NetworkKitName = "den-network"
	// SessionsVMPath is where an instance's sessions folder lands in the VM. It is not Claude's own
	// ~/.claude/projects: a mount there makes sbx create a missing ~/.claude owned by root, which Claude
	// then cannot write (measured on sbx 0.54.1 with an image that ships no ~/.claude). The den-claude
	// startup step links ~/.claude/projects to this path, and names it too: keep both in step.
	SessionsVMPath = "/den-sessions"
)

type Mount struct {
	Path, Target string
	ReadOnly     bool
}

type Port struct{ Sandbox, Host int }

type Env struct {
	Name      string
	Kits      []string // absolute dirs or verbatim refs, in order
	Workspace string
	Mounts    []Mount
	Env       map[string]string
	Ports     []Port
	Template  string
	CPUs      int    // 0: not written, sbx's default
	Memory    string // "": not written, sbx's default
}

// scalar renders s as a YAML scalar, quoted only when YAML needs it.
func scalar(s string) string {
	out, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Sprintf("%q", s)
	}
	return strings.TrimSuffix(string(out), "\n")
}

// Render is deterministic: env keys are sorted, every list keeps the order given.
func Render(e Env) ([]byte, error) {
	if e.Name == "" || e.Workspace == "" {
		return nil, fmt.Errorf("sbxenv: name and workspace are required")
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "schemaVersion: \"1\"\nname: %s\nagent: %s\n", scalar(e.Name), AgentName)
	if len(e.Kits) > 0 {
		b.WriteString("kits:\n")
		for _, k := range e.Kits {
			fmt.Fprintf(&b, "  - %s\n", scalar(k))
		}
	}
	fmt.Fprintf(&b, "workspace: %s\n", scalar(e.Workspace))
	if len(e.Mounts) > 0 {
		b.WriteString("additionalWorkspaces:\n")
		for _, m := range e.Mounts {
			fmt.Fprintf(&b, "  - path: %s\n", scalar(m.Path))
			if m.Target != "" {
				fmt.Fprintf(&b, "    target: %s\n", scalar(m.Target))
			}
			if m.ReadOnly {
				b.WriteString("    readOnly: true\n")
			}
		}
	}
	if len(e.Env) > 0 {
		keys := make([]string, 0, len(e.Env))
		for k := range e.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("env:\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "  %s: %s\n", scalar(k), scalar(e.Env[k]))
		}
	}
	if len(e.Ports) > 0 {
		b.WriteString("ports:\n")
		for _, p := range e.Ports {
			fmt.Fprintf(&b, "  - sandbox: %d\n", p.Sandbox)
			if p.Host != 0 {
				fmt.Fprintf(&b, "    host: %d\n", p.Host)
			}
		}
	}
	if e.Template != "" || e.CPUs > 0 || e.Memory != "" {
		b.WriteString("sandboxOptions:\n")
		if e.Template != "" {
			fmt.Fprintf(&b, "  template: %s\n", scalar(e.Template))
		}
		if e.CPUs > 0 {
			fmt.Fprintf(&b, "  cpus: %d\n", e.CPUs)
		}
		if e.Memory != "" {
			fmt.Fprintf(&b, "  memory: %s\n", scalar(e.Memory))
		}
	}
	return b.Bytes(), nil
}
