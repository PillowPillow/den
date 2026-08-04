package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mustWriteFile writes content to a file, creating parent directories as needed.
func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLintValidCheckout(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "stacks", "devx", "stack.yaml"),
		"image: devx:v1\nbase: claude\n")
	cmd := NewRootCmdWith(Deps{})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"lint", root})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("lint: %v", err)
	}
	if !strings.Contains(out.String(), "ok") {
		t.Errorf("output = %q", out.String())
	}
}

func TestLintInvalidCheckoutFails(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "stacks", "devx", "stack.yaml"),
		"image: devx:v1\nbase: claude\negres: []\n")
	cmd := NewRootCmdWith(Deps{})
	cmd.SetArgs([]string{"lint", root})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "egres") {
		t.Fatalf("expected the lint failure, got: %v", err)
	}
}
