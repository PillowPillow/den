package repos

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeTableFile(t *testing.T, home, content string) string {
	t.Helper()
	path := TablePath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadTableMissingIsEmpty(t *testing.T) {
	got, err := ReadTable(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got.Repos == nil || len(got.Repos) != 0 {
		t.Fatalf("Repos = %#v, want empty non-nil map", got.Repos)
	}
}

func TestTableRoundTrip(t *testing.T) {
	home := t.TempDir()
	in := Table{Repos: map[string]Entry{
		"gitlab.com/me/api": {Path: "/src/api"},
		"github.com/me/web": {Path: "/src/web"},
	}}
	if err := WriteTable(home, in); err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "state", "v2", "repos.yaml"); TablePath(home) != want {
		t.Fatalf("TablePath = %s, want %s", TablePath(home), want)
	}
	got, err := ReadTable(home)
	if err != nil {
		t.Fatal(err)
	}
	if got.Schema != TableSchema || !reflect.DeepEqual(got.Repos, in.Repos) {
		t.Fatalf("got %#v, want schema %d and %#v", got, TableSchema, in.Repos)
	}
}

func TestReadTableRefusesUnknownKey(t *testing.T) {
	home := t.TempDir()
	path := writeTableFile(t, home, "schema: 1\nrepos:\n  gitlab.com/me/api:\n    path: /src/api\n    url: https://tok@gitlab.com/me/api\n")
	_, err := ReadTable(home)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v, want a refusal naming %s", err, path)
	}
}

func TestReadTableNamesTheRemedy(t *testing.T) {
	corrupt := t.TempDir()
	path := writeTableFile(t, corrupt, "schema: 1\nrepos: [\n")
	unreadable := t.TempDir()
	if err := os.MkdirAll(TablePath(unreadable), 0o700); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	otherPath := writeTableFile(t, other, "schema: 2\nrepos: {}\n")
	for home, file := range map[string]string{corrupt: path, unreadable: TablePath(unreadable), other: otherPath} {
		_, err := ReadTable(home)
		want := "delete " + file + " and run `den scan`"
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want the remedy %q", err, want)
		}
	}
}

func TestReadTableRefusesOtherSchema(t *testing.T) {
	home := t.TempDir()
	path := writeTableFile(t, home, "schema: 2\nrepos: {}\n")
	_, err := ReadTable(home)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v, want a refusal naming %s", err, path)
	}
}
