package converge

import (
	"context"
	"fmt"
	"io"

	"github.com/PillowPillow/den/internal/build"
	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/source"
)

// buildDriver converges one stack image a source declares under
// `resources.builds`.
//
// It WRAPS the existing builder — build.Chain, build.Plan, build.Execute —
// rather than shelling out to `den build`, and never reimplements any of it.
// Two reasons, and the second is the one that matters: a second build path
// would drift from the first (the 2026-08-03 divergence between `den build`
// and the spawn's image check is the precedent this repo already paid for),
// and `den build` owns the whole create → exec → stop → template save → rm
// sequence, which nothing else in den knows how to do correctly.
type buildDriver struct {
	// stackRoot is the SOURCE's checkout: a declared build builds the source's
	// stack, never a personal one that happens to share its name.
	stackRoot string
	stack     string
	runner    sbx.Runner
	denHome   string
	source    string
	// force rebuilds even when the image exists. True on a first install and
	// on a version increase: the image of version 1.0.0 is not the image of
	// 1.1.0, and sbx has no way to tell den which one it holds.
	force bool
}

func (d *buildDriver) resume() string {
	return fmt.Sprintf("run `den source configure %s`", d.source)
}

// Inspect asks sbx whether the declared image exists. It reads the stack from
// the SOURCE checkout to learn the image reference — the manifest names a
// stack, and only the stack knows what it produces.
func (d *buildDriver) Inspect(ctx context.Context) (Observation, error) {
	stack, err := config.LoadStack(d.stackRoot, d.stack)
	if err != nil {
		return Observation{}, err
	}
	images := &build.SbxImages{Runner: d.runner}
	has, err := images.Has(ctx, stack.Image)
	if err != nil {
		return Observation{}, fmt.Errorf("reading the sbx image inventory: %w", err)
	}
	return Observation{Present: has, Detail: "image " + stack.Image + " is built"}, nil
}

func (d *buildDriver) Plan(o Observation) ResourcePlan {
	p := ResourcePlan{
		ID:       d.stack,
		Kind:     KindStackBuild,
		Known:    true,
		Observed: o.Detail,
		Expected: "the image of stack " + d.stack + ", built from this source",
		Resume:   d.resume(),
		Action:   ActionCreate,
	}
	switch {
	case o.Present && !d.force:
		p.Action = ActionUnchanged
	case o.Present && d.force:
		// The image exists but belongs to another version of the source: den
		// rebuilds, and the plan says "update" so the user sees that something
		// they already have will be replaced.
		p.Action = ActionUpdate
	}
	return p
}

// Apply runs the real builder over the source's own stack chain.
//
// force is passed through to build.Plan: on a first install and on a version
// increase, an existing image is not evidence that it was built from THIS
// version of the source — nothing in sbx records which source content produced
// a template. A resumed convergence of the SAME version does not force, which
// is what makes a resume cheap.
func (d *buildDriver) Apply(ctx context.Context, _ Answers, out io.Writer) error {
	stacks, err := config.LoadStacks(d.stackRoot)
	if err != nil {
		return err
	}
	chain, _, err := build.Chain(stacks, d.stack)
	if err != nil {
		return err
	}
	steps, err := build.Plan(ctx, chain, build.Target{Name: d.stack, Ref: d.stack},
		d.force, &build.SbxImages{Runner: d.runner})
	if err != nil {
		return err
	}
	// DenHome, not stackRoot: the build's scratch directories and its mixin
	// cache belong to the personal den home, while the stack definitions come
	// from the source. Conflating the two would write into a git checkout den
	// updates by fast-forward.
	return build.Execute(ctx, steps, build.Deps{Sbx: d.runner, DenHome: d.denHome}, out, out)
}

// Verify re-asks sbx for the image. force is deliberately ignored here: the
// question after a build is "does the image exist now", and answering "no,
// because we would rebuild it" would fail every successful build.
func (d *buildDriver) Verify(ctx context.Context) (Observation, error) {
	return d.Inspect(ctx)
}

// Drivers builds the ordered driver list for one manifest.
//
// The ORDER is the application order of spec §9.2, and it is not arbitrary:
// credentials first (a build pulls from the registry they authenticate), then
// the machine's network policy (a build's egress must be allowed before it
// runs), then the images. Within each group, manifest order — a source that
// declares two credentials gets them applied in the order it wrote them.
type Drivers struct {
	// Name is the INSTALL name of the source on this machine, not
	// metadata.name: it is what every `den source configure <name>` remedy
	// must print, and `--name` may have overridden the recommendation.
	Name string
	// StackRoot is the source checkout the declared stacks are read from.
	StackRoot string
	DenHome   string
	Runner    sbx.Runner
	Secrets   sbx.SecretRunner
	// Force rebuilds declared images even when they exist.
	Force bool
}

// ForManifest instantiates one driver per declared resource, in application
// order, against a single observation of the machine.
func (f Drivers) ForManifest(m *source.Manifest, state *SbxState) []ResourceDriver {
	var out []ResourceDriver
	for _, c := range m.Resources.Credentials {
		out = append(out, &credentialDriver{
			res: c, state: state, runner: f.Runner, secrets: f.Secrets, source: f.Name,
		})
	}
	if len(m.Resources.BuildNetwork.Allow) > 0 {
		out = append(out, &networkDriver{
			allow: m.Resources.BuildNetwork.Allow, state: state, runner: f.Runner, source: f.Name,
		})
	}
	for _, b := range m.Resources.Builds {
		out = append(out, &buildDriver{
			stackRoot: f.StackRoot, stack: b.Stack, runner: f.Runner,
			denHome: f.DenHome, source: f.Name, force: f.Force,
		})
	}
	return out
}
