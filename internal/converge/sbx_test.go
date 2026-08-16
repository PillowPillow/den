package converge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/source"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// stateFake answers the two inspection commands from the committed fixtures —
// the sanitized output of a real sbx v0.38.0 (prototype 2026-08-14).
func stateFake(t *testing.T) *sbx.Fake {
	t.Helper()
	return &sbx.Fake{Responses: map[string]sbx.Response{
		"secret ls -g": {Output: fixture(t, "secret-ls.txt")},
		"policy ls --type network --source local --decision allow --json": {
			Output: fixture(t, "policy-ls.json")},
	}}
}

func TestParseSbxVersion(t *testing.T) {
	cases := map[string]string{
		"sbx version: v0.38.0 c022b14634c4bea846ca12870d1d5e97d5868b54\n": "v0.38.0",
		"sbx version: v0.38.0\n":           "v0.38.0",
		"noise\nsbx version: v1.2.3 abc\n": "v1.2.3",
		"something else entirely\n":        "",
	}
	for output, want := range cases {
		if got := ParseSbxVersion(output); got != want {
			t.Errorf("ParseSbxVersion(%q) = %q, want %q", output, got, want)
		}
	}
}

// The two tables of `sbx secret ls -g`, read by column. den identifies a
// service by name, a registry by host, and a custom secret by targets and
// environment variable — and reads neither masked value nor placeholder.
func TestReadSbxStateParsesBothTables(t *testing.T) {
	state, err := ReadSbxState(context.Background(), stateFake(t))
	if err != nil {
		t.Fatalf("ReadSbxState: %v", err)
	}
	if !state.Services["github"] {
		t.Error("the (stored) service credential must count as present")
	}
	if !state.Services["anthropic"] {
		t.Error("an (oauth configured) service credential must count as present too")
	}
	if !state.Registries["registry.example.test:443"] {
		t.Errorf("registries = %v", state.Registries)
	}
	if !state.Customs[customKey("api.example.test", "EXAMPLE_TOKEN")] {
		t.Errorf("customs = %v", state.Customs)
	}
	if !state.AllowedHosts["api.example.test"] || !state.AllowedHosts["registry.example.test:443"] {
		t.Errorf("allowed hosts = %v", state.AllowedHosts)
	}
	if state.Services["registry.example.test:443"] {
		t.Error("a registry row must not be read as a service")
	}
}

// A layout den does not recognize is an observation ERROR, never an empty
// result: an empty state reads as "nothing is configured" and would make den
// reconfigure credentials that already exist.
func TestParseSecretListRefusesAnUnknownTable(t *testing.T) {
	_, err := parseSecretList("SCOPE      KIND       NAME\n(global)   service    github\n")
	if err == nil || !strings.Contains(err.Error(), "header") {
		t.Fatalf("parseSecretList = %v, expected a refusal naming the header", err)
	}
}

// An inspection that FAILS is `unknown`, never "absent" (prototype 2026-08-14:
// a restricted environment denied Keychain access and both commands exited 1
// on a machine whose credentials were perfectly configured).
func TestReadSbxStateReportsAnObserverFailure(t *testing.T) {
	f := &sbx.Fake{Default: sbx.Response{Err: errors.New("exit status 1")}}
	if _, err := ReadSbxState(context.Background(), f); err == nil {
		t.Fatal("a failing observation must not answer an empty state")
	}
}

func digitaleoManifest(t *testing.T) *source.Manifest {
	t.Helper()
	m, err := source.LoadManifest(validSourceTree(t))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// validSourceTree writes the tracer-bullet manifest: three credentials, two
// allowed hosts, one build.
func validSourceTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	// A BUILDABLE stack: `resources.builds` names it, and den only builds a
	// stack that declares what to run (config.Stack.Buildable).
	writeFile(t, filepath.Join(root, "stacks", "base", "stack.yaml"),
		"image: base:v1\nbase: claude\nprovision:\n  steps: [provision/base.sh]\n")
	writeFile(t, filepath.Join(root, "stacks", "base", "provision", "base.sh"), "#!/bin/sh\ntrue\n")
	writeFile(t, filepath.Join(root, "nests", "leo.yaml"), "stack: base\n")
	writeFile(t, filepath.Join(root, "den-source.yaml"), `schema_version: 1
kind: source
metadata: { name: dg, version: 1.0.0 }
exports:
  nests:
    - { name: leo, path: nests/leo.yaml }
  stacks:
    - { name: base, path: stacks/base/stack.yaml }
inputs:
  credentials:
    gitlab_token:
      prompt: GitLab personal access token
resources:
  credentials:
    - { id: github, type: sbx_github, scope: global }
    - id: gitlab_registry
      type: sbx_registry
      scope: global
      host: registry.example.test:443
      value_from: { credential: gitlab_token }
    - id: gitlab_http
      type: sbx_http_substitution
      scope: global
      host: gitlab.example.test
      environment: GITLAB_TOKEN
      value_from: { credential: gitlab_token }
  build_network:
    allow:
      - api.example.test
      - cdn.example.test
  builds:
    - stack: base
`)
	return root
}

