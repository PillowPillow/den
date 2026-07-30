package config

import (
	"strings"
	"testing"
)

// target is a minimal schema, independent of den's real types: this test
// exercises the decoder, not the configuration.
type target struct {
	Egress []string `yaml:"egress"`
}

func TestDecodeYAMLStrictRewritesUnknownKeys(t *testing.T) {
	raw := []byte("egress:\n  - a\negres:\n  - b\n") // typo on line 3
	var c target
	err := DecodeYAMLStrict("/den/config.yaml", raw, &c)
	if err == nil {
		t.Fatal("expected an error on the unknown key")
	}
	msg := err.Error()

	if !strings.Contains(msg, `unknown key "egres"`) {
		t.Errorf("message = %q, expected a rewritten mention of the offending key", msg)
	}
	// The line number is what makes the message useful: it must survive.
	if !strings.Contains(msg, "line 3") {
		t.Errorf("message = %q, expected the offending key's line", msg)
	}
	// The internal Go type has no business in front of the user.
	if strings.Contains(msg, "not found in type") || strings.Contains(msg, "config.target") {
		t.Errorf("message = %q, the internal Go type must no longer appear", msg)
	}
	// The original format is preserved.
	if !strings.Contains(msg, "/den/config.yaml: invalid YAML:") {
		t.Errorf("message = %q, expected the format `%%s: invalid YAML: %%w`", msg)
	}
}

