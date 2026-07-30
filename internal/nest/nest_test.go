package nest

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeNest(t *testing.T, denHome, name, content string) {
	t.Helper()
	dir := filepath.Join(denHome, "nests")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const completeNest = `
stack: dgdevx
env:
  SOME_VAR: value
egress:
  - 10.22.11.54:27017
repos:
  - { path: ~/dev/review-mgmt }
  - { path: ~/dev/front-app, optional: true }
ports:
  base: 9100
  publish:
    - { name: vite, container: 5173, open: true }
    - { name: cdp, container: 9223, loopback_lock: true }
agents:
  claude: ~/.den/agents/claude-fullstack
`

func TestLoadNest(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	denHome := t.TempDir()
	writeNest(t, denHome, "fullstack", completeNest)

	n, err := LoadNest(denHome, "fullstack")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n.Name != "fullstack" || n.Stack != "dgdevx" {
		t.Errorf("nest = %+v", n)
	}
	if n.Env["SOME_VAR"] != "value" {
		t.Errorf("Env = %v", n.Env)
	}
	if len(n.Repos) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(n.Repos))
	}
	if want := filepath.Join(home, "dev/review-mgmt"); n.Repos[0].Path != want {
		t.Errorf("Repos[0].Path = %q, expected %q (tilde expanded)", n.Repos[0].Path, want)
	}
	if n.Repos[0].Optional {
		t.Error("Repos[0] must be required")
	}
	if !n.Repos[1].Optional {
		t.Error("Repos[1] must be optional")
	}
	if got := n.Repos[0].Name(); got != "review-mgmt" {
		t.Errorf("Name() = %q, expected %q", got, "review-mgmt")
	}
	if n.Ports.Base != 9100 || len(n.Ports.Publish) != 2 {
		t.Errorf("Ports = %+v", n.Ports)
	}
	if !n.Ports.Publish[1].LoopbackLock {
		t.Error("the cdp port must be loopback_lock")
	}
	if want := filepath.Join(home, ".den/agents/claude-fullstack"); n.Agents["claude"] != want {
		t.Errorf("Agents[claude] = %q, expected %q", n.Agents["claude"], want)
	}
}

// A nest's name is its file's basename — this is the nominal case, and the
// only one: there is no other source of identity.
func TestLoadNestNameDerivedFromFile(t *testing.T) {
	denHome := t.TempDir()
	writeNest(t, denHome, "review", "stack: devx\n")
	n, err := LoadNest(denHome, "review")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n.Name != "review" {
		t.Errorf("Name = %q, expected %q derived from the file", n.Name, "review")
	}
}

// A `name:` in the content used to be a second, divergent source of identity:
// `den nest ls` stopped being sorted (sorting is on filenames) and
// `den nest show <displayed-name>` would look for a file that didn't exist.
// The field no longer exists in the schema: strict decoding rejects it at the
// source, with a message naming the offending key.
func TestLoadNestRejectsANameInContent(t *testing.T) {
	denHome := t.TempDir()
	writeNest(t, denHome, "api", "name: fullstack\nstack: devx\n")
	_, err := LoadNest(denHome, "api")
	if err == nil {
		t.Fatal("expected a rejection: a nest's identity comes from its file, not its content")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error = %q, expected a mention of the `name` key", err.Error())
	}
}

func TestLoadNestRejectsAnUnknownKey(t *testing.T) {
	denHome := t.TempDir()
	writeNest(t, denHome, "review", "stack: devx\negres:\n  - github.com\n")
	_, err := LoadNest(denHome, "review")
	if err == nil {
		t.Fatal("expected an error on the unknown key `egres`")
	}
	if !strings.Contains(err.Error(), "egres") {
		t.Errorf("error = %q, expected a mention of the offending key", err.Error())
	}
	if !strings.Contains(err.Error(), filepath.Join(denHome, "nests", "review.yaml")) {
		t.Errorf("error = %q, expected the offending file's path", err.Error())
	}
}

func TestLoadNestEmptyFile(t *testing.T) {
	denHome := t.TempDir()
	writeNest(t, denHome, "review", "")
	n, err := LoadNest(denHome, "review")
	if err != nil {
		t.Fatalf("an empty nest must not be a load error: %v", err)
	}
	if n.Name != "review" {
		t.Errorf("Name = %q, expected %q derived from the file", n.Name, "review")
	}
}

