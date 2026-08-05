package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testDenHome builds a complete ~/.den and points DEN_HOME at it.
func testDenHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	writeUnder(t, dir, "config.yaml", `
agents:
  claude:
    config_dir: /tmp/den-agents/claude
    env: { CLAUDE_CONFIG_DIR: "{config_dir}" }
    bin_dirs: ["$HOME/.local/bin"]
    update: "claude update"
defaults:
  agent: claude
  stack: devx
egress:
  - api.anthropic.com
`)
	writeUnder(t, dir, "stacks/devx/stack.yaml", "image: devx:v1\n")
	writeUnder(t, dir, "stacks/dgdevx/stack.yaml", "image: dgdevx:v1\nparent: devx\negress: [gitlab.digitaleo.com]\n")
	writeUnder(t, dir, "nests/api.yaml", "stack: devx\nrepos:\n  - { path: /dev/api }\n")
	writeUnder(t, dir, "nests/fullstack.yaml", `
stack: dgdevx
egress: ["10.22.11.54:27017"]
repos:
  - { path: /dev/api }
  - { path: /dev/front, optional: true }
`)

	t.Setenv("DEN_HOME", dir)
	return dir
}

func TestNestLsListsNests(t *testing.T) {
	testDenHome(t)
	out, err := run(t, "nest", "ls")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, expected := range []string{"api", "fullstack", "devx", "dgdevx"} {
		if !strings.Contains(out, expected) {
			t.Errorf("output = %q, expected containing %q", out, expected)
		}
	}
	// sorted: api before fullstack
	if strings.Index(out, "api") > strings.Index(out, "fullstack") {
		t.Errorf("output not sorted: %q", out)
	}
}

