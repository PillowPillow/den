// Package agent resolves the active agent profile and generates the
// disposable mixin layered onto `sbx create` (spec §5, §9).
package agent

import (
	"fmt"
	"strings"

	"github.com/PillowPillow/den/internal/config"
)

// freshnessAttempts bounds update retries. Three attempts spaced 10s apart
// absorb the NON-instant propagation of the egress policy (spec §7): without
// them, a single network hiccup at boot would abort the whole startup.
const freshnessAttempts = 3

// FreshnessCommand renders the argv of the agent update command, to place as
// the LAST setup.startup of the LAST kit (spec §9.1).
//
// Two non-negotiable invariants:
//
//  1. bin_dirs are injected LITERALLY into an `export PATH`. They contain
//     `$HOME` targeting the VM's home; expanding it on the host side would
//     produce a path that doesn't exist in the microVM. The sbx dispatcher
//     wraps each command in a non-login `su`, whose PATH doesn't contain
//     ~/.local/bin — without this line, `claude` exits 127.
//  2. The script is fail-closed. The dispatcher does `exit $rc` on the first
//     failure, which deprives the FOLLOWING kits of their startup commands —
//     exactly why this kit is layered last.
func FreshnessCommand(agentName string, a config.Agent) ([]string, error) {
	if strings.TrimSpace(a.Update) == "" {
		return nil, fmt.Errorf(
			"agent %q: no update command declared — a sandbox must never start "+
				"with a stale agent", agentName)
	}

	// The binary to probe is the first word of the update command. This is a
	// convention, not a deduction: it's documented in the script's error
	// message so the diagnostic stays readable inside the VM.
	binary := strings.Fields(a.Update)[0]

	var b strings.Builder
	b.WriteString("set -uo pipefail\n\n")
	if len(a.BinDirs) > 0 {
		// Double quotes, unescaped: $HOME must be expanded by the VM's bash, not
		// by den.
		fmt.Fprintf(&b, "# non-login su: minimal PATH, restore the agent's bin_dirs.\n")
		fmt.Fprintf(&b, "export PATH=%q\n\n", strings.Join(a.BinDirs, ":")+":$PATH")
	}
	fmt.Fprintf(&b, "if ! command -v %s >/dev/null 2>&1; then\n", binary)
	fmt.Fprintf(&b, "  echo \"agent %s: FATAL binary %s not found (PATH=$PATH)\" >&2\n", agentName, binary)
	b.WriteString("  exit 127\n")
	b.WriteString("fi\n\n")
	b.WriteString("attempt=1\n")
	fmt.Fprintf(&b, "while [ \"$attempt\" -le %d ]; do\n", freshnessAttempts)
	fmt.Fprintf(&b, "  if output=\"$(%s 2>&1)\"; then\n", a.Update)
	fmt.Fprintf(&b, "    echo \"agent %s: up to date\"\n", agentName)
	b.WriteString("    exit 0\n")
	b.WriteString("  fi\n")
	fmt.Fprintf(&b, "  echo \"agent %s: attempt ${attempt}/%d failed:\" >&2\n", agentName, freshnessAttempts)
	b.WriteString("  echo \"$output\" >&2\n")
	fmt.Fprintf(&b, "  if [ \"$attempt\" -lt %d ]; then\n", freshnessAttempts)
	b.WriteString("    sleep 10\n")
	b.WriteString("  fi\n")
	b.WriteString("  attempt=$((attempt + 1))\n")
	b.WriteString("done\n\n")
	fmt.Fprintf(&b, "echo \"agent %s: FATAL update failed after %d attempts (fail-closed)\" >&2\n",
		agentName, freshnessAttempts)
	b.WriteString("exit 1\n")

	return []string{"bash", "-c", b.String()}, nil
}
