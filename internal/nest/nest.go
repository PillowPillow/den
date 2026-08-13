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

// Repo is a repository co-mounted in the sandbox. Exactly one of Path and Key
// is set (LoadNest refuses both and neither-with-url): Path is a machine
// path, Key is an indirection through the personal `repos:` mapping of
// config.yaml — the one thing that lets a TEAM nest travel between machines
// (spec 2026-08-04 §2.4). A Key entry's Path stays EMPTY until Resolve fills
// it, which is why every path-keyed check in this package has to tolerate one.
//
// AdHoc records where the entry came from, and it is not cosmetic: it decides
// which place a "repo not found" tells the user to correct — a `repos:` line in
// a yaml file, or the command they just typed. Sending someone to edit a nest
// file over a path they typed by hand is the kind of wrong remedy den exists to
// avoid (spec §2).
//
// `yaml:"-"` states the intent rather than leaving it to a side effect of the
// decoder: strict decoding (KnownFields(true)) would already reject a hand
// written `adhoc:`, but that is a property of the loader, not of this type.
type Repo struct {
	Path string `yaml:"path"`
	Key  string `yaml:"key"`
	// URL is INDICATIVE only: it enriches the unmapped-key refusal with the
	// clone command the user probably wants. den never clones a work repo.
	URL      string `yaml:"url"`
	Optional bool   `yaml:"optional"`
	AdHoc    bool   `yaml:"-"`
}

// Name is the repo's selection identity — what --without/--only address it
// by, and the name den prints for it. The KEY when set — it is the shareable
// identity — else the path basename.
//
// It is NOT the worktree directory component: worktree.Path names that
// directory after the repository PATH's basename (filepath.Base(repoPath)),
// which for a key-typed repo diverges from Name(). checkUniqueMountBasenames
// (repos.go) is the pre-flight that keeps two repos from landing on the same
// worktree directory when Name() and that basename diverge.
func (r Repo) Name() string {
	if r.Key != "" {
		return r.Key
	}
	return filepath.Base(r.Path)
}

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

// The values of a nest's `select:` — how it decides WHICH of its optional
// repos a spawn mounts.
//
// A string rather than a `generic: true` boolean: "all" is a value one can
// write to state the intent, and a third mode later would not have to break
// the type. The zero value "" means SelectAll, so no existing nest changes
// behaviour by being re-read.
const (
	// SelectAll mounts every optional repo unless --without/--only/-i says
	// otherwise. The historical behaviour, and the default.
	SelectAll = "all"
	// SelectPrompt declares a nest with NO default selection: the repos are
	// chosen at spawn time. It exists for the generic nest of a microservice
	// team — thirty repos of which a session wants four, in a combination
	// that changes weekly — where a nest file per feature is not tenable.
	SelectPrompt = "prompt"
)

// Nest is a spawnable object (spec §4.3).
type Nest struct {
	// Name comes from the file's basename, NEVER from its content: an object
	// has a single, non-forgeable identity (spec §2).
	Name  string `yaml:"-"`
	Stack string `yaml:"stack"`
	// Select is `select:`; see SelectAll / SelectPrompt. LoadNest refuses any
	// other value.
	Select string            `yaml:"select"`
	Env    map[string]string `yaml:"env"`
	Egress []string          `yaml:"egress"`
	Repos  []Repo            `yaml:"repos"`
	Ports  Ports             `yaml:"ports"`
	Agents map[string]string `yaml:"agents"` // per-agent config_dir override
}

// PromptsForRepos reports whether this nest chooses its repos at spawn time.
//
// A method rather than `n.Select == SelectPrompt` at each call site: three
// packages ask the question (spawn's entry point, the checklist's starting
// state, the no-terminal refusal), and the zero value has to mean SelectAll in
// all three — a comparison written by hand would eventually be written against
// SelectAll instead, where "" would answer false.
func (n *Nest) PromptsForRepos() bool { return n.Select == SelectPrompt }

// CheckWithout refuses `--without` on a nest that selects its repos at spawn
// time, and is the SOLE author of that sentence.
//
// `--without` subtracts from a default selection, and a `select: prompt` nest
// declares it has none: that is the whole meaning of the mode, and it is why its
// checklist starts EMPTY (spawn's promptOptionalRepos). Accepted, the flag
// silenced the checklist and resolved the MAXIMAL set minus the named repos —
// den handing out, in silence, the thirty-repo default the nest exists not to
// have. Refusing is the reading a user cannot misinterpret, and the repo refuses
// rather than normalizing in silence (spec §2). `--only` stays accepted, and is
// what the message names: it states the set outright, which is exactly the
// question the checklist asks.
//
// It lives in THIS package, not in internal/spawn where it was first written,
// because two commands have to give one verdict: `den spawn` (step 0bis) and
// `den nest show`, which internal/cli documents as its dry-run and which reaches
// nest.Resolve without ever going through Spawn. One flag answered twice in two
// dialects is the divergence this function exists to make impossible — the same
// argument spawn.ResolveStack already carries for the stack reference. Nothing
// here is learnt about sandboxes: the mode is Nest.Select, the file is
// FilePath, both this package's own.
//
// nestRoot is where the nest was LOADED from — a source's directory for
// `corp:api`, the den home otherwise — so the message names the file the user
// must edit and not a local namesake of it.
func (n *Nest) CheckWithout(nestRoot string, without []string) error {
	if len(without) == 0 || !n.PromptsForRepos() {
		return nil
	}
	return fmt.Errorf(
		"nest %s selects its repos at spawn time (`select: prompt` in %s), so there is no "+
			"default selection for `--without` to subtract from — name the repos you want "+
			"with `--only repo,...`, this nest's non-interactive spelling",
		n.Name, FilePath(nestRoot, n.Name))
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
	// Validated here rather than at spawn: `den nest ls` and `den lint` must
	// see the same refusal, and the earliest reader is the one that gives the
	// user the shortest path to the faulty line.
	switch n.Select {
	case "", SelectAll, SelectPrompt:
	default:
		// %q on the offending value, not %s: a `select: "prompt "` with a
		// trailing space would otherwise render as a message naming the right
		// word and refusing it anyway.
		return nil, fmt.Errorf(
			"nest %q: `select:` is %q, which is not a known mode — use %q (mount every optional "+
				"repo, the default) or %q (choose the repos at spawn time); fix `select:` in %s",
			name, n.Select, SelectAll, SelectPrompt, path)
	}
	for i, r := range n.Repos {
		switch {
		case r.Path != "" && r.Key != "":
			return nil, fmt.Errorf(
				"nest %q: repo entry %d sets both `path:` and `key:` — a repo has ONE identity: "+
					"`path:` is a machine path, `key:` resolves through `repos:` in config.yaml. "+
					"Two identities is a contradiction, not a precedence den can arbitrate", name, i)
		case r.Path == "" && r.Key == "":
			return nil, fmt.Errorf(
				"nest %q: repo entry %d sets neither `path:` nor `key:` — den has nothing to mount", name, i)
		case r.URL != "" && r.Key == "":
			return nil, fmt.Errorf(
				"nest %q: repo entry %d sets `url:` without `key:` — url exists only to enrich the "+
					"unmapped-key message; on a `path:` entry it would never be read", name, i)
		}
		if r.Path != "" {
			if n.Repos[i].Path, err = config.ExpandPath(r.Path); err != nil {
				return nil, fmt.Errorf("nest %q, repo %q: %w", n.Name, r.Path, err)
			}
		}
	}
	// After expansion: two differently-written paths can converge.
	if err := checkUniqueNames(n.Repos, "nest"); err != nil {
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
