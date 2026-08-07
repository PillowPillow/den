package config

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
)

var (
	sshModes        = []string{"agent-forward", "mount", "none"}
	worktreeLayouts = []string{"central", "per-repo"}
)

// validateBinDir checks that a bin_dirs entry is really a PATH.
//
// agent.FreshnessCommand injects these literally into an `export PATH=%q`,
// deliberately unescaped: the `$HOME` they may contain targets the VM's home
// and must be expanded by the VM's bash, not by den (invariant 1 of
// FreshnessCommand). Escaping to protect against this would kill the one
// thing this field must be able to do.
//
// This isn't a security hole — it's the user's own config, and `update:` is
// arbitrary shell by contract — but a TYPING defect: bin_dirs is documented
// as a list of paths, so we refuse what isn't one.
func validateBinDir(d string) error {
	if d == "" {
		return fmt.Errorf("empty entry — an empty element in PATH adds the current directory")
	}
	if strings.Contains(d, "$(") {
		return fmt.Errorf(
			"%q contains a command substitution $( — bin_dirs is a list of paths, "+
				"and the VM's bash would execute it at startup", d)
	}
	if strings.Contains(d, "`") {
		return fmt.Errorf(
			"%q contains a backtick ` — bin_dirs is a list of paths, "+
				"and the VM's bash would execute it at startup", d)
	}
	// The tilde is NOT expanded inside double quotes: `export
	// PATH="~/.local/bin:$PATH"` puts a directory literally named "~" into
	// the VM's PATH. That's the `claude: exit 127` invariant 1 of
	// FreshnessCommand exists to prevent. Unquoted, bash would expand it, but
	// den doesn't generate that form and can't without losing protection for
	// spaces.
	//
	// Prefix only: a "~" in the MIDDLE of a path isn't an expansion for bash
	// either, it's an ordinary filename character.
	if strings.HasPrefix(d, "~") {
		return fmt.Errorf(
			"%q starts with ~ — bash doesn't expand tilde inside double quotes, "+
				"and the VM's PATH would receive a directory literally named \"~\"; "+
				"write $HOME instead, which does get expanded", d)
	}
	// `$HOME` and `${HOME}` remain legitimate: they're the whole reason this
	// field exists. Only COMMAND substitution is refused, not variable expansion.
	for _, r := range d {
		if unicode.IsControl(r) {
			return fmt.Errorf(
				"%q contains a control character (%q) — it would be escaped as a literal "+
					"in the VM's PATH, corrupting the path", d, r)
		}
	}
	return nil
}

// Validate checks the internal consistency of config.yaml and returns ALL the
// errors found. Accumulating rather than stopping at the first: `den doctor`
// must show everything there is to fix at once.
func (g *Global) Validate() []error {
	var errs []error

	if len(g.Agents) == 0 {
		errs = append(errs, fmt.Errorf("agents: the registry is empty, declare at least one agent"))
	}

	names := slices.Sorted(maps.Keys(g.Agents)) // deterministic error ordering

	for _, name := range names {
		a := g.Agents[name]
		// Not "required": LoadGlobalUnvalidated already defaults an ABSENT
		// config_dir (config.go), so this only fires on a value that was
		// explicitly written and is blank — same split as worktree_root
		// (ValidateWorktree below): the loader defaults the empty string, this
		// TrimSpace catches what the loader's `== ""` check let through.
		if strings.TrimSpace(a.ConfigDir) == "" {
			errs = append(errs, fmt.Errorf(
				"agents.%s.config_dir: blank — it would reach `sbx create` as an empty "+
					"positional argument and mount nothing; remove the key to use the default, "+
					"or set a real path", name))
		}
		// TrimSpace, not `== ""`: agent.FreshnessCommand judges on TrimSpace
		// too, and the stricter of the two judges must win.
		if strings.TrimSpace(a.Update) == "" {
			errs = append(errs, fmt.Errorf(
				"agents.%s.update: required — a sandbox must never start with a stale agent", name))
		}
		for i, d := range a.BinDirs {
			if err := validateBinDir(d); err != nil {
				// Indexed key: an agent may have several bin_dirs, and
				// "bin_dirs" alone wouldn't say which one to fix.
				errs = append(errs, fmt.Errorf("agents.%s.bin_dirs[%d]: %w", name, i, err))
			}
		}
	}

	// TrimSpace wherever a field is "required": otherwise `defaults.agent: "  "`
	// would be judged as declared, then looked up as-is in the registry,
	// yielding an "agent not found" error instead of a "field to fill in" one.
	switch {
	case strings.TrimSpace(g.Defaults.Agent) == "":
		errs = append(errs, fmt.Errorf("defaults.agent: required"))
	default:
		if _, ok := g.Agents[g.Defaults.Agent]; !ok {
			errs = append(errs, fmt.Errorf(
				"defaults.agent: %q is missing from the registry (declared agents: %v)", g.Defaults.Agent, names))
		}
	}

	if strings.TrimSpace(g.Defaults.Stack) == "" {
		errs = append(errs, fmt.Errorf("defaults.stack: required"))
	}

	if !slices.Contains(sshModes, g.SSH.Mode) {
		errs = append(errs, fmt.Errorf("ssh.mode: %q unknown (expected: %v)", g.SSH.Mode, sshModes))
	}
	if g.SSH.Mode == "mount" && strings.TrimSpace(g.SSH.Dir) == "" {
		errs = append(errs, fmt.Errorf("ssh.dir: required when ssh.mode is mount"))
	}

	for i, m := range g.Mounts {
		// Indexed key, like agents.*.bin_dirs above: a config may carry several
		// mounts, and "mounts" alone would not say which one to fix.
		if strings.TrimSpace(m.Host) == "" {
			errs = append(errs, fmt.Errorf(
				"mounts[%d].host: required — a mount with no host path mounts nothing", i))
		}
		// A relative link names no stable location: the VM's startup shell
		// expands it from a cwd den does not control. Refused HERE rather than
		// at boot, where the message would land in a microVM log nobody reads.
		// `$HOME/...` and `~/...` are the two VM-side forms den emits verbatim.
		if l := strings.TrimSpace(m.Link); l != "" &&
			!strings.HasPrefix(l, "/") &&
			!strings.HasPrefix(l, "$HOME/") &&
			!strings.HasPrefix(l, "~/") {
			errs = append(errs, fmt.Errorf(
				"mounts[%d].link: must be absolute, or start with $HOME/ or ~/ — "+
					"%q is relative to a working directory den does not control in the VM", i, m.Link))
		}
	}

	errs = append(errs, g.ValidateWorktree()...)

	for _, key := range slices.Sorted(maps.Keys(g.Repos)) {
		if strings.TrimSpace(g.Repos[key]) == "" {
			errs = append(errs, fmt.Errorf(
				"repos.%s: blank — this key is what a nest's `key:` resolves to; "+
					"set a real path, or remove the entry", key))
		}
	}

	return errs
}

