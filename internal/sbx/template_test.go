package sbx

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The real payload, copied from the 2026-07-31 survey (spec §14.0). Two
// images, one of them qualified the way sbx actually reports them.
const templateLsJSON = `{"images":[
  {"id":"11a2e5ef4234","repository":"docker.io/library/devx","tag":"v1",
   "flavor":"claude-code-docker","created_at":"2026-07-27T06:44:57Z","size":6477492753},
  {"id":"22b3f6a05555","repository":"ghcr.io/acme/dgdevx","tag":"v2",
   "flavor":"claude-code-docker","created_at":"2026-07-28T09:10:00Z","size":700}
]}`

func TestTemplatesDecodesTheSurveyedSchema(t *testing.T) {
	f := &Fake{Responses: map[string]Response{
		"template ls --json": {Output: []byte(templateLsJSON)},
	}}

	list, err := Templates(context.Background(), f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("got %d templates, want 2: %+v", len(list), list)
	}
	if list[0].Repository != "docker.io/library/devx" || list[0].Tag != "v1" {
		t.Errorf("first template = %+v", list[0])
	}
	if list[0].ID != "11a2e5ef4234" || list[0].Flavor != "claude-code-docker" {
		t.Errorf("first template = %+v", list[0])
	}
	if !f.HasCalled("template", "ls", "--json") {
		t.Errorf("calls = %v, want a `template ls --json`", f.Calls)
	}
}

// sbx's order is kept, NOT sorted: the listing is by creation time, which is
// more useful in a diagnostic than an alphabetical one, and sorting here would
// take the choice away from every caller.
func TestTemplatesKeepsSbxOrder(t *testing.T) {
	f := &Fake{Responses: map[string]Response{
		"template ls --json": {Output: []byte(templateLsJSON)},
	}}
	list, err := Templates(context.Background(), f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if list[0].Repository != "docker.io/library/devx" || list[1].Repository != "ghcr.io/acme/dgdevx" {
		t.Errorf("order = %q then %q, want sbx's own", list[0].Repository, list[1].Repository)
	}
}

// A valid JSON object WITHOUT the key must be an error, never a silent empty
// list — the doctrine Ls states. Silence costs a specific thing here: an empty
// inventory reads as "no image is built", so `den <nest>` would refuse every
// spawn on a machine where every image is present.
func TestTemplatesRefusesAJSONWithoutTheImagesKey(t *testing.T) {
	f := &Fake{Responses: map[string]Response{
		"template ls --json": {Output: []byte(`{"templates":[]}`)},
	}}
	_, err := Templates(context.Background(), f)
	if err == nil {
		t.Fatal("expected a refusal on a JSON without the `images` key")
	}
	if !strings.Contains(err.Error(), "images") || !strings.Contains(err.Error(), "templates") {
		t.Errorf("message = %q, want it to name the missing key AND quote the raw output", err)
	}
}

func TestTemplatesRefusesUnreadableJSON(t *testing.T) {
	f := &Fake{Responses: map[string]Response{
		"template ls --json": {Output: []byte("not json")},
	}}
	if _, err := Templates(context.Background(), f); err == nil {
		t.Fatal("expected a refusal on unreadable JSON")
	}
}

// The runner's error travels AS IS, like Ls: ExecError already renders the
// binary and the full argv, and a prefix here would write the subcommand twice.
func TestTemplatesDoesNotRepeatTheSubcommand(t *testing.T) {
	boom := errors.New("sbx template ls --json: exit status 1")
	f := &Fake{Default: Response{Err: boom}}
	_, err := Templates(context.Background(), f)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the runner's own error unwrapped", err)
	}
}

