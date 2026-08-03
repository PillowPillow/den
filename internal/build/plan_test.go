package build

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/sbx"
)

// fakeImages is the injected inventory. It RECORDS what it was asked, because
// half of what Plan promises is about what it does NOT consult.
type fakeImages struct {
	present []string
	asked   []string
	err     error
}

func (f *fakeImages) Has(_ context.Context, image string) (bool, error) {
	f.asked = append(f.asked, image)
	if f.err != nil {
		return false, f.err
	}
	return slices.Contains(f.present, image), nil
}

// plan is the whole `den build [target]` pipeline, minus the execution.
func plan(t *testing.T, files map[string]string, target string, force bool, images Images) []Step {
	t.Helper()
	return planScriptless(t, files, target, force, images)
}

// planScriptless is plan with some stacks deprived of their build.sh — the
// "declared but not buildable" shape.
func planScriptless(t *testing.T, files map[string]string, target string, force bool, images Images,
	scriptless ...string) []Step {
	t.Helper()
	chain, err := Chain(loadStacks(t, files, scriptless...), target)
	if err != nil {
		t.Fatalf("building the chain: %v", err)
	}
	steps, err := Plan(context.Background(), chain, target, force, images)
	if err != nil {
		t.Fatalf("planning: %v", err)
	}
	return steps
}

func built(steps []Step) []string {
	var out []string
	for _, s := range steps {
		if s.Build {
			out = append(out, s.Stack.Name)
		}
	}
	return out
}

func skipped(steps []Step) []string {
	var out []string
	for _, s := range steps {
		if !s.Build {
			out = append(out, s.Stack.Name)
		}
	}
	return out
}

// `den build` with no argument is "everything, in topological order" (spec §6):
// nothing is an ancestor, so nothing is arbitrated and the inventory is never
// consulted — which is also why this form cannot be broken by an
// `sbx template ls` that fails.
func TestPlanWithoutATargetBuildsEverythingAndConsultsNothing(t *testing.T) {
	images := &fakeImages{present: []string{"alpha:v1", "gamma:v1", "delta:v1", "beta:v1", "zeta:v1"}}
	steps := plan(t, fixture, "", false, images)

	if got := strings.Join(built(steps), ","); got != "alpha,beta,gamma,delta,zeta" {
		t.Errorf("built = %v, want every stack in topological order", built(steps))
	}
	if len(images.asked) != 0 {
		t.Errorf("inventory consulted for %v, want no consultation at all", images.asked)
	}
}

// The table of spec §6: ancestors are skipped when their image is there, the
// TARGET never is — the user named it, and rebuilding on demand is what the
// command is for.
func TestPlanSkipsAnAncestorWhoseImageIsBuilt(t *testing.T) {
	images := &fakeImages{present: []string{"alpha:v1", "delta:v1"}}
	steps := plan(t, fixture, "delta", false, images)

	if got := strings.Join(built(steps), ","); got != "gamma,delta" {
		t.Errorf("built = %v, want gamma,delta — alpha is there, delta is the target", built(steps))
	}
	if got := strings.Join(skipped(steps), ","); got != "alpha" {
		t.Errorf("skipped = %v, want alpha", skipped(steps))
	}
	// The skipped step STAYS in the plan, and says why: a `den build delta`
	// that printed one line would leave "alpha was already built" and "den
	// forgot alpha" indistinguishable.
	if len(steps) != 3 || !strings.Contains(steps[0].Skipped, "alpha:v1") ||
		!strings.Contains(steps[0].Skipped, "--force") {
		t.Errorf("steps = %+v, want the skipped alpha kept and its reason naming its image", steps)
	}
}

