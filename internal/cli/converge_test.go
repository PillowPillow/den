package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	den "github.com/PillowPillow/den"
	"github.com/PillowPillow/den/internal/doctor"
	"github.com/PillowPillow/den/internal/prompt"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/source"
	"github.com/PillowPillow/den/internal/worktree"
)

// The manifested fixture. One credential, one exported stack, one exported
// nest needing one repository — the smallest source that still exercises every
// wire this task adds: an input, a resource, a discovery and an export.
const fixtureManifest = `schema_version: 1
kind: source
metadata:
  name: dg
  version: 1.0.0
requires:
  den: ">=1.7.0"
exports:
  nests:
    - { name: api, path: nests/api.yaml }
  stacks:
    - { name: base, path: stacks/base/stack.yaml }
inputs:
  credentials:
    registry_token:
      prompt: "Registry token"
resources:
  credentials:
    - { id: github, type: sbx_github, scope: global }
    - id: registry
      type: sbx_registry
      scope: global
      host: registry.example.test:443
      value_from: { credential: registry_token }
  build_network:
    allow:
      - registry.example.test
  builds:
    - stack: base
`

const (
	fixtureRepoURL = "https://git.example.test/team/api.git"
	// The OPTIONAL repository: a nest stays ready without it, which is what
	// separates "this machine is missing something" from "this machine cannot
	// run this nest".
	fixtureOptionalURL = "https://git.example.test/team/front.git"
)

// makeManifestedSourceRepo builds a source repo carrying den-source.yaml and
// returns its file:// URL. Real git on a temporary directory, like every other
// source test in this package — den's own clone path is what is under test.
func makeManifestedSourceRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "team-source")
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("den-source.yaml", fixtureManifest)
	write("stacks/base/stack.yaml",
		"image: base:v1\nbase: claude\nprovision:\n  steps: [provision/base.sh]\n")
	write("stacks/base/provision/base.sh", "#!/bin/sh\ntrue\n")
	write("nests/api.yaml", "stack: base\nrepos:\n"+
		"  - { key: api, url: "+fixtureRepoURL+" }\n"+
		"  - { key: front, url: "+fixtureOptionalURL+", optional: true }\n")
	gitCmd(t, dir, "init", "-b", "main")
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-m", "init")
	return "file://" + dir
}

// makeWorkRepo creates a working checkout whose origin IS the repository the
// fixture nest declares, so discovery confirms it by remote — the one match
// kind den acts on without a human.
func makeWorkRepo(t *testing.T, parent, name string) string {
	t.Helper()
	return makeWorkRepoFor(t, parent, name, fixtureRepoURL)
}

func makeWorkRepoFor(t *testing.T, parent, name, remote string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "init", "-b", "main")
	gitCmd(t, dir, "remote", "add", "origin", remote)
	return dir
}

// convergedSbx is a machine on which everything the fixture declares is
// ALREADY configured: both credentials, the build egress, and the image.
//
// The tests around it lock the CLI WIRING — which files are written, which plan
// is printed, what a refused confirmation leaves behind — so starting from a
// converged machine keeps them about that. acceptance_test.go starts from an
// empty sbx.NewMachine() instead, which is where the mutations themselves are
// observed.
func convergedSbx() *sbx.Machine {
	m := sbx.NewMachine()
	m.Services["github"] = true
	m.Registries["registry.example.test:443"] = true
	m.Allowed["registry.example.test"] = true
	m.Images["base:v1"] = true
	return m
}

// convergeDeps wires the injected world of a convergence run: real git (file://
// remotes only), the fake machine, an environment holding the answer file's
// credential, and a pinned den version. IsTTY stays nil — these runs are
// non-interactive, which is what `--answers` plus `--yes` is for.
func convergeDeps(runner sbx.Runner) Deps {
	return Deps{
		Git: worktree.NewGit(),
		Sbx: runner,
		Getenv: func(k string) string {
			if k == "DEN_TEST_REGISTRY_TOKEN" {
				return "sentinel-token"
			}
			return ""
		},
		DenVersion: func() string { return "1.7.0" },
	}
}

// writeAnswerFile writes the answer file a scripted onboarding uses. The
// credential travels as an environment variable NAME — never a value.
func writeAnswerFile(t *testing.T, roots ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "answers.yaml")
	body := "repository_roots: [" + strings.Join(roots, ", ") + "]\n" +
		"credentials:\n  registry_token:\n    from_env: DEN_TEST_REGISTRY_TOKEN\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// runCLI executes the real root tree, so the flags are parsed off an actual
// command line and the wiring in root.go is exercised, not bypassed.
func runCLI(t *testing.T, d Deps, args ...string) (string, error) {
	t.Helper()
	root := NewRootCmdWith(d)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	return err == nil
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestInitWithSourceWritesTheSourceAwareHome is the acceptance shape of §8: one
// command, no terminal, and a home that holds the source's nests instead of the
// shipped example.
func TestInitWithSourceWritesTheSourceAwareHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "den")
	work := t.TempDir()
	makeWorkRepo(t, work, "api")
	url := makeManifestedSourceRepo(t)
	f := convergedSbx()

	out, err := runCLI(t, convergeDeps(f), "init",
		"--source", url, "--answers", writeAnswerFile(t, work), "--yes", "--den-home", home)
	if err != nil {
		t.Fatalf("init --source: %v\n%s", err, out)
	}

	// The global config is the source-aware example, byte for byte: no
	// defaults.stack, no placeholder nest.
	embedded, err := den.SourceAwareDenHome.ReadFile("examples/den-home-source/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(home, "config.yaml")); got != string(embedded) {
		t.Errorf("config.yaml is not the source-aware example:\n%s", got)
	}
	if exists(t, filepath.Join(home, "nests", "example.yaml")) {
		t.Error("`den init --source` created the placeholder nest: the source publishes the nests")
	}
	if !exists(t, filepath.Join(home, "sources", "dg", "den-source.yaml")) {
		t.Fatal("the source was not installed under sources/dg")
	}
	personal := readFile(t, filepath.Join(home, "source-config", "dg.yaml"))
	if !strings.Contains(personal, "api:") || !strings.Contains(personal, filepath.Join(work, "api")) {
		t.Errorf("the discovered repository was not mapped:\n%s", personal)
	}
	receipt := readFile(t, filepath.Join(home, "state", "sources", "dg.yaml"))
	if !strings.Contains(receipt, "status: ready") {
		t.Errorf("receipt is not ready:\n%s", receipt)
	}
	if !strings.Contains(out, "status: ready") {
		t.Errorf("the plan was not printed:\n%s", out)
	}
	// No leftover staging: a candidate that became a source leaves nothing
	// behind in the cache it was assembled in.
	if entries, err := os.ReadDir(filepath.Join(home, "cache", "sources")); err == nil && len(entries) > 0 {
		t.Errorf("staging directory still holds %d entries after a successful install", len(entries))
	}
}

