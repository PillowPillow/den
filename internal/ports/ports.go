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
	"slices"
	"strconv"
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
	// Canonical is false when the window is not the nest's hashed (or declared)
	// block: those URLs are valid FOR THIS INSTANCE ONLY, and the caller must
	// say so. What it does NOT say is who holds the canonical block — the scan
	// reads the host, and a bound port names no owner. One candidate is now
	// excluded, though, and it was the likeliest: THIS sandbox's own earlier
	// publication, which Resolve reuses instead of shifting past (see
	// reusableWindow). What it could not reuse is in Resolution.Stale, named.
	// Anything else is another sandbox or a foreign listener, and a caller must
	// not pick between them.
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
	// AlreadyPublished is true when the live sandbox ALREADY carries exactly
	// this host↔container mapping (Options.Published named it). The row still
	// renders — it is part of the table the user asked for — but the caller
	// must NOT publish it again: `sbx ports --publish` is not idempotent, and
	// republishing a pair the same sandbox already has is a `409 Conflict:
	// port 127.0.0.1:<H>/tcp is already published` (measured 2026-07-31).
	AlreadyPublished bool
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

// addName is the NAME column of a pair the nest never declared. den does not
// invent one from the ports: a made-up "vite" would read exactly like a
// declaration, and the one thing the table must keep straight is which rows the
// nest promises and which the command line added for this run alone.
//
// The container port is appended because the bare "added" stopped distinguishing
// anything the moment a second `--add` appeared, and the nest where `--add` is
// the ONLY thing displayed — one with no `ports:`, the contract #6 locked — is
// exactly where every row then carried the same name (issue #21). The container
// port is a PROPERTY of the pair, so it survives reordering; an index
// (added-1, added-2) would have named the command line's argument order
// instead, which is not what the row is.
func addName(container int) string {
	return fmt.Sprintf("added:%d", container)
}

// ParseAdd reads ONE `--add` value into the Port the caller hands to
// Options.Extra.
//
// The grammar is `sbx ports --publish`'s own, minus the protocol suffix den
// never writes (see PublishSpec): `HOST_PORT:CONTAINER_PORT`, or the three-field
// form `127.0.0.1:HOST_PORT:CONTAINER_PORT`. The host address is accepted ONLY
// as the loopback literal — the flag exists to add a PAIR, never to widen a
// bind §8 refuses to widen anywhere else, and the refusal says so in the same
// words Resolve's does, tunnel included.
//
// Every refusal quotes the value back: the user typed one token, and reading
// what den made of it is how they see which half of the pair was wrong.
func ParseAdd(value string) (Port, error) {
	fields := strings.Split(value, ":")

	// The three-field form: the address is peeled off here, so the two-field
	// parse below is the ONLY place a host and a container port are read.
	if len(fields) == 3 {
		hostIP := fields[0]
		// An EMPTY address is malformed, not "non-loopback": `:8080:3000` says
		// nothing about a bind, and answering it with the LAN refusal would
		// lecture the user about a choice they never made. (Loopback's own
		// isLoopback accepts "" as "den decides" — that is a resolution
		// default, and it has no meaning in a written-out pair.)
		if hostIP == "" {
			return Port{}, malformedAdd(value)
		}
		if hostIP != Loopback {
			return Port{}, fmt.Errorf(
				"--add %q: %s is not a bind address den offers — den publishes on %s and nothing else "+
					"(spec §8): the microVM is the boundary, and a LAN bind pierces it host-side; for remote "+
					"access use a tunnel (`ssh -L HOST_PORT:%s:HOST_PORT you@host`), which delegates "+
					"authentication to SSH",
				value, hostIP, Loopback, Loopback)
		}
		fields = fields[1:]
	}

	if len(fields) != 2 {
		return Port{}, malformedAdd(value)
	}
	host, ok := parsePortField(fields[0])
	if !ok {
		return Port{}, malformedAdd(value)
	}
	container, ok := parsePortField(fields[1])
	if !ok {
		return Port{}, malformedAdd(value)
	}
	// No Open, no LoopbackLock: those are DECLARATIONS, and a pair added on the
	// command line declares nothing — it is published, listed, and that is all.
	return Port{Name: addName(container), Container: container, Host: host}, nil
}

// malformedAdd is the single sentence every shape refusal of ParseAdd speaks.
// One wording for one situation: the value quoted back, then the grammar it
// failed to match.
func malformedAdd(value string) error {
	return fmt.Errorf(
		"--add %q is not a HOST_PORT:CONTAINER_PORT pair — write it as `--add 8080:3000` (or "+
			"`--add %s:8080:3000`), where both ports are numbers in 1-65535",
		value, Loopback)
}

