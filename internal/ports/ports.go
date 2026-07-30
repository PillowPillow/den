// Package ports computes the host ports a nest publishes (spec §8): a
// deterministic window per nest, an offset per declared port, and a
// collision-avoidance scan against the host.
//
// It is pure logic behind an injected Scanner: nothing here opens a socket,
// runs sbx, or prints. Rendering (`den ports`) and execution (`sbx ports
// --publish`) belong to the CLI, and PUBLICATION IS ON DEMAND — internal/spawn
// must never import this package, or every spawn would start binding host
// ports nobody asked for (spec §8, HANDOFF decision 8).
package ports

import (
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/PillowPillow/den/internal/nest"
)

// Loopback is the ONLY host address den ever binds. Non-negotiable (spec §8):
// the microVM is the boundary, and a LAN bind pierces it host-side. "Access
// from outside" is served by an SSH tunnel — `ssh -L H:127.0.0.1:H you@host` —
// where authentication is delegated to SSH, never by widening this.
const Loopback = "127.0.0.1"

const (
	// WindowSize is the number of ports reserved per nest. 10, from §8: it
	// bounds how many ports a nest may declare, and it is the granularity of
	// the collision shift.
	WindowSize = 10
	// firstBase and blockCount span the window range: 9000..17990, aligned on
	// WindowSize.
	firstBase  = 9000
	blockCount = 900
	// maxShifts bounds the collision scan. A host busy across ten consecutive
	// windows is a situation to REPORT, not to keep scanning through: without
	// this, an aggressive listener (or a scanner that always answers "busy")
	// walks den off the end of the port range.
	maxShifts = 10
)

// Scanner answers whether a host port is free on Loopback.
//
// An interface, injected, like doctor.Deps and sbx.Runner: the scan is the one
// system dependency of this package, and keeping it behind this narrow method
// is what lets the whole suite run without opening a single socket.
type Scanner interface {
	Free(port int) bool
}

// Base is the deterministic window base for a nest name (spec §8):
//
//	base = 9000 + hash(name) % 900 * 10
//
// THE HASH FUNCTION IS A CONTRACT, NOT AN IMPLEMENTATION DETAIL. §8 promises a
// bookmarkable URL: change the hash and every URL a user wrote down moves. It
// is FNV-1a 32-bit from the standard library — `fnv.New32a()`, the name's bytes
// written in order, `Sum32()` — and testdata/window.golden pins the result for
// a fixed list of names so that a well-meant "improvement" reddens the suite.
func Base(nestName string) int {
	h := fnv.New32a()
	// hash.Hash's Write never returns an error (documented on the interface),
	// so there is nothing to handle here.
	_, _ = h.Write([]byte(nestName))
	return firstBase + int(h.Sum32()%blockCount)*WindowSize
}

// Window is the block of WindowSize host ports a resolution landed on.
type Window struct {
	Base int
	// Canonical is false when the scan had to move the window: those URLs are
	// valid FOR THIS INSTANCE ONLY, and the caller must say so. The first
	// instance of a nest always keeps the canonical, bookmarkable window.
	Canonical bool
	// Shifts is how many blocks the window moved (0 when canonical).
	Shifts int
}

// Last is the window's final port, inclusive.
func (w Window) Last() int { return w.Base + WindowSize - 1 }

// Port is a declared port placed on the host.
type Port struct {
	Name         string
	Container    int
	Host         int
	Open         bool
	LoopbackLock bool
}

// PublishSpec renders the port for `sbx ports --publish`, whose attested
// grammar is `[[HOST_IP:]HOST_PORT:]SANDBOX_PORT[/PROTOCOL]` (spec §14.0).
//
// The host IP is written in FULL, always: omitting it would let sbx apply its
// own default, and that default is not den's to assume — the one thing §8 does
// not negotiate is that the bind is on 127.0.0.1. No protocol suffix: a nest's
// `ports:` declares none, and inventing one would go beyond the attested
// surface.
func (p Port) PublishSpec() string {
	return fmt.Sprintf("%s:%d:%d", Loopback, p.Host, p.Container)
}

// Options carries what the caller may ask of a resolution.
type Options struct {
	// HostIP is the address the caller wants to bind. Empty means Loopback.
	// Anything else is REFUSED — the field exists so that a flag offering to
	// force it has something to be refused by, not so that it can succeed.
	HostIP string
}

// Resolution is the full answer: which window, and where each declared port
// landed on the host.
type Resolution struct {
	Nest   string
	Window Window
	// Ports follows the nest's DECLARATION ORDER, never sorted — see Resolve.
	Ports []Port
}

