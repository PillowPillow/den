package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/PillowPillow/den/internal/converge"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/source"
)

func answersManifest() *source.Manifest {
	return &source.Manifest{
		SchemaVersion: source.ManifestSchema,
		Kind:          "source",
		Metadata:      source.Metadata{Name: "dg", Version: "1.0.0"},
		Inputs: source.Inputs{Credentials: map[string]source.CredentialInput{
			"gitlab_token": {Prompt: "GitLab personal access token"},
		}},
	}
}

// credentialManifest declares actual RESOURCES on top of answersManifest's
// shape: a github credential (no input — sbx collects it interactively) and a
// registry credential fed by the "registry_token" input. It is what exercises
// stillMissingCredentials' machine-aware filtering, which answersManifest
// cannot: an input with no resource behind it has nothing for den to inspect.
func credentialManifest() *source.Manifest {
	return &source.Manifest{
		SchemaVersion: source.ManifestSchema,
		Kind:          "source",
		Metadata:      source.Metadata{Name: "dg", Version: "1.0.0"},
		Inputs: source.Inputs{Credentials: map[string]source.CredentialInput{
			"registry_token": {Prompt: "Registry token"},
		}},
		Resources: source.Resources{Credentials: []source.CredentialResource{
			{ID: "github", Type: source.CredentialGitHub, Scope: source.ScopeGlobal},
			{
				ID: "registry", Type: source.CredentialRegistry, Scope: source.ScopeGlobal,
				Host:      "registry.example.test:443",
				ValueFrom: source.ValueFrom{Credential: "registry_token"},
			},
		}},
	}
}

// answersCmd is a bare command carrying the streams collectInitialAnswers
// reads and writes: the function under test takes its input from the cobra
// command, never from the process.
//
// SetContext, so cmd.Context() answers context.Background() the way a command
// dispatched through cobra's own Execute always does (Command.Context's own
// doc: nil "Otherwise" — only before Execute or SetContext ran). Load-bearing
// since stillMissingCredentials threads this context into converge.ReadSbxState;
// a real Runner given a nil one would panic building its exec.Cmd, and even the
// Machine double, which ignores it, should not be handed one no production
// call path would ever produce.
func answersCmd(stdin string) (*cobra.Command, *bytes.Buffer) {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetIn(strings.NewReader(stdin))
	return cmd, out
}

// The two paths must produce the SAME typed answers: that equivalence is what
// makes the non-interactive flow a real rehearsal of the interactive one
// rather than a second implementation nobody exercises (spec §7.1).
func TestInteractiveAndAnswerFileProduceTheSameAnswers(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "answers.yaml")
	if err := os.WriteFile(path, []byte("repository_roots: ["+root+"]\n"+
		"credentials:\n  gitlab_token:\n    from_env: GLPAT\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	fileCmd, _ := answersCmd("")
	fromFile, err := collectInitialAnswers(fileCmd, Deps{
		Getenv: func(k string) string {
			if k == "GLPAT" {
				return "sentinel-secret"
			}
			return ""
		},
	}, answersManifest(), path, false)
	if err != nil {
		t.Fatalf("answer-file collection: %v", err)
	}

	var prompted []string
	interactiveCmd, out := answersCmd(root + "\n")
	fromPrompt, err := collectInitialAnswers(interactiveCmd, Deps{
		IsTTY: func() bool { return true },
		ReadSecret: func(prompt string) (string, error) {
			prompted = append(prompted, prompt)
			return "sentinel-secret", nil
		},
	}, answersManifest(), "", false)
	if err != nil {
		t.Fatalf("interactive collection: %v", err)
	}

	if len(fromPrompt.RepositoryRoots) != 1 || fromPrompt.RepositoryRoots[0] != fromFile.RepositoryRoots[0] {
		t.Errorf("roots = %v, want %v", fromPrompt.RepositoryRoots, fromFile.RepositoryRoots)
	}
	if fromPrompt.Credentials["gitlab_token"].Value != fromFile.Credentials["gitlab_token"].Value {
		t.Error("the two paths resolved different credential values")
	}
	// The manifest's own wording is what the user sees: a source that explains
	// which token it wants must not have that sentence replaced by a key name.
	if len(prompted) != 1 || !strings.Contains(prompted[0], "GitLab personal access token") {
		t.Errorf("prompts = %v, expected the manifest's own prompt", prompted)
	}
	// The value is typed, never echoed: nothing den printed may contain it.
	if strings.Contains(out.String(), "sentinel-secret") {
		t.Errorf("the credential was echoed:\n%s", out.String())
	}
}

// Without a terminal and without an answer file there is nobody to ask. den
// refuses rather than reading a pipe that will never answer — and names both
// flags, because a CI needs `--answers` AND `--yes` to get through.
func TestCollectInitialAnswersRefusesWithoutATerminal(t *testing.T) {
	cmd, _ := answersCmd("")
	_, err := collectInitialAnswers(cmd, Deps{}, answersManifest(), "", false)
	if err == nil {
		t.Fatal("expected a refusal with no terminal and no answer file")
	}
	for _, want := range []string{"--answers", "--yes", "gitlab_token"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, expected a mention of %q", err.Error(), want)
		}
	}
}

