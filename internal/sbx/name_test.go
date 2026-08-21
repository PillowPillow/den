package sbx

import (
	"strings"
	"testing"
)

func TestSandboxName(t *testing.T) {
	cases := []struct {
		nest, worktree, want string
	}{
		{"api", "", "api"},
		{"api", "feat12", "api.feat12"},
		{"my-api", "feat", "my-api.feat"},
	}
	for _, c := range cases {
		got, err := SandboxName(c.nest, c.worktree)
		if err != nil {
			t.Errorf("SandboxName(%q,%q): unexpected error %v", c.nest, c.worktree, err)
			continue
		}
		if got != c.want {
			t.Errorf("SandboxName(%q,%q) = %q, want %q", c.nest, c.worktree, got, c.want)
		}
	}
}

// wantKind locks down WHICH component was rejected: swapping the arguments
// inside SandboxName (e.g. validating the worktree with kind "nest") would
// let a test that only checks for an error's presence pass, while handing the
// caller a misleading message.
func TestSandboxNameRejectsIllegalComponents(t *testing.T) {
	cases := []struct{ nest, worktree, wantKind string }{
		{"my.api", "feat", "nest"},         // dot in the nest
		{"api", "feature/123", "worktree"}, // slash in the worktree (real case: branch name)
		{"api", "feat.12", "worktree"},     // dot in the worktree
		{"", "feat", "nest"},               // empty nest
	}
	for _, c := range cases {
		_, err := SandboxName(c.nest, c.worktree)
		if err == nil {
			t.Errorf("SandboxName(%q,%q) must fail", c.nest, c.worktree)
			continue
		}
		if !strings.Contains(err.Error(), c.wantKind) {
			t.Errorf("SandboxName(%q,%q): error %q must mention kind %q",
				c.nest, c.worktree, err.Error(), c.wantKind)
		}
	}
}

// The round trip is the central invariant: without --label, the name IS the state.
func TestSplitNameIsTheExactInverse(t *testing.T) {
	cases := []struct{ nest, worktree string }{
		{"api", ""},
		{"api", "feat12"},
		{"my-api", "feat-2"},
		{"a-b", "c-d"},
	}
	for _, c := range cases {
		name, err := SandboxName(c.nest, c.worktree)
		if err != nil {
			t.Fatalf("SandboxName(%q,%q): %v", c.nest, c.worktree, err)
		}
		nest, wt := SplitName(name)
		if nest != c.nest || wt != c.worktree {
			t.Errorf("round trip of %q: got (%q,%q), want (%q,%q)",
				name, nest, wt, c.nest, c.worktree)
		}
	}
}

// A sandbox created by hand outside den must not make the split panic.
func TestSplitNameForeign(t *testing.T) {
	nest, wt := SplitName("sandbox-created-by-hand")
	if nest != "sandbox-created-by-hand" || wt != "" {
		t.Errorf("got (%q,%q)", nest, wt)
	}
	// Two dots: only the first separates, the rest belongs to the worktree —
	// den never produces this, but den ls must stay total.
	nest, wt = SplitName("a.b.c")
	if nest != "a" || wt != "b.c" {
		t.Errorf("got (%q,%q), want (a, b.c)", nest, wt)
	}
}

// ValidateSandboxName is the single source of truth for "is this name one den
// would have built?". It is exported so that internal/agent and argv
// assembly cannot diverge: they did diverge, and the "api." case passed on
// one side and not the other.
func TestValidateSandboxName(t *testing.T) {
	// "a" left this list on 2026-08-21: sbx refuses any name under two
	// characters. "a.b" replaces it — a one-character COMPONENT is still
	// legal, the floor being on the assembled name. See MinNameLength.
	for _, name := range []string{"api", "api.feat12", "my-api.feat-2", "api2", "a.b"} {
		if err := ValidateSandboxName(name); err != nil {
			t.Errorf("%q must be accepted: %v", name, err)
		}
	}

	// Non-canonical forms: they would rebuild into something other than
	// themselves, so two distinct names would designate the same sandbox.
	rejected := []string{
		"",             // empty
		"api.",         // rebuilds to "api" — the hole in component validation
		".feat12",      // empty nest
		"api..feat",    // worktree ".feat", leading separator
		"api.feat.sup", // the separator does not repeat
		"my_api",       // charset
		"-api",         // indistinguishable from a flag
	}
	for _, name := range rejected {
		if err := ValidateSandboxName(name); err == nil {
			t.Errorf("%q must be rejected", name)
		}
	}
}

// The property that holds it all together: ValidateSandboxName accepts
// EXACTLY the image of SandboxName, no more and no less.
func TestValidateSandboxNameAcceptsTheImageOfSandboxName(t *testing.T) {
	for _, nest := range []string{"api", "my-api", "a1", "x+y"} {
		for _, worktree := range []string{"", "feat", "feat-2", "my_wt", "-wt", ""} {
			name, err := SandboxName(nest, worktree)
			if err != nil {
				continue // SandboxName already rejected: nothing to cross-check
			}
			if err := ValidateSandboxName(name); err != nil {
				t.Errorf("SandboxName(%q,%q) = %q but ValidateSandboxName rejects it: %v",
					nest, worktree, name, err)
			}
		}
	}
}

// MEASURED on sbx v0.38.0, 2026-08-21: `sbx create --name a` answers
// `name must match regexp ^[a-zA-Z0-9][a-zA-Z0-9.-]+$`, and `--name ab` is
// accepted. den used to build the refused name for `den up a`.
func TestSandboxNameRefusesANameShorterThanSbxAccepts(t *testing.T) {
	_, err := SandboxName("a", "")
	if err == nil {
		t.Fatal("expected a refusal on a one-character sandbox name")
	}
	for _, want := range []string{`"a"`, "--as", "-w"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to contain %q", err, want)
		}
	}
}

// The floor is on the WHOLE name, never on a component: "a.b" is three
// characters and sbx takes it. Enforcing it per component would refuse a
// legal name.
func TestSandboxNameAcceptsAShortComponentInALongEnoughName(t *testing.T) {
	name, err := SandboxName("a", "b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "a.b" {
		t.Errorf("name = %q, want %q", name, "a.b")
	}
}

// The plus sign left the charset on 2026-08-21: sbx answers `sandbox name
// cannot contain plus signs`. It was reachable through -w without anyone
// typing one — `feat/c++` flattened to `feat-c++`.
func TestSandboxNameRefusesThePlusSign(t *testing.T) {
	if _, err := SandboxName("api", "feat-c++"); err == nil {
		t.Fatal("expected a refusal on a plus sign in a sandbox name")
	}
}
