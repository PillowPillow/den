package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/doctor"
	"github.com/PillowPillow/den/internal/ports"
	"github.com/PillowPillow/den/internal/sbx"
)

// freeScanner answers "free" everywhere: the first instance of a nest, the
// common case, and the only one against which a golden can be frozen.
type freeScanner struct{}

func (freeScanner) Free(int) bool { return true }

// busyBlockScanner holds ONE block of ports.WindowSize as taken. It is what
// drives the collision shift: the canonical window is refused, the next one
// answers free, so the whole block moves by exactly ports.WindowSize.
type busyBlockScanner struct{ base int }

func (b busyBlockScanner) Free(port int) bool {
	return port < b.base || port >= b.base+ports.WindowSize
}

// forbiddenScanner fails the test if it is consulted at all — the double for
// every case where den must reach a window WITHOUT reading the host. There are
// two, and nothing but a scanner that refuses to answer tells them apart from a
// scan that happened to agree:
//
//   - a nest declaring no port never touches the host ("no publication" is only
//     half the promise, "no scan" is the other half);
//   - a sandbox already published on its window reuses it (#15): a scanner
//     answering "free" would produce the same window for the wrong reason, and
//     one answering "busy" would produce the shift the fix exists to prevent.
type forbiddenScanner struct{ t *testing.T }

func (s forbiddenScanner) Free(port int) bool {
	s.t.Helper()
	s.t.Fatalf("den must reach its window without probing the host here; port %d was scanned", port)
	return false
}

// pub builds one entry of the `ports` array of `sbx ls --json`, in the schema
// recorded 2026-07-31: loopback and tcp, the only shape den publishes.
func pub(host, container int) string {
	return fmt.Sprintf(`{"host_ip":"127.0.0.1","host_port":%d,"sandbox_port":%d,"protocol":"tcp"}`,
		host, container)
}

// lsWithPorts is lsWith for ONE running sandbox that already publishes
// something — the fixture of every re-run case. The array is written as raw
// JSON rather than marshalled from sbx.Publication on purpose: what these tests
// pin is den's reading of sbx's output, and a fixture built from den's own
// struct would agree with it by construction.
func lsWithPorts(name string, publications ...string) map[string]sbx.Response {
	out := fmt.Sprintf(
		`{"sandboxes":[{"name":%q,"status":"running","ports":[%s],"workspaces":["/w"]}]}`,
		name, strings.Join(publications, ","))
	return map[string]sbx.Response{"ls --json": {Output: []byte(out)}}
}

// runPorts runs `den ports` through the REAL command tree, with Deps built BY
// HAND — never sbxDeps, and never SystemDeps.
//
// This is not a style preference. sbxDeps is SystemDeps with Sbx swapped, so
// its Scanner is the real ports.ListenScanner: a single test reaching for it
// would bind host sockets across 9000-17990 on the machine running the suite,
// and the verdict would depend on whatever else that machine happens to be
// listening on. Everything `den ports` needs is injected here instead.
//
// The opener is left NIL here, and that is the second hermeticity guarantee of
// this helper: every test but the two that inject their own double goes through
// this door, so no run of the suite can launch a browser on the machine.
//
// Separate streams, because half of what this command promises is WHICH stream
// a line lands on (the table on stdout, the non-canonical warning on stderr).
// The runner is an sbx.Runner, not a *sbx.Fake: one case (#16's wake) needs a
// second `ls --json` to answer differently from the first, which the Fake's
// argv-keyed Responses cannot express — see wakingRunner. Every other call site
// passes the Fake itself.
func runPorts(t *testing.T, f sbx.Runner, s ports.Scanner, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return runPortsWithOpener(t, f, s, nil, args...)
}

// wakingRunner is an sbx.Fake whose SECOND `ls --json` answers as a woken
// sandbox does: running, and publishing what the stopped listing hid.
//
// It exists rather than a `Sequence` field on sbx.Fake because the need is one
// test's, and sbx.Fake is a production type three packages share: a
// call-ordering feature there would be a contract everyone can accidentally
// depend on. Everything else — Calls, HasCalled — comes through the embedded
// Fake unchanged.
type wakingRunner struct {
	*sbx.Fake
	woken   []byte
	lsCount int
}

func (r *wakingRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	out, err := r.Fake.Run(ctx, args...)
	if len(args) == 2 && args[0] == "ls" {
		r.lsCount++
		if r.lsCount > 1 {
			return slices.Clone(r.woken), nil
		}
	}
	return out, err
}

// runPortsWithOpener is runPorts with the browser opener supplied — a FAKE,
// always: ports.OpenURL, the real one, is never named by a test.
func runPortsWithOpener(t *testing.T, f sbx.Runner, s ports.Scanner, open func(string) error,
	args ...string) (stdout, stderr string, err error) {
	t.Helper()
	deps := Deps{Doctor: doctor.FakeDeps(), Sbx: f, Scanner: s, Open: open}
	return executeCmdSeparateStreams(t, NewRootCmdWith(deps), args...)
}

// portsNestYAML is the nest of spec §8's own sample: a declared base (so the
// golden is independent of the hash, already pinned by
// internal/ports/testdata/window.golden), one opened port, one plain, one
// loopback-locked.
const portsNestYAML = `stack: devx
ports:
  base: 9100
  publish:
    - { name: vite, container: 5173, open: true }
    - { name: api,  container: 3000 }
    - { name: cdp,  container: 9223, loopback_lock: true }
`

// portsTwoPortNestYAML is portsNestYAML's plainer sibling: a declared base and
// TWO ports, no marker. It is the nest of the `--add` cases, where the third
// publication must be the added pair and nothing else — a third declared port
// would make "the last call" ambiguous to read in the golden.
const portsTwoPortNestYAML = `stack: devx
ports:
  base: 9100
  publish:
    - { name: vite, container: 5173 }
    - { name: api,  container: 3000 }
`

// portsDenHome writes a den home holding exactly one nest. `den ports` reads
// nothing else from it — no config.yaml — and that is deliberate: the fixture
// says so.
func portsDenHome(t *testing.T, nestName, content string) string {
	t.Helper()
	dir := t.TempDir()
	writeNest(t, dir, nestName, content)
	return dir
}

// portsCalls keeps the `sbx ports …` invocations of a run, in order — the
// publications, and nothing else: `ls --json` travels through the same Fake.
// The filter is on the FIRST argument, never on containment: a workspace path
// or a sandbox named "ports" must not be mistaken for a publication.
func portsCalls(f *sbx.Fake) [][]string {
	var out [][]string
	for _, c := range f.Calls {
		if len(c) > 0 && c[0] == "ports" {
			out = append(out, c)
		}
	}
	return out
}

// assertPublishGolden freezes the publications against a golden, in the format
// of internal/sbx/testdata/create-*.golden: one argv per line, arguments joined
// by a space, trailing newline included. The file is a PARAMETER so that a
// second case (`--add`) records itself in the same format rather than growing a
// second serializer beside this one.
func assertPublishGolden(t *testing.T, f *sbx.Fake, file string) [][]string {
	t.Helper()
	calls := portsCalls(f)
	lines := make([]string, 0, len(calls))
	for _, c := range calls {
		lines = append(lines, strings.Join(c, " "))
	}
	got := strings.Join(lines, "\n") + "\n"

	path := filepath.Join("testdata", file)
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if got != string(want) {
		t.Errorf("%s\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
	return calls
}

// rowFor returns the table row of a named port, or "" when absent.
func rowFor(stdout, name string) string {
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), name+" ") {
			return line
		}
	}
	return ""
}

