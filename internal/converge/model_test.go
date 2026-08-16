package converge

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PillowPillow/den/internal/source"
)

func ready(id, kind string) ResourcePlan {
	return ResourcePlan{ID: id, Kind: kind, Action: ActionUnchanged, Known: true, Observed: "configured"}
}

func TestAggregateStatus(t *testing.T) {
	notReady := NestReadiness{Name: "leo", Status: source.NestNotReady, MissingRepos: []string{"api"}}
	isReady := NestReadiness{Name: "leo", Status: source.NestReady}

	cases := []struct {
		name      string
		resources []ResourcePlan
		nests     []NestReadiness
		want      Status
		succeeds  bool
	}{
		{"everything ready", []ResourcePlan{ready("github", KindCredential)}, []NestReadiness{isReady},
			source.StatusReady, true},
		{"a nest misses a required repo", []ResourcePlan{ready("github", KindCredential)}, []NestReadiness{notReady},
			source.StatusPartiallyReady, true},
		{"a managed resource cannot converge",
			[]ResourcePlan{{ID: "github", Kind: KindCredential, Action: ActionBlocked, Known: true}},
			[]NestReadiness{isReady}, source.StatusBlocked, false},
		{"den could not observe",
			[]ResourcePlan{{ID: "github", Kind: KindCredential, Action: ActionCreate}},
			[]NestReadiness{isReady}, source.StatusUnknown, false},
		{"unknown wins over a missing repo",
			[]ResourcePlan{{ID: "github", Kind: KindCredential, Action: ActionCreate}},
			[]NestReadiness{notReady}, source.StatusUnknown, false},
		{"blocked wins over a missing repo",
			[]ResourcePlan{{ID: "github", Kind: KindCredential, Action: ActionBlocked, Known: true}},
			[]NestReadiness{notReady}, source.StatusBlocked, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := AggregateStatus(c.resources, c.nests)
			if got != c.want {
				t.Errorf("AggregateStatus = %q, want %q", got, c.want)
			}
			if Succeeds(got) != c.succeeds {
				t.Errorf("Succeeds(%q) = %v, want %v", got, Succeeds(got), c.succeeds)
			}
		})
	}
}

// A nest is not_ready only for ITS OWN required repositories. The optional one
// and the one another nest needs must not touch its verdict — that asymmetry
// is the whole reason `optional:` exists.
func TestEvaluateReadiness(t *testing.T) {
	matches := []RepoMatch{
		{Requirement: RepoRequirement{Key: "api", RequiredBy: []string{"leo"}}, Kind: MatchAbsent},
		{Requirement: RepoRequirement{Key: "crm", OptionalFor: []string{"leo", "go-dgdev"}}, Kind: MatchAbsent},
		{Requirement: RepoRequirement{Key: "ops", RequiredBy: []string{"go-dgdev"}},
			Kind: MatchRemote, Path: "/dev/ops", Confirmed: true},
		{Requirement: RepoRequirement{Key: "docs", RequiredBy: []string{"leo"}},
			Kind: MatchName, Path: "/dev/docs"}, // found, but a guess: not mapped
	}
	got := EvaluateReadiness([]string{"leo", "go-dgdev"}, matches)

	if len(got) != 2 || got[0].Name != "go-dgdev" || got[1].Name != "leo" {
		t.Fatalf("readiness = %+v, expected both nests sorted by name", got)
	}
	if got[0].Status != source.NestReady {
		t.Errorf("go-dgdev = %+v, want ready: its only required repo is mapped", got[0])
	}
	if got[1].Status != source.NestNotReady {
		t.Fatalf("leo = %+v, want not_ready", got[1])
	}
	if strings.Join(got[1].MissingRepos, ",") != "api,docs" {
		t.Errorf("leo.MissingRepos = %v, want the two unconfirmed REQUIRED keys, sorted", got[1].MissingRepos)
	}
}

// Spec §12.3: every convergence error names the resource, the observed and
// expected states, what remains, and the exact command that resumes.
func TestResourceErrorNamesEveryPartOfTheContract(t *testing.T) {
	cause := errors.New("sbx exited 1")
	err := &ResourceError{
		Resource:  "gitlab-registry",
		Observed:  "absent",
		Expected:  "configured for gitlab.example.test:4567",
		Remaining: "den will retry it on the next configure",
		Resume:    "run `den source configure dg`",
		Cause:     cause,
	}
	for _, want := range []string{
		"gitlab-registry", "absent", "configured for gitlab.example.test:4567",
		"den will retry it", "den source configure dg", "sbx exited 1",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, expected a mention of %q", err.Error(), want)
		}
	}
	if !errors.Is(err, cause) {
		t.Error("the cause must stay inspectable through errors.Is")
	}
}

