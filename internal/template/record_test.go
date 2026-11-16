package template

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/worktree"
)

func TestMain(m *testing.M) {
	worktree.NeutralizeGitEnvironment()
	os.Exit(m.Run())
}

func sampleRecord() Record {
	return Record{
		Template:       "web",
		Tag:            "web:v2",
		Source:         Source{Kind: "repo", URL: "git@h:o/r.git", Ref: "v1"},
		Commit:         "abc",
		DefinitionHash: "h1",
		From:           From{Ref: "docker.io/x/y:1", Digest: "sha256:aa"},
		BuiltAt:        time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC),
	}
}

func TestRecordRoundTrip(t *testing.T) {
	home := t.TempDir()
	want := sampleRecord()
	if err := WriteRecord(home, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRecord(home, "web")
	if err != nil {
		t.Fatal(err)
	}
	want.Schema = RecordSchema
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	info, err := os.Stat(recordPath(home, "web"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, err %v", info.Mode(), err)
	}
	_, err = ReadRecord(home, "absent")
	var fe *config.FileError
	if !errors.Is(err, fs.ErrNotExist) || !errors.As(err, &fe) {
		t.Fatalf("absent record: %v", err)
	}
}

func TestReadRecordRefusesAnotherSchema(t *testing.T) {
	home := t.TempDir()
	if err := WriteRecord(home, sampleRecord()); err != nil {
		t.Fatal(err)
	}
	p := recordPath(home, "web")
	raw, _ := os.ReadFile(p)
	os.WriteFile(p, []byte(strings.Replace(string(raw), "schema: 1", "schema: 9", 1)), 0o600)
	if _, err := ReadRecord(home, "web"); err == nil || !strings.Contains(err.Error(), "schema 9") {
		t.Fatalf("got %v", err)
	}
}

func TestReadRecordRefusesAMismatchedName(t *testing.T) {
	home := t.TempDir()
	if err := WriteRecord(home, sampleRecord()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(recordPath(home, "web"), recordPath(home, "other")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRecord(home, "other"); err == nil || !strings.Contains(err.Error(), `records template "web"`) {
		t.Fatalf("got %v", err)
	}
}

func TestTrustIsPerURL(t *testing.T) {
	home := t.TempDir()
	if ok, err := Trusted(home, "a"); ok || err != nil {
		t.Fatalf("empty home: %v %v", ok, err)
	}
	if err := Trust(home, "a"); err != nil {
		t.Fatal(err)
	}
	if err := Trust(home, "a"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := Trusted(home, "a"); !ok {
		t.Fatal("a not trusted")
	}
	if ok, _ := Trusted(home, "b"); ok {
		t.Fatal("b trusted")
	}
	if _, err := os.Stat(filepath.Join(home, "state", "v2", "template-sources.yaml")); err != nil {
		t.Fatal(err)
	}
}
