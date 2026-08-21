// Package sbx drives the `sbx` CLI: sandbox naming, argument assembly,
// execution behind a mockable interface.
package sbx

import (
	"fmt"
	"strings"

	"github.com/PillowPillow/den/internal/config"
)

// NameSeparator separates the nest from the worktree in a sandbox name.
//
// `sbx create --name` allows the dot, and den forbids it in nest and worktree
// names, so the split is EXACT, without consulting the nest list. A "-"
// separator would make `my-api-feat` ambiguous (nest `my-api`+wt `feat`, or
// nest `my`+wt `api-feat`) and would need a longest-prefix match against
// declared nests — a sandbox would become unaddressable as soon as its nest
// is deleted.
const NameSeparator = "."

// MinNameLength is the shortest sandbox name sbx accepts.
//
// MEASURED, 2026-08-21 on sbx v0.38.0, and it is a WHOLE-NAME rule, not a
// per-component one — which is why it lives here and not in
// config.ValidateSandboxComponent:
//
//	$ sbx create --name a ...
//	ERROR: name must match regexp ^[a-zA-Z0-9][a-zA-Z0-9.-]+$: a
//	$ sbx create --name ab ...        # accepted
//
// The floor is the `+` quantifier in that regexp: one leading alphanumeric,
// then at least one more character. sbx v0.39.0 names the rule its own help
// had omitted ("omitted the leading-alphanumeric and two-character-minimum
// rules"), so den has been able to build the refused name since before
// v0.35.0 — `den up a` produced the sandbox name "a".
//
// A one-character COMPONENT stays legal, and deliberately: "api.a" is five
// characters and sbx takes it. Only a bare one-character nest is short enough
// to be refused, which is why the check is on the assembled name.
const MinNameLength = 2

// SandboxName builds the sandbox name of a nest, optionally worktreed.
// This name is den's only state carrier: `--label` does not exist in sbx.
func SandboxName(nest, worktree string) (string, error) {
	if err := config.ValidateSandboxComponent("nest", nest); err != nil {
		return "", err
	}
	name := nest
	if worktree != "" {
		if err := config.ValidateSandboxComponent("worktree", worktree); err != nil {
			return "", err
		}
		name = nest + NameSeparator + worktree
	}
	if len(name) < MinNameLength {
		// The remedy names both exits, because which one applies is the
		// user's call: rename the nest, or give this sandbox an instance.
		return "", fmt.Errorf(
			"sandbox name %q: %d characters, and sbx refuses anything under %d "+
				"(`name must match regexp ^[a-zA-Z0-9][a-zA-Z0-9.-]+$`) — rename the nest, "+
				"or name the instance with `--as` or `-w`", name, len(name), MinNameLength)
	}
	return name, nil
}

// SplitName is the inverse of SandboxName. TOTAL function: it validates
// nothing and never fails, because it also applies to sandboxes created
// outside den that `sbx ls` reports. A name without a separator is a nest
// without a worktree.
func SplitName(name string) (nest, worktree string) {
	nest, worktree, _ = strings.Cut(name, NameSeparator)
	return nest, worktree
}

// ValidateSandboxName checks that a name is one den would have built.
//
// SINGLE source of truth, exported and consumed by everyone who turns a name
// into a host path or an `sbx` argument: component-by-component validation
// used to exist in two copies, and they diverged.
//
// It round-trips through the validating constructor rather than redefining a
// charset — config.ValidateSandboxComponent stays the only source — then
// compares the rebuilt name to the original. That final comparison is what
// catches what component validation lets through: "api." splits into "api" +
// an empty worktree, two valid components, and would rebuild into "api". sbx
// would accept that name, and `sbx ls` would split it back into "api": two
// names for one sandbox.
func ValidateSandboxName(name string) error {
	nest, worktree := SplitName(name)
	rebuilt, err := SandboxName(nest, worktree)
	if err != nil {
		return err
	}
	if rebuilt != name {
		return fmt.Errorf("sandbox name %q: non-canonical form (rebuilds to %q)", name, rebuilt)
	}
	return nil
}
