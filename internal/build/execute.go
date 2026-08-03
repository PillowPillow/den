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

// Execute runs the plan, in order.
//
// EVERY provision file of the whole chain is read BEFORE the first create —
// the ordering internal/spawn states at length: anything rejectable up front is
// rejected before the first side effect. Here the side effect is expensive
// rather than messy: a chain that built four minutes of base image and only
// then discovered a missing step would have spent that time to reach a refusal
// den could make instantly. Nothing is read twice; the text is what the
// payloads are composed from.
func Execute(ctx context.Context, steps []Step, d Deps, out io.Writer) error {
	provisioned := make(map[string]Provisioning, len(steps))
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
		provisioned[s.Stack.Name] = p
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
		if err := buildOne(ctx, d, s.Stack, provisioned[s.Stack.Name], out); err != nil {
			return err
		}
	}
	return nil
}

// buildOne is one stack's whole sequence. A function of its own so the
// teardown can be a `defer` — in a loop it would pile up until Execute
// returned, which is exactly the leak it exists to prevent.
func buildOne(ctx context.Context, d Deps, s *config.Stack, p Provisioning, out io.Writer) error {
	name := SandboxName(s.Name)

	// A pre-existing build sandbox is a REFUSAL, not a `rm --force` first.
	// `<stack>-build` is a legal nest name (the component charset allows it),
	// so a blind cleanup can destroy a real sandbox of the user's. The teardown
	// below being deferred, a leftover only survives a den killed by SIGKILL:
	// rare enough to deserve a human look.
	boxes, err := sbx.Ls(ctx, d.Sbx)
	if err != nil {
		return fmt.Errorf("stack %q: could not check whether %s already exists: %w", s.Name, name, err)
	}
	if sbx.Find(boxes, name) != nil {
		return fmt.Errorf(
			"stack %q: a sandbox named %s already exists — den will not remove it for you, "+
				"because that name is also a legal nest name. Inspect it, then `sbx rm --force %s`",
			s.Name, name, name)
	}

	scratch := ScratchDir(d.DenHome, s.Name)
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return fmt.Errorf("stack %q: could not create the build scratch %s: %w", s.Name, scratch, &config.FileError{Err: err})
	}

	// The parent's IMAGE, not its name: `--template` takes a reference. Empty
	// for a root stack, which is what makes CreateArgv use `base:` instead.
	parentImage := ""
	if s.Parent != "" {
		parentImage = s.ParentImage
	}
	argv, err := CreateArgv(s, parentImage, scratch)
	if err != nil {
		return err
	}
	if _, err := d.Sbx.Run(ctx, argv...); err != nil {
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

	for i := range p.Steps {
		if _, err := d.Sbx.Run(ctx, "exec", name, "--", "bash", "-lc", p.Payload(i)); err != nil {
			return fmt.Errorf("stack %q: step %d/%d %s failed: %w",
				s.Name, i+1, len(p.Steps), p.Steps[i].Path, err)
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
