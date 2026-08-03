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

// refusingScanner fails the test the moment it is consulted. It is the only
// way to prove that a window the sandbox ALREADY publishes on is reused without
// probing the host: a scanner answering "free" would produce the same window
// for the wrong reason, and one answering "busy" would produce a shift the fix
// exists to prevent.
type refusingScanner struct{ t *testing.T }

func (s refusingScanner) Free(port int) bool {
	s.t.Helper()
	s.t.Fatalf("the sandbox is already published on its window: den must reuse it without reading "+
		"the host; port %d was scanned", port)
	return false
}

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

// The exhaustion message may not send the user hunting a listener that is
// their own — and since Resolve reads what THIS sandbox publishes, it must no
// longer name that sandbox's own `den ports` runs among the candidates.
//
// That hedge was the pre-#15 state of the world: `den ports` published a window
// and never took it down, so re-running it found the window busy, shifted, and
// published again, until every candidate block was held by the user's own
// earlier runs. Now Options.Published makes den recognise its own window and
// reuse it, so reaching exhaustion with nothing of this sandbox's in range
// means the blocks really do belong to something else. Naming "an earlier
// `den ports` run of your own sandbox" here would send the user looking for a
// culprit den has already ruled out.
func TestResolveExhaustionRulesOutThisSandboxsOwnPublications(t *testing.T) {
	n := nestWith("api", 9100, nest.PortDecl{Name: "vite", Container: 5173})
	_, err := Resolve(n, Options{}, allBusyScanner{})
	if err == nil {
		t.Fatal("an always-busy host must end in an error")
	}
	// Still the attested command that answers "what does a sandbox publish".
	if !strings.Contains(err.Error(), "sbx ports") {
		t.Errorf("the message must point at the attested listing command; got: %v", err)
	}
	if strings.Contains(err.Error(), "den ports") {
		t.Errorf("den read this sandbox's publications and reused none of them: the message may no "+
			"longer blame an earlier `den ports` run of this sandbox; got: %v", err)
	}
}

// ... unless the sandbox really does hold blocks in the range on mappings the
// nest no longer declares. Those den CAN name, with the pair and the command
// that releases it — the one holder of a busy block that comes with a remedy.
func TestResolveExhaustionNamesThisSandboxsStalePublications(t *testing.T) {
	n := nestWith("api", 9100, nest.PortDecl{Name: "vite", Container: 5173})
	// Published on the canonical block, but for a container port the nest no
	// longer declares: unusable as a window, and in the way of one.
	_, err := Resolve(n, Options{Published: []Published{{Host: 9100, Container: 4444}}}, allBusyScanner{})
	if err == nil {
		t.Fatal("an always-busy host must end in an error")
	}
	for _, want := range []string{"9100->4444", "--unpublish 127.0.0.1:9100:4444"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message must name the stale publication and its remedy (%q); got: %v", want, err)
		}
	}
}

// #15, in the pure layer: a sandbox already published on its canonical window
// resolves back onto it, WITHOUT scanning — the scan is what used to call den's
// own publication a collision and move the whole block.
//
// The scanner is the forbidding one: reusing a window den holds is not a
// question the host can answer, and probing it would be probing ports den has
// already accounted for.
func TestResolveReusesTheWindowThisSandboxIsAlreadyPublishedOn(t *testing.T) {
	n := nestWith("api", 9100,
		nest.PortDecl{Name: "vite", Container: 5173},
		nest.PortDecl{Name: "api", Container: 3000},
	)
	published := []Published{{Host: 9100, Container: 5173}, {Host: 9101, Container: 3000}}

	r, err := Resolve(n, Options{Published: published}, refusingScanner{t: t})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.Window.Base != 9100 || !r.Window.Canonical {
		t.Errorf("window = %d (canonical=%v), want 9100 canonical: the sandbox is already published "+
			"there and re-reading the table must not move it", r.Window.Base, r.Window.Canonical)
	}
	for _, p := range r.Ports {
		if !p.AlreadyPublished {
			t.Errorf("port %q (%d) is already published: it must be marked so, or the caller "+
				"republishes it and sbx answers 409", p.Name, p.Host)
		}
	}
}

