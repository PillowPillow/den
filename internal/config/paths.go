// Package config loads and validates the content of ~/.den (config.yaml, stacks/).
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Home resolves the den config directory. Priority: flag > $DEN_HOME > ~/.den.
//
// The result is ALWAYS absolute: worktree_root derives from it, and this path
// later goes to `git worktree` and `sbx create`, where cwd is no longer guaranteed.
func Home(flagValue string) (string, error) {
	raw := flagValue
	if raw == "" {
		raw = os.Getenv("DEN_HOME")
	}
	if raw == "" {
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
		raw = filepath.Join(h, ".den")
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
