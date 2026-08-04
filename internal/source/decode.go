package source

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
)

// SandboxNest is the nest a LIVE sandbox came from: a root to load it from,
// the source it belongs to (empty for a local nest) and its bare name. It is
// Locate's return value in the other direction, and deliberately the same
// shape, so a caller that resolves a reference and a caller that resolves a
// sandbox name reach nest.LoadNest with the same three strings.
type SandboxNest struct {
	Root   string
	Source string
	Name   string
}

// Ref is the reference the user can TYPE to address this nest again — the
// only spelling a message may print, since a flattened sandbox name is
// exactly what den could not resolve without the walk below.
func (s SandboxNest) Ref() string { return config.JoinSourceRef(s.Source, s.Name) }

// nestCandidate is one (installed source, bare nest) pair whose flattened
// reference produces a given sandbox nest component. Exists says whether the
// nest file is really there — the two arbitrations below need the near-misses
// too, to tell "ambiguous" from "den looked and found nothing".
type nestCandidate struct {
	SandboxNest
	Path   string
	Exists bool
}

// nestCandidates works backwards from the sandbox name alone, and it can do
// so EXACTLY rather than by guessing, for the reason spawn's call site states
// when it flattens: flattening rewrites only the ":" separator, because an
// installed source name (config.ValidateSourceName) and a loadable nest name
// (config.ValidateSandboxComponent) are both already inside the sandbox
// charset. So "s:n" always flattens to literally "s-n", and for each installed
// source s the component admits at most ONE decomposition — the single prefix
// "s-". The remainder is then the only nest name that could have produced it.
//
// Deterministic order (sorted by source name) because two of the three
// consumers put these paths in an error message, and a refusal whose two
// candidates swap places between runs is a refusal nobody can quote.
//
// Fail-open on a ReadDir error, like spawn's crossSourceCollision — the
// sibling query on the same walk, which asks instead whether ANOTHER source
// also decomposes a name it is about to create. That one is kept separate on
// purpose: it excludes a source, answers a bool, and its refusals are pinned
// word for word.
func nestCandidates(denHome, component string) []nestCandidate {
	entries, err := os.ReadDir(Root(denHome))
	if err != nil {
		return nil
	}
	var out []nestCandidate
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		prefix := e.Name() + config.FlattenedSourceSeparator
		if !strings.HasPrefix(component, prefix) {
			continue
		}
		bare := component[len(prefix):]
		if bare == "" {
			continue
		}
		root := Dir(denHome, e.Name())
		path := nest.FilePath(root, bare)
		_, statErr := os.Stat(path)
		out = append(out, nestCandidate{
			SandboxNest: SandboxNest{Root: root, Source: e.Name(), Name: bare},
			Path:        path,
			Exists:      statErr == nil,
		})
	}
	slices.SortFunc(out, func(a, b nestCandidate) int { return strings.Compare(a.Source, b.Source) })
	return out
}

