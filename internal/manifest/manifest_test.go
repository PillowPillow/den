package manifest

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// sample is the manifest of a worktree'd source nest mounting one keyed repo
// and one repo given on the command line — the composition every later reader
// has to survive.
func sample() Manifest {
	return Manifest{
		Sandbox: "corp-api.feat12",
		Nest:    Nest{Ref: "corp:api", File: "/home/x/.den/sources/corp/nests/api.yaml"},
		Worktree: &Worktree{
			Name:   "feat12",
			Branch: "feature/12",
			Layout: "central",
			Root:   "/home/x/.den/worktrees",
		},
		Repos: []Repo{
			{
				Name:     "api",
				Origin:   OriginKey,
				Key:      "api",
				Repo:     "/home/x/dev/api",
				Mount:    "/home/x/.den/worktrees/feat12/api",
				Worktree: true,
			},
			{
				Name:   "hotfix",
				Origin: OriginCommandLine,
				Repo:   "/tmp/hotfix",
				Mount:  "/tmp/hotfix",
			},
		},
		GitDirs: []string{"/home/x/dev/api/.git"},
	}
}

// The round-trip is the property the whole feature rests on: rm replays what
// spawn wrote, and a field lost between the two would silently stop a worktree
// from being reclaimed. Modelled on TestReadMixinRereadsWhatWriteMixinWrote.
func TestReadRereadsWhatWriteWrote(t *testing.T) {
	denHome := t.TempDir()
	want := sample()
	if err := Write(denHome, want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Read(denHome, want.Sandbox)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	want.Schema = Schema // Write stamps it; the caller does not
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round-trip lost data:\n got %#v\nwant %#v", got, want)
	}
}

// The golden is what a human reads when a `den rm` misbehaves. It is compared
// by hand: this repo has no -update flag, on purpose.
func TestWriteRendersTheGoldenFile(t *testing.T) {
	denHome := t.TempDir()
	if err := Write(denHome, sample()); err != nil {
		t.Fatal(err)
	}
	path, err := Path(denHome, "corp-api.feat12")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "manifest.golden"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("rendered manifest differs from testdata/manifest.golden\n got:\n%s\nwant:\n%s", got, want)
	}
}

// A spawn without -w must render NO worktree block: a block of empty strings
// would read as a worktree literally named "", and worktree.Remove's own guard
// against that name exists because it erases the worktree root's parent.
func TestWriteOmitsTheWorktreeBlockWithoutAWorktree(t *testing.T) {
	denHome := t.TempDir()
	m := sample()
	m.Sandbox = "api"
	m.Worktree = nil
	m.Repos = []Repo{{Name: "api", Origin: OriginPath, Repo: "/dev/api", Mount: "/dev/api"}}
	if err := Write(denHome, m); err != nil {
		t.Fatal(err)
	}
	path, _ := Path(denHome, "api")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "worktree:\n") {
		t.Errorf("no worktree block expected:\n%s", content)
	}
}

