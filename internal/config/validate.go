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

// normalizeLink returns the VM path a link DENOTES, for comparison only.
//
// `~/x` and `$HOME/x` are the same path in the VM, and agent.LinkCommand
// rewrites the first form into the second at emission — deliberately at
// emission, so the config surface keeps the form the user wrote. Comparison
// therefore has to do the same rewrite here rather than rely on a canonical
// value at load: without it, `link: ~/.ssh` beside `ssh.mode: mount` walks
// straight through the duplicate check and one of the two silently disappears.
//
// The result is NEVER stored back into the config: it is a comparison key.
func normalizeLink(l string) string {
	if strings.HasPrefix(l, "~/") {
		return "$HOME/" + strings.TrimPrefix(l, "~/")
	}
	return l
}

// validateMountLink checks that a mounts[].link is really a VM PATH.
//
// Same fault line as validateBinDir above, different charset. agent.LinkCommand
// emits the link inside DOUBLE quotes (`den_link 'host' "link" 'key'`) and
// cannot escape it: `$HOME` must be expanded by the VM's bash, which is the
// whole point of the field. So everything bash would act on inside double
// quotes has to be refused here, at the one place that reads config.yaml.
//
// This is a TYPING defect first — link: is documented as a path — and a real
// hazard second: `link: $HOME/x$(curl … | sh)` would otherwise be executed
// inside the VM on every boot, and a stray `"` would break the startup script's
// syntax, which aborts the whole run before den_link ever prints the marker the
// gate reads.
//
// $HOME only, and only spelled `$HOME`: the link script runs `set -uo
// pipefail`, so an unset ${FOO} aborts the boot with a message that names
// neither the mount nor the config key. Narrowing the accepted set is what
// turns that into a refusal the user can read, before any VM exists.
//
// `${HOME}` is refused too, though bash expands it identically. Two spellings
// of one concept is what the duplicate-link check cannot survive: agent.
// LinkCommand rewrites `~/` and nothing else, so `${HOME}/.ssh` would compare
// unequal to the `$HOME/.ssh` of the ssh.mode sugar and walk straight through
// the collision refusal — the very fault this validation was extended to catch.
func validateMountLink(l string) error {
	// A relative link names no stable location: the VM's startup shell expands
	// it from a cwd den does not control. Refused HERE rather than at boot,
	// where the message would land in a microVM log nobody reads. `$HOME/...`
	// and `~/...` are the two VM-side forms den emits verbatim.
	if !strings.HasPrefix(l, "/") &&
		!strings.HasPrefix(l, "$HOME/") &&
		!strings.HasPrefix(l, "~/") {
		return fmt.Errorf(
			"must be absolute, or start with $HOME/ or ~/ — "+
				"%q is relative to a working directory den does not control in the VM", l)
	}
	if strings.Contains(l, "$(") {
		return fmt.Errorf(
			"%q contains a command substitution $( — link is a path, and the VM's bash "+
				"would execute it at every boot", l)
	}
	if strings.Contains(l, "`") {
		return fmt.Errorf(
			"%q contains a backtick ` — link is a path, and the VM's bash would execute "+
				"it at every boot", l)
	}
	if strings.ContainsAny(l, "\"\\") {
		return fmt.Errorf(
			`%q contains a quote or a backslash — den emits link inside double quotes, `+
				`where either one would break the startup script's syntax and abort the boot `+
				`before den can say why`, l)
	}
	// Variable expansion is refused EXCEPT $HOME/${HOME}, which is the reason
	// this field is emitted unescaped at all.
	for rest := l; ; {
		i := strings.Index(rest, "$")
		if i < 0 {
			break
		}
		rest = rest[i:]
		switch {
		case strings.HasPrefix(rest, "$HOME"):
			rest = rest[len("$HOME"):]
		default:
			return fmt.Errorf(
				"%q expands a variable other than $HOME — the VM's link script runs with "+
					"`set -u`, so an unset one aborts the boot naming neither the mount nor "+
					"this key; write the path out, or use $HOME", l)
		}
	}
	for _, r := range l {
		if unicode.IsControl(r) {
			return fmt.Errorf(
				"%q contains a control character (%q) — it would reach the VM's startup "+
					"script verbatim and corrupt the path", l, r)
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

	// seenLinks maps a link target, NORMALIZED for comparison only, to the
	// config key that claimed it first. See validateMountLink for why the
	// normalization cannot happen at load instead.
	seenLinks := map[string]string{}
	if g.SSH.Mode == "mount" && strings.TrimSpace(g.SSH.Dir) != "" {
		// The ssh.mode sugar (nest.resolveMounts) generates a mount on
		// SSHLinkTarget, and it is appended LAST, so it wins the `ln -sfn` race
		// against any `mounts:` entry aiming at the same path. Seeded first here
		// so the refusal names the entry the user can move — `mounts[N]` — and
		// not the sugar, which has no line in config.yaml to edit.
		seenLinks[normalizeLink(SSHLinkTarget)] = "ssh.dir (ssh.mode: mount)"
	}

	for i, m := range g.Mounts {
		// Indexed key, like agents.*.bin_dirs above: a config may carry several
		// mounts, and "mounts" alone would not say which one to fix.
		switch {
		case strings.TrimSpace(m.Host) == "":
			errs = append(errs, fmt.Errorf(
				"mounts[%d].host: required — a mount with no host path mounts nothing", i))
		case !filepath.IsAbs(m.Host):
			// Same fault, same shape as worktree_root below, and it must be
			// refused in the SAME place: LoadGlobalUnvalidated's ExpandPath only
			// touches a leading "~", so a hand-written relative host survives to
			// `sbx create`'s argv. Spawn's own pre-flight os.Stat does not catch
			// it — a relative path resolves against den's cwd and can very well
			// exist there — so the only refusal left was sbx.CreateArgv's, which
			// fires AFTER worktrees and branches have been created, leaving the
			// user to clean up by hand what den just made.
			errs = append(errs, fmt.Errorf(
				"mounts[%d].host: %q is not an absolute path — den hands it to `sbx create` "+
					"verbatim, where it would resolve against a directory den does not control; "+
					"write an absolute path, or \"~/...\"", i, m.Host))
		}
		if l := strings.TrimSpace(m.Link); l != "" {
			if err := validateMountLink(l); err != nil {
				errs = append(errs, fmt.Errorf("mounts[%d].link: %w", i, err))
				continue // a refused link is not compared: it will never be emitted
			}
			// Two mounts on ONE link target is not a merge, it is a silent
			// override: the link phase runs `ln -sfn` per entry, in order, and
			// the last one wins while both print their success line. den refuses
			// rather than normalizing in silence (spec §2).
			key := normalizeLink(l)
			if first, taken := seenLinks[key]; taken {
				errs = append(errs, fmt.Errorf(
					"mounts[%d].link: %q is already linked by %s — the link phase would run "+
						"`ln -sfn` twice on the same VM path and only the last would survive, "+
						"silently; give one of them another link, or drop it", i, m.Link, first))
				continue
			}
			seenLinks[key] = fmt.Sprintf("mounts[%d]", i)
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
		// resolves against the REPO's directory: `den up <nest> -w feat1` would
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