// TestInitWithoutSourceStillCreatesTheEmbeddedExample is the regression the
// whole task hangs on: `den init` alone must keep shipping the example home.
func TestInitWithoutSourceStillCreatesTheEmbeddedExample(t *testing.T) {
	home := filepath.Join(t.TempDir(), "den")
	if _, err := runCLI(t, convergeDeps(convergedSbx()), "init", "--den-home", home); err != nil {
		t.Fatalf("init: %v", err)
	}
	for _, rel := range []string{"config.yaml", filepath.Join("nests", "example.yaml")} {
		if !exists(t, filepath.Join(home, rel)) {
			t.Errorf("`den init` no longer creates %s", rel)
		}
	}
	if exists(t, filepath.Join(home, "source-config")) {
		t.Error("`den init` wrote a source configuration for a home that has no source")
	}
}

// TestInitRefusesConvergenceFlagsWithoutASource keeps a contradiction
// rejectable from the flags alone, before any side effect (spec §6 ordering).
func TestInitRefusesConvergenceFlagsWithoutASource(t *testing.T) {
	home := filepath.Join(t.TempDir(), "den")
	out, err := runCLI(t, convergeDeps(convergedSbx()), "init", "--yes", "--den-home", home)
	if err == nil {
		t.Fatalf("expected a refusal:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--source") {
		t.Errorf("the refusal does not name the flag that is missing: %v", err)
	}
	if exists(t, filepath.Join(home, "config.yaml")) {
		t.Error("the refused init still created a home")
	}
}

// TestSourceAddManifestedConvergesUnderTheGivenName covers the manifested
// branch of `den source add`, and that `--name` beats metadata.name — the
// install name is a per-machine decision (spec §5.1).
func TestSourceAddManifestedConvergesUnderTheGivenName(t *testing.T) {
	home := filepath.Join(t.TempDir(), "den")
	work := t.TempDir()
	makeWorkRepo(t, work, "api")
	url := makeManifestedSourceRepo(t)

	out, err := runCLI(t, convergeDeps(convergedSbx()), "source", "add", url,
		"--name", "corp", "--answers", writeAnswerFile(t, work), "--yes", "--den-home", home)
	if err != nil {
		t.Fatalf("source add: %v\n%s", err, out)
	}
	if !exists(t, filepath.Join(home, "sources", "corp", "den-source.yaml")) {
		t.Fatal("--name did not override metadata.name")
	}
	if exists(t, filepath.Join(home, "sources", "dg")) {
		t.Error("the source was also installed under its recommended name")
	}
	if !exists(t, filepath.Join(home, "source-config", "corp.yaml")) {
		t.Error("no personal configuration was written for the installed source")
	}
}

// TestSourceAddLegacyKeepsTheOldPath: a source without den-source.yaml is
// installed exactly as before, and convergence writes nothing for it.
func TestSourceAddLegacyKeepsTheOldPath(t *testing.T) {
	home := filepath.Join(t.TempDir(), "den")
	url := makeSourceRepo(t)

	out, err := runCLI(t, convergeDeps(convergedSbx()), "source", "add", url,
		"--name", "corp", "--den-home", home)
	if err != nil {
		t.Fatalf("source add: %v\n%s", err, out)
	}
	if !strings.Contains(out, "installed") {
		t.Errorf("the legacy message is gone:\n%s", out)
	}
	if !exists(t, filepath.Join(home, "sources", "corp", "stacks", "devx", "stack.yaml")) {
		t.Fatal("the legacy source was not installed")
	}
	for _, rel := range []string{"source-config", filepath.Join("state", "sources")} {
		if exists(t, filepath.Join(home, rel)) {
			t.Errorf("a legacy source produced %s: only a manifested source is converged", rel)
		}
	}
}

// TestSourceConfigureMapsARepositoryClonedLater is the resume shape of §11.1:
// the same source, reconverged from the INSTALLED checkout, picks up a
// repository that did not exist at install time.
func TestSourceConfigureMapsARepositoryClonedLater(t *testing.T) {
	home := filepath.Join(t.TempDir(), "den")
	work := t.TempDir()
	url := makeManifestedSourceRepo(t)
	d := convergeDeps(convergedSbx())

	out, err := runCLI(t, d, "source", "add", url,
		"--answers", writeAnswerFile(t, work), "--yes", "--den-home", home)
	if err != nil {
		t.Fatalf("source add: %v\n%s", err, out)
	}
	if !strings.Contains(out, "not_ready") {
		t.Errorf("a nest whose repository is absent is not reported not_ready:\n%s", out)
	}

	makeWorkRepo(t, work, "api")
	out, err = runCLI(t, d, "source", "configure", "dg",
		"--answers", writeAnswerFile(t, work), "--yes", "--den-home", home)
	if err != nil {
		t.Fatalf("source configure: %v\n%s", err, out)
	}
	personal := readFile(t, filepath.Join(home, "source-config", "dg.yaml"))
	if !strings.Contains(personal, filepath.Join(work, "api")) {
		t.Errorf("configure did not map the repository cloned since:\n%s", personal)
	}
	// configure never contacts a remote: it converges what is installed.
	if entries, err := os.ReadDir(filepath.Join(home, "cache", "sources")); err == nil && len(entries) > 0 {
		t.Errorf("configure staged %d clone(s): it must read the installed checkout", len(entries))
	}
}

// TestSourceConfigureRefusesALegacySource: convergence needs a contract, and
// the refusal says which command does apply.
func TestSourceConfigureRefusesALegacySource(t *testing.T) {
	home := filepath.Join(t.TempDir(), "den")
	d := convergeDeps(convergedSbx())
	if _, err := runCLI(t, d, "source", "add", makeSourceRepo(t),
		"--name", "corp", "--den-home", home); err != nil {
		t.Fatal(err)
	}
	_, err := runCLI(t, d, "source", "configure", "corp", "--yes", "--den-home", home)
	if err == nil {
		t.Fatal("expected a refusal: a legacy source has nothing to converge")
	}
	if !strings.Contains(err.Error(), "den-source.yaml") {
		t.Errorf("the refusal does not name the missing contract: %v", err)
	}
}

// TestConvergenceWithoutYesAppliesNothing is the confirmation contract: no
// terminal and no `--yes` prints the whole plan, exits ZERO, and leaves the
// machine and the den home exactly as they were.
func TestConvergenceWithoutYesAppliesNothing(t *testing.T) {
	home := filepath.Join(t.TempDir(), "den")
	work := t.TempDir()
	makeWorkRepo(t, work, "api")
	f := convergedSbx()

	out, err := runCLI(t, convergeDeps(f), "init", "--source", makeManifestedSourceRepo(t),
		"--answers", writeAnswerFile(t, work), "--den-home", home)
	if err != nil {
		t.Fatalf("a printed plan is not a failure: %v\n%s", err, out)
	}
	if !strings.Contains(out, "RESOURCES") || !strings.Contains(out, "--yes") {
		t.Errorf("the plan or its remedy is missing:\n%s", out)
	}
	// Absences, one by one: AcquireCandidate legitimately creates its staging
	// directory before planning, so tree equality would fail for the wrong
	// reason.
	for _, rel := range []string{"config.yaml", filepath.Join("sources", "dg"),
		"source-config", filepath.Join("state", "sources")} {
		if exists(t, filepath.Join(home, rel)) {
			t.Errorf("a plan that was not confirmed created %s", rel)
		}
	}
	for _, mutating := range [][]string{{"secret", "set"}, {"secret", "set-custom"},
		{"policy", "allow"}, {"create"}} {
		if f.HasCalled(mutating...) {
			t.Errorf("a plan that was not confirmed ran `sbx %s`", strings.Join(mutating, " "))
		}
	}
}

// TestSourceAddPrintsTheManualRepoMigration: den tells the user how to move a
// legacy `repos:` entry into the source's personal configuration, and copies
// NOTHING — the global file is byte-identical afterwards (spec §6).
func TestSourceAddPrintsTheManualRepoMigration(t *testing.T) {
	home := filepath.Join(t.TempDir(), "den")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	embedded, err := den.SourceAwareDenHome.ReadFile("examples/den-home-source/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(home, "config.yaml")
	before := string(embedded) + "\nrepos:\n  api: " + filepath.Join(t.TempDir(), "api") + "\n"
	if err := os.WriteFile(global, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	// No repository root: discovery confirms nothing, so the key really is only
	// available in the global file.
	answers := writeAnswerFile(t, t.TempDir())

	out, err := runCLI(t, convergeDeps(convergedSbx()), "source", "add", makeManifestedSourceRepo(t),
		"--answers", answers, "--yes", "--den-home", home)
	if err != nil {
		t.Fatalf("source add: %v\n%s", err, out)
	}
	if !strings.Contains(out, filepath.Join(home, "source-config", "dg.yaml")) {
		t.Errorf("the migration block does not name the destination file:\n%s", out)
	}
	if !strings.Contains(out, "api:") {
		t.Errorf("the migration block does not carry the key to move:\n%s", out)
	}
	if got := readFile(t, global); got != before {
		t.Errorf("den modified the global configuration:\n%s", got)
	}
}

// publishFixture republishes the fixture source at a new version. The URL is a
// file:// remote, so its directory is the path it names.
func publishFixture(t *testing.T, url, version string) {
	t.Helper()
	dir := strings.TrimPrefix(url, "file://")
	body := strings.Replace(fixtureManifest, "version: 1.0.0", "version: "+version, 1)
	if err := os.WriteFile(filepath.Join(dir, "den-source.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-m", "publish "+version)
}

// installFixture creates a source-aware home converged on the fixture at
// 1.0.0, and returns the remote URL — the state every update, status and
// doctor test starts from.
//
// Through `den init --source`, not `den source add`: that is how such a home
// comes to exist, and a home assembled any other way would have no config.yaml
// for doctor to read.
func installFixture(t *testing.T, d Deps, home, work string) string {
	t.Helper()
	url := makeManifestedSourceRepo(t)
	out, err := runCLI(t, d, "init", "--source", url,
		"--answers", writeAnswerFile(t, work), "--yes", "--den-home", home)
	if err != nil {
		t.Fatalf("init --source: %v\n%s", err, out)
	}
	return url
}

// A greater version is a full convergence: plan, confirmation, then the
// checkout and the active version move together.
func TestSourceUpdateConvergesAGreaterVersion(t *testing.T) {
	home := filepath.Join(t.TempDir(), "den")
	work := t.TempDir()
	makeWorkRepo(t, work, "api")
	d := convergeDeps(convergedSbx())
	url := installFixture(t, d, home, work)

	publishFixture(t, url, "2.0.0")
	out, err := runCLI(t, d, "source", "update", "dg",
		"--answers", writeAnswerFile(t, work), "--yes", "--den-home", home)
	if err != nil {
		t.Fatalf("source update: %v\n%s", err, out)
	}
	if !strings.Contains(out, "2.0.0") {
		t.Errorf("the plan does not name the version being converged:\n%s", out)
	}
	personal := readFile(t, filepath.Join(home, "source-config", "dg.yaml"))
	if !strings.Contains(personal, "version: 2.0.0") {
		t.Errorf("the machine is not configured for the new version:\n%s", personal)
	}
	receipt := readFile(t, filepath.Join(home, "state", "sources", "dg.yaml"))
	if !strings.Contains(receipt, "status: ready") || !strings.Contains(receipt, "version: 2.0.0") {
		t.Errorf("receipt = %s", receipt)
	}
	if !strings.Contains(readFile(t, filepath.Join(home, "sources", "dg", "den-source.yaml")), "2.0.0") {
		t.Error("the installed checkout was not fast-forwarded")
	}
}

// The same version is not an update, whatever the commit says — and den says
// which of the two situations it found.
func TestSourceUpdateReportsUnchangedThenDrift(t *testing.T) {
	home := filepath.Join(t.TempDir(), "den")
	work := t.TempDir()
	makeWorkRepo(t, work, "api")
	d := convergeDeps(convergedSbx())
	url := installFixture(t, d, home, work)
	head := func() string {
		return readFile(t, filepath.Join(home, "sources", "dg", "den-source.yaml"))
	}
	before := head()

	out, err := runCLI(t, d, "source", "update", "dg", "--den-home", home)
	if err != nil {
		t.Fatalf("source update: %v\n%s", err, out)
	}
	if !strings.Contains(out, "1.0.0") || !strings.Contains(out, "unchanged") {
		t.Errorf("an up-to-date source is not reported unchanged:\n%s", out)
	}

	// The team pushes content without bumping the version.
	dir := strings.TrimPrefix(url, "file://")
	if err := os.WriteFile(filepath.Join(dir, "stacks", "base", "stack.yaml"),
		[]byte("image: base:v2\nbase: claude\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-m", "content without a version")

	out, err = runCLI(t, d, "source", "update", "dg", "--den-home", home)
	if err != nil {
		t.Fatalf("a drift is a warning, not a failure: %v\n%s", err, out)
	}
	if !strings.Contains(out, "1.0.0") {
		t.Errorf("the drift warning does not name the version:\n%s", out)
	}
	if head() != before {
		t.Error("den moved the checkout for a version that did not change")
	}
}

// den converges forward. A published version below the configured one is a
// refusal, and the checkout stays where it is.
func TestSourceUpdateRefusesADowngrade(t *testing.T) {
	home := filepath.Join(t.TempDir(), "den")
	work := t.TempDir()
	makeWorkRepo(t, work, "api")
	d := convergeDeps(convergedSbx())
	url := installFixture(t, d, home, work)
	before := readFile(t, filepath.Join(home, "source-config", "dg.yaml"))

	publishFixture(t, url, "0.9.0")
	_, err := runCLI(t, d, "source", "update", "dg", "--yes", "--den-home", home)
	if err == nil {
		t.Fatal("expected a refusal: 0.9.0 is older than the configured 1.0.0")
	}
	if !strings.Contains(err.Error(), "older") {
		t.Errorf("the refusal does not say what is wrong: %v", err)
	}
	if got := readFile(t, filepath.Join(home, "source-config", "dg.yaml")); got != before {
		t.Errorf("a refused update changed the personal configuration:\n%s", got)
	}
	if !strings.Contains(readFile(t, filepath.Join(home, "sources", "dg", "den-source.yaml")), "1.0.0") {
		t.Error("a refused update moved the checkout")
	}
}

// A LoadPersonal error that is not "the file does not exist" must not be read
// as "never configured here": DecideUpdate's first check is `configured ==
// ""`, and it returns UpdateConverge on that check before the downgrade
// refusal (c < 0) further down is ever reached. A machine configured for
// 1.0.0 whose personal file went corrupt would then accept a team publish of
// an OLDER version as a legitimate first install — exactly the defect this
// locks: the refusal must fire before DecideUpdate is ever called with an
// empty `configured`, not after it.
func TestSourceUpdateRefusesWhenThePersonalFileIsUnreadable(t *testing.T) {
	home := filepath.Join(t.TempDir(), "den")
	work := t.TempDir()
	makeWorkRepo(t, work, "api")
	d := convergeDeps(convergedSbx())
	url := installFixture(t, d, home, work)

	personalPath := filepath.Join(home, "source-config", "dg.yaml")
	before := readFile(t, personalPath)
	// An unknown key a strict decode refuses — the corruption a hand edit or a
	// half-written file can leave behind.
	corrupt := before + "bogus_field: true\n"
	if err := os.WriteFile(personalPath, []byte(corrupt), 0o600); err != nil {
		t.Fatal(err)
	}

	publishFixture(t, url, "0.9.0")
	_, err := runCLI(t, d, "source", "update", "dg", "--yes", "--den-home", home)
	if err == nil {
		t.Fatal("expected a refusal: den cannot tell whether 0.9.0 is a downgrade with the " +
			"personal file unreadable")
	}
	if strings.Contains(err.Error(), "den converges forward only") {
		t.Errorf("the refusal reached DecideUpdate's downgrade wording instead of stopping at "+
			"the unreadable file: %v", err)
	}
	if !strings.Contains(err.Error(), personalPath) {
		t.Errorf("the refusal does not name the unreadable file: %v", err)
	}
	if got := readFile(t, personalPath); got != corrupt {
		t.Errorf("a refused update touched the personal configuration:\n%s", got)
	}
	if !strings.Contains(readFile(t, filepath.Join(home, "sources", "dg", "den-source.yaml")), "1.0.0") {
		t.Error("a refused update moved the checkout")
	}
}

// `den source status` reports without asking anything, and its exit code
// carries the verdict: only blocked and unknown are failures (spec §12.1).
func TestSourceStatusExitsNonZeroOnlyWhenDenCannotUseTheSource(t *testing.T) {
	work := t.TempDir()
	makeWorkRepo(t, work, "api")

	t.Run("ready", func(t *testing.T) {
		home := filepath.Join(t.TempDir(), "den")
		d := convergeDeps(convergedSbx())
		installFixture(t, d, home, work)

		out, err := runCLI(t, d, "source", "status", "dg", "--den-home", home)
		if err != nil {
			t.Fatalf("a usable source is not a failure: %v\n%s", err, out)
		}
		if !strings.Contains(out, "status: ready") || !strings.Contains(out, "RESOURCES") {
			t.Errorf("status output:\n%s", out)
		}
	})

	t.Run("partially ready", func(t *testing.T) {
		home := filepath.Join(t.TempDir(), "den")
		d := convergeDeps(convergedSbx())
		installFixture(t, d, home, t.TempDir()) // no repository on this machine

		out, err := runCLI(t, d, "source", "status", "dg", "--den-home", home)
		if err != nil {
			t.Fatalf("a missing working repository is not a failure: %v\n%s", err, out)
		}
		if !strings.Contains(out, "partially_ready") || !strings.Contains(out, "not_ready") {
			t.Errorf("status output:\n%s", out)
		}
		if !strings.Contains(out, fixtureRepoURL) || !strings.Contains(out, "den source configure dg") {
			t.Errorf("the missing repository is reported without its url or its remedy:\n%s", out)
		}
	})

	t.Run("unknown", func(t *testing.T) {
		home := filepath.Join(t.TempDir(), "den")
		f := convergedSbx()
		d := convergeDeps(f)
		installFixture(t, d, home, work)
		// The machine stops answering — the shape the prototype observed when
		// Keychain access was denied.
		f.Fail["secret ls -g"] = errors.New("keychain access denied")

		out, err := runCLI(t, d, "source", "status", "dg", "--den-home", home)
		if err == nil {
			t.Fatalf("an unobservable machine must not exit zero:\n%s", out)
		}
		if strings.Contains(out, "absent") {
			t.Errorf("an unobserved resource was reported absent:\n%s", out)
		}
	})
}

// With no name, every installed source is reported, in sorted order, and a
// legacy source is named rather than silently skipped.
func TestSourceStatusReportsEverySourceInOrder(t *testing.T) {
	home := filepath.Join(t.TempDir(), "den")
	work := t.TempDir()
	makeWorkRepo(t, work, "api")
	d := convergeDeps(convergedSbx())
	installFixture(t, d, home, work)
	if _, err := runCLI(t, d, "source", "add", makeSourceRepo(t),
		"--name", "corp", "--den-home", home); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, d, "source", "status", "--den-home", home)
	if err != nil {
		t.Fatalf("source status: %v\n%s", err, out)
	}
	corp, dg := strings.Index(out, "corp"), strings.Index(out, "dg")
	if corp < 0 || dg < 0 {
		t.Fatalf("both sources must be reported:\n%s", out)
	}
	if corp > dg {
		t.Errorf("sources are not reported in sorted order:\n%s", out)
	}
	if !strings.Contains(out, "legacy") {
		t.Errorf("the legacy source is reported without saying why it has no status:\n%s", out)
	}
}

// doctor is where an unobservable machine must NOT read as healthy: the source
// check is unknown, the exit is non-zero, and "all good" never appears.
func TestDoctorReportsAnUnobservableSourceAsUnknown(t *testing.T) {
	home := filepath.Join(t.TempDir(), "den")
	work := t.TempDir()
	makeWorkRepo(t, work, "api")
	d := convergeDeps(convergedSbx())
	installFixture(t, d, home, work)

	blind := &sbx.Fake{Default: sbx.Response{Err: errors.New("keychain access denied")}}
	out, err := runDoctorWithSbx(t, home, doctor.FakeDeps(), blind)
	if err == nil {
		t.Fatalf("an unknown source must make doctor exit non-zero:\n%s", out)
	}
	if !strings.Contains(out, "source dg") || !strings.Contains(out, "unknown") {
		t.Errorf("the source check is missing from the report:\n%s", out)
	}
	if strings.Contains(out, "all good") {
		t.Errorf("doctor claimed a healthy machine it could not observe:\n%s", out)
	}
}

// The healthy case, so the check is not one that only ever fails.
func TestDoctorReportsAReadySource(t *testing.T) {
	home := filepath.Join(t.TempDir(), "den")
	work := t.TempDir()
	makeWorkRepo(t, work, "api")
	d := convergeDeps(convergedSbx())
	installFixture(t, d, home, work)

	out, err := runDoctorWithSbx(t, home, doctor.FakeDeps(), convergedSbx())
	if err != nil {
		t.Fatalf("a converged source must not fail doctor: %v\n%s", err, out)
	}
	if !strings.Contains(out, "[ok  ] source dg") {
		t.Errorf("the source check is missing or not ok:\n%s", out)
	}
}

// A machine den could not OBSERVE is refused before the confirmation, not
// after it.
//
// The regression this pins was reported from a colleague's fresh laptop: sbx
// requires a one-time `sbx policy init <profile>` before any policy command
// answers, so `policy ls` failed, every resource line read `observed: unknown
// (…)`, and den STILL printed the plan, prompted `apply this plan? [y/N]`, took
// the `y` and only refused inside Apply — after the applying receipt was
// written. den held the fatal fact before the prompt: asking anyway is the bug,
// and the sbx error is only its cause.
//
// Interactive on purpose (IsTTY true, `d.Prompt` a `prompt.Fake`): the
// non-interactive path refuses earlier, for a different reason — no terminal
// to collect a credential on — which would leave the confirmation itself
// untested.
func TestSourceAddRefusesAnUnobservableMachineBeforeConfirming(t *testing.T) {
	home := filepath.Join(t.TempDir(), "den")
	work := t.TempDir()
	makeWorkRepo(t, work, "api")
	f := convergedSbx()
	f.Fail["policy ls --type network --source local --decision allow --json"] = errors.New(
		"ERROR: global network policy has not been initialized")
	d := convergeDeps(f)
	d.IsTTY = func() bool { return true }
	// A Fake, not nil: a nil Prompter would make a regression PANIC on the
	// asking call rather than actually ask it, which would pass this test for
	// the wrong reason the moment convergeDeps starts wiring a Prompt of its
	// own. Recording Confirms is what lets the assertion below survive that.
	pf := &prompt.Fake{}
	d.Prompt = pf

	root := NewRootCmdWith(d)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"source", "add", makeManifestedSourceRepo(t),
		"--answers", writeAnswerFile(t, work), "--den-home", home})
	err := root.Execute()
	out := buf.String()

	if err == nil {
		t.Fatalf("den converged a machine it could not observe:\n%s", out)
	}
	if len(pf.Confirms) != 0 {
		t.Errorf("den asked to confirm a plan it already knew it could not apply:\n%s", out)
	}
	msg := err.Error() + "\n" + out
	if !strings.Contains(msg, "global network policy has not been initialized") {
		t.Errorf("the refusal hides the cause it was refused for:\n%s", msg)
	}
	if f.HasCalled("secret", "set") || f.HasCalled("policy", "allow") || f.HasCalled("create") {
		t.Errorf("den mutated a machine it could not observe: %v", f.Calls)
	}
	if exists(t, source.ReceiptPath(home, "dg")) {
		t.Errorf("den opened the applying window on a machine it could not observe")
	}
}

// den asks NOTHING on a machine it cannot observe.
//
// The test above passes `--answers`, which is exactly what hides this half:
// with a file supplying the roots and the credential, den has no question left
// to ask, so a refusal placed after the questions and one placed before them
// are indistinguishable there. This run carries no answer file at all — the
// fresh-laptop shape the 2026-08-18 report described — so the repository-roots
// question and the registry-token prompt are both live. den must refuse before
// either: a token typed into a run that was doomed before the question is a
// secret collected for nothing, and the human retypes it on the retry.
//
// The prompter is scripted with answers it must never consume. Without them a
// regression would fail on prompt.Fake's "no scripted answer left" instead of
// on the assertions below, which would pass this test for the wrong reason the
// day the refusal moves again.
func TestSourceAddAsksNothingOnAnUnobservableMachine(t *testing.T) {
	home := filepath.Join(t.TempDir(), "den")
	work := t.TempDir()
	makeWorkRepo(t, work, "api")
	f := convergedSbx()
	f.Fail["secret ls -g"] = errors.New("keychain access denied")
	d := convergeDeps(f)
	d.IsTTY = func() bool { return true }
	pf := &prompt.Fake{
		LineAnswers:    []string{work},
		SecretAnswers:  []string{"sentinel-token"},
		ConfirmAnswers: []bool{true},
	}
	d.Prompt = pf

	root := NewRootCmdWith(d)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetIn(strings.NewReader(""))
	root.SetArgs([]string{"source", "add", makeManifestedSourceRepo(t), "--den-home", home})
	err := root.Execute()
	out := buf.String()

	if err == nil {
		t.Fatalf("den converged a machine it could not observe:\n%s", out)
	}
	if !strings.Contains(err.Error(), "den will not converge a machine it cannot see") {
		t.Errorf("the refusal is not the unobservable one: %v\n%s", err, out)
	}
	if len(pf.Secrets) != 0 {
		t.Errorf("den collected a credential it was about to discard: %v", pf.Secrets)
	}
	// Two questions land in Fake.Lines now — the repository roots and the
	// repo choices — and this run must have asked neither: it was already lost
	// before the collector ran, and lost again before the second planning pass.
	if len(pf.Lines) != 0 {
		t.Errorf("den asked a line question on a run it had already lost: %v", pf.Lines)
	}
}

// blindingPrompter blinds the machine the first time den asks a Line question.
//
// It replaces a blindingReader that hooked stdin, back when resolveRepoChoices
// was den's only production stdin read; that question now goes through the
// Prompter, so the hook moved with it. The window is unchanged — the repository
// choice is asked between the two planning passes, so blinding the machine here
// reproduces the sbx daemon dying while the human answers, which is what the
// second guard covers. Driving it from the QUESTION rather than from a call
// counter keeps the test independent of how many times den observes the
// machine, which is exactly what this change alters.
//
// The blind fires BEFORE the answer is produced, for the same reason it fired
// before the Read returned: den must still be waiting on the human when the
// machine goes away.
//
// No mutex: the only goroutine that touches the Machine is the one calling
// Line, since den scans, plans and applies sequentially on the command's own
// goroutine.
type blindingPrompter struct {
	*prompt.Fake
	blind func()
	fired bool
}

func (p *blindingPrompter) Line(ctx context.Context, r prompt.LineRequest) (string, error) {
	if !p.fired {
		p.fired = true
		p.blind()
	}
	return p.Fake.Line(ctx, r)
}

// A machine that stops answering BETWEEN the two planning passes is refused
// too — the second plan is checked, not only the first.
//
// The first pass observed a healthy machine, so the early probe and the guard
// under it both passed. Then the human answered a repository choice, the daemon
// died, and the second pass — the one whose plan is actually printed and
// confirmed — came back all-`unknown`. Without a re-check den printed that plan,
// took the `y`, and refused from inside Apply with the applying receipt already
// written: the exact shape the first guard exists to prevent, reached through
// the one door that guard does not cover.
func TestConvergenceRefusesWhenTheMachineBecomesUnobservableMidRun(t *testing.T) {
	home := filepath.Join(t.TempDir(), "den")
	work := t.TempDir()
	// A directory named like the repository whose remote does NOT confirm it:
	// discovery leaves it unconfirmed, which is what makes den ask, and asking
	// is what opens the window this test drives.
	makeWorkRepoFor(t, work, "api", "https://git.example.test/someone-else/api.git")
	f := convergedSbx()
	d := convergeDeps(f)
	d.IsTTY = func() bool { return true }
	// One LineAnswer, because `api` is the single unconfirmed match — the
	// optional `front` is absent, and an absent repository has no candidate to
	// choose. ConfirmAnswers says yes: without the re-check den must get all the
	// way to Apply, which is the failure this pins. With it, Confirm is never
	// called.
	pf := &prompt.Fake{LineAnswers: []string{"1"}, ConfirmAnswers: []bool{true}}
	d.Prompt = &blindingPrompter{
		Fake: pf,
		blind: func() {
			f.Fail["secret ls -g"] = errors.New("the sbx daemon stopped answering")
		},
	}

	root := NewRootCmdWith(d)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"init", "--source", makeManifestedSourceRepo(t),
		"--answers", writeAnswerFile(t, work), "--den-home", home})
	err := root.Execute()
	out := buf.String()

	if err == nil {
		t.Fatalf("den converged a machine that went blind mid-run:\n%s", out)
	}
	if !strings.Contains(err.Error(), "den will not converge a machine it cannot see") {
		t.Errorf("the refusal came from somewhere other than the unobservable guard: %v\n%s", err, out)
	}
	// The blind must have fired on the repo-choice question, not on the
	// repository-roots one. Line has two production callers now, and this test
	// reaches the right one only because the answer file supplies the roots —
	// so the repo choice IS the first Line call, and blinding there falls
	// between the two planning passes. Should a future fixture make den ask for
	// roots as well, the blind would fire before the FIRST plan, the first
	// guard would refuse with the identical message, and this test would go on
	// passing without covering its own window.
	if len(pf.Lines) != 1 || !strings.Contains(pf.Lines[0].Question, "repo api") {
		t.Errorf("the machine went blind on the wrong question: %v", pf.Lines)
	}
	if len(pf.Confirms) != 0 {
		t.Errorf("den asked to confirm an all-unknown plan: %v", pf.Confirms)
	}
	if exists(t, source.ReceiptPath(home, "dg")) {
		t.Errorf("den opened the applying window on a machine it could not observe")
	}
}

