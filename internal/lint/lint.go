// Package lint validates a stacks/nests checkout — a team source repo or a
// clone of one — without touching the sbx BINARY, git or the network: it
// imports internal/sbx for its pure validators (ValidateMemory, ValidateCPUs
// — neither shells out), the same way it already imports internal/config and
// internal/nest, to judge under the compiler role (spec 2026-08-24 §6.4) what
// `sbx env create` would later refuse. SandboxName is deliberately NOT among
// them: the nest-level name check it used to back was removed (see checkNest,
// below), because nest.LoadNest already validates the charset before a *Nest
// reaches this package, and the length floor belongs at the point sbx sees
// the assembled name, not here. ONE implementation, three consumers (spec
// 2026-08-04 §5): the team repo's CI (`den lint`), `den source add`
// (post-clone) and `den source update` (pre-fast-forward, the fail-closed
// gate).
package lint

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
	"github.com/PillowPillow/den/internal/sbx"
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
	resolved, errs := resolveRoot(root)
	if len(errs) > 0 {
		return errs
	}
	stacks, err := config.LoadStacks(resolved)
	if err != nil {
		return []error{err} // structural: nothing more can be judged
	}
	nests, broken, err := nest.ListNests(resolved)
	if err != nil {
		return []error{err}
	}
	for _, b := range broken {
		errs = append(errs, fmt.Errorf("nest %q: %w", b.Name, b.Err))
	}
	// stacks passed for BOTH parameters: without a catalogue there is no
	// narrower "judged" set than the full scan — see judge's doc.
	return append(errs, judge(resolved, stacks, stacks, nests)...)
}

// Catalogue is the explicit export list of a manifested source: the names it
// publishes, nothing else. Names only, no paths — a manifested source's
// exports are validated to sit at the canonical location before lint runs
// (source.ValidateManifest), so the loaders' own by-name rule is what reads
// them here, as it is at spawn.
type Catalogue struct {
	Stacks []string
	Nests  []string
}

// RunCatalogue is Run for a source that PUBLISHES its objects (spec
// 2026-08-14 §5.2): the same checks, over the declared catalogue instead of
// whatever the directories contain — with ONE exception, a stack's
// `parent:` (see below).
//
// nest `stack:` stays scoped to the catalogue. A stack present in the
// checkout but absent from `exports.stacks` is an implementation detail of
// the source, so an exported nest may not resolve its `stack:` through it —
// the teammate who installs the source addresses exported names and nothing
// else. Feeding the directory scan to THAT check would accept exactly what
// spawn later refuses, and the refusal would only appear on the teammate's
// machine.
//
// stack `parent:` does NOT stay scoped to the catalogue (Task 7 review fix,
// 2026-08-16). A stack's parent is internal composition — nothing on the
// personal side ever addresses it — and buildDriver.Apply
// (internal/converge/build.go) already resolves the chain through a full
// config.LoadStacks scan plus build.Chain: den BUILDS what the
// catalogue-scoped version REFUSED, with a message that named the
// checkout's own stacks/ directory, where the parent visibly exists. judge
// is handed two config.Stacks for exactly this split: `stacks`, the
// export-scoped set that decides WHAT is judged, and `parents`, the full
// checkout scan that decides what a `parent:` chain may resolve THROUGH.
//
// Ruling on unexported faults: an unexported stack's own faults (a broken
// stack.yaml, a bad `kit:`/`provision.*` path, its OWN unresolved
// `parent:`) ARE lint findings, but only when the stack lies on an EXPORTED
// stack's `parent:` ancestry — the closure parentClosure computes below.
// That closure, not the export list, is the published surface for
// `parent:` purposes: it is exactly what `den build` walks for that export
// (build.Chain refuses on ANY broken link or cycle anywhere in the chain,
// not only the immediate parent), so judging less would let lint ACCEPT a
// chain build REFUSES — the direction that matters more, since
// source.Lint deletes the clone it accepted. An orphan stack that no
// export's ancestry reaches stays unjudged: "nothing published can reach
// it" still holds for it, unchanged from before this fix.
func RunCatalogue(root string, cat Catalogue) []error {
	resolved, errs := resolveRoot(root)
	if len(errs) > 0 {
		return errs
	}

	// The full scan `parent:` resolves through — see the doc above. A
	// structural failure here (an unreadable stacks/ directory) is reported
	// the same way Run reports it: nothing more can be judged.
	parents, err := config.LoadStacks(resolved)
	if err != nil {
		return append(errs, err)
	}

	// Built by name rather than filtered from LoadStacks: an EXPORTED stack
	// that does not load must be reported (the catalogue promised it), and a
	// filter over the scan would silently drop it — the scan skips a
	// directory without a stack.yaml entirely.
	stacks := config.Stacks{Healthy: map[string]*config.Stack{}, Root: filepath.Join(resolved, "stacks")}
	for _, name := range cat.Stacks {
		s, err := config.LoadStack(resolved, name)
		if err != nil {
			stacks.Broken = append(stacks.Broken, config.BrokenStack{Name: name, Err: err})
			continue
		}
		stacks.Healthy[name] = s
	}

	var nests []*nest.Nest
	for _, name := range cat.Nests {
		n, err := nest.LoadNest(resolved, name)
		if err != nil {
			errs = append(errs, fmt.Errorf("nest %q: %w", name, err))
			continue
		}
		nests = append(nests, n)
	}
	return append(errs, judge(resolved, stacks, parents, nests)...)
}

