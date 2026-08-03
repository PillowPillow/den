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
// `den <nest> -w build` really produces. The hyphen keeps the build sandbox a
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
// Deliberately NOT sbx.CreateArgv, which assembles a spawn: no generated
// mixin, no stack kits, no repo workspaces. Every one of those serves a VM the
// user attaches to and keeps; this one is destroyed at step 5 of the sequence.
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
		return nil, fmt.Errorf("stack %q: cannot name its build sandbox: %w", s.Name, err)
	}

	argv := []string{"create", "--name", name}
	positional := s.Base
	if parentImage != "" {
		argv = append(argv, "--template", parentImage)
		positional = sbx.PositionalAgent
	}
	if positional == "" {
		// Unreachable through LoadStack, which refuses a buildable stack with
		// no origin. Kept as a BOUNDARY guard: CreateArgv is exported and takes
		// a struct anyone can fill, the doctrine sbx.CreateArgv states for its
		// own inputs.
		return nil, notBuildableOriginError(s)
	}
	return append(argv, positional, scratch), nil
}

func notBuildableOriginError(s *config.Stack) error {
	return fmt.Errorf(
		"stack %q: no origin — declare `base:` (root stack) or `parent:` in %s",
		s.Name, filepath.Join(s.Dir, "stack.yaml"))
}