// A bad answer file is refused BEFORE den asks the machine anything.
//
// den refuses on what files alone decide before it observes — the doctrine that
// orders the spawn sequence (spec §6), applied to a convergence. The machine
// here is blind AND the file is wrong: reporting the machine first would send
// the user to fix sbx, re-run, and only then learn the file names a credential
// the source does not declare. Two round trips for two faults den held from the
// start.
//
// The zero-call assertion is the load-bearing half. Asserting the message alone
// would still pass with the probe running first and its error swallowed
// somewhere, and this is the ordering the observability probe put at risk by
// landing above the collector.
func TestABadAnswerFileIsRefusedBeforeTheMachineIsAsked(t *testing.T) {
	home := filepath.Join(t.TempDir(), "den")
	path := filepath.Join(t.TempDir(), "answers.yaml")
	if err := os.WriteFile(path, []byte(
		"credentials:\n  ghost_token:\n    from_env: DEN_TEST_REGISTRY_TOKEN\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := convergedSbx()
	f.Fail["secret ls -g"] = errors.New("keychain access denied")
	d := convergeDeps(f)
	d.IsTTY = func() bool { return true }
	d.Prompt = &prompt.Fake{}

	out, err := runCLI(t, d, "source", "add", makeManifestedSourceRepo(t),
		"--answers", path, "--den-home", home)

	if err == nil {
		t.Fatalf("den accepted an answer file naming an undeclared credential:\n%s", out)
	}
	if !strings.Contains(err.Error(), "ghost_token") || !strings.Contains(err.Error(), path) {
		t.Errorf("the refusal names neither the credential nor the file: %v\n%s", err, out)
	}
	if len(f.Calls) != 0 {
		t.Errorf("den questioned the machine before settling the answer file: %v", f.Calls)
	}
}

// countCalls counts the sbx invocations whose argv starts with prefix.
func countCalls(m *sbx.Machine, prefix string) int {
	n := 0
	for _, c := range m.Calls {
		if strings.HasPrefix(strings.Join(c, " "), prefix) {
			n++
		}
	}
	return n
}

// One doctor run reads the machine ONCE, whatever the number of sources.
//
// Reported by the PR82 review (F10): Status re-read sbx per source, so a home
// with N manifested sources ran `secret ls -g` N times and `policy ls` N+1
// times. The verdict cannot differ between two sources: it is a fact about the
// machine, not about the source.
//
// The COUNTS are what this pins, and only them. What an unobservable machine
// prints is the other half of the same finding, pinned by
// TestDoctorPrintsTheUnobservableCauseOnce below.
func TestDoctorReadsTheMachineOnceForEverySource(t *testing.T) {
	home := filepath.Join(t.TempDir(), "den")
	work := t.TempDir()
	makeWorkRepo(t, work, "api")
	d := convergeDeps(convergedSbx())
	installFixture(t, d, home, work)
	if out, err := runCLI(t, d, "source", "add", makeManifestedSourceRepo(t),
		"--name", "corp", "--answers", writeAnswerFile(t, work), "--yes",
		"--den-home", home); err != nil {
		t.Fatalf("source add corp: %v\n%s", err, out)
	}

	// A machine of its own, so the counts below are doctor's alone and not the
	// two installations'.
	f := convergedSbx()
	out, err := runDoctorWithSbx(t, home, doctor.FakeDeps(), f)
	if err != nil {
		t.Fatalf("doctor: %v\n%s", err, out)
	}
	// Asserted before the counts: with a single source the counts hold by
	// accident, and a fixture that installed only one source would pass a test
	// that proves nothing.
	for _, name := range []string{"source dg", "source corp"} {
		if !strings.Contains(out, name) {
			t.Fatalf("%s is missing from the report — the counts below prove nothing:\n%s",
				name, out)
		}
	}
	if n := countCalls(f, "secret ls"); n != 1 {
		t.Errorf("`sbx secret ls` ran %d times, want 1: the observation is not shared", n)
	}
	// 2, not 1: networkPolicyChecks keeps its own read on purpose — it reports
	// sbx's own refusal, flattened, which is a different message from the one
	// ReadSbxState wraps.
	if n := countCalls(f, "policy ls"); n != 2 {
		t.Errorf("`sbx policy ls` ran %d times, want 2 (1 network check + 1 shared read)", n)
	}
}

// The other half of the same property: a home whose sources are all legacy
// reads the machine not at all. A legacy source declares nothing doctor could
// judge, and hoisting the observation out of the loop must not make den pay a
// subprocess for a loop that turns zero times.
func TestDoctorReadsNothingForALegacyOnlyHome(t *testing.T) {
	home := testDenHome(t)
	if out, err := runCLI(t, convergeDeps(convergedSbx()), "source", "add",
		makeSourceRepo(t), "--name", "corp", "--den-home", home); err != nil {
		t.Fatalf("source add corp: %v\n%s", err, out)
	}

	f := convergedSbx()
	out, err := runDoctorWithSbx(t, home, doctor.FakeDeps(), f)
	if err != nil {
		t.Fatalf("doctor: %v\n%s", err, out)
	}
	if n := countCalls(f, "secret ls"); n != 0 {
		t.Errorf("`sbx secret ls` ran %d times on a legacy-only home, want 0", n)
	}
}

// reportLine returns the single report line naming a check, so an assertion
// can be about THAT line rather than about the whole page — "the report
// contains X somewhere" would pass on a line meant for another source.
func reportLine(t *testing.T, out, name string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, name) {
			return line
		}
	}
	t.Fatalf("no report line for %q:\n%s", name, out)
	return ""
}