// ValidateWorktree checks only the two fields that decide WHERE worktrees
// live: worktree_layout and worktree_root.
//
// It exists so `den rm` can validate what it USES without validating what it
// doesn't care about — cleanWorktrees (internal/cli/rm.go) reads only these
// two fields, and a config's doctrine is that a broken ~/.den never blocks
// access to a live VM.
//
// The layout check can't be replaced by LoadGlobalUnvalidated's default:
// that one only defaults the EMPTY string, so a typo like `centrl` would
// survive and compute a wrong worktree path — den would clean up in the
// wrong place, silently.
func (g *Global) ValidateWorktree() []error {
	var errs []error
	if !slices.Contains(worktreeLayouts, g.WorktreeLayout) {
		errs = append(errs, fmt.Errorf(
			"worktree_layout: %q unknown (expected: %v)", g.WorktreeLayout, worktreeLayouts))
	}
	// TrimSpace, as everywhere else: LoadGlobalUnvalidated only defaults the
	// empty string, so `worktree_root: "   "` would survive and become a
	// relative path — a directory literally named "␣␣␣" created in the
	// user's current directory.
	//
	// The switch (not two independent checks): on an empty root, the
	// absoluteness check below would raise a SECOND error on the same field,
	// and the user would read two lines for one fault.
	switch {
	case strings.TrimSpace(g.WorktreeRoot) == "":
		// Not "required" either, for the same reason as config_dir above: an
		// ABSENT worktree_root is already defaulted by LoadGlobalUnvalidated
		// (config.go:73); this only fires on a written-but-blank value.
		errs = append(errs, fmt.Errorf(
			"worktree_root: blank — it's the root under which den creates and finds worktrees; "+
				"remove the key to use the default, or set an absolute path"))
	case !filepath.IsAbs(g.WorktreeRoot):
		// LoadGlobalUnvalidated only makes the DEFAULT absolute; a
		// hand-written value passes through unchanged, ExpandPath touching
		// only "~". Without this check, a `worktree_root: wt-relative`
		// resolves against the REPO's directory: `den spawn <nest> -w feat1` would
		// really create a worktree and a branch inside the user's repo, then
		// fail on sbx's argv guard — leaving the user to clean up by hand
		// what den just created. Symmetrically, `den rm` would clean up in
		// the wrong place, silently.
		errs = append(errs, fmt.Errorf(
			"worktree_root: %q is not an absolute path — den passes it as-is to "+
				"`git worktree add`, where it would resolve against the repo and create "+
				"worktrees INSIDE it; write an absolute path, or \"~/...\"", g.WorktreeRoot))
	}
	return errs
}
