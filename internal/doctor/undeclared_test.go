package doctor

import (
	"strings"
	"testing"
)

// The level is the decision the whole feature turns on, so it is asserted on
// EVERY shape the check can take rather than on one of them.
//
// Undeclared machine state is not a fault: a user who ran `sbx setup`, or who
// keeps a host credential den has no business managing, has a correct machine.
// A warning would end `den doctor` on "no failure, but N warning(s): review the
// [warn] lines" for a machine with nothing to review.
func TestUndeclaredCheckNeverWeighsOnTheReport(t *testing.T) {
	for _, s := range []UndeclaredSurface{
		UnobservedSurface("sbx secrets", "sbx secret ls -g"),
		OpaqueSurface("sbx mcp servers", "sbx mcp ls", true),
		OpaqueSurface("sbx mcp servers", "sbx mcp ls", false),
		NamedSurface("sbx secrets", "sbx secret ls -g", []string{"service github"}, nil),
		NamedSurface("sbx secrets", "sbx secret ls -g", []string{"service github"},
			map[string]bool{"service github": true}),
	} {
		c := UndeclaredCheck(s)
		if c.Level != LevelOK {
			t.Errorf("%q reported at level %d, want LevelOK: %s", s.Name, c.Level, c.Detail)
		}
		if c.Blocking() {
			t.Errorf("%q blocks the exit code: %s", s.Name, c.Detail)
		}
	}
}

// The entry no source declares is NAMED. A count is unactionable, and the one
// name the reader is looking for is the credential they forgot they set.
func TestUndeclaredCheckNamesWhatNoSourceDeclares(t *testing.T) {
	s := NamedSurface("sbx secrets", "sbx secret ls -g",
		[]string{"registry ghcr.io:443", "service github"},
		map[string]bool{"service github": true})

	if got := s.Undeclared(); len(got) != 1 || got[0] != "registry ghcr.io:443" {
		t.Fatalf("Undeclared() = %q, want [registry ghcr.io:443]", got)
	}
	detail := UndeclaredCheck(s).Detail
	if !strings.Contains(detail, "registry ghcr.io:443") {
		t.Errorf("the undeclared entry is not named: %q", detail)
	}
	if strings.Contains(detail, "service github") {
		t.Errorf("a DECLARED entry is reported as undeclared: %q", detail)
	}
	// den never removes what it did not create, and the line says so rather
	// than leaving the reader to wonder whether `--fix` would.
	if !strings.Contains(detail, "never removes") {
		t.Errorf("the line does not say den leaves it alone: %q", detail)
	}
}

// A home with no source declares nothing, and then everything present is
// undeclared. That is the honest answer, not a defect to suppress.
func TestUndeclaredCheckReportsEverythingWhenNoSourceDeclaresAnything(t *testing.T) {
	s := NamedSurface("sbx secrets", "sbx secret ls -g",
		[]string{"registry ghcr.io:443", "service github"}, nil)
	detail := UndeclaredCheck(s).Detail
	for _, want := range []string{"registry ghcr.io:443", "service github", "2 of 2"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail %q is missing %q", detail, want)
		}
	}
}

// Everything declared is a plain count. Nothing to act on, nothing to name.
func TestUndeclaredCheckSaysSoWhenEverythingIsDeclared(t *testing.T) {
	s := NamedSurface("sbx secrets", "sbx secret ls -g", []string{"service github"},
		map[string]bool{"service github": true})
	detail := UndeclaredCheck(s).Detail
	if !strings.Contains(detail, "all declared") {
		t.Errorf("detail %q does not report a fully declared surface", detail)
	}
	if strings.Contains(detail, "undeclared:") {
		t.Errorf("detail %q names an undeclared entry on a fully declared surface", detail)
	}
}

// An OPAQUE surface reports presence and points at the command that lists it.
// den cannot name these rows without guessing a layout nobody measured
// (sbx.ReadStore, spec §14.3), so the report sends the user to the primary
// source instead of paraphrasing it.
func TestUndeclaredCheckPointsAtThePrimarySourceForAnOpaqueSurface(t *testing.T) {
	occupied := UndeclaredCheck(OpaqueSurface("sbx mcp servers", "sbx mcp ls", true)).Detail
	if !strings.Contains(occupied, "present, undeclared") {
		t.Errorf("an occupied opaque surface is not named as undeclared: %q", occupied)
	}
	if !strings.Contains(occupied, "sbx mcp ls") {
		t.Errorf("the line does not name the command that lists it: %q", occupied)
	}

	empty := UndeclaredCheck(OpaqueSurface("sbx mcp servers", "sbx mcp ls", false)).Detail
	if strings.Contains(empty, "undeclared") {
		t.Errorf("an empty surface is reported as undeclared: %q", empty)
	}
}

// "den could not look" is its own verdict, and it carries NO cause: the
// machine cause is owned by the source lines, which state it exactly once
// (internal/cli, sourceDetail). This line names the command instead.
func TestUnobservedSurfaceNamesTheCommandRatherThanRestatingTheCause(t *testing.T) {
	detail := UndeclaredCheck(UnobservedSurface("sbx secrets", "sbx secret ls -g")).Detail
	if !strings.Contains(detail, "skipped") {
		t.Errorf("an unobserved surface does not say it was skipped: %q", detail)
	}
	if !strings.Contains(detail, "sbx secret ls -g") {
		t.Errorf("the line does not name the command to run: %q", detail)
	}
	// The one reading that must never appear: an unread surface is not an
	// empty one.
	if strings.Contains(detail, "nothing on this machine") {
		t.Errorf("an unread surface is reported as empty: %q", detail)
	}
}
