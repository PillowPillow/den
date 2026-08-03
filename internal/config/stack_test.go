package config

import (
	"errors"
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

// An empty stack.yaml USED to load: name from the directory, everything else
// zero. It no longer does, and the change is deliberate — a stack with no
// `image:` has no reference to build into or spawn from, whichever way it is
// used (see LoadStack's own comment for the three directions).
//
// What this test pins is the SHAPE of that refusal, which is the part that
// matters: it is a named load error citing the file, not a crash and not a
// silently healthy stack. LoadStacks then routes it to Broken, so an empty
// draft directory still does not sink a den full of working stacks — see
// TestLoadStacksABrokenStackDoesNotHideTheHealthyOnes.
func TestLoadStackRefusesAnEmptyFile(t *testing.T) {
	denHome := t.TempDir()
	writeStack(t, denHome, "devx", "")
	_, err := LoadStack(denHome, "devx")
	if err == nil {
		t.Fatal("LoadStack accepted a stack.yaml declaring nothing at all")
	}
	for _, want := range []string{`"devx"`, "image:", filepath.Join("stacks", "devx", "stack.yaml")} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q — it must name the fault and the file", err, want)
		}
	}
}

// THE closed diagnostic loop this whole branch exists to eliminate, caught one
// stage earlier than the loop itself: a BUILDABLE stack with no `image:` used
// to build in full, run `sbx template save <n>-build ""`, and report success —
// after which `den <nest>` demanded the `den build` that had just succeeded.
// den owning `template save` makes the name correct by construction; only this
// refusal makes it non-empty.
func TestLoadStackRefusesABuildableStackWithNoImage(t *testing.T) {
	home := t.TempDir()
	writeStack(t, home, "devx", "base: claude\nprovision:\n  steps: [./provision/go.sh]\n")
	_, err := LoadStack(home, "devx")
	if err == nil {
		t.Fatal("LoadStack accepted a buildable stack with no image: to save into")
	}
	for _, want := range []string{`"devx"`, "image:", filepath.Join("stacks", "devx", "stack.yaml")} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// UNCONDITIONAL, and this is the case that decides it: gating the check on
// Buildable() — the shape the two sibling refusals below take — would let a
// PULLABLE stack with no `image:` through. It then hands any child declaring
// `parent:` an empty ParentImage, which build.CreateArgv reads as "root stack"
// and refuses with "no origin — declare `base:` or `parent:`" naming the
// CHILD's stack.yaml. That file already declares `parent:`: the wrong file, and
// a remedy the user has already applied. Verified against build.CreateArgv on
// 2026-08-03 before choosing the unconditional form.
func TestLoadStackRefusesAPullableStackWithNoImage(t *testing.T) {
	home := t.TempDir()
	writeStack(t, home, "base", "kit: ./kit\n") // no provision.steps: not buildable
	_, err := LoadStack(home, "base")
	if err == nil {
		t.Fatal("LoadStack accepted a pullable stack with no image: — a `parent:` of it would " +
			"then be refused over ITS own file")
	}
	if !strings.Contains(err.Error(), "image:") {
		t.Errorf("error %q does not name the missing key", err)
	}
}

// Whitespace is not a reference. sbx.CreateArgv guards the same question with
// the same TrimSpace, and two guards on one question must not disagree: an
// `image: "  "` that LoadStack let through would reach `sbx template save` as
// a blank argument.
func TestLoadStackRefusesAWhitespaceOnlyImage(t *testing.T) {
	home := t.TempDir()
	writeStack(t, home, "devx", "image: \"   \"\nbase: claude\nprovision:\n  steps: [./go.sh]\n")
	if _, err := LoadStack(home, "devx"); err == nil {
		t.Fatal("LoadStack accepted `image: \"   \"` as a reference")
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

// The same verdict, machine-readable. A caller that has to ACT on the
// difference — `den build`, whose refusal for a broken `parent:` must name the
// parent's file and not the child's — would otherwise match on the word
// "unreadable", i.e. on a message anyone is free to reword.
func TestStacksGetUnreadableVerdictIsTyped(t *testing.T) {
	denHome := t.TempDir()
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	writeStack(t, denHome, "other", "image: other:v1\nimag: typo\n")
	stacks, err := LoadStacks(denHome)
	if err != nil {
		t.Fatal(err)
	}

	var unreadable *UnreadableStackError
	_, errBroken := stacks.Get("other")
	if !errors.As(errBroken, &unreadable) {
		t.Fatalf("a broken stack must give an *UnreadableStackError; got %T: %v", errBroken, errBroken)
	}
	if unreadable.Name != "other" {
		t.Errorf("Name = %q, want the stack that does not load", unreadable.Name)
	}
	// The cause stays reachable, and it is the one LoadStacks recorded: doctor
	// and den build both relay it to name the file to fix.
	if !strings.Contains(unreadable.Unwrap().Error(), filepath.Join("stacks", "other", "stack.yaml")) {
		t.Errorf("Unwrap() = %v, want LoadStack's failure, which cites the file", unreadable.Unwrap())
	}

	// And "not found" is NOT that type: the two fixes stay distinguishable
	// without reading either message.
	_, errMissing := stacks.Get("never-declared")
	if errors.As(errMissing, &unreadable) {
		t.Errorf("a stack that does not exist must not read as unreadable; got: %v", errMissing)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Buildability is read off `provision.steps` and nothing else. It is the SOLE
// source of the verdict, consumed by `den build` and by the spawn's image
// check, which spec §6 requires to agree.
func TestStackBuildableFollowsProvisionSteps(t *testing.T) {
	var pullable Stack
	if pullable.Buildable() {
		t.Error("a stack with no provision.steps is not buildable — its image: is one sbx pulls")
	}
	buildable := Stack{Provision: Provision{Steps: []string{"./provision/go.sh"}}}
	if !buildable.Buildable() {
		t.Error("a stack declaring provision.steps is buildable")
	}
}

// The paths follow the rule `kit`/`kits` already follow: relative in YAML,
// absolute after loading, resolved against the STACK directory — not against
// the directory the user happened to run den from.
func TestLoadStackResolvesProvisionPathsAgainstTheStackDirectory(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "stacks", "devx")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "stack.yaml"), `
image: devx:v1
base: claude
provision:
  includes:
    - ../../lib/common.sh
  steps:
    - ./provision/go-tools.sh
`)
	s, err := LoadStack(home, "devx")
	if err != nil {
		t.Fatalf("LoadStack: %v", err)
	}
	wantInclude := filepath.Join(home, "lib", "common.sh")
	if got := s.Provision.Includes[0]; got != wantInclude {
		t.Errorf("includes[0] = %q, want %q", got, wantInclude)
	}
	wantStep := filepath.Join(dir, "provision", "go-tools.sh")
	if got := s.Provision.Steps[0]; got != wantStep {
		t.Errorf("steps[0] = %q, want %q", got, wantStep)
	}
}

// Two origins for one image is a contradiction, not a precedence to arbitrate:
// den refuses rather than silently preferring one.
func TestLoadStackRefusesBaseAndParentTogether(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "stacks", "devx")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "stack.yaml"), `
image: devx:v1
base: claude
parent: other
provision:
  steps: [./provision/go.sh]
`)
	_, err := LoadStack(home, "devx")
	if err == nil {
		t.Fatal("LoadStack accepted both base: and parent: — want a refusal")
	}
	for _, want := range []string{"base:", "parent:", "stack.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q — it must name the fault and the file", err, want)
		}
	}
}

// A buildable stack with NO origin cannot be built: den does not know what to
// start from. The message offers both remedies, because which one is right
// depends on whether the stack is a root.
func TestLoadStackRefusesBuildableStackWithNoOrigin(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "stacks", "devx")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "stack.yaml"), `
image: devx:v1
provision:
  steps: [./provision/go.sh]
`)
	_, err := LoadStack(home, "devx")
	if err == nil {
		t.Fatal("LoadStack accepted provision.steps with neither base: nor parent:")
	}
	for _, want := range []string{"base:", "parent:"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not offer the remedy %q", err, want)
		}
	}
}

// The mirror case, and the one a naive validation breaks: a stack with no
// provision.steps declares no origin BY DESIGN — its image: is one sbx pulls.
// Demanding an origin of it would refuse a configuration §6 calls legitimate.
func TestLoadStackAcceptsAPullableStackWithNoOrigin(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "stacks", "pulled")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "stack.yaml"), "image: ghcr.io/acme/base:v3\n")
	s, err := LoadStack(home, "pulled")
	if err != nil {
		t.Fatalf("LoadStack refused a pullable stack: %v", err)
	}
	if s.Buildable() {
		t.Error("a stack with no provision.steps must not be buildable")
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
