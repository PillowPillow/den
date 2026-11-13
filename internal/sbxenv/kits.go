package sbxenv

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed den-claude/spec.yaml
var denClaudeSpec []byte

// DenClaudeKit is the shared agent kit; the embedded copy is the only source.
func DenClaudeKit() []byte { return append([]byte(nil), denClaudeSpec...) }

// NetworkKit is the per-instance mixin carrying the egress allowlist.
func NetworkKit(allow []string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "schemaVersion: \"2\"\nkind: mixin\nname: %s\nversion: 1.0.0\npermissions:\n  network:\n    allow:\n", NetworkKitName)
	for _, h := range allow {
		fmt.Fprintf(&b, "      - %s\n", scalar(h))
	}
	return []byte(b.String())
}

// GitFile is the .git file that points a worktree at its admin dir.
func GitFile(adminDir string) []byte { return []byte("gitdir: " + adminDir + "\n") }

// DenClaudeKitWithImage swaps the pinned image for another; an empty image returns the embedded spec as is.
// The embedded spec holds exactly one `  image:` line, a test keeps it so.
func DenClaudeKitWithImage(image string) []byte {
	spec := DenClaudeKit()
	if image == "" {
		return spec
	}
	lines := strings.Split(string(spec), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "  image: ") {
			lines[i] = "  image: " + scalar(image)
		}
	}
	return []byte(strings.Join(lines, "\n"))
}
