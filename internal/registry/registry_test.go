package registry

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func sample() Record {
	return Record{
		Instance:   "api.feat-12",
		Nest:       "api",
		Template:   "claude-code-docker",
		Tag:        "claude-code-docker:v2",
		ConfigFile: "/p/den.yaml",
		Project:    "/p",
		RunDir:     "/p/.den/run/api.feat-12",
		GenDir:     "/h/.den/gen",
		EnvFiles:   []string{"/h/.den/gen/base.yaml", "/p/.den/run/api.feat-12/sbxenv.yaml"},
		EnvHashes:  []string{"e3b0c442", "9f86d081"},
		ConfigHash: "abc123",
		Start:      "claude",
		Worktree: &Worktree{
			Branch: "feature/12",
			Dir:    "feat-12",
			Layout: "central",
			Root:   "/h/.den/wt",
		},
		Repos: []Repo{
			{Name: "api", Repo: "/dev/api", Mount: "/h/.den/wt/feat-12/api", Target: "/work/api", Worktree: true},
			{Name: "lib", Repo: "/dev/lib", Mount: "/dev/lib", Target: "/work/lib"},
		},
	}
}

func TestReadRereadsWhatWriteWrote(t *testing.T) {
	denHome := t.TempDir()
	want := sample()
	if err := Write(denHome, want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Read(denHome, want.Instance)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	want.Schema = Schema
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round-trip lost data:\n got %#v\nwant %#v", got, want)
	}
}

func TestWriteRendersTheGoldenFile(t *testing.T) {
	denHome := t.TempDir()
	if err := Write(denHome, sample()); err != nil {
		t.Fatal(err)
	}
	path, err := Path(denHome, "api.feat-12")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "record.golden"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("record differs from testdata/record.golden:\n--- got\n%s\n--- want\n%s", got, want)
	}
}

func writeRaw(t *testing.T, denHome, name, content string) string {
	t.Helper()
	path, err := Path(denHome, name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadRefusesAnUnknownKey(t *testing.T) {
	denHome := t.TempDir()
	writeRaw(t, denHome, "api", "schema: 2\ninstance: api\nworktre: {}\n")
	_, err := Read(denHome, "api")
	if err == nil || !strings.Contains(err.Error(), "worktre") {
		t.Fatalf("want an error naming the unknown key, got %v", err)
	}
}

func TestWriteStampsSchemaTwo(t *testing.T) {
	denHome := t.TempDir()
	r := sample()
	r.Schema = 7
	if err := Write(denHome, r); err != nil {
		t.Fatal(err)
	}
	got, err := Read(denHome, r.Instance)
	if err != nil {
		t.Fatal(err)
	}
	if got.Schema != 2 || Schema != 2 {
		t.Errorf("schema %d (const %d), want 2", got.Schema, Schema)
	}
}

func TestReadNamesTheRemedyForSchemaOne(t *testing.T) {
	denHome := t.TempDir()
	path := writeRaw(t, denHome, "am", "schema: 1\ninstance: am\n")
	_, err := Read(denHome, "am")
	want := path + ": schema 1 was written by a den before templates; this den cannot act on it — remove the VM with `sbx rm am`, then delete " + path
	if err == nil || err.Error() != want {
		t.Fatalf("got %v\nwant %s", err, want)
	}
}

func TestReadRefusesAnotherSchema(t *testing.T) {
	denHome := t.TempDir()
	writeRaw(t, denHome, "api", "schema: 3\ninstance: api\n")
	_, err := Read(denHome, "api")
	if err == nil || !strings.Contains(err.Error(), "schema 3") {
		t.Fatalf("want a schema refusal, got %v", err)
	}
	if errors.Is(err, fs.ErrNotExist) {
		t.Error("a schema refusal must not look like absence")
	}
}

func TestReadReportsAbsenceAsNotExist(t *testing.T) {
	_, err := Read(t.TempDir(), "api")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("want fs.ErrNotExist, got %v", err)
	}
}

func TestPathRefusesAHostileName(t *testing.T) {
	if _, err := Path(t.TempDir(), "../x"); err == nil {
		t.Fatal("Path accepted ../x")
	}
	if err := Write(t.TempDir(), Record{Instance: "../x"}); err == nil {
		t.Fatal("Write accepted ../x")
	}
	if err := Remove(t.TempDir(), "../x"); err == nil {
		t.Fatal("Remove accepted ../x")
	}
}

func TestListReturnsBrokenFiles(t *testing.T) {
	denHome := t.TempDir()
	for _, n := range []string{"zed", "api"} {
		r := sample()
		r.Instance = n
		if err := Write(denHome, r); err != nil {
			t.Fatal(err)
		}
	}
	bad := writeRaw(t, denHome, "bad", "schema: 9\n")
	got, broken, err := List(denHome)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Instance != "api" || got[1].Instance != "zed" {
		t.Errorf("records = %#v", got)
	}
	if len(broken) != 1 || broken[0].Path != bad || broken[0].Err == nil {
		t.Errorf("broken = %#v", broken)
	}
}

func TestListToleratesNoStateDir(t *testing.T) {
	got, broken, err := List(t.TempDir())
	if err != nil || got != nil || broken != nil {
		t.Fatalf("got %v %v %v", got, broken, err)
	}
}

func TestRemoveToleratesAbsence(t *testing.T) {
	denHome := t.TempDir()
	if err := Write(denHome, sample()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := Remove(denHome, "api.feat-12"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Read(denHome, "api.feat-12"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("record survived Remove: %v", err)
	}
}

func TestDirNeverPointsAtV1State(t *testing.T) {
	if d := Dir("/h/.den"); strings.Contains(d, filepath.Join("state", "sandboxes")) {
		t.Errorf("Dir = %s collides with v1 state", d)
	}
}

func TestReadRefusesARecordWhoseInstanceDiffersFromItsFile(t *testing.T) {
	denHome := t.TempDir()
	path := writeRaw(t, denHome, "api", "schema: 2\ninstance: web\n")
	_, err := Read(denHome, "api")
	if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), `"web"`) {
		t.Fatalf("want a refusal naming %s and the recorded instance, got %v", path, err)
	}
	if errors.Is(err, fs.ErrNotExist) {
		t.Error("a mismatch refusal must not look like absence")
	}
}

func TestListReportsARecordWhoseInstanceDiffersFromItsFileAsBroken(t *testing.T) {
	denHome := t.TempDir()
	path := writeRaw(t, denHome, "api", "schema: 2\ninstance: web\n")
	got, broken, err := List(denHome)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 || len(broken) != 1 || broken[0].Path != path {
		t.Errorf("records %#v, broken %#v", got, broken)
	}
}
