// Package build orders the stack images and runs them (spec §6, "Build DAG").
//
// It lives outside internal/cli for the reason internal/spawn does: the graph,
// the cycle detection, the "is this image already there?" arbitration and the
// build sequence itself are all worth testing on their own, and none of them
// needs cobra. Nothing here spawns a process: den runs each stack's
// provisioning through sbx.Runner (Execute), an interface injected exactly as
// internal/ports isolates the socket bind behind Scanner — so the whole argv
// sequence is asserted against a recorder, never against a real VM.
package build

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/PillowPillow/den/internal/config"
)

// Excluded is a stack `den build` (no target) leaves out because its `parent:`
// chain reaches a stack den cannot resolve, and why.
//
// Returned rather than swallowed, for the reason Step.Skipped exists: a silent
// skip and a forgotten stack look identical from the outside. And Reason is
// composed HERE, where the verdict is known, exactly as Step.Skipped is: the
// two causes do not send the user to the same file — an unreadable ancestor is
// fixed in its own stack.yaml, a `parent:` naming nothing is fixed on the line
// that names it. The CLI prints the sentence; it does not decide it.
type Excluded struct {
	Stack  string // the stack that is not built
	Reason string // why, remedy included
}

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
// fails to decode is only a REFUSAL when it is the target or an ancestor of
// the target — through config.Stacks.Get, which distinguishes "unreadable"
// from "does not exist" and would otherwise send the user to create a file
// they already have.
//
// Without a target the same broken stack used as a `parent:` is not a refusal
// either: it would sink every healthy stack around it, which is precisely what
// the doctrine above forbids. The stacks whose ancestry reaches it are EXCLUDED
// from the chain and returned as such — measured on this branch, 2026-08-03: a
// den holding one healthy stack, one unreadable stack and one child of the
// unreadable one answered `den build` with a refusal and built nothing.
//
// A `parent:` naming a stack that does not exist AT ALL is the same defect
// class and takes the same answer, only with the other remedy: there the fault
// really is the `parent:` line, so the exclusion names the file that declares
// it. Only the REFUSABLE cases stay refusals — a named target, and a parent
// cycle, which is a contradiction no other stack can be walked around.
//
// Every healthy stack is a root of the no-target walk, so a whole affected
// subtree comes back stack by stack rather than as the top of the subtree
// alone: the user gets the list of what was not built, not a list to deduce it
// from.
func Chain(stacks config.Stacks, target string) ([]*config.Stack, []Excluded, error) {
	roots := stacks.Names()
	if target != "" {
		// Get, not a map lookup: it is the sole source of the "unreadable vs
		// absent" verdict, and it lists the declared stacks in its message.
		if _, err := stacks.Get(target); err != nil {
			return nil, nil, err
		}
		roots = []string{target}
	}

	var out []*config.Stack
	var excluded []Excluded
	built := map[string]bool{}
	for _, root := range roots {
		err := visit(stacks, root, nil, built, &out)
		var ancestry *ancestryError
		if target == "" && errors.As(err, &ancestry) {
			// Nothing to roll back: visit appends a stack only AFTER its whole
			// ancestry has been walked, so a chain that dies on an unresolvable
			// ancestor has emitted none of itself.
			excluded = append(excluded, Excluded{Stack: root, Reason: ancestry.reason})
			continue
		}
		if err != nil {
			return nil, nil, err
		}
	}
	return out, excluded, nil
}

// ancestryError is the walk's verdict on a `parent:` den cannot resolve. A TYPE
// rather than a plain error because Chain answers it two ways, and it carries
// BOTH wordings for that reason: err is the refusal a named target gets, reason
// is the same fault as the one-line exclusion the no-target form reports. They
// are written together, here, so the remedy cannot differ between the two forms.
type ancestryError struct {
	err    error  // the refusal, verbatim
	reason string // why the stack is not built, remedy included
}

func (e *ancestryError) Error() string { return e.err.Error() }

func (e *ancestryError) Unwrap() error { return e.err }

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
		parent, err := stacks.Get(s.Parent)
		if err != nil {
			// ...but "fix `parent:` in <this stack>" is the remedy for ONE of
			// Get's two verdicts. A parent that does not load is a parent that
			// is really there: the `parent:` line naming it is correct, and the
			// file to fix is the parent's own — which Get's verdict already
			// cites. Sending the user to edit the child's `parent:` there would
			// point at the one line that is not wrong.
			var unreadable *config.UnreadableStackError
			if errors.As(err, &unreadable) {
				return &ancestryError{
					// The refusal names the affected stack and then hands over to
					// Get's verdict, which names the unreadable stack AND its
					// file. NOTHING is appended after it: that verdict wraps a
					// multi-line YAML diagnostic, and a remedy trailing it would
					// read as belonging to its last line — the reason
					// config.Stacks.Get gives for appending no location either.
					err:    fmt.Errorf("stack %q: %w", name, err),
					reason: fmt.Sprintf("its ancestor %q is unreadable, fix that stack first", s.Parent),
				}
			}
			return &ancestryError{
				err: fmt.Errorf("stack %q: %w — fix `parent:` in %s", name, err, stackFile(s)),
				// The exclusion line is not Get's message: that one lists the
				// declared stacks over several lines, which belongs in a refusal
				// the user asked for, not in a one-line report about a stack
				// they did not name. What survives is what to edit.
				reason: fmt.Sprintf("its ancestor %q does not exist — fix `parent:` in %s", s.Parent, stackFile(s)),
			}
		}
		// The parent's image is carried on the child so Execute does not have to
		// re-resolve the graph it was handed a linear chain of.
		s.ParentImage = parent.Image
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
