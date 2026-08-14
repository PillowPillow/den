package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/PillowPillow/den/internal/converge"
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

// answersCmd is a bare command carrying the streams collectInitialAnswers
// reads and writes: the function under test takes its input from the cobra
// command, never from the process.
func answersCmd(stdin string) (*cobra.Command, *bytes.Buffer) {
	cmd := &cobra.Command{}
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
	}, answersManifest(), path, true)
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
	}, answersManifest(), path, true)
	if err != nil {
		t.Fatalf("collectInitialAnswers: %v", err)
	}
	if a.Credentials["gitlab_token"].Value != "sentinel-secret" {
		t.Errorf("credentials = %v", a.Credentials)
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
	_, err := collectInitialAnswers(cmd, Deps{}, answersManifest(), path, true)
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
	}, answersManifest(), path, true)
	if err == nil || !strings.Contains(err.Error(), "ghost_token") {
		t.Fatalf("error = %v, expected the undeclared credential to be named", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error = %q, expected the answer file to be named", err.Error())
	}
}

// Confirmation: `--yes` applies, a typed "n" does not, and no terminal means
// the plan is printed and nothing happens — never a default yes.
func TestConfirm(t *testing.T) {
	cases := []struct {
		name  string
		yes   bool
		tty   bool
		stdin string
		want  bool
	}{
		{"--yes", true, false, "", true},
		{"typed yes", false, true, "y\n", true},
		{"typed no", false, true, "n\n", false},
		{"empty line", false, true, "\n", false},
		{"no terminal", false, false, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd, out := answersCmd(c.stdin)
			d := Deps{}
			if c.tty {
				d.IsTTY = func() bool { return true }
			}
			got, err := confirm(cmd, d, c.yes)
			if err != nil {
				t.Fatalf("confirm: %v", err)
			}
			if got != c.want {
				t.Errorf("confirm = %v, want %v", got, c.want)
			}
			if !got && !strings.Contains(out.String(), "nothing was applied") {
				t.Errorf("a refused plan must say nothing happened:\n%s", out.String())
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
