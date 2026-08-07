package config

import (
	"errors"
	"fmt"
	"io/fs"
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

// Mount is one entry of `mounts:` — a host directory made available inside the
// microVM, and optionally linked to a path the VM's tools actually read.
//
// The two path fields belong to DIFFERENT MACHINES, and conflating them is the
// bug this type exists to prevent:
//
//   - Host is a HOST path. den expands it (ExpandPath, like `repos:` and
//     `ssh.dir`) and hands it to `sbx create`, which mounts it at that SAME
//     absolute path inside the VM (spec A11 — sbx takes no mount-target flag,
//     probed 2026-08-07, so den cannot choose where it lands).
//   - Link is a VM path. den NEVER expands it: `$HOME` is /Users/<me> on the
//     host and /home/agent in the VM. It is emitted verbatim into the startup
//     shell and expanded there. Same reasoning as bin_dirs in
//     internal/agent/freshness.go.
//
// Link empty is legitimate, not a degenerate case: it is right whenever the
// consuming tool can be pointed at the host path by an environment variable,
// which is how the agent's own config dir works (CLAUDE_CONFIG_DIR).
type Mount struct {
	Host string `yaml:"host"`
	Link string `yaml:"link"`
	RO   bool   `yaml:"ro"`
}

// SSHLinkTarget is where `ssh.mode: mount` links ssh.dir.
//
// `$HOME` and not an absolute path: it is expanded by the VM's bash, whose
// $HOME is /home/agent. Writing /home/agent here would hard-code the microVM's
// user into den. See Mount above for the full rule.
//
// It lives HERE and not in internal/nest, where the sugar is applied, because
// Validate must compare a user's `mounts[].link` against it: the sugar and a
// hand-written `link: $HOME/.ssh` collide on the same VM path, and only the
// package that owns config.yaml's validation can refuse that. nest imports
// config, so the sugar reads the same constant — one spelling, one concept.
const SSHLinkTarget = "$HOME/.ssh"

// Global is the content of ~/.den/config.yaml, with defaults applied and paths expanded.
type Global struct {
	Agents         map[string]Agent `yaml:"agents"`
	Defaults       Defaults         `yaml:"defaults"`
	SSH            SSH              `yaml:"ssh"`
	WorktreeLayout string           `yaml:"worktree_layout"`
	WorktreeRoot   string           `yaml:"worktree_root"`
	Egress         []string         `yaml:"egress"`
	// Repos maps a repo KEY (used by team nests via `key:`, spec 2026-08-04
	// §2.4) to a path on THIS machine. Personal by design: it is the one part
	// of a shared nest that cannot travel.
	Repos map[string]string `yaml:"repos"`
	// Mounts is GLOBAL and deliberately not part of the stack/nest cascade.
	// A `host:` is a path on THIS machine, and den already refuses `path:` on a
	// nest that comes from a source for exactly that reason — a stack in a
	// shared source declaring one would reintroduce what `den lint` exists to
	// refuse. Per-stack mounts would need the same key indirection as `repos:`,
	// which is a separate design.
	Mounts []Mount `yaml:"mounts"`
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
		wrapped := fmt.Errorf("reading %s: %w", path, &FileError{Err: err})

		// The remedy is added HERE, in the one wrap every LoadGlobal caller and
		// doctor.go's config.yaml check inherit (paths.go's "sole definition"
		// doctrine) — not at each call site, where a future caller could add a
		// new read path and simply forget it.
		//
		// Gated tightly on fs.ErrNotExist, checked against the raw error (a
		// *fs.PathError, which errors.Is unwraps on its own): a config.yaml
		// that EXISTS but fails to read — wrong permissions, or a directory
		// sitting where the file should be — must not point at `den init`,
		// because deninit.Run refuses outright whenever config.yaml already
		// exists (see internal/deninit/deninit.go). Suggesting a command that
		// is guaranteed to refuse is worse than naming no remedy at all: it
		// reads as "run this to fix it" when the honest answer is "this file
		// is broken, fix it yourself".
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w — run `den init` to create one", wrapped)
		}
		return nil, wrapped
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
	for i := range g.Mounts {
		// Trim before expanding: a stray space in the YAML would otherwise survive
		// into `sbx create`'s argv (Host) or into the VM's startup shell (Link),
		// where a leading space breaks the `$HOME/` prefix the link phase relies on.
		// Trimmed at LOAD so exactly one value exists — validation and every later
		// consumer read the same string, and no downstream copy can diverge.
		g.Mounts[i].Host = strings.TrimSpace(g.Mounts[i].Host)
		g.Mounts[i].Link = strings.TrimSpace(g.Mounts[i].Link)
		// A trailing slash makes `ln` resolve THROUGH an existing correct symlink
		// instead of replacing it, so the link phase refuses on every boot after
		// the first. `$HOME/.ssh/` and `$HOME/.ssh` denote the same VM path, so
		// stripping is lossless. Normalised at LOAD, beside the trim above, so one
		// canonical value reaches validation, the mixin and the emitted shell
		// alike — not re-derived at each consumer.
		//
		// The RESULT is what is guarded, never the input length: `len(l) > 1`
		// let "//" through and TrimRight reduced it to "", which Validate then
		// reads as "no link asked for" (an empty link is legitimate, see the
		// Mount doc above). den would mount the directory, silently link
		// nothing, and report success — the exact silent wrong-path failure the
		// link phase exists to remove. An all-slashes link denotes the VM's
		// root, so "/" is what it collapses to.
		if l := g.Mounts[i].Link; strings.HasSuffix(l, "/") {
			if trimmed := strings.TrimRight(l, "/"); trimmed != "" {
				g.Mounts[i].Link = trimmed
			} else {
				g.Mounts[i].Link = "/"
			}
		}
		// Host only. Link is a VM path — see the Mount doc comment.
		if g.Mounts[i].Host, err = ExpandPath(g.Mounts[i].Host); err != nil {
			return nil, fmt.Errorf("mounts[%d].host: %w", i, err)
		}
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
	for key, p := range g.Repos {
		expanded, err := ExpandPath(p)
		if err != nil {
			return nil, fmt.Errorf("repos.%s: %w", key, err)
		}
		g.Repos[key] = expanded
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
