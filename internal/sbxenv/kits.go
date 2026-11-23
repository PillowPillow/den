package sbxenv

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

//go:embed den-claude/spec.yaml
var denClaudeSpec []byte

// TrustJSONFile is the list of VM folders the den-claude startup step marks trusted in
// ~/.claude.json, under the kit's files/home/ like the repo map.
const TrustJSONFile = "files/home/.den/trust.json"

// RenderTrustJSON renders no folder as [], never null: the startup step refuses anything but an array.
func RenderTrustJSON(paths []string) []byte {
	if paths == nil {
		paths = []string{}
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(paths)
	return b.Bytes()
}

// DenClaudeKit is the agent kit on image: the template's image pinned by digest, or the official image
// a nest names. The embedded spec holds a placeholder in its one `  image:` line, never an image of
// den's own, so an empty image is refused rather than defaulted. Without autoTrust the kit loses its
// `environment:` block, CLAUDE_CODE_SANDBOXED with it, and Claude asks before it trusts a folder.
// The whole block goes: a `variables:` left holding only comments decodes as null, and sbx's
// reaction to that is unmeasured.
func DenClaudeKit(image string, autoTrust bool) ([]byte, error) {
	if image == "" {
		return nil, errors.New("the den-claude kit needs an image: den sets it from the template's record or the image the nest names")
	}
	lines := strings.Split(string(denClaudeSpec), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "  image: ") {
			lines[i] = "  image: " + scalar(image)
		}
	}
	spec := []byte(strings.Join(lines, "\n"))
	if autoTrust {
		return spec, nil
	}
	return dropTopLevel(spec, "environment")
}

// DenClaudeBuildKit is DenClaudeKit without its setup block. The build VM never attaches Claude, and
// the block's jq would refuse a template whose own build steps are what install jq. It has no nest,
// so no auto_trust: it keeps CLAUDE_CODE_SANDBOXED.
func DenClaudeBuildKit(image string) ([]byte, error) {
	spec, err := DenClaudeKit(image, true)
	if err != nil {
		return nil, err
	}
	return dropTopLevel(spec, "setup")
}

// dropTopLevel removes the top-level `key:` line and every indented or comment line under it.
func dropTopLevel(spec []byte, key string) ([]byte, error) {
	lines := strings.Split(string(spec), "\n")
	out := make([]string, 0, len(lines))
	in, found := false, false
	for _, l := range lines {
		if l == key+":" {
			in, found = true, true
			continue
		}
		if in && l != "" && l[0] != ' ' && l[0] != '#' {
			in = false
		}
		if !in {
			out = append(out, l)
		}
	}
	if !found {
		return nil, fmt.Errorf("the embedded den-claude spec has no top-level `%s:` line to drop", key)
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
