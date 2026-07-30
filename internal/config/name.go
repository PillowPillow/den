package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ValidateName rejects object names that don't designate a direct child of
// ~/.den. A nest or stack name is an identifier, not a path: without this
// guard, `den nest show ../../../../etc/passwd` builds a path that escapes
// the den home. The name also becomes a sandbox name and the seed of the port
// window hash, so it must stay clean at the source.
//
// kind names the object in the error message ("nest", "stack").
func ValidateName(kind, name string) error {
	switch {
	case name == "":
		return fmt.Errorf("%s: name is empty", kind)
	case strings.ContainsRune(name, '/') || strings.ContainsRune(name, filepath.Separator):
		return fmt.Errorf(
			"%s %q: a name cannot contain a path separator — it's an identifier "+
				"in ~/.den, not a path", kind, name)
	case name == "." || name == "..":
		return fmt.Errorf("%s %q: reserved name", kind, name)
	}
	return nil
}

// alphanumericChars is what a sandbox name component MUST start with. A name
// starting with "-" or "+" is indistinguishable from a flag, both for
// `sbx create --name` and for den itself.
const alphanumericChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// sandboxComponentChars is everything a sandbox name component may contain
// past the first character (see alphanumericChars).
//
// `sbx create --name` accepts "letters, numbers, hyphens, periods, plus signs
// and minus signs". den is stricter by one notch: the period is excluded,
// because it's the separator in `<nest>.<worktree>` and splitting must stay
// exact without consulting the list of declared nests.
//
// This is the ONLY definition of the charset: ValidateSandboxComponent checks
// it rune by rune rather than duplicating it in a regexp, so the two can never diverge.
const sandboxComponentChars = alphanumericChars + "+-"

// FlattenSandboxComponent renders the "nameable" version of a name: every
// character outside the charset becomes a "-". This is what lets
// `den <nest> -w feature/123` work on the real branch "feature/123" while
// naming its sandbox "<nest>.feature-123".
//
// Flattening is not injective: "feat/try" and "feat-try" render the same
// component, hence the same worktree directory. That's not a gap —
// worktree.Ensure compares the requested branch to the existing directory's
// and refuses the second name with a message naming both branches.
func FlattenSandboxComponent(kind, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("%s: name is empty", kind)
	}
	var b strings.Builder
	for _, r := range name {
		if strings.ContainsRune(sandboxComponentChars, r) {
			b.WriteRune(r)
			continue
		}
		b.WriteRune('-')
	}
	flattened := b.String()
	// The only case flattening can't fix: position 1. The message is
	// ValidateSandboxComponent's, on the ORIGINAL name — the one the user
	// typed — since returning the flattened form would send them looking for
	// a name that isn't in their command line.
	if err := ValidateSandboxComponent(kind, flattened); err != nil {
		if original := ValidateSandboxComponent(kind, name); original != nil {
			err = original
		}
		return "", err
	}
	return flattened, nil
}

// ValidateSandboxComponent checks that a name can become a sandbox name
// component. The message distinguishes two faults: a character outside the
// charset ("character %q is not allowed") and a misplaced first character
// (since "-" is allowed from position 2 onward, calling it "not allowed" in
// position 1 would be wrong and confusing).
func ValidateSandboxComponent(kind, name string) error {
	if name == "" {
		return fmt.Errorf("%s: name is empty", kind)
	}
	runes := []rune(name)
	if !strings.ContainsRune(alphanumericChars, runes[0]) {
		return fmt.Errorf(
			"%s %q: must start with a letter or digit, not %q — a name that doesn't "+
				"start with an alphanumeric is indistinguishable from a flag", kind, name, string(runes[0]))
	}
	for _, r := range runes[1:] {
		if !strings.ContainsRune(sandboxComponentChars, r) {
			return fmt.Errorf(
				"%s %q: character %q is not allowed — this name becomes a sandbox name, "+
					"limited to letters, digits, \"-\" and \"+\" (\".\" is reserved for the "+
					"<nest>.<worktree> separator)", kind, name, string(r))
		}
	}
	return nil
}
