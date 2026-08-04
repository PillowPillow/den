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

// TestRunPrefixedParentDirect exercises checkStack's OWN prefixed-`parent:`
// branch directly (lint.go's checkStack, not the nest-level `stack:` case
// TestRunPrefixedRefInsideSource already covers). Both call the same
// config.SplitSourceRef helper but on independent code paths.
func TestRunPrefixedParentDirect(t *testing.T) {
	root := writeTree(t, map[string]string{
		"stacks/base/stack.yaml":      validStack,
		"stacks/child/stack.yaml":     "image: child:v1\nparent: corp:base\nprovision:\n  steps: [./provision/x.sh]\n",
		"stacks/child/provision/x.sh": "true\n",
	})
	errs := Run(root)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "bare") && strings.Contains(e.Error(), "child") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a bare-reference error for stack %q's `parent:`, got: %v", "child", errs)
	}
}

// TestRunMissingKitAndKits covers existence checking for BOTH the `kit:`
// and `kits:` YAML keys, which TestRunMissingProvisionFile never exercised
// (Task 4 review finding #3): only `provision.steps` had a missing-file
// test before this.
func TestRunMissingKitAndKits(t *testing.T) {
	root := writeTree(t, map[string]string{
		"stacks/devx/stack.yaml": "image: devx:v1\nbase: claude\nkit: ./missing-kit\nkits: [./missing-kits-dir]\n",
	})
	errs := Run(root)
	joined := errsString(errs)
	if !strings.Contains(joined, "kit:") || !strings.Contains(joined, "missing-kit") {
		t.Errorf("expected a missing-`kit:` finding, got: %v", errs)
	}
	if !strings.Contains(joined, "kits:") || !strings.Contains(joined, "missing-kits-dir") {
		t.Errorf("expected a missing-`kits:` finding, got: %v", errs)
	}
}

// TestRunKitAndKitsEscapeRoot covers confinement checking for BOTH `kit:`
// and `kits:` (Task 4 review finding #3): the original suite only proved
// confinement for `provision.includes` (TestRunPathEscapesRoot).
func TestRunKitAndKitsEscapeRoot(t *testing.T) {
	root := writeTree(t, map[string]string{
		"stacks/devx/stack.yaml": "image: devx:v1\nbase: claude\nkit: ../../../outside-kit\nkits: [../../../outside-kits]\n",
	})
	if err := os.MkdirAll(filepath.Join(root, "..", "outside-kit"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "..", "outside-kits"), 0o755); err != nil {
		t.Fatal(err)
	}
	errs := Run(root)
	joined := errsString(errs)
	if !strings.Contains(joined, "kit:") || !strings.Contains(joined, "escapes") {
		t.Errorf("expected a `kit:` confinement finding, got: %v", errs)
	}
	if !strings.Contains(joined, "kits:") {
		t.Errorf("expected a `kits:` confinement finding, got: %v", errs)
	}
}

