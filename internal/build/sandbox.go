package build

import (
	"fmt"
	"path/filepath"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/sbx"
)

// buildSuffix distinguishes the throwaway VM from a real sandbox.
//
// A `-` and not a `.`: the period is the `<nest>.<worktree>` separator, and
// `devx.build` would decompose into a worktree named "build" — a name
// `den up <nest> -w build` really produces. The hyphen keeps the build sandbox a
// single component, which is what `sbx.SplitName` then reads it as.
const buildSuffix = "-build"

// SandboxName is the throwaway VM `den build` works in.
//
// NOT collision-proof, and it cannot be: the component charset (§2) makes
// `devx-build` a perfectly legal nest name. That is precisely why Execute
// REFUSES a pre-existing sandbox by this name instead of removing it — see
// there.
func SandboxName(stack string) string { return stack + buildSuffix }

// ScratchDir is the empty directory mounted into the build VM.
//
// `sbx create` requires at least one path, and mounting the host's /tmp into a
// build VM has no justification. Under `cache/`, which spec §3 already
// declares reconstructible and never a source of truth.
func ScratchDir(denHome, stack string) string {
	return filepath.Join(denHome, "cache", "build", stack)
}

// CreateArgv is the `sbx create` of a BUILD sandbox.
//
// Deliberately NOT sbx.EnvFile, which renders a spawn's `.sbxenv.yaml`: no
// generated mixin, no stack kits, no repo workspaces. Every one of those
// serves a VM the user attaches to and keeps; this one is destroyed at step 5
// of the sequence.
// Sharing the builder would have meant weakening its guards — it refuses a
// create with no mixin, and rightly so for a spawn.
//
// parentImage empty ⇒ ROOT stack: no `--template`, and the positional is
// `s.Base`, which is what selects the starting image. Non-empty ⇒ DERIVED: the
// image decides, and the positional is `sbx.PositionalAgent` for the reason
// stated there.
func CreateArgv(s *config.Stack, parentImage, scratch string) ([]string, error) {
	name := SandboxName(s.Name)
	// Guarded here rather than trusted from config.ValidateName: that one
	// accepts names sbx would reject (it only forbids separators and the two
	// reserved dots), and a build must not reach a process to learn it.
	if err := sbx.ValidateSandboxName(name); err != nil {
		return nil, fmt.Errorf("stack %q: cannot name its build sandbox — rename the stack directory %s: %w", s.Name, s.Dir, err)
	}

	argv := []string{"create", "--name", name}
	positional := s.Base
	if parentImage != "" {
		argv = append(argv, "--template", parentImage)
		positional = sbx.PositionalAgent
	}
	if positional == "" {
		// Unreachable through LoadStack — but only since it started refusing an
		// empty `image:` UNCONDITIONALLY, and that is worth recording, because
		// this comment claimed unreachability while the hole was open.
		//
		// LoadStack refuses a BUILDABLE stack with no origin, which covers the
		// direct shape. It did not cover the indirect one: a stack with no
		// `image:` used as a `parent:` handed its child an empty ParentImage,
		// read one line above as "root stack", so the positional fell back to
		// the child's `base:` — which a stack declaring `parent:` does not have.
		// This refusal then fired on the CHILD, naming the child's stack.yaml,
		// which already declares the `parent:` the message asks for. Wrong file,
		// remedy already applied — and it fired in Execute's whole-chain
		// pre-flight, so one stack missing `image:` refused the entire
		// `den build`. Measured 2026-08-03, closed in config.LoadStack.
		//
		// Kept as a BOUNDARY guard: CreateArgv is exported and takes a struct
		// anyone can fill, the doctrine sbx.EnvFile states for its own inputs.
		return nil, notBuildableOriginError(s)
	}
	return append(argv, positional, scratch), nil
}

func notBuildableOriginError(s *config.Stack) error {
	return fmt.Errorf(
		"stack %q: no origin — declare `base:` (root stack) or `parent:` in %s",
		s.Name, filepath.Join(s.Dir, "stack.yaml"))
}