// judge is the shared verdict: the checks themselves never learn WHERE the
// objects came from. One implementation, so a manifested source can never be
// accepted on a rule a legacy one is refused on, or the reverse.
//
// Two config.Stacks, not one: `stacks` decides what is JUDGED — whose own
// faults are findings, and what a nest's `stack:` may resolve through.
// `parents` decides what a `parent:` chain may resolve THROUGH, and may be
// broader. Run passes the same value for both — without a catalogue,
// "judged" and "resolvable" are already the same set. RunCatalogue does not
// (see its doc).
func judge(root string, stacks, parents config.Stacks, nests []*nest.Nest) []error {
	var errs []error
	for _, b := range stacks.Broken {
		errs = append(errs, fmt.Errorf("stack %q: %w", b.Name, b.Err))
	}
	// Every JUDGED root is checked against ITS OWN loaded copy — stacks,
	// never parents (final whole-branch review, I1, 2026-08-16). The two
	// copies can disagree: config.LoadStacks (which builds parents) skips a
	// `stacks/<name>` directory entry whose os.ReadDir DirEntry.IsDir() lies
	// — lstat-based, so a SYMLINK to a directory answers false — while
	// config.LoadStack (which RunCatalogue uses to build stacks, by name,
	// from the catalogue) reads straight through the symlink and loads it
	// fine. Before this loop existed, a root absent from parents.Healthy was
	// silently absent from the closure below too (parentClosure's walk keys
	// on parents.Healthy), so checkStack never ran on it at all: an exported
	// stack behind a symlink could declare an absolute path or an escaping
	// one and lint clean. Iterating stacks.Names() first, unconditionally,
	// is what `judge`'s own doc already promised — "stacks decides what is
	// JUDGED" — and this is the only loop that can make good on it, because
	// only `stacks` is guaranteed to hold every root's real load.
	for _, name := range stacks.Names() {
		errs = append(errs, checkStack(root, parents, stacks.Healthy[name])...)
		// checkStackResources runs ONLY here, over the JUDGED set, never in
		// the closure loop below — see its own doc for why.
		errs = append(errs, checkStackResources(stacks.Healthy[name])...)
	}
	// The ancestor closure of the JUDGED stacks, resolved through `parents`:
	// for Run, stacks == parents == the full scan, so every healthy stack is
	// already its own root, already checked above, and this loop adds
	// nothing new — unchanged from before this function took two arguments.
	// For RunCatalogue it is strictly the exported stacks plus every
	// unexported stack their `parent:` chain actually reaches; see
	// parentClosure's doc for why an orphan stays out of it. The `isRoot`
	// guard skips names the loop above already checked (against `stacks`,
	// their own copy) so a root's fault is never reported twice.
	closure := parentClosure(stacks, parents)
	for _, name := range closure {
		if _, isRoot := stacks.Healthy[name]; isRoot {
			continue
		}
		errs = append(errs, checkStack(root, parents, parents.Healthy[name])...)
	}
	errs = append(errs, checkCycles(parents, closure)...)
	for _, n := range nests {
		errs = append(errs, checkNest(root, stacks, n)...)
	}
	return errs
}

