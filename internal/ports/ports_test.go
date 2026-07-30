package ports

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/nest"
)

// freeScanner answers "free" for every port: the first instance of a nest, the
// common case. Nothing here opens a socket — see hermeticity_test.go.
type freeScanner struct{}

func (freeScanner) Free(int) bool { return true }

// busyScanner holds the ports it considers taken. A map, not a range, so a
// test can occupy a SINGLE port of a window and still assert the WHOLE window
// moves.
type busyScanner map[int]bool

func (b busyScanner) Free(port int) bool { return !b[port] }

// allBusyScanner never yields: it exists to drive the shift bound.
type allBusyScanner struct{}

func (allBusyScanner) Free(int) bool { return false }

func nestWith(name string, base int, decls ...nest.PortDecl) *nest.Nest {
	return &nest.Nest{Name: name, Ports: nest.Ports{Base: base, Publish: decls}}
}

// goldenNames is FIXED. Adding or removing a name here rewrites the contract
// this golden exists to protect — don't, unless the hash itself is being
// deliberately changed.
var goldenNames = []string{
	"api", "web", "fullstack", "data", "infra",
	"mobile", "docs", "legacy", "x", "nest-42",
}

// TestBaseGolden freezes the window computed for a fixed list of nest names.
//
// The point is NOT that these numbers are right — any stable hash gives
// "right" numbers. The point is that they never change: §8 promises a
// BOOKMARKABLE URL, and a different hash function silently invalidates every
// URL a user wrote down.
func TestBaseGolden(t *testing.T) {
	var b strings.Builder
	for _, name := range goldenNames {
		base := Base(name)
		fmt.Fprintf(&b, "%s %d-%d\n", name, base, base+WindowSize-1)
	}
	path := filepath.Join("testdata", "window.golden")
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if b.String() != string(want) {
		t.Errorf("the port window is a CONTRACT (bookmarkable URLs, spec §8): "+
			"a change here invalidates every URL already noted by a user.\n"+
			"--- got ---\n%s\n--- want ---\n%s", b.String(), want)
	}
}

// The window is aligned on a block of 10 above 9000: the shift on collision
// moves by a whole block, and an unaligned base would interleave two nests.
func TestBaseIsABlockOfTenAboveNineThousand(t *testing.T) {
	for _, name := range goldenNames {
		base := Base(name)
		if base < 9000 || base > 9000+899*10 {
			t.Errorf("Base(%q) = %d, outside 9000..17990", name, base)
		}
		if (base-9000)%WindowSize != 0 {
			t.Errorf("Base(%q) = %d is not aligned on a block of %d", name, base, WindowSize)
		}
	}
}