// yaml.v3's "yaml: unmarshal errors:" header is visible on every invalid YAML
// in den — it's the first line of `den nest ls` on a broken nest, while the
// detail line right below is already rewritten.
//
// What does NOT change: the "line N:" prefix yaml.v3 places before each
// detail. It's the line number that makes the message actionable, and it's
// pinned by TestDecodeYAMLStrictRewritesUnknownKeys.
func TestDecodeYAMLStrictRewritesTheYamlV3Header(t *testing.T) {
	raw := []byte("egress:\n  - a\negres:\n  - b\n")
	var c target
	err := DecodeYAMLStrict("/den/config.yaml", raw, &c)
	if err == nil {
		t.Fatal("expected an error on the unknown key")
	}
	if strings.Contains(err.Error(), "yaml: unmarshal errors:") {
		t.Errorf("yaml.v3's raw header remains in the message: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "decoding errors") {
		t.Errorf("message = %q, expected a rewritten header", err.Error())
	}
}

// Several unknown keys in the same file: all rewritten, none lost.
func TestDecodeYAMLStrictRewritesEveryUnknownKey(t *testing.T) {
	raw := []byte("egres:\n  - a\nworktre_root: /tmp\n")
	var c target
	err := DecodeYAMLStrict("/den/config.yaml", raw, &c)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{`unknown key "egres"`, `unknown key "worktre_root"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, expected %q", err.Error(), want)
		}
	}
}

// A YAML error that doesn't follow the "field X not found in type T" pattern
// must pass through UNCHANGED: an imperfect message beats a mangled one.
func TestDecodeYAMLStrictLeavesAnUnrecognizedMessageAlone(t *testing.T) {
	raw := []byte("egress: [this is not a closed list")
	var c target
	err := DecodeYAMLStrict("/den/config.yaml", raw, &c)
	if err == nil {
		t.Fatal("expected an error on malformed YAML")
	}
	if !strings.Contains(err.Error(), "/den/config.yaml: invalid YAML:") {
		t.Errorf("message = %q, expected the original format", err.Error())
	}
	// Nothing was rewritten: yaml.v3's diagnostic is the only one available.
	if strings.Contains(err.Error(), "unknown key") {
		t.Errorf("message = %q: this isn't an unknown-key error", err.Error())
	}
	if len(err.Error()) <= len("/den/config.yaml: invalid YAML: ") {
		t.Errorf("message = %q: the original diagnostic was lost", err.Error())
	}
}

// A MULTI-DOCUMENT file must not be read halfway.
//
// yaml.Decoder.Decode reads only ONE document; anything following a "---" was
// silently ignored. That's exactly the failure mode DecodeYAMLStrict exists
// to prevent — an allowlist that doesn't apply without a word — just at
// document granularity instead of key granularity.
//
// Measured on the assembled binary, on a nest with two documents:
// `den nest show api` returned rc=0 and only displayed the first, the
// `stack:` and an entire `egress:` block from the second having never
// existed for den.
func TestDecodeYAMLStrictRejectsAMultiDocumentFile(t *testing.T) {
	raw := []byte("egress:\n  - first\n---\negress:\n  - second\n")
	var c target
	err := DecodeYAMLStrict("/den/nests/api.yaml", raw, &c)
	if err == nil {
		t.Fatalf("multi-document file accepted: the second document is silently ignored "+
			"(egress read = %v)", c.Egress)
	}
	msg := err.Error()
	// The file to fix, and the word saying WHAT to fix: "invalid YAML" alone
	// would send someone looking for a syntax error, while the file is
	// syntactically correct.
	if !strings.Contains(msg, "/den/nests/api.yaml") {
		t.Errorf("message = %q, must name the offending file", msg)
	}
	if !strings.Contains(msg, "document") {
		t.Errorf("message = %q, must say the file carries multiple documents", msg)
	}
}

// The counterpart: a single document followed by an end marker, or a file
// that only contains one document, remain accepted. Without this test, a
// check that rejected any file containing "---" would pass the test above —
// and break configurations that open with a start-of-document marker, a
// perfectly legal form.
func TestDecodeYAMLStrictAcceptsASingleDocumentEvenWithAMarker(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"no marker", "egress:\n  - a\n"},
		{"start marker", "---\negress:\n  - a\n"},
		{"start and end markers", "---\negress:\n  - a\n...\n"},
		{"empty document", ""},
		{"comments only", "# nothing but comments\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var dest target
			if err := DecodeYAMLStrict("/den/config.yaml", []byte(c.raw), &dest); err != nil {
				t.Errorf("legal form refused: %v", err)
			}
		})
	}
}

// The forms that lose NOTHING but stay refused.
//
// Measured: the second Decode succeeds but re-encodes as an EMPTY document in
// all three cases: a trailing "---" with nothing behind it, the front-matter
// form "---\n...\n---", and a "---" followed only by comments. Nothing is
// actually dropped in any of them.
//
// The refusal is KEPT — it's fail-closed, "..." is the real end-of-document
// marker, and the ambiguity isn't worth tolerating — but the message must not
// claim a loss that doesn't happen. The front-matter habit is common enough
// that the case arises in practice, and a user blocked on a file with no real
// fault, given a false explanation, is exactly what this test exists to prevent.
func TestDecodeYAMLStrictRefusesWithoutClaimingALossThatDidNotHappen(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"trailing --- marker with no content", "egress:\n  - a\n---\n"},
		{"front-matter (--- ... ---)", "---\negress:\n  - a\n---\n"},
		{"--- followed only by comments", "egress:\n  - a\n---\n# nothing but comments\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var dest target
			err := DecodeYAMLStrict("/den/nests/api.yaml", []byte(c.raw), &dest)
			// Fail-closed, assumed and locked in: these forms REMAIN refused.
			if err == nil {
				t.Fatal("multi-document form accepted: the refusal is deliberately fail-closed")
			}
			msg := err.Error()
			// The FALSE CLAIM, forbidden: here, nothing is actually ignored.
			if strings.Contains(msg, "silently ignored") {
				t.Errorf("message claims a loss that does NOT happen — the following document is "+
					"empty (measured); got: %s", msg)
			}
			if !strings.Contains(msg, "/den/nests/api.yaml") {
				t.Errorf("message must name the offending file; got: %s", msg)
			}
			// And it must say WHAT to do, the file having no syntax fault.
			if !strings.Contains(msg, "document") {
				t.Errorf("message must talk about documents; got: %s", msg)
			}
		})
	}
}