func planFixture() *Plan {
	p := &Plan{
		Source:        "dg",
		Version:       "1.0.0",
		TrustBoundary: "building stack base runs the provision scripts of source dg",
		Resources: []ResourcePlan{
			ready("github", KindCredential),
			{ID: "gitlab-registry", Kind: KindCredential, Action: ActionCreate, Known: true,
				Expected: "configured for gitlab.example.test:4567", Resume: "run `den source configure dg`"},
			{ID: "build_network", Kind: KindBuildNetwork, Action: ActionUpdate, Known: true,
				Observed: "1 of 2 hosts allowed", Expected: "cdn.example.test, acli.example.test"},
			{ID: "base", Kind: KindStackBuild, Action: ActionCreate, Known: true, Expected: "image base:v1"},
		},
		RepoMatches: []RepoMatch{
			{Requirement: RepoRequirement{Key: "api", URL: "https://gitlab.example.test/team/api.git",
				RequiredBy: []string{"leo"}}, Kind: MatchRemote, Path: "/dev/api", Confirmed: true},
			{Requirement: RepoRequirement{Key: "crm", URL: "https://gitlab.example.test/team/crm.git",
				RequiredBy: []string{"leo"}}, Kind: MatchAbsent},
		},
	}
	p.Nests = EvaluateReadiness([]string{"leo", "go-dgdev"}, p.RepoMatches)
	p.Status = AggregateStatus(p.Resources, p.Nests)
	return p
}

// Two plans built from the same facts must render identically, byte for byte:
// the acceptance tests compare output, and a user comparing two runs must see
// only real changes.
func TestRenderPlanIsDeterministic(t *testing.T) {
	var first, second strings.Builder
	RenderPlan(&first, planFixture())
	RenderPlan(&second, planFixture())
	if first.String() != second.String() {
		t.Fatalf("two identical plans rendered differently:\n%s\n---\n%s", first.String(), second.String())
	}
	out := first.String()
	for _, want := range []string{
		"source: dg  version: 1.0.0",
		"provision scripts of source dg", // the trust boundary, before confirmation
		"create    credential     gitlab-registry",
		"unchanged credential     github",
		"api",
		"crm",
		"leo",
		"go-dgdev",
		"status: partially_ready",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plan is missing %q:\n%s", want, out)
		}
	}
}

// A credential value must never reach a plan, a status or a receipt — not the
// value, and not the environment variable it came from (spec §10.2, §12.3).
func TestRenderedOutputCarriesNoSecret(t *testing.T) {
	p := planFixture()
	// The worst case: a resource whose observed state was carelessly built
	// from an answer. The redaction is on CredentialAnswer.String, so even
	// this renders hidden.
	answer := CredentialAnswer{FromEnv: "GLPAT", Value: "sentinel-secret"}
	p.Resources = append(p.Resources, ResourcePlan{
		ID: "gitlab-http", Kind: KindCredential, Action: ActionCreate, Known: true,
		Expected: "substitution for gitlab.example.test: " + answer.String(),
	})

	var plan, status strings.Builder
	RenderPlan(&plan, p)
	RenderStatus(&status, p)
	receipt := p.Receipt("0123456789abcdef", "sha256:abc", time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC))

	for _, rendered := range []string{plan.String(), status.String(), fmt.Sprint(receipt)} {
		if strings.Contains(rendered, "sentinel-secret") {
			t.Errorf("a credential value was rendered:\n%s", rendered)
		}
	}
	if !strings.Contains(plan.String(), Redacted) {
		t.Errorf("the hidden value must be visible as %q:\n%s", Redacted, plan.String())
	}
}

// An unobservable resource is rendered as unknown, never as absent: the two
// have different remedies, and the user acts on the word.
func TestRenderNeverCallsAnUnobservableResourceAbsent(t *testing.T) {
	p := &Plan{
		Source: "dg", Version: "1.0.0",
		Resources: []ResourcePlan{{ID: "github", Kind: KindCredential, Action: ActionCreate}},
	}
	p.Status = AggregateStatus(p.Resources, nil)

	var plan, status strings.Builder
	RenderPlan(&plan, p)
	RenderStatus(&status, p)
	for _, out := range []string{plan.String(), status.String()} {
		if !strings.Contains(out, "unknown") {
			t.Errorf("expected the unknown wording:\n%s", out)
		}
		if strings.Contains(out, "absent") {
			t.Errorf("an unobservable resource must not be reported as absent:\n%s", out)
		}
	}
}

// The status view is what a user reads to know what to do next: a missing
// repository names its url and the command that maps it.
func TestRenderStatusNamesTheRemedyForAMissingRepo(t *testing.T) {
	var out strings.Builder
	RenderStatus(&out, planFixture())
	for _, want := range []string{
		"status: partially_ready",
		"leo",
		"crm (https://gitlab.example.test/team/crm.git)",
		"den source configure dg",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status is missing %q:\n%s", want, out.String())
		}
	}
}

// The receipt den writes comes from the plan, so what it attests and what the
// user confirmed cannot drift.
func TestPlanReceiptMirrorsThePlan(t *testing.T) {
	p := planFixture()
	r := p.Receipt("0123456789abcdef", "sha256:abc", time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC))
	if r.Status != p.Status || r.Version != "1.0.0" || r.Commit != "0123456789abcdef" {
		t.Fatalf("receipt = %+v", r)
	}
	if r.Nests["leo"].Status != source.NestNotReady || r.Nests["go-dgdev"].Status != source.NestReady {
		t.Errorf("receipt nests = %+v", r.Nests)
	}
	if len(r.Nests["leo"].MissingRepos) != 1 || r.Nests["leo"].MissingRepos[0] != "crm" {
		t.Errorf("receipt leo = %+v", r.Nests["leo"])
	}
	if r.Resources.Stacks["base"] != source.ResourceReady {
		t.Errorf("receipt stacks = %+v", r.Resources.Stacks)
	}
	if r.Resources.Credentials != source.ResourceReady {
		t.Errorf("receipt credentials = %q", r.Resources.Credentials)
	}
}