// The target's own image is never consulted: it is built either way, and
// asking would be a process spent on a decision that is already made.
func TestPlanDoesNotConsultTheInventoryForTheTarget(t *testing.T) {
	images := &fakeImages{present: []string{"alpha:v1", "gamma:v1", "delta:v1"}}
	steps := plan(t, fixture, "delta", false, images)

	if got := strings.Join(built(steps), ","); got != "delta" {
		t.Errorf("built = %v, want delta only", built(steps))
	}
	if slices.Contains(images.asked, "delta:v1") {
		t.Errorf("inventory asked about the target's own image (%v)", images.asked)
	}
}

// `--force` propagates to the ancestors — and it does so by consulting
// nothing: the decision no longer depends on what is built.
func TestPlanForceRebuildsTheAncestorsWithoutConsulting(t *testing.T) {
	images := &fakeImages{present: []string{"alpha:v1", "gamma:v1", "delta:v1"}}
	steps := plan(t, fixture, "delta", true, images)

	if got := strings.Join(built(steps), ","); got != "alpha,gamma,delta" {
		t.Errorf("built = %v, want the whole chain", built(steps))
	}
	if len(images.asked) != 0 {
		t.Errorf("inventory consulted for %v under --force, want no consultation", images.asked)
	}
}

// The same chain, one flag apart. Stated as one assertion because that
// difference IS the flag's definition.
func TestPlanWithoutForceSkipsWhatForceRebuilds(t *testing.T) {
	present := []string{"alpha:v1", "gamma:v1"}
	lenient := plan(t, fixture, "delta", false, &fakeImages{present: present})
	forced := plan(t, fixture, "delta", true, &fakeImages{present: present})

	if got := strings.Join(built(lenient), ","); got != "delta" {
		t.Errorf("without --force, built = %v, want delta only", built(lenient))
	}
	if got := strings.Join(built(forced), ","); got != "alpha,gamma,delta" {
		t.Errorf("with --force, built = %v, want the whole chain", built(forced))
	}
}

// An inventory that fails is NOT fail-open, in either direction: skipping
// would produce the 403 this command exists to prevent, building anyway would
// spend minutes the user did not ask for. den refuses, and names the flag that
// makes the question go away.
func TestPlanRefusesWhenTheInventoryFails(t *testing.T) {
	chain, err := Chain(loadStacks(t, fixture), "delta")
	if err != nil {
		t.Fatalf("building the chain: %v", err)
	}
	_, err = Plan(context.Background(), chain, "delta", false,
		&fakeImages{err: errors.New("sbx template ls --json: exit status 1")})
	if err == nil {
		t.Fatal("expected a refusal when the image inventory cannot be read")
	}
	msg := err.Error()
	for _, want := range []string{`"alpha"`, "alpha:v1", "den build delta --force", "exit status 1"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message = %q, want it to contain %q", msg, want)
		}
	}
}

// SbxImages reads `sbx template ls --json` AT MOST ONCE, however many stacks
// it arbitrates: the answer costs a process, and the inventory does not change
// during a plan.
func TestSbxImagesReadsTheInventoryOnce(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"template ls --json": {Output: []byte(`{"images":[
			{"repository":"docker.io/library/alpha","tag":"v1"}]}`)},
	}}
	images := &SbxImages{Runner: f}

	yes, err := images.Has(context.Background(), "alpha:v1")
	if err != nil || !yes {
		t.Fatalf("Has(alpha:v1) = (%v, %v), want (true, nil)", yes, err)
	}
	no, err := images.Has(context.Background(), "gamma:v1")
	if err != nil || no {
		t.Fatalf("Has(gamma:v1) = (%v, %v), want (false, nil)", no, err)
	}

	calls := 0
	for _, c := range f.Calls {
		if len(c) > 0 && c[0] == "template" {
			calls++
		}
	}
	if calls != 1 {
		t.Errorf("`sbx template ls` called %d times, want exactly 1: %v", calls, f.Calls)
	}
}

