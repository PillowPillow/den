package deninit

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/PillowPillow/den/internal/config"
)

// fakeSrc is a deliberately trivial tree: deninit must not owe its result to
// the real examples/den-home content, only to config.GlobalPath's location
// and to what src happens to contain.
var fakeSrc = fstest.MapFS{
	"config.yaml":            {Data: []byte("defaults:\n  agent: claude\n"), Mode: 0o644},
	"nests/example.yaml":     {Data: []byte("stack: devx\n"), Mode: 0o644},
	"stacks/devx/stack.yaml": {Data: []byte("kind: local\n"), Mode: 0o644},
}

func TestRunCreatesTheThreeFilesAtTheirRelativePaths(t *testing.T) {
	home := t.TempDir()
	var out bytes.Buffer

	if err := Run(home, fakeSrc, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for rel, want := range map[string]string{
		"config.yaml":            "defaults:\n  agent: claude\n",
		"nests/example.yaml":     "stack: devx\n",
		"stacks/devx/stack.yaml": "kind: local\n",
	} {
		path := filepath.Join(home, filepath.FromSlash(rel))
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		if string(got) != want {
			t.Errorf("%s content = %q, want %q", rel, got, want)
		}
		// fstest.MapFS's own Mode field plays no part here — Run writes
		// 0o644 explicitly (deninit.go), so this checks Run's choice, not
		// whatever fakeSrc happened to declare.
		if info, err := os.Stat(path); err != nil {
			t.Errorf("stat %s: %v", rel, err)
		} else if perm := info.Mode().Perm(); perm != 0o644 {
			t.Errorf("%s permissions = %o, want 0644", rel, perm)
		}
	}

	for _, dir := range []string{home, filepath.Join(home, "nests"), filepath.Join(home, "stacks", "devx")} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat %s: %v", dir, err)
		}
		if perm := info.Mode().Perm(); perm != 0o755 {
			t.Errorf("%s permissions = %o, want 0755", dir, perm)
		}
	}

	// The brief is explicit that Run must NOT pre-create these — they are
	// either created lazily by code that already exists (spawn's agent
	// profile dir, mixin's cache) or an optional convention no loader
	// consumes (lib/). Creating any of them here would add a fourth
	// permissions policy to defend for no reader that needs it.
	for _, dir := range []string{"agents", filepath.Join("cache", "mixins"), "worktrees", "lib"} {
		if _, err := os.Stat(filepath.Join(home, dir)); !os.IsNotExist(err) {
			t.Errorf("%s must not be pre-created by Run, stat err = %v", dir, err)
		}
	}
}

// TestRunAcceptsAPreexistingEmptyDirectory is the `mkdir ~/.den` case the
// brief calls out: t.TempDir() already returns an existing, empty directory,
// so the directory merely existing must not itself be read as "already
// initialized".
func TestRunAcceptsAPreexistingEmptyDirectory(t *testing.T) {
	home := t.TempDir()
	var out bytes.Buffer

	if err := Run(home, fakeSrc, &out); err != nil {
		t.Fatalf("Run on a preexisting empty dir: %v", err)
	}
	if _, err := os.Stat(config.GlobalPath(home)); err != nil {
		t.Fatalf("config.yaml missing after Run: %v", err)
	}
}

// TestRunCreatesTheHomeDirectoryWhenItDoesNotExistYet covers the OTHER end of
// that same case: a den home that has never been touched at all, where even
// the top-level directory is missing (the common case for a fresh install,
// as opposed to the `mkdir ~/.den` case above). Run must create it, not
// require the caller to.
func TestRunCreatesTheHomeDirectoryWhenItDoesNotExistYet(t *testing.T) {
	home := filepath.Join(t.TempDir(), "den-home-not-yet-created")
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("test setup: %s must not exist yet, stat err = %v", home, err)
	}
	var out bytes.Buffer

	if err := Run(home, fakeSrc, &out); err != nil {
		t.Fatalf("Run on a den home that does not exist yet: %v", err)
	}
	if _, err := os.Stat(config.GlobalPath(home)); err != nil {
		t.Fatalf("config.yaml missing after Run: %v", err)
	}
}

// TestAPartialRunLeavesTheHomeReRunnable locks down the write order in Run:
// config.yaml (the sentinel Run's own refusal probe checks) must be written
// LAST, so a failure partway through never leaves a home holding ONLY the
// sentinel — which a retry would then refuse as "already initialized"
// despite never having actually completed.
//
// The obstruction is a stray FILE at <home>/nests: fakeSrc's alphabetical
// order (after the sentinel is moved to the end) writes nests/example.yaml
// before stacks/devx/stack.yaml and before config.yaml, so MkdirAll(<home>/
// nests, ...) fails on the FIRST file, before Run ever reaches the sentinel.
func TestAPartialRunLeavesTheHomeReRunnable(t *testing.T) {
	home := t.TempDir()
	strayFile := filepath.Join(home, "nests")
	if err := os.WriteFile(strayFile, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("seeding the stray file at %s: %v", strayFile, err)
	}
	var out bytes.Buffer

	if err := Run(home, fakeSrc, &out); err == nil {
		t.Fatal("expected Run to fail: nests/ can't be created where a file already sits")
	}
	if _, statErr := os.Stat(config.GlobalPath(home)); !os.IsNotExist(statErr) {
		t.Fatalf("config.yaml must NOT exist after a failed Run — it would wrongly refuse the retry below, stat err = %v", statErr)
	}

	// Fix the obstruction and retry: THIS is the property under test — a
	// home left behind by a failed Run must still be completable, not
	// permanently bricked behind "already initialized".
	if err := os.Remove(strayFile); err != nil {
		t.Fatalf("removing the stray file: %v", err)
	}
	if err := Run(home, fakeSrc, &out); err != nil {
		t.Fatalf("retry after fixing the obstruction: %v", err)
	}
	if _, statErr := os.Stat(config.GlobalPath(home)); statErr != nil {
		t.Fatalf("config.yaml missing after the successful retry: %v", statErr)
	}
}

func TestRunRefusesWhenConfigYamlAlreadyExists(t *testing.T) {
	home := t.TempDir()
	existing := "defaults:\n  agent: mine\n"
	if err := os.WriteFile(config.GlobalPath(home), []byte(existing), 0o644); err != nil {
		t.Fatalf("seeding config.yaml: %v", err)
	}
	var out bytes.Buffer

	err := Run(home, fakeSrc, &out)
	if err == nil {
		t.Fatal("Run: expected a refusal, got nil")
	}
	if !strings.Contains(err.Error(), "already initialized") || !strings.Contains(err.Error(), config.GlobalPath(home)) {
		t.Errorf("error = %q, want it to name %q and say \"already initialized\"", err, config.GlobalPath(home))
	}

	// The existing config.yaml must survive untouched...
	got, readErr := os.ReadFile(config.GlobalPath(home))
	if readErr != nil {
		t.Fatalf("reading config.yaml after refusal: %v", readErr)
	}
	if string(got) != existing {
		t.Errorf("config.yaml was overwritten: got %q, want %q", got, existing)
	}
	// ...and nothing else must have been written either — a refusal writes
	// NOTHING, not "everything except the file that already existed".
	if _, statErr := os.Stat(filepath.Join(home, "nests", "example.yaml")); !os.IsNotExist(statErr) {
		t.Errorf("nests/example.yaml exists after a refused Run: stat err = %v", statErr)
	}
}
