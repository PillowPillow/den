package converge

import (
	"context"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/sbx"
)

// buildFake answers the image inventory, and the sandbox listing every build
// pre-flight reads.
func buildFake(builtImages string) *sbx.Fake {
	return &sbx.Fake{Responses: map[string]sbx.Response{
		"template ls --json": {Output: []byte(`{"images":[` + builtImages + `]}`)},
		"ls --json":          {Output: []byte(`{"sandboxes":[]}`)},
	}}
}

func buildDriverFor(t *testing.T, f *sbx.Fake, force bool) ResourceDriver {
	t.Helper()
	drivers := Drivers{
		Name: "dg", StackRoot: validSourceTree(t), DenHome: t.TempDir(),
		Runner: f, Secrets: f, Force: force,
	}.ForManifest(digitaleoManifest(t), &SbxState{})
	return drivers[len(drivers)-1] // the declared build is last, per the application order
}

// An image that is already built and a source that has not moved is
// `unchanged`: that is what makes `den source configure` cheap enough to run
// after cloning one missing repository.
func TestBuildDriverSkipsAnExistingImage(t *testing.T) {
	f := buildFake(`{"repository":"docker.io/library/base","tag":"v1"}`)
	d := buildDriverFor(t, f, false)

	o, err := d.Inspect(context.Background())
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !o.Present {
		t.Fatalf("observation = %+v, want the image found", o)
	}
	if p := d.Plan(o); p.Action != ActionUnchanged || p.Kind != KindStackBuild || p.ID != "base" {
		t.Errorf("plan = %+v, want an unchanged stack build", p)
	}
}

// A first install and a version increase rebuild, even when an image of that
// name exists: nothing in sbx records WHICH version of the source produced a
// template, so an existing image is not evidence it matches this one.
func TestBuildDriverRebuildsWhenForced(t *testing.T) {
	f := buildFake(`{"repository":"docker.io/library/base","tag":"v1"}`)
	d := buildDriverFor(t, f, true)

	o, err := d.Inspect(context.Background())
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if p := d.Plan(o); p.Action != ActionUpdate {
		t.Errorf("plan = %+v, want update: the image exists but belongs to another version", p)
	}
}

// A missing image is a create, and the plan names the stack the user would be
// authorizing a provision run for.
func TestBuildDriverPlansAMissingImage(t *testing.T) {
	f := buildFake("")
	d := buildDriverFor(t, f, false)

	o, err := d.Inspect(context.Background())
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	p := d.Plan(o)
	if p.Action != ActionCreate {
		t.Errorf("plan = %+v, want create", p)
	}
	if !strings.Contains(p.Expected, "base") {
		t.Errorf("expected = %q, want the stack named", p.Expected)
	}
	if !strings.Contains(p.Resume, "den source configure dg") {
		t.Errorf("resume = %q, want the resume command", p.Resume)
	}
}

// An inventory den cannot read is an observation failure, not "no image": the
// service turns that into `unknown`, and a plan built on a wrong "absent"
// would rebuild every image on a machine that has them all.
func TestBuildDriverReportsAnUnreadableInventory(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"template ls --json": {Output: []byte(`{"unexpected":[]}`)},
	}}
	d := buildDriverFor(t, f, false)

	if _, err := d.Inspect(context.Background()); err == nil {
		t.Fatal("an unreadable inventory must be an error, never an empty answer")
	}
}
