// Package config loads and validates the content of ~/.den (config.yaml, stacks/).
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultHome is the last rung of Home's precedence on its own: ~/.den, with
// neither the flag nor $DEN_HOME consulted.
//
// Exported for the one thing Home cannot answer — "is this den home the plain
// default, or did something have to be typed to get here?". internal/deninit
// asks that to decide whether `den init`'s closing `den doctor` hint needs to
// carry --den-home. Deliberately NOT expressible as Home("") with the
// environment ignored: DEN_HOME is precisely what must not reach it there, and
// a caller comparing against Home("") would be asking whether the CURRENT
// shell agrees, not whether the path is the default.
func DefaultHome() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		// os.UserHomeDir's message ("$HOME is not defined") is accurate but
		// silent: it says neither what den was looking for nor that two
		// fallbacks exist. This happens for real under systemd, in a
		// container, or in a cron job.
		return "", fmt.Errorf(
			"could not locate den's configuration directory (~/.den): %w — "+
				"pass --den-home <dir>, or set DEN_HOME", err)
	}
	return filepath.Abs(filepath.Join(h, ".den"))
}

// Home resolves the den config directory. Priority: flag > $DEN_HOME > ~/.den.
//
// The result is ALWAYS absolute: worktree_root derives from it, and this path
// later goes to `git worktree` and `sbx create`, where cwd is no longer guaranteed.
//
// The last rung goes through DefaultHome rather than repeating filepath.Join(h,
// ".den") here, so the two can never answer different strings for the same
// machine — deninit compares them for equality.
func Home(flagValue string) (string, error) {
	raw := flagValue
	if raw == "" {
		raw = os.Getenv("DEN_HOME")
	}
	if raw == "" {
		return DefaultHome()
	}
	return filepath.Abs(raw)
}

// GlobalPath is the SOLE definition of where the global config lives:
// <denHome>/config.yaml. Every reader and every message that names the file
// to fix must go through this, or a divergence between them would be
// invisible (same doctrine as agent/mixin.go's mixinDir/mixinPath).
func GlobalPath(denHome string) string {
	return filepath.Join(denHome, "config.yaml")
}

// ExpandPath expands a leading "~" in a path. Deliberately minimal: neither
// $VAR nor ~user. The $HOME values found in bin_dirs target the VM's home and
// must cross den untouched (see spec §9.1).
func ExpandPath(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		// The offending path is named: this function expands the "~" in
		// config_dir, ssh.dir and repos, and the raw error wouldn't say which
		// field carried the tilde.
		return "", fmt.Errorf("expanding %q: %w — set HOME, or write an absolute path", p, err)
	}
	if p == "~" {
		return h, nil
	}
	return filepath.Join(h, p[2:]), nil
}
