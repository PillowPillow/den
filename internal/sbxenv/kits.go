package sbxenv

import (
	_ "embed"
	"errors"
	"fmt"
	"strings"
)

//go:embed den-claude/spec.yaml
var denClaudeSpec []byte

// DenClaudeKit is the agent kit on image: the template's image pinned by digest, or the official image
// a nest names. The embedded spec holds a placeholder in its one `  image:` line, never an image of
// den's own, so an empty image is refused rather than defaulted.
func DenClaudeKit(image string) ([]byte, error) {
	if image == "" {
		return nil, errors.New("the den-claude kit needs an image: den sets it from the template's record or the image the nest names")
	}
	lines := strings.Split(string(denClaudeSpec), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "  image: ") {
			lines[i] = "  image: " + scalar(image)
		}
	}
	return []byte(strings.Join(lines, "\n")), nil
}

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
