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
//
// checkNoDuplicatePaths runs FIRST, and as an entirely separate pass over the
// list, because the case it catches — the exact same path given twice — is
// also a basename collision, and the basename message misdescribes it: it
// sends the reader hunting for a SECOND, distinct path that shares nothing
// with the one on screen, when there isn't one. A single fused loop would
// report whichever collision it reaches first by position (so
// [/dev/a, /dev/b/a, /dev/a] would blame the basename at index 1 and never
// reach the exact duplicate at index 2); the separate pre-pass makes the more
// useful diagnosis win regardless of where in the list it falls.
func checkUniqueNames(repos []Repo, scope string) error {
	if err := checkNoDuplicatePaths(repos, scope); err != nil {
		return err
	}
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

// checkNoDuplicatePaths is checkUniqueNames' pre-pass; see its comment for
// why this runs first and separately.
//
// Compared after filepath.Clean, not as raw strings: a declared `repos:`
// entry is only ever tilde-expanded (LoadNest, nest.go), never Cleaned, while
// a command-line path IS Cleaned (parseRepoArg) — so a declared
// `path: /dev/api/` and a typed `/dev/api` are the same directory that raw
// equality would call different, silently falling through to the basename
// message this check exists to preempt. Raw equality was the rejected
// alternative for exactly that reason.
//
// A key-typed entry is SKIPPED, not compared: its Path is empty until Resolve
// fills it from the personal mapping, and filepath.Clean("") is "." — so two
// unrelated `key:` repos would both land on "." and LoadNest would refuse a
// perfectly legal nest with a message naming a path nobody wrote. Their
// uniqueness is not lost, it is enforced one loop later, on Name(), which
// returns the Key for exactly these entries. Once Resolve HAS filled the
// paths, the merged check sees them as ordinary paths and two keys pointing
// at the same directory still collide here.
func checkNoDuplicatePaths(repos []Repo, scope string) error {
	seen := make(map[string]Repo, len(repos))
	for _, r := range repos {
		if r.Path == "" {
			continue
		}
		clean := filepath.Clean(r.Path)
		if previous, ok := seen[clean]; ok {
			return duplicatePathError(previous, r, scope)
		}
		seen[clean] = r
	}
	return nil
}

// duplicatePathError names the collision as what it is — the same path given
// twice — rather than deduplicating in silence: den refuses rather than
// normalizing a configuration it cannot honor unambiguously (spec §2), the
// same call parseRepoArg makes about a path's whitespace. Which of the two
// wordings applies follows AdHoc, same as repoOrigin:
//
//   - both from the command line: neither is more "correct" than the other,
//     so the remedy is simply to drop one.
//   - both declared: the fix is in the yaml, one `repos:` entry is redundant.
//   - one of each: unambiguous — the declared entry stands, the positional is
//     the fixable half, so the remedy names dropping IT specifically.
func duplicatePathError(a, b Repo, scope string) error {
	switch {
	case a.AdHoc && b.AdHoc:
		// Naming a alone, not both: this branch is only ever reached with
		// a.Path == b.Path byte-for-byte, never merely "the same after another
		// Clean". Each ad-hoc Path already IS filepath.Clean's output
		// (parseRepoArg), and Clean is idempotent — so two ad-hoc entries
		// denoting the same directory converge to one canonical string before
		// they ever reach this function, whatever their raw, as-typed forms
		// were. There is no second spelling left to show.
		return fmt.Errorf(
			"repo %s is given twice on the command line — drop one occurrence", a.Path)
	case !a.AdHoc && !b.AdHoc:
		return fmt.Errorf(
			"repo %s is declared twice in the %s — remove one `repos:` entry", a.Path, scope)
	default:
		declared := a
		if a.AdHoc {
			declared = b
		}
		return fmt.Errorf(
			"repo %s is already declared in the %s — drop it from the command line",
			declared.Path, scope)
	}
}

// checkUniqueMountBasenames rejects two repos whose MOUNT basename collides —
// two different identities pointing at directories that happen to share a
// last component (`api-v1 -> /dev/a/api`, `api-v2 -> /dev/b/api`).
//
// It exists because Repo.Name() and the on-disk name parted ways when `key:`
// arrived: Name() returns the KEY for a key-typed entry, while worktree.Path
// still derives the directory from the repository path — under the default
// central layout it joins <worktree_root>/<wt>/Base(repoPath). So a pair of
// distinct keys walks past checkNoDuplicatePaths (the paths do differ) and past
// checkUniqueNames (the keys do differ), and then, under -w, targets one single
// directory. Before `key:`, Name() WAS the basename and checkUniqueNames caught
// this pre-flight; this pass restores that coverage without taking the key
// back, because the key is precisely the shareable identity a team nest needs
// --without/--only to address.
//
// worktree.Path is the ONLY collision claimed here, and deliberately so. An
// earlier draft of this comment and of the message below also asserted that the
// two would collide as `sbx create` workspaces. That is not den's to assert:
// the workspaces it passes are HOST paths (sbx.Config.Workspaces), so what
// becomes of two distinct ones inside the VM is a statement about sbx's mount
// behavior — and under §14.1's own open hypothesis A11 (each workspace mounted
// at the same absolute path in the VM as on the host) /dev/a/api and /dev/b/api
// would NOT collide at all. Nothing has measured it; anything about sbx is a
// hypothesis to document, never an assertion (see configDirToken, resolve.go),
// and it has no business in a user-facing refusal.
//
// Leaving it to worktree.Ensure was the rejected alternative. It does refuse —
// checkOwnership sees a directory belonging to another repository — but only
// after the FIRST worktree has been created, which is the one orphaned
// worktree per spawn that the whole ordering of internal/spawn (spec §6:
// everything rejectable from config alone happens before the first side
// effect) exists to prevent.
//
// It refuses unconditionally even though the collision it names needs -w, and
// needs the central layout (per-repo puts the worktree under <repo>/.den/<wt>,
// which does not collide). Narrowing it to the case that bites would be a
// behavior CHANGE, not a fix: before `key:`, Name() was the basename and
// checkUniqueNames refused this pair whatever the flags — so refusing
// unconditionally is what preserves the pre-branch verdict. It also keeps
// nest.Resolve free of any notion of -w, a spawn-time flag the cascade does not
// receive and should not start threading.
//
// Runs LAST, after checkNoDuplicatePaths and checkUniqueNames, so the two
// sharper diagnoses win where they apply: the same path given twice and the
// same short name given twice are both basename collisions too, and this
// message misdescribes either one.
//
// An entry whose Path is still empty is skipped, the same tolerance
// checkNoDuplicatePaths documents and for the same reason — LoadNest never
// fills a key's path, and Base("") is ".", which would collide two unrelated
// keys under a name nobody wrote.
func checkUniqueMountBasenames(repos []Repo, scope string) error {
	seen := make(map[string]Repo, len(repos))
	for _, r := range repos {
		if r.Path == "" {
			continue
		}
		base := filepath.Base(r.Path)
		if previous, ok := seen[base]; ok {
			return fmt.Errorf(
				"repos %s and %s both mount a directory named %q — den names each worktree directory "+
					"after that last component (<worktree_root>/<worktree>/%s), so under -w the two would "+
					"land on the same directory; rename one of the directories, or drop one of the two "+
					"entries from the %s",
				repoOrigin(previous), repoOrigin(r), base, base, scope)
		}
		seen[base] = r
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
//
// A key-typed entry is named "key <k>", not by its path: before Resolve it HAS
// none, and a bare empty string would leave the reader with half a collision to
// hunt. Once resolveRepoKeys HAS filled the path, the key still leads and the
// path follows in parentheses — both halves are needed and neither substitutes
// for the other. The key is the line to edit under `repos:` in config.yaml (the
// nest's `key: api-v1` is not itself wrong, and on a teammate's machine the same
// nest is fine); the path is the directory the collision is actually about, and
// checkUniqueMountBasenames' remedy — rename one of them — cannot be acted on
// without seeing it. Reporting the path alone was the rejected form: it names a
// directory the user never typed, leaving them to reverse the mapping by hand.
//
// The AdHoc arm comes first without ambiguity — a positional's Path is never
// empty (parseRepoArg refuses a blank one) and it never carries a Key.
func repoOrigin(r Repo) string {
	switch {
	case r.AdHoc:
		return r.Path + " (command line)"
	case r.Key != "" && r.Path != "":
		return "key " + r.Key + " (" + r.Path + ")"
	case r.Path == "":
		return "key " + r.Key
	default:
		return r.Path
	}
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
//     `den up scratch --repo .` work: sbx.checkWorkspace rejects every relative path,
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
