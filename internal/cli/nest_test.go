package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/source"
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

// testDenHomePrompting adds the nest the new mode exists for: `select: prompt`,
// with one required repo, one optional path and one optional `key:` this machine
// does not map (the config.yaml above declares no `repos:` at all).
//
// The unmapped key is the point of the fixture, not decoration: on a prompting
// nest it is a NORMAL state — the repo is one of thirty the session did not ask
// for — and it is exactly the state that used to make `den nest show` refuse.
func testDenHomePrompting(t *testing.T) string {
	t.Helper()
	dir := testDenHome(t)
	writeUnder(t, dir, "nests/generic.yaml", `
select: prompt
stack: devx
repos:
  - { path: /dev/api }
  - { path: /dev/front, optional: true }
  - { key: crm, optional: true, url: git@github.com:acme/crm.git }
`)
	return dir
}

// `den nest show` is documented as the dry-run of `den spawn`, and a dry-run
// that ACCEPTS a flag the run refuses is not one. `--without` on a `select:
// prompt` nest is refused by internal/spawn at its step 0bis; this command never
// goes through Spawn, so until the verdict moved into nest.CheckWithout it
// printed a confident resolution of a command den would have rejected — one
// flag, two answers.
//
// The message parts asserted here are the ones TestSpawnRefusesWithoutOnAPromptingNest
// asserts on the spawn side: same nest, same flag, same sentence, on purpose.
//
// The `select: all` floor is TestNestShowRespectsSelectionFlags above — there
// `--without front` still resolves and still drops the repo.
func TestNestShowRefusesWithoutOnAPromptingNest(t *testing.T) {
	dir := testDenHomePrompting(t)

	out, err := run(t, "nest", "show", "generic", "--without", "front")
	if err == nil {
		t.Fatalf("--without on a nest with no default selection must be refused; got:\n%s", out)
	}
	for _, want := range []string{
		"generic",
		"--without",
		"`--only repo,...`",
		filepath.Join(dir, "nests", "generic.yaml"),
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must carry %q; got: %v", want, err)
		}
	}
	if strings.Contains(out, "stack:") {
		t.Errorf("a refused command must not also print a resolution:\n%s", out)
	}
}

// The dry-run of the new mode must RUN on the nests the mode exists for. Before
// this, `den nest show generic` died on the first optional key this machine does
// not map — the exact state a `select: prompt` nest is built to have, since it
// declares a team's catalogue and a machine maps what it works on.
//
// Rendering AND annotating are one assertion here on purpose: a resolution that
// merely stopped refusing would have dropped the repo silently, which is the
// other way to get this wrong.
func TestNestShowRendersAPromptingNestAndNamesItsUnmappedKeys(t *testing.T) {
	dir := testDenHomePrompting(t)

	out, err := run(t, "nest", "show", "generic")
	if err != nil {
		t.Fatalf("the dry-run of a prompting nest must resolve: %v", err)
	}
	// The nest is really rendered, not summarised: the mapped repos are there.
	for _, want := range []string{"devx:v1", "/dev/api", "/dev/front"} {
		if !strings.Contains(out, want) {
			t.Errorf("output must still resolve the nest (%q missing):\n%s", want, out)
		}
	}
	// And the unmapped key is named, with the fix and the clone URL — the same
	// sentence a spawn would have refused with.
	for _, want := range []string{"crm", filepath.Join(dir, "config.yaml"), "git@github.com:acme/crm.git"} {
		if !strings.Contains(out, want) {
			t.Errorf("the unmapped key must be reported with its remedy (%q missing):\n%s", want, out)
		}
	}
	// It is NOT listed as a repo of the spawn: it has no path on this machine,
	// and a listing of paths is what `repos:` promises.
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "  - ") && strings.HasSuffix(line, "(optional)") &&
			strings.Contains(line, "crm") {
			t.Errorf("an unmapped key must not be listed as a mounted repo: %q", line)
		}
	}
}

