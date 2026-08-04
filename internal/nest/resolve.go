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
	// Repos are the repositories given as positionals on the command line, raw:
	// tilde unexpanded, possibly relative. They are additive to the nest's
	// `repos:`, and they are NOT addressable by --without/--only — a repo typed
	// by hand is removed by not typing it.
	Repos []string
	// Cwd resolves the relative entries of Repos. A parameter, not an
	// os.Getwd() inside this package: the resolution stays pure, so `den
	// scratch .` is assertable without a test having to chdir, and the one
	// system call lives with the other world access in internal/spawn.
	Cwd string
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

	repos, err := selectRepos(n.Repos, o.Without, o.Only)
	if err != nil {
		return nil, fmt.Errorf("nest %q: %w", n.Name, err)
	}
	adhoc, err := parseRepoArgs(o.Cwd, o.Repos)
	if err != nil {
		return nil, fmt.Errorf("nest %q: %w", n.Name, err)
	}
	// Positionals FIRST, declared repos after: internal/spawn turns this list
	// into `sbx create`'s workspaces in order, and sbx.Sandbox.Workdir — the
	// directory the attached shell starts in — is Workspaces[0]. The gesture
	// "I am mounting X on the fly" means "I have come to work in X".
	//
	// This is the merge point of the whole feature, and the reason nothing
	// downstream needs a branch: from here on a repo given on the command line
	// IS a repo. It gets a worktree under -w, its common git dir mounted, its
	// place in the argv — by construction, not by repetition.
	repos = append(adhoc, repos...)
	// Re-checked on the MERGED list: LoadNest only ever saw the file. A
	// positional colliding with a declared basename makes --without, the
	// worktree path and the sbx positional ambiguous at once.
	if err := checkUniqueNames(repos, "spawn"); err != nil {
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
