package sbx

import (
	"strings"
	"testing"
)

// A plain aligned table survives untouched — the property that matters most:
// StripUpdateBanner runs on EVERY `sbx secret ls -g`, banner or not.
const secretTable = `SCOPE      TYPE       NAME       SECRET
(global)   service    github     (stored)
(global)   registry   r.test:443 token-***

CUSTOM SECRETS
SCOPE      TARGETS         ENV        PLACEHOLDER  SECRET
(global)   api.test        TOKEN      sbx-cs-0000  token-***
`

func TestStripUpdateBannerLeavesARealTableAlone(t *testing.T) {
	if got := StripUpdateBanner(secretTable); got != secretTable {
		t.Errorf("the table was modified:\n--- got ---\n%s\n--- want ---\n%s", got, secretTable)
	}
}

func TestStripUpdateBannerRemovesTheWholeBox(t *testing.T) {
	got := StripUpdateBanner(secretTable + updateBanner)
	if strings.Contains(got, "╭") || strings.Contains(got, "│") || strings.Contains(got, "╰") {
		t.Errorf("box characters survived:\n%s", got)
	}
	if strings.Contains(got, "Docker Sandboxes Update Available") {
		t.Errorf("the framed content survived:\n%s", got)
	}
	if !strings.Contains(got, "(global)   service    github") {
		t.Errorf("a real row was dropped:\n%s", got)
	}
}

// Recognized by SHAPE, not by the words inside: sbx is free to reword the
// banner, and a matcher keyed on "Update Available" would go silently blind.
func TestStripUpdateBannerDoesNotDependOnTheWording(t *testing.T) {
	reworded := "╭────────────────────────╮\n│ Une nouvelle version    │\n╰────────────────────────╯\n"
	if got := strings.TrimSpace(StripUpdateBanner(reworded)); got != "" {
		t.Errorf("StripUpdateBanner = %q, want everything dropped", got)
	}
}
