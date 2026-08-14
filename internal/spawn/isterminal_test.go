//go:build darwin || linux

package spawn

import (
	"os"
	"testing"
)

// This file is the half of the terminal probe that #66 made testable, and it is
// tagged to the two platforms den ships (.goreleaser.yml): the fallback build
// keeps the old `os.ModeCharDevice` heuristic, under which `/dev/null` answers
// true, so asserting false there would be asserting against the documented
// fallback rather than against a bug.
//
// The positive case — a real terminal answers true — stays untested and
// untestable: a suite that acquired a tty would stop being hermetic (CLAUDE.md,
// test conventions). It was measured by hand instead, on darwin in a real
// terminal, 2026-08-14: `stdin ioctl=true`, `stdout ioctl=true`,
// `/dev/null ioctl=false`. The linux spelling (TCGETS) is cross-compile-verified
// only.

// TestIsTerminalRejectsDevNull is the whole point of #66 in one assertion.
//
// `/dev/null` is a character device, so the probe den shipped until now
// (`info.Mode()&os.ModeCharDevice != 0`) answered TRUE for it — and `sbx exec
// -it` with no real terminal behind it silently DISCARDS the command's output
// while reporting rc=0 (spec §14.0). `< /dev/null` is the canonical CI and cron
// stdin, so that answer was a data-loss path with a clean exit code.
//
// This test could not exist before the split: LooksInteractive reads the
// os.Stdin and os.Stdout globals, which a test cannot replace with a file
// without reaching into package state. isTerminal takes the file, so the one
// verdict that CAN be pinned down without a tty now is.
func TestIsTerminalRejectsDevNull(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("opening %s: %v", os.DevNull, err)
	}
	defer f.Close()

	if isTerminal(f) {
		t.Errorf("%s must not read as a terminal: it is a character device, and answering "+
			"true for it is exactly the bug #66 closes", os.DevNull)
	}
}

// TestIsTerminalRejectsARegularFile covers the redirected-output shape —
// `den exec api -- cmd > log.txt`. The old heuristic already answered false
// here (a regular file is no character device), so this test is a guard against
// a rewrite that over-corrects, not a bug being fixed.
func TestIsTerminalRejectsARegularFile(t *testing.T) {
	f, err := os.Create(t.TempDir() + "/out.txt")
	if err != nil {
		t.Fatalf("creating the file: %v", err)
	}
	defer f.Close()

	if isTerminal(f) {
		t.Error("a regular file must not read as a terminal")
	}
}

// TestIsTerminalRejectsAClosedFile pins the error path: the ioctl fails with
// EBADF and the probe must read that as "no terminal", never panic and never
// answer true. den calls this on os.Stdin and os.Stdout, and a process started
// with fd 0 closed is a real shape (`den spawn api <&-`).
//
// It asserts on a CLOSED FILE, not on a stale descriptor number, and that is
// what makes the assertion sound rather than probable. Closing an *os.File sets
// its Sysfd to -1 (internal/poll's destroy), so Fd() answers ^uintptr(0) — a
// number no open can be handed. The earlier form of this test kept the bare
// uintptr and had to argue that a reused fd would land on a regular file or a
// pipe and answer false anyway; the argument was correct and is now unnecessary.
func TestIsTerminalRejectsAClosedFile(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("opening %s: %v", os.DevNull, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}

	if isTerminal(f) {
		t.Error("a closed file must not read as a terminal")
	}
}
