package sbx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckEnvFileAcceptsWhatEnvFileWrote(t *testing.T) {
	out, err := EnvFile(completeEnv())
	if err != nil {
		t.Fatalf("EnvFile: %v", err)
	}
	path := filepath.Join(t.TempDir(), ".sbxenv.yaml")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := CheckEnvFile(path); err != nil {
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
		if err := CheckEnvFile(path); err == nil {
			t.Errorf("%s: must be refused", name)
		}
	}
}

func TestCheckEnvFileRefusesAMissingFile(t *testing.T) {
	if err := CheckEnvFile(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Error("an absent record must be refused, not treated as empty")
	}
}