// C1: the argument is a SANDBOX name, exactly one, and a wrong count is
// refused BEFORE anything is asked of sbx — the same contract as `den sh` and
// `den rm`, through the same argsBetween validator.
func TestPortsTakesExactlyOneSandboxName(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		expected string
	}{
		{
			"missing argument",
			[]string{"ports"},
			"den ports: one argument expected, none received — usage: den ports <name> [flags]",
		},
		{
			"two arguments",
			[]string{"ports", "a", "b"},
			`den ports: exactly one argument expected, 2 received, starting with "b" — usage: den ports <name> [flags]`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &sbx.Fake{}
			_, _, err := runPorts(t, f, freeScanner{},
				append([]string{"--den-home", t.TempDir()}, c.args...)...)
			if err == nil {
				t.Fatalf("%v must be rejected on argument count", c.args)
			}
			if err.Error() != c.expected {
				t.Errorf("message = %q, expected %q", err.Error(), c.expected)
			}
			if len(f.Calls) != 0 {
				t.Errorf("a rejected argument count must reach sbx for nothing; calls: %v", f.Calls)
			}
		})
	}
}

// C4/C5/C6: the §8 table, frozen byte-for-byte.
//
// The golden is the contract on the WHOLE render — header, column alignment,
// marker placement, the tunnel line — which no set of substring assertions
// covers: they say what must appear, never what must not have moved. The
// explicit assertions below it are the ones a reader of a failing diff needs
// spelled out.
//
// The scheme is `http://` on EVERY row, including the loopback-locked one:
// spec §8's sample writes `ws://` for cdp, but den has no way to know a
// declared port's protocol — `ports.publish` carries none — and inventing one
// per port name would be guessing on the user's behalf.
func TestPortsRendersTheSpecTable(t *testing.T) {
	denHome := portsDenHome(t, "web", portsNestYAML)
	f := &sbx.Fake{Responses: lsWith("web.feat123")}

	stdout, stderr, err := runPorts(t, f, freeScanner{},
		"--den-home", denHome, "ports", "web.feat123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	path := filepath.Join("testdata", "ports-table.golden")
	want, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("reading %s: %v", path, readErr)
	}
	if stdout != string(want) {
		t.Errorf("the §8 table is a contract.\n--- got ---\n%s\n--- want ---\n%s", stdout, want)
	}
	if stderr != "" {
		t.Errorf("a canonical window has nothing to warn about; stderr = %q", stderr)
	}

	// C5/C9, per row: the markers belong to the ports that declared them, and
	// to no others. A golden alone would go green on a render that put every
	// marker on every row, as long as the file was regenerated with it.
	if row := rowFor(stdout, "cdp"); !strings.Contains(row, "[loopback-locked]") {
		t.Errorf("the loopback_lock port must be marked; row = %q", row)
	}
	for _, name := range []string{"vite", "api"} {
		if row := rowFor(stdout, name); strings.Contains(row, "[loopback-locked]") {
			t.Errorf("port %q does not declare loopback_lock; row = %q", name, row)
		}
	}
	if row := rowFor(stdout, "vite"); !strings.Contains(row, "[opened]") {
		t.Errorf("the `open: true` port must be marked; row = %q", row)
	}
	for _, name := range []string{"api", "cdp"} {
		if row := rowFor(stdout, name); strings.Contains(row, "[opened]") {
			t.Errorf("port %q does not declare open; row = %q", name, row)
		}
	}

	// C6: the tunnel line is paste-ready and machine-independent — the literal
	// `you@$(hostname)`, expanded by the user's own shell, never by den.
	for _, want := range []string{"ssh -L 9100:127.0.0.1:9100", "you@$(hostname)"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout must contain %q; got:\n%s", want, stdout)
		}
	}
	// C4: every row on the same scheme, the locked one included.
	for _, want := range []string{
		"http://127.0.0.1:9100", "http://127.0.0.1:9101", "http://127.0.0.1:9102",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout must contain %q; got:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "ws://") {
		t.Errorf("den declares no protocol per port: the scheme is uniform; got:\n%s", stdout)
	}
}

