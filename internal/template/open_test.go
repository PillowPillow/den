package template

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/v2/internal/config"
)

const digest = "sha256:4a94000000000000000000000000000000000000000000000000000000000000"

func writeDefinition(t *testing.T, root, name string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "schema: 1\ntemplates:\n  " + name + ":\n    from: docker.io/docker/sandbox-templates:claude-code\n    kits: [ghcr.io/acme/kit:1]\n"
	if err := os.WriteFile(filepath.Join(root, "den-template.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func repoChoice(name, url string) Choice {
	return Choice{Value: name, Key: name, Entry: config.TemplateEntry{Repo: url},
		DeclaredIn: &config.Loaded{Path: "/proj/den.yaml", Dir: "/proj", File: config.File{}}}
}

func record(t *testing.T, home string, c Choice, commit, hash string) {
	t.Helper()
	err := WriteRecord(home, Record{Template: c.Name(), Tag: c.Tag(), Source: SourceOf(c), Commit: commit,
		DefinitionHash: hash, From: From{Ref: "docker.io/docker/sandbox-templates:claude-code", Digest: digest}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenRefusesAnUnbuiltTemplate(t *testing.T) {
	_, err := Open(t.TempDir(), repoChoice("digitaleo", "git@h:o/r.git"))
	if err == nil {
		t.Fatal("want an error")
	}
	want := "template \"digitaleo\" is not built — run `den build digitaleo` from /proj"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("got %q, want %q", err, want)
	}
}

func TestOpenUnbuiltOmitsFromForAGlobalTemplate(t *testing.T) {
	c := repoChoice("digitaleo", "git@h:o/r.git")
	c.DeclaredIn = &config.Loaded{Path: "/h/den.yaml", Dir: "/h", Global: true}
	_, err := Open(t.TempDir(), c)
	if err == nil || strings.Contains(err.Error(), " from ") || !strings.Contains(err.Error(), "`den build digitaleo`") {
		t.Fatalf("got %v", err)
	}
}

func TestOpenRefusesAnotherSource(t *testing.T) {
	home := t.TempDir()
	record(t, home, repoChoice("digitaleo", "git@h:o/a.git"), "c1", "h")
	_, err := Open(home, repoChoice("digitaleo", "git@h:o/b.git"))
	if err == nil {
		t.Fatal("want an error")
	}
	for _, s := range []string{"git@h:o/a.git", "git@h:o/b.git", "den build digitaleo --force"} {
		if !strings.Contains(err.Error(), s) {
			t.Fatalf("%q missing from %q", s, err)
		}
	}
}

func TestOpenRefusesAMissingCopy(t *testing.T) {
	home := t.TempDir()
	c := repoChoice("digitaleo", "git@h:o/r.git")
	record(t, home, c, "c1", "h")
	_, err := Open(home, c)
	if err == nil || !strings.Contains(err.Error(), "`den build digitaleo`") {
		t.Fatalf("got %v", err)
	}
}

func TestOpenReadsTheDefinitionFromTheCopy(t *testing.T) {
	home := t.TempDir()
	c := repoChoice("digitaleo", "git@h:o/r.git")
	record(t, home, c, "c1", "h")
	writeDefinition(t, CopyDir(home, "digitaleo"), "digitaleo")
	r, err := Open(home, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Def.Kits) != 1 || !strings.HasSuffix(r.Def.Kits[0], "ghcr.io/acme/kit:1") && r.Def.Kits[0] != "ghcr.io/acme/kit:1" {
		t.Fatalf("kits %v", r.Def.Kits)
	}
	if want := "docker.io/docker/sandbox-templates@" + digest; r.KitImage != want {
		t.Fatalf("KitImage %q, want %q", r.KitImage, want)
	}
	if r.Root() != CopyDir(home, "digitaleo") {
		t.Fatalf("Root %q", r.Root())
	}
}

func TestOpenRefusesARecordWithoutDigest(t *testing.T) {
	home := t.TempDir()
	c := repoChoice("digitaleo", "git@h:o/r.git")
	if err := WriteRecord(home, Record{Template: "digitaleo", Tag: c.Tag(), Source: SourceOf(c),
		From: From{Ref: "docker.io/x/y:1"}}); err != nil {
		t.Fatal(err)
	}
	writeDefinition(t, CopyDir(home, "digitaleo"), "digitaleo")
	_, err := Open(home, c)
	if err == nil || !strings.Contains(err.Error(), "`den build digitaleo`") || !strings.Contains(err.Error(), "docker.io/x/y:1") {
		t.Fatalf("got %v", err)
	}
}

func TestOpenRefusesADefinitionWithoutTheTemplate(t *testing.T) {
	home := t.TempDir()
	c := repoChoice("digitaleo", "git@h:o/r.git")
	record(t, home, c, "c1", "h")
	writeDefinition(t, CopyDir(home, "digitaleo"), "other")
	_, err := Open(home, c)
	if err == nil || !strings.Contains(err.Error(), filepath.Join(CopyDir(home, "digitaleo"), "den-template.yaml")) ||
		!strings.Contains(err.Error(), "`den build digitaleo`") {
		t.Fatalf("got %v", err)
	}
}

func TestOpenReadsADirTemplateInPlace(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	writeDefinition(t, dir, "legacy")
	c := Choice{Value: "legacy", Key: "legacy", Entry: config.TemplateEntry{Dir: dir}}
	record(t, home, c, "", "h")
	r, err := Open(home, c)
	if err != nil {
		t.Fatal(err)
	}
	if r.Root() != dir || r.Defs == nil {
		t.Fatalf("Root %q Defs %v", r.Root(), r.Defs)
	}
}

func TestOpenOnAnImageReadsNothing(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	c := Choice{Value: "x", Image: "docker.io/docker/sandbox-templates:x"}
	r, err := Open(home, c)
	if err != nil {
		t.Fatal(err)
	}
	if r.KitImage != c.Image || r.Defs != nil || r.Record != nil || r.Root() != "" {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(home); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Open touched the den home")
	}
}
