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

// DenClaudeBuildKit is DenClaudeKit without its setup block. The build VM never attaches Claude, and
// the block's jq would refuse a template whose own build steps are what install jq.
func DenClaudeBuildKit(image string) ([]byte, error) {
	spec, err := DenClaudeKit(image)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(spec), "\n")
	out := make([]string, 0, len(lines))
	inSetup, found := false, false
	for _, l := range lines {
		if l == "setup:" {
			inSetup, found = true, true
			continue
		}
		if inSetup && l != "" && l[0] != ' ' && l[0] != '#' {
			inSetup = false
		}
		if !inSetup {
			out = append(out, l)
		}
	}
	if !found {
		return nil, errors.New("the embedded den-claude spec has no top-level `setup:` line to drop")
	}
	return []byte(strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"), nil
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
