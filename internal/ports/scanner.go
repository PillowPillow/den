package ports

import (
	"fmt"
	"net"
)

// ListenScanner is the real Scanner, and the ONLY file of this package that
// touches the network. It is isolated here for the same reason the TTY probe
// of `den <nest> -i` is a one-liner behind Deps: what cannot be tested without
// leaving the hermetic suite must be the smallest possible surface, so that
// everything AROUND it stays tested. The rest of the package — window, offsets,
// shift, refusals — never sees a socket.
//
// Untested by construction: any test here would bind a real host port, which
// the repo's suite forbids (no test opens a socket, `go test ./...` must stay
// hermetic and parallel-safe).
type ListenScanner struct{}

// Free reports whether the port can be BOUND on Loopback right now.
//
// A bind attempt, not a dial: dialing answers "is someone listening", which is
// not the same question. A port held by a socket in TIME_WAIT, or bound by a
// process that accepts nothing, refuses the connection while still refusing the
// bind — den would publish onto it and `sbx ports --publish` would be the one
// to fail, long after den promised the URL.
//
// The listener is closed immediately: this is a probe, and the real bind is
// sbx's. The window between the two is an unavoidable race — the alternative,
// holding every probed port until publication, would mean den itself squatting
// ten ports per nest.
func (ListenScanner) Free(port int) bool {
	l, err := net.Listen("tcp", fmt.Sprintf("%s:%d", Loopback, port))
	if err != nil {
		return false
	}
	// The close error is deliberately ignored: the verdict is the SUCCESSFUL
	// bind above. A failure to release a listener we just created says nothing
	// about whether the port was free, and reporting "occupied" for it would
	// shift a window that is in fact perfectly usable.
	_ = l.Close()
	return true
}
