package nest

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/PillowPillow/den/v2/internal/config"
	"github.com/PillowPillow/den/v2/internal/origin"
	"github.com/PillowPillow/den/v2/internal/repos"
)

// MachineRepo is a clone of this machine the nest does not necessarily use, for the instance's repo map.
type MachineRepo struct{ Name, Path string }

// MissingRepo is a key whose origin no folder of this machine answers for yet.
type MissingRepo struct {
	Key, Origin, DeclaredIn string
	Optional                bool
}

// MissingReposError lists every key of a nest without a folder, so one `git clone` pass fixes them all.
type MissingReposError struct {
	Nest    string
	Missing []MissingRepo
}

func (e *MissingReposError) Error() string {
	var b strings.Builder
	if len(e.Missing) == 1 {
		fmt.Fprintf(&b, "nest %q: 1 repo has no folder on this machine:\n", e.Nest)
	} else {
		fmt.Fprintf(&b, "nest %q: %d repos have no folder on this machine:\n", e.Nest, len(e.Missing))
	}
	for _, m := range e.Missing {
		fmt.Fprintf(&b, "  - %s: git clone %s   (declared in %s)\n", m.Key, m.Origin, m.DeclaredIn)
	}
	b.WriteString("already cloned? run `den scan <folder that holds your clones>`")
	return b.String()
}

// keyResolver resolves the `key:` entries of one nest against the nest's file, the global file and
// the table `den scan` writes. Keys without a folder pile up in missing, for a single refusal.
type keyResolver struct {
	nest         string
	file, global *config.Loaded
	table        repos.Table
	missing      []MissingRepo
	warnings     []string
}

// resolve answers ok=false for a key that is dropped (optional) or deferred to the missing list.
func (k *keyResolver) resolve(ref config.RepoRef) (Repo, bool, error) {
	key := ref.Key
	// A nest of the global file has one declaring file: comparing it with itself is no check at all.
	own := k.file == k.global || k.file.Path == k.global.Path
	g, inGlobal := k.global.File.Repos[key]
	var p config.RepoDecl
	inFile := false
	if !own {
		p, inFile = k.file.File.Repos[key]
	}
	if !inGlobal && !inFile {
		where := k.global.Path
		if !own {
			where = k.file.Path + " or of " + k.global.Path
		}
		if ref.Optional {
			k.warnings = append(k.warnings, fmt.Sprintf("optional repo key %q is not in repos: of %s; dropped", key, where))
			return Repo{}, false, nil
		}
		if own {
			return Repo{}, false, fmt.Errorf("nest %q: repo key %q is not in repos: of %s; add it there", k.nest, key, where)
		}
		return Repo{}, false, fmt.Errorf("nest %q: repo key %q is not in repos: of %s; add it to one of them", k.nest, key, where)
	}

	var gPath, pPath string
	var err error
	if inGlobal && g.Path != "" {
		if gPath, err = absPath(g.Path, k.global.Dir); err != nil {
			return Repo{}, false, fmt.Errorf("%s: repos.%s: %w", k.global.Path, key, err)
		}
	}
	if inFile && p.Path != "" {
		if pPath, err = absPath(p.Path, k.file.Dir); err != nil {
			return Repo{}, false, fmt.Errorf("%s: repos.%s: %w", k.file.Path, key, err)
		}
	}

	if inGlobal && inFile {
		conflict := func(mine, theirs string) error {
			return fmt.Errorf("repo key %q: %s says %s, %s says %s — one key names one repo; rename one",
				key, k.file.Path, mine, k.global.Path, theirs)
		}
		if p.Origin != "" && g.Origin != "" && identity(p.Origin) != identity(g.Origin) {
			return Repo{}, false, conflict(p.Origin, g.Origin)
		}
		if pPath != "" && gPath != "" && pPath != gPath {
			return Repo{}, false, conflict(pPath, gPath)
		}
		// Without this check a team project reusing a key name the user gave another repo would mount
		// that other repo without a word. A global entry that carries its own origin is the user's
		// word, and the remedy for an SSH alias whose remote never yields the project's identity.
		if p.Origin != "" && gPath != "" && g.Origin == "" && !repos.Valid(gPath, identity(p.Origin)) {
			return Repo{}, false, fmt.Errorf("repo key %q: %s gives origin %s, but %s (set in %s) has no remote with that identity — "+
				"fix one of the two, or add origin: to the key in %s", key, k.file.Path, p.Origin, gPath, k.global.Path, k.global.Path)
		}
	}

	repo := Repo{Name: key, Optional: ref.Optional, Key: key, Origin: p.Origin}
	if repo.Origin == "" {
		repo.Origin = g.Origin
	}
	switch {
	case gPath != "":
		repo.Path, repo.DeclaredIn = gPath, k.global.Path
	case pPath != "":
		repo.Path, repo.DeclaredIn = pPath, k.file.Path
	default:
		declaredIn := k.global.Path
		if inFile && p.Origin != "" {
			declaredIn = k.file.Path
		}
		e, ok := k.table.Repos[identity(repo.Origin)]
		if !ok || e.Path == "" {
			m := MissingRepo{Key: key, Origin: repo.Origin, DeclaredIn: declaredIn, Optional: ref.Optional}
			if ref.Optional {
				k.warnings = append(k.warnings, fmt.Sprintf("optional repo key %q has no folder on this machine "+
					"(git clone %s, declared in %s); dropped", key, m.Origin, m.DeclaredIn))
			} else {
				k.missing = append(k.missing, m)
			}
			return Repo{}, false, nil
		}
		repo.Path, repo.DeclaredIn = filepath.Clean(e.Path), declaredIn
	}
	return repo, true, nil
}

// identity is "" for an origin config.Load already refused; it then matches nothing.
func identity(url string) string {
	id, err := origin.Identity(url)
	if err != nil {
		return ""
	}
	return id
}

// machineRepos lists the global keys, then the clones `den scan` recorded that no key names. A fork
// carries two identities to one folder, so each path is listed once; a folder that no longer exists
// is left out rather than offered to the VM as a repo it could ask for.
func machineRepos(global *config.Loaded, table repos.Table) []MachineRepo {
	var out []MachineRepo
	listed := map[string]bool{}
	add := func(name, path string) {
		if listed[path] {
			return
		}
		if _, err := os.Stat(path); err != nil {
			return
		}
		listed[path] = true
		out = append(out, MachineRepo{Name: name, Path: path})
	}
	for _, key := range sortedNames(global.File.Repos) {
		if path, err := absPath(global.File.Repos[key].Path, global.Dir); err == nil {
			add(key, path)
		}
	}
	paths := make([]string, 0, len(table.Repos))
	for _, e := range table.Repos {
		if e.Path != "" {
			paths = append(paths, filepath.Clean(e.Path))
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		add(filepath.Base(p), p)
	}
	return out
}
