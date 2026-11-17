package template

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/PillowPillow/den/v2/internal/config"
)

func lsRemote(out string, err error) (fakeGit, *int) {
	n := new(int)
	return func(context.Context, string, []string) ([]byte, error) {
		*n++
		return []byte(out), err
	}, n
}

func openRepo(t *testing.T) *Resolved {
	t.Helper()
	home := t.TempDir()
	c := repoChoice("digitaleo", "git@h:o/r.git")
	record(t, home, c, "89abcdef0123456789abcdef0123456789abcdef", "h")
	writeDefinition(t, CopyDir(home, "digitaleo"), "digitaleo")
	r, err := Open(home, c)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestNoticeOnANewerRemote(t *testing.T) {
	g, _ := lsRemote("0123abcd00000000000000000000000000000000\tHEAD\n", nil)
	got := Notices(context.Background(), g, openRepo(t))
	want := "template digitaleo: a newer definition is available (89abcdef -> 0123abcd); run 'den build digitaleo --pull'"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %q", got)
	}
}

func TestNoticeIsSilentWhenUpToDate(t *testing.T) {
	g, _ := lsRemote("89abcdef0123456789abcdef0123456789abcdef\tHEAD\n", nil)
	if got := Notices(context.Background(), g, openRepo(t)); len(got) != 0 {
		t.Fatalf("got %q", got)
	}
}

func TestNoticeIsSilentOnError(t *testing.T) {
	g, _ := lsRemote("", errors.New("timeout"))
	if got := Notices(context.Background(), g, openRepo(t)); len(got) != 0 {
		t.Fatalf("got %q", got)
	}
}

func TestNoticeOnAnImageRunsNoGit(t *testing.T) {
	g, n := lsRemote("", nil)
	r, _ := Open(t.TempDir(), Choice{Value: "x", Image: "docker.io/docker/sandbox-templates:x"})
	if got := Notices(context.Background(), g, r); got != nil || *n != 0 {
		t.Fatalf("got %q, %d git calls", got, *n)
	}
}

func TestNoticeOnADriftedDir(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	writeDefinition(t, dir, "legacy")
	c := Choice{Value: "legacy", Key: "legacy", Entry: config.TemplateEntry{Dir: dir}}
	d, err := config.LoadDefinitions(dir)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := DefinitionHash(d, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	record(t, home, c, "", hash)
	g, n := lsRemote("", nil)
	r, err := Open(home, c)
	if err != nil {
		t.Fatal(err)
	}
	if got := Notices(context.Background(), g, r); len(got) != 0 {
		t.Fatalf("unchanged dir noticed: %q", got)
	}
	os.WriteFile(filepath.Join(dir, "den-template.yaml"), []byte("schema: 1\ntemplates:\n  legacy:\n    from: docker.io/x/y:2\n"), 0o644)
	r, err = Open(home, c)
	if err != nil {
		t.Fatal(err)
	}
	got := Notices(context.Background(), g, r)
	want := "template legacy: its definition changed since the last build; run 'den build legacy'"
	if len(got) != 1 || got[0] != want || *n != 0 {
		t.Fatalf("got %q, %d git calls", got, *n)
	}
}
