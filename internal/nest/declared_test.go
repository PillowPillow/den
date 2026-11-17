package nest

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDeclaredOriginsReadsProjectIndexAndGlobal(t *testing.T) {
	home, proj, other := t.TempDir(), t.TempDir(), t.TempDir()
	g := write(t, home, "den.yaml", "schema: 1\nrepos:\n  api: {origin: 'git@gitlab.com:dig/api.git', path: /src/api}\n  bare: /src/bare\n")
	p := write(t, proj, "den.yaml", "schema: 1\nrepos:\n  web: {origin: 'git@gitlab.com:dig/web.git'}\n  local: {path: /x/local}\n")
	o := write(t, other, "den.yaml", "schema: 1\nrepos:\n  lib: {origin: 'git@gitlab.com:dig/lib.git'}\n")
	write(t, home, "state/v2/nests.yaml", "nests:\n  a: "+p+"\n  b: "+o+"\n")
	got, err := DeclaredOrigins(home, proj)
	if err != nil {
		t.Fatal(err)
	}
	want := []Declared{
		{Key: "api", Origin: "git@gitlab.com:dig/api.git", Identity: "gitlab.com/dig/api", File: g},
		{Key: "web", Origin: "git@gitlab.com:dig/web.git", Identity: "gitlab.com/dig/web", File: p},
		{Key: "lib", Origin: "git@gitlab.com:dig/lib.git", Identity: "gitlab.com/dig/lib", File: o},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestDeclaredOriginsSkipsAnUnreadableIndexedFile(t *testing.T) {
	home, proj, other := t.TempDir(), t.TempDir(), t.TempDir()
	write(t, home, "den.yaml", "schema: 1\n")
	write(t, proj, "den.yaml", "schema: 1\nrepos:\n  web: {origin: 'git@gitlab.com:dig/web.git'}\n")
	broken := write(t, other, "den.yaml", "schema: 1\nbogus: 1\n")
	missing := filepath.Join(other, "gone", "den.yaml")
	write(t, home, "state/v2/nests.yaml", "nests:\n  a: "+broken+"\n  b: "+missing+"\n")
	got, err := DeclaredOrigins(home, proj)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Key != "web" {
		t.Errorf("got %+v", got)
	}
}

func TestDeclaredOriginsRefusesABrokenProjectFile(t *testing.T) {
	home, proj := t.TempDir(), t.TempDir()
	write(t, proj, "den.yaml", "schema: 1\nbogus: 1\n")
	if _, err := DeclaredOrigins(home, proj); err == nil {
		t.Fatal("want an error")
	}
}

func TestDeclaredOriginsDoesNotClaim(t *testing.T) {
	home, proj := t.TempDir(), t.TempDir()
	write(t, home, "den.yaml", nestFile("g"))
	write(t, proj, "den.yaml", nestFile("api")+"repos:\n  web: {origin: 'git@gitlab.com:dig/web.git'}\n")
	idx := write(t, home, "state/v2/nests.yaml", "nests: {}\n")
	before, _ := os.ReadFile(idx)
	if _, err := DeclaredOrigins(home, proj); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(idx)
	if string(before) != string(after) {
		t.Errorf("nests.yaml changed: %q -> %q", before, after)
	}
}

func TestDeclaredOriginsSortsByFileThenKey(t *testing.T) {
	root := t.TempDir()
	home, proj, other := filepath.Join(root, "z"), filepath.Join(root, "b"), filepath.Join(root, "a")
	g := write(t, home, "den.yaml", "schema: 1\nrepos:\n  zg: {origin: 'git@gitlab.com:d/zg.git', path: /s/zg}\n  ag: {origin: 'git@gitlab.com:d/ag.git', path: /s/ag}\n")
	p := write(t, proj, "den.yaml", "schema: 1\nrepos:\n  zp: {origin: 'git@gitlab.com:d/zp.git'}\n  ap: {origin: 'git@gitlab.com:d/ap.git'}\n")
	o := write(t, other, "den.yaml", "schema: 1\nrepos:\n  zo: {origin: 'git@gitlab.com:d/zo.git'}\n  ao: {origin: 'git@gitlab.com:d/ao.git'}\n")
	write(t, home, "state/v2/nests.yaml", "nests:\n  n: "+o+"\n")
	got, err := DeclaredOrigins(home, proj)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, d := range got {
		order = append(order, d.File+"|"+d.Key)
	}
	want := []string{o + "|ao", o + "|zo", p + "|ap", p + "|zp", g + "|ag", g + "|zg"}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("got %v\nwant %v", order, want)
	}
}

func TestDeclaredOriginsListsAFileOnceWhenTwoNestsIndexIt(t *testing.T) {
	home, proj, other := t.TempDir(), t.TempDir(), t.TempDir()
	write(t, home, "den.yaml", "schema: 1\n")
	write(t, proj, "den.yaml", "schema: 1\n")
	o := write(t, other, "den.yaml", "schema: 1\nrepos:\n  lib: {origin: 'git@gitlab.com:dig/lib.git'}\n")
	write(t, home, "state/v2/nests.yaml", "nests:\n  a: "+o+"\n  b: "+o+"\n")
	got, err := DeclaredOrigins(home, proj)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("got %+v", got)
	}
}

func TestDeclaredOriginsRefusesABrokenGlobalFile(t *testing.T) {
	home, proj := t.TempDir(), t.TempDir()
	write(t, home, "den.yaml", "schema: 1\nbogus: 1\n")
	if _, err := DeclaredOrigins(home, proj); err == nil {
		t.Fatal("want an error")
	}
}