// C3: publication is ONE `sbx ports <sandbox> --publish …` per declared port,
// in declaration order — never a single argv carrying three --publish flags.
//
// The distinction is not cosmetic: a batched call is all-or-nothing on sbx's
// side, so one port already taken in the VM would take the whole window down
// with it, and the error would name a spec without saying which of the nest's
// ports it belongs to.
//
// The golden freezes the argv the way internal/sbx/testdata/create-*.golden
// freezes `sbx create`'s; the assertions below it are what a reader of a
// failing diff needs spelled out — and what keeps a regenerated golden from
// blessing a batched call.
func TestPortsPublishesOneCallPerDeclaredPort(t *testing.T) {
	denHome := portsDenHome(t, "web", portsNestYAML)
	f := &sbx.Fake{Responses: lsWith("web.feat123")}

	_, _, err := runPorts(t, f, freeScanner{},
		"--den-home", denHome, "ports", "web.feat123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := assertPublishGolden(t, f, "ports-publish.golden")

	// One call per declared port: the nest of portsNestYAML declares three.
	if len(calls) != 3 {
		t.Fatalf("the nest declares 3 ports, so 3 publications are expected; got %d: %v",
			len(calls), calls)
	}
	// ... and each one carries EXACTLY ONE --publish. A single call bearing
	// three of them would still count as three flags, but as one publication.
	for _, c := range calls {
		n := 0
		for _, a := range c {
			if a == "--publish" {
				n++
			}
		}
		if n != 1 {
			t.Errorf("each publication carries exactly one --publish; got %d in %v", n, c)
		}
		if c[1] != "web.feat123" {
			t.Errorf("the sandbox is the first positional of `sbx ports`; got %v", c)
		}
	}
	// DECLARATION ORDER, the one that numbers the host ports (ports.Resolve):
	// publishing out of order would bind the URLs the table prints to the
	// wrong containers.
	var gotSpecs []string
	for _, c := range calls {
		gotSpecs = append(gotSpecs, c[len(c)-1])
	}
	wantSpecs := []string{"127.0.0.1:9100:5173", "127.0.0.1:9101:3000", "127.0.0.1:9102:9223"}
	if !slices.Equal(gotSpecs, wantSpecs) {
		t.Errorf("specs = %v, want the declaration order %v", gotSpecs, wantSpecs)
	}
}

// C3, the failure half: a refused publication ABORTS, with the runner's own
// error and before the table is printed. Continuing would leave the user a
// table promising URLs that were never bound.
func TestPortsAbortsOnTheFirstFailedPublish(t *testing.T) {
	denHome := portsDenHome(t, "web", portsNestYAML)
	boom := errors.New("sbx: address already in use")
	responses := lsWith("web.feat123")
	// The SECOND publication fails: a first-call failure would go green on an
	// implementation that collects every error and publishes on regardless.
	responses["ports web.feat123 --publish 127.0.0.1:9101:3000"] = sbx.Response{Err: boom}
	f := &sbx.Fake{Responses: responses}

	stdout, _, err := runPorts(t, f, freeScanner{},
		"--den-home", denHome, "ports", "web.feat123")
	if err == nil {
		t.Fatal("a refused publication must fail the command")
	}
	if !errors.Is(err, boom) {
		t.Errorf("the runner's own error must reach the user; got: %v", err)
	}
	if n := len(portsCalls(f)); n != 2 {
		t.Errorf("publication stops on the first failure: expected 2 calls, got %d: %v",
			n, portsCalls(f))
	}
	if strings.Contains(stdout, "NAME") || strings.Contains(stdout, "http://") {
		t.Errorf("no table may be printed for a window that was not published; got:\n%s", stdout)
	}
}

// C2: the window is seeded by the NEST, not by the sandbox.
//
// `den ports api` and `den ports api.feat12` are two instances of ONE nest: a
// window seeded by the sandbox name would give each worktree its own base, and
// the bookmarkable URL of §8 would stop existing the moment a worktree is
// spawned. The two renders must place every port identically.
func TestPortsSeedsTheWindowOnTheNestNotTheSandbox(t *testing.T) {
	denHome := portsDenHome(t, "api", "stack: devx\nports:\n  publish:\n"+
		"    - { name: vite, container: 5173 }\n    - { name: api, container: 3000 }\n")
	base := ports.Base("api")

	f := &sbx.Fake{Responses: lsWith("api", "api.feat12")}
	plain, _, err := runPorts(t, f, freeScanner{}, "--den-home", denHome, "ports", "api")
	if err != nil {
		t.Fatalf("den ports api: %v", err)
	}
	worktree, _, err := runPorts(t, f, freeScanner{}, "--den-home", denHome, "ports", "api.feat12")
	if err != nil {
		t.Fatalf("den ports api.feat12: %v", err)
	}

	// Everything below the header line: the rows and the tunnel line, i.e.
	// every host port the command published a URL for.
	bodyOf := func(s string) string { _, body, _ := strings.Cut(s, "\n"); return body }
	if bodyOf(plain) != bodyOf(worktree) {
		t.Errorf("a worktree must land on its nest's window.\n--- api ---\n%s\n--- api.feat12 ---\n%s",
			plain, worktree)
	}
	for _, want := range []string{
		fmt.Sprintf("window: %d-%d", base, base+ports.WindowSize-1),
		fmt.Sprintf("http://127.0.0.1:%d", base),
		fmt.Sprintf("http://127.0.0.1:%d", base+1),
	} {
		for _, got := range []string{plain, worktree} {
			if !strings.Contains(got, want) {
				t.Errorf("output must contain %q; got:\n%s", want, got)
			}
		}
	}
	// The header still names the sandbox the user asked for: only the WINDOW
	// comes from the nest.
	if !strings.Contains(worktree, "nest: api") || !strings.Contains(worktree, "sandbox: api.feat12") {
		t.Errorf("the header must name both the nest and the sandbox; got:\n%s", worktree)
	}
}

// C7: a busy canonical window moves the WHOLE block to the next one, and the
// header stops claiming the canonical, bookmarkable range.
func TestPortsShiftsTheWholeWindowWhenTheCanonicalOneIsBusy(t *testing.T) {
	denHome := portsDenHome(t, "api", "stack: devx\nports:\n  publish:\n"+
		"    - { name: vite, container: 5173 }\n    - { name: api, container: 3000 }\n")
	base := ports.Base("api")
	shifted := base + ports.WindowSize
	f := &sbx.Fake{Responses: lsWith("api")}

	stdout, stderr, err := runPorts(t, f, busyBlockScanner{base: base},
		"--den-home", denHome, "ports", "api")
	if err != nil {
		t.Fatalf("a busy window must shift, not fail: %v", err)
	}
	for _, want := range []string{
		fmt.Sprintf("window: %d-%d", shifted, shifted+ports.WindowSize-1),
		fmt.Sprintf("http://127.0.0.1:%d", shifted),
		fmt.Sprintf("http://127.0.0.1:%d", shifted+1),
		fmt.Sprintf("ssh -L %d:127.0.0.1:%d", shifted, shifted),
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the whole block must move to the next one: expected %q; got:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, fmt.Sprintf("127.0.0.1:%d", base)) {
		t.Errorf("no port of the busy window may be published; got:\n%s", stdout)
	}
	// The header, pinned by EQUALITY rather than by a list of forbidden words.
	// `(canonical)` — the claim "this URL is bookmarkable" — must be gone, and
	// nothing may take its place: the diagnosis of a moved window belongs to
	// stderr (asserted just below), so the header carries the range and stops
	// there. A forbidden-substring list would go green on any wording nobody
	// thought to forbid; an equality cannot.
	header, _, _ := strings.Cut(stdout, "\n")
	wantHeader := fmt.Sprintf("nest: api   sandbox: api   window: %d-%d",
		shifted, shifted+ports.WindowSize-1)
	if header != wantHeader {
		t.Errorf("a shifted window's header states the range and nothing else\n got: %q\nwant: %q",
			header, wantHeader)
	}
	// C7's substance — "this window is not the canonical one, and is valid for
	// this instance only" — is SAID, in full, on the stream that carries
	// diagnostics.
	for _, want := range []string{"canonical", "THIS INSTANCE ONLY"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr must state %q; got: %q", want, stderr)
		}
	}
}

// C8: the same split internal/spawn enforces on its SSH warnings — the
// diagnostic on stderr, the table on stdout, neither carrying a piece of the
// other. A user piping `den ports` into a script gets the table and nothing
// else; a user reading the terminal gets the warning.
func TestPortsWarnsOnStderrAndTablesOnStdout(t *testing.T) {
	denHome := portsDenHome(t, "api", "stack: devx\nports:\n  publish:\n"+
		"    - { name: vite, container: 5173 }\n")
	base := ports.Base("api")
	f := &sbx.Fake{Responses: lsWith("api")}

	stdout, stderr, err := runPorts(t, f, busyBlockScanner{base: base},
		"--den-home", denHome, "ports", "api")
	if err != nil {
		t.Fatalf("a busy window must shift, not fail: %v", err)
	}
	for _, want := range []string{
		"warning", "busy", "moved the whole block",
		// The CANONICAL range, the one the user may have bookmarked: without
		// it the warning says a window moved without ever naming the window
		// that was taken, and the back-computation of that range (the shift
		// count times the block size) would be free to be wrong.
		fmt.Sprintf("%d-%d", base, base+ports.WindowSize-1),
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr must carry the warning (%q); got: %q", want, stderr)
		}
	}
	// No fragment of the table on stderr: not the column header, not a row.
	for _, unwanted := range []string{"NAME", "CONTAINER", "URL", "http://"} {
		if strings.Contains(stderr, unwanted) {
			t.Errorf("stderr = %q, must carry no part of the table (%q)", stderr, unwanted)
		}
	}
	// ... and no fragment of the warning on stdout — including the two words
	// most tempting to echo in the header, `canonical` and `instance`.
	for _, unwanted := range []string{"warning", "busy", "moved the whole block", "canonical", "instance"} {
		if strings.Contains(stdout, unwanted) {
			t.Errorf("stdout = %q, must carry no part of the warning (%q)", stdout, unwanted)
		}
	}
	if !strings.Contains(stdout, "NAME") || !strings.Contains(stdout, "http://127.0.0.1:") {
		t.Errorf("stdout must still carry the table; got: %q", stdout)
	}
}

