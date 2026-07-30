// Package nest loads nests (spawnable objects) and computes the pure
// derivations that follow: repo selection, egress union, agent resolution.
package nest

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/PillowPillow/den/internal/config"
)

// Repo is a repository co-mounted in the sandbox.
type Repo struct {
	Path     string `yaml:"path"`
	Optional bool   `yaml:"optional"`
}

// Name is the repo's short name (basename), used by --without/--only.
func (r Repo) Name() string { return filepath.Base(r.Path) }

// PortDecl is a port declared by the nest (spec §8).
type PortDecl struct {
	Name         string `yaml:"name"`
	Container    int    `yaml:"container"`
	Open         bool   `yaml:"open"`
	LoopbackLock bool   `yaml:"loopback_lock"`
}

// Ports carries the declared port window. Base is declarative only: den does
// not derive it from anything, it is read as-is (`den nest ls` display).
type Ports struct {
	Base    int        `yaml:"base"`
	Publish []PortDecl `yaml:"publish"`
}

// Nest is a spawnable object (spec §4.3).
type Nest struct {
	// Name comes from the file's basename, NEVER from its content: an object
	// has a single, non-forgeable identity (spec §2).
	Name   string            `yaml:"-"`
	Stack  string            `yaml:"stack"`
	Env    map[string]string `yaml:"env"`
	Egress []string          `yaml:"egress"`
	Repos  []Repo            `yaml:"repos"`
	Ports  Ports             `yaml:"ports"`
	Agents map[string]string `yaml:"agents"` // per-agent config_dir override
}

// NestNotFoundError reports the one LoadNest failure that means "this object
// does not exist": the nest file is ABSENT. Exported as a type, not a plain
// message, because the CLI must distinguish this case from others to decide
// whether to suggest a close subcommand (`den doctr` => `den doctor`).
//
// The discrimination is on fs.ErrNotExist alone: a file that is present but
// unreadable (permissions, EISDIR) is a nest that EXISTS, and suggesting a
// command in its place would send the user down the wrong path when they
// actually have a permissions problem.
type NestNotFoundError struct {
	Name string
	Path string
	Err  error
}

func (e *NestNotFoundError) Error() string {
	return fmt.Sprintf("nest %q: reading %s: %v", e.Name, e.Path, &config.FileError{Err: e.Err})
}

func (e *NestNotFoundError) Unwrap() error { return e.Err }

// FilePath is the SOLE definition of where a nest's file lives:
// <denHome>/nests/<name>.yaml. LoadNest is the only reader; any other
// caller that needs to NAME the file (to tell the user what to fix) must
// go through this too, or the two could silently diverge if the layout
// ever moved.
func FilePath(denHome, name string) string {
	return filepath.Join(denHome, "nests", name+".yaml")
}

// LoadNest reads <denHome>/nests/<name>.yaml.
func LoadNest(denHome, name string) (*Nest, error) {
	// ValidateName BEFORE ValidateSandboxComponent, in this exact order: the
	// two overlap (the sandbox charset already rejects "/", "." and ".."), but
	// ValidateName names the INTENT ("this is an identifier in ~/.den, not a
	// path") for ../../etc/passwd. Swapping the order would surface "the
	// character '/' is forbidden" instead — true, but missing the real issue:
	// an attempt to escape the den home.
	if err := config.ValidateName("nest", name); err != nil {
		return nil, err
	}
	// A nest's name becomes a sandbox name (sbx has no --label): reject it
	// here rather than at spawn time so the problem surfaces as early as
	// `den nest ls`.
	if err := config.ValidateSandboxComponent("nest", name); err != nil {
		return nil, err
	}
	path := FilePath(denHome, name)

	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, &NestNotFoundError{Name: name, Path: path, Err: err}
		}
		return nil, fmt.Errorf("nest %q: reading %s: %w", name, path, &config.FileError{Err: err})
	}

	var n Nest
	if err := config.DecodeYAMLStrict(path, raw, &n); err != nil {
		return nil, err
	}

	n.Name = name // the filename is authoritative, unconditionally
	for i, r := range n.Repos {
		if n.Repos[i].Path, err = config.ExpandPath(r.Path); err != nil {
			return nil, fmt.Errorf("nest %q, repo %q: %w", n.Name, r.Path, err)
		}
	}
	// After expansion: two differently-written paths can converge.
	if err := checkUniqueNames(n.Repos); err != nil {
		return nil, fmt.Errorf("nest %q: %w", n.Name, err)
	}
	for agent, dir := range n.Agents {
		expanded, err := config.ExpandPath(dir)
		if err != nil {
			return nil, fmt.Errorf("nest %q, agent %q: %w", n.Name, agent, err)
		}
		n.Agents[agent] = expanded
	}
	return &n, nil
}

// BrokenNest is a nest present on disk but not loadable.
type BrokenNest struct {
	Name string
	Err  error
}

// ListNests loads all declared nests, sorted by name.
//
// An unreadable nest does NOT hide the others: it is reported separately.
// Strict decoding makes a typo like `egres:` fatal to loading, and letting
// that hide the entire list would leave the user with no way to see which
// file is at fault — which is exactly what `den nest ls` and `den doctor`
// exist to tell them.
//
// The returned error is reserved for STRUCTURAL failures (an unreadable
// nests/ directory): in that case there is nothing to list at all.
func ListNests(denHome string) ([]*Nest, []BrokenNest, error) {
	root := filepath.Join(denHome, "nests")
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("reading %s: %w", root, &config.FileError{Err: err})
	}

	// candidate carries both the truncated name (the identity, used for
	// sorting and for LoadNest) and the full filename (a display fallback for
	// a ".yaml" file whose truncated name is empty, see below).
	type candidate struct{ name, file string }
	var candidates []candidate
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		candidates = append(candidates, candidate{
			name: strings.TrimSuffix(e.Name(), ".yaml"),
			file: e.Name(),
		})
	}
	// Sort by truncated name, NOT by filename: they diverge as soon as two
	// names share a prefix ('-' precedes '.' in ASCII), see
	// TestListNestsSortDivergesFromFileOrder.
	slices.SortFunc(candidates, func(a, b candidate) int { return strings.Compare(a.name, b.name) })

	nests := make([]*Nest, 0, len(candidates))
	var broken []BrokenNest
	for _, c := range candidates {
		n, err := LoadNest(denHome, c.name)
		if err != nil {
			displayName := c.name
			if displayName == "" {
				// A file literally named ".yaml": the truncated name is
				// empty. Fall back to the full filename so the warning
				// names something — otherwise the user has no way to know
				// which file to remove.
				displayName = c.file
			}
			broken = append(broken, BrokenNest{Name: displayName, Err: err})
			continue
		}
		nests = append(nests, n)
	}
	return nests, broken, nil
}
