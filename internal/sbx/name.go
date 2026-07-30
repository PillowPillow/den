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

// SandboxName builds the sandbox name of a nest, optionally worktreed.
// This name is den's only state carrier: `--label` does not exist in sbx.
func SandboxName(nest, worktree string) (string, error) {
	if err := config.ValidateSandboxComponent("nest", nest); err != nil {
		return "", err
	}
	if worktree == "" {
		return nest, nil
	}
	if err := config.ValidateSandboxComponent("worktree", worktree); err != nil {
		return "", err
	}
	return nest + NameSeparator + worktree, nil
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
