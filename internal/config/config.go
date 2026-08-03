package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Agent describes an entry in the agent registry (spec §4.1 and §9).
type Agent struct {
	ConfigDir string            `yaml:"config_dir"`
	Env       map[string]string `yaml:"env"`
	// BinDirs: IN-VM paths added to PATH before the freshness command (spec §9.1).
	// Never expanded on the host side.
	BinDirs []string `yaml:"bin_dirs"`
	// Update: the agent's update command, run at VM boot.
	Update string `yaml:"update"`
}

// Defaults carries the default choices used when neither the nest nor the flags decide.
type Defaults struct {
	Agent string `yaml:"agent"`
	Stack string `yaml:"stack"`
}

// SSH describes the SSH access mode inside the VM (spec §10).
type SSH struct {
	Mode string `yaml:"mode"` // agent-forward | mount | none
	Dir  string `yaml:"dir"`  // used when mode=mount
}

// Global is the content of ~/.den/config.yaml, with defaults applied and paths expanded.
type Global struct {
	Agents         map[string]Agent `yaml:"agents"`
	Defaults       Defaults         `yaml:"defaults"`
	SSH            SSH              `yaml:"ssh"`
	WorktreeLayout string           `yaml:"worktree_layout"`
	WorktreeRoot   string           `yaml:"worktree_root"`
	Egress         []string         `yaml:"egress"`
}

// LoadGlobalUnvalidated reads <denHome>/config.yaml, applies defaults and
// expands host paths, WITHOUT checking the result's consistency.
//
// Reserved for `den doctor`, which needs to accumulate and display ALL
// inconsistencies at once (doctor.go): loading through LoadGlobal would stop
// at the first load error and never reach its own validation. Every other
// caller must go through LoadGlobal instead — validation is not optional on
// the path that builds a microVM.
func LoadGlobalUnvalidated(denHome string) (*Global, error) {
	path := GlobalPath(denHome)
	raw, err := os.ReadFile(path)
	if err != nil {
		// FileError, not the raw error: the raw one is a *fs.PathError that
		// repeats the path we just named. %w stays mandatory — the chain must
		// survive.
		return nil, fmt.Errorf("reading %s: %w", path, &FileError{Err: err})
	}

	var g Global
	if err := DecodeYAMLStrict(path, raw, &g); err != nil {
		return nil, err
	}

	if g.SSH.Mode == "" {
		g.SSH.Mode = "agent-forward"
	}
	if g.WorktreeLayout == "" {
		g.WorktreeLayout = "central"
	}
	if g.WorktreeRoot == "" {
		g.WorktreeRoot = filepath.Join(denHome, "worktrees")
	}

	if g.WorktreeRoot, err = ExpandPath(g.WorktreeRoot); err != nil {
		return nil, err
	}
	if g.SSH.Dir, err = ExpandPath(g.SSH.Dir); err != nil {
		return nil, err
	}
	for name, a := range g.Agents {
		// Same shape as worktree_root above: defaulted here, against the LIVE
		// denHome, rather than baked into the example file at `den init` time.
		// Substituting the resolved home into config_dir when the file is
		// written would freeze it — `den init --den-home /tmp/foo` would write
		// `/tmp/foo/agents/claude` into config.yaml, and a later move of that
		// home (or a changed DEN_HOME) would leave the agent profile pointing
		// at the old, now-stale location, silently. A default recomputed on
		// every load instead tracks whichever home is live.
		//
		// `== ""` exactly, not TrimSpace: this is what lets `config_dir: "   "`
		// reach Validate() and be refused there, instead of being silently
		// swapped for the default.
		if a.ConfigDir == "" {
			a.ConfigDir = filepath.Join(denHome, "agents", name)
		}
		if a.ConfigDir, err = ExpandPath(a.ConfigDir); err != nil {
			return nil, fmt.Errorf("agent %s: %w", name, err)
		}
		g.Agents[name] = a // map values are not addressable
	}
	return &g, nil
}

// LoadGlobal loads <denHome>/config.yaml and REJECTS an inconsistent configuration.
func LoadGlobal(denHome string) (*Global, error) {
	g, err := LoadGlobalUnvalidated(denHome)
	if err != nil {
		return nil, err
	}
	if errs := g.Validate(); len(errs) > 0 {
		return nil, ConfigError(denHome, errs)
	}
	return g, nil
}

// ConfigError assembles a list of validation errors into a single error naming
// the file to fix.
//
// All faults, never just the first: shared between LoadGlobal and commands
// that validate only PART of the config (internal/cli/rm.go), so the user
// always reads the same shape of message.
func ConfigError(denHome string, errs []error) error {
	var b strings.Builder
	fmt.Fprintf(&b, "invalid configuration in %s:", GlobalPath(denHome))
	for _, e := range errs {
		fmt.Fprintf(&b, "\n  - %v", e)
	}
	return errors.New(b.String())
}
