package source

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func finalReceipt() Receipt {
	return Receipt{
		SchemaVersion:  ReceiptSchema,
		Status:         StatusPartiallyReady,
		Version:        "1.0.0",
		Commit:         "0123456789abcdef",
		ManifestDigest: "sha256:0123456789abcdef",
		AppliedAt:      time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC),
		Resources: ReceiptResources{
			Credentials:  ResourceReady,
			BuildNetwork: ResourceReady,
			Stacks:       map[string]ResourceStatus{"base": ResourceReady},
		},
		Nests: map[string]ReceiptNest{
			"go-dgdev": {Status: NestReady},
			"leo":      {Status: NestNotReady, MissingRepos: []string{"js.agentic-bank"}},
		},
	}
}

func TestWriteReceiptRoundTripsPrivately(t *testing.T) {
	home := t.TempDir()
	want := finalReceipt()
	if err := WriteReceipt(home, "dg", want); err != nil {
		t.Fatalf("WriteReceipt: %v", err)
	}
	got, err := LoadReceipt(home, "dg")
	if err != nil {
		t.Fatalf("LoadReceipt: %v", err)
	}
	if got.Status != StatusPartiallyReady || got.Version != "1.0.0" || got.Commit != want.Commit {
		t.Errorf("receipt = %+v", got)
	}
	if got.Resources.Stacks["base"] != ResourceReady {
		t.Errorf("resources.stacks = %+v", got.Resources.Stacks)
	}
	leo := got.Nests["leo"]
	if leo.Status != NestNotReady || len(leo.MissingRepos) != 1 || leo.MissingRepos[0] != "js.agentic-bank" {
		t.Errorf("nests[leo] = %+v", leo)
	}
	if !got.AppliedAt.Equal(want.AppliedAt) {
		t.Errorf("applied_at = %v, want %v", got.AppliedAt, want.AppliedAt)
	}
	info, err := os.Stat(ReceiptPath(home, "dg"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", info.Mode().Perm())
	}
	// state/ is not cache/: den never purges it, and the receipt is the final
	// commit marker of a convergence. It must live under state/sources/.
	if want, got := filepath.Join(home, "state", "sources", "dg.yaml"), ReceiptPath(home, "dg"); got != want {
		t.Errorf("ReceiptPath = %q, want %q", got, want)
	}
}

// The receipt is written where anyone with the machine can read it, and it is
// kept forever. Nothing transient and nothing secret may reach it: not a
// credential value, not the name of the environment variable it came from, not
// the repository roots the wizard scanned this run (spec §10.2).
func TestReceiptCarriesNoSecretAndNoTransientAnswer(t *testing.T) {
	r := finalReceipt()
	home := t.TempDir()
	if err := WriteReceipt(home, "dg", r); err != nil {
		t.Fatalf("WriteReceipt: %v", err)
	}
	raw, err := os.ReadFile(ReceiptPath(home, "dg"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"sentinel-secret", "GLPAT", "gitlab_token", "repository_roots", "/Users/"} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("the receipt contains %q:\n%s", forbidden, raw)
		}
	}
}

// An `applying` receipt is what makes a partial application resumable: it
// names the version being converged and the one still active, and every
// consumer refuses while it is there (spec §11.3).
func TestApplyingReceiptRoundTripsItsResumeState(t *testing.T) {
	home := t.TempDir()
	applying := Receipt{
		SchemaVersion:   ReceiptSchema,
		Status:          StatusApplying,
		PreviousVersion: "1.0.0",
		TargetVersion:   "1.1.0",
		TargetCommit:    "fedcba9876543210",
		ManifestDigest:  "sha256:fedcba",
		Resources:       ReceiptResources{Credentials: ResourceReady},
	}
	if err := WriteReceipt(home, "dg", applying); err != nil {
		t.Fatalf("WriteReceipt: %v", err)
	}
	got, err := LoadReceipt(home, "dg")
	if err != nil {
		t.Fatalf("LoadReceipt: %v", err)
	}
	if got.Status != StatusApplying || got.PreviousVersion != "1.0.0" || got.TargetVersion != "1.1.0" {
		t.Errorf("receipt = %+v", got)
	}
	// A final receipt has no target: an omitted field must stay omitted rather
	// than be written as an empty string a later reader would compare against.
	raw, err := os.ReadFile(ReceiptPath(home, "dg"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "applied_at") {
		t.Errorf("an applying receipt has no applied_at:\n%s", raw)
	}
}

func TestLoadReceiptRefusesUnknownKeysSchemasAndStatuses(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"camelCase key", "schemaVersion: 1\nstatus: ready\n", "schemaVersion"},
		{"unsupported schema", "schema_version: 2\nstatus: ready\n", "schema_version"},
		{"unknown status", "schema_version: 1\nstatus: mostly\n", "status"},
		{"unknown nest status", "schema_version: 1\nstatus: ready\nnests:\n  leo: { status: maybe }\n", "maybe"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			mkdirAll(t, filepath.Dir(ReceiptPath(home, "dg")))
			writeFile(t, ReceiptPath(home, "dg"), c.content)
			_, err := LoadReceipt(home, "dg")
			if err == nil {
				t.Fatalf("expected a refusal mentioning %q", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, expected a mention of %q", err.Error(), c.want)
			}
		})
	}
}

func TestLoadReceiptReportsAbsenceAsSuch(t *testing.T) {
	_, err := LoadReceipt(t.TempDir(), "dg")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("error = %v, expected it to wrap os.ErrNotExist", err)
	}
}

func TestWriteReceiptKeepsThePreviousFileWhenReplacementFails(t *testing.T) {
	home := t.TempDir()
	if err := WriteReceipt(home, "dg", finalReceipt()); err != nil {
		t.Fatalf("WriteReceipt: %v", err)
	}
	dir := filepath.Dir(ReceiptPath(home, "dg"))
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	if err := WriteReceipt(home, "dg", Receipt{SchemaVersion: ReceiptSchema, Status: StatusBlocked}); err == nil {
		t.Fatal("expected the write to fail on a read-only directory")
	}
	got, err := LoadReceipt(home, "dg")
	if err != nil {
		t.Fatalf("the previous receipt must stay readable: %v", err)
	}
	if got.Status != StatusPartiallyReady {
		t.Errorf("status = %q, expected the previous receipt intact", got.Status)
	}
}

func TestReceiptRefusesAnIllegalSourceName(t *testing.T) {
	home := t.TempDir()
	if err := WriteReceipt(home, "../escape", finalReceipt()); err == nil {
		t.Error("WriteReceipt accepted a traversing source name")
	}
	if _, err := LoadReceipt(home, "../escape"); err == nil {
		t.Error("LoadReceipt accepted a traversing source name")
	}
}