// TestRunAbsoluteDeclaredKit is the scenario Task 4 review finding #1 names
// exactly: an absolute `kit:` path that happens to point INSIDE root, on
// THIS machine, so a naive existence-and-confinement check would pass it.
// The refusal must fire anyway, because it is a judgment about how the path
// was WRITTEN, not about where it happens to resolve.
func TestRunAbsoluteDeclaredKit(t *testing.T) {
	root := t.TempDir()
	kitDir := filepath.Join(root, "stacks", "devx", "kit")
	if err := os.MkdirAll(kitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stackYAML := "image: devx:v1\nbase: claude\nkit: " + kitDir + "\n"
	if err := os.WriteFile(filepath.Join(root, "stacks", "devx", "stack.yaml"), []byte(stackYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	errs := Run(root)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "absolute") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an absolute-path refusal even though the path exists and resolves inside root, got: %v", errs)
	}
}

// TestRunAbsoluteDeclaredKitsAndSteps proves the absolute-path refusal (Task
// 4 review finding #1) isn't limited to `kit:` — TestRunAbsoluteDeclaredKit
// only covered that one key, and `kits:`/`provision.steps:` are the ones a
// real stack is more likely to use.
func TestRunAbsoluteDeclaredKitsAndSteps(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "stacks", "devx")
	if err := os.MkdirAll(filepath.Join(dir, "provision"), 0o755); err != nil {
		t.Fatal(err)
	}
	absKit := filepath.Join(dir, "abs-kit")
	if err := os.MkdirAll(absKit, 0o755); err != nil {
		t.Fatal(err)
	}
	absStep := filepath.Join(dir, "provision", "abs.sh")
	if err := os.WriteFile(absStep, []byte("true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	y := "image: devx:v1\nbase: claude\nkits: [" + absKit + "]\nprovision:\n  steps: [" + absStep + "]\n"
	if err := os.WriteFile(filepath.Join(dir, "stack.yaml"), []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	joined := errsString(Run(root))
	if !strings.Contains(joined, "kits:") || !strings.Contains(joined, "absolute") {
		t.Errorf("absolute kits: entry not refused: %v", joined)
	}
	if !strings.Contains(joined, "provision.steps:") || !strings.Contains(joined, "absolute") {
		t.Errorf("absolute provision.steps: entry not refused: %v", joined)
	}
}

// TestRunAbsoluteAndRelativeSiblingsAreNotConflated pins the reason
// AbsoluteDeclaredPaths is tracked BY INDEX rather than by value
// (config.Stack, internal/config/stack.go): two `kits:` entries that
// resolve to the IDENTICAL absolute string — one written relative to the
// stack directory, one written already-absolute — must not be conflated. A
// value-keyed lookup cannot tell them apart once both have resolved to the
// same string; exactly one of the two is actually an absolute declaration,
// and only that one should be refused.
func TestRunAbsoluteAndRelativeSiblingsAreNotConflated(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "stacks", "devx")
	kitDir := filepath.Join(dir, "kit")
	if err := os.MkdirAll(kitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	y := "image: devx:v1\nbase: claude\nkits: [kit, " + kitDir + "]\n"
	if err := os.WriteFile(filepath.Join(dir, "stack.yaml"), []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	errs := Run(root)
	absoluteCount := 0
	for _, e := range errs {
		if strings.Contains(e.Error(), "absolute") {
			absoluteCount++
		}
	}
	if absoluteCount != 1 {
		t.Fatalf("expected exactly ONE absolute-path refusal — only the entry actually "+
			"written absolute, not its relative sibling that resolves to the same string — got %d: %v",
			absoluteCount, errs)
	}
}

// TestRunSymlinkEscapesRoot is Task 4 review finding #2: a symlink COMMITTED
// inside the checkout, pointing at a target outside it. Lexically the link
// itself is confined and os.Stat follows it successfully — only resolving
// through EvalSymlinks before comparing catches it.
func TestRunSymlinkEscapesRoot(t *testing.T) {
	root := writeTree(t, map[string]string{
		"stacks/devx/stack.yaml": "image: devx:v1\nbase: claude\nprovision:\n  steps: [./provision/x.sh]\n",
	})
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "outside.sh")
	if err := os.WriteFile(outsideFile, []byte("true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(root, "stacks", "devx", "provision", "x.sh")
	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideFile, linkPath); err != nil {
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
		t.Fatalf("expected a confinement error for a symlink escaping the checkout, got: %v", errs)
	}
}

// A stack's declared paths are refused when absolute (checkDeclaredPath), but
// a NEST's `repos:` were not checked at all — so a source nest shipping
// `path: /Users/alice/dev/x` linted clean and failed on every colleague's
// machine. That is precisely what lint exists to catch: an object is not
// distributable if it depends on the layout of the machine that authored it.
func TestRunRefusesAnAbsoluteRepoPathInANest(t *testing.T) {
	root := writeTree(t, map[string]string{
		"stacks/devx/stack.yaml": "image: devx:v1\nbase: claude\n",
		"nests/api.yaml":         "stack: devx\nrepos:\n  - { path: /Users/alice/dev/x }\n",
	})
	errs := Run(root)
	if len(errs) == 0 {
		t.Fatal("an absolute repo path in a source nest must be refused")
	}
	joined := errsString(errs)
	for _, want := range []string{"/Users/alice/dev/x", "key:", "repos:"} {
		if !strings.Contains(joined, want) {
			t.Errorf("findings %v lack %q", errs, want)
		}
	}
}

// "~/dev/x" is the same fault wearing a different hat: LoadNest expands it to
// an absolute path on the authoring machine, and it names a directory only
// that machine has.
func TestRunRefusesATildeRepoPathInANest(t *testing.T) {
	root := writeTree(t, map[string]string{
		"stacks/devx/stack.yaml": "image: devx:v1\nbase: claude\n",
		"nests/api.yaml":         "stack: devx\nrepos:\n  - { path: ~/dev/x }\n",
	})
	if len(Run(root)) == 0 {
		t.Fatal("a ~-rooted repo path expands to a machine path and must be refused")
	}
}

// A `key:` entry is the shareable form and must stay clean — the check must
// not fire on the very construct it recommends.
func TestRunAcceptsKeyReposInANest(t *testing.T) {
	root := writeTree(t, map[string]string{
		"stacks/devx/stack.yaml": "image: devx:v1\nbase: claude\n",
		"nests/api.yaml":         "stack: devx\nrepos:\n  - { key: api }\n  - { key: front, optional: true }\n",
	})
	if errs := Run(root); len(errs) != 0 {
		t.Errorf("a key-only nest is the shareable form and must lint clean, got: %v", errs)
	}
}