// The polluted sandbox of smoke #2 §1.7: three declared ports, three windows
// published, all mapping the same containers. The LOWEST block wins, which is
// the canonical one — the URL the user bookmarked. Healing back to it is what
// makes the fix retroactive on a VM that lived through #15.
func TestResolveReusesTheLowestWindowItHolds(t *testing.T) {
	n := nestWith("api", 9100, nest.PortDecl{Name: "vite", Container: 5173})
	published := []Published{
		{Host: 9120, Container: 5173},
		{Host: 9100, Container: 5173},
		{Host: 9110, Container: 5173},
	}

	r, err := Resolve(n, Options{Published: published}, refusingScanner{t: t})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.Window.Base != 9100 {
		t.Errorf("window base = %d, want 9100: the lowest block this sandbox holds is the canonical "+
			"one, and re-reading must converge back onto the bookmarkable URLs", r.Window.Base)
	}
	if len(r.Stale) != 0 {
		t.Errorf("nothing was skipped, so nothing is stale; got %v", r.Stale)
	}
}

// A window half-published — what #22's aborted run leaves behind — is FINISHED,
// not abandoned. den reuses the block and publishes only the missing port.
func TestResolveFinishesAPartiallyPublishedWindow(t *testing.T) {
	n := nestWith("api", 9100,
		nest.PortDecl{Name: "vite", Container: 5173},
		nest.PortDecl{Name: "api", Container: 3000},
	)

	r, err := Resolve(n, Options{Published: []Published{{Host: 9100, Container: 5173}}},
		refusingScanner{t: t})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.Window.Base != 9100 {
		t.Errorf("window base = %d, want 9100", r.Window.Base)
	}
	if !r.Ports[0].AlreadyPublished {
		t.Error("vite is published: it must not be published again")
	}
	if r.Ports[1].AlreadyPublished {
		t.Error("api was never published: it must be, or the row shows a URL nothing answers")
	}
}

// A publication of this sandbox that does NOT match a declaration is not a
// window: den falls back to the scan, and reports what it skipped so the caller
// can name the one holder it knows.
func TestResolveReportsItsOwnPublicationsInSkippedBlocks(t *testing.T) {
	n := nestWith("api", 9100, nest.PortDecl{Name: "vite", Container: 5173})

	r, err := Resolve(n, Options{Published: []Published{{Host: 9100, Container: 4444}}},
		busyScanner{9100: true})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.Window.Base != 9110 {
		t.Errorf("window base = %d, want 9110: 9100 carries a mapping the nest no longer declares "+
			"and cannot be reused", r.Window.Base)
	}
	if len(r.Stale) != 1 || r.Stale[0] != (Published{Host: 9100, Container: 4444}) {
		t.Errorf("Stale = %v, want the 9100->4444 publication den skipped past", r.Stale)
	}
}

// #22 in the pure layer: an identical `--add` is recognised, not republished.
func TestResolveMarksAnExtraPairTheSandboxAlreadyPublishes(t *testing.T) {
	n := nestWith("api", 9100, nest.PortDecl{Name: "vite", Container: 5173})
	extra := Port{Name: addName(8081), Host: 9801, Container: 8081}

	r, err := Resolve(n, Options{
		Extra:     []Port{extra},
		Published: []Published{{Host: 9100, Container: 5173}, {Host: 9801, Container: 8081}},
	}, refusingScanner{t: t})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	last := r.Ports[len(r.Ports)-1]
	if last.Host != 9801 || !last.AlreadyPublished {
		t.Errorf("the added pair %v must be marked already published: re-running the identical "+
			"command answered 409 before this (#22)", last)
	}
}

// The same reading, on a PORTLESS nest — §1.8's `beta`, where `--add` is the
// whole command. The early return must carry the marking too, or the one nest
// shape `--add` exists for is the one that still 409s.
func TestResolveMarksAnExtraPairOfAPortlessNest(t *testing.T) {
	n := nestWith("beta", 0)
	extra := Port{Name: addName(8080), Host: 9601, Container: 8080}

	r, err := Resolve(n, Options{
		Extra:     []Port{extra},
		Published: []Published{{Host: 9601, Container: 8080}},
	}, refusingScanner{t: t})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(r.Ports) != 1 || !r.Ports[0].AlreadyPublished {
		t.Errorf("the added pair of a portless nest must be marked already published; got %v", r.Ports)
	}
}