// The output half of F10: an unobservable machine states its cause ONCE.
//
// Every source's plan carries the same cause — one read feeds them all — so
// printing it per source repeated sbx's four-line refusal down the report and
// buried the verdicts (review PR82). doctor prints it on the `sbx policy`
// line; each source line points there and still reports its own unknown.
func TestDoctorPrintsTheUnobservableCauseOnce(t *testing.T) {
	home := filepath.Join(t.TempDir(), "den")
	work := t.TempDir()
	makeWorkRepo(t, work, "api")
	d := convergeDeps(convergedSbx())
	installFixture(t, d, home, work)
	if out, err := runCLI(t, d, "source", "add", makeManifestedSourceRepo(t),
		"--name", "corp", "--answers", writeAnswerFile(t, work), "--yes",
		"--den-home", home); err != nil {
		t.Fatalf("source add corp: %v\n%s", err, out)
	}

	blind := &sbx.Fake{Default: sbx.Response{Err: errors.New("keychain access denied")}}
	out, err := runDoctorWithSbx(t, home, doctor.FakeDeps(), blind)
	if err == nil {
		t.Fatalf("an unobservable machine must exit non-zero:\n%s", out)
	}
	if n := strings.Count(out, "keychain access denied"); n != 1 {
		t.Errorf("the cause is printed %d times, want exactly 1:\n%s", n, out)
	}
	// The dedup must not cost the verdict: each source still fails on its own
	// line, and still names where the cause is.
	for _, name := range []string{"source dg", "source corp"} {
		line := reportLine(t, out, name)
		if !strings.Contains(line, "unknown") {
			t.Errorf("%s no longer reports unknown: %q", name, line)
		}
		if !strings.Contains(line, "sbx") {
			t.Errorf("%s points at no carrier of the cause: %q", name, line)
		}
	}
}

