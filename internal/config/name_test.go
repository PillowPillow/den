package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateNameAcceptsOrdinaryNames(t *testing.T) {
	for _, name := range []string{"devx", "dgdevx", "web-2", "front_app", "api.v2"} {
		if err := ValidateName("stack", name); err != nil {
			t.Errorf("ValidateName(%q) = %v, want accepted", name, err)
		}
	}
}

func TestValidateNameRejectsWhatEscapesDenHome(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"", "empty"},
		{"../../../../etc/passwd", "separator"},
		{"a/b", "separator"},
		{"..", "reserved"},
		{".", "reserved"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateName("nest", c.name)
			if err == nil {
				t.Fatalf("ValidateName(%q) = nil, want a rejection", c.name)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, expected a mention of %q", err.Error(), c.want)
			}
			if !strings.Contains(err.Error(), "nest") {
				t.Errorf("error = %q, expected a mention of the object kind", err.Error())
			}
		})
	}
}

func TestValidateSandboxComponent(t *testing.T) {
	valid := []string{"api", "my-api", "api2", "A-B"}
	for _, name := range valid {
		if err := ValidateSandboxComponent("nest", name); err != nil {
			t.Errorf("%q must be accepted, refused with: %v", name, err)
		}
	}

	// The period is reserved for the <nest>.<worktree> separator; underscore
	// and slash are refused by `sbx create --name` itself. A name starting
	// with "-" is indistinguishable from a flag both for sbx and for den
	// (`den nest show -api` fails on an unknown flag before ever reaching nest
	// resolution): -api, --, - are therefore all refused.
	//
	// "v1+beta" is in this list since 2026-08-21, and it used to be in the
	// valid one: sbx answers `sandbox name cannot contain plus signs`
	// (measured on v0.38.0). See sandboxComponentChars.
	invalid := []string{
		"", "my.api", "my_api", "feature/123", "my api", "café",
		"-api", "--", "+", "-", "v1+beta", "c++",
	}
	for _, name := range invalid {
		if err := ValidateSandboxComponent("nest", name); err == nil {
			t.Errorf("%q must be refused", name)
		}
	}
}

// The message must name the offending character: "invalid" without saying
// what forces the user to guess.
func TestValidateSandboxComponentMessageIsActionable(t *testing.T) {
	err := ValidateSandboxComponent("worktree", "feature/123")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"worktree", "feature/123", "/"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message must contain %q; got: %v", want, err)
		}
	}
}

func TestFlattenSandboxComponent(t *testing.T) {
	cases := []struct{ name, want string }{
		{"feature/123", "feature-123"}, // the real case: a branch name
		{"feat/trial", "feat-trial"},
		{"my_wt", "my-wt"},
		{"feat.12", "feat-12"}, // the period is the <nest>.<worktree> separator
		{"café", "caf-"},
		{"feat//x", "feat--x"}, // no merging of runs: one character, one dash
		{"feat12", "feat12"},   // already valid: rendered as-is
		// The plus left the charset on 2026-08-21 (sbx: "sandbox name cannot
		// contain plus signs"), so it is now flattened like any other
		// out-of-charset rune. It used to survive untouched.
		{"v1+beta", "v1-beta"},
		{"A-B", "A-B"},
	}
	for _, c := range cases {
		got, err := FlattenSandboxComponent("worktree", c.name)
		if err != nil {
			t.Errorf("FlattenSandboxComponent(%q): unexpected error %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("FlattenSandboxComponent(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

// The invariant that justifies this function's existence: what it returns is
// always acceptable to ValidateSandboxComponent. That's what lets the rest of
// den — sbx.SandboxName, `sbx create`'s argv, policy — stay STRICT: flattening
// lives upstream of them, it doesn't loosen them.
func TestFlattenSandboxComponentAlwaysReturnsAValidComponent(t *testing.T) {
	for _, name := range []string{
		"feature/123", "my_wt", "feat.12", "café", "feat//x", "a b", "wt!", "é1",
		"api", "x", strings.Repeat("a/b", 20),
	} {
		got, err := FlattenSandboxComponent("worktree", name)
		if err != nil {
			continue // refused: nothing to cross-check
		}
		if err := ValidateSandboxComponent("worktree", got); err != nil {
			t.Errorf("FlattenSandboxComponent(%q) = %q, which ValidateSandboxComponent refuses: %v",
				name, got, err)
		}
	}
}

// Corollary of the previous test, stated separately because it's the one
// protecting worktree.Path: a component that kept a path separator would make
// the worktree land in a subdirectory — or outside worktree_root entirely.
func TestFlattenSandboxComponentNeverReturnsAPathSeparator(t *testing.T) {
	for _, name := range []string{"feature/123", "../evade", "a/b/c", `a\b`} {
		got, err := FlattenSandboxComponent("worktree", name)
		if err != nil {
			continue
		}
		if strings.ContainsRune(got, '/') || strings.ContainsRune(got, filepath.Separator) {
			t.Errorf("FlattenSandboxComponent(%q) = %q: contains a path separator", name, got)
		}
	}
}

// Flattening is not invention: a name whose first character isn't
// alphanumeric is REFUSED, never silently prefixed. The user finds the name
// they typed in the message, not one den chose for them.
func TestFlattenSandboxComponentRefusesWhatItCannotFlatten(t *testing.T) {
	for _, name := range []string{"", "-wip", "/x", "_x", ".hidden"} {
		if got, err := FlattenSandboxComponent("worktree", name); err == nil {
			t.Errorf("FlattenSandboxComponent(%q) = %q, want a refusal", name, got)
		}
	}
}

// A misplaced name ("-api") has no forbidden character: "-" is allowed
// elsewhere in the name. Saying "the character '-' is not allowed" would
// therefore be wrong; the message must talk about position, not charset.
func TestValidateSandboxComponentMessageFirstCharacter(t *testing.T) {
	err := ValidateSandboxComponent("nest", "-api")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"nest", "-api"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message must contain %q; got: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "is not allowed") {
		t.Errorf(
			"\"-api\" must not say that \"-\" is not allowed, it's just misplaced; got: %v",
			err.Error())
	}
}
