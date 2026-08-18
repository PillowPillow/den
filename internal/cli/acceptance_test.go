package cli

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/sbx"
)

// This file is the tracer bullet: the whole onboarding, through the REAL cobra
// tree, against an sbx that starts empty and changes as den configures it.
//
// It lives in internal/cli rather than internal/converge (where the plan put
// it) because the flow under test IS the command tree: internal/cli imports
// internal/converge, so an acceptance test invoking cli.NewRootCmdWith from
// inside converge would be an import cycle.
//
// It duplicates no unit test. The unit tests fix one behavior each on a
// converged machine; this one fixes the SEQUENCE — what den does first, what it
// refuses to do at all, and what survives an interruption.

// noCredentialLeak walks the whole den home and fails if any file carries the
// sentinel value.
//
// The whole home, not just the streams: the constraint is that a credential
// never enters a plan, a log, an error, the personal configuration or a receipt
// (spec §5.3), and only a sweep proves the negative for the files den wrote
// along the way.
func noCredentialLeak(t *testing.T, home, sentinel string) {
	t.Helper()
	err := filepath.WalkDir(home, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil // a file den did not write and cannot read is not evidence
		}
		if strings.Contains(string(raw), sentinel) {
			t.Errorf("%s carries the credential value", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// publishFixtureWith republishes the fixture at a new version, optionally
// adding one host to the egress its builds need — so an update has a resource
// to converge, the way a real version bump does.
func publishFixtureWith(t *testing.T, url, version, extraHost string) {
	t.Helper()
	dir := strings.TrimPrefix(url, "file://")
	body := strings.Replace(fixtureManifest, "version: 1.0.0", "version: "+version, 1)
	if extraHost != "" {
		body = strings.Replace(body, "      - registry.example.test",
			"      - registry.example.test\n      - "+extraHost, 1)
	}
	if err := os.WriteFile(filepath.Join(dir, "den-source.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-m", "publish "+version)
}

// mutationOrder reduces what the machine was asked to do to the sequence spec
// §9.2 fixes: credentials, then the machine's network policy, then the images.
// Inspections are dropped — they happen throughout, and ordering them says
// nothing.
func mutationOrder(m *sbx.Machine) []string {
	var out []string
	for _, c := range m.Calls {
		switch {
		case c[0] == "secret" && c[1] != "ls":
			out = append(out, "credential")
		case c[0] == "policy" && c[1] == "allow":
			out = append(out, "policy")
		case c[0] == "create":
			out = append(out, "build")
		}
	}
	return out
}

// The whole flow on a fresh machine: one command, no terminal, and a den home
// plus an sbx configured for the source.
//
// github is the ONE exception to "nothing configured": since Task 5
// (internal/cli/answers.go), den refuses BEFORE applying anything when a
// declared sbx_github credential is genuinely absent and there is no
// terminal — it takes no `value_from` at all (manifest.go's own refusal), so
// sbx always collects it interactively, through the Attach Task 4 wired, and
// a command with no terminal could never complete it here either. This
// test's subject is the OTHER machinery a fresh laptop still needs — mutation
// order, the source-aware home, the personal mapping, the receipt, nest
// usability — so it starts from a machine on which a human already ran
// `sbx secret set github` once, the way a real provisioned laptop would.
func TestAcceptanceFreshHomeConvergesEverythingTheSourceDeclares(t *testing.T) {
	home := filepath.Join(t.TempDir(), "den")
	work := t.TempDir()
	makeWorkRepo(t, work, "api")
	makeWorkRepoFor(t, work, "front", fixtureOptionalURL)
	url := makeManifestedSourceRepo(t)
	m := sbx.NewMachine() // nothing configured, except github — see the comment above
	m.Services["github"] = true

	out, err := runCLI(t, convergeDeps(m), "init", "--source", url,
		"--answers", writeAnswerFile(t, work), "--yes", "--den-home", home)
	if err != nil {
		t.Fatalf("init --source: %v\n%s", err, out)
	}

	// 1. The order of the mutations is the safety property: a build pulls
	// through the registry credential and needs the egress. github is
	// pre-configured (see the function comment above), so the only credential
	// mutation this run makes is the registry one — still enough to prove the
	// ordering.
	order := mutationOrder(m)
	lastCredential, firstPolicy, firstBuild := -1, -1, -1
	for i, kind := range order {
		switch kind {
		case "credential":
			lastCredential = i
		case "policy":
			if firstPolicy < 0 {
				firstPolicy = i
			}
		case "build":
			if firstBuild < 0 {
				firstBuild = i
			}
		}
	}
	if lastCredential < 0 || firstPolicy < 0 || firstBuild < 0 {
		t.Fatalf("mutation order = %v, want credentials, the policy and a build", order)
	}
	if firstPolicy < lastCredential || firstBuild < firstPolicy {
		t.Errorf("mutation order = %v, want every credential, then the policy, then the build", order)
	}

	// 2. The machine really holds what the source declared. github is NOT
	// asserted here (M2, final whole-branch review, 2026-08-16): the fixture
	// seeds it four lines above this test's start, so `m.Services["github"]`
	// can no longer fail — an always-true clause inside a result assertion
	// invites the next reader to believe this run configured it, when the
	// function comment above already says it did not.
	if !m.Registries["registry.example.test:443"] {
		t.Errorf("credentials = %v", m.Registries)
	}
	// The STORED form, with the port sbx appends to a portless host — that is
	// what a later inspection compares against (sbx.NormalizeNetworkResource).
	if !m.Allowed["registry.example.test:443"] {
		t.Errorf("allowed hosts = %v", m.Allowed)
	}
	if !m.Images["base:v1"] {
		t.Errorf("images = %v, want the declared stack built", m.Images)
	}

	// 3. The den home is the source-aware one, scoped to this source.
	if exists(t, filepath.Join(home, "nests", "example.yaml")) ||
		exists(t, filepath.Join(home, "stacks")) {
		t.Error("`den init --source` shipped the example home: the source publishes the catalogue")
	}
	personal := readFile(t, filepath.Join(home, "source-config", "dg.yaml"))
	for _, want := range []string{filepath.Join(work, "api"), filepath.Join(work, "front")} {
		if !strings.Contains(personal, want) {
			t.Errorf("personal configuration does not map %s:\n%s", want, personal)
		}
	}
	receipt := readFile(t, filepath.Join(home, "state", "sources", "dg.yaml"))
	if !strings.Contains(receipt, "status: ready") || !strings.Contains(receipt, "version: 1.0.0") {
		t.Errorf("receipt:\n%s", receipt)
	}

	// 4. The source is usable straight away, and its nest resolves through the
	// source's own mapping.
	show, err := runCLI(t, convergeDeps(m), "nest", "show", "dg:api", "--den-home", home)
	if err != nil {
		t.Fatalf("the installed source is not usable: %v\n%s", err, show)
	}
	if !strings.Contains(show, filepath.Join(work, "api")) {
		t.Errorf("the nest does not resolve its repo key through the source mapping:\n%s", show)
	}

	// 5. Nothing anywhere carries the credential.
	if strings.Contains(out, "sentinel-token") {
		t.Errorf("the plan or the application printed the credential:\n%s", out)
	}
	noCredentialLeak(t, home, "sentinel-token")
}

// A required repository this machine does not have is `partially_ready`: the
// installation SUCCEEDS, and only the required key is named — an optional one
// changes nothing (spec §7.2).
func TestAcceptancePartialInstallNamesOnlyTheRequiredRepository(t *testing.T) {
	home := filepath.Join(t.TempDir(), "den")
	work := t.TempDir() // neither repository is here
	m := sbx.NewMachine()
	// github pre-configured: see TestAcceptanceFreshHomeConvergesEverythingTheSourceDeclares's
	// comment — this run has no terminal either, and the repository readiness
	// under test here has nothing to do with that credential.
	m.Services["github"] = true

	out, err := runCLI(t, convergeDeps(m), "init", "--source", makeManifestedSourceRepo(t),
		"--answers", writeAnswerFile(t, work), "--yes", "--den-home", home)
	if err != nil {
		t.Fatalf("a missing working repository must not fail the installation: %v\n%s", err, out)
	}
	if !strings.Contains(out, "partially_ready") {
		t.Errorf("status:\n%s", out)
	}

	status, err := runCLI(t, convergeDeps(m), "source", "status", "dg", "--den-home", home)
	if err != nil {
		t.Fatalf("partially_ready is not a failure: %v\n%s", err, status)
	}
	if !strings.Contains(status, "missing repo: api") {
		t.Errorf("the required repository is not named:\n%s", status)
	}
	if strings.Contains(status, "missing repo: front") || strings.Contains(status, "front (") {
		t.Errorf("an optional repository was reported as missing:\n%s", status)
	}
}

// An initialized den home is never rewritten (spec §8), and `den source
// configure` picks up a repository cloned since — without contacting the
// remote, which the test proves by deleting it.
func TestAcceptanceExistingHomeSurvivesAndConfigureCompletesIt(t *testing.T) {
	home := filepath.Join(t.TempDir(), "den")
	work := t.TempDir()
	url := makeManifestedSourceRepo(t)
	m := sbx.NewMachine()
	// github pre-configured: see TestAcceptanceFreshHomeConvergesEverythingTheSourceDeclares's
	// comment — the initial `init --source` below has no terminal either, and
	// what this test proves (an existing home surviving, configure completing
	// it later) has nothing to do with that credential.
	m.Services["github"] = true

	// A den home the user already had, with settings of their own.
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	before := "agents:\n  claude:\n    config_dir: /tmp/mine/claude\ndefaults:\n  agent: claude\n" +
		"egress:\n  - api.anthropic.com\n"
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, convergeDeps(m), "init", "--source", url,
		"--answers", writeAnswerFile(t, work), "--yes", "--den-home", home)
	if err != nil {
		t.Fatalf("init --source: %v\n%s", err, out)
	}
	if got := readFile(t, filepath.Join(home, "config.yaml")); got != before {
		t.Errorf("den rewrote an existing global configuration:\n%s", got)
	}

	// The repository is cloned afterwards, and the remote is gone — configure
	// converges what is INSTALLED, so neither is a problem.
	makeWorkRepo(t, work, "api")
	if err := os.RemoveAll(strings.TrimPrefix(url, "file://")); err != nil {
		t.Fatal(err)
	}
	out, err = runCLI(t, convergeDeps(m), "source", "configure", "dg",
		"--answers", writeAnswerFile(t, work), "--yes", "--den-home", home)
	if err != nil {
		t.Fatalf("source configure: %v\n%s", err, out)
	}
	if !strings.Contains(out, "status: ready") {
		t.Errorf("configure did not complete the installation:\n%s", out)
	}
	if !strings.Contains(readFile(t, filepath.Join(home, "source-config", "dg.yaml")),
		filepath.Join(work, "api")) {
		t.Error("the repository cloned since was not mapped")
	}
}

// The update sequence, end to end: a refused confirmation changes nothing, an
// interrupted application leaves the previous version active and every consumer
// refusing, and `den source configure` finishes it.
func TestAcceptanceUpdateRefusedThenInterruptedThenResumed(t *testing.T) {
	home := filepath.Join(t.TempDir(), "den")
	work := t.TempDir()
	makeWorkRepo(t, work, "api")
	// The optional repository is here too, so the only thing that can make a
	// consumer refuse below is the interrupted convergence itself.
	makeWorkRepoFor(t, work, "front", fixtureOptionalURL)
	m := sbx.NewMachine()
	// github pre-configured: see TestAcceptanceFreshHomeConvergesEverythingTheSourceDeclares's
	// comment — the initial `init --source` below has no terminal either, and
	// the update/interrupt/resume sequence under test here has nothing to do
	// with that credential.
	m.Services["github"] = true
	d := convergeDeps(m)

	url := makeManifestedSourceRepo(t)
	if out, err := runCLI(t, d, "init", "--source", url,
		"--answers", writeAnswerFile(t, work), "--yes", "--den-home", home); err != nil {
		t.Fatalf("init --source: %v\n%s", err, out)
	}
	personalPath := filepath.Join(home, "source-config", "dg.yaml")
	installedManifest := filepath.Join(home, "sources", "dg", "den-source.yaml")

	publishFixtureWith(t, url, "1.1.0", "new.example.test")

	// 1. No `--yes` and no terminal: the plan is printed, nothing is applied.
	out, err := runCLI(t, d, "source", "update", "dg",
		"--answers", writeAnswerFile(t, work), "--den-home", home)
	if err != nil {
		t.Fatalf("a printed plan is not a failure: %v\n%s", err, out)
	}
	if !strings.Contains(out, "1.1.0") || !strings.Contains(out, "--yes") {
		t.Errorf("the plan or its remedy is missing:\n%s", out)
	}
	// The fetch DID move the remote-tracking refs — that is unavoidable, the
	// version is in the fetched content. What must not have moved is the
	// checkout and the version this machine is configured for.
	if !strings.Contains(readFile(t, installedManifest), "1.0.0") {
		t.Error("a refused update moved the checkout")
	}
	if !strings.Contains(readFile(t, personalPath), "version: 1.0.0") {
		t.Error("a refused update changed the active version")
	}
	if m.Allowed["new.example.test:443"] {
		t.Error("a refused update configured the machine")
	}

	// 2. Confirmed, but the machine refuses one resource halfway.
	m.Fail["policy allow network new.example.test"] = errors.New("sbx refused")
	if _, err := runCLI(t, d, "source", "update", "dg",
		"--answers", writeAnswerFile(t, work), "--yes", "--den-home", home); err == nil {
		t.Fatal("expected the failing resource to stop the update")
	}
	if !strings.Contains(readFile(t, personalPath), "version: 1.0.0") {
		t.Error("a half-applied version became the active one")
	}
	if !strings.Contains(readFile(t, filepath.Join(home, "state", "sources", "dg.yaml")),
		"status: applying") {
		t.Error("the interrupted run left no applying marker")
	}
	// Every consumer refuses while that marker is there.
	if _, err := runCLI(t, d, "nest", "show", "dg:api", "--den-home", home); err == nil {
		t.Error("a half-converged source must not be usable")
	}

	// 3. The resume, from the installed checkout.
	delete(m.Fail, "policy allow network new.example.test")
	out, err = runCLI(t, d, "source", "configure", "dg",
		"--answers", writeAnswerFile(t, work), "--yes", "--den-home", home)
	if err != nil {
		t.Fatalf("the resume must complete: %v\n%s", err, out)
	}
	if !strings.Contains(readFile(t, personalPath), "version: 1.1.0") {
		t.Error("the resume did not activate the new version")
	}
	if !m.Allowed["new.example.test:443"] {
		t.Error("the resume did not apply the resource that had failed")
	}
	if _, err := runCLI(t, d, "nest", "show", "dg:api", "--den-home", home); err != nil {
		t.Errorf("the converged source is not usable: %v", err)
	}
	noCredentialLeak(t, home, "sentinel-token")
}

// TestRealSourceAcceptance converges a REAL source checkout, named by
// DIGITALEO_DEN_ENV (or any other path put there), through the same command
// tree.
//
// Skipped by default, and hermetic when it runs: the checkout is copied into a
// temporary git remote, the den home is temporary, and sbx is the double. It
// reads nothing from ~/.den and mutates nothing outside t.TempDir() — a test
// that touched the developer's own machine would be unrunnable on the machine
// it is most useful on.
//
// What it asserts is derived from the manifest rather than hard-coded: this
// test travels with den, the source it converges does not, and a count written
// here would be wrong the first time the team publishes a nest.
func TestRealSourceAcceptance(t *testing.T) {
	checkout := os.Getenv("DIGITALEO_DEN_ENV")
	if checkout == "" {
		t.Skip("DIGITALEO_DEN_ENV is not set: this test converges a real source checkout")
	}
	remote := copyToRemote(t, checkout)
	home := filepath.Join(t.TempDir(), "den")
	m := sbx.NewMachine()
	// github pre-configured: see TestAcceptanceFreshHomeConvergesEverythingTheSourceDeclares's
	// comment — this run has no terminal either, and Task 5 (internal/cli/answers.go)
	// refuses before applying anything when a declared sbx_github credential is
	// genuinely absent and there is none. Unlike the other four acceptance
	// tests, what THIS manifest declares is not known here — it travels with
	// whatever checkout DIGITALEO_DEN_ENV names — so the seed is unconditional:
	// harmless if the real source declares no github credential, load-bearing
	// if it does.
	m.Services["github"] = true

	// Every declared input answered from the environment, with a sentinel this
	// test then hunts for across the whole den home.
	manifest := readFile(t, filepath.Join(remote, "den-source.yaml"))
	answers := filepath.Join(t.TempDir(), "answers.yaml")
	body := "repository_roots: []\ncredentials:\n"
	names := declaredCredentialInputs(manifest)
	for _, name := range names {
		body += "  " + name + ":\n    from_env: DEN_ACCEPTANCE_" + strings.ToUpper(name) + "\n"
	}
	if len(names) == 0 {
		body = "repository_roots: []\n"
	}
	if err := os.WriteFile(answers, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	d := convergeDeps(m)
	d.Getenv = func(string) string { return "sentinel-token" }

	out, err := runCLI(t, d, "init", "--source", "file://"+remote,
		"--answers", answers, "--yes", "--den-home", home)
	if err != nil {
		t.Fatalf("converging %s: %v\n%s", checkout, err, out)
	}
	// No repository root was given, so a source whose nests need repos comes
	// out partially_ready. Both are successes; blocked and unknown are not.
	if !strings.Contains(out, "status: ready") && !strings.Contains(out, "status: partially_ready") {
		t.Errorf("the source did not converge:\n%s", out)
	}
	if _, err := runCLI(t, d, "source", "status", "--den-home", home); err != nil {
		t.Errorf("source status: %v", err)
	}
	noCredentialLeak(t, home, "sentinel-token")
}

// declaredCredentialInputs reads the names under `inputs.credentials:` without
// decoding the manifest: this test must keep working against a schema newer
// than the den running it, and a strict decode would refuse one.
func declaredCredentialInputs(manifest string) []string {
	var out []string
	inInputs, inCredentials := false, false
	for _, line := range strings.Split(manifest, "\n") {
		switch {
		case line == "inputs:":
			inInputs = true
		case inInputs && strings.HasPrefix(line, "  credentials:"):
			inCredentials = true
		case inCredentials && strings.HasPrefix(line, "    ") && strings.HasSuffix(strings.TrimSpace(line), ":"):
			if name := strings.TrimSuffix(strings.TrimSpace(line), ":"); !strings.Contains(name, " ") {
				out = append(out, name)
			}
		case len(line) > 0 && !strings.HasPrefix(line, " "):
			inInputs, inCredentials = false, false
		}
	}
	return out
}

// copyToRemote copies a checkout's TRACKED content into a fresh git repository
// under t.TempDir(). A copy, not the checkout itself: den fetches from this
// remote and the real repository must come out of the test byte-identical,
// including its git state.
func copyToRemote(t *testing.T, checkout string) string {
	t.Helper()
	dest := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	err := filepath.WalkDir(checkout, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(checkout, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		if e.IsDir() {
			if e.Name() == ".git" {
				return fs.SkipDir
			}
			return os.MkdirAll(filepath.Join(dest, rel), 0o755)
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		info, statErr := e.Info()
		if statErr != nil {
			return statErr
		}
		return os.WriteFile(filepath.Join(dest, rel), raw, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dest, "init", "-b", "main")
	gitCmd(t, dest, "add", "-A")
	gitCmd(t, dest, "commit", "-m", "acceptance copy")
	return dest
}
