package spawn

import (
	"syscall"
	"unsafe"
)

// isTerminal reports whether fd is a terminal, by asking the kernel the only
// question that actually answers it: fetch the descriptor's terminal
// attributes, and see whether it has any.
//
// THIS FILE IS THE DARWIN HALF of den issue #66, and it is three firsts in this
// repo at once — the first platform-specific file, the first `unsafe` import,
// and the first raw syscall (`syscall` appeared only for errno constants in
// config/file.go and worktree/worktree.go). All three were weighed and accepted
// on 2026-08-14, because there is no fourth option: this module allows stdlib +
// cobra + yaml.v3 only, which rules out `golang.org/x/term` and
// `golang.org/x/sys`, and no stdlib route to a real terminal test avoids the
// ioctl.
//
// What it replaces, and why the replacement was worth those firsts:
// `os.ModeCharDevice` answers true for EVERY character device — `/dev/null`,
// `/dev/zero`, `/dev/random` — not only for a terminal. It was a heuristic
// wearing the name of a test, and `sbx exec -it` with no real terminal behind it
// silently DISCARDS the command's output while still reporting rc=0 (spec
// §14.0). `< /dev/null` is the canonical CI and cron stdin, so the heuristic was
// a data-loss path with a clean exit code. #60 narrowed it to require stdin AND
// stdout, which closed the redirected-stdout shape; the residual case — a
// `/dev/null` stdin with a real terminal on stdout — is what this file closes.
//
// TIOCGETA is the darwin spelling of "get the termios struct"; linux spells the
// same request TCGETS (isterminal_linux.go). Measured by hand in a real terminal
// on darwin, 2026-08-14: stdin true, stdout true, `/dev/null` false. That
// measurement is the positive case, and it stays a measurement — a test that
// acquired a tty would stop the suite being hermetic (CLAUDE.md). The negative
// case IS tested, in isterminal_test.go.
//
// No file suffix redundancy: the `_darwin` in the name is the build constraint,
// which is why there is no `//go:build` line above.
//
// The `uintptr(unsafe.Pointer(&t))` conversion stays INLINE in the call
// expression on purpose. That is the only form the unsafe.Pointer rules allow
// (rule 4): hoisting it into a variable lets the garbage collector move `t`
// between the conversion and the call, and the kernel then writes into freed
// memory. It reads like a line begging to be extracted. It must not be.
func isTerminal(fd uintptr) bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, fd,
		uintptr(syscall.TIOCGETA), uintptr(unsafe.Pointer(&t)), 0, 0, 0)
	return errno == 0
}