// DecodeSandboxNest maps a sandbox's NEST COMPONENT back to the nest that
// declared it — the reverse of the flattening every source nest spawns
// under, and the reason `den ports corp-api` can work at all.
//
// It exists because `den ls` prints the FLATTENED name: that string is what
// the user has in front of them, den's own --detach message recommends
// `den ports <it>`, and the README tells them to retry with it. Reading it as
// a local nest name — which is what a bare sbx.SplitName does — looks for
// <denHome>/nests/corp-api.yaml, a file that by construction never exists.
//
// The caller splits the ".<worktree>" suffix off FIRST (sbx.SplitName): the
// worktree is not part of what a source prefixes, and flattening excludes
// "." from the charset precisely so that split stays exact.
//
// THE DECODE IS UNAMBIGUOUS BY CONSTRUCTION, and that is spawn's doing rather
// than an assumption made here: before any side effect, spawn refuses both
// decompositions that could compete — a same-named LOCAL nest, and another
// installed source whose own decomposition also names a real nest file. So
// among loadable nests, at most one owner exists for a given string.
//
// What spawn cannot refuse is what happens AFTERWARDS: a local nest file
// written, or a second source installed, once the sandbox is already live.
// den does not arbitrate that. It refuses, naming both candidates and the
// prefixed spelling that is never ambiguous — the same shape as the spawn-time
// refusals, because it is the same fault arriving late.
func DecodeSandboxNest(denHome, component string) (SandboxNest, error) {
	local := nest.FilePath(denHome, component)
	_, localErr := os.Stat(local)
	localExists := localErr == nil

	all := nestCandidates(denHome, component)
	var found []nestCandidate
	for _, c := range all {
		if c.Exists {
			found = append(found, c)
		}
	}

	switch {
	case localExists && len(found) > 0:
		// THE SOURCE-SIDE SPELLING IS OFFERED CONDITIONALLY, never flatly, and
		// that is the whole care in this sentence. A live sandbox with this
		// name may genuinely have come from EITHER nest: spawn's collision
		// block runs only for a source reference (it sits inside
		// `if srcName != ""`), so `den corp-api` on the local nest is never
		// checked against source decompositions and creates this situation
		// with no refusal anywhere. A flattened sandbox name carries no record
		// of which nest made it — that is exactly why den cannot arbitrate.
		//
		// So `den ports corp:api` is a remedy for ONE of the two origins, and
		// on the other it would succeed while publishing the wrong nest's
		// declared ports into the sandbox — a command that works, does
		// something, and fixes nothing.
		//
		// The local-origin case has no spelling at all, so the honest remedy
		// is that the sandbox has to go. --keep-worktrees, because den cannot
		// attribute its worktrees to a nest either. The lasting fix is the
		// rename, and the LOCAL file is the one to rename: a rename inside a
		// source's clone is reverted by its next `den source update`.
		return SandboxNest{}, fmt.Errorf(
			"sandbox %q: two nests produce this name — the local nest %s and the source nest %s — "+
				"and a sandbox name records neither, so den will not guess which one declares its "+
				"repos. If it was spawned from the SOURCE nest, address it as `%s`, which always "+
				"resolves there. If it was spawned from the LOCAL nest, no spelling reaches it: "+
				"destroy it with `den rm %s --keep-worktrees` and re-spawn it once the names no "+
				"longer collide. Either way the lasting fix is to rename %s (renaming inside a "+
				"source is reverted by its next `den source update`)",
			component, local, found[0].Path, found[0].Ref(), component, local)

	case len(found) > 1:
		return SandboxNest{}, fmt.Errorf(
			"sandbox %q: two installed sources decompose this name — %s and %s. den will not guess "+
				"which one declares its repos; address it as `%s` or `%s`, which are never ambiguous",
			component, found[0].Path, found[1].Path, found[0].Ref(), found[1].Ref())

	case len(found) == 1:
		return found[0].SandboxNest, nil

	case localExists:
		return SandboxNest{Root: denHome, Source: "", Name: component}, nil

	case len(all) > 0:
		// A source prefix matched but nothing exists on either side. The local
		// fallback would send the caller into nest.LoadNest and surface
		// "reading <denHome>/nests/corp-api.yaml", naming a file that could
		// never have existed — the raw error this decode exists to replace. den
		// names both places it actually looked instead.
		return SandboxNest{}, fmt.Errorf(
			"sandbox %q: den cannot find the nest it came from — neither the local nest %s nor the "+
				"source nest %s exists (it would be addressed `%s`). `den nest ls` lists every nest "+
				"den can reach",
			component, local, all[0].Path, all[0].Ref())

	default:
		// Nothing about sources is involved: an ordinary local name. Returned
		// WITHOUT an error so the caller's own nest.LoadNest produces its
		// familiar message (and its NestNotFoundError type) unchanged — a den
		// that never installed a source must not learn a new dialect for a
		// nest file it deleted.
		return SandboxNest{Root: denHome, Source: "", Name: component}, nil
	}
}