// parentClosure returns the names of every JUDGED stack (roots) together
// with every HEALTHY stack their `parent:` chain reaches, transitively,
// resolved through parents — sorted, for the same reproducibility reason
// config.Stacks.Names is.
//
// A stack that fails to resolve (absent, or present but broken) is never
// added: there is no *config.Stack for judge's checkStack loop to run on,
// and its OWN fault already surfaces at the point that names it — the
// CHILD's `stacks.Get(s.Parent)` in checkStack, which distinguishes
// "unreadable" from "does not exist" and cites the right file either way.
// Walking further past it here would only rediscover the same fault a
// second time, with a worse message.
//
// The `seen` guard doubles as cycle protection: a `parent:` cycle would
// otherwise recurse forever. It reports NOTHING about the cycle itself —
// that verdict belongs to checkCycles, which judge calls with this same
// closure as its start set.
func parentClosure(roots, parents config.Stacks) []string {
	seen := map[string]bool{}
	var walk func(name string)
	walk = func(name string) {
		s, ok := parents.Healthy[name]
		if !ok || seen[name] {
			return
		}
		seen[name] = true
		if s.Parent != "" {
			walk(s.Parent)
		}
	}
	for _, name := range roots.Names() {
		walk(name)
	}
	return slices.Sorted(maps.Keys(seen))
}

// resolveRoot turns the caller's path into the one every confinement check
// compares against.
func resolveRoot(root string) (string, []error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", []error{fmt.Errorf("resolving %q: %w", root, err)}
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return "", []error{fmt.Errorf("%s: not a directory — `den lint` validates a checkout root", abs)}
	}
	// EvalSymlinks the root itself, once, so every confinement check below
	// compares like-for-like: a declared path is resolved the SAME way (see
	// checkDeclaredPath). Comparing a resolved declared path against an
	// unresolved root would misjudge any checkout that itself sits behind a
	// symlink — verified on this repo's own dev machine, where macOS
	// resolves a plain t.TempDir() under a symlinked /var → /private/var
	// (Task 4 review finding #2). Stat above already proved abs exists, so
	// this cannot fail on ENOENT.
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", []error{fmt.Errorf("resolving %s: %w", abs, err)}
	}
	return resolved, nil
}

