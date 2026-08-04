package config

import "strings"

// SourceRefSeparator splits "<source>:<name>". ":" and not "/": a source
// object is NOT addressable as a relative path from the den home, and a
// path-looking name would suggest it is. A plain YAML scalar carries ":"
// unquoted as long as no space follows, so `stack: corp:devx` stays writable
// as-is (spec 2026-08-04 §2.3).
const SourceRefSeparator = ":"

// SplitSourceRef splits a reference on its FIRST separator. ("", ref) when
// there is none: a bare name is a local object. An empty source component
// (":devx") collapses to local rather than erroring here — validation of the
// PARTS belongs to the callers, which know whether they hold a source name, a
// stack name or a nest name and can say so in the message.
func SplitSourceRef(ref string) (source, name string) {
	before, after, found := strings.Cut(ref, SourceRefSeparator)
	if !found {
		return "", ref
	}
	return before, after
}

// ValidateSourceName rejects names that cannot designate a directory under
// <denHome>/sources/. Both guards, in ValidateName-first order like LoadNest:
// the path-escape intent ("../..") reads better than a charset complaint. The
// sandbox charset then applies because a source name becomes the PREFIX of
// flattened sandbox names ("corp:api" → sandbox "corp-api") — a character sbx
// refuses would only surface at spawn time, far from the `den source add`
// that accepted it.
func ValidateSourceName(name string) error {
	if err := ValidateName("source", name); err != nil {
		return err
	}
	return ValidateSandboxComponent("source", name)
}