// The shift warning may not assert what den cannot know — and it may no longer
// hedge about what den now DOES know.
//
// The scan interrogates the HOST: a canonical window that answers "busy" says
// its ports are BOUND, never by whom. Two wordings have been wrong here in
// turn. "The first instance of the nest keeps the canonical window" claimed a
// rival instance den had no evidence for. Its replacement hedged between that
// rival and "an earlier `den ports` run of THIS sandbox" — true at the time,
// and the very defect of #15: re-reading the table shifted every run. den now
// reads what the sandbox publishes and reuses that window, so reaching a shift
// RULES OUT this sandbox, and saying otherwise sends the user hunting a culprit
// den has already cleared.
//
// Two-sided on purpose. Forbidding a phrase alone would go green on any
// reworded falsehood; the positive half pins the sentence to the situation den
// is actually in.
func TestPortsShiftWarningRulesOutThisSandboxWithoutBlamingARival(t *testing.T) {
	denHome := portsDenHome(t, "api", "stack: devx\nports:\n  publish:\n"+
		"    - { name: vite, container: 5173 }\n")
	base := ports.Base("api")
	f := &sbx.Fake{Responses: lsWith("api.feat12")}

	stdout, stderr, err := runPorts(t, f, busyBlockScanner{base: base},
		"--den-home", denHome, "ports", "api.feat12")
	if err != nil {
		t.Fatalf("a busy window must shift, not fail: %v", err)
	}

	// The two claims den has no evidence for: a FIRST, other instance holding
	// the canonical window, and this sandbox's own earlier run — which den has
	// positively excluded by reading its publications.
	for _, unwanted := range []string{"first instance", "den ports api.feat12"} {
		if strings.Contains(stderr, unwanted) {
			t.Errorf("the warning may not name %q as the holder: den scans the host, which says a port "+
				"is bound and never by whom, and it has already ruled this sandbox out; got: %q",
				unwanted, stderr)
		}
	}
	// ... and what it must say instead: this sandbox is excluded, and here is
	// how to find out who is not.
	for _, want := range []string{
		"read what api.feat12 already publishes",
		"another sandbox of yours, or a foreign listener",
		"sbx ports SANDBOX",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the warning must state %q; got: %q", want, stderr)
		}
	}
	// The split of C8 still holds: none of this leaks into the table.
	for _, unwanted := range []string{"foreign listener", "sbx ports", "publishes"} {
		if strings.Contains(stdout, unwanted) {
			t.Errorf("stdout = %q, must carry no part of the warning (%q)", stdout, unwanted)
		}
	}
}

// #15 end to end through the command tree: a sandbox `sbx ls --json` reports as
// already publishing its canonical window re-renders the SAME table and makes
// no `sbx ports --publish` call at all.
//
// The scanner is the forbidden one: reading the host is exactly what turned the
// second run into a second window. Three declared ports became nine host
// publications in three runs before this (smoke #2 §1.7).
func TestPortsRereadsAPublishedWindowWithoutRepublishing(t *testing.T) {
	denHome := portsDenHome(t, "web", portsNestYAML)
	f := &sbx.Fake{Responses: lsWithPorts("web.feat123",
		pub(9100, 5173), pub(9101, 3000), pub(9102, 9223))}

	stdout, stderr, err := runPorts(t, f, forbiddenScanner{t: t},
		"--den-home", denHome, "ports", "web.feat123")
	if err != nil {
		t.Fatalf("re-reading a published table must succeed: %v", err)
	}
	if calls := portsCalls(f); len(calls) != 0 {
		t.Errorf("nothing was missing: den must publish nothing; calls: %v", calls)
	}
	if !strings.Contains(stdout, "window: 9100-9109 (canonical)") {
		t.Errorf("the window must be the canonical one, unmoved; got: %q", stdout)
	}
	if stderr != "" {
		t.Errorf("a window den recognises as its own is not a collision: stderr = %q", stderr)
	}
}

// The same reading, half-published — what an aborted run leaves (#22 §1.10).
// den finishes the window instead of moving to a new one.
func TestPortsPublishesOnlyWhatIsMissingFromAPublishedWindow(t *testing.T) {
	denHome := portsDenHome(t, "web", portsTwoPortNestYAML)
	f := &sbx.Fake{Responses: lsWithPorts("web.feat123", pub(9100, 5173))}

	stdout, _, err := runPorts(t, f, forbiddenScanner{t: t},
		"--den-home", denHome, "ports", "web.feat123")
	if err != nil {
		t.Fatalf("finishing a half-published window must succeed: %v", err)
	}
	calls := portsCalls(f)
	if len(calls) != 1 || !slices.Equal(calls[0],
		[]string{"ports", "web.feat123", "--publish", "127.0.0.1:9101:3000"}) {
		t.Errorf("only the missing port must be published; calls: %v", calls)
	}
	if !strings.Contains(stdout, "window: 9100-9109 (canonical)") {
		t.Errorf("the window must be the one already half-bound; got: %q", stdout)
	}
}

// #22 end to end: the identical `--add` re-run. It answered `409 Conflict: …
// already published` before this — and did so AFTER binding one more declared
// window, so the command both failed and left a mess.
func TestPortsAddIsRerunnableWhenTheSandboxAlreadyPublishesIt(t *testing.T) {
	denHome := portsDenHome(t, "web", portsTwoPortNestYAML)
	f := &sbx.Fake{Responses: lsWithPorts("web.feat123",
		pub(9100, 5173), pub(9101, 3000), pub(9801, 8081))}

	stdout, _, err := runPorts(t, f, forbiddenScanner{t: t},
		"--den-home", denHome, "ports", "web.feat123", "--add", "9801:8081")
	if err != nil {
		t.Fatalf("an identical re-run must succeed, not 409: %v", err)
	}
	if calls := portsCalls(f); len(calls) != 0 {
		t.Errorf("everything is already published: den must publish nothing; calls: %v", calls)
	}
	if !strings.Contains(stdout, "http://127.0.0.1:9801") {
		t.Errorf("the added pair is published and must still be listed; got: %q", stdout)
	}
}

// #16: a STOPPED sandbox is started, and its publications are read AFTER.
//
// `sbx ports --publish` needs a container endpoint and does not restart a
// sandbox to get one — it answers `500 Internal Server Error: … no container
// endpoint with IP address found`, naming neither the state nor a remedy. And
// the reading #15's fix depends on is unavailable until then: `sbx ls --json`
// omits the `ports` array entirely while a sandbox is stopped, while every
// publication comes back on resume (measured 2026-07-31).
//
// Both halves are asserted, and the SECOND listing is the load-bearing one: a
// wake that reused the first reading would resolve a fresh window over ports
// already bound — publishing them twice, which is the defect #15 just closed.
func TestPortsStartsAStoppedSandboxAndRereadsIt(t *testing.T) {
	denHome := portsDenHome(t, "web", portsTwoPortNestYAML)
	stopped := `{"sandboxes":[{"name":"web.feat123","status":"stopped","workspaces":["/w"]}]}`
	f := &wakingRunner{
		Fake:  &sbx.Fake{Responses: map[string]sbx.Response{"ls --json": {Output: []byte(stopped)}}},
		woken: lsWithPorts("web.feat123", pub(9100, 5173), pub(9101, 3000))["ls --json"].Output,
	}

	stdout, stderr, err := runPorts(t, f, forbiddenScanner{t: t},
		"--den-home", denHome, "ports", "web.feat123")
	if err != nil {
		t.Fatalf("a stopped sandbox must be started, not relayed as a 500: %v", err)
	}
	if !f.HasCalled("exec", "web.feat123", "true") {
		t.Errorf("the sandbox must be started before anything is published; calls: %v", f.Calls)
	}
	if f.lsCount != 2 {
		t.Errorf("ls --json was read %d time(s): the reading after the wake is the whole point — a "+
			"stopped sandbox hides what it publishes", f.lsCount)
	}
	if calls := portsCalls(f.Fake); len(calls) != 0 {
		t.Errorf("the woken sandbox already publishes both ports: den must publish nothing; "+
			"calls: %v", calls)
	}
	if !strings.Contains(stderr, "is stopped: den is starting it") {
		t.Errorf("starting a VM takes seconds: it must be announced on stderr; got %q", stderr)
	}
	if strings.Contains(stdout, "stopped") {
		t.Errorf("stdout carries the table and nothing else; got %q", stdout)
	}
}