// Strict decoding, like every other den decode: a mistyped key must be a load
// error, not a silence that leaves rm with nothing to reclaim.
func TestReadRefusesAnUnknownKey(t *testing.T) {
	denHome := t.TempDir()
	if err := os.MkdirAll(Dir(denHome), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(Dir(denHome), "api.yaml")
	if err := os.WriteFile(path, []byte("schema: 1\nsandbox: api\nworktre: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(denHome, "api"); err == nil {
		t.Fatal("an unknown key must be refused")
	} else if !strings.Contains(err.Error(), path) {
		t.Errorf("the message must name the file; got: %v", err)
	}
}

// A manifest from another den must not be decoded optimistically: a field
// whose MEANING changed would send worktree.Remove at a directory this writer
// never created. Readers treat the refusal as "no usable manifest".
func TestReadRefusesAnotherSchema(t *testing.T) {
	denHome := t.TempDir()
	if err := os.MkdirAll(Dir(denHome), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(Dir(denHome), "api.yaml"),
		[]byte("schema: 2\nsandbox: api\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(denHome, "api"); err == nil {
		t.Fatal("another schema must be refused")
	}
}

// Absence is distinguishable from corruption, because the two get different
// messages from rm: a mention for a legacy sandbox, a warning naming the file
// for a corrupt one.
func TestReadReportsAbsenceAsNotExist(t *testing.T) {
	_, err := Read(t.TempDir(), "api")
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("absence must surface as os.ErrNotExist; got: %v", err)
	}
}

// The name becomes a path, so it is validated before it is joined. Without
// this, a name `sbx ls` reports but den would never create escapes state/.
func TestPathRefusesAHostileName(t *testing.T) {
	if _, err := Path("/den", "api/../../evade"); err == nil {
		t.Fatal("a name that is not a legal sandbox name must be refused")
	}
}

// List is the orphan scan's input: one bad file must not hide every good one,
// and a den home that never spawned has no state/ at all.
func TestListSkipsBrokenFilesAndToleratesNoStateDir(t *testing.T) {
	empty, broken, err := List(t.TempDir())
	if err != nil || len(empty) != 0 || len(broken) != 0 {
		t.Fatalf("a den home without state/ must list nothing: %v %v %v", empty, broken, err)
	}

	denHome := t.TempDir()
	if err := Write(denHome, sample()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(Dir(denHome), "bad.yaml"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	good, bad, err := List(denHome)
	if err != nil {
		t.Fatal(err)
	}
	if len(good) != 1 || good[0].Sandbox != "corp-api.feat12" {
		t.Errorf("the readable manifest must survive its broken neighbour: %#v", good)
	}
	if len(bad) != 1 || !strings.HasSuffix(bad[0].Path, "bad.yaml") {
		t.Errorf("the broken file must be named: %#v", bad)
	}
}

// The lax reader's whole reason to exist: the record Read refuses is usually a
// NEWER den's — an unknown schema, an unknown key, and mounts that are still
// exactly the paths sbx received. `den rm`'s guard has to see those mounts, or
// it moves a live sandbox's workspace to the trash.
func TestLaxMountsReadsWhatReadRefuses(t *testing.T) {
	denHome := t.TempDir()
	if err := os.MkdirAll(Dir(denHome), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(Dir(denHome), "api.reco.yaml")
	content := "schema: 9999\nsandbox: api.reco\ninvented_by_a_newer_den: yes\n" +
		"repos:\n  - name: api\n    mount: /w/feat12\n    unknown_key: 1\n  - name: web\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(denHome, "api.reco"); err == nil {
		t.Fatal("this file must still be refused by the strict reader")
	}
	mounts, err := LaxMounts(path)
	if err != nil {
		t.Fatalf("the lax reader must accept it: %v", err)
	}
	// The second repo names no mount and contributes nothing: it is not an
	// error, it genuinely names no directory.
	if len(mounts) != 1 || mounts[0] != "/w/feat12" {
		t.Errorf("mounts must be read, and only mounts; got %#v", mounts)
	}
}

// An error means "this file answers nothing" — the caller then treats it as an
// unknown sharer and reclaims nothing. Both states reach it: unparseable, and
// unreadable (List reports the second as Broken too).
func TestLaxMountsFailsOnAFileItCannotRead(t *testing.T) {
	denHome := t.TempDir()
	if err := os.MkdirAll(Dir(denHome), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(Dir(denHome), "api.reco.yaml")
	if err := os.WriteFile(path, []byte("repos: [ {mount: /w/feat12\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LaxMounts(path); err == nil {
		t.Error("a file that does not parse must be an error, not an empty answer")
	} else if !strings.Contains(err.Error(), path) {
		t.Errorf("the message must name the file; got: %v", err)
	}
	if _, err := LaxMounts(filepath.Join(Dir(denHome), "absent.yaml")); err == nil {
		t.Error("a file that cannot be read must be an error too")
	}
}

// Removing what is already gone is not a failure: rm calls this after
// reclaiming everything, and refusing there would fail a `den rm` that did its
// whole job (doctrine T13/T16).
func TestRemoveToleratesAnAbsentFile(t *testing.T) {
	if err := Remove(t.TempDir(), "api"); err != nil {
		t.Errorf("removing an absent manifest must not fail: %v", err)
	}
}