// The ABSENCE of a nest is the only load failure that means "this object does
// not exist", and the CLI relies on it to decide whether to suggest a close
// subcommand (`den doctr` => `den doctor`). It must therefore be recognizable
// other than by reading the message.
func TestLoadNestMissingIsARecognizableType(t *testing.T) {
	denHome := t.TempDir()
	writeNest(t, denHome, "api", "stack: devx\n") // the nests/ directory exists

	_, err := LoadNest(denHome, "absent")
	var notFound *NestNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("errors.As(err, &NestNotFoundError) must succeed; err = %v (%T)", err, err)
	}
	if notFound.Name != "absent" {
		t.Errorf("Name = %q, expected %q", notFound.Name, "absent")
	}
	if expected := filepath.Join(denHome, "nests", "absent.yaml"); notFound.Path != expected {
		t.Errorf("Path = %q, expected %q", notFound.Path, expected)
	}
	// fs.ErrNotExist must stay in the chain: code that already tests
	// os.IsNotExist on this error must not stop working.
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("fs.ErrNotExist must stay detectable in the chain; err = %v", err)
	}
	// The message does not change: this type changes what the code can
	// inspect, not what the user reads.
	for _, expected := range []string{`nest "absent"`, filepath.Join(denHome, "nests", "absent.yaml")} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("message = %q, expected it to contain %q", err.Error(), expected)
		}
	}
	// The path exactly once, and in den's own wording. This is the most
	// mundane error in the project — a mistyped nest name — and it rendered,
	// measured on the binary:
	//
	//	nest "unknown": reading <path>: open <path>: no such file or directory
	//
	// The OS's *fs.PathError already carries the absolute path LoadNest just
	// named, and its raw reason must not leak past config.FileError.
	path := filepath.Join(denHome, "nests", "absent.yaml")
	if n := strings.Count(err.Error(), path); n != 1 {
		t.Errorf("the path appears %d times, expected 1; message: %s", n, err.Error())
	}
	if strings.Contains(err.Error(), "no such file or directory") {
		t.Errorf("the raw OS reason must not leak: %s", err.Error())
	}
}

// The counterpart: a nest that IS present but unreadable is not "not found".
// A directory in the file's place, not a chmod 0000: the suite runs as root,
// where permissions block nothing.
func TestLoadNestUnreadableIsNotNotFound(t *testing.T) {
	denHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(denHome, "nests", "api.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := LoadNest(denHome, "api")
	if err == nil {
		t.Fatal("an unreadable nest must be a load error")
	}
	var notFound *NestNotFoundError
	if errors.As(err, &notFound) {
		t.Errorf("a present nest must not be reported as not found; err = %v", err)
	}
}

// Two repos sharing a basename are not honorable: --without/--only address
// them by this name (a single `--without api` would silently drop both), and
// at plan 2 the worktree_root/<wt>/<repo> layout would collide them onto the
// same directory. Reject the config at the source rather than serve
// surprising behavior.
func TestLoadNestRejectsTwoHomonymousRepos(t *testing.T) {
	denHome := t.TempDir()
	writeNest(t, denHome, "fullstack", "repos:\n  - { path: /tmp/x/api }\n  - { path: /tmp/y/api }\n")
	_, err := LoadNest(denHome, "fullstack")
	if err == nil {
		t.Fatal("expected a rejection: two repos share the basename `api`")
	}
	for _, expected := range []string{"fullstack", "api", "/tmp/x/api", "/tmp/y/api"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("error = %q, expected a mention of %q", err.Error(), expected)
		}
	}
}

// The collision must be detected AFTER expansion: two differently-written
// paths can name the same basename once `~` is resolved.
func TestLoadNestRejectsHomonymsAfterExpansion(t *testing.T) {
	denHome := t.TempDir()
	writeNest(t, denHome, "fullstack", "repos:\n  - { path: ~/dev/api }\n  - { path: /srv/api, optional: true }\n")
	if _, err := LoadNest(denHome, "fullstack"); err == nil {
		t.Fatal("expected a rejection: the two expanded paths share the basename `api`")
	}
}

func TestLoadNestMissing(t *testing.T) {
	if _, err := LoadNest(t.TempDir(), "ghost"); err == nil {
		t.Fatal("expected an error for a missing nest")
	}
}

