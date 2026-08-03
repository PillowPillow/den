// Package build orders the stack images (spec §6, "Build DAG").
//
// It lives outside internal/cli for the reason internal/spawn does: the graph,
// the cycle detection and the "is this image already there?" arbitration are
// the only parts worth testing, and none of them needs cobra. The one thing
// that cannot be tested — actually running a `build.sh` — is isolated behind
// the Script interface, exactly as internal/ports isolates the socket bind
// behind Scanner.
package build

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/PillowPillow/den/internal/config"
)

// Chain returns the stacks `den build [target]` must consider, ANCESTORS
// FIRST — the topological order of spec §6. It decides nothing about what is
// actually rebuilt; that is Plan's job.
//
// target empty ⇒ every healthy stack, which is the `den build` (no argument)
// form. A named target ⇒ that stack and its ancestors, and nothing else.
//
// THE ORDER IS DETERMINISTIC, and that is a requirement rather than a nicety:
// issue #8 asks for the order in a golden file, which is only possible if two
// runs on the same configuration produce the same list. Two things make it so.
// Roots are walked in NAME order (config.Stacks.Names sorts; a Go map does
// not), and within a chain each stack has exactly one `parent`, so there is no
// tie left to break below the roots. The "sort by name on a tie" the issue
// asks for is therefore entirely carried by the root ordering.
//
// A BROKEN stack does not sink the whole build, on the same doctrine as
// config.LoadStacks: `den build` walks the healthy ones, and a stack that
// fails to decode is only named if it is the target or an ancestor of it —
// through config.Stacks.Get, which distinguishes "unreadable" from "does not
// exist" and would otherwise send the user to create a file they already have.
func Chain(stacks config.Stacks, target string) ([]*config.Stack, error) {
	roots := stacks.Names()
	if target != "" {
		// Get, not a map lookup: it is the sole source of the "unreadable vs
		// absent" verdict, and it lists the declared stacks in its message.
		if _, err := stacks.Get(target); err != nil {
			return nil, err
		}
		roots = []string{target}
	}

	var out []*config.Stack
	built := map[string]bool{}
	for _, root := range roots {
		if err := visit(stacks, root, nil, built, &out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// visit walks one stack's ancestry, emitting parents before children.
//
// path is the chain currently being descended, and it is what makes the cycle
// message name the WHOLE cycle rather than just the stack where the walk
// noticed it: `a → b → a` has to be readable, not deducible.
func visit(stacks config.Stacks, name string, path []string, built map[string]bool, out *[]*config.Stack) error {
	if i := slices.Index(path, name); i >= 0 {
		cycle := append(slices.Clone(path[i:]), name)
		// The file named is the one that closes the cycle — the LAST stack of
		// the path, whose `parent:` points back. Naming the first would send
		// the user to a file that is not wrong on its own.
		closer, err := stacks.Get(path[len(path)-1])
		where := ""
		if err == nil {
			where = fmt.Sprintf(" — fix `parent:` in %s", stackFile(closer))
		}
		return fmt.Errorf("stack %q: parent cycle %s%s", name, strings.Join(cycle, " → "), where)
	}
	if built[name] {
		return nil
	}

	s, err := stacks.Get(name)
	if err != nil {
		return err
	}
	if s.Parent != "" {
		// The parent is resolved HERE rather than inside the recursive call so
		// the refusal names the stack that DECLARES the missing parent. The
		// error from Get alone says `stack "base" not found`, which is true and
		// unactionable: nothing in it points at the file to edit.
		if _, err := stacks.Get(s.Parent); err != nil {
			return fmt.Errorf("stack %q: %w — fix `parent:` in %s", name, err, stackFile(s))
		}
		if err := visit(stacks, s.Parent, append(slices.Clone(path), name), built, out); err != nil {
			return err
		}
	}

	built[name] = true
	*out = append(*out, s)
	return nil
}

// stackFile is the file a `parent:` refusal sends the user to. config.Stack
// carries its directory, not its file, because everything else in the repo
// composes the same join (internal/spawn does it for the kit refusal).
func stackFile(s *config.Stack) string { return filepath.Join(s.Dir, "stack.yaml") }
