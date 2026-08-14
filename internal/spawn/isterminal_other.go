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
// os.NewFile does NOT take ownership of the descriptor for a Stat: the file is
// never closed, so fd stays the caller's to manage — which matters, since every
// caller passes os.Stdin or os.Stdout.
func isTerminal(fd uintptr) bool {
	f := os.NewFile(fd, "")
	if f == nil {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
