package config

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
)

// Provision is what den plays INSIDE the build VM (spec §6). den owns the
// sequence around it; a stack only says what to run.
type Provision struct {
	// Includes is concatenated ahead of EVERY step, and is never a step of its
	// own. Order is significant. Relative to the stack directory in YAML,
	// absolute after loading.
	//
	// The contract, which den cannot verify and the spec therefore states: it
	// DEFINES, it does not act. Each `sbx exec` opens a fresh shell, so this
	// text is re-emitted once per step — a side effect here runs N times, in
	// silence.
	Includes []string `yaml:"includes"`
	// Steps is one `sbx exec` per entry, in order. Relative to the stack
	// directory in YAML, absolute after loading.
	//
	// One exec per entry and not one big payload: it is what lets a failure
	// name the step that produced it. The price is that nothing but the VM
	// filesystem survives from one step to the next — hence Includes.
	Steps []string `yaml:"steps"`
}

// Stack is a buildable image recipe (spec §4.2).
type Stack struct {
	// Name comes from the directory name, NEVER from the content: an object
	// has a single, unforgeable identity (spec §2).
	Name   string `yaml:"-"`
	Image  string `yaml:"image"`
	Parent string `yaml:"parent"` // build DAG edge
	Kit    string `yaml:"kit"`    // relative to the stack directory in YAML, absolute after loading
	// Kits: cross-cutting kits layered BEFORE Kit (e.g. ssh-known-hosts).
	// Relative to the stack directory in YAML, absolute after loading.
	// ORDER IS SIGNIFICANT: it's an sbx layering order, not a set — never sort it.
	Kits   []string `yaml:"kits"`
	Egress []string `yaml:"egress"`

	// Base is the sbx AGENT positional, and applies to ROOT stacks only — the
	// ones with no `parent:`, which therefore get no `--template`. There the
	// positional is load-bearing: it selects the starting image
	// (`claude` → docker/sandbox-templates:claude-code-docker). With a
	// `--template`, it is the image's flavor that decides and den passes
	// `sbx.PositionalAgent` instead.
	Base      string    `yaml:"base"`
	Provision Provision `yaml:"provision"`

	Dir string `yaml:"-"` // stack directory, filled in at load time

	// ParentImage is Parent's own `image:`, filled in by build.Chain's walk —
	// never decoded from YAML, hence `yaml:"-"`. A build derives from the
	// parent's IMAGE, not its name (`sbx create --template` takes a
	// reference), and Chain already holds the parent *Stack at the moment it
	// resolves it; carrying the image here spares Execute a second walk of the
	// graph over a chain it was handed as a flat, already-ordered slice.
	ParentImage string `yaml:"-"`
}

// DeclaredKits returns the kits this stack declares, in sbx's LAYERING ORDER:
// `kits:` (cross-cutting) first, then `kit:`. Empty entries are filtered out —
// a blank line is not a missing kit.
//
// SOLE SOURCE of "which kits this stack declares, in what order". The order
// is a SAFETY property, not display convenience: sbx layers kits in `--kit`
// order, and the mixin — appended AFTER this list by sbx.CreateArgv — must
// stay last so its freshness command is the last startup step run (spec §9.1).
func (s *Stack) DeclaredKits() []string {
	kits := make([]string, 0, len(s.Kits)+1)
	for _, k := range s.Kits {
		if k != "" {
			kits = append(kits, k)
		}
	}
	if s.Kit != "" {
		kits = append(kits, s.Kit)
	}
	return kits
}

// Buildable reports whether den knows how to build this stack's image.
//
// SOLE SOURCE of that verdict, and it is read on BOTH sides: `den build` skips
// a stack that is not buildable, and the spawn's image check stays silent on
// it (internal/spawn, checkStackImage). Spec §6 requires the two to agree —
// measured on 2026-08-03, they had already drifted once, and a `den build` on
// a den holding a single pullable stack built NOTHING while demanding a script
// for the stack den had already decided not to build.
func (s *Stack) Buildable() bool { return len(s.Provision.Steps) > 0 }

// LoadStack reads <denHome>/stacks/<name>/stack.yaml.
func LoadStack(denHome, name string) (*Stack, error) {
	if err := ValidateName("stack", name); err != nil {
		return nil, err
	}
	dir := filepath.Join(denHome, "stacks", name)
	path := filepath.Join(dir, "stack.yaml")

	raw, err := os.ReadFile(path)
	if err != nil {
		// "declare it" and "fix the permissions" are two different fixes:
		// doctor relays this message verbatim, it must decide which.
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("stack %q: not found — expected %s", name, path)
		}
		return nil, fmt.Errorf("stack %q: could not read %s: %w", name, path, &FileError{Err: err})
	}

	var s Stack
	if err := DecodeYAMLStrict(path, raw, &s); err != nil {
		return nil, err
	}

	s.Name = name // the directory is authoritative, unconditionally
	s.Dir = dir
	if s.Kit != "" && !filepath.IsAbs(s.Kit) {
		s.Kit = filepath.Join(dir, s.Kit)
	}
	resolveAgainst(dir, s.Kits)
	resolveAgainst(dir, s.Provision.Includes)
	resolveAgainst(dir, s.Provision.Steps)

	// Checked only for a stack den would actually BUILD. A pullable stack
	// declares no origin by design, and demanding one of it would refuse the
	// configuration spec §6 calls legitimate.
	if s.Buildable() {
		switch {
		case s.Base != "" && s.Parent != "":
			return nil, fmt.Errorf(
				"stack %q: `base:` and `parent:` are both set in %s — a stack has ONE origin: "+
					"`parent:` builds FROM another stack's image, `base:` starts from an sbx agent. "+
					"Two origins for one image is a contradiction, not a precedence den can arbitrate",
				name, path)
		case s.Base == "" && s.Parent == "":
			return nil, fmt.Errorf(
				"stack %q: `provision.steps` is declared but neither `base:` nor `parent:` is, in %s — "+
					"den does not know what to build FROM. Add `base: claude` for a root stack, "+
					"or `parent: <stack>` to derive from another",
				name, path)
		}
	}
	return &s, nil
}

