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
