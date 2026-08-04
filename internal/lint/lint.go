// Package lint validates a stacks/nests checkout — a team source repo or a
// clone of one — without touching git, sbx or the network. ONE implementation,
// three consumers (spec 2026-08-04 §5): the team repo's CI (`den lint`),
// `den source add` (post-clone) and `den source update` (pre-fast-forward,
// the fail-closed gate).
package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
)

// Run validates the checkout rooted at root and returns EVERY finding, in a
// deterministic order — a CI log must be reproducible, and showing one error
// per push when a repo has five is five pushes instead of one.
//
// A nil (or empty) result means valid — callers should test len(), never
// compare against nil. The checks reuse the production loaders
// (config.LoadStacks, nest.ListNests) so lint can never accept what a spawn
// would later refuse — one judge, not two.
func Run(root string) []error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return []error{fmt.Errorf("resolving %q: %w", root, err)}
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return []error{fmt.Errorf("%s: not a directory — `den lint` validates a checkout root", abs)}
	}
	// EvalSymlinks the root itself, once, so every confinement check below
	// compares like-for-like: a declared path is resolved the SAME way (see
	// checkDeclaredPath). Comparing a resolved declared path against an
	// unresolved root would misjudge any checkout that itself sits behind a
	// symlink — verified on this repo's own dev machine, where macOS
	// resolves a plain t.TempDir() under a symlinked /var → /private/var
	// (Task 4 review finding #2). Stat above already proved abs exists, so
	// this cannot fail on ENOENT.
	root, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return []error{fmt.Errorf("resolving %s: %w", abs, err)}
	}

	var errs []error

	stacks, err := config.LoadStacks(root)
	if err != nil {
		return append(errs, err) // structural: nothing more can be judged
	}
	for _, b := range stacks.Broken {
		errs = append(errs, fmt.Errorf("stack %q: %w", b.Name, b.Err))
	}

	for _, name := range stacks.Names() {
		s := stacks.Healthy[name]
		errs = append(errs, checkStack(root, stacks, s)...)
	}
	errs = append(errs, checkCycles(stacks)...)

	nests, broken, err := nest.ListNests(root)
	if err != nil {
		return append(errs, err)
	}
	for _, b := range broken {
		errs = append(errs, fmt.Errorf("nest %q: %w", b.Name, b.Err))
	}
	for _, n := range nests {
		errs = append(errs, checkNest(root, stacks, n)...)
	}
	return errs
}

// checkStack judges one healthy stack: its parent reference, and every path
// it declares. Paths were made absolute by LoadStack against the stack dir;
// what is checked here is that they were written RELATIVE and stay INSIDE
// root and exist. Confinement is a shareability rule, not a security one: a
// path that escapes the checkout depends on the machine that receives the
// source, so the object is not distributable (spec 2026-08-04 §5).
func checkStack(root string, stacks config.Stacks, s *config.Stack) []error {
	var errs []error
	if s.Parent != "" {
		if source, _ := config.SplitSourceRef(s.Parent); source != "" {
			errs = append(errs, fmt.Errorf(
				"stack %q: `parent: %s` is a prefixed reference — inside a source, references are "+
					"bare and resolve in the source itself: the install name is chosen per machine "+
					"and CI knows none", s.Name, s.Parent))
		} else if _, err := stacks.Get(s.Parent); err != nil {
			errs = append(errs, fmt.Errorf("stack %q: %w", s.Name, err))
		}
	}

	// Each group is checked under its OWN YAML key, not flattened: a
	// refusal must name the exact key to fix (`kit:` vs `kits:` vs
	// `provision.includes:` vs `provision.steps:`), and only the loader
	// (config.Stack.AbsoluteDeclaredPaths) still knows which group an
	// absolute entry came from.
	var kit []string
	if s.Kit != "" {
		kit = []string{s.Kit}
	}
	groups := []struct {
		key   string
		paths []string
	}{
		{"kit", kit},
		{"kits", s.Kits},
		{"provision.includes", s.Provision.Includes},
		{"provision.steps", s.Provision.Steps},
	}
	for _, g := range groups {
		abs := s.AbsoluteDeclaredPaths[g.key] // parallel by index to g.paths, see the field's doc
		for i, p := range g.paths {
			if p == "" {
				continue
			}
			wasAbsolute := i < len(abs) && abs[i]
			errs = append(errs, checkDeclaredPath(root, s, g.key, p, wasAbsolute)...)
		}
	}
	return errs
}

