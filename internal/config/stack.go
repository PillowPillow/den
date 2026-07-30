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

	Dir string `yaml:"-"` // stack directory, filled in at load time
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
	for i, k := range s.Kits {
		if k != "" && !filepath.IsAbs(k) {
			s.Kits[i] = filepath.Join(dir, k)
		}
	}
	return &s, nil
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
			// No location appended: the wrapped error already cites the full
			// path of the broken stack.yaml, and it's multi-line.
			return nil, fmt.Errorf("stack %q: unreadable: %w", name, c.Err)
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
