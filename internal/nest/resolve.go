package nest

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/PillowPillow/den/internal/config"
)

// Options carries the overrides coming from CLI flags (the cascade's last level).
type Options struct {
	Agent   string   // --agent
	Without []string // --without
	Only    []string // --only
}

// configDirToken is the marker substituted in the agent's env values.
//
// What it holds is a FACT: the HOST path of the agent's profile, the one den
// creates and that `sbx create` receives as a workspace.
//
// That this is useful INSIDE the VM is an unverified HYPOTHESIS — spec §14.1
// A11: that sbx mounts each workspace at the same absolute path in the VM as
// on the host. Project convention: anything about sbx's actual behavior is a
// hypothesis to document, never an assertion. Three real smokes have run
// against sbx since (the last on 2026-08-03, v0.35.0) and none aimed at this
// surface: A11 is open for want of a measurement, not for want of a binary.
//
// If it is false, CLAUDE_CONFIG_DIR points nowhere inside the VM and the
// agent starts from scratch on every spawn, silently — see A11 for what would
// falsify it at the first smoke test, and for what else depends on it (the
// `-w <host path>` of every attach).
const configDirToken = "{config_dir}"

// Resolved is a fully resolved nest: nothing left to compute downstream.
// internal/spawn consumes it as-is to build the mixin and the sbx create argv.
type Resolved struct {
	// DenHome is ALWAYS absolute (Resolve guarantees it): the generated mixin
	// is written under <DenHome>/cache/mixins/, and that path then goes as-is
	// to `sbx create`, where cwd is no longer guaranteed.
	DenHome string

	Nest  *Nest
	Stack *config.Stack

	AgentName      string
	Agent          config.Agent
	AgentConfigDir string // nest override if present, else the global registry

	// Env is the union READY TO APPLY: agent env ∪ nest env, nest winning,
	// {config_dir} substituted EVERYWHERE (both agent and nest). The
	// substitution is a cascade rule, not a display concern: it belongs here,
	// not in the mixin.
	Env map[string]string

	Egress []string // sorted union of baseline ∪ stack ∪ nest
	Repos  []Repo   // applied selection, declaration order

	SSHMode        string
	SSHDir         string
	WorktreeLayout string
	WorktreeRoot   string
}

// mergeEnv applies the agent ← nest cascade and substitutes {config_dir} in
// BOTH sources: a nest may reassert an agent variable (e.g. CLAUDE_CONFIG_DIR)
// with the same token, and since the nest wins the cascade, an asymmetric
// substitution would let the literal token win instead.
// Always returns a non-nil map: consumers iterate without a guard.
func mergeEnv(agentEnv, nestEnv map[string]string, configDir string) map[string]string {
	out := make(map[string]string, len(agentEnv)+len(nestEnv))
	for k, v := range agentEnv {
		out[k] = strings.ReplaceAll(v, configDirToken, configDir)
	}
	for k, v := range nestEnv {
		out[k] = strings.ReplaceAll(v, configDirToken, configDir) // the nest is lower in the cascade: it wins
	}
	return out
}

// resolveAgent determines the active agent and its config_dir.
// Name priority: --agent flag > defaults.agent.
// config_dir priority: nest override for THIS agent > global registry.
func resolveAgent(g *config.Global, n *Nest, flagAgent string) (string, config.Agent, string, error) {
	name := flagAgent
	if name == "" {
		name = g.Defaults.Agent
	}

	a, ok := g.Agents[name]
	if !ok {
		available := slices.Sorted(maps.Keys(g.Agents))
		return "", config.Agent{}, "", fmt.Errorf(
			"unknown agent %q (declared agents: %v)", name, available)
	}

	configDir := a.ConfigDir
	if n != nil {
		if override, ok := n.Agents[name]; ok && override != "" {
			configDir = override
		}
	}
	return name, a, configDir, nil
}

// resolveRepoKeys fills the Path of every key-typed repo from the personal
// mapping. Refusal BEFORE any side effect, naming the exact file and line to
// add — and the clone command when the nest declared one (spec 2026-08-04
// §2.4). denHome locates config.yaml through GlobalPath: the message and the
// reader must never disagree on where that file lives.
func resolveRepoKeys(denHome string, mapping map[string]string, repos []Repo) ([]Repo, error) {
	out := slices.Clone(repos)
	for i, r := range out {
		if r.Key == "" {
			continue
		}
		path, ok := mapping[r.Key]
		if !ok {
			hint := ""
			if r.URL != "" {
				hint = fmt.Sprintf(" (clone: %s)", r.URL)
			}
			return nil, fmt.Errorf(
				"repo key %q is not mapped on this machine — add `%s: <local path>` under `repos:` "+
					"in %s%s", r.Key, r.Key, config.GlobalPath(denHome), hint)
		}
		out[i].Path = path
	}
	return out, nil
}

// Resolve applies the full global ← stack ← nest ← flags cascade.
//
// stacks is a config.Stacks rather than a map: the verdict "is this stack
// usable" distinguishes "unreadable" from "not declared", and that
// distinction belongs to config.Stacks.Get, its single source of truth. A map
// alone could only say "not found" — including for a stack that IS present
// but whose stack.yaml has a typo, sending the user to create a file that
// already exists.
func Resolve(denHome string, g *config.Global, stacks config.Stacks, n *Nest, o Options) (*Resolved, error) {
	if !filepath.IsAbs(denHome) {
		return nil, fmt.Errorf(
			"den home %q: not an absolute path (derived paths go as-is to "+
				"git worktree and sbx create, where cwd is no longer guaranteed)", denHome)
	}

	stackName := n.Stack
	if stackName == "" {
		stackName = g.Defaults.Stack
	}
	s, err := stacks.Get(stackName)
	if err != nil {
		// Nothing is appended after Get's error: it already locates what
		// needs locating (the stacks directory for "not found", the faulty
		// stack.yaml for "unreadable"). A suffix here would land behind
		// yaml.v3's MULTI-LINE diagnostic, where it would read as the
		// location of its last line.
		return nil, fmt.Errorf("nest %q: %w", n.Name, err)
	}

	agentName, agent, configDir, err := resolveAgent(g, n, o.Agent)
	if err != nil {
		return nil, fmt.Errorf("nest %q: %w", n.Name, err)
	}

	repos, err := resolveRepoKeys(denHome, g.Repos, n.Repos)
	if err != nil {
		return nil, fmt.Errorf("nest %q: %w", n.Name, err)
	}
	repos, err = selectRepos(repos, o.Without, o.Only)
	if err != nil {
		return nil, fmt.Errorf("nest %q: %w", n.Name, err)
	}

	return &Resolved{
		DenHome:        denHome,
		Nest:           n,
		Stack:          s,
		AgentName:      agentName,
		Agent:          agent,
		AgentConfigDir: configDir,
		Env:            mergeEnv(agent.Env, n.Env, configDir),
		Egress:         unionEgress(g.Egress, s.Egress, n.Egress),
		Repos:          repos,
		SSHMode:        g.SSH.Mode,
		SSHDir:         g.SSH.Dir,
		WorktreeLayout: g.WorktreeLayout,
		WorktreeRoot:   g.WorktreeRoot,
	}, nil
}
