package build

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/config"
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

// buildableFixture is graph_test.go's fixture graph with the one thing that
// makes a stack buildable under the new model: `provision.steps` plus an
// origin (`base:` for the two roots, `parent:` already there for the rest).
//
// Kept SEPARATE from `fixture` rather than edited in place: `fixture` (Chain's
// own tests, in graph_test.go) exercises the walk and its ordering only, and
// must stay exactly as spare as that question needs — none of Chain's tests
// consult Buildable.
//
//	alpha ← beta
//	      ← gamma ← delta
//	zeta
var buildableFixture = map[string]string{
	"alpha": "image: alpha:v1\nbase: claude\nprovision:\n  steps: [./provision/setup.sh]\n",
	"beta":  "image: beta:v1\nparent: alpha\nprovision:\n  steps: [./provision/setup.sh]\n",
	"gamma": "image: gamma:v1\nparent: alpha\nprovision:\n  steps: [./provision/setup.sh]\n",
	"delta": "image: delta:v1\nparent: gamma\nprovision:\n  steps: [./provision/setup.sh]\n",
	"zeta":  "image: zeta:v1\nbase: claude\nprovision:\n  steps: [./provision/setup.sh]\n",
}

// plan is the whole `den build [target]` pipeline, minus the execution: load,
// Chain, Plan. A stack den cannot build says so directly in its YAML (no
// `provision.steps`) — see TestPlanSkipsANotBuildableStackAmongBuildableSiblings
// and its neighbours — there is no separate "scriptless" fixture shape to arm.
func plan(t *testing.T, files map[string]string, target string, force bool, images Images) []Step {
	t.Helper()
	chain, _, err := Chain(loadStacks(t, files), target)
	if err != nil {
		t.Fatalf("building the chain: %v", err)
	}
	steps, err := Plan(context.Background(), chain, LocalTarget(target), force, images)
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
	steps := plan(t, buildableFixture, "", false, images)

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
	steps := plan(t, buildableFixture, "delta", false, images)

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
	steps := plan(t, buildableFixture, "delta", false, images)

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
	steps := plan(t, buildableFixture, "delta", true, images)

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
	lenient := plan(t, buildableFixture, "delta", false, &fakeImages{present: present})
	forced := plan(t, buildableFixture, "delta", true, &fakeImages{present: present})

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
	chain, _, err := Chain(loadStacks(t, buildableFixture), "delta")
	if err != nil {
		t.Fatalf("building the chain: %v", err)
	}
	_, err = Plan(context.Background(), chain, LocalTarget("delta"), false,
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

// The complement of TestPlanSkipsAnAncestorWhoseImageIsBuilt: an ancestor
// whose image is NOT in the inventory is built, ahead of the target that
// derives from it.
//
// This test used to read "a stack with no `image:` is simply built — nothing
// can report an empty reference as present, so there is nothing to arbitrate
// and no special case to write", over a `base` fixture declaring no `image:`.
// True of Plan in isolation, false end-to-end: the empty reference then reached
// `sbx template save <n>-build ""` and, as a `parent:`, refused the CHILD over
// the child's own file. config.LoadStack now refuses an empty `image:`
// outright, so the shape that sentence described can no longer be loaded — and
// the assertion underneath it, which never depended on the emptiness, keeps its
// job under a name that says what it proves.
func TestPlanBuildsAnAncestorWhoseImageIsMissing(t *testing.T) {
	files := map[string]string{
		"base":  "image: base:v1\nbase: claude\nprovision:\n  steps: [./provision/base.sh]\n",
		"child": "image: child:v1\nparent: base\nprovision:\n  steps: [./provision/child.sh]\n",
	}
	images := &fakeImages{} // an empty inventory: neither image is built
	steps := plan(t, files, "child", false, images)
	if got := strings.Join(built(steps), ","); got != "base,child" {
		t.Errorf("built = %v, want base,child", built(steps))
	}
}

var _ Images = (*fakeImages)(nil)
var _ Images = (*SbxImages)(nil)

// A stack den cannot build is skipped and NAMED, never a refusal — the answer
// must match the spawn's silence (Task 2). The skip line carries the reason,
// and for this cause there is no --force that would help.
func TestPlanSkipsANotBuildableStack(t *testing.T) {
	pullable := &config.Stack{Name: "pulled", Image: "ghcr.io/acme/base:v3"}
	steps, err := Plan(context.Background(), []*config.Stack{pullable}, Target{}, false, nil)
	if err != nil {
		t.Fatalf("Plan refused a pullable stack: %v", err)
	}
	if len(steps) != 1 || steps[0].Build {
		t.Fatalf("steps = %+v, want one skipped step", steps)
	}
	if !strings.Contains(steps[0].Skipped, "provision.steps") {
		t.Errorf("skip reason %q does not name what is missing", steps[0].Skipped)
	}
}

// The one exception: the stack the user NAMED. A "skipped" line there would
// read as success for a build they asked for specifically.
func TestPlanRefusesANamedStackItCannotBuild(t *testing.T) {
	pullable := &config.Stack{Name: "pulled", Image: "ghcr.io/acme/base:v3"}
	_, err := Plan(context.Background(), []*config.Stack{pullable}, LocalTarget("pulled"), false, nil)
	if err == nil {
		t.Fatal("Plan accepted a named stack with no provision.steps")
	}
	for _, want := range []string{"pulled", "provision.steps"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// THE DEFECT THIS SPLIT EXISTS FOR, measured on the bench 2026-08-03: a den
// holding one pullable stack (`image:` and no provision.steps) plus buildable
// ones answered `den build` with a refusal naming the pullable one, and built
// NOTHING. den had already decided, in the spawn's own image check, that such
// a stack is not its business; Execute's pre-flight said the opposite.
//
// TestPlanSkipsANotBuildableStack above proves the single-stack case; this is
// the integration shape the defect actually had — a not-buildable stack
// SHARING A CHAIN with buildable ones.
func TestPlanSkipsANotBuildableStackAmongBuildableSiblings(t *testing.T) {
	files := map[string]string{
		"alpha": "image: alpha:v1\nbase: claude\nprovision:\n  steps: [./provision/setup.sh]\n",
		"beta":  "image: beta:v1\nparent: alpha\nprovision:\n  steps: [./provision/setup.sh]\n",
		"gamma": "image: gamma:v1\nparent: alpha\nprovision:\n  steps: [./provision/setup.sh]\n",
		"delta": "image: delta:v1\nparent: gamma\nprovision:\n  steps: [./provision/setup.sh]\n",
		"zeta":  "image: zeta:v1\n", // no provision.steps: declared, not buildable
	}
	steps := plan(t, files, "", false, &fakeImages{})

	if got := strings.Join(built(steps), ","); got != "alpha,beta,gamma,delta" {
		t.Errorf("built = %v, want every buildable stack", built(steps))
	}
	if got := strings.Join(skipped(steps), ","); got != "zeta" {
		t.Errorf("skipped = %v, want zeta — declared, not buildable", skipped(steps))
	}
	// And the reason does NOT offer --force, which would change nothing here.
	for _, s := range steps {
		if s.Stack.Name == "zeta" {
			if !strings.Contains(s.Skipped, "provision.steps") || strings.Contains(s.Skipped, "--force") {
				t.Errorf("zeta skipped for %q, want it to name provision.steps and not offer --force", s.Skipped)
			}
		}
	}
}

// Same for an ANCESTOR: `den build delta` must not demand provision.steps for
// a base image that is only ever pulled.
func TestPlanSkipsANotBuildableAncestor(t *testing.T) {
	files := map[string]string{
		"alpha": "image: alpha:v1\n", // no provision.steps: declared, not buildable
		"gamma": "image: gamma:v1\nparent: alpha\nprovision:\n  steps: [./provision/setup.sh]\n",
		"delta": "image: delta:v1\nparent: gamma\nprovision:\n  steps: [./provision/setup.sh]\n",
	}
	steps := plan(t, files, "delta", false, &fakeImages{})

	if got := strings.Join(built(steps), ","); got != "gamma,delta" {
		t.Errorf("built = %v, want gamma,delta", built(steps))
	}
	// It is skipped before any inventory question: there is nothing to build
	// whatever the answer would have been.
	images := &fakeImages{}
	plan(t, files, "delta", false, images)
	if slices.Contains(images.asked, "alpha:v1") {
		t.Errorf("inventory asked about a stack den cannot build (%v)", images.asked)
	}
}

// The one exception: the stack the user NAMED. `den build zeta` on a stack den
// cannot build is a request to refuse, not to answer with a skip line — doing
// nothing silently would read as success.
//
// TestPlanRefusesANamedStackItCannotBuild above covers the same refusal on a
// hand-built Stack with an empty Dir; this one goes through Chain/loadStacks
// so the message is checked against a REAL stack.yaml path.
func TestPlanRefusesANamedTargetWithNoProvisionSteps(t *testing.T) {
	files := map[string]string{"zeta": "image: zeta:v1\n"}
	chain, _, err := Chain(loadStacks(t, files), "zeta")
	if err != nil {
		t.Fatalf("building the chain: %v", err)
	}
	_, err = Plan(context.Background(), chain, LocalTarget("zeta"), false, &fakeImages{})
	if err == nil {
		t.Fatal("expected a refusal on a target den has no provision.steps for")
	}
	msg := err.Error()
	for _, want := range []string{
		`"zeta"`, "provision.steps", "zeta:v1", filepath.Join("stacks", "zeta", "stack.yaml"),
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message = %q, want it to contain %q", msg, want)
		}
	}
}

// Same defect as internal/spawn's image refusal, one command removed: the
// remedy has to name the reference the USER typed. `den build delta --force`
// on a den that also owns a local `delta` addresses that local stack, and
// says nothing about the source stack the plan was actually arbitrating.
func TestPlanRemedyNamesTheSourceReference(t *testing.T) {
	chain, _, err := Chain(loadStacks(t, buildableFixture), "delta")
	if err != nil {
		t.Fatalf("building the chain: %v", err)
	}
	_, err = Plan(context.Background(), chain, Target{Name: "delta", Ref: "corp:delta"}, false,
		&fakeImages{err: errors.New("sbx template ls --json: exit status 1")})
	if err == nil {
		t.Fatal("expected a refusal when the image inventory cannot be read")
	}
	msg := err.Error()
	if !strings.Contains(msg, "den build corp:delta --force") {
		t.Errorf("message = %q, want the prefixed remedy", msg)
	}
	if strings.Contains(msg, "den build delta --force") {
		t.Errorf("message = %q names the BARE stack, which addresses a different (local) stack", msg)
	}
}
