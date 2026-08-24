package doctor

import (
	"fmt"
	"slices"
	"strings"
)

// UndeclaredSurface is one machine-side store sbx writes, as den observed it,
// next to what den's own sources declare about it.
//
// ONE type for every surface, and that is the requirement rather than a
// convenience: sbx v0.39.0 ships three authors of machine state (`sbx setup` /
// `sbx secret set`, `sbx skills import`, `sbx mcp add`) and den's report must
// grow a fourth by adding a value, not a paragraph. den's own model is that
// `~/.den` is the single source of truth; the point of this type is to say out
// loud where that is not the case.
//
// Two grades of observation, because den genuinely has two. A surface den can
// enumerate (the global secret store, parsed by column) carries Entries and is
// reported by identity. A surface den can only probe (the MCP registry, the
// skills store — see sbx.ReadStore for why naming their rows would be a guess)
// carries Occupied alone. Collapsing the two would mean either inventing names
// den never read, or throwing away the ones it did.
//
// Build one through NamedSurface, OpaqueSurface or UnobservedSurface rather
// than by hand: the constructors are what keep Entries and Occupied from
// contradicting each other.
type UndeclaredSurface struct {
	// Name is the check's name in the report.
	Name string
	// Look is the sbx command that shows the detail — the primary source the
	// report sends the user to instead of paraphrasing it.
	Look string
	// Known is false when den could not observe the surface at all. "den could
	// not look" and "den looked and found nothing" are different facts with
	// different remedies (converge.Observation carries the same distinction).
	Known bool
	// Entries are the identities den read, sorted. Empty on an opaque surface.
	Entries []string
	// Occupied says something is there. On a named surface it restates
	// len(Entries) > 0; on an opaque one it is everything den knows.
	Occupied bool
	// Declared holds every identity some den source claims. Nil is legitimate
	// and common: a home with no source declares nothing, and then everything
	// present is undeclared — which is the honest answer, not a defect.
	Declared map[string]bool
}

// NamedSurface builds a surface den could enumerate.
func NamedSurface(name, look string, entries []string, declared map[string]bool) UndeclaredSurface {
	return UndeclaredSurface{
		Name: name, Look: look, Known: true,
		Entries: entries, Occupied: len(entries) > 0, Declared: declared,
	}
}

// OpaqueSurface builds a surface den could only probe for presence.
func OpaqueSurface(name, look string, occupied bool) UndeclaredSurface {
	return UndeclaredSurface{Name: name, Look: look, Known: true, Occupied: occupied}
}

// UnobservedSurface builds the surface den could not read.
//
// It takes NO cause, and that is a decision rather than an omission. sbx's
// refusals are paragraphs, this report is a column of one-line checks, and —
// the reason that actually settles it — the machine cause already has an owner:
// the source lines state it exactly once and point at each other for the rest
// (internal/cli, sourceDetail). A governance line restating it would print the
// same refusal a second time, which is the regression PR82 removed and a test
// still pins.
//
// So the line names the COMMAND instead. It is the primary source: running it
// reproduces sbx's own error in full, unflattened, which is more than den
// could quote anyway — the same pattern as "`den source status %s` says what
// is missing".
func UnobservedSurface(name, look string) UndeclaredSurface {
	return UndeclaredSurface{Name: name, Look: look}
}

// Undeclared lists what is present on the machine that no source declares,
// sorted. Empty on an opaque surface: den knows something is there, not what.
func (s UndeclaredSurface) Undeclared() []string {
	var out []string
	for _, e := range s.Entries {
		if !s.Declared[e] {
			out = append(out, e)
		}
	}
	slices.Sort(out)
	return out
}

// UndeclaredCheck renders one surface as a diagnostic.
//
// LevelOK, never LevelWarning, and this is the decision the whole feature
// turns on. Undeclared machine state is NOT a fault: a user who ran `sbx
// setup`, or who keeps a host credential den has no business managing, has a
// perfectly correct machine. A warning would end `den doctor` on "no failure,
// but N warning(s): review the [warn] lines" (internal/cli/doctor.go) for a
// machine with nothing to review — the same contradiction the unmapped
// optional repo keys line was fixed to stop producing, and the fastest way to
// teach a user to stop reading the report.
//
// It is printed all the same, because SILENCE is the actual defect being
// repaired here. den refuses rather than normalizing without saying so (spec
// §2); on these surfaces it does neither — it says nothing at all, and a
// machine reachable by three other authors reads as a machine den fully
// describes.
//
// den NEVER removes any of it, and the line says so rather than implying it:
// the same doctrine `den rm` follows about a creation record it could not read.
// There is no `--fix` behind this check and there must not be one.
func UndeclaredCheck(s UndeclaredSurface) Check {
	if !s.Known {
		return Check{Name: s.Name, Level: LevelOK, Detail: fmt.Sprintf(
			"skipped: den could not read this surface, so state no source declares cannot be "+
				"told apart from an empty machine — run `%s` to see why", s.Look)}
	}
	if !s.Occupied {
		return Check{Name: s.Name, Level: LevelOK, Detail: "nothing on this machine"}
	}
	// Opaque: den probed, something answered, and it cannot name it. Saying
	// "present, undeclared" anyway is the point — den has no source declaring
	// any of these two surfaces at all, so whatever is there is undeclared by
	// construction, and the user is sent to the command that lists it.
	if len(s.Entries) == 0 {
		return Check{Name: s.Name, Level: LevelOK, Detail: fmt.Sprintf(
			"present, undeclared: no den source declares this store, and den neither wrote it "+
				"nor removes it — `%s` lists it", s.Look)}
	}
	undeclared := s.Undeclared()
	if len(undeclared) == 0 {
		return Check{Name: s.Name, Level: LevelOK, Detail: fmt.Sprintf(
			"%d entry(ies), all declared by a source", len(s.Entries))}
	}
	// Every one is NAMED, not counted: "3 undeclared" is unactionable, and the
	// one the reader is looking for is the credential they forgot they set.
	//
	// Worded without a pronoun for the list, on purpose: the count is 1 as
	// often as it is 5, and "den did not write them" reads wrong on the single
	// entry that is the common case (measured on a real machine, 2026-08-24:
	// "1 of 4"). Same care as identities() one file over.
	return Check{Name: s.Name, Level: LevelOK, Detail: fmt.Sprintf(
		"%d of %d present, undeclared: %s — not written by den, which never removes what it "+
			"did not create; `%s` shows the detail", len(undeclared), len(s.Entries),
		strings.Join(undeclared, ", "), s.Look)}
}