// The wake is fail-closed: a sandbox that is still not running afterwards ends
// the command rather than resolving a window against publications sbx is still
// hiding — which would bind, a second time, ports that are already bound.
func TestPortsRefusesASandboxThatDoesNotStart(t *testing.T) {
	denHome := portsDenHome(t, "web", portsTwoPortNestYAML)
	f := &sbx.Fake{Responses: map[string]sbx.Response{"ls --json": {Output: []byte(
		`{"sandboxes":[{"name":"web.feat123","status":"stopped","workspaces":["/w"]}]}`)}}}

	_, _, err := runPorts(t, f, freeScanner{}, "--den-home", denHome, "ports", "web.feat123")
	if err == nil {
		t.Fatal("a sandbox still stopped after den started it must not be published into")
	}
	if !strings.Contains(err.Error(), "stopped") || !strings.Contains(err.Error(), "running") {
		t.Errorf("the refusal must render the status read and the one expected; got: %v", err)
	}
	if calls := portsCalls(f); len(calls) != 0 {
		t.Errorf("nothing may be published; calls: %v", calls)
	}
}

// C11: `--add H:C` publishes ONE extra pair on top of the declared window —
// its own `sbx ports … --publish 127.0.0.1:H:C` call, LAST, and its own row.
//
// The host port is the one the user WROTE: 8080, not the next free slot of the
// window. An added pair takes no offset and is not scanned — the window numbers
// the declared ports, and renumbering an explicit request would publish an
// address nobody asked for.
func TestPortsAddPublishesAnExtraPairLast(t *testing.T) {
	denHome := portsDenHome(t, "web", portsTwoPortNestYAML)
	f := &sbx.Fake{Responses: lsWith("web.feat123")}

	stdout, _, err := runPorts(t, f, freeScanner{},
		"--den-home", denHome, "ports", "web.feat123", "--add", "8080:3000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := assertPublishGolden(t, f, "ports-publish-add.golden")

	// Two declared ports plus the added pair: three publications, one per pair.
	if len(calls) != 3 {
		t.Fatalf("2 declared ports + 1 added = 3 publications; got %d: %v", len(calls), calls)
	}
	// The added pair goes LAST, after everything the nest declared: it is an
	// addition to the window, never a substitution inside it.
	want := []string{"ports", "web.feat123", "--publish", "127.0.0.1:8080:3000"}
	if !slices.Equal(calls[2], want) {
		t.Errorf("last publication = %v, want %v", calls[2], want)
	}
	// ... and it is in the table, on the address it was published at.
	if !strings.Contains(stdout, "http://127.0.0.1:8080") {
		t.Errorf("the added pair must have its own row; got:\n%s", stdout)
	}
	// The declared ports are untouched by the addition.
	for _, url := range []string{"http://127.0.0.1:9100", "http://127.0.0.1:9101"} {
		if !strings.Contains(stdout, url) {
			t.Errorf("the declared ports keep their window: %q missing from:\n%s", url, stdout)
		}
	}
}

// C11 again, through the door the flag's own help advertises: `--add` is
// `(repeatable)`, and newPortsCmd says repeating it "is how a second pair is
// asked for" — so TWO values publish TWO extra pairs, in the order they were
// written, after everything the nest declared.
//
// One value is not enough to pin this. A parseAdds that OVERWRITES its
// accumulator instead of appending passes every single-`--add` case in this file
// and still drops all but the last pair here: `--add 8080:3000 --add 9090:4000`
// would publish 9090 alone — no error, no row, no `sbx ports … --publish` call
// for 8080 — and the run would look like it succeeded while half of what the
// user asked for was silently gone.
func TestPortsPublishesEveryRepeatedAdd(t *testing.T) {
	denHome := portsDenHome(t, "web", portsTwoPortNestYAML)
	f := &sbx.Fake{Responses: lsWith("web.feat123")}

	stdout, _, err := runPorts(t, f, freeScanner{},
		"--den-home", denHome, "ports", "web.feat123",
		"--add", "8080:3000", "--add", "9090:4000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := assertPublishGolden(t, f, "ports-publish-two-adds.golden")

	// Two declared ports plus BOTH added pairs: four publications, one per pair.
	if len(calls) != 4 {
		t.Fatalf("2 declared ports + 2 added = 4 publications; got %d: %v", len(calls), calls)
	}
	// Command-line order, and both after the declared window: the added pairs
	// are an addition to it, never a substitution inside it.
	for i, want := range [][]string{
		{"ports", "web.feat123", "--publish", "127.0.0.1:8080:3000"},
		{"ports", "web.feat123", "--publish", "127.0.0.1:9090:4000"},
	} {
		if !slices.Equal(calls[2+i], want) {
			t.Errorf("publication %d = %v, want %v", 3+i, calls[2+i], want)
		}
	}
	// ... and each is in the table, on the address it was published at.
	for _, url := range []string{"http://127.0.0.1:8080", "http://127.0.0.1:9090"} {
		if !strings.Contains(stdout, url) {
			t.Errorf("every added pair must have its own row; %q missing from:\n%s", url, stdout)
		}
	}
	// The NAME column distinguishes them. A bare "added" on both rows was what
	// issue #21 measured, and the row that needs the name most is the one on a
	// nest with no `ports:` at all, where the added pairs are the WHOLE table.
	// The container port names the pair because it is a property of the pair;
	// the host port is what the window may move.
	for _, name := range []string{"added:3000", "added:4000"} {
		if !strings.Contains(stdout, name) {
			t.Errorf("the NAME column must distinguish the added pairs; %q missing from:\n%s", name, stdout)
		}
	}
}

// The duplicate-host refusal of ports.Resolve, reached the ONLY way a user can
// reach it: two `--add` values naming the same host port. It is unreachable from
// this command while `--add` cannot carry two pairs, and unreachable means
// untested — the guard would sit there while den published `8080:4000` and
// dropped `8080:3000`, which is the half-bound outcome it was written against.
//
// Nothing is published: the refusal is knowable from the flags alone, so it
// lands before `sbx ls`, like every other `--add` refusal here.
func TestPortsRefusesTwoAddsOnTheSameHostPort(t *testing.T) {
	denHome := portsDenHome(t, "web", portsTwoPortNestYAML)
	f := &sbx.Fake{Responses: lsWith("web.feat123")}

	stdout, _, err := runPorts(t, f, freeScanner{},
		"--den-home", denHome, "ports", "web.feat123",
		"--add", "8080:3000", "--add", "8080:4000")
	if err == nil {
		t.Fatal("`--add 8080:3000 --add 8080:4000` both ask for host port 8080: den must refuse, " +
			"not publish one of the two and drop the other")
	}
	// Both pairs are named, not just the port: the user wrote two tokens, and
	// the message has to say which two collided.
	for _, fragment := range []string{"8080", "3000", "4000"} {
		if !strings.Contains(err.Error(), fragment) {
			t.Errorf("the refusal must name %q; got: %v", fragment, err)
		}
	}
	if f.HasCalled("ports") {
		t.Errorf("nothing is published until everything asked for is admissible; calls: %v", f.Calls)
	}
	if strings.Contains(stdout, "http://") {
		t.Errorf("no table may be printed for a refused run; got:\n%s", stdout)
	}
}

// C12: a host address other than 127.0.0.1 is REFUSED — and refused FIRST, as
// step (1) of the command, before `sbx ls` and therefore before any publication.
//
// "Nothing is published" is the whole point: a refusal discovered halfway would
// leave the declared ports bound and the user's actual request rejected, which
// is the worst of the two outcomes. The assertion on `f.Calls` being EMPTY —
// not merely free of `ports` calls — is what pins the ordering: `ls --json`
// travels through the same Fake.
func TestPortsRefusesANonLoopbackAdd(t *testing.T) {
	denHome := portsDenHome(t, "web", portsTwoPortNestYAML)
	f := &sbx.Fake{Responses: lsWith("web.feat123")}

	stdout, _, err := runPorts(t, f, freeScanner{},
		"--den-home", denHome, "ports", "web.feat123", "--add", "0.0.0.0:8080:3000")
	if err == nil {
		t.Fatal("a LAN bind must be refused: the microVM is the boundary, and 0.0.0.0 pierces it host-side")
	}
	for _, fragment := range []string{"0.0.0.0", ports.Loopback} {
		if !strings.Contains(err.Error(), fragment) {
			t.Errorf("the refusal must name %q; got: %v", fragment, err)
		}
	}
	if f.HasCalled("ports") {
		t.Errorf("a refused --add publishes nothing, not even the declared ports; calls: %v", f.Calls)
	}
	if len(f.Calls) != 0 {
		t.Errorf("--add is parsed before sbx is asked anything at all; calls: %v", f.Calls)
	}
	if strings.Contains(stdout, "http://") {
		t.Errorf("no table may be printed for a refused run; got:\n%s", stdout)
	}
}

// The same door again, for a collision den would otherwise CAUSE ITSELF: an
// `--add` on a host port the window already gave to a declared port.
//
// Without the refusal, `--add 9101:4000` on this nest publishes 9100, then
// 9101, then fails binding 9101 a second time — the declared ports stay bound,
// the added pair does not, and no table is printed. `f.Calls` holding no
// `ports` call is what pins the promise the command's godoc makes: nothing is
// published until everything asked for is admissible.
func TestPortsRefusesAnAddOnADeclaredHostPort(t *testing.T) {
	denHome := portsDenHome(t, "web", portsTwoPortNestYAML)
	f := &sbx.Fake{Responses: lsWith("web.feat123")}

	stdout, _, err := runPorts(t, f, freeScanner{},
		"--den-home", denHome, "ports", "web.feat123", "--add", "9101:4000")
	if err == nil {
		t.Fatal("`--add 9101:4000` collides with the window's own `api` on 9101: den must refuse, " +
			"not publish half the window and fail on itself")
	}
	for _, fragment := range []string{"9101", "api"} {
		if !strings.Contains(err.Error(), fragment) {
			t.Errorf("the refusal must name %q; got: %v", fragment, err)
		}
	}
	if f.HasCalled("ports") {
		t.Errorf("a collision refused after publication is the failure itself: nothing may be published; "+
			"calls: %v", f.Calls)
	}
	if strings.Contains(stdout, "http://") {
		t.Errorf("no table may be printed for a refused run; got:\n%s", stdout)
	}
}

// C12, the other half of the same door: a malformed `--add` is refused on the
// same terms — before sbx, with the value quoted back so the user sees what den
// read.
func TestPortsRefusesAMalformedAdd(t *testing.T) {
	denHome := portsDenHome(t, "web", portsTwoPortNestYAML)
	f := &sbx.Fake{Responses: lsWith("web.feat123")}

	_, _, err := runPorts(t, f, freeScanner{},
		"--den-home", denHome, "ports", "web.feat123", "--add", "8080")
	if err == nil {
		t.Fatal("`--add 8080` is not a HOST_PORT:CONTAINER_PORT pair and must be refused")
	}
	if !strings.Contains(err.Error(), `"8080"`) {
		t.Errorf("the refused value must be quoted back; got: %v", err)
	}
	if len(f.Calls) != 0 {
		t.Errorf("a malformed --add reaches sbx for nothing; calls: %v", f.Calls)
	}
}

// C13: an unknown sandbox speaks the vocabulary of `den rm` and `den sh` —
// same sentence, same live list. A third dialect for the same situation is how
// users stop recognizing den's own messages.
func TestPortsUnknownSandbox(t *testing.T) {
	denHome := portsDenHome(t, "api", portsNestYAML)
	f := &sbx.Fake{Responses: lsWith("api", "web")}

	_, _, err := runPorts(t, f, freeScanner{}, "--den-home", denHome, "ports", "missing")
	if err == nil {
		t.Fatal("an unknown sandbox name must produce an error")
	}
	if !strings.Contains(err.Error(), `sandbox "missing" not found (live: [`) {
		t.Errorf("the message must follow `den rm`'s pattern; got: %v", err)
	}
	for _, name := range []string{"api", "web"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the message must list the live sandbox %q; got: %v", name, err)
		}
	}
	if f.HasCalled("ports") {
		t.Errorf("nothing may be published for a sandbox that does not exist; calls: %v", f.Calls)
	}
}

// C14: the guard is sbx.Sandbox.CheckAttachable, the SHARED one — `den ports`
// publishes into a VM, and a VM den knows nothing about is no more publishable
// than it is attachable.
func TestPortsRefusesASandboxThatIsNotAttachable(t *testing.T) {
	denHome := portsDenHome(t, "api", portsNestYAML)
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"paused","workspaces":["/w/api"]}]}`)},
	}}

	_, _, err := runPorts(t, f, freeScanner{}, "--den-home", denHome, "ports", "api")
	if err == nil {
		t.Fatal("a paused sandbox must not be published into")
	}
	// The exact message of the shared guard, not a paraphrase.
	want := (&sbx.Sandbox{Name: "api", Status: "paused"}).CheckAttachable()
	if err.Error() != want.Error() {
		t.Errorf("the refusal must be CheckAttachable's own: got %q, want %q", err, want)
	}
	for _, fragment := range []string{"status read", "den rm api"} {
		if !strings.Contains(err.Error(), fragment) {
			t.Errorf("the message must contain %q; got: %v", fragment, err)
		}
	}
	if f.HasCalled("ports") {
		t.Errorf("nothing may be published into a paused VM; calls: %v", f.Calls)
	}
}

// C15: the nest file is named in FULL. "nest api not found" would leave the
// user guessing which den home den was looking in — and --den-home makes that
// question a real one.
func TestPortsNamesTheMissingNestFile(t *testing.T) {
	denHome := t.TempDir() // a live sandbox, but no nest was ever written
	f := &sbx.Fake{Responses: lsWith("api.feat12")}

	_, _, err := runPorts(t, f, freeScanner{}, "--den-home", denHome, "ports", "api.feat12")
	if err == nil {
		t.Fatal("a sandbox whose nest is gone must not resolve a window")
	}
	if !strings.Contains(err.Error(), filepath.Join(denHome, "nests", "api.yaml")) {
		t.Errorf("the error must name %s; got: %v", filepath.Join(denHome, "nests", "api.yaml"), err)
	}
	if f.HasCalled("ports") {
		t.Errorf("nothing may be published without a nest; calls: %v", f.Calls)
	}
}

// C16: a nest declaring no port publishes nothing AND probes nothing. The
// scanner is what proves the second half: it fails the test if it is asked
// anything at all.
func TestPortsWithNoDeclaredPortNeitherPublishesNorScans(t *testing.T) {
	denHome := portsDenHome(t, "api", "stack: devx\n")
	f := &sbx.Fake{Responses: lsWith("api")}

	stdout, _, err := runPorts(t, f, forbiddenScanner{t: t}, "--den-home", denHome, "ports", "api")
	if err != nil {
		t.Fatalf("a nest without ports is not an error: %v", err)
	}
	if f.HasCalled("ports") {
		t.Errorf("a nest declaring no port publishes nothing; calls: %v", f.Calls)
	}
	// Pinned rather than left accidental: the command still renders its header
	// and its (empty) table, so `den ports` never answers with silence.
	if !strings.Contains(stdout, "nest: api") || !strings.Contains(stdout, "NAME") {
		t.Errorf("the table must still be rendered; got: %q", stdout)
	}
	if strings.Contains(stdout, "http://") {
		t.Errorf("no port is declared, so no URL may be printed; got: %q", stdout)
	}
	// No window was placed on the host, so the header may not claim one and
	// the tunnel line may not point into it.
	for _, s := range []string{"window:", "ssh -L"} {
		if strings.Contains(stdout, s) {
			t.Errorf("a portless nest placed no window, so %q may not appear; got: %q", s, stdout)
		}
	}
}

// C10: `open: true` opens ONE browser, on the URL the table just printed, and
// leaves every other port alone.
//
// The assertion is an EQUALITY on the whole recording, not a containment: it is
// the only shape that states the three halves of the promise at once — the
// right URL, exactly one call, and nothing for `api` or `cdp`. A containment
// check would go green on an implementation opening all three ports, and a
// count alone would go green on one opening the wrong one.
//
// The opener is a func literal recording into a slice: no exec, no browser, no
// dependency on the machine running the suite.
func TestPortsOpensOnlyTheDeclaredOpenPort(t *testing.T) {
	denHome := portsDenHome(t, "web", portsNestYAML)
	f := &sbx.Fake{Responses: lsWith("web.feat123")}
	var opened []string

	stdout, _, err := runPortsWithOpener(t, f, freeScanner{},
		func(url string) error { opened = append(opened, url); return nil },
		"--den-home", denHome, "ports", "web.feat123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// vite is the only `open: true` port of portsNestYAML, and the first
	// declared: base 9100 + offset 0 — the very URL its row carries.
	want := []string{"http://127.0.0.1:9100"}
	if !slices.Equal(opened, want) {
		t.Errorf("opened = %v, want exactly %v", opened, want)
	}
	// The table is still the command's output: opening is a side effect ON TOP
	// of the render, never a replacement for it.
	if !strings.Contains(stdout, "http://127.0.0.1:9100") {
		t.Errorf("the table must still carry the opened URL; got:\n%s", stdout)
	}
}

// C10, the wiring half: the shipped binary really opens. Everything above runs
// on a double, so without this the whole feature could be a no-op in production
// with the suite green.
//
// The field is checked for non-nilness ONLY — calling it would launch a browser
// on the machine running the suite, which is exactly what ports.OpenURL's
// "untested by construction" godoc says nothing here may do.
func TestSystemDepsCarriesABrowserOpener(t *testing.T) {
	if SystemDeps().Open == nil {
		t.Error("SystemDeps().Open is nil: the shipped binary would never open an `open: true` port")
	}
}

// C10, the third half: a nil opener is a NO-OP, not a crash.
//
// Every wiring test in this package builds Deps by hand and leaves Open unset
// (so does runPorts), so a command dereferencing it unguarded would panic on a
// nest declaring `open: true` — and the panic would be in the code path a user
// hits, not in an exotic one.
func TestPortsWithoutAnOpenerStillRendersTheTable(t *testing.T) {
	denHome := portsDenHome(t, "web", portsNestYAML)
	f := &sbx.Fake{Responses: lsWith("web.feat123")}

	stdout, _, err := runPorts(t, f, freeScanner{},
		"--den-home", denHome, "ports", "web.feat123")
	if err != nil {
		t.Fatalf("a nil opener must be a no-op, not an error: %v", err)
	}
	if row := rowFor(stdout, "vite"); !strings.Contains(row, "http://127.0.0.1:9100") {
		t.Errorf("the table must be rendered in full without an opener; row = %q", row)
	}
}

// C10: an opener that FAILS does not fail the command. The table is already on
// stdout and the ports are already published — turning "the browser did not
// come up" into a non-zero exit would tell a script the publication failed,
// which it did not. The user hears about it on stderr at most.
func TestPortsSurvivesAnOpenerFailure(t *testing.T) {
	denHome := portsDenHome(t, "web", portsNestYAML)
	f := &sbx.Fake{Responses: lsWith("web.feat123")}
	boom := errors.New("no browser on this machine")

	stdout, _, err := runPortsWithOpener(t, f, freeScanner{},
		func(string) error { return boom },
		"--den-home", denHome, "ports", "web.feat123")
	if err != nil {
		t.Fatalf("a browser that would not start must not fail `den ports`: %v", err)
	}
	if row := rowFor(stdout, "vite"); !strings.Contains(row, "http://127.0.0.1:9100") {
		t.Errorf("the table stands on its own; row = %q", row)
	}
}

// The scanner's wiring half, twin of TestSystemDepsCarriesABrowserOpener and
// added for the very reason that one gives: everything above runs on a double,
// so without this the whole feature could be broken in production with the
// suite green.
//
// It is the sharper of the two, because the doubles are MANDATORY here:
// runPorts exists precisely so no test inherits ports.ListenScanner (it binds
// host sockets across 9000-17990), which means no other test in this suite ever
// exercises SystemDeps().Scanner. Measured: deleting `Scanner: ports.ListenScanner{}`
// from SystemDeps left `make test` green in all 12 packages, while the shipped
// binary would have failed the mainline path — ports.Resolve's `if s == nil`
// guard refuses EVERY nest declaring a port with "no port scanner — den cannot
// tell whether the window is free".
//
// The field is checked for non-nilness ONLY, exactly as the opener's is: calling
// Free would bind a real host port, which ListenScanner's own "untested by
// construction" godoc says nothing here may do.
func TestSystemDepsCarriesAPortScanner(t *testing.T) {
	if SystemDeps().Scanner == nil {
		t.Error("SystemDeps().Scanner is nil: `den ports` would refuse every nest with a declared port")
	}
}

// portsSpecRows returns the §5 command-table rows that name `den ports`, in
// file order.
//
// The spec is read from a fixed relative path, as internal/config's
// TestExampleDenHomeIsValid reads examples/den-home: `go test` runs this
// package from its own directory, and internal/cli is exactly two levels under
// the module root.
//
// The scan is SCOPED to §5 — from its heading to the next one — because §5 is
// the section the contract is about. §6 and §8 mention `den ports` in prose
// («les ports ne sont PAS publiés au spawn → …», «publication à la demande :
// …»), and neither a table row hiding in another section nor a prose sentence
// here may stand in for the command table's own row.
func portsSpecRows(t *testing.T) []string {
	t.Helper()
	path := filepath.Join("..", "..", "docs", "superpowers", "specs",
		"2026-07-27-den-cli-design.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the spec at %s: %v", path, err)
	}
	var rows []string
	var seen bool
	inSection := false
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "## ") {
			inSection = strings.HasPrefix(line, "## 5.")
			seen = seen || inSection
			continue
		}
		if inSection && strings.HasPrefix(line, "| `den ports") {
			rows = append(rows, line)
		}
	}
	if !seen {
		t.Fatalf("no `## 5.` heading in %s: the command table this test reads has moved", path)
	}
	return rows
}