// `den nest ls` prints the healthy nests AND reports the broken ones by name,
// but still returns an error (non-zero exit code): the list is browsable, but
// something is still left to fix.
func TestNestLsReportsBrokenOnesAndReturnsAnError(t *testing.T) {
	dir := testDenHomeWithNest(t, "api")
	if err := os.WriteFile(filepath.Join(dir, "nests", "broken.yaml"), []byte("egres: [x]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "nest", "ls", "--den-home", dir)
	if err == nil {
		t.Fatal("expected an error: a nest is broken")
	}
	if !strings.Contains(out, "api") {
		t.Errorf("the healthy nest must stay listed; got:\n%s", out)
	}
	// Exact string, not just "broken": LoadNest already names the file in its
	// decode error, which would make Contains(out, "broken") true even if
	// `den nest ls` omitted bn.Name.
	if !strings.Contains(out, "! broken:") {
		t.Errorf("the broken nest must be reported by name; got:\n%s", out)
	}
}

// A ~/.den/nests holding ONLY broken nests must not print "no nest declared":
// the user has nests, they are just all unreadable — an absence message would
// be a lie on top of a false success (exit code 0 while there is something to
// fix).
func TestNestLsDoesNotPrintNoNestDeclaredWhenAllAreBroken(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "nests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nests", "broken.yaml"), []byte("egres: [x]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "nest", "ls", "--den-home", dir)
	if err == nil {
		t.Fatal("expected an error: the only nest present is broken")
	}
	if strings.Contains(out, "no nest declared") {
		t.Errorf("a broken nest is not an absence of nest; got:\n%s", out)
	}
}

func TestNestShowPrintsTheResolution(t *testing.T) {
	testDenHome(t)
	out, err := run(t, "nest", "show", "fullstack")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []string{
		"fullstack",
		"dgdevx:v1",              // stack's image
		"claude",                 // resolved agent
		"/tmp/den-agents/claude", // resolved config_dir
		"10.22.11.54:27017",      // nest's egress
		"api.anthropic.com",      // baseline egress
		"gitlab.digitaleo.com",   // stack's egress
		"/dev/front",             // optional repo listed
	}
	for _, e := range expected {
		if !strings.Contains(out, e) {
			t.Errorf("output = %q, expected containing %q", out, e)
		}
	}
}

func TestNestShowUnknownNest(t *testing.T) {
	testDenHome(t)
	if _, err := run(t, "nest", "show", "ghost"); err == nil {
		t.Fatal("expected an error for an unknown nest")
	}
}

func TestNestShowPrintsSubstitutedEnv(t *testing.T) {
	testDenHome(t)
	out, err := run(t, "nest", "show", "api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "CLAUDE_CONFIG_DIR=/tmp/den-agents/claude") {
		t.Errorf("the printed env must be substituted; got:\n%s", out)
	}
	if strings.Contains(out, "{config_dir}") {
		t.Errorf("the {config_dir} token must never be printed; got:\n%s", out)
	}
}

func TestNestShowRespectsSelectionFlags(t *testing.T) {
	testDenHome(t)
	out, err := run(t, "nest", "show", "fullstack", "--without", "front")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "/dev/front") {
		t.Errorf("the excluded repo still appears: %q", out)
	}
	if !strings.Contains(out, "/dev/api") {
		t.Errorf("the required repo disappeared: %q", out)
	}
}

// `den nest show corp:api` reads the nest AND its stack from the source's own
// root (sources/corp/{nests,stacks}), never from ~/.den — same doctrine as
// spawn.go: source.Locate is the sole place a reference becomes a root.
//
// A LOCAL stack of the SAME name "devx", declaring a DIFFERENT image, is the
// discriminating fixture: without it, "devx" resolves from either root and
// the test cannot tell "loaded from the source" from "loaded from a root
// that happens to contain it". With it, only the printed image says which
// root won.
func TestNestShowAcceptsASourceReference(t *testing.T) {
	dir := t.TempDir()
	writeUnder(t, dir, "config.yaml", `
defaults:
  agent: claude
  stack: devx
agents:
  claude:
    config_dir: /tmp/den-agents/claude
    update: "claude update"
`)
	// wrongstack:v1, not devx:v1's own "local-" prefixed variant: the LATTER
	// would still contain "devx:v1" as a trailing substring and defeat the
	// very discrimination this fixture exists to provide.
	writeUnder(t, dir, filepath.Join("stacks", "devx", "stack.yaml"), "image: wrongstack:v1\n")
	writeUnder(t, dir, filepath.Join("sources", "corp", "stacks", "devx", "stack.yaml"), "image: devx:v1\n")
	writeUnder(t, dir, filepath.Join("sources", "corp", "nests", "api.yaml"),
		"stack: devx\nrepos:\n  - { path: /dev/api }\n")

	out, err := run(t, "nest", "show", "corp:api", "--den-home", dir)
	if err != nil {
		t.Fatalf("den nest show corp:api: %v", err)
	}
	if !strings.Contains(out, "devx:v1") {
		t.Errorf("the stack must have been read from the source's own root; got:\n%s", out)
	}
	if strings.Contains(out, "wrongstack:v1") {
		t.Errorf("the LOCAL stack of the same name must not have been used; got:\n%s", out)
	}
}

// A nest loaded FROM a source may reference its stack only BARE: a prefixed
// `stack:` would resolve differently per machine (whichever name the OTHER
// source happens to be installed under there) — same refusal as spawn.go and
// `den lint`'s checkNest.
func TestNestShowRefusesAPrefixedStackInsideASource(t *testing.T) {
	dir := t.TempDir()
	writeUnder(t, dir, "config.yaml",
		"defaults:\n  agent: claude\n  stack: devx\nagents:\n  claude: {config_dir: /tmp/c, update: x}\n")
	writeUnder(t, dir, filepath.Join("sources", "corp", "nests", "api.yaml"), "stack: other:devx\n")

	_, err := run(t, "nest", "show", "corp:api", "--den-home", dir)
	if err == nil {
		t.Fatal("expected a refusal: a source nest's `stack:` must be bare")
	}
	if !strings.Contains(err.Error(), "bare") {
		t.Errorf("the message must say WHY: %v", err)
	}
}

// A LOCAL nest (no source prefix on the NEST itself) may still reference its
// stack with a PREFIXED reference ("stack: corp:teamstack") — same doctrine
// as internal/spawn.Spawn's TestSpawnLocalNestWithSourceStack. Before the
// spawn.ResolveStack extraction, `den nest show` called
// `config.LoadStacks(home)` unconditionally and never consulted
// source.Locate for the STACK reference at all, so this nest resolved under
// `den spawn` but refused under `den nest show` with an unrelated "stack
// not found" — a divergence nobody asked to fix and nothing pinned. This
// test is that pin: it fails if `den nest show` ever regresses to reading
// only the personal den home for the stack.
func TestNestShowResolvesALocalNestsPrefixedStackReference(t *testing.T) {
	dir := t.TempDir()
	// defaults.stack must still name a REAL bare stack — config.LoadGlobal
	// validates it unconditionally, regardless of whether the nest under
	// test ever falls back on it. Not consulted here: n.Stack ("corp:
	// teamstack") is non-empty, so ResolveStack never reaches the fallback.
	writeUnder(t, dir, "config.yaml",
		"defaults:\n  agent: claude\n  stack: devx\nagents:\n  claude: {config_dir: /tmp/c, update: x}\n")
	writeUnder(t, dir, filepath.Join("stacks", "devx", "stack.yaml"), "image: devx:v1\n")
	writeUnder(t, dir, filepath.Join("sources", "corp", "stacks", "teamstack", "stack.yaml"),
		"image: teamstack:v1\n")
	writeUnder(t, dir, filepath.Join("nests", "n.yaml"), "stack: corp:teamstack\nrepos:\n  - { path: /dev/api }\n")

	out, err := run(t, "nest", "show", "n", "--den-home", dir)
	if err != nil {
		t.Fatalf("den nest show n: %v", err)
	}
	if !strings.Contains(out, "teamstack:v1") {
		t.Errorf("the prefixed stack reference must have resolved from the source; got:\n%s", out)
	}
}

// `den nest ls` lists source nests too, prefixed `<source>:<name>` — the
// same reference spawn, `den sh`/`rm`/`ports` and `den nest show` all accept
// for that same nest.
func TestNestLsListsSourceNests(t *testing.T) {
	dir := testDenHomeWithNest(t, "api")
	writeUnder(t, dir, filepath.Join("sources", "corp", "stacks", "devx", "stack.yaml"), "image: devx:v1\n")
	writeUnder(t, dir, filepath.Join("sources", "corp", "nests", "backend.yaml"), "stack: devx\n")

	out, err := run(t, "nest", "ls", "--den-home", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "api") {
		t.Errorf("the local nest must stay listed; got:\n%s", out)
	}
	if !strings.Contains(out, "corp:backend") {
		t.Errorf("the source nest must be listed, prefixed; got:\n%s", out)
	}
}

// A broken source nest is reported like a broken LOCAL one, prefixed — and it
// must not hide the healthy local nests, nor a healthy nest of the SAME
// source.
func TestNestLsReportsBrokenSourceNestsPrefixed(t *testing.T) {
	dir := testDenHomeWithNest(t, "api")
	writeUnder(t, dir, filepath.Join("sources", "corp", "stacks", "devx", "stack.yaml"), "image: devx:v1\n")
	writeUnder(t, dir, filepath.Join("sources", "corp", "nests", "backend.yaml"), "stack: devx\n")
	writeUnder(t, dir, filepath.Join("sources", "corp", "nests", "broken.yaml"), "egres: [x]\n")

	out, err := run(t, "nest", "ls", "--den-home", dir)
	if err == nil {
		t.Fatal("expected an error: a source nest is broken")
	}
	if !strings.Contains(out, "api") || !strings.Contains(out, "corp:backend") {
		t.Errorf("the healthy nests must stay listed; got:\n%s", out)
	}
	if !strings.Contains(out, "! corp:broken:") {
		t.Errorf("the broken source nest must be reported by its prefixed name; got:\n%s", out)
	}
}

// A MISSING sources/ directory must not break the local listing: `den nest
// ls` still shows what IS there. The ordinary case for a den that never ran
// `den source add`.
//
// This is one of three ways listSourceNests can fail open — see
// TestNestLsToleratesASourcesNestsDirThatCannotBeRead (a source installed but
// broken) and TestNestLsSkipsNonDirectoryEntriesUnderSources (junk directly
// under sources/) for the other two.
func TestNestLsToleratesAnUnreadableSourcesDir(t *testing.T) {
	dir := testDenHomeWithNest(t, "api")
	out, err := run(t, "nest", "ls", "--den-home", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "api") {
		t.Errorf("the local nest must stay listed; got:\n%s", out)
	}
}

// An installed source whose OWN nests/ cannot be read at all (not merely one
// broken nest inside it — TestNestLsReportsBrokenSourceNestsPrefixed already
// covers that) is reported as a single broken entry named "<source>:", and
// must not hide the local listing either.
func TestNestLsToleratesASourcesNestsDirThatCannotBeRead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("unreliable as root: permissions are ignored")
	}
	dir := testDenHomeWithNest(t, "api")
	nestsDir := filepath.Join(dir, "sources", "corp", "nests")
	if err := os.MkdirAll(nestsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(nestsDir, 0o000); err != nil {
		t.Fatal(err)
	}
	// EMPIRICAL check, following TestLsReportsAnUnreadableNestsRoot's model: a
	// chmod 0o000 is not enough everywhere (root, containers, some CFS setups
	// also ignore permissions).
	if _, err := os.ReadDir(nestsDir); err == nil {
		if err := os.Chmod(nestsDir, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Skip("reading a 0o000 directory succeeds on this environment: unreliable here")
	}
	t.Cleanup(func() {
		// t.TempDir() must be able to clean up behind us.
		if err := os.Chmod(nestsDir, 0o755); err != nil {
			t.Fatal(err)
		}
	})

	out, err := run(t, "nest", "ls", "--den-home", dir)
	if err == nil {
		t.Fatal("expected an error: the source's own nests/ cannot be read")
	}
	if !strings.Contains(out, "api") {
		t.Errorf("the local nest must stay listed despite the broken source; got:\n%s", out)
	}
	if !strings.Contains(out, "! corp:") {
		t.Errorf("the source must be reported broken, by name, as \"corp:\"; got:\n%s", out)
	}
}

// A stray non-directory entry directly under sources/ (a README, a leftover
// file — never a git clone) is skipped rather than fed to nest.ListNests as
// if it were an installed source's root.
func TestNestLsSkipsNonDirectoryEntriesUnderSources(t *testing.T) {
	dir := testDenHomeWithNest(t, "api")
	writeUnder(t, dir, filepath.Join("sources", "corp", "stacks", "devx", "stack.yaml"), "image: devx:v1\n")
	writeUnder(t, dir, filepath.Join("sources", "corp", "nests", "backend.yaml"), "stack: devx\n")
	if err := os.WriteFile(filepath.Join(dir, "sources", "README.md"), []byte("not a source\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "nest", "ls", "--den-home", dir)
	if err != nil {
		t.Fatalf("a stray file under sources/ must not fail the listing: %v", err)
	}
	if !strings.Contains(out, "api") || !strings.Contains(out, "corp:backend") {
		t.Errorf("the real nests must stay listed; got:\n%s", out)
	}
	if strings.Contains(out, "README") {
		t.Errorf("the stray file must not be treated as a source; got:\n%s", out)
	}
}

// `den nest show corp:api` printed `nest:   api`, dropping the prefix the
// user typed — and on a den that also owns a LOCAL `api`, that header names
// a different nest than the one being shown.
func TestNestShowHeaderKeepsTheSourcePrefix(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, minimalConfig)
	writeUnder(t, dir, filepath.Join("sources", "corp", "stacks", "devx", "stack.yaml"),
		"image: corp-devx:v1\n")
	writeUnder(t, dir, filepath.Join("sources", "corp", "nests", "api.yaml"), "stack: devx\n")

	out, err := run(t, "nest", "show", "corp:api", "--den-home", dir)
	if err != nil {
		t.Fatalf("den nest show corp:api: %v", err)
	}
	if !strings.Contains(out, "nest:   corp:api") {
		t.Errorf("the header must name the reference the user typed; got:\n%s", out)
	}
}

func TestNestShowResolvesCommandLineRepos(t *testing.T) {
	testDenHome(t)
	out, err := run(t, "nest", "show", "api", "/dev/hotfix")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// `den nest show` is the dry-run of a spawn: it must list what would be
	// mounted, and say which entries came from the command line — "required"
	// and "optional" describe a `repos:` declaration, which this is not.
	if !strings.Contains(out, "/dev/hotfix (command line)") {
		t.Errorf("output = %q, expected the ad-hoc repo listed with its origin", out)
	}
	if !strings.Contains(out, "/dev/api (required)") {
		t.Errorf("output = %q, expected the declared repo to keep its own wording", out)
	}
}

// TestNestShowResolvesRelativeCommandLineRepos is what actually exercises
// newNestShowCmd's os.Getwd() call: an ABSOLUTE positional (the case above)
// never touches opts.Cwd, since nest.parseRepoArgs only consults it to
// resolve a RELATIVE path — that call could be missing entirely and the test
// above would still pass. A relative positional forces the wiring: without
// it, opts.Cwd stays "" and nest.Resolve refuses with its own "Cwd is unset"
// wiring-defect message instead of resolving.
func TestNestShowResolvesRelativeCommandLineRepos(t *testing.T) {
	testDenHome(t)
	out, err := run(t, "nest", "show", "api", "./hotfix")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(wd, "hotfix") + " (command line)"
	if !strings.Contains(out, expected) {
		t.Errorf("output = %q, expected containing %q — the relative path must resolve "+
			"against the process's working directory", out, expected)
	}
}
