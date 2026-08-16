package sbx

import "testing"

// NormalizeNetworkResource is the sole definition of how sbx stores a network
// rule, shared by Machine and by internal/converge's inspection. The case that
// matters most is the one no source in this repository exercises: an EXPLICIT
// port must survive untouched, or den would match `foo.example:8080` against a
// rule for `foo.example:443` and report a host as allowed that is not.
func TestNormalizeNetworkResource(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"cdn.playwright.dev", "cdn.playwright.dev:443"},
		{"acli.atlassian.com", "acli.atlassian.com:443"},
		// An explicit port is what the source MEANT: never rewritten, and never
		// silently widened to the default.
		{"foo.example:8080", "foo.example:8080"},
		{"gitlab.digitaleo.com:4567", "gitlab.digitaleo.com:4567"},
		// Wildcards are hosts like any other here — sbx matches them, den only
		// compares the string.
		{"*.digitaleo.com", "*.digitaleo.com:443"},
		// IPv6: the brackets are what separates an address from a port, and a
		// bare address is full of colons that are not one.
		{"[::1]", "[::1]:443"},
		{"[::1]:8080", "[::1]:8080"},
		{"::1", "::1"},
	} {
		if got := NormalizeNetworkResource(tc.in); got != tc.want {
			t.Errorf("NormalizeNetworkResource(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// github is interactive on sbx's side (measured 2026-08-16, `sbx secret set
// --help` on v0.38.0): a Run leaves stdin nil, exactly like the real
// Exec.Run, so the double must fail it the same way the real command would —
// not the bare state flip it used to answer with, which is what let
// converge/sbx.go dispatch the credential through the wrong method for a
// whole review cycle without a single test catching it.
func TestMachineRefusesTheGithubCredentialThroughRun(t *testing.T) {
	m := NewMachine()
	if _, err := m.Run(t.Context(), "secret", "set", "-g", "github"); err == nil {
		t.Fatal("Run must fail: its stdin is nil, and sbx's prompt would read EOF")
	}
	if m.Services["github"] {
		t.Error("a failed Run must not have configured the credential")
	}
}

// Attach is the one method that wires a real terminal (runner.go's own doc),
// so it is the only method allowed to complete the credential the way a human
// at the keyboard would — and Machine records that it went through Attach
// specifically, not just that SOME method with this argv ran.
func TestMachineConfiguresTheGithubCredentialThroughAttach(t *testing.T) {
	m := NewMachine()
	if err := m.Attach(t.Context(), "secret", "set", "-g", "github"); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if !m.Services["github"] {
		t.Error("Attach must configure the credential")
	}
	if !m.HasAttached("secret", "set", "-g", "github") {
		t.Errorf("expected the call recorded as an attach; attaches: %v", m.Attaches)
	}
}

// And the driver-facing consequence: a declared host carrying an explicit port
// is NOT satisfied by a rule for the default port. The machine records what it
// was told, so this reads as the round trip den performs.
func TestMachineStoresAnExplicitPortAsGiven(t *testing.T) {
	m := NewMachine()
	if _, err := m.Run(t.Context(), "policy", "allow", "network", "foo.example:8080"); err != nil {
		t.Fatal(err)
	}
	if !m.Allowed["foo.example:8080"] {
		t.Errorf("allowed = %v, want the port as declared", m.Allowed)
	}
	if m.Allowed["foo.example:443"] {
		t.Errorf("allowed = %v: an explicit port was widened to the default", m.Allowed)
	}
}