// C17: the §5 command table and the implemented signature are ONE contract.
//
// `den ports` takes a SANDBOX name — newPortsCmd's godoc says why, and
// TestPortsTakesExactlyOneSandboxName holds the runtime half — so the row that
// announces it must read `den ports <name> …`. The older `den ports <nest>`
// wording described an argument this command never accepted: a user following
// the table would type a nest name and be told the sandbox was not found.
//
// The expected row is DERIVED from cmd.Use, not retyped: renaming the argument
// in code turns this red until the spec follows, which is the only version of
// this test worth having. The command is built with nil dependencies on
// purpose — only Use and the flag set are read here, RunE never runs.
func TestSpecCommandTableNamesThePortsArgumentAsImplemented(t *testing.T) {
	cmd := newPortsCmd(new(string), nil, nil, nil)
	// The one literal below that code cannot supply is `[--add H:C]`; this is
	// what keeps it honest rather than decorative.
	if cmd.Flags().Lookup("add") == nil {
		t.Fatal("`den ports` no longer carries --add: the row's `[--add H:C]` is stale")
	}
	want := "| `den " + cmd.Use + " [--add H:C]` |"

	rows := portsSpecRows(t)
	if len(rows) != 1 {
		t.Fatalf("§5 must name `den ports` in exactly one table row, found %d: %q", len(rows), rows)
	}
	if !strings.HasPrefix(rows[0], want) {
		t.Errorf("the §5 row for `den ports` must open with %q, got %q", want, rows[0])
	}
}

