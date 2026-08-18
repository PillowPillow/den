package selfupdate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// assertGolden compares against a file edited BY HAND: this repo has no
// -update flag for goldens, on purpose.
func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if got != string(want) {
		t.Fatalf("golden %s mismatch\n got: %q\nwant: %q", name, got, string(want))
	}
}

func TestMethodRefusalHomebrew(t *testing.T) {
	err := MethodRefusal(MethodHomebrew, "/opt/homebrew/Caskroom/den/1.8.1/den")
	assertGolden(t, "refusal_homebrew.golden", err.Error())
}

func TestMethodRefusalGoInstall(t *testing.T) {
	err := MethodRefusal(MethodGoInstall, "/Users/dev/go/bin/den")
	assertGolden(t, "refusal_goinstall.golden", err.Error())
}

func TestMethodRefusalArchiveIsNil(t *testing.T) {
	if err := MethodRefusal(MethodArchive, "/Users/dev/.local/bin/den"); err != nil {
		t.Fatalf("MethodArchive must not refuse, got %v", err)
	}
}

func TestVersionErrorText(t *testing.T) {
	err := &VersionError{Observed: "v1.5.0-17-g0ec48d8-dirty"}
	assertGolden(t, "refusal_version.golden", err.Error())
}

func TestWriteErrorTextAndUnwrap(t *testing.T) {
	err := &WriteError{Dir: "/usr/local/bin", Err: os.ErrPermission}
	assertGolden(t, "refusal_write.golden", err.Error())
	if !errors.Is(err, os.ErrPermission) {
		t.Fatal("WriteError must unwrap to the cause")
	}
}
