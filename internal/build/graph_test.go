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
	chain, err := Chain(loadStacks(t, fixture), "")
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
	first, err := Chain(stacks, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := Chain(stacks, "")
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
	chain, err := Chain(loadStacks(t, fixture), "delta")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.Join(names(chain), ","); got != "alpha,gamma,delta" {
		t.Errorf("chain = %v, want alpha,gamma,delta (ancestors first, nothing else)", names(chain))
	}
}

func TestChainOnARootTargetIsJustThatStack(t *testing.T) {
	chain, err := Chain(loadStacks(t, fixture), "zeta")
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
	_, err := Chain(stacks, "a")
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
	_, err := Chain(stacks, "")
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
	_, err := Chain(stacks, "dgdevx")
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
	_, err := Chain(loadStacks(t, fixture), "nope")
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
	chain, err := Chain(stacks, "")
	if err != nil {
		t.Fatalf("a broken stack must not fail the whole chain: %v", err)
	}
	if got := names(chain); len(got) != 1 || got[0] != "alpha" {
		t.Errorf("chain = %v, want [alpha]", got)
	}
}

// ...but it IS named when it is the target, with the "unreadable" verdict and
// not "not found": the user has the file, they must fix it, not create it.
func TestChainRefusesABrokenTargetAsUnreadable(t *testing.T) {
	stacks := loadStacks(t, map[string]string{"broken": "imag: typo\n"})
	_, err := Chain(stacks, "broken")
	if err == nil {
		t.Fatal("expected a refusal on an unreadable target")
	}
	if !strings.Contains(err.Error(), "unreadable") {
		t.Errorf("message = %q, want the `unreadable` verdict, not `not found`", err)
	}
}

func TestChainOnAnEmptyDenIsEmpty(t *testing.T) {
	chain, err := Chain(loadStacks(t, nil), "")
	if err != nil {
		t.Fatalf("a den with no stack is not an error: %v", err)
	}
	if len(chain) != 0 {
		t.Errorf("chain = %v, want empty", names(chain))
	}
}