// C17's sibling for the HANDOFF: the spec's command table is not the only
// document announcing the signature — HANDOFF.md carries it twice (§8's
// decision and its own command list), and a reader following EITHER would type
// a nest name and be told the sandbox was not found. Same contract, so the
// expected wording is derived from cmd.Use the same way.
func TestHandoffNamesThePortsArgumentAsImplemented(t *testing.T) {
	cmd := newPortsCmd(new(string), nil, nil, nil)
	// No closing backtick: HANDOFF writes the signature both bare and with
	// its `[--add H:C]` tail, and the contract here is the ARGUMENT's name.
	want := "`den " + cmd.Use

	path := filepath.Join("..", "..", "docs", "superpowers", "handoffs", "HANDOFF.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the handoff at %s: %v", path, err)
	}
	for i, line := range strings.Split(string(b), "\n") {
		if !strings.Contains(line, "`den ports <") {
			continue
		}
		if !strings.Contains(line, want) {
			t.Errorf("%s:%d names `den ports` with an argument that is not %s: %q",
				path, i+1, want, line)
		}
	}
}

// The `--add` host port is NEVER scanned (Options.Extra's contract: re-running
// the command must re-read a table whose added pair this sandbox already
// publishes), so sbx can refuse it host-side AFTER the declared window is
// bound. That failure must behave like every failed publication (C3's failure
// half): the runner's own error, publication stopped there, and NO table — the
// declared ports stay published, and newPortsCmd's own comment says so.
func TestPortsAddRefusedBySbxAbortsAfterTheDeclaredWindow(t *testing.T) {
	denHome := portsDenHome(t, "web", portsTwoPortNestYAML)
	boom := errors.New("sbx: address already in use")
	responses := lsWith("web.feat123")
	// The declared window binds fine; the ADDED pair is the one already taken
	// on the host — the case the scan cannot see, since 8080 is outside the
	// window and Extra pairs take no probe.
	responses["ports web.feat123 --publish 127.0.0.1:8080:3000"] = sbx.Response{Err: boom}
	f := &sbx.Fake{Responses: responses}

	stdout, _, err := runPorts(t, f, freeScanner{},
		"--den-home", denHome, "ports", "web.feat123", "--add", "8080:3000")
	if err == nil {
		t.Fatal("a refused publication must fail the command")
	}
	if !errors.Is(err, boom) {
		t.Errorf("the runner's own error must reach the user; got: %v", err)
	}
	// Both declared ports were published BEFORE the added pair failed: the
	// abort does not unwind them, and pinning the count is what keeps this
	// partial-publish outcome documented rather than accidental.
	if n := len(portsCalls(f)); n != 3 {
		t.Errorf("2 declared publications + the failed add = 3 calls, got %d: %v",
			n, portsCalls(f))
	}
	if strings.Contains(stdout, "NAME") || strings.Contains(stdout, "http://") {
		t.Errorf("no table may be printed for a window that was not fully published; got:\n%s", stdout)
	}
}