// parsePortField reads one field as a port number, and accepts NOTHING else:
// no padding (strconv.Atoi refuses it, and so does sbx), no 0, nothing above
// 65535. It answers with a bool rather than an error because the sentence the
// user reads is malformedAdd's, whichever field was wrong.
func parsePortField(field string) (int, bool) {
	n, err := strconv.Atoi(field)
	if err != nil || n < 1 || n > 65535 {
		return 0, false
	}
	return n, true
}

// Published is one host↔container mapping the live sandbox ALREADY carries.
//
// Only the two port numbers: the bind address and the protocol are the
// caller's to filter (den publishes on Loopback/tcp and nothing else, so a
// publication on any other address is not den's to reason about), and carrying
// them here would invite this package to decide about addresses it has already
// refused everywhere else.
type Published struct {
	Host      int
	Container int
}

// Options carries what the caller may ask of a resolution.
type Options struct {
	// HostIP is the address the caller wants to bind. Empty means Loopback.
	// Anything else is REFUSED — the field exists so that a flag offering to
	// force it has something to be refused by, not so that it can succeed.
	HostIP string
	// Published is what the sandbox ALREADY publishes, read from the live VM
	// before the resolution. It is what makes `den ports` re-readable, and the
	// whole of the fix for #15/#22.
	//
	// Without it the scan reads the host, finds den's OWN previous publication
	// holding the canonical window, calls it busy, shifts the whole block and
	// binds a second set of host ports for the same VM — three declared ports
	// became nine host publications in three runs of the same command (smoke
	// #2 §1.7), and the tenth run failed outright. With it, den recognises its
	// own window, re-renders the same table and publishes nothing.
	//
	// EMPTY MEANS "PUBLISHES NOTHING", NOT "UNKNOWN": a caller that cannot read
	// the sandbox (a stopped VM hides the `ports` field entirely) must not pass
	// an empty slice and call it a reading — it would resolve a fresh window
	// over publications that come back on resume.
	Published []Published
	// Extra are the pairs `--add` asked for on top of what the nest declares
	// (ParseAdd is what turns a flag value into one). They are resolved,
	// published and listed WITH the declared ports, but they take no window
	// offset and are not scanned: the host port is the one the user WROTE, and
	// renumbering an explicit request would publish an address nobody asked
	// for.
	Extra []Port
}

