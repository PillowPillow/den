package source

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeNest(t *testing.T, root, name string) string {
	t.Helper()
	path := filepath.Join(root, "nests", name+".yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("stack: devx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDecodeSandboxNestLocal(t *testing.T) {
	home := t.TempDir()
	writeNest(t, home, "api")

	got, err := DecodeSandboxNest(home, "api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Root != home || got.Source != "" || got.Name != "api" {
		t.Errorf("decoded %+v, want the local nest", got)
	}
	if got.Ref() != "api" {
		t.Errorf("Ref() = %q, want the bare name", got.Ref())
	}
}

// The whole point: `den ls` prints "corp-api", so that is the string the user
// has in front of them, and it must decode back to the source nest that
// produced it.
func TestDecodeSandboxNestFromASource(t *testing.T) {
	home := t.TempDir()
	writeNest(t, Dir(home, "corp"), "api")

	got, err := DecodeSandboxNest(home, "corp-api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Root != Dir(home, "corp") || got.Source != "corp" || got.Name != "api" {
		t.Errorf("decoded %+v, want the corp source's nest api", got)
	}
	if got.Ref() != "corp:api" {
		t.Errorf("Ref() = %q, want corp:api — the spelling a message must print", got.Ref())
	}
}

// A source nest whose own bare name carries a "-": the decomposition is still
// unique, because only ONE prefix can match a given installed source name.
func TestDecodeSandboxNestWithAHyphenatedBareName(t *testing.T) {
	home := t.TempDir()
	writeNest(t, Dir(home, "corp"), "api-v2")

	got, err := DecodeSandboxNest(home, "corp-api-v2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Source != "corp" || got.Name != "api-v2" {
		t.Errorf("decoded %+v, want corp:api-v2", got)
	}
}

// The edge the review named: a LOCAL nest file created after a source sandbox
// was spawned makes the decode retroactively ambiguous. den must not pick one
// silently — same shape as spawn's own collision refusals.
func TestDecodeSandboxNestRefusesALocalAndSourceAmbiguity(t *testing.T) {
	home := t.TempDir()
	local := writeNest(t, home, "corp-api")
	fromSource := writeNest(t, Dir(home, "corp"), "api")

	_, err := DecodeSandboxNest(home, "corp-api")
	if err == nil {
		t.Fatal("expected a refusal: two nests produce this sandbox name")
	}
	for _, want := range []string{local, fromSource, "corp:api"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

// Two installed sources decomposing the same string. spawn refuses to CREATE
// this, but a source installed after the fact can produce it, and the decode
// must not arbitrate it either.
func TestDecodeSandboxNestRefusesACrossSourceAmbiguity(t *testing.T) {
	home := t.TempDir()
	a := writeNest(t, Dir(home, "corp"), "api-v2")
	b := writeNest(t, Dir(home, "corp-api"), "v2")

	_, err := DecodeSandboxNest(home, "corp-api-v2")
	if err == nil {
		t.Fatal("expected a refusal: two sources decompose this sandbox name")
	}
	for _, want := range []string{a, b, "corp:api-v2", "corp-api:v2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

// No source prefixes the name: this is an ordinary local nest, and a missing
// file must keep producing the caller's own familiar "reading …/nests/api.yaml"
// rather than a sources-flavoured diagnosis about a nest nobody mentioned.
func TestDecodeSandboxNestFallsBackToLocalWhenNothingDecodes(t *testing.T) {
	home := t.TempDir()
	writeNest(t, Dir(home, "corp"), "other")

	got, err := DecodeSandboxNest(home, "api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Root != home || got.Source != "" || got.Name != "api" {
		t.Errorf("decoded %+v, want the local fallback", got)
	}
}

// A source's prefix DOES match but neither it nor a local file declares the
// nest. Here the raw "reading …/nests/corp-api.yaml" would name a file that by
// construction never exists, so den names both spellings it looked for.
func TestDecodeSandboxNestRefusesWhenAPrefixMatchesButNothingExists(t *testing.T) {
	home := t.TempDir()
	writeNest(t, Dir(home, "corp"), "other")

	_, err := DecodeSandboxNest(home, "corp-api")
	if err == nil {
		t.Fatal("expected a refusal naming what den looked for")
	}
	for _, want := range []string{"corp-api", "corp:api", "den nest ls"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

// A den with no sources/ at all decodes locally and never errors: the
// overwhelmingly common case must cost nothing and change nothing.
func TestDecodeSandboxNestWithNoSourcesInstalled(t *testing.T) {
	home := t.TempDir()
	got, err := DecodeSandboxNest(home, "some-nest")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Root != home || got.Source != "" || got.Name != "some-nest" {
		t.Errorf("decoded %+v, want the local fallback", got)
	}
}