// A PORTLESS nest with `--add` publishes the added pair and ONLY describes the
// added pair: no window was placed on the host (ports.Resolve scanned nothing,
// bound nothing in the range), so a header claiming `window: <base>-<last>
// (canonical)` and a tunnel line on that base would point at ports nothing
// listens on. The header stops at the nest and the sandbox; the added row and
// its URL are the whole output — and the whole point of the command here.
func TestPortsPortlessNestWithAddRendersNoWindow(t *testing.T) {
	denHome := portsDenHome(t, "api", "stack: devx\n")
	f := &sbx.Fake{Responses: lsWith("api")}

	stdout, _, err := runPorts(t, f, forbiddenScanner{t: t},
		"--den-home", denHome, "ports", "api", "--add", "8080:3000")
	if err != nil {
		t.Fatalf("a portless nest with an added pair is not an error: %v", err)
	}

	// The added pair really is published — its own call, like any other.
	calls := portsCalls(f)
	want := []string{"ports", "api", "--publish", "127.0.0.1:8080:3000"}
	if len(calls) != 1 || !slices.Equal(calls[0], want) {
		t.Fatalf("publications = %v, want exactly [%v]", calls, want)
	}
	// ... and its row is in the table, under the plain header.
	for _, s := range []string{"nest: api", "sandbox: api", "http://127.0.0.1:8080"} {
		if !strings.Contains(stdout, s) {
			t.Errorf("output must contain %q; got:\n%s", s, stdout)
		}
	}
	// No phantom range, no tunnel to a port nothing was bound on.
	for _, s := range []string{"window:", "(canonical)", "ssh -L"} {
		if strings.Contains(stdout, s) {
			t.Errorf("a portless nest placed no window, so %q may not appear; got:\n%s", s, stdout)
		}
	}
}

// A source reference names a sandbox that was never spawned under its
// prefixed name — ":" is not in sbx's charset, so the live VM is
// "corp-api", the FLATTENED reference, exactly as spawn named it. `den ports
// corp:api` must reach THAT sandbox, and must load the nest's declarations
// from the source's own root (sources/corp/nests/api.yaml), not from
// ~/.den/nests, which holds no such file.
func TestPortsAcceptsASourceReference(t *testing.T) {
	denHome := t.TempDir()
	writeUnder(t, denHome, filepath.Join("sources", "corp", "nests", "api.yaml"),
		"stack: devx\nports:\n  base: 9100\n  publish:\n"+
			"    - { name: vite, container: 5173 }\n    - { name: api, container: 3000 }\n")

	f := &sbx.Fake{Responses: lsWith("corp-api")}
	stdout, _, err := runPorts(t, f, freeScanner{}, "--den-home", denHome, "ports", "corp:api")
	if err != nil {
		t.Fatalf("den ports corp:api: %v", err)
	}

	// The sandbox den talked to is the flattened name — the argv sbx.Fake
	// actually received.
	if !f.HasCalled("ports", "corp-api", "--publish", "127.0.0.1:9100:5173") {
		t.Errorf("expected a publish call against sandbox corp-api; calls: %v", f.Calls)
	}
	if !strings.Contains(stdout, "sandbox: corp-api") {
		t.Errorf("the header must name the flattened sandbox; got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "vite") {
		t.Errorf("the nest's declared ports must have been read from the source; got:\n%s", stdout)
	}
}

// A source reference naming an installed source but a nest that does not
// exist there is refused with the ordinary "nest not found" diagnostic — not
// a colon-shaped sandbox name that sbx would reject.
func TestPortsSourceReferenceUnknownNest(t *testing.T) {
	denHome := t.TempDir()
	writeUnder(t, denHome, filepath.Join("sources", "corp", "nests", "api.yaml"), "stack: devx\n")

	f := &sbx.Fake{Responses: lsWith("corp-web")}
	_, _, err := runPorts(t, f, freeScanner{}, "--den-home", denHome, "ports", "corp:web")
	if err == nil {
		t.Fatal("a nest absent from the source must be refused")
	}
	// Not just "an error" — this is the SOURCE's own nest-not-found file, not
	// a "sandbox not found" refusal or an unrelated one: the sandbox
	// "corp-web" is live (lsWith above), so a wrong assertion here would pass
	// against the pre-Task-10 code path just as well, on a completely
	// different error.
	want := filepath.Join(denHome, "sources", "corp", "nests", "web.yaml")
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, expected it to name %s", err.Error(), want)
	}
}
