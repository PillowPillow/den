package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTree materializes a checkout: keys are relative paths, values file
// contents. Directories are implied.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const validStack = "image: devx:v1\nbase: claude\n"

func TestRunValidCheckout(t *testing.T) {
	root := writeTree(t, map[string]string{
		"stacks/devx/stack.yaml": validStack,
		"nests/api.yaml":         "stack: devx\nrepos:\n  - { key: api }\n",
	})
	if errs := Run(root); len(errs) != 0 {
		t.Fatalf("expected no errors, got: %v", errs)
	}
}

func TestRunMissingRoot(t *testing.T) {
	errs := Run(filepath.Join(t.TempDir(), "absent"))
	if len(errs) == 0 {
		t.Fatal("expected an error for a missing root")
	}
}

func TestRunBrokenStackYAML(t *testing.T) {
	root := writeTree(t, map[string]string{
		"stacks/devx/stack.yaml": "image: devx:v1\nbase: claude\negres: []\n", // typo → strict decode error
	})
	errs := Run(root)
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "egres") || !strings.Contains(errs[0].Error(), "stack.yaml") {
		t.Fatalf("expected the strict-YAML error naming stack.yaml, got: %v", errs)
	}
}

func TestRunParentCycle(t *testing.T) {
	root := writeTree(t, map[string]string{
		"stacks/a/stack.yaml":     "image: a:v1\nparent: b\nprovision:\n  steps: [./provision/x.sh]\n",
		"stacks/a/provision/x.sh": "true\n",
		"stacks/b/stack.yaml":     "image: b:v1\nparent: a\nprovision:\n  steps: [./provision/x.sh]\n",
		"stacks/b/provision/x.sh": "true\n",
	})
	errs := Run(root)
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "cycle") {
		t.Fatalf("expected a cycle error, got: %v", errs)
	}
}

func TestRunPathEscapesRoot(t *testing.T) {
	root := writeTree(t, map[string]string{
		"stacks/devx/stack.yaml":     "image: devx:v1\nbase: claude\nprovision:\n  includes: [../../../outside.sh]\n  steps: [./provision/x.sh]\n",
		"stacks/devx/provision/x.sh": "true\n",
	})
	// The escaping file EXISTS, to prove the refusal is about confinement,
	// not existence.
	if err := os.WriteFile(filepath.Join(root, "..", "outside.sh"), []byte("true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	errs := Run(root)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "escapes") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a confinement error, got: %v", errs)
	}
}

func TestRunMissingProvisionFile(t *testing.T) {
	root := writeTree(t, map[string]string{
		"stacks/devx/stack.yaml": "image: devx:v1\nbase: claude\nprovision:\n  steps: [./provision/absent.sh]\n",
	})
	errs := Run(root)
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "absent.sh") {
		t.Fatalf("expected a missing-file error, got: %v", errs)
	}
}

func TestRunPrefixedRefInsideSource(t *testing.T) {
	root := writeTree(t, map[string]string{
		"stacks/devx/stack.yaml": validStack,
		"nests/api.yaml":         "stack: corp:devx\nrepos:\n  - { key: api }\n",
	})
	errs := Run(root)
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "bare") {
		t.Fatalf("expected a bare-reference error, got: %v", errs)
	}
}

func TestRunUnknownStackRef(t *testing.T) {
	root := writeTree(t, map[string]string{
		"stacks/devx/stack.yaml": validStack,
		"nests/api.yaml":         "stack: nope\nrepos:\n  - { key: api }\n",
	})
	errs := Run(root)
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "nope") || !strings.Contains(errs[0].Error(), "not found") {
		t.Fatalf("expected a not-found error naming the stack, got: %v", errs)
	}
}

func TestRunNestWithoutStack(t *testing.T) {
	root := writeTree(t, map[string]string{
		"stacks/devx/stack.yaml": validStack,
		"nests/api.yaml":         "repos:\n  - { key: api }\n",
	})
	errs := Run(root)
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "stack:") {
		t.Fatalf("expected a missing-`stack:` error, got: %v", errs)
	}
}

// TestRunAccumulatesAllFindings pins the core contract stated in the task
// brief: Run does not stop at the first finding, it reports every one — the
// same doctrine as config.Validate. A single independent stack finding and a
// single independent nest finding must BOTH surface from one call.
func TestRunAccumulatesAllFindings(t *testing.T) {
	root := writeTree(t, map[string]string{
		"stacks/devx/stack.yaml": "image: devx:v1\nbase: claude\nprovision:\n  steps: [./provision/absent.sh]\n",
		"nests/api.yaml":         "stack: nope\nrepos:\n  - { key: api }\n",
	})
	errs := Run(root)
	if len(errs) < 2 {
		t.Fatalf("expected both the stack finding and the nest finding, got: %v", errs)
	}
	joined := errsString(errs)
	if !strings.Contains(joined, "absent.sh") {
		t.Errorf("missing the provision-file finding, got: %v", errs)
	}
	if !strings.Contains(joined, "nope") {
		t.Errorf("missing the unknown-stack finding, got: %v", errs)
	}
}

func errsString(errs []error) string {
	var b strings.Builder
	for _, e := range errs {
		b.WriteString(e.Error())
		b.WriteString("\n")
	}
	return b.String()
}

func TestRunSelfParentCycle(t *testing.T) {
	root := writeTree(t, map[string]string{
		"stacks/a/stack.yaml":     "image: a:v1\nparent: a\nprovision:\n  steps: [./provision/x.sh]\n",
		"stacks/a/provision/x.sh": "true\n",
	})
	errs := Run(root)
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "cycle") || !strings.Contains(errs[0].Error(), "a -> a") {
		t.Fatalf("expected a self-parent cycle error naming `a -> a`, got: %v", errs)
	}
}