// The other side of the dedup: den keeps repeating a cause NOTHING else in the
// report states.
//
// ReadSbxState gives up in four places, and only one of them — `policy ls`
// failing — also fails doctor's own `sbx policy` check. Here `secret ls -g`
// ANSWERS, with a header den does not parse (what a newer sbx changing that
// table looks like, per parseSecretList), so the policy check is a plain [ok]
// and no other line says why den is blind. Pointing at it would have deleted
// the cause from a report that exits 1 — worse than repeating it.
func TestDoctorRepeatsTheCauseNoOtherCheckStates(t *testing.T) {
	home := filepath.Join(t.TempDir(), "den")
	work := t.TempDir()
	makeWorkRepo(t, work, "api")
	d := convergeDeps(convergedSbx())
	installFixture(t, d, home, work)
	if out, err := runCLI(t, d, "source", "add", makeManifestedSourceRepo(t),
		"--name", "corp", "--answers", writeAnswerFile(t, work), "--yes",
		"--den-home", home); err != nil {
		t.Fatalf("source add corp: %v\n%s", err, out)
	}

	// The machine ANSWERS both calls; den cannot read the first one's table.
	unparsable := &sbx.Fake{Responses: map[string]sbx.Response{
		"secret ls -g": {Output: []byte("SCOPE KIND LABEL\n")},
		"policy ls --type network --source local --decision allow --json": {
			Output: []byte(`{"rules":[]}`)},
	}}
	out, err := runDoctorWithSbx(t, home, doctor.FakeDeps(), unparsable)
	if err == nil {
		t.Fatalf("an unobservable machine must exit non-zero:\n%s", out)
	}
	if !strings.Contains(out, "unrecognized table header") {
		t.Fatalf("the report exits 1 without ever naming the cause:\n%s", out)
	}
	if strings.Contains(out, "the sbx check above carries the cause") {
		t.Errorf("a source line points at a check that states nothing — "+
			"`sbx policy` answered [ok] here:\n%s", out)
	}
}

