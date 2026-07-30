package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeStack creates <denHome>/stacks/<name>/stack.yaml and returns denHome.
func writeStack(t *testing.T, denHome, name, content string) string {
	t.Helper()
	dir := filepath.Join(denHome, "stacks", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stack.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return denHome
}

func TestLoadStack(t *testing.T) {
	denHome := t.TempDir()
	writeStack(t, denHome, "dgdevx", `
image: dgdevx:v1
parent: devx
kit: ./kit
egress:
  - gitlab.digitaleo.com
`)

	s, err := LoadStack(denHome, "dgdevx")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name != "dgdevx" || s.Image != "dgdevx:v1" || s.Parent != "devx" {
		t.Errorf("stack = %+v", s)
	}
	if want := filepath.Join(denHome, "stacks", "dgdevx"); s.Dir != want {
		t.Errorf("Dir = %q, want %q", s.Dir, want)
	}
	if want := filepath.Join(denHome, "stacks", "dgdevx", "kit"); s.Kit != want {
		t.Errorf("Kit = %q, want an absolute path %q", s.Kit, want)
	}
	if len(s.Egress) != 1 || s.Egress[0] != "gitlab.digitaleo.com" {
		t.Errorf("Egress = %v", s.Egress)
	}
}

// A stack's name is its directory name — the sole, nominal case.
func TestLoadStackNameComesFromTheDirectory(t *testing.T) {
	denHome := t.TempDir()
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	s, err := LoadStack(denHome, "devx")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name != "devx" {
		t.Errorf("Name = %q, want %q derived from the directory", s.Name, "devx")
	}
}

// Same rule as for nests: a stack doesn't carry its name in its content.
// LoadStacks indexes its map by this directory name, while LoadStack looks up
// by directory name too — two keys for one object must never diverge.
func TestLoadStackRejectsANameInTheContent(t *testing.T) {
	denHome := t.TempDir()
	writeStack(t, denHome, "devx", "name: other\nimage: devx:v1\n")
	_, err := LoadStack(denHome, "devx")
	if err == nil {
		t.Fatal("expected a rejection: a stack's identity comes from its directory, not its content")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error = %q, expected a mention of the `name` key", err.Error())
	}
}

// LoadStacks must index by directory name, the only identity that exists:
// it's this key that resolves defaults.stack and nest.stack.
func TestLoadStacksIndexesByDirectoryName(t *testing.T) {
	denHome := t.TempDir()
	writeStack(t, denHome, "devx", "image: devx:v1\n")

	stacks, err := LoadStacks(denHome)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s, ok := stacks.Healthy["devx"]
	if !ok {
		t.Fatalf("stacks = %v, expected an entry under the directory name %q", stacks.Healthy, "devx")
	}
	if s.Name != "devx" {
		t.Errorf("Name = %q, want %q", s.Name, "devx")
	}
}

func TestLoadStackRejectsAnUnknownKey(t *testing.T) {
	denHome := t.TempDir()
	writeStack(t, denHome, "devx", "image: devx:v1\negres: [github.com]\n")
	_, err := LoadStack(denHome, "devx")
	if err == nil {
		t.Fatal("expected an error on the unknown key `egres`")
	}
	if !strings.Contains(err.Error(), "egres") {
		t.Errorf("error = %q, expected a mention of the offending key", err.Error())
	}
}

func TestLoadStackEmptyFile(t *testing.T) {
	denHome := t.TempDir()
	writeStack(t, denHome, "devx", "")
	s, err := LoadStack(denHome, "devx")
	if err != nil {
		t.Fatalf("an empty stack.yaml must not be a load error: %v", err)
	}
	if s.Name != "devx" {
		t.Errorf("Name = %q, want %q derived from the directory", s.Name, "devx")
	}
}

// A missing stack and an unreadable stack call for two different fixes:
// "declare it" versus "fix the permissions". `doctor` relays this message
// verbatim, it must decide which.
func TestLoadStackMissing(t *testing.T) {
	denHome := t.TempDir()
	_, err := LoadStack(denHome, "ghost")
	if err == nil {
		t.Fatal("expected an error for a missing stack")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, expected an explicit not-found message", err.Error())
	}
	if !strings.Contains(err.Error(), filepath.Join(denHome, "stacks", "ghost")) {
		t.Errorf("error = %q, expected the stack's expected path", err.Error())
	}
}

func TestLoadStackUnreadable(t *testing.T) {
	denHome := t.TempDir()
	// stack.yaml present but unreadable (here: it's a directory) — this is not
	// a missing stack, and the message must not claim it is.
	if err := os.MkdirAll(filepath.Join(denHome, "stacks", "devx", "stack.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := LoadStack(denHome, "devx")
	if err == nil {
		t.Fatal("expected an error for an unreadable stack")
	}
	if strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q: the stack exists, it's unreadable", err.Error())
	}
	if !strings.Contains(err.Error(), "read") {
		t.Errorf("error = %q, expected a read-error message", err.Error())
	}
}

func TestLoadStackRejectsANameThatEscapesDenHome(t *testing.T) {
	root := t.TempDir()
	denHome := filepath.Join(root, "home")
	// A perfectly valid stack, but OUTSIDE the den home.
	writeStack(t, root, "outside", "image: outside:v1\n")

	// <denHome>/stacks/../../stacks/outside == <root>/stacks/outside
	if _, err := LoadStack(denHome, "../../stacks/outside"); err == nil {
		t.Error("LoadStack loaded a stack located outside the den home")
	}
	if _, err := LoadStack(denHome, ".."); err == nil {
		t.Error(`LoadStack("..") = nil, want a rejection`)
	}
}

func TestLoadStacksAll(t *testing.T) {
	denHome := t.TempDir()
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	writeStack(t, denHome, "dgdevx", "image: dgdevx:v1\nparent: devx\n")
	// a directory without a stack.yaml must be silently ignored, not crash
	if err := os.MkdirAll(filepath.Join(denHome, "stacks", "draft"), 0o755); err != nil {
		t.Fatal(err)
	}

	stacks, err := LoadStacks(denHome)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stacks.Healthy) != 2 {
		t.Fatalf("expected 2 stacks, got %d: %v", len(stacks.Healthy), stacks.Healthy)
	}
	if stacks.Healthy["dgdevx"].Parent != "devx" {
		t.Errorf("parent of dgdevx = %q", stacks.Healthy["dgdevx"].Parent)
	}
}

func TestLoadStacksMissingDirectory(t *testing.T) {
	// No stacks/ directory: not an error, just an empty den.
	stacks, err := LoadStacks(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stacks.Healthy) != 0 {
		t.Errorf("expected 0 stacks, got %d", len(stacks.Healthy))
	}
}

func TestLoadStackResolvesCrossCuttingKits(t *testing.T) {
	denHome := t.TempDir()
	writeStack(t, denHome, "devx", `image: devx:v1
kit: ./kit
kits:
  - ../../kits/ssh-known-hosts
  - /already/absolute
`)

	s, err := LoadStack(denHome, "devx")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{
		filepath.Join(denHome, "kits", "ssh-known-hosts"),
		"/already/absolute",
	}
	if len(s.Kits) != len(want) {
		t.Fatalf("Kits = %v, want %d entries", s.Kits, len(want))
	}
	for i, a := range want {
		if s.Kits[i] != a {
			t.Errorf("Kits[%d] = %q, want %q", i, s.Kits[i], a)
		}
	}
}

// The order is a LAYERING order: sorting it would break the semantics.
func TestLoadStackPreservesKitOrder(t *testing.T) {
	denHome := t.TempDir()
	writeStack(t, denHome, "devx", `image: devx:v1
kits: [./z-dernier, ./a-premier]
`)

	s, err := LoadStack(denHome, "devx")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filepath.Base(s.Kits[0]) != "z-dernier" || filepath.Base(s.Kits[1]) != "a-premier" {
		t.Errorf("the declared order must be preserved; got %v", s.Kits)
	}
}

// DeclaredKits is the SOLE SOURCE of "which kits, in what order": both
// doctor and the spawn path consume it. Its two properties are therefore
// tested here, at the source, not twice at the consumers.
func TestStackDeclaredKits(t *testing.T) {
	cases := []struct {
		name  string
		stack Stack
		want  []string
	}{
		{
			// The LAYERING order: `kits:` (cross-cutting) then `kit:`. The
			// mixin is appended afterward by sbx.CreateArgv and must stay last.
			"layering order: kits: then kit:",
			Stack{Kits: []string{"/k/transverse", "/k/other"}, Kit: "/k/devx"},
			[]string{"/k/transverse", "/k/other", "/k/devx"},
		},
		{
			// The round-2 fix's regression: the filter only covered the
			// singular `kit:`, never an empty entry INSIDE `kits:`.
			"empty entry in kits: (plural)",
			Stack{Kits: []string{"", "/k/transverse", ""}, Kit: "/k/devx"},
			[]string{"/k/transverse", "/k/devx"},
		},
		{
			"singular kit: empty",
			Stack{Kits: []string{"/k/transverse"}, Kit: ""},
			[]string{"/k/transverse"},
		},
		{
			// A stack without a kit is valid (spec §4.2): an empty slice, not
			// a tricky nil nor a phantom entry.
			"no kit declared",
			Stack{},
			[]string{},
		},
		{
			"only empty entries",
			Stack{Kits: []string{"", ""}, Kit: ""},
			[]string{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.stack.DeclaredKits()
			if !slices.Equal(got, c.want) {
				t.Errorf("DeclaredKits() = %v, want %v", got, c.want)
			}
		})
	}
}

// DeclaredKits must not write into the stack it reads: it's called twice on
// the spawn path (existence check, then argv), and aliasing the underlying
// slice would make the second call diverge from the first.
func TestStackDeclaredKitsDoesNotMutateTheStack(t *testing.T) {
	s := Stack{Kits: []string{"/k/a", "/k/b"}, Kit: "/k/c"}
	first := s.DeclaredKits()
	first[0] = "/k/OVERWRITTEN"

	if !slices.Equal(s.Kits, []string{"/k/a", "/k/b"}) {
		t.Errorf("Kits mutated by the caller: %v", s.Kits)
	}
	if second := s.DeclaredKits(); !slices.Equal(second, []string{"/k/a", "/k/b", "/k/c"}) {
		t.Errorf("second call = %v, want identical to the first", second)
	}
}

func TestLoadStackWithoutKits(t *testing.T) {
	denHome := t.TempDir()
	writeStack(t, denHome, "devx", "image: devx:v1\n")

	s, err := LoadStack(denHome, "devx")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.Kits) != 0 {
		t.Errorf("Kits = %v, want empty", s.Kits)
	}
}

