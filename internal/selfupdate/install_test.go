package selfupdate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestProbeWritableAcceptsATempDir(t *testing.T) {
	dir := t.TempDir()
	if err := ProbeWritable(dir); err != nil {
		t.Fatalf("ProbeWritable(%s) = %v", dir, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("the probe left %d file(s) behind: %v", len(entries), entries)
	}
}

func TestProbeWritableRefusesAMissingDir(t *testing.T) {
	err := ProbeWritable(filepath.Join(t.TempDir(), "nope"))
	if err == nil {
		t.Fatal("a missing directory must be refused before anything is downloaded")
	}
	var we *WriteError
	if !errors.As(err, &we) {
		t.Fatalf("want a *WriteError naming the remedy, got %T: %v", err, err)
	}
}

func TestSwapBinaryReplacesInPlace(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "den")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SwapBinary(target, []byte("new")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("target holds %q, want \"new\"", got)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode is %v, want 0755", info.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("the staging file survived the swap: %v", entries)
	}
}

func TestSwapBinaryLeavesNoResidueOnFailure(t *testing.T) {
	dir := t.TempDir()
	// The target is a DIRECTORY, so the rename fails after the staging file
	// was written — the window the cleanup exists for.
	target := filepath.Join(dir, "den")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SwapBinary(target, []byte("new")); err == nil {
		t.Fatal("renaming onto a directory must fail")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("a staging file survived the failure: %v", entries)
	}
}

func TestSwapBinaryRefusesAPlantedStagingFile(t *testing.T) {
	// The staging name is predictable and ProbeWritable creates then removes it
	// long before the download finishes. Anyone able to write in the install
	// directory can drop a symlink in that window; os.WriteFile would have
	// FOLLOWED it and written the new binary — chmod 0755 and all — wherever it
	// pointed. O_EXCL makes that an EEXIST refusal instead.
	dir := t.TempDir()
	target := filepath.Join(dir, "den")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(elsewhere, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	staging := filepath.Join(dir, stagingName(os.Getpid()))
	if err := os.Symlink(elsewhere, staging); err != nil {
		t.Fatal(err)
	}

	err := SwapBinary(target, []byte("new"))
	if err == nil {
		t.Fatal("a staging path that already exists must be refused")
	}
	var se *StagingError
	if !errors.As(err, &se) {
		t.Fatalf("want a *StagingError naming the file to remove, got %T: %v", err, err)
	}
	got, readErr := os.ReadFile(elsewhere)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "untouched" {
		t.Fatalf("den wrote through the planted symlink: %q", got)
	}
	// The refusal must not delete the planted file either: it is not den's, and
	// the message tells the user to remove it. A cleanup armed before the
	// exclusive create removed it and made that sentence a lie.
	if _, statErr := os.Lstat(staging); statErr != nil {
		t.Fatalf("den removed a staging file it did not create: %v", statErr)
	}
	if got, readErr := os.ReadFile(target); readErr != nil || string(got) != "old" {
		t.Fatalf("the target was touched despite the refusal: %q (%v)", got, readErr)
	}
}