// ONE CASE PER FORM, as issue #8 asks: the matching between the single string
// a stack writes and the two fields sbx reports is the only real design
// question the issue had left.
func TestNormalizeImageRef(t *testing.T) {
	cases := []struct {
		ref      string
		wantRepo string
		wantTag  string
		why      string
	}{
		{"devx:v1", "docker.io/library/devx", "v1",
			"the common form: a stack writes it bare, sbx reports it qualified"},
		{"library/devx:v1", "docker.io/library/devx", "v1",
			"a namespace, not a registry: `library` has no dot and is not localhost"},
		{"docker.io/library/devx:v1", "docker.io/library/devx", "v1",
			"already qualified: untouched"},
		{"devx", "docker.io/library/devx", "latest",
			"no tag is `latest` everywhere else too, so it must be here"},
		{"ghcr.io/acme/devx:v2", "ghcr.io/acme/devx", "v2",
			"a first component with a dot is a registry host"},
		{"acme/devx:v1", "docker.io/acme/devx", "v1",
			"a first component without a dot is a namespace under the default registry"},
		{"localhost:5000/devx:v1", "localhost:5000/devx", "v1",
			"the colon before the last slash is a PORT, not a tag"},
		{"localhost/devx", "localhost/devx", "latest",
			"bare localhost is a registry by convention, dot or no dot"},
		{"docker.io/devx:v1", "docker.io/library/devx", "v1",
			"registry-qualified with ONE component still completes to `library`, the same as the bare form"},
		{"index.docker.io/devx:v1", "docker.io/library/devx", "v1",
			"index.docker.io is docker.io's other name to every OCI client — folded before completion, so this matches the exact same template"},
		{"index.docker.io/acme/devx:v2", "docker.io/acme/devx", "v2",
			"the fold is unconditional: a namespace already present is kept, only the registry name lands on the one form sbx reports"},
		{"ghcr.io/devx:v2", "ghcr.io/devx", "v2",
			"guard: ghcr.io has no `library` convention, so its one component stays a genuine repository, uncompleted"},
	}
	for _, c := range cases {
		t.Run(c.ref, func(t *testing.T) {
			repo, tag := NormalizeImageRef(c.ref)
			if repo != c.wantRepo || tag != c.wantTag {
				t.Errorf("NormalizeImageRef(%q) = (%q, %q), want (%q, %q) — %s",
					c.ref, repo, tag, c.wantRepo, c.wantTag, c.why)
			}
		})
	}
}

// A mistyped `devx:` is NOT `devx:latest`. Completing it would silently match
// an image the user did not name; travelling back as written makes the
// comparison fail and the message quote what they typed.
func TestNormalizeImageRefDoesNotCompleteAnEmptyTag(t *testing.T) {
	repo, tag := NormalizeImageRef("devx:")
	if tag != "" || repo != "devx:" {
		t.Errorf("NormalizeImageRef(%q) = (%q, %q), want the reference back untouched with no tag",
			"devx:", repo, tag)
	}
}

// A digest, spelled in full: the hex length is what makes the `:` after the
// `@` look exactly like a tag separator to the split NormalizeImageRef does.
const digest = "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// ONE CASE PER FORM again, for the pin: `image:` can legitimately carry a
// digest, and the tag split would turn every one of these into a repository
// and a tag that exist nowhere.
func TestNormalizeImageRefLeavesADigestPinUnmatchable(t *testing.T) {
	cases := []struct {
		ref string
		why string
	}{
		{"devx@" + digest,
			"the `:` sits after the last `/`, which is the tag split's exact trigger — but it opens the algorithm"},
		{"ghcr.io/acme/devx@" + digest,
			"a qualified pin is no more splittable: `acme/devx@sha256` is not a repository"},
		{"devx:v1@" + digest,
			"tag AND digest is a legal form, and the pin still decides which image is meant"},
	}
	for _, c := range cases {
		t.Run(c.ref, func(t *testing.T) {
			repo, tag := NormalizeImageRef(c.ref)
			// Untouched and tagless: sbx reports no digest, so den must not
			// name a repository/tag pair it cannot compare against anything.
			if repo != c.ref || tag != "" {
				t.Errorf("NormalizeImageRef(%q) = (%q, %q), want the reference back untouched with no tag — %s",
					c.ref, repo, tag, c.why)
			}
			if !IsDigestRef(c.ref) {
				t.Errorf("IsDigestRef(%q) = false, want true — %s", c.ref, c.why)
			}
		})
	}
}

