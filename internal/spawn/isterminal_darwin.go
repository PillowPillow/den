package spawn

import (
	"os"
	"syscall"
	"unsafe"
)

// isTerminal reports whether f is a terminal, by asking the kernel the only
// question that actually answers it: fetch the descriptor's terminal
// attributes, and see whether it has any.
//
// THIS FILE IS THE DARWIN HALF of den issue #66, and it is three firsts in this
// repo at once — the first platform-specific file, the first `unsafe` import,
// and the first raw syscall (`syscall` appeared only for errno constants in
// config/file.go and worktree/worktree.go). All three were weighed and accepted
// on 2026-08-14. That premise held the day it was written: `git show
// a98aee9:go.mod` requires cobra, pflag and yaml.v3, and nothing else, so
// ruling out `golang.org/x/term` and `golang.org/x/sys` was correct then.
//
// It stopped holding two days later, on 2026-08-16, when #74 (source
// onboarding) added `golang.org/x/term` (and `golang.org/x/mod`) to go.mod
// for a credential read in internal/cli/root.go. Nobody revisited this file
// — the ordinary way a true comment rots. That root.go usage is itself gone
// now, replaced by the Prompter on this branch, but `golang.org/x/term`
// stays a direct require: internal/prompt/huhui imports it for
// `term.IsTerminal`, the same capability this file hand-rolls with the ioctl
// below.
//
// The ioctl is kept anyway, ON PURPOSE: replacing a measured mechanism on
// two platforms is its own change, with its own measurement to redo on both,
// and it is a separate ticket rather than something folded into a
// documentation fix.
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
// It takes the *os.File and calls Fd() itself, rather than taking the bare
// uintptr the first cut of #66 passed. This signature is the SHARED one — all
// three platforms carry it — and it is the fallback that forces it, not this
// file: `isterminal_other.go` needs a Stat, and reaching a Stat from a bare
// descriptor means `os.NewFile`, which takes ownership and installs a close
// finalizer. The whole story is written there, at the site that would otherwise
// rebuild it. Here the parameter costs nothing: Fd() on a nil receiver answers
// ^uintptr(0) and on a closed file answers the same (internal/poll sets Sysfd to
// -1 on destroy), so both degrade to EBADF, which is already the "no terminal"
// verdict below.
//
// Fd() is also free of the SetBlocking side effect its doc warns about, for the
// two files den actually passes. `fd()` calls `pfd.SetBlocking()` only when
// `f.nonblock` is set, and `os.Stdin`/`os.Stdout` are `NewFile` results, so
// `newFile` sees `kind == kindNewFile`: `pollable` then reduces to the
// already-non-blocking flag, and the only branch that sets `f.nonblock` under it
// is guarded by `kind == kindSock`. Blocking or not, the flag stays false. Read
// out of os/file_unix.go's `newFile` on go1.26.1, not assumed from Fd()'s doc.
//
// The `uintptr(unsafe.Pointer(&t))` conversion stays INLINE in the call
// expression on purpose. That is the only form the unsafe.Pointer rules allow
// (rule 4): hoisting it into a variable lets the garbage collector move `t`
// between the conversion and the call, and the kernel then writes into freed
// memory. It reads like a line begging to be extracted. It must not be.
func isTerminal(f *os.File) bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, f.Fd(),
		uintptr(syscall.TIOCGETA), uintptr(unsafe.Pointer(&t)), 0, 0, 0)
	return errno == 0
}