// A host port the sandbox publishes to ANOTHER container port is refused, here,
// before anything is bound. sbx keys its 409 on the host port alone, so this
// would fail anyway — late, with the declared ports already published, and with
// a message naming neither the mapping in the way nor how to clear it.
func TestResolveRefusesAnExtraPairOnAHostPortPublishedElsewhere(t *testing.T) {
	n := nestWith("api", 9100, nest.PortDecl{Name: "vite", Container: 5173})

	_, err := Resolve(n, Options{
		Extra:     []Port{{Name: addName(9999), Host: 9801, Container: 9999}},
		Published: []Published{{Host: 9801, Container: 8081}},
	}, refusingScanner{t: t})
	if err == nil {
		t.Fatal("a host port already published to another container port must be refused")
	}
	for _, want := range []string{"9801", "8081", "--unpublish 127.0.0.1:9801:8081"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name %q; got: %v", want, err)
		}
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

// An allBusyScanner on purpose: a nest with no port binds nothing, so it must
// never probe the host — a saturated window (which would fail any real scan)
// cannot fail it.
func TestResolveWithoutDeclaredPortsYieldsAnEmptyWindow(t *testing.T) {
	r, err := Resolve(nestWith("api", 9100), Options{}, allBusyScanner{})
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

// A declared `ports.base:` is the nest's own choice, but not an unchecked
// one: the hashed base is aligned and in range by construction, and a declared
// base must offer the same guarantees before the scan trusts it.
func TestResolveRefusesADeclaredBaseBelowPrivilegedBoundary(t *testing.T) {
	n := nestWith("api", 80, nest.PortDecl{Name: "vite", Container: 5173})
	_, err := Resolve(n, Options{}, freeScanner{})
	if err == nil {
		t.Fatal("a base in the privileged range must be refused, never probed")
	}
	if !strings.Contains(err.Error(), "api") || !strings.Contains(err.Error(), "80") {
		t.Errorf("the error must name the nest and the declared base: %v", err)
	}
}

func TestResolveRefusesADeclaredBaseTooHighForTheShiftRange(t *testing.T) {
	n := nestWith("api", 65530, nest.PortDecl{Name: "vite", Container: 5173})
	_, err := Resolve(n, Options{}, freeScanner{})
	if err == nil {
		t.Fatal("a base whose shifted window can pass 65535 must be refused up front, " +
			"not reported later as a mysterious \"no free window\"")
	}
	if !strings.Contains(err.Error(), "65530") || !strings.Contains(err.Error(), "65426") {
		t.Errorf("the error must name the declared base and the highest acceptable one: %v", err)
	}
}

func TestResolveRefusesAnUnalignedDeclaredBase(t *testing.T) {
	n := nestWith("api", 9505, nest.PortDecl{Name: "vite", Container: 5173})
	_, err := Resolve(n, Options{}, freeScanner{})
	if err == nil {
		t.Fatal("an unaligned base must be refused: its window interleaves with the hashed windows of neighbours")
	}
	if !strings.Contains(err.Error(), "9505") || !strings.Contains(err.Error(), "9500") || !strings.Contains(err.Error(), "9510") {
		t.Errorf("the error must name the declared base and the two aligned candidates around it: %v", err)
	}
}

// The hashed range 9000..17990 bounds what den PICKS, not what a nest may
// ASK for: an explicit, aligned, in-range base above it is a valid choice.
func TestResolveAcceptsAnAlignedDeclaredBaseOutsideTheHashedRange(t *testing.T) {
	n := nestWith("api", 20000, nest.PortDecl{Name: "vite", Container: 5173})
	r, err := Resolve(n, Options{}, freeScanner{})
	if err != nil {
		t.Fatalf("an aligned base outside the hashed range is a valid explicit choice: %v", err)
	}
	if r.Window.Base != 20000 {
		t.Errorf("window base = %d, want 20000", r.Window.Base)
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

// `--add H:C` is a pair the NEST never declared: the host port is the one the
// user wrote, verbatim, so it takes no window offset.
func TestParseAddYieldsTheHostAndContainerPair(t *testing.T) {
	p, err := ParseAdd("8080:3000")
	if err != nil {
		t.Fatalf("`8080:3000` is the documented form of --add: %v", err)
	}
	if p.Host != 8080 || p.Container != 3000 {
		t.Errorf("ParseAdd(\"8080:3000\") = host %d, container %d; want 8080 and 3000", p.Host, p.Container)
	}
	if p.Open || p.LoopbackLock {
		t.Errorf("an --add pair declares no flag: %+v", p)
	}
	if got := p.PublishSpec(); got != "127.0.0.1:8080:3000" {
		t.Errorf("PublishSpec() = %q, want %q — an added pair binds the loopback like any other", got,
			"127.0.0.1:8080:3000")
	}
}

// The three-field form is accepted ONLY on 127.0.0.1: the flag exists to add a
// pair, never to widen the bind §8 refuses to widen anywhere else.
func TestParseAddRefusesANonLoopbackHostAddress(t *testing.T) {
	for _, value := range []string{
		"0.0.0.0:8080:3000",
		"192.168.1.10:8080:3000",
		// Not Loopback either, for the reason isLoopback's godoc gives:
		// "localhost" goes through the host's name service and can answer
		// something other than 127.0.0.1. (The other half of that godoc, the
		// IPv6 literal, is refused one door earlier — `::1:8080:3000` splits
		// into empty fields and is malformed, not merely non-loopback.)
		"localhost:8080:3000",
	} {
		_, err := ParseAdd(value)
		if err == nil {
			t.Errorf("--add %q must be refused: den publishes on %s and nothing else", value, Loopback)
			continue
		}
		if !strings.Contains(err.Error(), Loopback) {
			t.Errorf("--add %q: the refusal must name the only address den binds: %v", value, err)
		}
	}
	// The refused address is named, so the user reads what den rejected rather
	// than having to guess which field was wrong.
	_, err := ParseAdd("0.0.0.0:8080:3000")
	if !strings.Contains(err.Error(), "0.0.0.0") {
		t.Errorf("the refusal must name the refused address: %v", err)
	}
	// ... and the remedy is the one §8 offers everywhere else: a tunnel.
	if !strings.Contains(err.Error(), "ssh -L") {
		t.Errorf("the refusal must point at the tunnel, like Resolve's own: %v", err)
	}
}

func TestParseAddAcceptsAnExplicitLoopback(t *testing.T) {
	p, err := ParseAdd("127.0.0.1:8080:3000")
	if err != nil {
		t.Fatalf("127.0.0.1 written out is the default, not a forcing: %v", err)
	}
	if p.Host != 8080 || p.Container != 3000 {
		t.Errorf("host %d, container %d; want 8080 and 3000", p.Host, p.Container)
	}
}

func TestParseAddRefusesAMalformedValue(t *testing.T) {
	for _, value := range []string{
		"",                        // nothing at all
		"8080",                    // no container port
		"8080:3000:9000:1",        // one field too many
		"8080:",                   // an empty container port
		":3000",                   // an empty host port
		"127.0.0.1::3000",         // an empty host port, three-field form
		":8080:3000",              // an empty host address
		"::1:8080:3000",           // an IPv6 literal: ambiguous in a colon-separated grammar
		"vite:3000",               // a name is not a host port
		"8080:api",                // ... nor a container port
		"0:3000",                  // port 0 is not a port a user can reach
		"70000:3000",              // above 65535
		"8080:70000",              // ... on either side
		" 8080:3000",              // Atoi accepts no padding, and neither does sbx
		"127.0.0.1:8080:3000/tcp", // den declares no protocol (see PublishSpec)
	} {
		if _, err := ParseAdd(value); err == nil {
			t.Errorf("--add %q must be refused: it is not a HOST_PORT:CONTAINER_PORT pair", value)
		}
	}
}

// The added pair travels WITH the declared ports — same resolution, same
// publication, same table — but it is the LAST of them: the declared ports keep
// the offsets §8 numbers them by.
func TestResolveAppendsTheExtraPairAfterTheDeclaredPorts(t *testing.T) {
	n := nestWith("api", 9100,
		nest.PortDecl{Name: "vite", Container: 5173},
		nest.PortDecl{Name: "api", Container: 3000},
	)
	extra, err := ParseAdd("8080:3000")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// The scanner holds 8080 as TAKEN: an added pair is not scanned, because
	// the host port is the user's own explicit choice — shifting it would
	// publish an address they never asked for.
	r, err := Resolve(n, Options{Extra: []Port{extra}}, busyScanner{8080: true})
	if err != nil {
		t.Fatalf("an added pair is not scanned, so a busy host port cannot fail the resolution: %v", err)
	}
	if len(r.Ports) != 3 {
		t.Fatalf("2 declared ports + 1 added = 3: %+v", r.Ports)
	}
	if r.Ports[0].Host != 9100 || r.Ports[1].Host != 9101 {
		t.Errorf("the declared ports keep their offsets: %d, %d", r.Ports[0].Host, r.Ports[1].Host)
	}
	last := r.Ports[2]
	if last.Host != 8080 || last.Container != 3000 {
		t.Errorf("the added pair takes no offset: host %d, container %d; want 8080 and 3000",
			last.Host, last.Container)
	}
	if r.Window.Base != 9100 || !r.Window.Canonical {
		t.Errorf("an added pair moves no window: %+v", r.Window)
	}
}

// A nest declaring nothing still publishes what the user added on the command
// line — the early return of a portless nest must not swallow it.
func TestResolveCarriesTheExtraPairOfAPortlessNest(t *testing.T) {
	extra, err := ParseAdd("8080:3000")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	r, err := Resolve(nestWith("api", 9100), Options{Extra: []Port{extra}}, allBusyScanner{})
	if err != nil {
		t.Fatalf("a portless nest with an added pair scans nothing and fails on nothing: %v", err)
	}
	if len(r.Ports) != 1 || r.Ports[0].Host != 8080 {
		t.Fatalf("the added pair must reach the caller: %+v", r.Ports)
	}
}

// The window bounds the DECLARED ports, the ones it numbers. An added pair
// takes no offset, so it cannot overflow a window it never occupies.
func TestResolveDoesNotCountExtraPairsAgainstTheWindow(t *testing.T) {
	decls := make([]nest.PortDecl, WindowSize)
	for i := range decls {
		decls[i] = nest.PortDecl{Name: fmt.Sprintf("p%d", i), Container: 3000 + i}
	}
	extra, err := ParseAdd("8080:3000")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	r, err := Resolve(nestWith("api", 9100, decls...), Options{Extra: []Port{extra}}, freeScanner{})
	if err != nil {
		t.Fatalf("a full window plus an added pair is not an overflow: %v", err)
	}
	if len(r.Ports) != WindowSize+1 {
		t.Errorf("%d ports resolved, want %d declared + 1 added", len(r.Ports), WindowSize)
	}
}

// An added pair asking for a host port the window ALREADY gave to a declared
// port is refused here, before anything is published.
//
// The caller publishes one pair at a time (`sbx ports … --publish` per port),
// so a resolution carrying 9100, 9101 and an added 9101 binds the first two and
// then fails on den's OWN collision: the window is left half-bound, no table is
// printed, and the error names sbx rather than the two lines of the request
// that cannot both be honoured.
func TestResolveRefusesAnExtraPairOnADeclaredHostPort(t *testing.T) {
	n := nestWith("web", 9100,
		nest.PortDecl{Name: "vite", Container: 5173},
		nest.PortDecl{Name: "api", Container: 3000},
		nest.PortDecl{Name: "cdp", Container: 9223},
	)
	extra, err := ParseAdd("9101:4000")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Resolve(n, Options{Extra: []Port{extra}}, freeScanner{}); err == nil {
		t.Fatal("`--add 9101:4000` lands on the host port the window already gave to `api`: den would " +
			"publish the first ports, then fail on its own collision with the window half-bound")
	} else {
		for _, fragment := range []string{"9101", "4000", "api"} {
			if !strings.Contains(err.Error(), fragment) {
				t.Errorf("the refusal must name the added pair and the declared port it collides with "+
					"(%q missing): %v", fragment, err)
			}
		}
	}
}

// The collision is read against the RESOLVED window, not the canonical one: a
// shifted window renumbers every declared port, so the host ports an added pair
// must clear are the ones this run actually publishes.
func TestResolveReadsTheExtraPairAgainstTheShiftedWindow(t *testing.T) {
	n := nestWith("web", 9100,
		nest.PortDecl{Name: "vite", Container: 5173},
		nest.PortDecl{Name: "api", Container: 3000},
	)
	extra, err := ParseAdd("9111:4000")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// One busy port moves the whole block to 9110: `api` is on 9111 now, and
	// that — not the canonical 9101 — is what the added pair collides with.
	if _, err := Resolve(n, Options{Extra: []Port{extra}}, busyScanner{9105: true}); err == nil {
		t.Fatal("the shifted window put `api` on 9111: an added 9111 collides with it")
	} else if !strings.Contains(err.Error(), "9111") || !strings.Contains(err.Error(), "api") {
		t.Errorf("the refusal must name the shifted host port and the declared port holding it: %v", err)
	}
}

// Two `--add` values naming the same host port collide with each other, and are
// refused on the same terms: the first would bind, the second would fail, and
// the pair of lines that cannot both be honoured is knowable from the flags
// alone — before the scan, and before a single publication.
func TestResolveRefusesTwoExtraPairsOnTheSameHostPort(t *testing.T) {
	first, err := ParseAdd("8080:3000")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	second, err := ParseAdd("8080:4000")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	n := nestWith("web", 9100, nest.PortDecl{Name: "vite", Container: 5173})
	if _, err := Resolve(n, Options{Extra: []Port{first, second}}, freeScanner{}); err == nil {
		t.Fatal("`--add 8080:3000 --add 8080:4000` asks for one host port twice: the second publication " +
			"would fail with the first already bound")
	} else {
		for _, fragment := range []string{"8080", "3000", "4000"} {
			if !strings.Contains(err.Error(), fragment) {
				t.Errorf("the refusal must name both added pairs (%q missing): %v", fragment, err)
			}
		}
	}
}

// ... including on a nest that declares nothing at all: the portless early
// return publishes the added pairs, so it must weigh them against each other
// too — a refusal placed only on the declared path would let this one through.
func TestResolveRefusesTwoExtraPairsOnTheSameHostPortOfAPortlessNest(t *testing.T) {
	first, err := ParseAdd("8080:3000")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	second, err := ParseAdd("8080:4000")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Resolve(nestWith("web", 9100), Options{Extra: []Port{first, second}}, freeScanner{}); err == nil {
		t.Fatal("a portless nest publishes the added pairs and nothing else: two of them on 8080 still " +
			"collide")
	} else if !strings.Contains(err.Error(), "8080") {
		t.Errorf("the refusal must name the host port asked for twice: %v", err)
	}
}

// The refusal is an EQUALITY, never a range: the window bounds what den
// NUMBERS, and a host port inside the block that no declared port was given is
// a port den publishes nothing on. Refusing it would reject a pair that binds
// perfectly well.
func TestResolveAcceptsAnExtraPairOnAnUnusedPortOfTheWindow(t *testing.T) {
	n := nestWith("web", 9100,
		nest.PortDecl{Name: "vite", Container: 5173},
		nest.PortDecl{Name: "api", Container: 3000},
	)
	extra, err := ParseAdd("9105:4000")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	r, err := Resolve(n, Options{Extra: []Port{extra}}, freeScanner{})
	if err != nil {
		t.Fatalf("9105 is inside the window but assigned to no declared port: %v", err)
	}
	if len(r.Ports) != 3 || r.Ports[2].Host != 9105 {
		t.Errorf("the added pair must reach the caller unchanged: %+v", r.Ports)
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
