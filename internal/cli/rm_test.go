package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/registry"
)

// mountedAm records am as a project instance on one repo den mounted in place, so rm never runs git.
func (f *upFixture) mountedAm(t *testing.T) registry.Record {
	t.Helper()
	api := f.dir(t, "api")
	run := filepath.Join(f.proj, ".den", "run", "am")
	rec := registry.Record{
		Instance: "am", Nest: "am", ConfigFile: filepath.Join(f.proj, "den.yaml"), Project: f.proj,
		RunDir: run, GenDir: filepath.Join(f.home, "gen", "am"),
		EnvFiles: []string{writeEnvFile(t, filepath.Join(run, "sbxenv.yaml"))}, EnvHashes: []string{envFileHash}, Start: api,
		Repos: []registry.Repo{{Name: "api", Repo: api, Mount: api, Target: api}},
	}
	f.record(t, rec)
	return rec
}

const envFileBody = "schemaVersion: \"1\"\n"

// envFileHash is the sha256 of envFileBody, as Create records it.
var envFileHash = func() string { sum := sha256.Sum256([]byte(envFileBody)); return hex.EncodeToString(sum[:]) }()

func writeEnvFile(t *testing.T, path string) string {
	t.Helper()
	writeTestFile(t, path, envFileBody)
	return path
}

func TestRmCmdRefusesWithoutARecord(t *testing.T) {
	f := newUpFixture(t)
	err := f.up2("rm", "am")
	want := "no den V2 record for \"am\" — den only removes what it created; use `sbx rm am`"
	if err == nil || err.Error() != want {
		t.Fatalf("error %v, want %q", err, want)
	}
	if len(f.fake.Calls) != 0 {
		t.Errorf("sbx calls %v, want none", f.fake.Calls)
	}
}

func TestRmCmdRemovesTheVMThroughEnvRm(t *testing.T) {
	f := newUpFixture(t)
	rec := f.mountedAm(t)
	f.live("am")

	if err := f.up2("rm", "am", "--force"); err != nil {
		t.Fatal(err)
	}
	want := append([]string{"--no-input", "env", "rm", "--force", "--skip-host-commands"}, rec.EnvFiles...)
	if !slices.ContainsFunc(f.fake.Calls, func(c []string) bool { return slices.Equal(c, want) }) {
		t.Errorf("calls %v, want %v", f.fake.Calls, want)
	}
	if _, err := registry.Read(f.home, "am"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("record survived: %v", err)
	}
	if _, err := os.Stat(rec.Repos[0].Repo); err != nil {
		t.Errorf("rm touched the mounted repo: %v", err)
	}
}

func TestRmCmdNeedsOneInstance(t *testing.T) {
	f := newUpFixture(t)
	if err := f.up2("rm"); err == nil || !strings.Contains(err.Error(), "accepts 1 arg") {
		t.Fatalf("error %v, want cobra's arity refusal", err)
	}
	if len(f.fake.Calls) != 0 {
		t.Errorf("sbx calls %v, want none", f.fake.Calls)
	}
}