// Resolve places a nest's declared ports on the host.
//
// Order of checks: everything rejectable from the configuration alone
// (overflow, bind address, loopback lock) comes BEFORE the scan, so a nest that
// cannot be published never probes the host — the same discipline
// internal/spawn applies before its first side effect.
func Resolve(n *nest.Nest, o Options, s Scanner) (*Resolution, error) {
	decls := n.Ports.Publish

	// More than one window declared: refuse rather than overflow. Port 11
	// would land in the NEXT nest's window, and the collision scan — which
	// reasons in whole blocks of WindowSize — would never see it coming.
	if len(decls) > WindowSize {
		return nil, fmt.Errorf(
			"nest %q declares %d ports, the window holds %d (spec §8) — remove ports from `ports.publish:`, "+
				"or split the nest: an 11th port would land in another nest's window",
			n.Name, len(decls), WindowSize)
	}

	// The loopback lock FIRST, and deliberately before the general bind
	// refusal below, which would also reject this address. Behind it, this
	// branch would be unreachable and the promise "refused even if forced"
	// would be carried by a message that never names the lock — leaving the
	// user to believe a flag exists that could unlock it.
	if !isLoopback(o.HostIP) {
		for _, d := range decls {
			if d.LoopbackLock {
				return nil, fmt.Errorf(
					"nest %q, port %q: loopback_lock — refused on %s, and no flag lifts it: this port speaks an "+
						"UNAUTHENTICATED protocol (CDP/Playwright), so binding it beyond %s hands the browser "+
						"driving your session to the network; for remote access use a tunnel "+
						"(`ssh -L HOST_PORT:%s:HOST_PORT you@host`), where SSH does the authenticating",
					n.Name, d.Name, o.HostIP, Loopback, Loopback)
			}
		}
		return nil, fmt.Errorf(
			"nest %q: %s is not a bind address den offers — den publishes on %s and nothing else (spec §8): "+
				"the microVM is the boundary, and a LAN bind pierces it host-side; for remote access use a "+
				"tunnel (`ssh -L HOST_PORT:%s:HOST_PORT you@host`), which delegates authentication to SSH",
			n.Name, o.HostIP, Loopback, Loopback)
	}

	if s == nil {
		return nil, fmt.Errorf(
			"nest %q: no port scanner — den cannot tell whether the window is free, and assuming it is would "+
				"hand a second instance the URLs of the first", n.Name)
	}

	// ports.base wins over the hash: it is the nest's own, explicit choice.
	base := n.Ports.Base
	if base == 0 {
		base = Base(n.Name)
	}

	window, err := scan(n.Name, base, len(decls), s)
	if err != nil {
		return nil, err
	}

	// DECLARATION ORDER IS THE OFFSET, and this list must NEVER be sorted.
	// This is the one exception to the repo's rule that everything displayed
	// or serialized is sorted (HANDOFF §8): sorting here renumbers every port
	// of the nest, and with them every URL a user has bookmarked.
	out := make([]Port, 0, len(decls))
	for i, d := range decls {
		out = append(out, Port{
			Name:         d.Name,
			Container:    d.Container,
			Host:         window.Base + i,
			Open:         d.Open,
			LoopbackLock: d.LoopbackLock,
		})
	}
	return &Resolution{Nest: n.Name, Window: window, Ports: out}, nil
}

// scan walks the window forward, a WHOLE BLOCK at a time, until it finds one
// where every port is free.
//
// A whole block, never port by port: two instances sharing a window
// port-by-port would interleave, and "which instance is on 9103" stops having
// an answer anyone can reason about. The first instance therefore always keeps
// the canonical window, and later ones are moved wholesale and flagged.
func scan(nestName string, base, declared int, s Scanner) (Window, error) {
	tried := make([]string, 0, maxShifts+1)
	for shift := 0; shift <= maxShifts; shift++ {
		candidate := base + shift*WindowSize
		if windowIsFree(candidate, s) {
			return Window{Base: candidate, Canonical: shift == 0, Shifts: shift}, nil
		}
		tried = append(tried, fmt.Sprintf("%d-%d", candidate, candidate+WindowSize-1))
	}
	return Window{}, fmt.Errorf(
		"nest %q: no free window after %d shifts (tried %s) — free host ports, or set `ports.base:` "+
			"in the nest to a quieter range",
		nestName, maxShifts, strings.Join(tried, ", "))
}

// windowIsFree probes the WHOLE window, not just the ports actually declared:
// §8 reserves the block, and a neighbour squatting 9107 today is what makes
// declaring a 8th port tomorrow silently collide.
func windowIsFree(base int, s Scanner) bool {
	for port := base; port < base+WindowSize; port++ {
		if !s.Free(port) {
			return false
		}
	}
	return true
}

// isLoopback accepts the empty string (the default, "den decides") and the
// literal Loopback. Nothing else — not "localhost", which resolves through the
// host's name service and can answer something other than 127.0.0.1, and not
// "::1": `sbx ports --publish`'s spec grammar separates its fields with `:`,
// where an IPv6 literal is ambiguous.
func isLoopback(hostIP string) bool {
	return hostIP == "" || hostIP == Loopback
}