// The negative side of the predicate, on the forms most likely to be confused
// with a pin: a colon alone never makes a digest.
func TestIsDigestRefOnTagReferences(t *testing.T) {
	for _, ref := range []string{"devx", "devx:v1", "ghcr.io/acme/devx:v2", "localhost:5000/devx:v1", "devx:"} {
		if IsDigestRef(ref) {
			t.Errorf("IsDigestRef(%q) = true, want false — no `@`, so it is a plain repository:tag", ref)
		}
	}
}

// Where the mis-parse would have bitten: `devx@sha256:…` split into the
// repository `devx@sha256` and the tag `<hex>` matched nothing either, but by
// accident. It must match nothing BY CONSTRUCTION, including on an inventory
// holding that very repository — sbx reports no digest, so a match would be den
// asserting an identity it never checked.
func TestFindTemplateNeverMatchesADigestPin(t *testing.T) {
	list := []Template{
		{Repository: "docker.io/library/devx", Tag: "v1"},
		{Repository: "ghcr.io/acme/dgdevx", Tag: "v2"},
	}
	for _, ref := range []string{
		"devx@" + digest,
		"docker.io/library/devx@" + digest,
		"ghcr.io/acme/dgdevx@" + digest,
		"devx:v1@" + digest,
	} {
		if got := FindTemplate(list, ref); got != nil {
			t.Errorf("FindTemplate(%q) = %+v, want nil — the inventory carries no digest to match it against",
				ref, got)
		}
	}
}

// The point of the normalization, stated as one assertion: the bare form a
// stack writes finds the qualified image sbx reports.
func TestFindTemplateMatchesAnUnqualifiedReference(t *testing.T) {
	list := []Template{
		{Repository: "docker.io/library/devx", Tag: "v1"},
		{Repository: "ghcr.io/acme/dgdevx", Tag: "v2"},
	}
	for _, ref := range []string{"devx:v1", "library/devx:v1", "docker.io/library/devx:v1"} {
		if got := FindTemplate(list, ref); got == nil {
			t.Errorf("FindTemplate(%q) = nil, want the docker.io/library/devx v1 template", ref)
		}
	}
	if got := FindTemplate(list, "devx:v2"); got != nil {
		t.Errorf("FindTemplate(%q) = %+v, want nil — the tag differs", "devx:v2", got)
	}
	if got := FindTemplate(list, "dgdevx:v2"); got != nil {
		t.Errorf("FindTemplate(%q) = %+v, want nil — the registry is ghcr.io, not the default one",
			"dgdevx:v2", got)
	}
}

// The docker.io namespace completion, exercised through FindTemplate rather
// than NormalizeImageRef alone: `docker.io/devx:v1` is the shape an OCI
// client resolves to `docker.io/library/devx`, and it must find the very
// template sbx reports under that qualified name.
func TestFindTemplateMatchesADockerIORegistryQualifiedReference(t *testing.T) {
	list := []Template{
		{Repository: "docker.io/library/devx", Tag: "v1"},
		{Repository: "ghcr.io/acme/dgdevx", Tag: "v2"},
	}
	if got := FindTemplate(list, "docker.io/devx:v1"); got == nil {
		t.Errorf("FindTemplate(%q) = nil, want the docker.io/library/devx v1 template", "docker.io/devx:v1")
	}
}

func TestFindTemplateOnAnEmptyInventory(t *testing.T) {
	if got := FindTemplate(nil, "devx:v1"); got != nil {
		t.Errorf("FindTemplate on an empty inventory = %+v, want nil", got)
	}
}
