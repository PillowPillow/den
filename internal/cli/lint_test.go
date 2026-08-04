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

// lint.Run's accumulation is pinned in internal/lint. What was not covered is
// the FRAME: `den lint` joins the findings into one error, and that joined
// error is what a team repo's CI actually consumes. A frame that stopped at
// the first finding would still satisfy every lint.Run test and would send a
// team back for one push per problem.
func TestLintReportsEveryFindingThroughTheCommand(t *testing.T) {
	root := t.TempDir()
	// Three findings lint.Run ACCUMULATES — not strict-decode failures, which
	// surface as a load error on the offending file and can end the report
	// early on that file rather than join the list.
	mustWriteFile(t, filepath.Join(root, "stacks", "alpha", "stack.yaml"),
		"image: alpha:v1\nparent: ghost-one\n")
	mustWriteFile(t, filepath.Join(root, "stacks", "beta", "stack.yaml"),
		"image: beta:v1\nparent: ghost-two\n")
	mustWriteFile(t, filepath.Join(root, "nests", "api.yaml"),
		"repos:\n  - { key: api }\n")

	cmd := NewRootCmdWith(Deps{})
	cmd.SetArgs([]string{"lint", root})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected the lint failure")
	}
	msg := err.Error()
	for _, want := range []string{"alpha", "ghost-one", "beta", "ghost-two", "api"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the joined report lacks %q; got:\n%s", want, msg)
		}
	}
	// The frame is a LIST, not a sentence: one line per finding.
	if got := strings.Count(msg, "\n  - "); got < 3 {
		t.Errorf("expected at least 3 itemized findings, counted %d; got:\n%s", got, msg)
	}
}