// `den nest show ../../../../etc/passwd` used to build a path outside
// DEN_HOME. The impact is low today (local CLI, the user's own files), but at
// plan 2 this name becomes a sandbox name, a `den.nest` label, and the seed of
// the port window's hash: reject it at the source.
func TestLoadNestRefusesANameEscapingDenHome(t *testing.T) {
	root := t.TempDir()
	denHome := filepath.Join(root, "home")
	// A perfectly valid nest YAML, but OUTSIDE the den home.
	if err := os.MkdirAll(filepath.Join(denHome, "nests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "outside.yaml"), []byte("stack: devx\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// <denHome>/nests/../../outside.yaml == <root>/outside.yaml
	if _, err := LoadNest(denHome, "../../outside"); err == nil {
		t.Error("LoadNest loaded a file located outside the den home")
	}
	for _, name := range []string{"a/b", "..", "."} {
		if _, err := LoadNest(denHome, name); err == nil {
			t.Errorf("LoadNest(%q) = nil, expected a rejection", name)
		}
	}
}

func TestListNestsSortsByName(t *testing.T) {
	denHome := t.TempDir()
	writeNest(t, denHome, "web", "stack: devx\n")
	writeNest(t, denHome, "api", "stack: devx\n")
	writeNest(t, denHome, "review", "stack: devx\n")
	// a non-YAML file must not be picked up
	if err := os.WriteFile(filepath.Join(denHome, "nests", "NOTES.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	nests, broken, err := ListNests(denHome)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(broken) != 0 {
		t.Fatalf("expected no broken nests, got %v", broken)
	}
	var names []string
	for _, n := range nests {
		names = append(names, n.Name)
	}
	expected := []string{"api", "review", "web"}
	if len(names) != 3 || names[0] != expected[0] || names[1] != expected[1] || names[2] != expected[2] {
		t.Errorf("names = %v, expected %v (sorted)", names, expected)
	}
}

// TestListNestsSortDivergesFromFileOrder locks in ListNests' explicit
// sorting. os.ReadDir already returns entries sorted by FILENAME: with nest
// names that all share the same ".yaml" suffix (the TestListNestsSortsByName
// case above), file order and nest-name order always coincide, and a test
// built solely on that case would not distinguish "ListNests sorts" from
// "ReadDir already sorts" — sort.Strings could be removed without failing the
// suite. Here "web-2.yaml" and "web.yaml" diverge: '-' (0x2D) precedes '.'
// (0x2E) in ASCII, so ReadDir returns "web-2.yaml" before "web.yaml", while
// once the ".yaml" suffix is stripped, the sorted order of nest names puts
// "web" before "web-2" (shorter prefix).
func TestListNestsSortDivergesFromFileOrder(t *testing.T) {
	denHome := t.TempDir()
	writeNest(t, denHome, "web", "stack: devx\n")
	writeNest(t, denHome, "web-2", "stack: devx\n")
	writeNest(t, denHome, "api", "stack: devx\n")

	nests, broken, err := ListNests(denHome)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(broken) != 0 {
		t.Fatalf("expected no broken nests, got %v", broken)
	}
	var names []string
	for _, n := range nests {
		names = append(names, n.Name)
	}
	expected := []string{"api", "web", "web-2"}
	if len(names) != 3 || names[0] != expected[0] || names[1] != expected[1] || names[2] != expected[2] {
		t.Errorf("names = %v, expected %v (sorted by nest name, not filename)", names, expected)
	}
}

// The assertion names the offending character "_": without it, the test would
// also pass if LoadNest failed for a completely different reason, unrelated
// to the sandbox charset.
func TestLoadNestRefusesANonSandboxableName(t *testing.T) {
	denHome := t.TempDir()
	writeNest(t, denHome, "mon_api", "stack: devx\nrepos: []\n")

	_, err := LoadNest(denHome, "mon_api")
	if err == nil {
		t.Fatal("a nest name that cannot become a sandbox name must be rejected at load time")
	}
	if !strings.Contains(err.Error(), "_") {
		t.Errorf("error = %q, expected a mention of the offending character \"_\"", err.Error())
	}
}

func TestListNestsListsHealthyAndReportsBroken(t *testing.T) {
	denHome := t.TempDir()
	writeNest(t, denHome, "api", "stack: devx\nrepos: []\n")
	writeNest(t, denHome, "broken", "stack: devx\negres:\n  - typo.example.test\n")
	writeNest(t, denHome, "web", "stack: devx\nrepos: []\n")

	nests, broken, err := ListNests(denHome)
	if err != nil {
		t.Fatalf("a faulty nest must not be a structural error: %v", err)
	}

	if len(nests) != 2 || nests[0].Name != "api" || nests[1].Name != "web" {
		t.Errorf("healthy nests must be listed and sorted; got %v", namesOf(nests))
	}
	if len(broken) != 1 || broken[0].Name != "broken" {
		t.Fatalf("the faulty nest must be reported; got %v", broken)
	}
	// The diagnostic must stay actionable: file, line, key.
	msg := broken[0].Err.Error()
	for _, expected := range []string{"broken.yaml", "egres"} {
		if !strings.Contains(msg, expected) {
			t.Errorf("the diagnostic must contain %q; got: %s", expected, msg)
		}
	}
}

func TestListNestsAllHealthy(t *testing.T) {
	denHome := t.TempDir()
	writeNest(t, denHome, "api", "stack: devx\nrepos: []\n")

	nests, broken, err := ListNests(denHome)
	if err != nil || len(nests) != 1 || len(broken) != 0 {
		t.Errorf("nests=%v broken=%v err=%v", namesOf(nests), broken, err)
	}
}

func TestListNestsMissingDir(t *testing.T) {
	nests, broken, err := ListNests(t.TempDir())
	if err != nil {
		t.Errorf("a ~/.den without a nests directory is not an error: %v", err)
	}
	if len(nests) != 0 || len(broken) != 0 {
		t.Errorf("nests=%v broken=%v", nests, broken)
	}
}

// TestListNestsUnreadableRoot locks in the distinction between a broken nest
// (2nd return value) and a STRUCTURAL failure (3rd value): when the nests/
// root itself is unreadable, there is nothing to list at all, and ListNests
// must say so via an error naming the full path rather than returning a
// silent empty list.
func TestListNestsUnreadableRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("test unreliable as root: permissions are ignored")
	}
	denHome := t.TempDir()
	root := filepath.Join(denHome, "nests")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o000); err != nil {
		t.Fatal(err)
	}
	// os.Geteuid() == 0 does not cover every case (containers, filesystems
	// that also ignore permissions): verify EMPIRICALLY that the read fails
	// before asserting anything, rather than assuming 0o000 is enough on this
	// machine.
	if _, err := os.ReadDir(root); err == nil {
		if err := os.Chmod(root, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Skip("reading a 0o000 directory succeeds on this environment: test unreliable here")
	}
	t.Cleanup(func() {
		// t.TempDir() must be able to clean up after us.
		if err := os.Chmod(root, 0o755); err != nil {
			t.Fatal(err)
		}
	})

	nests, broken, err := ListNests(denHome)
	if err == nil {
		t.Fatal("expected a structural error for an unreadable nests/ root")
	}
	if nests != nil || broken != nil {
		t.Errorf("nests=%v broken=%v, expected nil on a structural failure", nests, broken)
	}
	// Contains(root) would be true by construction: os.ReadDir's raw
	// *fs.PathError already names the absolute path, wrapped or not.
	// HasPrefix on den's own prefix proves that ListNests adds ITS context
	// ("reading <root>: "), not just that the OS named the path.
	if !strings.HasPrefix(err.Error(), "reading "+root) {
		t.Errorf("error = %q, expected the prefix %q", err.Error(), "reading "+root)
	}
}

// A file literally named ".yaml" has an empty truncated name: without a
// fallback, the warning would name neither the nest nor the file, and the
// user would have no way to know which file to remove.
func TestListNestsYamlFileWithoutNameFallsBackToFilename(t *testing.T) {
	denHome := t.TempDir()
	writeNest(t, denHome, "", "stack: devx\n")

	_, broken, err := ListNests(denHome)
	if err != nil {
		t.Fatalf("a faulty nest must not be a structural error: %v", err)
	}
	if len(broken) != 1 {
		t.Fatalf("expected exactly one broken nest; got %v", broken)
	}
	if broken[0].Name == "" {
		t.Errorf("a broken nest's name must never be empty; Name=%q Err=%v", broken[0].Name, broken[0].Err)
	}
	if broken[0].Name != ".yaml" {
		t.Errorf("the name must fall back to the full filename %q; got %q", ".yaml", broken[0].Name)
	}
}

// Asking for ONE specific nest stays strict: answering "it's broken" is the
// only honest answer when that's the one named.
func TestLoadNestStaysStrict(t *testing.T) {
	denHome := t.TempDir()
	writeNest(t, denHome, "broken", "egres: [x]\n")

	if _, err := LoadNest(denHome, "broken"); err == nil {
		t.Fatal("LoadNest must stay strict on an unreadable nest")
	}
}

func namesOf(nests []*Nest) []string {
	out := make([]string, 0, len(nests))
	for _, n := range nests {
		out = append(out, n.Name)
	}
	return out
}
