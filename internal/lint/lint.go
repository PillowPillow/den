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
// An empty result means valid. The checks reuse the production loaders
// (config.LoadStacks, nest.ListNests) so lint can never accept what a spawn
// would later refuse — one judge, not two.
func Run(root string) []error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return []error{fmt.Errorf("resolving %q: %w", root, err)}
	}
	root = abs
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return []error{fmt.Errorf("%s: not a directory — `den lint` validates a checkout root", root)}
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
// what is checked here is that they stay INSIDE root and exist. Confinement
// is a shareability rule, not a security one: a path that escapes the
// checkout depends on the machine that receives the source, so the object is
// not distributable (spec 2026-08-04 §5).
func checkStack(root string, stacks config.Stacks, s *config.Stack) []error {
	var errs []error
	if s.Parent != "" {
		if strings.Contains(s.Parent, config.SourceRefSeparator) {
			errs = append(errs, fmt.Errorf(
				"stack %q: `parent: %s` is a prefixed reference — inside a source, references are "+
					"bare and resolve in the source itself: the install name is chosen per machine "+
					"and CI knows none", s.Name, s.Parent))
		} else if _, err := stacks.Get(s.Parent); err != nil {
			errs = append(errs, fmt.Errorf("stack %q: %w", s.Name, err))
		}
	}
	paths := slices.Concat(s.DeclaredKits(), s.Provision.Includes, s.Provision.Steps)
	for _, p := range paths {
		rel, err := filepath.Rel(root, p)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			errs = append(errs, fmt.Errorf(
				"stack %q: %s escapes the checkout — a source is self-contained: a path outside "+
					"its tree depends on the receiving machine and cannot be shared", s.Name, p))
			continue
		}
		if _, err := os.Stat(p); err != nil {
			errs = append(errs, fmt.Errorf("stack %q: %s: %w", s.Name, p, err))
		}
	}
	return errs
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
	if strings.Contains(n.Stack, config.SourceRefSeparator) {
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
