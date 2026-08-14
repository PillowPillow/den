package source

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWritePersonalRoundTripsPrivately(t *testing.T) {
	home := t.TempDir()
	want := Personal{
		SchemaVersion: PersonalSchema,
		Version:       "1.0.0",
		Repos:         map[string]string{"go-dgdev": "~/Development/go.dgdev"},
	}
	if err := WritePersonal(home, "dg", want); err != nil {
		t.Fatalf("WritePersonal: %v", err)
	}
	got, err := LoadPersonal(home, "dg")
	if err != nil {
		t.Fatalf("LoadPersonal: %v", err)
	}
	if got.Version != want.Version {
		t.Errorf("version = %q, want %q", got.Version, want.Version)
	}
	// As AUTHORED: a "~" that came back expanded would be written back
	// absolute by the next WritePersonal, silently rewriting a file the user
	// owns and edits.
	if got.Repos["go-dgdev"] != "~/Development/go.dgdev" {
		t.Errorf("repos = %#v, expected the path exactly as authored", got.Repos)
	}
	info, err := os.Stat(PersonalPath(home, "dg"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", info.Mode().Perm())
	}
	dir, err := os.Stat(filepath.Dir(PersonalPath(home, "dg")))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if dir.Mode().Perm() != 0o700 {
		t.Errorf("directory mode = %o, want 700", dir.Mode().Perm())
	}
}

// ExpandedRepos is a SEPARATE map on purpose: resolution needs real paths,
// and the loaded model must keep what the user typed.
func TestExpandedReposDoesNotTouchTheLoadedModel(t *testing.T) {
	p := &Personal{Repos: map[string]string{"api": "~/Development/api"}}
	expanded, err := p.ExpandedRepos()
	if err != nil {
		t.Fatalf("ExpandedRepos: %v", err)
	}
	if strings.HasPrefix(expanded["api"], "~") {
		t.Errorf("expanded[api] = %q, expected the tilde resolved", expanded["api"])
	}
	if p.Repos["api"] != "~/Development/api" {
		t.Errorf("Repos[api] = %q, expansion must not write back into the model", p.Repos["api"])
	}
}

// The personal file is replaced ATOMICALLY: a den killed mid-write, or a write
// that cannot complete at all, must leave the previous configuration readable.
// A truncated one would make every later command refuse a source that is in
// fact installed.
func TestWritePersonalKeepsThePreviousFileWhenReplacementFails(t *testing.T) {
	home := t.TempDir()
	first := Personal{SchemaVersion: PersonalSchema, Version: "1.0.0", Repos: map[string]string{"api": "/dev/api"}}
	if err := WritePersonal(home, "dg", first); err != nil {
		t.Fatalf("WritePersonal: %v", err)
	}

	// Read-only directory: the temporary sibling cannot be created, so the
	// replacement fails before anything touches the live file.
	dir := filepath.Dir(PersonalPath(home, "dg"))
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	second := Personal{SchemaVersion: PersonalSchema, Version: "2.0.0"}
	if err := WritePersonal(home, "dg", second); err == nil {
		t.Fatal("expected the write to fail on a read-only directory")
	}
	got, err := LoadPersonal(home, "dg")
	if err != nil {
		t.Fatalf("the previous configuration must stay readable: %v", err)
	}
	if got.Version != "1.0.0" || got.Repos["api"] != "/dev/api" {
		t.Errorf("loaded = %+v, expected the previous configuration intact", got)
	}
}

func TestLoadPersonalRefusesUnknownKeysAndSchemas(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"camelCase key", "schemaVersion: 1\nversion: 1.0.0\n", "schemaVersion"},
		{"unknown key", "schema_version: 1\nversion: 1.0.0\nrepository_roots: [~/x]\n", "repository_roots"},
		{"unsupported schema", "schema_version: 2\nversion: 1.0.0\n", "schema_version"},
		{"non-exact version", "schema_version: 1\nversion: \">=1.0.0\"\n", "version"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			mkdirAll(t, filepath.Dir(PersonalPath(home, "dg")))
			writeFile(t, PersonalPath(home, "dg"), c.content)
			_, err := LoadPersonal(home, "dg")
			if err == nil {
				t.Fatalf("expected a refusal mentioning %q", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, expected a mention of %q", err.Error(), c.want)
			}
		})
	}
}

// An absent file is not a fault: it is a source that is not configured on this
// machine yet, and `den source configure` is what fixes it. Callers must be
// able to tell that from an unreadable file.
func TestLoadPersonalReportsAbsenceAsSuch(t *testing.T) {
	_, err := LoadPersonal(t.TempDir(), "dg")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("error = %v, expected it to wrap os.ErrNotExist", err)
	}
}

// A name that is not a legal source name never composes a path: den would
// otherwise read and REPLACE a file outside source-config/.
func TestPersonalRefusesAnIllegalSourceName(t *testing.T) {
	home := t.TempDir()
	if err := WritePersonal(home, "../escape", Personal{SchemaVersion: PersonalSchema, Version: "1.0.0"}); err == nil {
		t.Error("WritePersonal accepted a traversing source name")
	}
	if _, err := LoadPersonal(home, "../escape"); err == nil {
		t.Error("LoadPersonal accepted a traversing source name")
	}
}

// Empty and absent are the SAME answer here, and it must not be nil: nil is
// how nest.Resolve recognizes a caller with no source scope, so a manifested
// source that maps nothing would fall back on the global config.yaml.repos —
// the mapping spec §6 says is never consulted for a manifested source.
func TestExpandedReposIsNeverNil(t *testing.T) {
	for _, p := range []*Personal{{}, {Repos: map[string]string{}}} {
		got, err := p.ExpandedRepos()
		if err != nil {
			t.Fatalf("ExpandedRepos: %v", err)
		}
		if got == nil {
			t.Fatal("ExpandedRepos returned nil: an unmapped manifested source must still refuse, not fall back")
		}
	}
}