// Task 2's invariant, under the dedup above: a source that is ALSO blocked
// keeps its BLOCKING refusal as the printed explanation.
//
// The refusal is about the source — a spawn would raise it — so it is not the
// repeated machine cause the dedup targets. Blocked here by removing the
// personal file, which is what RequireUsable refuses on.
func TestDoctorKeepsTheBlockingRefusalOnAnUnobservableMachine(t *testing.T) {
	home := filepath.Join(t.TempDir(), "den")
	work := t.TempDir()
	makeWorkRepo(t, work, "api")
	d := convergeDeps(convergedSbx())
	installFixture(t, d, home, work)
	if err := os.Remove(filepath.Join(home, "source-config", "dg.yaml")); err != nil {
		t.Fatal(err)
	}

	blind := &sbx.Fake{Default: sbx.Response{Err: errors.New("keychain access denied")}}
	out, err := runDoctorWithSbx(t, home, doctor.FakeDeps(), blind)
	if err == nil {
		t.Fatalf("a blocked source must exit non-zero:\n%s", out)
	}
	line := reportLine(t, out, "source dg")
	if !strings.Contains(line, "not configured on this machine") {
		t.Errorf("the blocking refusal is gone from the source line: %q", line)
	}
	if strings.Contains(line, "keychain access denied") {
		t.Errorf("the machine cause is repeated on a blocked source line: %q", line)
	}
}