// A fully-answered file needs no terminal at all: that is the whole point of
// the automation path.
func TestCollectInitialAnswersNeedsNoTerminalWhenTheFileIsComplete(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "answers.yaml")
	if err := os.WriteFile(path, []byte("repository_roots: ["+root+"]\n"+
		"credentials:\n  gitlab_token:\n    from_env: GLPAT\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd, _ := answersCmd("")
	a, err := collectInitialAnswers(cmd, Deps{
		Getenv: func(string) string { return "sentinel-secret" },
	}, answersManifest(), path, false)
	if err != nil {
		t.Fatalf("collectInitialAnswers: %v", err)
	}
	if a.Credentials["gitlab_token"].Value != "sentinel-secret" {
		t.Errorf("credentials = %v", a.Credentials)
	}
}

// A registry credential genuinely absent from the machine still refuses
// without a terminal, and the refusal names the exact answer-file key that
// would supply it — the narrower half of Task 5's contract: fewer refusals,
// never a weaker one. `--yes` is set: this run intends to apply, which is
// exactly when a credential an answer file COULD have supplied must still
// block it.
func TestCollectInitialAnswersRefusesAGenuinelyAbsentRegistryCredential(t *testing.T) {
	m := sbx.NewMachine()
	m.Services["github"] = true // present: must not be named in this refusal

	cmd, _ := answersCmd("")
	_, err := collectInitialAnswers(cmd, Deps{Sbx: m}, credentialManifest(), "", true)
	if err == nil {
		t.Fatal("expected a refusal: the registry credential is absent and there is no terminal")
	}
	for _, want := range []string{"registry_token", "from_env"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, expected a mention of %q", err.Error(), want)
		}
	}
	if strings.Contains(err.Error(), "github") {
		t.Errorf("error = %q: github is already configured and must not be named", err.Error())
	}
}

// A github credential genuinely absent from the machine refuses too, but with
// a DIFFERENT remedy: it takes no `value_from` (manifest.go's own refusal),
// so sbx always collects it interactively, and the message must say a
// terminal is required rather than point at an answer-file key that could
// never have worked. `--yes` is set — see
// TestCollectInitialAnswersDoesNotRefuseOverAnAbsentGithubCredentialOnAPlanOnlyRun
// for why that matters here: without it, this run would never try to APPLY
// anything, and den has nothing to refuse yet.
func TestCollectInitialAnswersRefusesAnAbsentGithubCredentialNamingTheTerminal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "answers.yaml")
	if err := os.WriteFile(path,
		[]byte("credentials:\n  registry_token:\n    from_env: TEST_REGISTRY_TOKEN\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := sbx.NewMachine()
	m.Registries["registry.example.test:443"] = true // present: must not be named below

	cmd, _ := answersCmd("")
	_, err := collectInitialAnswers(cmd, Deps{
		Sbx:    m,
		Getenv: func(k string) string { return map[string]string{"TEST_REGISTRY_TOKEN": "tok"}[k] },
	}, credentialManifest(), path, true)
	if err == nil {
		t.Fatal("expected a refusal: github is absent and there is no terminal to configure it on")
	}
	if !strings.Contains(err.Error(), "terminal") {
		t.Errorf("error = %q, expected it to say a terminal is required", err.Error())
	}
	if strings.Contains(err.Error(), "registry_token") {
		t.Errorf("error = %q: the registry credential was answered and must not be named", err.Error())
	}
	if strings.Contains(err.Error(), "credentials.github") {
		t.Errorf("error = %q: github takes no answer-file key (manifest.go refuses value_from for it)",
			err.Error())
	}
}

// M1 (final whole-branch review, 2026-08-16): when ReadSbxState itself FAILS
// (not "github absent", but "den could not read sbx at all"),
// stillMissingCredentials' safe fallback still refuses — that verdict is
// right, and TestCollectInitialAnswersRefusesAnAbsentGithubCredentialNamingTheTerminal
// already locks it — but before this fix the message named the wrong cause:
// "cannot be configured without a terminal" sends the user to find a
// terminal they do not need, when the real fault is the failed read. The
// message must now name the read failure instead.
func TestCollectInitialAnswersNamesTheReadFailureWhenSbxIsUnreadable(t *testing.T) {
	readErr := errors.New("sbx: connection refused")
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"secret ls -g": {Err: readErr},
	}}

	cmd, _ := answersCmd("")
	_, err := collectInitialAnswers(cmd, Deps{Sbx: f}, credentialManifest(), "", true)
	if err == nil {
		t.Fatal("expected a refusal: sbx could not be read, so github must be assumed absent")
	}
	if !strings.Contains(err.Error(), readErr.Error()) {
		t.Errorf("error = %q, expected it to name the read failure %q", err.Error(), readErr.Error())
	}
	// Both must hold at once: naming the read failure is only the fix if it
	// REPLACES the misleading sentence for the same credential — a message
	// that dropped "github" entirely (rather than swapping its wording)
	// would pass a check for the read error alone.
	if !strings.Contains(err.Error(), `"github"`) {
		t.Errorf("error = %q, expected the github credential still named", err.Error())
	}
	if strings.Contains(err.Error(), "cannot be configured without a terminal") {
		t.Errorf("error = %q: the read failed, not github specifically — the old wording is misleading here",
			err.Error())
	}
}

