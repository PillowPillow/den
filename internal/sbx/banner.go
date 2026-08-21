package sbx

import "strings"

// StripUpdateBanner removes sbx's "Update Available" box from a TEXT output.
//
// It is DecodeJSON's problem, on the one sbx read that has no `--json`:
// `sbx secret ls -g` prints an aligned table, den parses it by column offsets
// (internal/converge, parseSecretList), and the banner's corner lines carry a
// single field where that parser demands two — so a banner turned the whole
// credential inventory into `sbx secret ls: unreadable custom secret row
// "╭────╮"`. den announcing an unreadable machine because sbx advertised a
// release. Read DecodeJSON for the full story and for why silencing the banner
// at the source is not available.
//
// A line is banner iff, once trimmed, it is non-empty AND either:
//
//   - every rune of it is box-drawing (U+2500–U+257F) — the ╭──╮, ├──┤ and
//     ╰──╯ rules; or
//   - it opens AND closes with │ — the framed content rows.
//
// Recognized by SHAPE rather than by the words inside, on purpose: "Docker
// Sandboxes Update Available", the version numbers and the brew line are all
// wording sbx is free to change tomorrow, and a matcher keyed on them would
// go silently blind. The frame is what makes it a box.
//
// No sbx table den parses can collide with either rule: the tables are plain
// aligned columns with no borders at all (`sbx secret ls -g`, measured on
// v0.38.0, 2026-08-14 — see the fixture in internal/converge/testdata), so no
// legitimate row is framed, and none is made of box-drawing characters alone.
//
// Unlike DecodeJSON, this strips banner lines WHEREVER they are rather than
// only behind the payload. The two are not in tension: DecodeJSON reads ONE
// value and a stream it cannot parse from the start is a stream it must
// refuse, where a text table has no such boundary — dropping the frame is the
// only reading of "ignore the banner" that means anything here.
func StripUpdateBanner(text string) string {
	lines := strings.Split(text, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if isBannerLine(line) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func isBannerLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(trimmed, "│") && strings.HasSuffix(trimmed, "│") {
		return true
	}
	for _, r := range trimmed {
		if r < 0x2500 || r > 0x257F {
			return false
		}
	}
	return true
}
