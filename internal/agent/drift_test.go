package agent

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// plantGolden materializes the mixin GOLDEN where ReadMixin will look for it.
//
// The golden, not a fresh RenderMixin output, on purpose: a RenderMixin →
// ReadMixin round trip would stay green even if BOTH sides got the same key
// path wrong ("cap" for "caps", "env" for "environment" — exactly the bug the
// spec carried before task 1). The golden is hand-written and never
// regenerated: it's the only reference independent of the decoder under test.
func plantGolden(t *testing.T, sandboxName string) string {
	t.Helper()
	denHome := t.TempDir()
	dir := filepath.Join(denHome, "cache", "mixins", sandboxName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile(filepath.Join("testdata", "mixin-complete.golden"))
	if err != nil {
		t.Fatalf("reading golden: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "spec.yaml"), golden, 0o600); err != nil {
		t.Fatal(err)
	}
	return denHome
}

func TestReadMixinDecodesTheGolden(t *testing.T) {
	denHome := plantGolden(t, "api.feat12")

	m, err := ReadMixin(denHome, "api.feat12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.SandboxName != "api.feat12" {
		t.Errorf("SandboxName = %q, want %q", m.SandboxName, "api.feat12")
	}
	if want := []string{"api.anthropic.com", "github.com"}; !slices.Equal(m.Egress, want) {
		t.Errorf("Egress = %v, want %v", m.Egress, want)
	}
	wantEnv := map[string]string{
		"CLAUDE_CONFIG_DIR": "/home/me/.den/agents/claude",
		"SOME_VAR":          "value",
	}
	if len(m.Env) != len(wantEnv) {
		t.Errorf("Env = %v, want %v", m.Env, wantEnv)
	}
	for k, v := range wantEnv {
		if m.Env[k] != v {
			t.Errorf("Env[%q] = %q, want %q", k, m.Env[k], v)
		}
	}
	// Freshness is an argv, whose last element is the bash script.
	if len(m.Freshness) != 3 || m.Freshness[0] != "bash" || m.Freshness[1] != "-c" {
		t.Fatalf("Freshness = %v, want [bash -c <script>]", m.Freshness)
	}
	if !strings.Contains(m.Freshness[2], "claude update") {
		t.Errorf("the freshness script must carry the update command; got:\n%s", m.Freshness[2])
	}
}

// A mixin written before 2026-08-10 carries `caps:` / `commands:` — the
// spelling sbx accepted up to v0.35. It is still the drift reference of every
// sandbox created back then and still running, and cache/mixins/ is never
// purged by den. Reading only the v0.38 spelling would decode those files to
// empty sections and report an emptied egress plus a vanished freshness
// command on EVERY attach: a permanent false warning, which is how a user
// learns to stop reading the drift report.
//
// The golden is frozen (copied from the pre-rename mixin-with-mounts.golden)
// and must never be regenerated: it describes what is already on disk on real
// machines, which no later den version can change.
func TestReadMixinStillDecodesThePreV038Spelling(t *testing.T) {
	denHome := t.TempDir()
	dir := filepath.Join(denHome, "cache", "mixins", "api.feat12")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile(filepath.Join("testdata", "mixin-legacy-pre-v038.golden"))
	if err != nil {
		t.Fatalf("reading golden: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "spec.yaml"), golden, 0o600); err != nil {
		t.Fatal(err)
	}

	m, err := ReadMixin(denHome, "api.feat12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := []string{"api.anthropic.com", "github.com"}; !slices.Equal(m.Egress, want) {
		t.Errorf("Egress = %v, want %v — an old mixin must not read as an emptied egress", m.Egress, want)
	}
	// Two startup entries: the link phase, then freshness. Both must come back,
	// in that order, or Differences reports a phantom change on both axes.
	if len(m.Links) != 3 || !strings.Contains(m.Links[2], "den mounts") {
		t.Errorf("Links = %v, want the link phase argv", m.Links)
	}
	if len(m.Freshness) != 3 || !strings.Contains(m.Freshness[2], "claude update") {
		t.Errorf("Freshness = %v, want the freshness argv", m.Freshness)
	}
}

// Spawn must distinguish "no reference" (cache purged, or sandbox created
// outside this den) from a broken read: both get reported, but not with the
// same message — the user doesn't respond the same way to a purged cache and
// a corrupted spec.yaml. Without %w, the two get conflated and Spawn can only
// render one message for two different situations.
func TestReadMixinAbsentIsDistinguishableFromABrokenRead(t *testing.T) {
	_, err := ReadMixin(t.TempDir(), "api")
	if err == nil {
		t.Fatal("a missing mixin must produce an error")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the error must wrap os.ErrNotExist; got: %v", err)
	}
}

// The message names the FULL path: unverifiable drift sends the user to this
// file, and "reading mixin" alone points at nothing.
func TestReadMixinNamesThePath(t *testing.T) {
	denHome := t.TempDir()
	_, err := ReadMixin(denHome, "api")
	if err == nil {
		t.Fatal("a missing mixin must produce an error")
	}
	path := filepath.Join(denHome, "cache", "mixins", "api", "spec.yaml")
	if !strings.Contains(err.Error(), path) {
		t.Errorf("the message must name %s; got: %v", path, err)
	}
	// Exactly ONCE. This message hits the terminal on every attach whose cache
	// was purged (spawn.reportDrift, "unverifiable drift"), wrapped in an
	// already long sentence: measured on the binary, it used to write the
	// absolute path twice and end on "no such file or directory".
	if n := strings.Count(err.Error(), path); n != 1 {
		t.Errorf("the path appears %d times, want 1; message: %s", n, err.Error())
	}
	if strings.Contains(err.Error(), "no such file or directory") {
		t.Errorf("the raw OS reason must not leak: %s", err.Error())
	}
}

func TestReadMixinRefusesUnreadableYAML(t *testing.T) {
	denHome := t.TempDir()
	dir := filepath.Join(denHome, "cache", "mixins", "api")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "spec.yaml"), []byte("\tnot yaml"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := ReadMixin(denHome, "api")
	if err == nil {
		t.Fatal("an unreadable spec.yaml must produce an error")
	}
	// And especially NOT os.ErrNotExist: Spawn would stay silent on a
	// corrupted mixin.
	if errors.Is(err, os.ErrNotExist) {
		t.Errorf("broken YAML must not be confused with a missing file; got: %v", err)
	}
}

// ReadMixin must reread exactly what WriteMixin just wrote: the two share the
// path, and a divergence would make drift UNDETECTABLE — ReadMixin would
// forever return os.ErrNotExist, and den would never again report anything
// but "unverifiable", for every sandbox, without anything actually failing.
// The golden above proves the keys, this one proves the path.
func TestReadMixinRereadsWhatWriteMixinWrote(t *testing.T) {
	denHome := t.TempDir()
	m := exampleMixin(t)
	if _, err := WriteMixin(denHome, m.SandboxName, m); err != nil {
		t.Fatalf("WriteMixin: %v", err)
	}

	read, err := ReadMixin(denHome, m.SandboxName)
	if err != nil {
		t.Fatalf("ReadMixin after WriteMixin: %v", err)
	}
	if d := Differences(m, read); len(d) != 0 {
		t.Errorf("a round trip must produce no difference; got: %v", d)
	}
}

// A round trip must hold up for values that YAML resolves to something other
// than a string — otherwise Differences reports a change on EVERY attach
// although nothing moved.
//
// The measured case: `Env{"TILDE":"~"}` was written as unquoted `TILDE: ~`,
// read back as `""`, and produced "env changed in config: TILDE" in a loop. A
// permanent false warning doesn't just cost noise: it destroys trust in the
// warning, and therefore the whole point of the drift check — the user
// learns to stop reading it, including the day an egress really was
// narrowed.
//
// The table covers the three families YAML 1.1 resolves outside a string:
// null (`~`, `null` and its casings), booleans, and numbers. exampleMixin
// contains none of them, hence the blind spot.
//
// KEYS are swept just as much as values, and it's not decorative symmetry:
// Env is a map[string]string that the nest and the agent fill in, NO charset
// validation applies anywhere in the cascade, and a null key is worse than a
// null value — it drops the WHOLE entry from the map, not just its
// right-hand side.
func TestRoundTripsHostileEnvValues(t *testing.T) {
	hostile := map[string]string{
		"TILDE":      "~",
		"NULL_LOW":   "null",
		"NULL_CAP":   "Null",
		"NULL_UPPER": "NULL",
		"EMPTY":      "",
		"NUMBER":     "8080",
		"FLOAT":      "1.5",
		"BOOL":       "true",
		"YES":        "yes",
		"OCTAL":      "0o17",
		"HEX":        "0x10",
		"TIME":       "1:30",
		"DASH":       "-",
		"STAR":       "*",
		// Hostile keys, same families. An innocuous value on the right: it's
		// the KEY being tested here.
		"~":    "tilde-value",
		"null": "null-low-value",
		"Null": "null-cap-value",
		"NULL": "null-upper-value",
		"true": "bool-value",
		"8080": "number-value",
		"1.5":  "float-value",
		"-":    "dash-value",
		"*":    "star-value",
		"0x10": "hex-value",
		"1:30": "time-value",
		"yes":  "yes-value",
	}

	denHome := t.TempDir()
	m := exampleMixin(t)
	m.Env = hostile
	if _, err := WriteMixin(denHome, m.SandboxName, m); err != nil {
		t.Fatalf("WriteMixin: %v", err)
	}

	read, err := ReadMixin(denHome, m.SandboxName)
	if err != nil {
		t.Fatalf("ReadMixin: %v", err)
	}
	for k, want := range hostile {
		if read.Env[k] != want {
			t.Errorf("Env[%q] = %q, want %q", k, read.Env[k], want)
		}
	}
	// From the user's perspective, this is the symptom that matters: no
	// warning on a configuration that hasn't moved.
	if d := Differences(m, read); len(d) != 0 {
		t.Errorf("an unchanged configuration must produce no difference; got: %v", d)
	}
}

// Same requirement on egress and on the freshness argv: they're user data
// too, they go through the same YAML rendering, and a value read back as
// `null` would suggest permanent drift.
func TestRoundTripsHostileEgressAndFreshness(t *testing.T) {
	denHome := t.TempDir()
	m := exampleMixin(t)
	m.Egress = []string{"null", "8080", "~", "github.com"}
	m.Freshness = []string{"bash", "-c", "true", "null", "~"}

	if _, err := WriteMixin(denHome, m.SandboxName, m); err != nil {
		t.Fatalf("WriteMixin: %v", err)
	}
	read, err := ReadMixin(denHome, m.SandboxName)
	if err != nil {
		t.Fatalf("ReadMixin: %v", err)
	}
	if !slices.Equal(read.Egress, m.Egress) {
		t.Errorf("Egress = %q, want %q", read.Egress, m.Egress)
	}
	if !slices.Equal(read.Freshness, m.Freshness) {
		t.Errorf("Freshness = %q, want %q", read.Freshness, m.Freshness)
	}
}

// Without this, a changed `mounts:` produces NO drift warning: ReadMixin
// would drop the link entry and Differences would compare two empty Links.
// The silence is exactly what the mounts feature exists to remove.
func TestReadMixinReadsBackTheLinkPhase(t *testing.T) {
	home := t.TempDir()
	m := Mixin{
		SandboxName: "api",
		Freshness:   []string{"bash", "-c", "FRESHNESS"},
		Links:       []string{"bash", "-c", "LINKS"},
	}
	if _, err := WriteMixin(home, "api", m); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadMixin(home, "api")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !slices.Equal(got.Links, m.Links) {
		t.Fatalf("Links = %v, want %v", got.Links, m.Links)
	}
	// The n-1 rule must still pick freshness, now that it is not entry 0.
	if !slices.Equal(got.Freshness, m.Freshness) {
		t.Fatalf("Freshness = %v, want %v", got.Freshness, m.Freshness)
	}
}

// A mixin written by an OLDER den carries one entry, which is freshness.
// Reading it must not mistake that entry for a link phase — doing so would
// report drift on every attach to a sandbox created before this change.
func TestReadMixinLeavesLinksEmptyForASingleEntryMixin(t *testing.T) {
	home := t.TempDir()
	m := Mixin{SandboxName: "api", Freshness: []string{"bash", "-c", "FRESHNESS"}}
	if _, err := WriteMixin(home, "api", m); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadMixin(home, "api")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got.Links) != 0 {
		t.Fatalf("Links = %v, want empty", got.Links)
	}
}

func TestDifferencesReportsAChangedLinkPhase(t *testing.T) {
	previous := Mixin{SandboxName: "api", Freshness: []string{"bash", "-c", "F"},
		Links: []string{"bash", "-c", "OLD"}}
	current := Mixin{SandboxName: "api", Freshness: []string{"bash", "-c", "F"},
		Links: []string{"bash", "-c", "NEW"}}
	if d := Differences(previous, current); len(d) == 0 {
		t.Fatal("a changed link phase must be reported as drift")
	}
}

func TestDifferencesIdenticalReturnsNothing(t *testing.T) {
	m := exampleMixin(t)
	if d := Differences(m, m); len(d) != 0 {
		t.Errorf("two identical mixins must produce no difference; got: %v", d)
	}
}

// The case the whole mechanism exists for: a NARROWED egress passes the
// settle-loop silently (the VM's broader policy obviously allows the
// narrower list), and the user believes their sandbox is tightened while it
// stayed open.
func TestDifferencesNamesTheNarrowedEgress(t *testing.T) {
	previous := exampleMixin(t)
	previous.Egress = []string{"api.anthropic.com", "github.com"}
	current := previous
	current.Egress = []string{"github.com"}

	d := Differences(previous, current)
	if len(d) != 1 {
		t.Fatalf("expected a single difference; got: %v", d)
	}
	if !strings.Contains(d[0], "api.anthropic.com") {
		t.Errorf("the difference must NAME the removed host; got: %q", d[0])
	}
	if strings.Contains(d[0], "github.com") {
		t.Errorf("an unchanged host must not be reported; got: %q", d[0])
	}
}

func TestDifferencesNamesTheAddedEgress(t *testing.T) {
	previous := exampleMixin(t)
	previous.Egress = []string{"github.com"}
	current := previous
	current.Egress = []string{"github.com", "pypi.org"}

	d := Differences(previous, current)
	if len(d) != 1 {
		t.Fatalf("expected a single difference; got: %v", d)
	}
	if !strings.Contains(d[0], "pypi.org") {
		t.Errorf("the difference must name the added host; got: %q", d[0])
	}
}

func TestDifferencesNamesEnvKeys(t *testing.T) {
	previous := exampleMixin(t)
	previous.Env = map[string]string{"KEPT": "1", "REMOVED": "1", "CHANGED": "before"}
	current := previous
	current.Env = map[string]string{"KEPT": "1", "ADDED": "1", "CHANGED": "after"}

	d := Differences(previous, current)
	joined := strings.Join(d, "\n")
	for _, key := range []string{"REMOVED", "ADDED", "CHANGED"} {
		if !strings.Contains(joined, key) {
			t.Errorf("key %q must be reported; got:\n%s", key, joined)
		}
	}
	if strings.Contains(joined, "KEPT") {
		t.Errorf("an unchanged key must not be reported; got:\n%s", joined)
	}
}

// Env VALUES never reach the terminal, only the keys. WriteMixin sets
// spec.yaml to 0600 precisely because environment.variables carries free-form
// user env, where an API key or a credentialed URI naturally ends up. A drift
// warning that prints them copies them into the terminal scrollback and into
// CI logs — undoing that precaution one level down.
func TestDifferencesDoesNotPrintEnvValues(t *testing.T) {
	previous := exampleMixin(t)
	previous.Env = map[string]string{"API_KEY": "sk-secret-before", "GONE": "secret-gone"}
	current := previous
	current.Env = map[string]string{"API_KEY": "sk-secret-after", "NEW": "secret-new"}

	joined := strings.Join(Differences(previous, current), "\n")
	for _, secret := range []string{"sk-secret-before", "sk-secret-after", "secret-gone", "secret-new"} {
		if strings.Contains(joined, secret) {
			t.Errorf("value %q must never be rendered; got:\n%s", secret, joined)
		}
	}
}

// The freshness command is the fail-closed guard of spec §9.1: it changes as
// soon as `bin_dirs` or `update` changes, and a live sandbox keeps running
// the OLD one on every boot.
func TestDifferencesReportsFreshness(t *testing.T) {
	previous := exampleMixin(t)
	current := previous
	current.Freshness = []string{"bash", "-c", "something else"}

	d := Differences(previous, current)
	if len(d) != 1 {
		t.Fatalf("expected a single difference; got: %v", d)
	}
	if !strings.Contains(d[0], "freshness") {
		t.Errorf("the difference must name freshness; got: %q", d[0])
	}
	// The script is multiline and can run to dozens of lines: rendering it
	// would drown out the other differences on the terminal.
	if strings.Contains(d[0], "something else") {
		t.Errorf("the script must not be rendered in full; got: %q", d[0])
	}
}

// Order must be deterministic: these lines go to the user's terminal on every
// attach, and Go map iteration order is random.
func TestDifferencesIsDeterministic(t *testing.T) {
	previous := exampleMixin(t)
	previous.Egress = []string{"a.test", "b.test", "c.test"}
	previous.Env = map[string]string{"A": "1", "B": "1", "C": "1", "D": "1"}
	current := previous
	current.Egress = []string{"z.test", "y.test"}
	current.Env = map[string]string{"Z": "1", "Y": "1"}

	reference := Differences(previous, current)
	if len(reference) < 6 {
		t.Fatalf("the test case must produce several differences; got: %v", reference)
	}
	for i := 0; i < 20; i++ {
		if d := Differences(previous, current); !slices.Equal(d, reference) {
			t.Fatalf("round %d: %v, want %v", i, d, reference)
		}
	}
}