// checkStack judges one healthy stack: its parent reference, and every path
// it declares. Paths were made absolute by LoadStack against the stack dir;
// what is checked here is that they were written RELATIVE and stay INSIDE
// root and exist. Confinement is a shareability rule, not a security one: a
// path that escapes the checkout depends on the machine that receives the
// source, so the object is not distributable (spec 2026-08-04 §5).
//
// parents resolves `s.Parent` — the set judge's caller decides that against
// (see judge's doc), which for RunCatalogue is broader than the stacks this
// function is CALLED for.
func checkStack(root string, parents config.Stacks, s *config.Stack) []error {
	var errs []error
	if s.Parent != "" {
		if source, _ := config.SplitSourceRef(s.Parent); source != "" {
			errs = append(errs, fmt.Errorf(
				"stack %q: `parent: %s` is a prefixed reference — inside a source, references are "+
					"bare and resolve in the source itself: the install name is chosen per machine "+
					"and CI knows none", s.Name, s.Parent))
		} else if _, err := parents.Get(s.Parent); err != nil {
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

// checkStackResources judges the `resources:` a stack DECLARES, on its own,
// without reading anywhere else in the cascade. Deliberately NOT called from
// checkStack: checkStack runs over every healthy stack judge decides is
// worth judging, roots AND the ancestor closure a `parent:` chain reaches
// (see judge's doc) — but `parent:` is a build-DAG edge, not field
// inheritance (config.Stack's own doc), so a stack that is ONLY a parent, and
// never a nest's own `stack:`, has its `resources:` read by nothing:
// nest.Resolve merges the global, the NEST'S OWN stack (never an ancestor)
// and the nest, and `den build` emits neither `--memory` nor `--cpus` at all.
// Refusing over a value no spawn and no build ever sends would delete a
// clone (`den source add`) over dead configuration. So this is called only
// from judge's roots loop, over `stacks.Names()` — the JUDGED set, which for
// RunCatalogue is exactly the exported stacks, never the unexported parents
// the closure loop also runs checkStack over.
//
// This is also why the check stays SEPARATE from nest.Resolve's own
// (`resolveResources`, internal/nest/resources.go): that function
// deliberately validates only the CASCADE WINNER — the merged value a spawn
// actually sends — with its own comment stating the same reasoning in the
// other direction (refusing over a value nothing sends refuses a
// configuration that works). Both rules are right, and they diverge on
// purpose: a consumer nest that writes no `resources:` of its own INHERITS
// the stack's value, so an exported stack's OWN `memory: 512m` is exactly
// the case where lint's stricter, source-side judgment earns its keep —
// `den source add` refuses it before any consumer ever resolves a cascade
// that would relay it.
func checkStackResources(s *config.Stack) []error {
	var errs []error
	// The SAME validators nest.Resolve uses, at a better occasion: `den
	// source add` refuses and deletes the clone, `den source update` refuses
	// before the fast-forward. An illegal size must die at the source, once,
	// not N times on its consumers — and sbx only refuses it server-side,
	// after the image pull (§14.5).
	if s.Resources.CPUs != nil {
		if err := sbx.ValidateCPUs(*s.Resources.CPUs); err != nil {
			errs = append(errs, fmt.Errorf("stack %q: %w", s.Name, err))
		}
	}
	if err := sbx.ValidateMemory(s.Resources.Memory); err != nil {
		errs = append(errs, fmt.Errorf("stack %q: %w", s.Name, err))
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

// checkCycles walks parent edges among HEALTHY stacks in stacks, starting
// only from startNames — the set judge decided is worth judging (every
// healthy stack for Run, the exported stacks' ancestor closure for
// RunCatalogue). stacks itself may be broader than startNames: RunCatalogue
// passes the full checkout scan, because a cycle can close through a stack
// outside the export list, and build.Chain (internal/build/graph.go) would
// refuse it the SAME way at `den build` regardless of who exports what — a
// cycle among only-catalogue stacks is exactly the accept-what-build-refuses
// case judge's doc warns against. Three colors are not needed at this scale:
// a walked set per start plus a global done set keeps it linear and the
// first cycle found names its members.
func checkCycles(stacks config.Stacks, startNames []string) []error {
	var errs []error
	done := map[string]bool{}
	for _, start := range startNames {
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
// resolvable in THIS checkout, and its own `resources:` (a cascade level in
// its own right, not only a stack's) must be a size sbx would accept. It
// does NOT re-check the sandbox-name charset — nest.LoadNest already did,
// and does not check name LENGTH at all — see the comment at the decision
// site below for why that check does not belong here.
func checkNest(root string, stacks config.Stacks, n *nest.Nest) []error {
	var errs []error
	// A switch, not three early returns: the three `stack:` faults exclude one
	// another (a missing reference cannot also be prefixed, and neither can be
	// resolved), but the `repos:` check below is independent of all of them and
	// must still run. Returning early there meant a nest with two unrelated
	// problems reported one — the "one push, not five" promise Run's godoc
	// makes, broken on the one object where both checks apply.
	switch source, _ := config.SplitSourceRef(n.Stack); {
	case n.Stack == "":
		errs = append(errs, fmt.Errorf(
			"nest %q: no `stack:` — a source nest cannot fall back on the personal defaults.stack: "+
				"it must spawn identically on every machine", n.Name))
	case source != "":
		errs = append(errs, fmt.Errorf(
			"nest %q: `stack: %s` is a prefixed reference — inside a source, references are bare "+
				"and resolve in the source itself: the install name is chosen per machine and CI "+
				"knows none", n.Name, n.Stack))
	default:
		if _, err := stacks.Get(n.Stack); err != nil {
			errs = append(errs, fmt.Errorf("nest %q: %w", n.Name, err))
		}
	}

	// NO check here against sbx's whole-name length floor
	// (sbx.SandboxName's MinNameLength), and deliberately so (Task 7 fix
	// round 1, review finding #1) — the charset half of "would this name a
	// legal sandbox" IS checked, just earlier and elsewhere: nest.LoadNest
	// already runs config.ValidateSandboxComponent("nest", n.Name) before a
	// *Nest ever reaches this function, so by the time checkNest sees
	// n.Name it has already passed the ONLY thing sbx.SandboxName(n.Name,
	// "") would check beyond length, given an empty worktree.
	//
	// The length floor itself does not belong here because `den lint`
	// validates a SOURCE checkout, never a personal one
	// (internal/cli/lint.go: "lint never reads the personal
	// configuration") — and a nest loaded FROM a source never spawns under
	// its bare n.Name at all: spawn.go flattens it to "<srcName>-<bareNest>"
	// first (internal/spawn/spawn.go's sandbox-naming comment, above its
	// FlattenSandboxComponent("nest", o.Nest) call), which is at least four
	// characters before instance is even considered. So
	// SandboxName(n.Name, "") was refusing a name no spawn of an installed
	// source ever sends — deleting a clone (`den source add`) over a false
	// positive. The name sbx actually sees is checked where it is finally
	// ASSEMBLED: spawn.go's own sbx.SandboxName(nestComponent, instance)
	// call, after flattening and instance are both applied.

	// The SAME resources check as checkStack's, for the SAME reason, one
	// cascade level up: `resources:` on a nest is not a personal-config-only
	// field (spec §12 lists `nests/<n>.yaml` as one of the four cascade
	// levels, alongside `stack.yaml`), so a team nest can ship an illegal
	// `memory:` just as easily as a team stack can. nest.Resolve refuses it
	// at every spawn regardless of which level supplied it — but that is
	// still N refusals on N consumer machines. Skipping this level would
	// leave exactly the gap task 7 exists to close, one field over from
	// where the spec's own §6.4 example (a stack's `memory:`) points.
	if n.Resources.CPUs != nil {
		if err := sbx.ValidateCPUs(*n.Resources.CPUs); err != nil {
			errs = append(errs, fmt.Errorf("nest %q: %w", n.Name, err))
		}
	}
	if err := sbx.ValidateMemory(n.Resources.Memory); err != nil {
		errs = append(errs, fmt.Errorf("nest %q: %w", n.Name, err))
	}

	return append(errs, checkNestRepos(n)...)
}

// checkNestRepos refuses ANY `path:` in a source nest's `repos:` — a stack
// has had checkDeclaredPath from the start, while a nest had no check at all.
// The rule is not "no ABSOLUTE path": a source is cloned onto machines with
// different layouts, and a work repo lives entirely outside the checkout, so
// a RELATIVE path is just as meaningless as an absolute one — it resolves
// against whatever directory each teammate happened to launch den from, not
// against anything the source itself defines. A source nest shipping
// `path: ../../dev/api` linted clean and then failed (or silently pointed at
// the wrong repo) on every colleague's machine, which is exactly the class of
// fault `den lint` exists to catch before the push. So the check has no
// `filepath.IsAbs` branch at all: `path:` is either empty (skip) or refused.
//
// The REMEDY is not checkDeclaredPath's, and deliberately so: telling the
// author to "declare it relative" is right for a stack's `kit:` (relative to
// the stack directory, and shipped inside the checkout) and wrong here. A
// work repo is not in the source tree at all, so no relative path could name
// it either. The only form that travels is `key:` plus the personal `repos:`
// mapping each teammate fills in — spec 2026-08-04 §2.4, and the mechanism
// that makes a team nest shareable in the first place.
//
// The judgement is on the path as LOADED, not as written, so `~/dev/x` is
// caught too: nest.LoadNest expands it (config.ExpandPath), and it names a
// directory only the authoring machine has, which is the same fault.
func checkNestRepos(n *nest.Nest) []error {
	var errs []error
	for _, r := range n.Repos {
		if r.Path == "" {
			continue
		}
		errs = append(errs, fmt.Errorf(
			"nest %q: `path: %s` cannot travel — a source is cloned onto machines with "+
				"different layouts, and a work repo lives outside the checkout, so neither an "+
				"absolute path nor one relative to it means anything on a colleague's machine. "+
				"Declare `key: <name>` instead and let each machine map it under `repos:` in its "+
				"own config.yaml", n.Name, r.Path))
	}
	return errs
}