// A broken stack does NOT hide the healthy stacks.
//
// Before this, LoadStack would fail and LoadStacks would propagate the error
// in bulk: a typo in a stack nobody uses would fail `den <nest>` and
// `den nest show` on a nest referencing a perfectly healthy stack. This is
// the same doctrine ListNests applies to nests.
func TestLoadStacksABrokenStackDoesNotHideTheHealthyOnes(t *testing.T) {
	denHome := t.TempDir()
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	writeStack(t, denHome, "other", "image: other:v1\nimag: typo\n")

	stacks, err := LoadStacks(denHome)
	if err != nil {
		t.Fatalf("a broken stack must not fail the load: %v", err)
	}
	if stacks.Healthy["devx"] == nil {
		t.Fatalf("the healthy stack devx has disappeared; healthy = %v", stacks.Names())
	}
	if len(stacks.Broken) != 1 || stacks.Broken[0].Name != "other" {
		t.Fatalf("expected exactly the stack \"other\" as broken; got %+v", stacks.Broken)
	}
	// The cause stays attached: without it, doctor couldn't say WHAT to fix.
	if !strings.Contains(stacks.Broken[0].Err.Error(), "imag") {
		t.Errorf("the broken stack's error must name the offending key; got: %v",
			stacks.Broken[0].Err)
	}
	// And a broken stack is NOT marked healthy: otherwise spawn would take it
	// for good and proceed with a zero-value Stack (empty image).
	if _, ok := stacks.Healthy["other"]; ok {
		t.Error("a broken stack must not appear among the healthy ones")
	}
}