// The drivers come out in the application order of spec §9.2 — credentials,
// then the machine policy, then the images — and each one plans from what the
// machine really answers.
func TestDriversPlanFromTheObservedMachine(t *testing.T) {
	f := stateFake(t)
	state, err := ReadSbxState(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	root := validSourceTree(t)
	m := digitaleoManifest(t)

	drivers := Drivers{Name: "dg", StackRoot: root, DenHome: t.TempDir(), Runner: f, Secrets: f}.
		ForManifest(m, state)
	if len(drivers) != 5 {
		t.Fatalf("drivers = %d, want three credentials, one policy group and one build", len(drivers))
	}

	var plans []ResourcePlan
	for _, d := range drivers[:4] { // the build driver is exercised on its own below
		o, err := d.Inspect(context.Background())
		if err != nil {
			t.Fatalf("Inspect: %v", err)
		}
		plans = append(plans, d.Plan(o))
	}
	if plans[0].ID != "github" || plans[0].Action != ActionUnchanged {
		t.Errorf("github = %+v, want unchanged: the fixture has it stored", plans[0])
	}
	if plans[1].ID != "gitlab_registry" || plans[1].Action != ActionUnchanged {
		t.Errorf("gitlab_registry = %+v, want unchanged: the fixture has that host", plans[1])
	}
	// The fixture's custom secret targets api.example.test, not
	// gitlab.example.test: this one really is absent.
	if plans[2].ID != "gitlab_http" || plans[2].Action != ActionCreate {
		t.Errorf("gitlab_http = %+v, want create", plans[2])
	}
	// One of the two declared hosts is already allowed: an update, not a
	// create, and the plan says how far along the machine is.
	if plans[3].Kind != KindBuildNetwork || plans[3].Action != ActionUpdate {
		t.Errorf("build_network = %+v, want update", plans[3])
	}
	if !strings.Contains(plans[3].Observed, "1 of 2") {
		t.Errorf("build_network observed = %q, want the count of allowed hosts", plans[3].Observed)
	}
}

// The three application commands, and where the secret travels: never in an
// argv den could avoid, and never in what a test double records.
func TestCredentialApplyKeepsTheValueOutOfArgv(t *testing.T) {
	f := stateFake(t)
	state, err := ReadSbxState(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	drivers := Drivers{Name: "dg", StackRoot: validSourceTree(t), DenHome: t.TempDir(), Runner: f, Secrets: f}.
		ForManifest(digitaleoManifest(t), state)

	answers := Answers{Credentials: map[string]CredentialAnswer{
		"gitlab_token": {FromEnv: "GLPAT", Value: "sentinel-secret"},
	}}
	var out strings.Builder
	for _, d := range drivers[:3] {
		if err := d.Apply(context.Background(), answers, &out); err != nil {
			t.Fatalf("Apply: %v", err)
		}
	}

	if !f.HasCalled("secret", "set", "-g", "github") {
		t.Errorf("expected the github command; calls: %v", f.Calls)
	}
	if !f.HasCalled("secret", "set", "-g", "--registry", "registry.example.test:443", "--password-stdin") {
		t.Errorf("expected the registry password to travel on stdin; calls: %v", f.Calls)
	}
	if len(f.Inputs) != 1 {
		t.Errorf("Inputs = %v, want exactly the registry password piped", f.Inputs)
	}
	if !f.HasCalled("secret", "set-custom", "-g", "--host", "gitlab.example.test",
		"--env", "GITLAB_TOKEN", "--value", sbx.RedactedArg) {
		t.Errorf("expected the custom secret with a redacted value; calls: %v", f.Calls)
	}
	// Nothing den recorded or printed may contain the value.
	for _, call := range f.Calls {
		for _, arg := range call {
			if strings.Contains(arg, "sentinel-secret") {
				t.Fatalf("the value reached an argv den records: %v", call)
			}
		}
	}
	if strings.Contains(out.String(), "sentinel-secret") {
		t.Errorf("the value was printed:\n%s", out.String())
	}
}

// A credential whose input was never answered is a ResourceError: it says what
// den wanted and how to supply it, instead of failing inside sbx.
func TestCredentialApplyRefusesAMissingAnswer(t *testing.T) {
	f := stateFake(t)
	state, err := ReadSbxState(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	drivers := Drivers{Name: "dg", StackRoot: validSourceTree(t), DenHome: t.TempDir(), Runner: f, Secrets: f}.
		ForManifest(digitaleoManifest(t), state)

	err = drivers[1].Apply(context.Background(), Answers{}, &strings.Builder{})
	var resErr *ResourceError
	if !errors.As(err, &resErr) {
		t.Fatalf("Apply = %v, want a *ResourceError", err)
	}
	for _, want := range []string{"gitlab_registry", "gitlab_token", "den source configure dg"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, expected a mention of %q", err.Error(), want)
		}
	}
}

// Applying the policy adds ONLY the hosts that are missing: reapplying the one
// already allowed would be a mutation den has no reason to make.
func TestNetworkApplyAddsOnlyTheMissingHosts(t *testing.T) {
	f := stateFake(t)
	state, err := ReadSbxState(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	drivers := Drivers{Name: "dg", StackRoot: validSourceTree(t), DenHome: t.TempDir(), Runner: f, Secrets: f}.
		ForManifest(digitaleoManifest(t), state)

	if err := drivers[3].Apply(context.Background(), Answers{}, &strings.Builder{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !f.HasCalled("policy", "allow", "network", "cdn.example.test") {
		t.Errorf("expected the missing host to be allowed; calls: %v", f.Calls)
	}
	if f.HasCalled("policy", "allow", "network", "api.example.test") {
		t.Errorf("an already-allowed host must not be reapplied; calls: %v", f.Calls)
	}
}

// Verify re-reads the machine rather than trusting the exit status of the
// command it just ran.
func TestVerifyReReadsTheMachine(t *testing.T) {
	f := stateFake(t)
	state, err := ReadSbxState(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	drivers := Drivers{Name: "dg", StackRoot: validSourceTree(t), DenHome: t.TempDir(), Runner: f, Secrets: f}.
		ForManifest(digitaleoManifest(t), state)

	before := len(f.Calls)
	if _, err := drivers[0].Verify(context.Background()); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(f.Calls) <= before {
		t.Error("Verify must observe the machine again, not reuse the pre-mutation reading")
	}
}

// The sbx service a github credential configures is decided by its TYPE, never
// by the `id:` the source chose.
//
// The two coincide in every manifest written so far, which is exactly what made
// this a trap: a source declaring `id: github-service` would have den inspect
// one service, configure another, then fail to verify what it had just applied —
// a resource permanently blocked by a name its author was free to pick.
func TestGithubCredentialIsKeyedByTypeNotByItsDeclaredID(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"secret ls -g": {Output: []byte(
			"SCOPE   TYPE     NAME    SECRET\nglobal  service  github  (stored)\n")},
		"policy ls --type network --source local --decision allow --json": {
			Output: []byte(`{"rules":[]}`)},
	}}
	state, err := ReadSbxState(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	d := &credentialDriver{
		res: source.CredentialResource{
			ID: "github-service", Type: source.CredentialGitHub, Scope: source.ScopeGlobal,
		},
		state: state, runner: f, secrets: f, source: "dg",
	}

	o, err := d.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !o.Present {
		t.Fatal("the configured github service was not seen: the driver keyed on the resource id")
	}
	if d.Plan(o).Action != ActionUnchanged {
		t.Errorf("action = %q, want unchanged on a machine that already has it", d.Plan(o).Action)
	}
}
