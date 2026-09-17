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

// MaxNameLength is the longest sandbox name sbx accepts, in BYTES.
//
// MEASURED, 2026-09-16 on sbx v0.43.0 (spec 2026-09-16-sbx-0.43-compat §1.1),
// and a WHOLE-NAME rule like MinNameLength, which is why it lives here:
//
//	$ sbx create --name a<62×b> …   # 63 bytes: accepted
//	$ sbx create --name a<63×b> …   # 64 bytes
//	ERROR: sandbox name cannot exceed 63 characters: abbb…
//
// Bytes, not runes: a 63-rune name carrying one `é` (64 bytes) is refused the
// same way. Inert for den — the charset is ASCII, so len() is exact — but it
// is the rule as sbx applies it. The release note of v0.43.0 announces the
// cap; the binary's own --help still does not mention it.
//
// The refusal happens before the image stage on sbx's side, but AFTER den has
// created the worktree of a `-w` spawn: hence ValidateCreatableSandboxName,
// asked upstream of the first side effect (spec §6, issue #96).
const MaxNameLength = 63

// ReservedName is the one sandbox name sbx keeps for itself.
//
// MEASURED, 2026-09-16 on sbx v0.43.0: `ERROR: sandbox name cannot be
// 'default'`. Whole-name rule: "api.default" is accepted, so it is checked on
// the assembled name and not on a component.
const ReservedName = "default"

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
// STRUCTURE ONLY, deliberately: it says nothing about whether `sbx create`
// would still accept the name today — ValidateCreatableSandboxName owns that
// question. Every caller here addresses a sandbox that ALREADY exists —
// `den rm`, manifest.Path, the mixin and drift readers, policy.Settle — and
// sbx tightens its naming rules across versions. Answering both questions at
// once strands what an older den legally created: `den rm api.foo-` would
// return the naming error and leave the VM running, which is the one outcome
// cleanWorktrees exists to prevent.
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

// ValidateCreatableSandboxName checks that `sbx create` will accept a name.
//
// The four rules below are WHOLE-NAME rules den can satisfy at assembly time
// and sbx can tighten between versions, so they are asked only where den
// CREATES — `sbx create` argv assembly, and the create branch of a spawn.
// Readers and `den rm` ask ValidateSandboxName instead, so a sandbox an older
// den built stays addressable for the whole of its life.
//
// The structural check runs FIRST because the trailing-character rule indexes
// the last BYTE: ValidateSandboxName round-trips through
// config.ValidateSandboxComponent, whose charset is ASCII, which is what makes
// that byte the last rune. Enforced here, never assumed of the caller.
func ValidateCreatableSandboxName(name string) error {
	if err := ValidateSandboxName(name); err != nil {
		return err
	}
	if len(name) < MinNameLength {
		// The remedy names both exits, because which one applies is the
		// user's call: rename the nest, or give this sandbox an instance.
		return fmt.Errorf(
			"sandbox name %q: %d characters, and sbx refuses anything under %d "+
				"(`name must match regexp ^[a-zA-Z0-9][a-zA-Z0-9.-]+$`) — rename the nest, "+
				"or name the instance with `--as` or `-w`", name, len(name), MinNameLength)
	}
	if len(name) > MaxNameLength {
		// The remedy names --as, not "shorten the branch": the branch is the
		// user's and stays as typed in git — only the sandbox's instance
		// label needs to be short.
		return fmt.Errorf(
			"sandbox name %q: %d characters, and sbx refuses anything over %d "+
				"(`sandbox name cannot exceed 63 characters`) — shorten the nest, or name the "+
				"instance with `--as <short-label>` instead of the branch",
			name, len(name), MaxNameLength)
	}
	if last := rune(name[len(name)-1]); !config.IsAlphanumeric(last) {
		// Only "-" can reach here: "." is excluded from the component
		// charset, and an empty worktree never appends the separator.
		return fmt.Errorf(
			"sandbox name %q: ends with %q, and sbx refuses that (`sandbox name must end "+
				"with an alphanumeric character`) — a branch ending in \"-\" flattens to this; "+
				"name the instance with `--as`", name, string(last))
	}
	if name == ReservedName {
		return fmt.Errorf(
			"sandbox name %q: reserved by sbx (`sandbox name cannot be 'default'`) — rename the nest",
			name)
	}
	return nil
}