// The failure is remembered too, and re-reported: retrying per stack would
// spawn one doomed process per ancestor and report the same failure N times.
func TestSbxImagesRemembersAFailure(t *testing.T) {
	f := &sbx.Fake{Default: sbx.Response{Err: errors.New("boom")}}
	images := &SbxImages{Runner: f}

	if _, err := images.Has(context.Background(), "alpha:v1"); err == nil {
		t.Fatal("expected the runner's failure")
	}
	if _, err := images.Has(context.Background(), "gamma:v1"); err == nil {
		t.Fatal("expected the remembered failure on the second call")
	}
	if len(f.Calls) != 1 {
		t.Errorf("calls = %v, want a single attempt", f.Calls)
	}
}

// A stack with no `image:` is simply built: nothing can report an empty
// reference as present, so there is nothing to arbitrate and no special case
// to write.
func TestPlanBuildsAStackWithoutAnImage(t *testing.T) {
	files := map[string]string{
		"base":  "parent: \n",
		"child": "image: child:v1\nparent: base\n",
	}
	images := &fakeImages{}
	steps := plan(t, files, "child", false, images)
	if got := strings.Join(built(steps), ","); got != "base,child" {
		t.Errorf("built = %v, want base,child", built(steps))
	}
}

var _ Images = (*fakeImages)(nil)
var _ Images = (*SbxImages)(nil)

// THE DEFECT THIS SPLIT EXISTS FOR, measured on the bench 2026-08-03: a den
// holding one pullable stack (`image:` and no build.sh) plus buildable ones
// answered `den build` with a refusal naming the pullable one, and built
// NOTHING. den had already decided, in the spawn's own image check, that such a
// stack is not its business; Execute's pre-flight said the opposite.
func TestPlanSkipsAStackWithNoBuildScript(t *testing.T) {
	steps := planScriptless(t, fixture, "", false, &fakeImages{}, "zeta")

	if got := strings.Join(built(steps), ","); got != "alpha,beta,gamma,delta" {
		t.Errorf("built = %v, want every buildable stack", built(steps))
	}
	if got := strings.Join(skipped(steps), ","); got != "zeta" {
		t.Errorf("skipped = %v, want zeta — declared, not buildable", skipped(steps))
	}
	// And the reason does NOT offer --force, which would change nothing here.
	for _, s := range steps {
		if s.Stack.Name == "zeta" {
			if !strings.Contains(s.Skipped, ScriptName) || strings.Contains(s.Skipped, "--force") {
				t.Errorf("zeta skipped for %q, want it to name %s and not offer --force", s.Skipped, ScriptName)
			}
		}
	}
}

// Same for an ANCESTOR: `den build delta` must not demand a build.sh for a base
// image that is only ever pulled.
func TestPlanSkipsAScriptlessAncestor(t *testing.T) {
	steps := planScriptless(t, fixture, "delta", false, &fakeImages{}, "alpha")

	if got := strings.Join(built(steps), ","); got != "gamma,delta" {
		t.Errorf("built = %v, want gamma,delta", built(steps))
	}
	// It is skipped before any inventory question: there is nothing to build
	// whatever the answer would have been.
	images := &fakeImages{}
	planScriptless(t, fixture, "delta", false, images, "alpha")
	if slices.Contains(images.asked, "alpha:v1") {
		t.Errorf("inventory asked about a stack den cannot build (%v)", images.asked)
	}
}

// The one exception: the stack the user NAMED. `den build zeta` on a stack den
// cannot build is a request to refuse, not to answer with a skip line — doing
// nothing silently would read as success.
func TestPlanRefusesANamedTargetWithNoBuildScript(t *testing.T) {
	chain, err := Chain(loadStacks(t, fixture, "zeta"), "zeta")
	if err != nil {
		t.Fatalf("building the chain: %v", err)
	}
	_, err = Plan(context.Background(), chain, "zeta", false, &fakeImages{})
	if err == nil {
		t.Fatal("expected a refusal on a target den has no build for")
	}
	msg := err.Error()
	for _, want := range []string{`"zeta"`, ScriptName, "zeta:v1"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message = %q, want it to contain %q", msg, want)
		}
	}
}
