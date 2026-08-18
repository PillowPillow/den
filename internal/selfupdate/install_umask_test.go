//go:build darwin || linux

package selfupdate

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestSwapBinaryModeSurvivesARestrictiveUmask pins the fix for the bug the
// reviewer measured: os.WriteFile's mode argument is a REQUEST the process
// umask subtracts from, not a guarantee. Under `umask 0111` the staging file
// landed 0644, and after the rename that was a non-executable den on the
// user's PATH with no error reported anywhere. SwapBinary now os.Chmods the
// staging file to 0755 before the rename, which this test asserts directly.
//
// syscall.Umask does not exist on Windows, so this file is tagged darwin ||
// linux — the two platforms this suite runs on (CLAUDE.md test conventions) —
// following internal/spawn/isterminal_test.go's pattern for the same reason.
func TestSwapBinaryModeSurvivesARestrictiveUmask(t *testing.T) {
	// t.TempDir and the initial write happen BEFORE the umask is tightened:
	// t.TempDir creates its directory 0700, and 0700&^0111 leaves no execute
	// bit at all, which would make the directory unusable rather than
	// exercising the bug under test.
	dir := t.TempDir()
	target := filepath.Join(dir, "den")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	old := syscall.Umask(0o111)
	defer syscall.Umask(old)

	if err := SwapBinary(target, []byte("new")); err != nil {
		t.Fatalf("SwapBinary: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("mode is %v under umask 0111, want 0755 — the chmod before the rename did not fire", got)
	}
}