// resolveAgainst rewrites the relative entries of a path list against dir, IN
// PLACE. Sole definition of the rule stated once in §4.2 and applying to
// `kits`, `provision.includes` and `provision.steps` alike: a blank entry is
// left alone rather than turned into the stack directory itself.
func resolveAgainst(dir string, paths []string) {
	for i, p := range paths {
		if p != "" && !filepath.IsAbs(p) {
			paths[i] = filepath.Join(dir, p)
		}
	}
}

// BrokenStack is a stack present on disk but not loadable.
type BrokenStack struct {
	Name string
	Err  error
}

// Stacks carries the result of loading <denHome>/stacks: the loadable stacks,
// and SEPARATELY the ones that aren't.
//
// A broken stack does NOT hide the others — same doctrine as ListNests
// (internal/nest/nest.go): a typo in a stack that NOBODY uses must not fail
// the whole load. Keeping the two lists separate means the only object named
// is the one that's actually broken.
type Stacks struct {
	Healthy map[string]*Stack
	Broken  []BrokenStack
	// Root is <denHome>/stacks, the directory these stacks were loaded from.
	// Used only in the "not found" message, to tell the user where to create
	// the missing stack.
	//
	// Empty on a hand-built Stacks (tests): the message then omits the
	// location rather than naming a directory that doesn't exist.
	Root string
}

// UnreadableStackError is Get's "declared but does not load" verdict, given a
// TYPE so a caller can act on the distinction instead of matching the text.
//
// `den build` is what needs it: a `parent:` naming an unreadable stack and a
// `parent:` naming a stack that does not exist call for two different files to
// be edited — the parent's own for the first, the child's `parent:` line for
// the second. Get stays the SOLE SOURCE of the verdict; this only makes the
// verdict inspectable with errors.As.
type UnreadableStackError struct {
	Name string // the stack that is present on disk but does not load
	Err  error  // LoadStack's own failure; it cites the full path of the file
}

// Error appends NO location: Err already cites the full path of the broken
// stack.yaml, and it's multi-line — anything trailing it reads as belonging to
// the last line of a YAML diagnostic.
func (e *UnreadableStackError) Error() string {
	return fmt.Sprintf("stack %q: unreadable: %v", e.Name, e.Err)
}

func (e *UnreadableStackError) Unwrap() error { return e.Err }

// Get returns the named stack, or an error that DISTINGUISHES the two ways of
// not having it: declared but unreadable, or not existing at all.
//
// SOLE SOURCE of this verdict. "not found" on a stack that exists but fails
// to load would send the user to create a file they already have, instead of
// fixing the one they have.
func (s Stacks) Get(name string) (*Stack, error) {
	if st, ok := s.Healthy[name]; ok {
		return st, nil
	}
	for _, c := range s.Broken {
		if c.Name == name {
			return nil, &UnreadableStackError{Name: name, Err: c.Err}
		}
	}
	if s.Root == "" {
		return nil, fmt.Errorf("stack %q not found (declared stacks: %v)", name, s.Names())
	}
	return nil, fmt.Errorf(
		"stack %q not found in %s (declared stacks: %v)", name, s.Root, s.Names())
}

// Names returns the names of the HEALTHY stacks, sorted. Meant for display: a
// Go map isn't ordered, and an error message must be reproducible.
func (s Stacks) Names() []string {
	return slices.Sorted(maps.Keys(s.Healthy))
}

// LoadStacks loads all declared stacks. A directory without a stack.yaml is
// ignored (a draft), a missing stacks/ directory gives an empty result: a
// freshly created den is not an error.
//
// The returned error is reserved for STRUCTURAL failures (an unreadable
// stacks/ directory): there, there is nothing to load at all. A stack that
// fails to decode goes into Broken instead, without interrupting anything —
// see the Stacks godoc.
func LoadStacks(denHome string) (Stacks, error) {
	root := filepath.Join(denHome, "stacks")
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Stacks{Healthy: map[string]*Stack{}, Root: root}, nil
		}
		return Stacks{}, fmt.Errorf("reading %s: %w", root, &FileError{Err: err})
	}

	out := Stacks{Healthy: make(map[string]*Stack), Root: root}
	// os.ReadDir already sorts by name: Broken is therefore deterministic
	// without an extra sort, and `den doctor` renders its diagnostics in the
	// same order across runs.
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, e.Name(), "stack.yaml")); err != nil {
			continue
		}
		s, err := LoadStack(denHome, e.Name())
		if err != nil {
			out.Broken = append(out.Broken, BrokenStack{Name: e.Name(), Err: err})
			continue
		}
		out.Healthy[e.Name()] = s // the directory is the identity, same as in LoadStack
	}
	return out, nil
}
