package converge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/source"
)

func writeAnswers(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "answers.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func noEnv(string) string { return "" }

func TestLoadAnswersResolvesCredentialEnvironmentWithoutPersistingIt(t *testing.T) {
	path := writeAnswers(t, `repository_roots: [~/Development]
credentials:
  gitlab_token:
    from_env: GLPAT
repos:
  api: ~/Development/api
`)
	got, err := LoadAnswers(path, func(k string) string {
		if k == "GLPAT" {
			return "sentinel-secret"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("LoadAnswers: %v", err)
	}
	if got.Credentials["gitlab_token"].Value != "sentinel-secret" {
		t.Fatalf("credential not resolved: %+v", got.Credentials["gitlab_token"])
	}
	if got.Credentials["gitlab_token"].FromEnv != "GLPAT" {
		t.Errorf("from_env = %q", got.Credentials["gitlab_token"].FromEnv)
	}
	// The roots and the repo overrides are expanded: everything downstream
	// compares them with real directories.
	if strings.HasPrefix(got.RepositoryRoots[0], "~") {
		t.Errorf("repository_roots[0] = %q, expected the tilde resolved", got.RepositoryRoots[0])
	}
	if strings.HasPrefix(got.Repos["api"], "~") {
		t.Errorf("repos[api] = %q, expected the tilde resolved", got.Repos["api"])
	}
}

func TestLoadAnswersRefusesFaults(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{
			"camelCase key",
			"repositoryRoots: [/dev]\n",
			"repositoryRoots",
		},
		{
			// A literal secret in a file people commit by accident is the one
			// mistake den can prevent by construction (spec §7.1).
			"literal credential value",
			"credentials:\n  gitlab_token:\n    value: sentinel-secret\n",
			"value",
		},
		{
			"missing environment variable",
			"credentials:\n  gitlab_token:\n    from_env: ABSENT\n",
			"ABSENT",
		},
		{
			"credential without a source",
			"credentials:\n  gitlab_token: {}\n",
			"from_env",
		},
		{
			"relative repository root",
			"repository_roots: [./dev]\n",
			"./dev",
		},
		{
			"relative repo override",
			"repos:\n  api: dev/api\n",
			"dev/api",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := LoadAnswers(writeAnswers(t, c.content), noEnv)
			if err == nil {
				t.Fatalf("expected a refusal mentioning %q", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, expected a mention of %q", err.Error(), c.want)
			}
			// Whatever the fault, the message may never carry a value.
			if strings.Contains(err.Error(), "sentinel-secret") {
				t.Errorf("the error leaks the credential value: %q", err.Error())
			}
		})
	}
}

// An answer file naming a credential the source does not declare is a typo the
// user must hear about: den would otherwise prompt for the real input while
// silently ignoring the one they filled in.
func TestValidateAnswersRefusesUndeclaredCredentials(t *testing.T) {
	m := &source.Manifest{Inputs: source.Inputs{
		Credentials: map[string]source.CredentialInput{"gitlab_token": {Prompt: "GitLab token"}},
	}}
	err := ValidateAnswers(m, Answers{Credentials: map[string]CredentialAnswer{
		"gitlab_token": {FromEnv: "GLPAT", Value: "sentinel-secret"},
		"github_token": {FromEnv: "GH", Value: "sentinel-secret"},
	}})
	if err == nil || !strings.Contains(err.Error(), "github_token") {
		t.Fatalf("ValidateAnswers = %v, expected the undeclared name to be refused", err)
	}
	if strings.Contains(err.Error(), "sentinel-secret") {
		t.Errorf("the error leaks the credential value: %q", err.Error())
	}
	if err := ValidateAnswers(m, Answers{Credentials: map[string]CredentialAnswer{
		"gitlab_token": {FromEnv: "GLPAT", Value: "sentinel-secret"},
	}}); err != nil {
		t.Errorf("ValidateAnswers refused a declared credential: %v", err)
	}
}

// Answers travel through plans, errors and logs. Their String/format must never
// render a value — the redaction is on the TYPE, so no call site can forget it.
func TestCredentialAnswerNeverRendersItsValue(t *testing.T) {
	a := CredentialAnswer{FromEnv: "GLPAT", Value: "sentinel-secret"}
	for _, rendered := range []string{
		a.String(),
		strings.Join([]string{a.String()}, ""),
	} {
		if strings.Contains(rendered, "sentinel-secret") {
			t.Errorf("rendered = %q, expected the value redacted", rendered)
		}
	}
	if !strings.Contains(a.String(), Redacted) {
		t.Errorf("rendered = %q, expected %q", a.String(), Redacted)
	}
}

// An answer file is optional: `den init --source` with no `--answers` collects
// the same typed answers interactively, and an empty path must not be read as
// a missing file.
func TestLoadAnswersOnAnAbsentPathIsAnError(t *testing.T) {
	if _, err := LoadAnswers(filepath.Join(t.TempDir(), "absent.yaml"), noEnv); err == nil {
		t.Fatal("expected an error naming the answer file the user passed")
	}
}
