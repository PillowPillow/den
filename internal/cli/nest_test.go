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

// M-1 — a nest carrying a subcommand's name is listed by `den nest ls` and
// resolved by `den nest show`, but `den <name>` will ALWAYS run the
// subcommand: it is never spawnable. This is the defect found in T3 with
// `-api` — den names an object it then refuses to address — and exactly the
// counterpart of D1's suggestion, which only holds in the other direction.
//
// The warning goes on STDERR: the list must stay pipeable without a warning
// slipping in, and that is exactly what executeCmd (which merges both
// streams) cannot distinguish.
func TestNestLsWarnsAboutNestsShadowedByASubcommand(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "nests"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"api", "ls", "version"} {
		if err := os.WriteFile(filepath.Join(dir, "nests", name+".yaml"),
			[]byte("stack: devx\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	stdout, stderr, err := executeCmdSeparateStreams(t, NewRootCmd(), "nest", "ls", "--den-home", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The list itself does not change: the shadowed nests EXIST.
	for _, name := range []string{"api", "ls", "version"} {
		if !strings.Contains(stdout, name) {
			t.Errorf("nest %q must stay listed; stdout = %q", name, stdout)
		}
	}
	for _, shadowed := range []string{"ls", "version"} {
		if !strings.Contains(stderr, shadowed) {
			t.Errorf("shadowed nest %q must be reported; stderr = %q", shadowed, stderr)
		}
	}
	// And especially no false positive: "api" is shadowed by nothing.
	if strings.Contains(stderr, "api") {
		t.Errorf("\"api\" is shadowed by no subcommand; stderr = %q", stderr)
	}
	// The warning must not pollute the pipeable output.
	if strings.Contains(stdout, "shadowed") || strings.Contains(stdout, "warning") {
		t.Errorf("the warning must go on stderr, not stdout; stdout = %q", stdout)
	}
}

// Without a shadowed nest, stderr must stay EMPTY: a permanent warning is a
// warning nobody reads anymore.
func TestNestLsWarnsAboutNothingWithoutACollision(t *testing.T) {
	dir := testDenHomeWithNest(t, "api")

	stdout, stderr, err := executeCmdSeparateStreams(t, NewRootCmd(), "nest", "ls", "--den-home", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "api") {
		t.Errorf("stdout = %q, expected the nest listed", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, expected empty: no nest is shadowed", stderr)
	}
}

// `den nest show corp:api` reads the nest AND its stack from the source's own
// root (sources/corp/{nests,stacks}), never from ~/.den — same doctrine as
// spawn.go: source.Locate is the sole place a reference becomes a root.
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

// An unreadable (or absent) sources/ directory must not break the local
// listing: `den nest ls` still shows what IS there.
func TestNestLsToleratesAnUnreadableSourcesDir(t *testing.T) {
	dir := testDenHomeWithNest(t, "api")
	// No sources/ directory at all — the ordinary case for a den that never
	// ran `den source add`.
	out, err := run(t, "nest", "ls", "--den-home", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "api") {
		t.Errorf("the local nest must stay listed; got:\n%s", out)
	}
}
