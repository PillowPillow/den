package agent

import (
	"fmt"
	"strings"

	"github.com/PillowPillow/den/internal/nest"
)

// LinkCommand renders the argv of the link phase: the startup command that
// makes a mounted host directory reachable at the path the VM's tools read.
//
// WHY THIS EXISTS AT ALL. sbx mounts every workspace at its HOST path (spec
// A11), and `sbx create` takes no mount-target flag (probed 2026-08-07). So a
// directory mounted from /Users/me/.ssh_sbx lands at /Users/me/.ssh_sbx inside
// the VM, while ssh reads $HOME/.ssh = /home/agent/.ssh. The mount succeeds and
// the tool never sees it. A symlink is the only bridge available.
//
// Returns nil when nothing needs linking, so RenderMixin can omit the startup
// entry entirely rather than emit a no-op that would show up in every golden.
//
// PLACED FIRST among den's startup entries, never last: the freshness command
// runs the agent's own updater, which reaches the network and git and therefore
// wants ~/.ssh already in place. Links after it would leave the FIRST boot
// running unlinked and only the second one correct — a bug that disappears when
// you reboot to investigate it. FreshnessCommand's own contract (spec §9.1)
// independently requires it to stay last.
func LinkCommand(mounts []nest.Mount) []string {
	var linked []nest.Mount
	for _, m := range mounts {
		if strings.TrimSpace(m.Link) != "" {
			linked = append(linked, m)
		}
	}
	if len(linked) == 0 {
		return nil
	}

	var b strings.Builder
	// `set -uo pipefail` and NOT `-e`, matching FreshnessCommand: every failure
	// below is handled explicitly, with a message naming the config key. `-e`
	// would abort on the first non-zero test with no diagnostic at all.
	b.WriteString("set -uo pipefail\n\n")
	b.WriteString(linkFunc)
	for _, m := range linked {
		// `~` is NOT expanded by bash inside double quotes, but the link MUST be
		// double-quoted so `$HOME` expands in the VM. Emitting `"~/.ssh"` verbatim
		// would create a directory literally named `~` in the startup script's cwd —
		// the tool would then read the wrong path with no error, which is the exact
		// failure this whole feature exists to remove. `~/x` and `$HOME/x` denote the
		// same VM path, so rewriting is lossless, and it is done HERE rather than in
		// config so the config surface keeps the form the user wrote.
		link := m.Link
		if strings.HasPrefix(link, "~/") {
			link = "$HOME/" + strings.TrimPrefix(link, "~/")
		}
		// Host SINGLE-quoted, link DOUBLE-quoted. This asymmetry is the whole
		// design rule made executable: den already expanded the host path and
		// the VM must not touch it, while $HOME in the link MUST expand here,
		// to the VM's /home/agent. Same reasoning as bin_dirs in freshness.go.
		fmt.Fprintf(&b, "den_link %s \"%s\" %s\n",
			shellSingleQuote(m.Host), link, shellSingleQuote(m.Key))
	}
	return []string{"bash", "-c", b.String()}
}

// linkFunc is the fail-closed linker, shared by every mount.
//
// Two decisions are load-bearing:
//
//  1. `-L` is tested BEFORE `-d`, because `-d` FOLLOWS symlinks: a symlink to a
//     directory satisfies both, and testing -d first would send an
//     already-correct link down the directory branch.
//
//  2. An existing directory is removed with `rmdir`, never `rm -rf`. rmdir
//     fails on a non-empty directory, so the emptiness test is enforced by the
//     kernel and not merely by the `if` above it: even under a concurrent write
//     the worst outcome is a refused boot, never destroyed data. An empty
//     directory is a base-image placeholder and safe to take; a non-empty one
//     is somebody's profile.
//
// Refusing matters more than it looks: `ln -sfn SRC DST` on an existing
// DIRECTORY does not replace it, it silently creates DST/<basename> INSIDE it.
// The tool then reads the wrong path with no error — the exact failure mode
// this feature was written to remove.
const linkFunc = `den_link() {
  src="$1"; dst="$2"; key="$3"
  parent="$(dirname "$dst")"
  if ! mkdir -p "$parent"; then
    echo "den mounts: FATAL cannot create $parent (from $key)" >&2
    exit 1
  fi
  if [ ! -L "$dst" ] && [ -d "$dst" ]; then
    if ! rmdir "$dst" 2>/dev/null; then
      echo "den mounts: FATAL $dst is a non-empty directory (from $key) — den refuses to replace it" >&2
      exit 1
    fi
  elif [ ! -L "$dst" ] && [ -e "$dst" ]; then
    echo "den mounts: FATAL $dst exists and is not a directory (from $key) — den refuses to replace it" >&2
    exit 1
  fi
  if ! ln -sfn "$src" "$dst"; then
    echo "den mounts: FATAL cannot link $dst -> $src (from $key)" >&2
    exit 1
  fi
  echo "den mounts: $dst -> $src"
}

`

// shellSingleQuote wraps s so bash reads it VERBATIM, embedded quotes included.
//
// Host paths and config keys are user data: a directory may legitimately be
// named with a space, a $ or a quote. Go's %q is NOT a substitute — it produces
// Go escaping, which bash reinterprets differently inside double quotes.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
