package build

import (
	"context"
	"fmt"
	"path/filepath"

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

// Target is the stack the user named, in the TWO spellings Plan needs, and
// it is a struct rather than two adjacent string parameters precisely so a
// call site cannot silently swap them.
//
//   - Name is the BARE name, the one the chain and config.Stacks are keyed
//     by. It is what the plan ARBITRATES on.
//   - Ref is the reference the user typed, prefixed when the target came from
//     a source ("corp:devx"). It is the only spelling a `den build ...`
//     remedy may print — internal/cli/build.go rewrites the argument to the
//     bare name before Chain sees it, and a remedy built from that name sends
//     the user to a DIFFERENT stack in a different root, one that on many
//     dens builds successfully and changes nothing about the failure.
//
// The zero Target is "no target at all" (bare `den build`): both fields empty.
type Target struct {
	Name string
	Ref  string
}

// LocalTarget is the Target of a bare, unprefixed argument, where the two
// spellings coincide.
func LocalTarget(name string) Target { return Target{Name: name, Ref: name} }

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
func Plan(ctx context.Context, chain []*config.Stack, target Target, force bool, images Images) ([]Step, error) {
	steps := make([]Step, 0, len(chain))
	for _, s := range chain {
		// A stack with NO `provision.steps` is not buildable, and that is a
		// DECLARED configuration rather than a mistake: its `image:` may name a
		// registry image sbx will pull, which is exactly why the spawn's own
		// image check leaves such a stack alone. Reading the same
		// config.Stack.Buildable on both sides is what keeps the two answers
		// the same — measured on 2026-08-03, they had drifted, and a `den build`
		// on a den holding one pullable base and three buildable stacks built
		// NOTHING.
		//
		// The one exception is the stack the user NAMED. That is a request den
		// must refuse rather than answer with a skip line: the user asked for
		// that build specifically, and silently doing nothing reads as success.
		if !s.Buildable() {
			if s.Name == target.Name {
				return nil, notBuildableError(s)
			}
			steps = append(steps, Step{
				Stack:   s,
				Skipped: "no `provision.steps`, nothing for den to build",
			})
			continue
		}

		// Only an ANCESTOR is a candidate for skipping — see the godoc. With no
		// target every stack is a root, so nothing is an ancestor and nothing is
		// consulted.
		if target.Name == "" || force || s.Name == target.Name {
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
				s.Name, s.Image, err, target.Ref)
		}
		if present {
			steps = append(steps, Step{
				Stack: s,
				// The remedy travels WITH the reason, because it is not the same
				// for every skip: --force rebuilds an image that is already
				// there, and does nothing at all for a stack with no
				// provision.steps.
				Skipped: fmt.Sprintf("image %s already built (--force rebuilds it)", s.Image),
			})
			continue
		}
		steps = append(steps, Step{Stack: s, Build: true})
	}
	return steps, nil
}

// notBuildableError is the refusal for a stack den is asked to build and that
// declares nothing to run.
//
// ONE definition for the two sites that produce it — Plan, on the stack the
// user NAMED, and Execute's pre-flight guard — because they answer the same
// fault, and the two copies of the previous version had already started to
// drift apart on a verb.
//
// The last clause is the WHOLE backward-compatibility story, in a dozen words.
// A user whose `~/.den` predates this change has a `stacks/<n>/build.sh` sitting
// right there, and every message den can produce is otherwise individually
// correct while never mentioning that the file is no longer read: `den build`
// skips the stack ("no `provision.steps`"), `den build <stack>` lands here, and
// `den up`/`den run` silently stops warning about the missing image. Three true
// answers, none naming the cause — the same closed loop, one level up. Naming
// the dead file here is what breaks it, and this is the message the user reaches
// by asking for the build explicitly.
func notBuildableError(s *config.Stack) error {
	return fmt.Errorf(
		"stack %q: nothing to build — declare `provision.steps` in %s "+
			"(den runs each entry in the build VM, in order, and saves the result as %s). "+
			"A `stacks/%s/build.sh` is no longer run: den owns the sequence now, and a stack "+
			"declares what to run instead of how to run it",
		s.Name, filepath.Join(s.Dir, "stack.yaml"), s.Image, s.Name)
}
