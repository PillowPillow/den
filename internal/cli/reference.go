package cli

import (
	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/source"
)

// `den sh`, `den rm` and `den ports` all take ONE argument naming a live
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
// evidence, and the explicit one is strictly better where it applies:
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
		root, _, bare, err := source.Locate(denHome, nestRef)
		if err != nil {
			return nil, err
		}
		return nest.LoadNest(root, bare)
	}
	component, _ := sbx.SplitName(sandboxName)
	sn, err := source.DecodeSandboxNest(denHome, component)
	if err != nil {
		return nil, err
	}
	return nest.LoadNest(sn.Root, sn.Name)
}
