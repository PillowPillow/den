//go:build !darwin && !linux

package spawn

import "os"

// isTerminal on a platform den does not ship keeps the PRE-#66 heuristic, and
// keeps it deliberately.
//
// den releases darwin and linux binaries only (.goreleaser.yml), and it drives
// `sbx`, which exists on neither of the platforms this file covers — so this
// code is never in a shipped binary. It exists so that `go build ./...` on some
// other GOOS answers a compile rather than "undefined: isTerminal".
//
// Answering `false` unconditionally was the other candidate and was rejected on
// 2026-08-14: it would make the `-i` checklist and an interactive spawn
// impossible on such a platform, with no way out — `-T` forces the tty OFF, and
// there is no counterpart that forces it ON. Degrading to the imperfect probe
// den shipped for a year is a worse verdict than den's best; degrading to a
// refusal nobody can override is a worse PRODUCT.
//
// So: a `/dev/null` here still reads as a terminal, exactly as it did
// everywhere before #66. That is why isterminal_test.go is tagged
// `darwin || linux` — asserting false here would assert against this documented
// fallback rather than against a bug.
//
// It IS type-checked, despite shipping nowhere: `task typecheck` builds
// GOOS=windows for this file and this file alone. Without that line it would be
// the only file in the repo no gate compiles.
//
// THIS FILE IS WHY isTerminal TAKES AN *os.File and not the bare descriptor #66
// first passed it. The sentence that used to stand here — "os.NewFile does NOT
// take ownership of the descriptor for a Stat: the file is never closed" — was
// false, and it was load-bearing: `os.NewFile` ends in
// `runtime.SetFinalizer(f.file, (*file).close)` (os/file_unix.go, go1.26.1), so
// the *os.File this function built was unreachable the moment it returned and
// SOME LATER GC closed the caller's descriptor. LooksInteractive calls twice, on
// os.Stdin and os.Stdout, so one `den spawn -i` on such a platform destroyed the
// process's stdin AND stdout at a nondeterministic later point. Reproduced on
// 2026-08-14 with a copy of the old body: after two runtime.GC(), a write to
// os.Stdout answered `bad file descriptor`.
//
// The fix is not a Close — `defer f.Close()` closes fd 0/1 immediately, which is
// the same bug brought forward. It is to stop FABRICATING an os.File around a
// descriptor this package never opened. Taking the caller's own file removes the
// second owner entirely, and it is the caller's file that every Stat here reads.
//
// `syscall.Fstat(int(fd), ...)` was the other way to keep the uintptr, and it is
// refused: syscall.Fstat does not exist on windows, and the GOOS=windows line in
// `task typecheck` is the ONLY gate that compiles this file at all — the fix
// would have broken the gate that guards it.
//
// No gate TESTS this file, which is how the false sentence survived review:
// isterminal_test.go is tagged `darwin || linux`, and typecheck only compiles
// GOOS=windows. Read what this body does; nothing here will fail for you.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
