package build

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/sbx"
)

// Deps is what Execute needs from the machine. Injected for the reason every
// field of cli.Deps is: the real Runner spawns processes, and DenHome is what
// `--den-home` redirects to keep the suite hermetic.
type Deps struct {
	Sbx     sbx.Runner
	DenHome string
}

// buildPlan is what the pre-flight loop below derives from a stack's config,
// BEFORE the first `sbx create`: the provisioning text and the create argv.
// Both come from pure functions over data den already has in hand — nothing
// here touches sbx — so computing them up front, and caching them here rather
// than recomputing inside buildOne, is what lets a chain reject everything
// config alone can reject before its first side effect.
type buildPlan struct {
	provisioning Provisioning
	createArgv   []string
}

// Execute runs the plan, in order.
//
// EVERYTHING rejectable from config alone runs BEFORE the first create — the
// ordering internal/spawn states at length: anything rejectable up front is
// rejected before the first side effect. That is not just ReadProvisioning:
// CreateArgv's own two guards (ValidateSandboxName; "no origin" for a stack
// with neither `base:` nor a resolved parent image) are just as cheap to run
// here and just as expensive to discover after several stacks have already
// built — so this loop runs CreateArgv too and keeps the resulting argv
// rather than throwing it away and reassembling it later. And whether a
// `<stack>-build` already exists is checked ONCE for the WHOLE chain, with a
// SINGLE `sbx ls --json`, rather than once per stack: a leftover for stack 3
// of 5 must be caught before stack 1 is even created, not discovered after
// stacks 1 and 2 have already spent minutes building — and one process beats
// N regardless.
func Execute(ctx context.Context, steps []Step, d Deps, out io.Writer) error {
	plans := make(map[string]buildPlan, len(steps))
	for _, s := range steps {
		if !s.Build {
			continue
		}
		// Plan already turns a stack with no provision.steps into a skip (or,
		// for the stack the user named, into a refusal), so no plan Plan
		// produces reaches this. The check stays as a GUARD, in the shape
		// agent.RenderMixin's freshness guard takes: Step is a bare exported
		// struct, and a hand-built plan must not reach half the chain before
		// discovering the hole.
		if !s.Stack.Buildable() {
			return notBuildableError(s.Stack)
		}
		p, err := ReadProvisioning(s.Stack)
		if err != nil {
			return err
		}
		// s.Stack.ParentImage, not a re-derived value, and read UNCONDITIONALLY:
		// build.Chain already resolved it, empty for a root stack — which is
		// exactly what makes CreateArgv fall back to `base:` — and Execute was
		// handed a flat chain precisely so it would not have to re-walk the
		// graph to get it back.
		argv, err := CreateArgv(s.Stack, s.Stack.ParentImage, ScratchDir(d.DenHome, s.Stack.Name))
		if err != nil {
			return err
		}
		plans[s.Stack.Name] = buildPlan{provisioning: p, createArgv: argv}
	}

	// ONE `sbx ls --json` for the whole chain — see the doc above — skipped
	// entirely when plans is empty: an all-skipped chain builds no `<stack>
	// -build` sandbox at all, so there is no name that could collide, and the
	// check below would loop over zero candidates. Not an optimization; the
	// call would be for nothing.
	if len(plans) > 0 {
		boxes, err := sbx.Ls(ctx, d.Sbx)
		if err != nil {
			return fmt.Errorf("could not check for pre-existing build sandboxes: %w", err)
		}
		for _, s := range steps {
			if !s.Build {
				continue
			}
			// A pre-existing build sandbox is a REFUSAL, not a `rm --force`
			// first. `<stack>-build` is a legal nest name (the component
			// charset allows it), so a blind cleanup can destroy a real
			// sandbox of the user's. The teardown in buildOne being deferred,
			// a leftover only survives a den killed by SIGKILL: rare enough to
			// deserve a human look.
			name := SandboxName(s.Stack.Name)
			if sbx.Find(boxes, name) != nil {
				return fmt.Errorf(
					"stack %q: a sandbox named %s already exists — den will not remove it for you, "+
						"because that name is also a legal nest name. Inspect it, then `sbx rm --force %s`",
					s.Stack.Name, name, name)
			}
		}
	}

	for i, s := range steps {
		if !s.Build {
			// Announced, never silent: a `den build dgdevx` that printed one
			// line would leave "devx was already built" indistinguishable from
			// "den forgot devx". The reason carries its own remedy.
			fmt.Fprintf(out, "[%d/%d] %s: skipped, %s\n", i+1, len(steps), s.Stack.Name, s.Skipped)
			continue
		}
		fmt.Fprintf(out, "[%d/%d] %s: building %s...\n", i+1, len(steps), s.Stack.Name, s.Stack.Image)
		if err := buildOne(ctx, d, s.Stack, plans[s.Stack.Name], out); err != nil {
			return err
		}
	}
	return nil
}

// buildOne is one stack's whole sequence — everything rejectable from config
// alone has already run in Execute's pre-flight loop, so the only thing left
// here that can fail before the first side effect is the scratch directory
// itself, which IS that side effect. A function of its own so the teardown
// can be a `defer` — in a loop it would pile up until Execute returned, which
// is exactly the leak it exists to prevent.
func buildOne(ctx context.Context, d Deps, s *config.Stack, plan buildPlan, out io.Writer) error {
	name := SandboxName(s.Name)

	scratch := ScratchDir(d.DenHome, s.Name)
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return fmt.Errorf("stack %q: could not create the build scratch %s: %w", s.Name, scratch, &config.FileError{Err: err})
	}

	if _, err := d.Sbx.Run(ctx, plan.createArgv...); err != nil {
		return fmt.Errorf("stack %q: could not create the build sandbox: %w", s.Name, err)
	}
	// From here on the VM exists: it must not outlive this function, whatever
	// happens. This is the `trap` every build.sh had to write by hand, and that
	// no test could verify.
	defer func() {
		if _, err := d.Sbx.Run(ctx, "rm", "--force", name); err != nil {
			fmt.Fprintf(out, "warning: build sandbox %s could not be removed: %v\n", name, err)
		}
	}()

	for i := range plan.provisioning.Steps {
		if _, err := d.Sbx.Run(ctx, "exec", name, "--", "bash", "-lc", plan.provisioning.Payload(i)); err != nil {
			return fmt.Errorf("stack %q: step %d/%d %s failed: %w",
				s.Name, i+1, len(plan.provisioning.Steps), plan.provisioning.Steps[i].Path, err)
		}
	}

	if _, err := d.Sbx.Run(ctx, "stop", name); err != nil {
		return fmt.Errorf("stack %q: could not stop the build sandbox before saving: %w", s.Name, err)
	}
	// den passes the image name. THE point of the whole sequence: `image:` and
	// what is actually saved cannot disagree, so `den build` succeeding and
	// `den <nest>` demanding `den build` can no longer both be true.
	if _, err := d.Sbx.Run(ctx, "template", "save", name, s.Image); err != nil {
		return fmt.Errorf("stack %q: could not save image %s: %w", s.Name, s.Image, err)
	}
	return nil
}