// Get must say WHICH of the two situations applies: "unreadable" and "not
// found" don't get fixed the same way. Answering "not found" for a stack that
// exists would send the user to create a file they already have.
func TestStacksGetDistinguishesUnreadableFromMissing(t *testing.T) {
	denHome := t.TempDir()
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	writeStack(t, denHome, "other", "image: other:v1\nimag: typo\n")
	stacks, err := LoadStacks(denHome)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := stacks.Get("devx"); err != nil {
		t.Errorf("a healthy stack must resolve: %v", err)
	}

	_, errBroken := stacks.Get("other")
	if errBroken == nil {
		t.Fatal("a broken stack must not resolve")
	}
	if !strings.Contains(errBroken.Error(), "unreadable") {
		t.Errorf("broken stack: message must say \"unreadable\", not \"not found\"; got: %v", errBroken)
	}

	_, errMissing := stacks.Get("never-declared")
	if errMissing == nil {
		t.Fatal("a missing stack must not resolve")
	}
	if !strings.Contains(errMissing.Error(), "not found") {
		t.Errorf("missing stack: message must say \"not found\"; got: %v", errMissing)
	}
	// The suggested stacks are the HEALTHY ones: proposing a broken stack as
	// a fallback would send the user straight into a wall.
	if strings.Contains(errMissing.Error(), "other") {
		t.Errorf("the list of declared stacks must only propose healthy stacks; got: %v", errMissing)
	}
}