func TestDeclaredBaseWinsOverTheHash(t *testing.T) {
	n := nestWith("api", 9500, nest.PortDecl{Name: "vite", Container: 5173})
	r, err := Resolve(n, Options{}, freeScanner{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.Window.Base != 9500 {
		t.Errorf("ports.base must win over the hash: got %d, want 9500", r.Window.Base)
	}
	if r.Ports[0].Host != 9500 {
		t.Errorf("first port = %d, want 9500", r.Ports[0].Host)
	}
}

// The offset is the DECLARATION order, and nothing else. The names here sort
// in the reverse of their declaration on purpose: a well-meant `slices.Sort`
// anywhere on this path renumbers every port of the nest.
func TestOffsetFollowsDeclarationOrderNotAlphabetical(t *testing.T) {
	n := nestWith("api", 9000,
		nest.PortDecl{Name: "zulu", Container: 5173},
		nest.PortDecl{Name: "alpha", Container: 3000},
		nest.PortDecl{Name: "mike", Container: 9223},
	)
	r, err := Resolve(n, Options{}, freeScanner{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := []struct {
		name string
		host int
	}{{"zulu", 9000}, {"alpha", 9001}, {"mike", 9002}}
	for i, w := range want {
		if r.Ports[i].Name != w.name || r.Ports[i].Host != w.host {
			t.Errorf("port %d = %s:%d, want %s:%d — the declaration order IS the offset",
				i, r.Ports[i].Name, r.Ports[i].Host, w.name, w.host)
		}
	}
}

func TestResolveRefusesMoreThanOneWindowOfPorts(t *testing.T) {
	decls := make([]nest.PortDecl, WindowSize+1)
	for i := range decls {
		decls[i] = nest.PortDecl{Name: fmt.Sprintf("p%d", i), Container: 3000 + i}
	}
	_, err := Resolve(nestWith("api", 0, decls...), Options{}, freeScanner{})
	if err == nil {
		t.Fatal("more than 10 declared ports must be refused: the 11th would land in the next nest's window")
	}
	if !strings.Contains(err.Error(), "api") || !strings.Contains(err.Error(), "11") {
		t.Errorf("the error must name the nest and the declared count: %v", err)
	}
}

// First instance: the window is canonical, the promised URL.
func TestResolveKeepsTheCanonicalWindowWhenFree(t *testing.T) {
	n := nestWith("api", 9100, nest.PortDecl{Name: "vite", Container: 5173})
	r, err := Resolve(n, Options{}, freeScanner{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !r.Window.Canonical || r.Window.Shifts != 0 {
		t.Errorf("a free window is canonical: %+v", r.Window)
	}
}

// Second instance: ONE port taken moves the WHOLE window. Shifting port by
// port would interleave the two instances' windows.
func TestResolveShiftsTheWholeWindowOnCollision(t *testing.T) {
	n := nestWith("api", 9100,
		nest.PortDecl{Name: "vite", Container: 5173},
		nest.PortDecl{Name: "api", Container: 3000},
	)
	r, err := Resolve(n, Options{}, busyScanner{9103: true})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.Window.Base != 9110 {
		t.Errorf("window base = %d, want 9110 (the whole block moves, not the single busy port)", r.Window.Base)
	}
	if r.Window.Canonical {
		t.Error("a shifted window is NOT canonical: this instance's URLs are not the bookmarkable ones")
	}
	if r.Window.Shifts != 1 {
		t.Errorf("shifts = %d, want 1", r.Window.Shifts)
	}
	if r.Ports[0].Host != 9110 || r.Ports[1].Host != 9111 {
		t.Errorf("ports must follow the shifted window: %d, %d", r.Ports[0].Host, r.Ports[1].Host)
	}
}

func TestResolveBoundsTheNumberOfShifts(t *testing.T) {
	n := nestWith("api", 9100, nest.PortDecl{Name: "vite", Container: 5173})
	_, err := Resolve(n, Options{}, allBusyScanner{})
	if err == nil {
		t.Fatal("an always-busy host must end in an error, never in an unbounded loop")
	}
	if !strings.Contains(err.Error(), "9100") || !strings.Contains(err.Error(), "api") {
		t.Errorf("the error must name the nest and the windows tried: %v", err)
	}
}

// loopback_lock is refused outside loopback EVEN IF FORCED, and it says so in
// its own words: the general "always 127.0.0.1" refusal would also reject this
// bind, so a lock check placed behind it would never run.
func TestResolveRefusesLoopbackLockedPortOutsideLoopback(t *testing.T) {
	n := nestWith("api", 9100,
		nest.PortDecl{Name: "vite", Container: 5173},
		nest.PortDecl{Name: "cdp", Container: 9223, LoopbackLock: true},
	)
	_, err := Resolve(n, Options{HostIP: "0.0.0.0"}, freeScanner{})
	if err == nil {
		t.Fatal("a loopback_lock port must be refused outside loopback, even forced")
	}
	if !strings.Contains(err.Error(), "loopback_lock") || !strings.Contains(err.Error(), "cdp") {
		t.Errorf("the refusal must name the lock and the port it protects: %v", err)
	}
}

// The general rule, with no lock in sight: den binds 127.0.0.1 and nothing
// else. Remote access is a tunnel, never a LAN bind.
func TestResolveRefusesAnyNonLoopbackBind(t *testing.T) {
	n := nestWith("api", 9100, nest.PortDecl{Name: "vite", Container: 5173})
	for _, ip := range []string{"0.0.0.0", "192.168.1.10", "::"} {
		_, err := Resolve(n, Options{HostIP: ip}, freeScanner{})
		if err == nil {
			t.Errorf("%q must be refused: the microVM is the boundary, a LAN bind pierces it host-side", ip)
			continue
		}
		if !strings.Contains(err.Error(), Loopback) {
			t.Errorf("%q: the refusal must name the only accepted address: %v", ip, err)
		}
	}
}

func TestResolveAcceptsAnExplicitLoopback(t *testing.T) {
	n := nestWith("api", 9100, nest.PortDecl{Name: "cdp", Container: 9223, LoopbackLock: true})
	if _, err := Resolve(n, Options{HostIP: Loopback}, freeScanner{}); err != nil {
		t.Errorf("127.0.0.1 asked for explicitly is the default, not a forcing: %v", err)
	}
}

// The publication spec is `sbx ports --publish`'s attested grammar
// (spec §14.0): [[HOST_IP:]HOST_PORT:]SANDBOX_PORT. den always writes it in
// full, host IP included — an omitted IP would let sbx pick the default, and
// the default is not den's to assume.
func TestPublishSpecPinsLoopback(t *testing.T) {
	p := Port{Name: "vite", Container: 5173, Host: 9100}
	if got := p.PublishSpec(); got != "127.0.0.1:9100:5173" {
		t.Errorf("PublishSpec() = %q, want %q", got, "127.0.0.1:9100:5173")
	}
}

func TestResolveWithoutDeclaredPortsYieldsAnEmptyWindow(t *testing.T) {
	r, err := Resolve(nestWith("api", 9100), Options{}, freeScanner{})
	if err != nil {
		t.Fatalf("a nest declaring no port is not an error: %v", err)
	}
	if len(r.Ports) != 0 {
		t.Errorf("no declared port must publish nothing: %+v", r.Ports)
	}
	if !r.Window.Canonical {
		t.Error("nothing to place, nothing to shift: the window stays canonical")
	}
}

// The scan is a system dependency, and Resolve must not silently do without
// it: a nil Scanner would make every window look free and hand the same URLs
// to two instances.
func TestResolveRefusesANilScanner(t *testing.T) {
	n := nestWith("api", 9100, nest.PortDecl{Name: "vite", Container: 5173})
	if _, err := Resolve(n, Options{}, nil); err == nil {
		t.Fatal("a nil scanner must be refused, never treated as an all-free host")
	}
}

func TestResolveCarriesTheDeclarationThrough(t *testing.T) {
	n := nestWith("api", 9100,
		nest.PortDecl{Name: "cdp", Container: 9223, Open: true, LoopbackLock: true},
	)
	r, err := Resolve(n, Options{}, freeScanner{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	got := r.Ports[0]
	if got.Name != "cdp" || got.Container != 9223 || !got.Open || !got.LoopbackLock {
		t.Errorf("the declaration's fields must reach the caller intact: %+v", got)
	}
	if r.Nest != "api" {
		t.Errorf("Nest = %q, want %q", r.Nest, "api")
	}
}
