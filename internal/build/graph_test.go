package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/config"
)

// writeStack creates <denHome>/stacks/<name>/stack.yaml.
func writeStack(t *testing.T, denHome, name, content string) {
	t.Helper()
	dir := filepath.Join(denHome, "stacks", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stack.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// loadStacks writes a set of stacks and loads them, the way `den build` does.
// The map is name → stack.yaml content.
//
// Writes ONLY the stack.yaml. This package's graph tests exercise Chain —
// the walk and its ordering — never buildability: whether a stack is one
// Plan would actually build is config.Stack.Buildable's question (spec §6),
// decided from `provision.steps` in the YAML alone, not from any file on
// disk. A fixture here therefore needs no script, real or fake.
func loadStacks(t *testing.T, files map[string]string) config.Stacks {
	t.Helper()
	denHome := t.TempDir()
	for name, content := range files {
		writeStack(t, denHome, name, content)
	}
	stacks, err := config.LoadStacks(denHome)
	if err != nil {
		t.Fatalf("loading stacks: %v", err)
	}
	return stacks
}

func names(chain []*config.Stack) []string {
	out := make([]string, 0, len(chain))
	for _, s := range chain {
		out = append(out, s.Name)
	}
	return out
}

// The fixture graph, with a tie the name sort has to break: beta and gamma are
// both children of alpha, and gamma is the one with a child of its own.
//
//	alpha ← beta
//	      ← gamma ← delta
//	zeta
var fixture = map[string]string{
	"alpha": "image: alpha:v1\n",
	"beta":  "image: beta:v1\nparent: alpha\n",
	"gamma": "image: gamma:v1\nparent: alpha\n",
	"delta": "image: delta:v1\nparent: gamma\n",
	"zeta":  "image: zeta:v1\n",
}

// The order in a golden file, exactly as issue #8 asks. It is only possible
// because the order is deterministic — a Go map is not, and the roots are
// sorted before the walk for that reason alone.
func TestChainOrderMatchesTheGolden(t *testing.T) {
	chain, _, err := Chain(loadStacks(t, fixture), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	path := filepath.Join("testdata", "order-all.golden")
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden: %v", err)
	}
	got := strings.Join(names(chain), "\n") + "\n"
	if got != string(want) {
		t.Errorf("order =\n%s\nwant (%s) =\n%s\n(there is no -update flag: edit the golden by hand)",
			got, path, want)
	}
}

// Determinism is the property the golden rests on: the same configuration must
// produce the same order twice. Asserted separately, because a golden that
// happened to match once would hide a map-order dependency.
func TestChainOrderIsStableAcrossRuns(t *testing.T) {
	stacks := loadStacks(t, fixture)
	first, _, err := Chain(stacks, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, _, err := Chain(stacks, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.Join(names(again), ",") != strings.Join(names(first), ",") {
			t.Fatalf("run %d = %v, first run = %v", i, names(again), names(first))
		}
	}
}

// A named target walks its ancestry and NOTHING else: `den build delta` must
// not rebuild beta or zeta, which have nothing to do with it.
func TestChainOnATargetTakesItsAncestorsOnly(t *testing.T) {
	chain, _, err := Chain(loadStacks(t, fixture), "delta")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.Join(names(chain), ","); got != "alpha,gamma,delta" {
		t.Errorf("chain = %v, want alpha,gamma,delta (ancestors first, nothing else)", names(chain))
	}
}

// The parent's IMAGE travels with the child, not its NAME: `sbx create
// --template` takes a reference, and Execute (Task 6) reads this field
// straight off the stack rather than re-resolving the graph it was handed as
// a flat, already-ordered chain.
func TestChainCarriesTheParentImageOnTheChild(t *testing.T) {
	chain, _, err := Chain(loadStacks(t, fixture), "delta")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	alpha, delta := chain[0], chain[len(chain)-1]
	if alpha.ParentImage != "" {
		t.Errorf("alpha.ParentImage = %q, want empty: alpha is a root, it has no parent", alpha.ParentImage)
	}
	if delta.ParentImage != "gamma:v1" {
		t.Errorf("delta.ParentImage = %q, want gamma:v1 (delta's parent gamma's image, not its name)", delta.ParentImage)
	}
}

func TestChainOnARootTargetIsJustThatStack(t *testing.T) {
	chain, _, err := Chain(loadStacks(t, fixture), "zeta")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := names(chain); len(got) != 1 || got[0] != "zeta" {
		t.Errorf("chain = %v, want [zeta]", got)
	}
}

// The cycle must be READABLE, not deducible: issue #8 asks for `a → b → a`
// named in full.
func TestChainNamesTheWholeCycle(t *testing.T) {
	stacks := loadStacks(t, map[string]string{
		"a": "image: a:v1\nparent: b\n",
		"b": "image: b:v1\nparent: a\n",
	})
	_, _, err := Chain(stacks, "a")
	if err == nil {
		t.Fatal("expected a refusal on a parent cycle")
	}
	if !strings.Contains(err.Error(), "a → b → a") {
		t.Errorf("message = %q, want the whole cycle `a → b → a`", err)
	}
	// The file named is the one that CLOSES the cycle — b's parent points back.
	// Naming a's file would send the user to edit a stack that is not wrong on
	// its own.
	if !strings.Contains(err.Error(), filepath.Join("stacks", "b", "stack.yaml")) {
		t.Errorf("message = %q, want it to name stacks/b/stack.yaml, the file that closes the cycle", err)
	}
}

func TestChainDetectsASelfParent(t *testing.T) {
	stacks := loadStacks(t, map[string]string{"a": "image: a:v1\nparent: a\n"})
	_, _, err := Chain(stacks, "")
	if err == nil {
		t.Fatal("expected a refusal on a stack that is its own parent")
	}
	if !strings.Contains(err.Error(), "a → a") {
		t.Errorf("message = %q, want the cycle `a → a`", err)
	}
}

// A missing parent names the stack that DECLARES it and the file to edit. The
// bare "stack not found" config.Stacks.Get produces is true and unactionable:
// nothing in it points at the `parent:` line at fault.
func TestChainNamesTheStackDeclaringAMissingParent(t *testing.T) {
	stacks := loadStacks(t, map[string]string{"dgdevx": "image: dgdevx:v1\nparent: devx\n"})
	_, _, err := Chain(stacks, "dgdevx")
	if err == nil {
		t.Fatal("expected a refusal on a parent that does not exist")
	}
	msg := err.Error()
	for _, want := range []string{`"dgdevx"`, `"devx"`, "parent:", filepath.Join("stacks", "dgdevx", "stack.yaml")} {
		if !strings.Contains(msg, want) {
			t.Errorf("message = %q, want it to contain %q", msg, want)
		}
	}
}

// A target that does not exist gets config.Stacks.Get's own verdict, which
// lists the declared stacks — den never invents a second wording for it.
func TestChainRefusesAnUnknownTarget(t *testing.T) {
	_, _, err := Chain(loadStacks(t, fixture), "nope")
	if err == nil {
		t.Fatal("expected a refusal on an unknown target")
	}
	if !strings.Contains(err.Error(), "alpha") {
		t.Errorf("message = %q, want it to list the declared stacks", err)
	}
}

// A stack present on disk but unreadable must not sink the whole build — the
// doctrine config.LoadStacks states. It is simply absent from the chain; the
// CLI reports it by name on stderr.
func TestChainWalksTheHealthyStacksAroundABrokenOne(t *testing.T) {
	stacks := loadStacks(t, map[string]string{
		"alpha":  "image: alpha:v1\n",
		"broken": "imag: typo\n", // strict decoding: unknown key
	})
	chain, excluded, err := Chain(stacks, "")
	if err != nil {
		t.Fatalf("a broken stack must not fail the whole chain: %v", err)
	}
	if got := names(chain); len(got) != 1 || got[0] != "alpha" {
		t.Errorf("chain = %v, want [alpha]", got)
	}
	// And nothing is EXCLUDED: a broken stack that is nobody's parent costs no
	// other stack its build, so den has nothing to report beyond the stack
	// itself — which the CLI already names from config.Stacks.Broken.
	if len(excluded) != 0 {
		t.Errorf("excluded = %+v, want none: broken is nobody's parent", excluded)
	}
}

// ...but it IS named when it is the target, with the "unreadable" verdict and
// not "not found": the user has the file, they must fix it, not create it.
func TestChainRefusesABrokenTargetAsUnreadable(t *testing.T) {
	stacks := loadStacks(t, map[string]string{"broken": "imag: typo\n"})
	_, _, err := Chain(stacks, "broken")
	if err == nil {
		t.Fatal("expected a refusal on an unreadable target")
	}
	if !strings.Contains(err.Error(), "unreadable") {
		t.Errorf("message = %q, want the `unreadable` verdict, not `not found`", err)
	}
}

// The same doctrine one level removed, and THE DEFECT this test exists for
// (measured on this branch, 2026-08-03): a broken stack used as a `parent:`
// made `den build` refuse and build nothing — alpha included, which has nothing
// to do with it. Without a target the healthy stacks are still walked; the one
// whose ancestry reaches the broken stack is EXCLUDED and named, because a
// silent skip and a forgotten stack look identical from the outside (Step.
// Skipped says the same thing about the other skip).
func TestChainExcludesAStackWhoseParentIsUnreadable(t *testing.T) {
	stacks := loadStacks(t, map[string]string{
		"alpha":  "image: alpha:v1\n",
		"broken": "imag: typo\n", // strict decoding: unknown key
		"child":  "image: child:v1\nparent: broken\n",
	})
	chain, excluded, err := Chain(stacks, "")
	if err != nil {
		t.Fatalf("a broken PARENT must not fail the whole chain: %v", err)
	}
	if got := names(chain); len(got) != 1 || got[0] != "alpha" {
		t.Errorf("chain = %v, want [alpha] — child's ancestry is broken, alpha's is not", got)
	}
	if len(excluded) != 1 || excluded[0].Stack != "child" {
		t.Fatalf("excluded = %+v, want child left out", excluded)
	}
	// The reason sends the user to the BROKEN stack, not to child's `parent:`
	// line, which names a stack that is really there.
	if !strings.Contains(excluded[0].Reason, "broken") || !strings.Contains(excluded[0].Reason, "unreadable") {
		t.Errorf("reason = %q, want it to name the unreadable ancestor as the thing to fix", excluded[0].Reason)
	}
	if strings.Contains(excluded[0].Reason, "parent:") {
		t.Errorf("reason = %q, want no `parent:` remedy: child's own line is correct", excluded[0].Reason)
	}
}

// A `parent:` naming a stack that does not exist AT ALL is the same defect
// class — it must not sink the healthy stacks either — with the OTHER remedy:
// there the fault really is the `parent:` line, so the report names the file
// that declares it. Sending the user to a stack that does not exist would name
// no file at all.
func TestChainExcludesAStackWhoseParentDoesNotExist(t *testing.T) {
	stacks := loadStacks(t, map[string]string{
		"alpha":  "image: alpha:v1\n",
		"dgdevx": "image: dgdevx:v1\nparent: devx\n",
	})
	chain, excluded, err := Chain(stacks, "")
	if err != nil {
		t.Fatalf("a missing PARENT must not fail the whole chain: %v", err)
	}
	if got := names(chain); len(got) != 1 || got[0] != "alpha" {
		t.Errorf("chain = %v, want [alpha] — dgdevx cannot be built, alpha can", got)
	}
	if len(excluded) != 1 || excluded[0].Stack != "dgdevx" {
		t.Fatalf("excluded = %+v, want dgdevx left out", excluded)
	}
	for _, want := range []string{`"devx"`, "parent:", filepath.Join("stacks", "dgdevx", "stack.yaml")} {
		if !strings.Contains(excluded[0].Reason, want) {
			t.Errorf("reason = %q, want it to contain %q", excluded[0].Reason, want)
		}
	}
}

// Every affected stack is named, not just the top of the subtree: `den build`
// reports what it did not build, and leaving the user to deduce grandchild from
// child is the same silence as not naming it at all.
func TestChainExcludesEveryDescendantOfABrokenStack(t *testing.T) {
	stacks := loadStacks(t, map[string]string{
		"broken":     "imag: typo\n",
		"child":      "image: child:v1\nparent: broken\n",
		"grandchild": "image: grandchild:v1\nparent: child\n",
	})
	chain, excluded, err := Chain(stacks, "")
	if err != nil {
		t.Fatalf("a broken ancestor must not fail the whole chain: %v", err)
	}
	if len(chain) != 0 {
		t.Errorf("chain = %v, want nothing buildable: both stacks descend from broken", names(chain))
	}
	// In root order, which is name order — the same determinism the golden rests on.
	got := make([]string, 0, len(excluded))
	for _, x := range excluded {
		if !strings.Contains(x.Reason, "broken") {
			t.Errorf("%s excluded for %q, want the unreadable stack named", x.Stack, x.Reason)
		}
		got = append(got, x.Stack)
	}
	if strings.Join(got, ",") != "child,grandchild" {
		t.Errorf("excluded = %v, want child,grandchild — each named on its own", got)
	}
}

// Under a TARGET the same broken ancestor IS a refusal — the user named the
// stack, and building it is impossible. The message must send them to the
// unreadable stack's own file: its child's `parent:` line is correct, it names
// a stack that is really there, and "fix `parent:` in <child>" would point at
// the one line that is not wrong.
func TestChainRefusesATargetWhoseAncestorIsUnreadable(t *testing.T) {
	stacks := loadStacks(t, map[string]string{
		"broken": "imag: typo\n",
		"child":  "image: child:v1\nparent: broken\n",
	})
	_, _, err := Chain(stacks, "child")
	if err == nil {
		t.Fatal("expected a refusal on a target whose ancestor does not load")
	}
	msg := err.Error()
	for _, want := range []string{`"child"`, "unreadable", filepath.Join("stacks", "broken", "stack.yaml")} {
		if !strings.Contains(msg, want) {
			t.Errorf("message = %q, want it to contain %q", msg, want)
		}
	}
	for _, unwanted := range []string{"fix `parent:`", filepath.Join("stacks", "child", "stack.yaml")} {
		if strings.Contains(msg, unwanted) {
			t.Errorf("message = %q, want it NOT to send the user to %q — the fault is the parent's file", msg, unwanted)
		}
	}
}

func TestChainOnAnEmptyDenIsEmpty(t *testing.T) {
	chain, _, err := Chain(loadStacks(t, nil), "")
	if err != nil {
		t.Fatalf("a den with no stack is not an error: %v", err)
	}
	if len(chain) != 0 {
		t.Errorf("chain = %v, want empty", names(chain))
	}
}
