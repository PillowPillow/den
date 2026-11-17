//go:build unix

package repos

import "syscall"

// configOpenFlags refuses a symlink as .git/config and keeps opening a FIFO from blocking.
const configOpenFlags = syscall.O_NOFOLLOW | syscall.O_NONBLOCK