// The floor: on a `select: all` nest the repo IS meant to be mounted, so an
// unmapped key is a real fault and the dry-run must keep refusing it — den
// drops nothing on its own (spec §2). Same nest file as the test above bar one
// line, which is the only difference that may decide this.
func TestNestShowStillRefusesAnUnmappedKeyOnAnOrdinaryNest(t *testing.T) {
	dir := testDenHome(t)
	writeUnder(t, dir, "nests/ordinary.yaml", `
stack: devx
repos:
  - { path: /dev/api }
  - { key: crm, optional: true, url: git@github.com:acme/crm.git }
`)

	out, err := run(t, "nest", "show", "ordinary")
	if err == nil {
		t.Fatalf("an unmapped key on a select: all nest must still refuse; got:\n%s", out)
	}
	if !strings.Contains(err.Error(), "crm") {
		t.Errorf("the refusal must name the key; got: %v", err)
	}
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

// `den nest show` is the dry-run of a spawn, and `mounts:` is the one part of
// what a sandbox receives that lives in NO nest file: it is global. Left out of
// this listing, the reader has no command that says which host directories a
// spawn will expose — and the `ssh:` line names a mode whose directory it never
// prints.
func TestNestShowPrintsTheMounts(t *testing.T) {
	dir := t.TempDir()
	writeUnder(t, dir, "config.yaml", `
agents:
  claude:
    update: "claude update"
defaults:
  agent: claude
  stack: devx
ssh:
  mode: mount
  dir: /host/ssh_sbx
mounts:
  - host: /host/digitaleo
    link: $HOME/.digitaleo
  - host: /host/datasets
    ro: true
`)
	writeUnder(t, dir, "stacks/devx/stack.yaml", "image: devx:v1\n")
	writeUnder(t, dir, "nests/api.yaml", "stack: devx\nrepos:\n  - { path: /dev/api }\n")
	t.Setenv("DEN_HOME", dir)

	out, err := run(t, "nest", "show", "api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []string{
		"mounts:",
		"/host/digitaleo -> $HOME/.digitaleo",
		"mounts[0]",
		// A link-less mount is legitimate and must be listed too: it lands at
		// its host path, which is exactly what the reader needs to know.
		"/host/datasets",
		"ro",
		// The ssh.mode sugar is an ordinary mount after resolution, and it is
		// the one that exposes the user's keys.
		"/host/ssh_sbx -> $HOME/.ssh",
		"ssh.dir",
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
// same reference spawn, `den exec`/`rm`/`ports` and `den nest show` all accept
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

// `den nest show corp:api` resolves through the SOURCE's mapping, and its
// unmapped-key remedy names the source's own file. A dry-run that sent the user
// to config.yaml would name a file where adding the key changes nothing: a
// manifested source never reads the global mapping (spec §6).
func TestNestShowUsesTheSourceScopedMapping(t *testing.T) {
	dir := testDenHome(t)
	writeUnder(t, dir, filepath.Join("sources", "corp", "stacks", "teamstack", "stack.yaml"), "image: teamstack:v1\n")
	writeUnder(t, dir, filepath.Join("sources", "corp", "nests", "api.yaml"),
		"stack: teamstack\nselect: prompt\nrepos:\n  - { key: api }\n  - { key: crm, optional: true }\n")
	writeUnder(t, dir, filepath.Join("sources", "corp", "den-source.yaml"), `schema_version: 1
kind: source
metadata: { name: corp, version: 1.0.0 }
exports:
  nests:
    - { name: api, path: nests/api.yaml }
  stacks:
    - { name: teamstack, path: stacks/teamstack/stack.yaml }
`)
	if err := source.WritePersonal(dir, "corp", source.Personal{
		Version: "1.0.0",
		Repos:   map[string]string{"api": "/dev/source-api"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := source.WriteReceipt(dir, "corp", source.Receipt{
		Status: source.StatusReady, Version: "1.0.0",
	}); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "nest", "show", "corp:api")
	if err != nil {
		t.Fatalf("den nest show corp:api: %v", err)
	}
	if !strings.Contains(out, "/dev/source-api") {
		t.Errorf("the source's own mapping must resolve the key:\n%s", out)
	}
	if want := source.PersonalPath(dir, "corp"); !strings.Contains(out, want) {
		t.Errorf("the unmapped key's remedy must name %s:\n%s", want, out)
	}
	if strings.Contains(out, filepath.Join(dir, "config.yaml")) {
		t.Errorf("a manifested source's remedy must not send the user to the global config:\n%s", out)
	}
}
