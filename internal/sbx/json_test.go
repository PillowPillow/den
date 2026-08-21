package sbx

import (
	"strings"
	"testing"
)

// updateBanner is the real thing, pasted byte for byte from a `den exec` that
// died on 2026-08-21 (sbx v0.38.0 announcing v0.39.0). The box-drawing
// characters matter: it is their first byte, 0xE2, that json.Unmarshal
// reported as `invalid character 'â' after top-level value`.
const updateBanner = `
╭──────────────────────────────────────────────────────────────────────────────────╮
│ Docker Sandboxes Update Available                                                │
├──────────────────────────────────────────────────────────────────────────────────┤
│ v0.38.0  →  v0.39.0                                                              │
├──────────────────────────────────────────────────────────────────────────────────┤
│ Release notes  https://github.com/docker/sbx-releases/releases/tag/v0.39.0       │
├──────────────────────────────────────────────────────────────────────────────────┤
│ To upgrade     brew upgrade docker/tap/sbx                                       │
╰──────────────────────────────────────────────────────────────────────────────────╯
`

func TestDecodeJSONIgnoresWhatFollowsTheValue(t *testing.T) {
	var doc struct {
		Name string `json:"name"`
	}
	if err := DecodeJSON("ls", []byte(`{"name":"swimspot"}`+updateBanner), &doc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if doc.Name != "swimspot" {
		t.Errorf("name = %q, want %q", doc.Name, "swimspot")
	}
}

// What PRECEDES the value stays an error: den does not go looking for a
// payload in the middle of a stream it does not understand. Same contract as
// internal/policy's readVerdict.
func TestDecodeJSONRefusesWhatPrecedesTheValue(t *testing.T) {
	var doc struct {
		Name string `json:"name"`
	}
	err := DecodeJSON("ls", []byte(updateBanner+`{"name":"swimspot"}`), &doc)
	if err == nil {
		t.Fatal("expected a refusal on a value buried behind unknown content")
	}
	if !strings.Contains(err.Error(), "sbx ls: unreadable JSON output") {
		t.Errorf("error = %v, want it to name the subcommand", err)
	}
	if !strings.Contains(err.Error(), "Docker Sandboxes Update Available") {
		t.Errorf("error = %v, want the raw output in it", err)
	}
}

// Empty stdout must NOT come back as a Decoder's bare "EOF": that reads as
// nothing at all, where Unmarshal at least said "unexpected end of JSON
// input". Tolerating the banner must not make this failure less diagnosable.
func TestDecodeJSONNamesEmptyOutputAsSuch(t *testing.T) {
	var doc struct {
		Name string `json:"name"`
	}
	err := DecodeJSON("template ls", []byte("   \n\t "), &doc)
	if err == nil {
		t.Fatal("expected a refusal on empty output")
	}
	if !strings.Contains(err.Error(), "wrote nothing to stdout") {
		t.Errorf("error = %v, want it to say the output was empty", err)
	}
	if !strings.Contains(err.Error(), "sbx template ls") {
		t.Errorf("error = %v, want it to name the subcommand", err)
	}
	if strings.Contains(err.Error(), "EOF") {
		t.Errorf("error = %v, want no bare EOF", err)
	}
}

func TestDecodeJSONStillRefusesBrokenJSON(t *testing.T) {
	var doc struct {
		Name string `json:"name"`
	}
	err := DecodeJSON("ls", []byte(`{"name":`), &doc)
	if err == nil {
		t.Fatal("expected a refusal on truncated JSON")
	}
	if !strings.Contains(err.Error(), "unreadable JSON output") {
		t.Errorf("error = %v", err)
	}
}
