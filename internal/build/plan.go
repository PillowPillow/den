package build

import (
	"context"
	"fmt"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/sbx"
)

// Images answers the one question spec §6 leaves open: "si son image manque".
//
// It is an interface, and it caches, because the answer costs a process. Until
// the 2026-07-31 survey it could not be answered at all — issue #8 was blocked
// on it, and its three fallbacks were a container-runtime dependency, a
// systematic rebuild, or no check whatsoever. `sbx template ls --json`
// (spec §14.0) closed that.
type Images interface {
	// Has reports whether this stack image is already built. The reference is
	// the raw string a stack writes in `image:`; matching it against what sbx
	// reports is sbx.FindTemplate's job.
	Has(ctx context.Context, image string) (bool, error)
}

// SbxImages answers from `sbx template ls --json`, reading it AT MOST ONCE.
//
// Lazy on purpose, and it is the laziness that carries a behaviour rather than
// an optimisation: `den build` (all) and `den build <stack> --force` rebuild
// everything by definition, so they consult nothing — and therefore cannot be
// broken by an `sbx template ls` that fails or whose schema moved. Only the one
// form that actually arbitrates on image presence pays for the call, and only
// that form can fail on it.
type SbxImages struct {
	Runner sbx.Runner

	loaded    []sbx.Template
	err       error
	consulted bool
}

func (s *SbxImages) Has(ctx context.Context, image string) (bool, error) {
	if !s.consulted {
		s.consulted = true
		s.loaded, s.err = sbx.Templates(ctx, s.Runner)
	}
	if s.err != nil {
		return false, s.err
	}
	return sbx.FindTemplate(s.loaded, image) != nil, nil
}

// Step is one stack of the chain and the verdict on it.
type Step struct {
	Stack *config.Stack
	// Build is false when the step is skipped. Skipped steps stay IN the plan:
	// `den build dgdevx` that silently printed one line would leave the user
	// unable to tell "devx was already there" from "den forgot devx".
	Build bool
	// Skipped says why, in the user's terms, and is empty when Build is true.
	Skipped string
}

// Plan decides what the chain actually rebuilds. The rules are spec §6's
// table, verbatim:
//
//	den build                  → everything, in topological order
//	den build <stack>          → ancestors only if their image is missing,
//	                             then the stack itself, unconditionally
//	den build <stack> --force  → the ancestors too
//
// The TARGET is always built. That asymmetry is the point of the command: the
// user named it, and rebuilding on demand is what `den build <stack>` is for.
// Only its ancestors are treated as a means to that end, and skipping the ones
// already built is what stops `den build dgdevx` from rebuilding a base image
// that takes minutes and has not changed.
//
// force with no target is a no-op rather than a refusal: `den build` already
// rebuilds everything, so the flag asks for what is happening anyway.
func Plan(ctx context.Context, chain []*config.Stack, target string, force bool, images Images) ([]Step, error) {
	steps := make([]Step, 0, len(chain))
	for _, s := range chain {
		// Only an ANCESTOR is a candidate for skipping — see the godoc. With no
		// target every stack is a root, so nothing is an ancestor and nothing is
		// consulted.
		if target == "" || force || s.Name == target {
			steps = append(steps, Step{Stack: s, Build: true})
			continue
		}
		present, err := images.Has(ctx, s.Image)
		if err != nil {
			// NOT fail-open. Skipping a build den could not justify skipping
			// would produce the exact failure this command exists to prevent:
			// `sbx create` refusing later with a 403 that speaks of
			// authorization, on an image nobody ever built. Building anyway
			// would be the other silent normalization — minutes of work the
			// user did not ask for.
			return nil, fmt.Errorf(
				"stack %q: den could not check whether image %s is already built: %w — "+
					"rebuild it anyway with `den build %s --force`",
				s.Name, s.Image, err, target)
		}
		if present {
			steps = append(steps, Step{
				Stack:   s,
				Skipped: fmt.Sprintf("image %s already built", s.Image),
			})
			continue
		}
		steps = append(steps, Step{Stack: s, Build: true})
	}
	return steps, nil
}