// Without `--yes`, this run will not apply anything even once confirmed —
// confirm() only ever returns true here on `yes`, since there is no terminal
// to type "y" on — so an absent github credential is not yet a reason to
// refuse: Service.Plan's own doctrine is that the plan still lists everything
// the source declares, and this run must reach it. Caught in review of this
// very task: the first draft of the no-terminal refusal did not gate on
// `yes` and turned a `den init --source --answers <file>` PLAN into a
// refusal, on a machine with no github credential yet.
func TestCollectInitialAnswersDoesNotRefuseOverAnAbsentGithubCredentialOnAPlanOnlyRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "answers.yaml")
	root := t.TempDir()
	if err := os.WriteFile(path, []byte("repository_roots: ["+root+"]\n"+
		"credentials:\n  registry_token:\n    from_env: TEST_REGISTRY_TOKEN\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := sbx.NewMachine() // github absent

	cmd, _ := answersCmd("")
	_, err := collectInitialAnswers(cmd, Deps{
		Sbx:    m,
		Getenv: func(k string) string { return map[string]string{"TEST_REGISTRY_TOKEN": "tok"}[k] },
	}, credentialManifest(), path, false)
	if err != nil {
		t.Fatalf("a plan-only run must not refuse over a credential it will not try to configure: %v", err)
	}
}

// TestSourceConfigureResumesWhenTheMachineAlreadyHoldsTheCredentials is Task
// 5's central scenario: `den source configure --yes` must clear an `applying`
// receipt left by an interrupted run when every credential the manifest
// declares is already configured in sbx — without an answer file
// re-supplying a secret sbx already holds (spec §11.3).
//
// The applying receipt is written directly rather than produced by a real
// failure: Apply installs the checkout only after every resource of a FIRST
// install verifies (service.go's own Apply ordering comment), so a resource
// failure during `source add` never leaves an INSTALLED checkout for
// `source configure` to resume — the interruption this test needs can only
// follow a successful install (or an interrupted UPDATE, already covered by
// TestAcceptanceUpdateRefusedThenInterruptedThenResumed). Both converge on
// the same state a real interruption leaves: an installed checkout whose
// receipt still reads `status: applying`.
func TestSourceConfigureResumesWhenTheMachineAlreadyHoldsTheCredentials(t *testing.T) {
	home := filepath.Join(t.TempDir(), "den")
	work := t.TempDir()
	makeWorkRepo(t, work, "api")
	makeWorkRepoFor(t, work, "front", fixtureOptionalURL)
	url := makeManifestedSourceRepo(t)
	m := sbx.NewMachine()
	// github pre-configured: after Task 4, sbx collects it only through a real
	// terminal (Attach), and after THIS task den refuses before applying
	// anything when it is genuinely absent and there is none — the initial
	// `source add` below has no terminal either, so a machine starting without
	// it could never get past its own bootstrap. This test's subject is the
	// RESUME, not the first-time github bootstrap (which needs a human once,
	// interactively) — so it starts from a machine on which that already
	// happened, the way a real provisioned machine would.
	m.Services["github"] = true
	d := convergeDeps(m)

	if out, err := runCLI(t, d, "source", "add", url,
		"--answers", writeAnswerFile(t, work), "--yes", "--den-home", home); err != nil {
		t.Fatalf("source add: %v\n%s", err, out)
	}
	if !m.Registries["registry.example.test:443"] {
		t.Fatalf("the fixture install did not configure the registry credential: %v", m.Registries)
	}

	// An interruption, simulated directly (see the comment above): the
	// checkout is installed and every resource is really converged, but the
	// receipt says a run is still applying.
	if err := source.WriteReceipt(home, "dg",
		source.Receipt{Status: source.StatusApplying, TargetVersion: "1.0.0"}); err != nil {
		t.Fatal(err)
	}

	// The resume: no terminal (convergeDeps sets no IsTTY), and an answer file
	// carrying NO `credentials:` block at all — the escape this task removes
	// is having to re-supply a secret sbx already holds.
	noCreds := filepath.Join(t.TempDir(), "answers.yaml")
	if err := os.WriteFile(noCreds, []byte("repository_roots: ["+work+"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI(t, d, "source", "configure", "dg",
		"--answers", noCreds, "--yes", "--den-home", home)
	if err != nil {
		t.Fatalf("the resume must proceed without a terminal: %v\n%s", err, out)
	}
	receipt := readFile(t, filepath.Join(home, "state", "sources", "dg.yaml"))
	if !strings.Contains(receipt, "status: ready") {
		t.Errorf("the resume did not clear the applying receipt:\n%s", receipt)
	}
}

// A nil Getenv is an environment holding NOTHING, never the process's own: a
// test that forgot to wire it must fail on the missing variable instead of
// silently passing because the developer exported it.
func TestDepsGetenvIsHermeticByDefault(t *testing.T) {
	t.Setenv("GLPAT", "sentinel-secret")
	path := filepath.Join(t.TempDir(), "answers.yaml")
	if err := os.WriteFile(path, []byte("credentials:\n  gitlab_token:\n    from_env: GLPAT\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd, _ := answersCmd("")
	_, err := collectInitialAnswers(cmd, Deps{}, answersManifest(), path, false)
	if err == nil || !strings.Contains(err.Error(), "GLPAT") {
		t.Fatalf("error = %v, expected the unset-variable refusal", err)
	}
}

// An answer file naming a credential the source does not declare is refused,
// naming the file: the same judgment converge makes, surfaced where the user
// can act on it.
func TestCollectInitialAnswersRefusesAnUndeclaredCredential(t *testing.T) {
	path := filepath.Join(t.TempDir(), "answers.yaml")
	if err := os.WriteFile(path, []byte("repository_roots: [/tmp]\n"+
		"credentials:\n  ghost_token:\n    from_env: GHOST\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd, _ := answersCmd("")
	_, err := collectInitialAnswers(cmd, Deps{
		Getenv: func(string) string { return "sentinel-secret" },
	}, answersManifest(), path, false)
	if err == nil || !strings.Contains(err.Error(), "ghost_token") {
		t.Fatalf("error = %v, expected the undeclared credential to be named", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error = %q, expected the answer file to be named", err.Error())
	}
}

// Confirmation: `--yes` applies, a typed "n" does not, and no terminal means
// the plan is printed and nothing happens — never a default yes. changes
// defaults to true in every row but the two that test Plan.Changes() itself:
// a plan with nothing to create or update needs no confirmation, but — the
// case that matters most — it must NOT relax the no-terminal refusal, which
// stays strict regardless of changes (collectInitialAnswers reasons about
// that refusal being unconditional).
func TestConfirm(t *testing.T) {
	cases := []struct {
		name    string
		yes     bool
		tty     bool
		stdin   string
		changes bool
		want    bool
	}{
		{"--yes", true, false, "", true, true},
		{"typed yes", false, true, "y\n", true, true},
		{"typed no", false, true, "n\n", true, false},
		{"empty line", false, true, "\n", true, false},
		{"no terminal", false, false, "", true, false},
		// A plan that changes nothing skips the question entirely: no stdin is
		// read (an empty reader would otherwise make ReadString fail), and the
		// prompt text never appears.
		{"no changes, terminal", false, true, "", false, true},
		// The no-terminal refusal is NOT relaxed by changes==false: without a
		// terminal den still has nobody to tell it `--yes` was intended, and
		// collectInitialAnswers' own reasoning about this branch depends on
		// that staying true unconditionally.
		{"no changes, no terminal", false, false, "", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd, out := answersCmd(c.stdin)
			d := Deps{}
			if c.tty {
				d.IsTTY = func() bool { return true }
			}
			got, err := confirm(cmd, d, c.yes, c.changes)
			if err != nil {
				t.Fatalf("confirm: %v", err)
			}
			if got != c.want {
				t.Errorf("confirm = %v, want %v", got, c.want)
			}
			if !got && !strings.Contains(out.String(), "nothing was applied") {
				t.Errorf("a refused plan must say nothing happened:\n%s", out.String())
			}
			if !c.changes && c.tty && strings.Contains(out.String(), "apply this plan?") {
				t.Errorf("a plan with nothing to change must not ask:\n%s", out.String())
			}
		})
	}
}

// converge.Redacted is the single spelling of a hidden value. Pinned from the
// CLI side too: this is the package that prints.
func TestRedactedSpelling(t *testing.T) {
	if converge.Redacted != "<redacted>" {
		t.Errorf("Redacted = %q", converge.Redacted)
	}
}

// The choices a human makes about ambiguous repositories land in the SAME
// Answers an answer file would have carried: the second planning pass then
// cannot tell the two apart, which is what keeps one planner for both flows.
func TestResolveRepoChoicesWritesConfirmedChoicesIntoTheAnswers(t *testing.T) {
	matches := []converge.RepoMatch{
		{ // confirmed already: never asked about
			Requirement: converge.RepoRequirement{Key: "api"},
			Kind:        converge.MatchRemote, Path: "/dev/api", Confirmed: true,
		},
		{ // two candidates: the user picks the second
			Requirement: converge.RepoRequirement{Key: "crm", URL: "https://example.test/team/crm.git"},
			Kind:        converge.MatchAmbiguous, Candidates: []string{"/dev/one/crm", "/dev/two/crm"},
		},
		{ // a name-only guess the user declines
			Requirement: converge.RepoRequirement{Key: "docs"},
			Kind:        converge.MatchName, Path: "/dev/docs",
		},
		{ // absent: there is nothing to choose
			Requirement: converge.RepoRequirement{Key: "ops"},
			Kind:        converge.MatchAbsent,
		},
	}
	cmd, out := answersCmd("2\n\n")
	a := converge.Answers{}
	if err := resolveRepoChoices(cmd, Deps{IsTTY: func() bool { return true }}, matches, &a); err != nil {
		t.Fatalf("resolveRepoChoices: %v", err)
	}
	if a.Repos["crm"] != "/dev/two/crm" {
		t.Errorf("repos = %#v, want the chosen candidate", a.Repos)
	}
	if _, mapped := a.Repos["docs"]; mapped {
		t.Error("a declined guess must stay unmapped: den mounts no directory it could not attribute")
	}
	if strings.Contains(out.String(), "ops") {
		t.Errorf("an absent repository has nothing to choose:\n%s", out.String())
	}
	if strings.Contains(out.String(), "api") {
		t.Errorf("a confirmed match must not be re-asked:\n%s", out.String())
	}
}

// Without a terminal the unconfirmed matches are REPORTED, not refused: a
// scripted run installs what it can and names what it could not attribute,
// with the answer-file key that settles it.
func TestResolveRepoChoicesReportsWithoutATerminal(t *testing.T) {
	matches := []converge.RepoMatch{{
		Requirement: converge.RepoRequirement{Key: "crm"},
		Kind:        converge.MatchAmbiguous, Candidates: []string{"/dev/one/crm", "/dev/two/crm"},
	}}
	cmd, out := answersCmd("")
	a := converge.Answers{}
	if err := resolveRepoChoices(cmd, Deps{}, matches, &a); err != nil {
		t.Fatalf("resolveRepoChoices: %v", err)
	}
	if len(a.Repos) != 0 {
		t.Errorf("repos = %#v, expected nothing chosen without a terminal", a.Repos)
	}
	for _, want := range []string{"crm", "repos:"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output must name %q:\n%s", want, out.String())
		}
	}
}