// Resolution is the full answer: which window, and where each declared port
// landed on the host.
type Resolution struct {
	Nest   string
	Window Window
	// Declared counts the leading entries of Ports that come from the nest's
	// `ports.publish` — the ones the window numbered; the rest are Extra
	// pairs. ZERO means the window was never placed on the host: a portless
	// nest scans nothing and binds nothing in its range, so a caller
	// rendering the window bounds or a tunnel line on its base would be
	// describing ports nothing listens on.
	Declared int
	// Ports follows the nest's DECLARATION ORDER, never sorted — see Resolve.
	Ports []Port
	// Stale are publications of THIS sandbox that sit in a window block the
	// resolution had to skip: mappings den itself bound on an earlier run and
	// can no longer reuse, because the nest no longer declares that container
	// port on that offset. They are the one holder of a busy block den can
	// name with certainty, and the reason it can hand over a remedy
	// (`sbx ports <name> --unpublish …`) instead of the old "den cannot tell
	// who holds it". Empty is the ordinary case.
	Stale []Published
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

	// A declared `ports.base:` is validated from the configuration alone: the
	// hashed base is aligned and in range by construction, a declared one
	// carries no such guarantee — unvalidated, base 80 would send the scan
	// probing privileged ports, and base 65530 would walk it past 65535 into
	// bind failures reported as "no free window", never naming the real cause.
	if b := n.Ports.Base; b != 0 {
		// The scan may move the window up to maxShifts whole blocks: the
		// highest base is the one whose LAST shifted port still fits in 65535.
		const maxBase = 65535 - (maxShifts*WindowSize + WindowSize - 1)
		if b < 1024 || b > maxBase {
			return nil, fmt.Errorf(
				"nest %q: `ports.base: %d` is outside 1024-%d — below 1024 are the privileged ports, and above "+
					"%d the collision scan (up to %d shifts of a whole window) would walk past port 65535",
				n.Name, b, maxBase, maxBase, maxShifts)
		}
		if b%WindowSize != 0 {
			return nil, fmt.Errorf(
				"nest %q: `ports.base: %d` is not aligned on a block of %d — the collision shift moves whole "+
					"blocks, and an unaligned window interleaves with the hashed windows of neighbouring nests; "+
					"use %d or %d",
				n.Name, b, WindowSize, b-b%WindowSize, b-b%WindowSize+WindowSize)
		}
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

	// The added pairs against EACH OTHER, from Options alone — with the other
	// configuration-only refusals, and therefore before the scan and before the
	// portless early return below, which publishes them too.
	//
	// A host port binds ONCE. Two `--add` values naming the same one is a
	// collision den would otherwise discover mid-publication: the caller binds
	// one pair at a time, so the first would take the port and the second would
	// fail against it, leaving the window half-bound with no table printed.
	seen := make(map[int]Port, len(o.Extra))
	for _, e := range o.Extra {
		if first, ok := seen[e.Host]; ok {
			return nil, fmt.Errorf(
				"nest %q: `--add %d:%d` and `--add %d:%d` both ask for host port %d — a host port binds "+
					"once, and den publishes one pair at a time, so the second would fail with the first "+
					"already bound; give one of them another host port, or drop it",
				n.Name, first.Host, first.Container, e.Host, e.Container, e.Host)
		}
		seen[e.Host] = e
	}

	// What the sandbox ALREADY publishes, indexed by host port — the reading
	// every decision below leans on. Host port and not the pair, because that
	// is what sbx keys its refusal on: publishing `9500:9999` over an existing
	// `9500:8080` is the SAME `409 Conflict: port 127.0.0.1:9500/tcp is already
	// published` as republishing `9500:8080` itself (measured 2026-07-31, both
	// forms). A map keyed by the pair would call the first one publishable.
	mine := indexPublished(o.Published)

	// The added pairs against what the sandbox already carries — with the other
	// pre-scan checks, and therefore before the portless early return below,
	// which publishes them too (a portless nest with `--add` is §1.8's `beta`,
	// and re-running it is the same 409 as anywhere else).
	extra, err := markExtra(n.Name, o.Extra, mine)
	if err != nil {
		return nil, err
	}

	// ports.base wins over the hash: it is the nest's own, explicit choice.
	base := n.Ports.Base
	if base == 0 {
		base = Base(n.Name)
	}

	// Nothing to place, nothing to probe: a nest declaring no port publishes
	// nothing, so it never touches the host — a saturated window (or no
	// scanner at all) cannot fail a resolution that binds nothing. The added
	// pairs still travel: they are the user's, not the nest's, and a portless
	// nest is exactly when `--add` is the whole point of the command.
	if len(decls) == 0 {
		return &Resolution{
			Nest:   n.Name,
			Window: Window{Base: base, Canonical: true},
			Ports:  extra,
		}, nil
	}
	// From here on the window WILL be placed: every return below carries
	// len(decls) declared ports numbered by it.

	// THE WINDOW THIS SANDBOX IS ALREADY ON WINS OVER ANY SCAN. Re-running
	// `den ports` is the ordinary gesture — re-reading the table — and the scan
	// cannot serve it: it reads the host, a bound port names no owner, and den's
	// own publication of the previous run is exactly what makes the canonical
	// block look busy. Recognising it first is what turns the command from
	// "bind ten more host ports" into "re-read".
	window, ok := reusableWindow(decls, base, mine)
	if !ok {
		if s == nil {
			return nil, fmt.Errorf(
				"nest %q: no port scanner — den cannot tell whether the window is free, and assuming it is would "+
					"hand a second instance the URLs of the first", n.Name)
		}
		var err error
		if window, err = scan(n.Name, base, s, mine); err != nil {
			return nil, err
		}
	}

	// DECLARATION ORDER IS THE OFFSET, and this list must NEVER be sorted.
	// This is the one exception to the repo's rule that everything displayed
	// or serialized is sorted (HANDOFF §8): sorting here renumbers every port
	// of the nest, and with them every URL a user has bookmarked.
	out := make([]Port, 0, len(decls)+len(extra))
	for i, d := range decls {
		host := window.Base + i
		// By construction of reusableWindow, a host port this sandbox holds
		// inside the chosen window carries THIS declaration's container port —
		// the lookup only ever confirms it. It is written out rather than
		// assumed so the flag stays right for a window the SCAN produced too,
		// where nothing is held and every port is genuinely still to publish.
		held, ok := mine[host]
		out = append(out, Port{
			Name:             d.Name,
			Container:        d.Container,
			Host:             host,
			Open:             d.Open,
			LoopbackLock:     d.LoopbackLock,
			AlreadyPublished: ok && held.Container == d.Container,
		})
	}
	// The added pairs against the ports just NUMBERED — here, and not with the
	// checks above, because the host port a declaration lands on is only known
	// once the window is resolved: a shifted window renumbers all of them, and
	// a comparison against the canonical base would clear a pair that collides
	// and refuse one that does not.
	//
	// An EQUALITY, never a range: the window bounds what den numbers, and a
	// host port inside the block that no declaration was given is one den
	// publishes nothing on — refusing it would reject a pair that binds fine.
	for _, e := range extra {
		for _, p := range out {
			if p.Host != e.Host {
				continue
			}
			return nil, fmt.Errorf(
				"nest %q: `--add %d:%d` asks for host port %d, which the window %d-%d already gave to the "+
					"declared port %q (%d:%d) — den publishes one pair at a time, so the added pair would "+
					"fail after the declared ports are already bound, leaving the window half-published; "+
					"give the added pair a host port this window did not assign, or drop it",
				n.Name, e.Host, e.Container, e.Host, window.Base, window.Last(),
				p.Name, p.Host, p.Container)
		}
	}

	// The added pairs come LAST, after everything the nest declared: an
	// addition to the window, never a substitution inside it — the declared
	// ports keep the offsets §8 numbers them by.
	out = append(out, extra...)
	return &Resolution{
		Nest:     n.Name,
		Window:   window,
		Declared: len(decls),
		Ports:    out,
		Stale:    staleBelow(base, window.Base, mine),
	}, nil
}

// indexPublished keys the sandbox's publications by HOST port — the key sbx
// itself refuses on (see Resolve). Later entries win, which cannot happen: a
// host port is published once per sandbox, and a duplicate would mean sbx
// contradicted itself.
func indexPublished(list []Published) map[int]Published {
	out := make(map[int]Published, len(list))
	for _, p := range list {
		out[p.Host] = p
	}
	return out
}

// markExtra decides, for each `--add` pair, whether the sandbox already carries
// it — and refuses the one shape sbx would reject in den's face.
//
// Three cases, and they are genuinely three:
//
//   - the host port is free of any publication → publish it, as before;
//   - the host port already carries THIS pair → the pair is done. The row still
//     renders (the user asked for the table, and the pair really is published),
//     but publishing it again is a `409 Conflict: … already published`, which is
//     issue #22: the identical re-run failed AFTER binding one more declared
//     window, so the command was both destructive and unusable;
//   - the host port carries a DIFFERENT container port → den refuses, here,
//     before a single publication. sbx keys its 409 on the host port alone, so
//     this would fail too, but it would fail late — with the declared ports
//     already bound — and its message ("already published") names neither the
//     mapping in the way nor the command that clears it.
func markExtra(nestName string, list []Port, mine map[int]Published) ([]Port, error) {
	out := make([]Port, 0, len(list))
	for _, e := range list {
		held, ok := mine[e.Host]
		if ok && held.Container != e.Container {
			return nil, fmt.Errorf(
				"nest %q: `--add %d:%d` asks for host port %d, which this sandbox already publishes to "+
					"container port %d — a host port publishes once per sandbox, whatever it points at, and "+
					"sbx refuses the second (`409 Conflict: port %s:%d/tcp is already published`); give the "+
					"pair another host port, or release the one in the way first "+
					"(`sbx ports SANDBOX --unpublish %s:%d:%d`)",
				nestName, e.Host, e.Container, e.Host, held.Container,
				Loopback, e.Host, Loopback, held.Host, held.Container)
		}
		e.AlreadyPublished = ok
		out = append(out, e)
	}
	return out, nil
}

// reusableWindow finds the window this sandbox is ALREADY published on, if it
// is on one.
//
// Only the blocks a scan could itself have produced are candidates —
// `base + k*WindowSize`, up to maxShifts — and the LOWEST one wins. That is
// what heals a sandbox the pre-fix den polluted: `alpha` ended smoke #2 with
// 9500-9502, 9510-9512 and 9520-9522 all mapping the same three containers, and
// the lowest match is 9500, the canonical block, the one URL the user
// bookmarked. Preferring a scan of the host instead would have picked the
// first FREE block — a fourth one.
//
// A block qualifies when every host port of it that this sandbox holds carries
// the container port that declaration would land there, and at least one does.
// The partial case is deliberate and is issue #22's wreckage: a run that aborted
// mid-publication left some of its window bound and the rest not, and den must
// finish it rather than move elsewhere.
//
// What this does NOT do is scan the block it reuses. A host port inside it that
// den does not hold may have been taken by a foreign process since; publishing
// it then fails with sbx's own `409 … address already in use`, which names the
// port (smoke #2 §1.9). Shifting away instead would abandon the window the
// sandbox is demonstrably serving on, to escape a collision on a port it may
// not even need.
func reusableWindow(decls []nest.PortDecl, base int, mine map[int]Published) (Window, bool) {
	if len(mine) == 0 {
		return Window{}, false
	}
	for shift := 0; shift <= maxShifts; shift++ {
		candidate := base + shift*WindowSize
		matched, usable := false, true
		for i, d := range decls {
			held, ok := mine[candidate+i]
			if !ok {
				continue // never published, or published elsewhere: den will bind it
			}
			if held.Container != d.Container {
				usable = false // one of den's own mappings, but not this one
				break
			}
			matched = true
		}
		if usable && matched {
			return Window{Base: candidate, Canonical: shift == 0, Shifts: shift}, true
		}
	}
	return Window{}, false
}

// staleBelow lists the sandbox's own publications sitting in the blocks between
// the canonical base and the window finally chosen — the blocks the resolution
// SKIPPED and this very sandbox is holding.
//
// It exists so the caller can say something true instead of the old "den cannot
// tell who holds it": for these, den can. They are what an earlier `den ports`
// bound and no longer matches — the residue of #15 on a sandbox that lived
// through it — and the only holder of a busy block that comes with a remedy.
//
// Sorted by host port: everything den displays is sorted (the one exception is
// the port table, whose order IS the offset), and map iteration would reorder
// the warning from run to run.
func staleBelow(base, chosen int, mine map[int]Published) []Published {
	var out []Published
	for host, p := range mine {
		if host >= base && host < chosen {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b Published) int { return a.Host - b.Host })
	return out
}

// scan walks the window forward, a WHOLE BLOCK at a time, until it finds one
// where every port is free.
//
// A whole block, never port by port: two instances sharing a window
// port-by-port would interleave, and "which instance is on 9103" stops having
// an answer anyone can reason about. The first instance therefore always keeps
// the canonical window, and later ones are moved wholesale and flagged.
func scan(nestName string, base int, s Scanner, mine map[int]Published) (Window, error) {
	tried := make([]string, 0, maxShifts+1)
	for shift := 0; shift <= maxShifts; shift++ {
		candidate := base + shift*WindowSize
		if windowIsFree(candidate, s) {
			return Window{Base: candidate, Canonical: shift == 0, Shifts: shift}, nil
		}
		tried = append(tried, fmt.Sprintf("%d-%d", candidate, candidate+WindowSize-1))
	}
	// The tried blocks are named, and so is the holder whenever den knows it.
	// It used to be unable to: the scan reads the host, and a bound port names
	// no owner, so the message had to hedge between "a foreign listener" and
	// "an earlier `den ports` run of your own". Half of that hedge is gone —
	// Resolve now reads what THIS sandbox publishes and reuses it instead of
	// shifting past it, so reaching this point means the sandbox's own
	// publications are not what stands here… unless they are stale ones den
	// cannot map onto a declaration, which is the case named below.
	if stale := staleBelow(base, base+(maxShifts+1)*WindowSize, mine); len(stale) > 0 {
		return Window{}, fmt.Errorf(
			"nest %q: no free window after %d shifts (tried %s) — and this sandbox itself publishes %s "+
				"inside that range, on mappings the nest no longer declares: release them "+
				"(`sbx ports SANDBOX --unpublish %s:%d:%d`, one per pair) and re-run, or set "+
				"`ports.base:` in the nest to a quieter range",
			nestName, maxShifts, strings.Join(tried, ", "), formatPublished(stale),
			Loopback, stale[0].Host, stale[0].Container)
	}
	return Window{}, fmt.Errorf(
		"nest %q: no free window after %d shifts (tried %s) — the scan reads the host, which says a "+
			"port is bound and never by whom, and none of it is this sandbox's own publication "+
			"(den read those first): those blocks are foreign listeners, or other sandboxes of "+
			"yours, whose publications stay bound as long as their VM lives (`sbx ports SANDBOX` "+
			"lists what one publishes). Free host ports, or set `ports.base:` in the nest to a "+
			"quieter range",
		nestName, maxShifts, strings.Join(tried, ", "))
}

// formatPublished renders a list of publications as `9500->8080, 9501->3000`,
// the form the warning and the exhaustion error both need. One writer, because
// the two sentences must describe a mapping the same way.
func formatPublished(list []Published) string {
	parts := make([]string, 0, len(list))
	for _, p := range list {
		parts = append(parts, fmt.Sprintf("%d->%d", p.Host, p.Container))
	}
	return strings.Join(parts, ", ")
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
