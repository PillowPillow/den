package nest

import (
	"fmt"
	"slices"
	"strings"
)

// checkUniqueNames rejects two repos sharing a basename within the same nest.
// The basename IS a repo's identity: --without/--only address it by this
// name, and it becomes a path (worktree_root/<wt>/<repo>) and the position of
// a `sbx create` argument. Two homonyms would make all three
// ambiguous — and `--without api` would silently drop two of them. This is a
// configuration that cannot be honored: a hard error, not a surprise.
func checkUniqueNames(repos []Repo) error {
	seen := make(map[string]string, len(repos))
	for _, r := range repos {
		if previous, ok := seen[r.Name()]; ok {
			return fmt.Errorf(
				"two repos share the short name %q (%s and %s) — this name is used by --without/--only "+
					"and by the worktree path, it must be unique within the nest",
				r.Name(), previous, repoIdentifier(r))
		}
		seen[r.Name()] = repoIdentifier(r)
	}
	return nil
}

// repoIdentifier names what the user typed to declare this repo, for
// diagnostics: a `path:` entry's path, or "key <k>" for a key-typed entry —
// which has no path to show (it is filled only after Resolve).
func repoIdentifier(r Repo) string {
	if r.Path != "" {
		return r.Path
	}
	return "key " + r.Key
}

// selectRepos applies --without / --only to the list declared by the nest.
// Required repos are always kept: only optional ones get filtered. Declaration
// order is preserved — it fixes the order of `sbx create` positionals.
func selectRepos(repos []Repo, without, only []string) ([]Repo, error) {
	if len(without) > 0 && len(only) > 0 {
		return nil, fmt.Errorf("--without and --only are mutually exclusive")
	}

	known := make(map[string]Repo, len(repos))
	for _, r := range repos {
		known[r.Name()] = r
	}

	check := func(flag string, values []string) error {
		for _, v := range values {
			if _, ok := known[v]; !ok {
				return fmt.Errorf("%s: repo %q unknown in this nest (available: %s)",
					flag, v, strings.Join(sortedNames(repos), ", "))
			}
		}
		return nil
	}
	if err := check("--without", without); err != nil {
		return nil, err
	}
	if err := check("--only", only); err != nil {
		return nil, err
	}

	excluded := make(map[string]bool, len(without))
	for _, v := range without {
		if !known[v].Optional {
			return nil, fmt.Errorf("--without: %q is a required repo of this nest, it cannot be removed", v)
		}
		excluded[v] = true
	}

	keep := make(map[string]bool, len(only))
	for _, v := range only {
		keep[v] = true
	}

	out := make([]Repo, 0, len(repos))
	for _, r := range repos {
		switch {
		case !r.Optional: // required: always kept
		case excluded[r.Name()]:
			continue
		case len(only) > 0 && !keep[r.Name()]:
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func sortedNames(repos []Repo) []string {
	out := make([]string, 0, len(repos))
	for _, r := range repos {
		out = append(out, r.Name())
	}
	slices.Sort(out)
	return out
}