// checkDeclaredPath judges one path declared under key ("kit", "kits",
// "provision.includes" or "provision.steps"). Three refusals, checked in an
// order chosen so each error names the clearest possible cause:
//
//  1. Written absolute in stack.yaml — refused UNCONDITIONALLY, regardless
//     of where it happens to point on THIS machine. An absolute path is a
//     property of the authoring machine; it can coincidentally resolve
//     inside root there and still be meaningless on a colleague's clone
//     (Task 4 review finding #1). Checked first because it is a judgment
//     about the DECLARATION, not about the filesystem — no reason to let a
//     Stat outcome distract from it.
//  2. Missing — checked before symlink resolution so a nonexistent path
//     reports "does not exist", not EvalSymlinks' own ENOENT wording aimed
//     at a different failure.
//  3. Escapes the checkout — root and the declared path are BOTH resolved
//     through EvalSymlinks before comparing. Resolving only one side would
//     misjudge either direction: a checkout itself behind a symlink (root
//     needs resolving) or, the case this exists for, a symlink COMMITTED
//     INSIDE the checkout pointing outside it (lexically confined, Stat
//     follows it and succeeds, and it dangles on a fresh clone — Task 4
//     review finding #2).
func checkDeclaredPath(root string, s *config.Stack, key, p string, wasAbsolute bool) []error {
	if wasAbsolute {
		return []error{fmt.Errorf(
			"stack %q: `%s: %s` is an absolute path in stack.yaml — a source is cloned onto "+
				"machines with different layouts; declare it relative to the stack directory "+
				"(or to the source root, via `../`) instead", s.Name, key, p)}
	}
	if _, err := os.Stat(p); err != nil {
		return []error{fmt.Errorf("stack %q: %s: %s: %w", s.Name, key, p, err)}
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return []error{fmt.Errorf("stack %q: %s: %s: %w", s.Name, key, p, err)}
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return []error{fmt.Errorf(
			"stack %q: %s: %s escapes the checkout — a source is self-contained: a path outside "+
				"its tree depends on the receiving machine and cannot be shared", s.Name, key, p)}
	}
	return nil
}

// checkCycles walks parent edges among HEALTHY stacks. Three colors are not
// needed at this scale: a walked set per start plus a global done set keeps
// it linear and the first cycle found names its members.
func checkCycles(stacks config.Stacks) []error {
	var errs []error
	done := map[string]bool{}
	for _, start := range stacks.Names() {
		if done[start] {
			continue
		}
		seen := map[string]bool{}
		var chain []string
		for cur := start; ; {
			if done[cur] {
				break
			}
			if seen[cur] {
				errs = append(errs, fmt.Errorf(
					"stack %q: `parent:` cycle (%s) — a build DAG must terminate on a `base:` stack",
					cur, strings.Join(append(chain, cur), " -> ")))
				break
			}
			seen[cur] = true
			chain = append(chain, cur)
			s, ok := stacks.Healthy[cur]
			if !ok || s.Parent == "" {
				break
			}
			cur = s.Parent
		}
		for n := range seen {
			done[n] = true
		}
	}
	slices.SortFunc(errs, func(a, b error) int { return strings.Compare(a.Error(), b.Error()) })
	return errs
}

// checkNest judges one loadable nest: its stack reference must be bare and
// resolvable in THIS checkout.
func checkNest(root string, stacks config.Stacks, n *nest.Nest) []error {
	var errs []error
	if n.Stack == "" {
		errs = append(errs, fmt.Errorf(
			"nest %q: no `stack:` — a source nest cannot fall back on the personal defaults.stack: "+
				"it must spawn identically on every machine", n.Name))
		return errs
	}
	if source, _ := config.SplitSourceRef(n.Stack); source != "" {
		errs = append(errs, fmt.Errorf(
			"nest %q: `stack: %s` is a prefixed reference — inside a source, references are bare "+
				"and resolve in the source itself: the install name is chosen per machine and CI "+
				"knows none", n.Name, n.Stack))
		return errs
	}
	if _, err := stacks.Get(n.Stack); err != nil {
		errs = append(errs, fmt.Errorf("nest %q: %w", n.Name, err))
	}
	return errs
}
