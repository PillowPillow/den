package spawn

import (
	"os"
	"syscall"
	"unsafe"
)

// isTerminal is the linux twin of the darwin probe. The reasoning — why the
// ioctl, why the three firsts it costs, why the parameter is an *os.File rather
// than a descriptor, why the conversion stays inline — is written once, in
// isterminal_darwin.go, and is not repeated here: two copies of one argument are
// two things to keep in agreement.
//
// The ONE difference is the request constant. linux spells "get the termios
// struct" TCGETS; darwin spells it TIOCGETA. Everything else is identical, and
// that is the reason these are two files rather than one file with a `runtime.GOOS`
// switch: a switch would compile a constant the other platform's syscall package
// does not define.
//
// Verified by cross-compilation only (`GOOS=linux GOARCH=amd64` and `arm64`,
// CGO_ENABLED=0, 2026-08-14). The hand measurement in a real terminal that
// settles the positive case was made on darwin; nobody has run this file against
// a live linux tty. `task typecheck` builds both platforms so a divergence here
// fails a gate rather than a user's spawn.
func isTerminal(f *os.File) bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, f.Fd(),
		uintptr(syscall.TCGETS), uintptr(unsafe.Pointer(&t)), 0, 0, 0)
	return errno == 0
}
