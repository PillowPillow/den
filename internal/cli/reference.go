package cli

import (
	"os"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/source"
)

// `den exec`, `den rm` and `den ports` all take ONE argument naming a live
// sandbox, and all three have to reconcile two spellings of it: the reference
// the user types (`corp:api`, possibly worktree'd) and the name the VM
// actually carries (`corp-api.feat12`), because ":" is not in sbx's `--name`
// charset. The two functions below are that reconciliation, in both
// directions, written ONCE — three copies of the forward half already existed
// and had drifted into three different treatments of the worktree suffix.

// sandboxNameOf turns the argument into the name of the live sandbox it
// designates.
//
// THE WORKTREE SUFFIX IS SPLIT OFF FIRST, and that is the whole fix: only the
// NEST component can carry a source prefix, while flattening rewrites every
// character outside the sandbox charset — including the "." that separates a
// worktree from its nest (config.FlattenSandboxComponent excludes "." on
// purpose). Flattening the whole argument therefore turned "corp:api.feat12"
// into "corp-api-feat12", a name spawn never creates, so a worktree'd source
// sandbox had no reachable prefixed spelling at all.
//
// A BARE argument is returned untouched, deliberately — not re-validated, not
// normalized. `sbx ls` reports sandboxes den did not create, and this function
// only has to say which live name to look for; the guards that matter (a name
// escaping the worktree root, say) live where a name becomes a path.
func sandboxNameOf(ref string) (string, error) {
	nestRef, wt := sbx.SplitName(ref)
	if src, _ := config.SplitSourceRef(nestRef); src == "" {
		return ref, nil
	}
	component, err := config.FlattenSandboxComponent("nest", nestRef)
	if err != nil {
		return "", err
	}
	if wt == "" {
		return component, nil
	}
	// Rejoined by hand rather than through sbx.SandboxName: that constructor
	// VALIDATES the worktree component, and the bare branch above validates
	// nothing. Routing only the source branch through it would make
	// `den rm corp:api.feature/123` refuse where `den rm api.feature/123`
	// merely reports the sandbox as absent — one situation, two dialects.
	return component + sbx.NameSeparator + wt, nil
}

// nestOfSandbox loads the nest a LIVE sandbox came from, accepting BOTH
// spellings of the argument — `corp:api` and the `corp-api` that `den ls`
// prints, which is the string a user actually has in front of them and the
// one den's own --detach message recommends.
//
// ref is what the user typed, sandboxName the live VM's name. The two
// branches are not a duplication of each other, they answer from different
// evidence. The explicit one is better on the DIAGNOSIS of an uninstalled
// source and equal on ARBITRATION — both refuse a name two nests explain,
// through the same source.LocalCollisionError:
//
//   - A PREFIXED ref needs no decoding at all — the user named the source, so
//     source.Locate resolves it directly and, when the source is not
//     installed, refuses by naming `den source add`. The reverse decode could
//     never produce that remedy: an uninstalled source leaves nothing under
//     sources/ to decode from, so a bare "corp-api" is indistinguishable from
//     an ordinary local nest name.
//   - A BARE ref is the flattened form and must be decoded, because the nest
//     component of a source-originated sandbox names no file under
//     <denHome>/nests (source.DecodeSandboxNest holds why that decode is
//     unambiguous, and when it refuses instead).
//
// Either way the worktree suffix is split off first, for sandboxNameOf's
// reason in reverse: it is not part of what a source prefixes.
func nestOfSandbox(denHome, ref, sandboxName string) (*nest.Nest, error) {
	if src, _ := config.SplitSourceRef(ref); src != "" {
		nestRef, _ := sbx.SplitName(ref)
		// LOCATE FIRST, THE COLLISION CHECK AFTER, and that order is exactly
		// what keeps this branch's advantage: a den home holding a local
		// nests/corp-api.yaml and no corp source at all must still be told
		// which source is missing and that `den source add` installs it —
		// checking the collision first would answer it with a refusal naming
		// a "source nest" no clone contains.
		root, _, bare, err := source.Locate(denHome, nestRef)
		if err != nil {
			return nil, err
		}
		// LOADED BEFORE THE COLLISION IS DECLARED: a refusal that names "the
		// source nest <path>" must not name a file that is not there. An
		// installed source missing this nest is no ambiguity at all, and this
		// branch has always reported it as the ordinary nest-not-found it is.
		n, err := nest.LoadNest(root, bare)
		if err != nil {
			return nil, err
		}
		// A TYPED PREFIX SAYS WHICH NEST THE USER MEANS, NOT WHICH NEST THE
		// SANDBOX CAME FROM, and only the second question is being asked here.
		// `corp:api` and a local nests/corp-api.yaml both flatten onto the one
		// live "corp-api", and spawn only ever checked that collision for a
		// SOURCE reference — so `den corp-api` created this state unrefused and
		// the VM may have come from either side. Reading the source nest's
		// `ports:`/`repos:` into a sandbox the local nest spawned is precisely
		// what the bare branch refuses (source.LocalCollisionError says why),
		// and arbitrating on one spelling only would have left the other as a
		// way to walk around that refusal by retyping.
		//
		// The component is the LIVE name's, not the reference's: it is the
		// string both nests must produce for them to collide at all, and for a
		// worktree'd sandbox the suffix is no part of it.
		component, _ := sbx.SplitName(sandboxName)
		local := nest.FilePath(denHome, component)
		if _, statErr := os.Stat(local); statErr == nil {
			// nestRef, not the raw argument: the source-side spelling the
			// message prints is the NEST's, and ref may carry a worktree.
			return nil, source.LocalCollisionError(component, local, nest.FilePath(root, bare), nestRef)
		}
		return n, nil
	}
	component, _ := sbx.SplitName(sandboxName)
	sn, err := source.DecodeSandboxNest(denHome, component)
	if err != nil {
		return nil, err
	}
	return nest.LoadNest(sn.Root, sn.Name)
}
