package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	den "github.com/PillowPillow/den"
	"github.com/PillowPillow/den/internal/sbx"
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
    - id: registry
      type: sbx_registry
      scope: global
      host: registry.example.test:443
      value_from: { credential: registry_token }
`

const fixtureRepoURL = "https://git.example.test/team/api.git"

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
	write("stacks/base/stack.yaml", "image: base:v1\nbase: claude\n")
	write("nests/api.yaml", "stack: base\nrepos:\n  - { key: api, url: "+fixtureRepoURL+" }\n")
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
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "init", "-b", "main")
	gitCmd(t, dir, "remote", "add", "origin", fixtureRepoURL)
	return dir
}

// convergedSbx is a machine on which everything the fixture declares is
// ALREADY configured.
//
// Deliberately static: what these tests lock is the CLI wiring — which files
// are written, which plan is printed, what a refused confirmation leaves
// behind. The apply/verify loop against a machine that CHANGES is locked by
// internal/converge/service_test.go, on a mutable double; duplicating it here
// would test the engine twice and the wiring once.
func convergedSbx() *sbx.Fake {
	return &sbx.Fake{
		Responses: map[string]sbx.Response{
			"secret ls -g": {Output: []byte(
				"SCOPE   TYPE      NAME                       SECRET\n" +
					"global  registry  registry.example.test:443  (stored)\n")},
			"policy ls --type network --source local --decision allow --json": {
				Output: []byte(`{"rules":[]}`)},
		},
	}
}

// convergeDeps wires the injected world of a convergence run: real git (file://
// remotes only), the fake machine, an environment holding the answer file's
// credential, and a pinned den version. IsTTY stays nil — these runs are
// non-interactive, which is what `--answers` plus `--yes` is for.
func convergeDeps(f *sbx.Fake) Deps {
	return Deps{
		Git: worktree.NewGit(),
		Sbx: f,
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