// The stacks directory only has value on "not found", and it must land on
// the RIGHT line.
//
// yaml.v3's error is multi-line. Appending "(in <D>/stacks)" behind it — what
// the caller used to do — reads as if "line 2" were located inside
// <D>/stacks, and is redundant besides: the broken stack.yaml's full path is
// already cited two lines above. On "not found", by contrast, it's the only
// indication of where to create the missing stack.
func TestStacksGetOnlyLocatesMissingStacks(t *testing.T) {
	denHome := t.TempDir()
	writeStack(t, denHome, "devx", "image: devx:v1\nimag: typo\n")
	stacks, err := LoadStacks(denHome)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(denHome, "stacks")

	// Broken: the offending stack.yaml is already named, nothing should be
	// appended after yaml.v3's multi-line diagnostic.
	_, errBroken := stacks.Get("devx")
	if errBroken == nil {
		t.Fatal("a broken stack must not resolve")
	}
	if strings.Contains(errBroken.Error(), root+")") {
		t.Errorf("the stacks directory is appended behind a multi-line YAML error, "+
			"where it reads as the location of the last line; got:\n%s", errBroken)
	}
	// The offending file's path, though, must indeed be there.
	if !strings.Contains(errBroken.Error(), filepath.Join(root, "devx", "stack.yaml")) {
		t.Errorf("message must name the offending stack.yaml; got: %s", errBroken)
	}

	// Missing: there, the directory is the only useful indication.
	_, errMissing := stacks.Get("never-declared")
	if errMissing == nil {
		t.Fatal("a missing stack must not resolve")
	}
	if !strings.Contains(errMissing.Error(), root) {
		t.Errorf("message must say WHERE the stack is expected; got: %s", errMissing)
	}
}
