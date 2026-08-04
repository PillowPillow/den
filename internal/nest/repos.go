package nest

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/PillowPillow/den/internal/config"
)

// checkUniqueNames rejects two repos sharing a basename within one list.
// The basename IS a repo's identity: --without/--only address it by this
// name, and it becomes a path (worktree_root/<wt>/<repo>) and the position of
// a `sbx create` argument. Two homonyms would make all three
// ambiguous — and `--without api` would silently drop two of them. This is a
// configuration that cannot be honored: a hard error, not a surprise.
//
// scope names WHAT the list is, because the two callers judge different things:
// LoadNest judges a single file (the fix is in the yaml), Resolve judges the
// merged spawn, where the collision may be between a file and the command line
// and only the second is fixable by retyping. repoOrigin() carries the other half.
func checkUniqueNames(repos []Repo, scope string) error {
	seen := make(map[string]Repo, len(repos))
	for _, r := range repos {
		if previous, ok := seen[r.Name()]; ok {
			return fmt.Errorf(
				"two repos share the short name %q (%s and %s) — this name is used by --without/--only "+
					"and by the worktree path, it must be unique within the %s",
				r.Name(), repoOrigin(previous), repoOrigin(r), scope)
		}
		seen[r.Name()] = r
	}
	return nil
}

// repoOrigin names where a repo came from, for the collision message. Named
// with the "repo" prefix, not "origin" alone, because this package sits next
// to git: a bare `origin` reads as the remote, not as "declared file vs.
// command line".
//
// A declared repo shows its path ALONE, which keeps LoadNest's message exactly
// what it was: at load time the command line does not exist, and mentioning it
// would send the user to correct something that had no part in the collision.
func repoOrigin(r Repo) string {
	if r.AdHoc {
		return r.Path + " (command line)"
	}
	return r.Path
}

// parseRepoArgs turns the command line's positionals into repos.
//
// cwd is REQUIRED as soon as there is one positional, and its absence is an
// error rather than a fallback on the process's working directory: that
// fallback would be a silent retreat to exactly the system access the parameter
// exists to keep out of this package, and it would only show itself at runtime,
// on the wrong path.
func parseRepoArgs(cwd string, raws []string) ([]Repo, error) {
	if len(raws) == 0 {
		return nil, nil
	}
	if cwd == "" {
		return nil, fmt.Errorf(
			"%d repo(s) given on the command line but nest.Options.Cwd is unset, so a relative "+
				"path has nothing to resolve against — this is a wiring defect in den, please report it",
			len(raws))
	}
	out := make([]Repo, 0, len(raws))
	for _, raw := range raws {
		r, err := parseRepoArg(cwd, raw)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// parseRepoArg normalizes ONE positional into a Repo.
//
// The order of the three steps is the whole point:
//
//  1. `:ro` is refused FIRST, before any path handling. sbx accepts the suffix
//     on a workspace, so someone who writes it is asking for something den
//     deliberately does not do — and letting the string through would answer
//     "no such path" about a directory that exists. The check runs on a
//     TRIMMED copy so a shell-quoted `" ~/dev/api:ro "` still hits it, but the
//     value that flows into the path itself is left untouched: trimming the
//     path would silently normalize a directory that legitimately has
//     leading/trailing spaces in its name, and den refuses rather than
//     normalizing in silence (spec §2) — such a directory must survive intact
//     and be named verbatim (with %q) by the later existence check.
//  2. ExpandPath, like `repos:`, `ssh.dir` and `config_dir`. It handles the
//     tilde and NOTHING else.
//  3. absolutize against cwd. This step, and only this step, is what makes
//     `den scratch .` work: sbx.checkWorkspace rejects every relative path,
//     because it would resolve against a working directory nothing guarantees
//     by the time sbx uses it.
func parseRepoArg(cwd, raw string) (Repo, error) {
	if strings.TrimSpace(raw) == "" {
		return Repo{}, fmt.Errorf(
			"empty repo path on the command line — `sbx create` would receive an empty positional, " +
				"which mounts nothing")
	}
	if strings.HasSuffix(strings.TrimSpace(raw), ":ro") {
		return Repo{}, fmt.Errorf(
			"repo %q: `:ro` is not supported — a repo given on the command line is mounted "+
				"writable, like a declared `repos:` entry", raw)
	}
	expanded, err := config.ExpandPath(raw)
	if err != nil {
		return Repo{}, err
	}
	if !filepath.IsAbs(expanded) {
		expanded = filepath.Join(cwd, expanded)
	}
	return Repo{Path: filepath.Clean(expanded), AdHoc: true}, nil
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
