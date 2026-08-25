package sbx

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckEnvFileAcceptsWhatEnvFileWrote(t *testing.T) {
	e := completeEnv()
	out, err := EnvFile(e)
	if err != nil {
		t.Fatalf("EnvFile: %v", err)
	}
	path := filepath.Join(t.TempDir(), ".sbxenv.yaml")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := CheckEnvFile(path, e.Name); err != nil {
		t.Errorf("den refuses a file it wrote itself: %v", err)
	}
}

func TestCheckEnvFileRefusesWhatDenCannotVouchFor(t *testing.T) {
	for name, content := range map[string]string{
		"truncated":    "schemaVersion: \"1\"\nage",
		"newer schema": "schemaVersion: \"2\"\nagent: shell\nname: api\n",
		"unknown key":  "schemaVersion: \"1\"\nagent: shell\nname: api\nfutureKey: 1\n",
		"empty":        "",
		"no name":      "schemaVersion: \"1\"\nagent: shell\n",
	} {
		path := filepath.Join(t.TempDir(), ".sbxenv.yaml")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := CheckEnvFile(path, "api"); err == nil {
			t.Errorf("%s: must be refused", name)
		}
	}
}

func TestCheckEnvFileRefusesAMissingFile(t *testing.T) {
	err := CheckEnvFile(filepath.Join(t.TempDir(), "nope.yaml"), "api")
	if err == nil {
		t.Fatal("an absent record must be refused, not treated as empty")
	}
	// Pinned, not just "err != nil": `den rm` (fix round 1, finding 1) relies
	// on errors.Is(err, fs.ErrNotExist) to tell an ABSENT record from an
	// UNREADABLE one, and give each its own message. A mutation that swallows
	// the read error (`content, _ := os.ReadFile(path)`) would still leave
	// this test green on `err == nil` alone — Decode would fail on the empty
	// content instead — but it would fail HERE, because the returned error
	// would no longer wrap the real os.ReadFile failure.
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the error must wrap fs.ErrNotExist so den rm can tell an absent record from an "+
			"unreadable one: %v", err)
	}
}

// The hazard this argument exists to close (fix round 1, finding 2): a file
// den can read and understand, sitting under sandbox A's directory, but
// describing sandbox B — `sbx env rm` resolves the sandbox it destroys FROM
// the file's content, so handing it this file would destroy B while the user
// asked to remove A.
func TestCheckEnvFileRefusesAFileDescribingAnotherSandbox(t *testing.T) {
	out, err := EnvFile(Env{
		Name:       "web",
		Image:      "devx:v1",
		MixinKit:   "/den/cache/mixins/web",
		Workspaces: []string{"/dev/web"},
	})
	if err != nil {
		t.Fatalf("EnvFile: %v", err)
	}
	path := filepath.Join(t.TempDir(), ".sbxenv.yaml")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	err = CheckEnvFile(path, "api")
	if err == nil {
		t.Fatal("a file naming a different sandbox must be refused")
	}
	if !strings.Contains(err.Error(), "web") || !strings.Contains(err.Error(), "api") {
		t.Errorf("the message must name both the file's sandbox and the one den looked for: %v", err)
	}
}
