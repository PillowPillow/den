package converge

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/PillowPillow/den/internal/nest"
	"github.com/PillowPillow/den/internal/source"
	"github.com/PillowPillow/den/internal/worktree"
)

// RepoRequirement is one repository key an installed source needs, merged
// across every exported nest that declares it.
//
// RequiredBy and OptionalFor are kept apart because they carry different
// consequences: a missing REQUIRED repository makes each nest in RequiredBy
// not_ready, while a missing optional one changes nothing (spec §7.2). Both
// are sorted, so a plan reads the same on every run.
type RepoRequirement struct {
	Key         string
	URL         string
	RequiredBy  []string
	OptionalFor []string
}

// Required reports whether some exported nest needs this repository to be
// usable at all.
func (r RepoRequirement) Required() bool { return len(r.RequiredBy) > 0 }

// MatchKind is how den found (or failed to find) a repository. The values are
// ordered by how much den knows, and only the top two are acted on without a
// human: everything else is a guess, and den never mounts a guess.
type MatchKind string

const (
	// MatchExplicit: the user named the directory (answer file or wizard).
	MatchExplicit MatchKind = "explicit"
	// MatchRemote: exactly one directory whose remote IS the declared
	// repository, whatever it is called on disk.
	MatchRemote MatchKind = "remote"
	// MatchName: the directory is named after the repository, but its remotes
	// say nothing. A guess.
	MatchName MatchKind = "name"
	// MatchAmbiguous: several candidates, none decisive.
	MatchAmbiguous MatchKind = "ambiguous"
	// MatchAbsent: this machine does not have it. Not an error — den never
	// clones a working repository (spec §3).
	MatchAbsent MatchKind = "absent"
)

// RepoMatch is the verdict for one requirement.
//
// Confirmed is what the planner acts on: only an explicit choice or an exact
// remote match earns it. A name-only or ambiguous match is REPORTED, and the
// plan stays unconfirmable until a human chooses or an answer file names the
// directory — mounting the wrong repository is worse than mounting none.
type RepoMatch struct {
	Requirement RepoRequirement
	Kind        MatchKind
	Path        string
	Candidates  []string
	Confirmed   bool
}

// CollectRepoRequirements reads every exported nest and merges their `repos:`
// declarations by key.
//
// By key rather than per nest: two nests of one source share a repository, and
// den must discover it once, map it once, and be able to say which nests a
// missing one blocks.
func CollectRepoRequirements(root string, m *source.Manifest) ([]RepoRequirement, error) {
	byKey := map[string]*RepoRequirement{}
	// Sorted nest names: the merge order decides nothing, but the ERROR order
	// does — a source with two conflicts must report the same one on every CI
	// run.
	for _, name := range m.ExportedNestNames() {
		n, err := nest.LoadNest(root, name)
		if err != nil {
			return nil, err
		}
		for _, r := range n.Repos {
			if r.Key == "" {
				// A `path:` in a source nest is refused by lint (it cannot
				// travel), so nothing to discover here.
				continue
			}
			req, seen := byKey[r.Key]
			if !seen {
				req = &RepoRequirement{Key: r.Key, URL: r.URL}
				byKey[r.Key] = req
			}
			// The URL is what discovery matches on, so two nests disagreeing
			// about it is a contradiction den cannot arbitrate: whichever it
			// picked, it could mount someone else's repository under a key the
			// other nest also uses.
			if req.URL != r.URL && r.URL != "" {
				if req.URL == "" {
					req.URL = r.URL
				} else {
					return nil, fmt.Errorf(
						"repo key %q is declared with two different urls in this source (%s and %s) — "+
							"den resolves a key to ONE repository per source; fix the nests that declare it",
						r.Key, req.URL, r.URL)
				}
			}
			if r.Optional {
				req.OptionalFor = append(req.OptionalFor, name)
			} else {
				req.RequiredBy = append(req.RequiredBy, name)
			}
		}
	}
	out := make([]RepoRequirement, 0, len(byKey))
	for _, key := range slices.Sorted(maps.Keys(byKey)) {
		req := byKey[key]
		slices.Sort(req.RequiredBy)
		slices.Sort(req.OptionalFor)
		out = append(out, *req)
	}
	return out, nil
}

// DiscoverRepos locates each required repository under the roots given for
// THIS execution. It never clones, and never writes anything.
//
// The classification is deliberately conservative. den confirms a mapping only
// when it is a fact — the user named the directory, or exactly one directory
// carries the declared remote. Everything else is reported for a human to
// settle, because a wrong mapping is not a missing repository: it silently
// mounts unrelated code into a sandbox and the mistake surfaces as a build
// failure nobody attributes to onboarding.
//
// The second return value is the plan WARNINGS the scan produced. A directory
// den could not open is not an error — a home tree holds several, and none may
// fail an installation — but it must not pass in silence either: an unreadable
// directory and a missing repository print the same `absent`, and only one of
// the two is answered by cloning something.
func DiscoverRepos(ctx context.Context, git worktree.Git, reqs []RepoRequirement, a Answers) ([]RepoMatch, []string, error) {
	// Scanned ONCE for all requirements: a developer's ~/Development holds
	// dozens of repositories, and re-reading every remote per key would fork
	// git n×m times for one answer.
	candidates, unreadable, err := scanRoots(ctx, git, a.RepositoryRoots)
	if err != nil {
		return nil, nil, err
	}
	var warnings []string
	if len(unreadable) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"%d director%s under the repository roots could not be read (first: %s) — a repository "+
				"under one of them is reported absent; fix the permissions, or name the directory "+
				"under `repos:` in the answer file",
			len(unreadable), plural(len(unreadable)), unreadable[0]))
	}

	out := make([]RepoMatch, 0, len(reqs))
	for _, req := range reqs {
		match := RepoMatch{Requirement: req, Kind: MatchAbsent}

		if chosen, ok := a.Repos[req.Key]; ok {
			// Verified, not trusted: an override naming a directory that is not
			// a git worktree would otherwise reach `sbx create` as a workspace
			// and fail inside the VM, far from the file that named it.
			if err := requireWorktree(ctx, git, chosen); err != nil {
				return nil, nil, fmt.Errorf("repo %q: %w", req.Key, err)
			}
			match.Kind, match.Path, match.Confirmed = MatchExplicit, chosen, true
			out = append(out, match)
			continue
		}

		wanted := normalizeRemote(req.URL)
		var byRemote, byName []string
		for _, c := range candidates {
			if wanted != "" && slices.Contains(c.remotes, wanted) {
				byRemote = append(byRemote, c.path)
				continue
			}
			if namesRepo(c.path, req) {
				byName = append(byName, c.path)
			}
		}

		switch {
		case len(byRemote) == 1:
			match.Kind, match.Path, match.Confirmed = MatchRemote, byRemote[0], true
		case len(byRemote) > 1:
			// Two directories carrying the SAME remote: clones of one
			// repository, and den cannot know which one the user works in.
			match.Kind, match.Candidates = MatchAmbiguous, byRemote
		case len(byName) == 1:
			match.Kind, match.Path = MatchName, byName[0]
		case len(byName) > 1:
			match.Kind, match.Candidates = MatchAmbiguous, byName
		}
		out = append(out, match)
	}
	return out, warnings, nil
}

// plural is the "y/ies" of the one warning above. Inline in the format string
// it would be a nested Sprintf nobody reads.
func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// MatchesFromMapping is the STATUS view of what DiscoverRepos answers for an
// installation: which declared repositories this machine can actually mount.
//
// It reads the mapping den already wrote and checks the directories still
// exist. No scan, no git, no classification: a status reports on a decision
// already made, and rediscovering would let `den source status` disagree with
// what a spawn will really mount.
//
// A mapped directory that has since been moved or deleted comes out ABSENT,
// exactly like one that was never mapped — because for the nest that needs it,
// it is.
func MatchesFromMapping(reqs []RepoRequirement, mapping map[string]string) []RepoMatch {
	out := make([]RepoMatch, 0, len(reqs))
	for _, req := range reqs {
		match := RepoMatch{Requirement: req, Kind: MatchAbsent}
		if path := mapping[req.Key]; path != "" {
			if _, err := os.Stat(path); err == nil {
				match.Kind, match.Path, match.Confirmed = MatchExplicit, path, true
			} else {
				// The path is kept on an absent match: the remedy is "clone it
				// back there, or remap it", and neither is actionable without
				// the directory den was told about.
				match.Path = path
			}
		}
		out = append(out, match)
	}
	return out
}

// UnconfirmedMatches returns the matches a human (or an answer file) still has
// to settle, in requirement order. An absent repository is NOT one of them:
// there is nothing to choose, and the nests that need it simply become
// not_ready.
func UnconfirmedMatches(matches []RepoMatch) []RepoMatch {
	var out []RepoMatch
	for _, m := range matches {
		if !m.Confirmed && m.Kind != MatchAbsent {
			out = append(out, m)
		}
	}
	return out
}

// candidate is one directory scanned under a root, with its normalized
// remotes.
type candidate struct {
	path    string
	remotes []string
}

// maxScanDepth bounds how far below a root the scan walks. Five levels covers
// the layouts developers actually use — `<root>/<repo>`, `<root>/<org>/<repo>`,
// and a couple of grouping directories above that — while keeping a root
// pointed at a home directory from walking until the disk stops answering.
const maxScanDepth = 5

// scan is one execution's walk. The accumulators are fields rather than
// pointer arguments because the recursion carries four of them, and a
// signature that long is where a caller passes the wrong `seen` map.
type scan struct {
	git  worktree.Git
	seen map[string]bool
	// found is every checkout the walk kept, unsorted until scanRoots ends.
	found []candidate
	// unreadable is every directory BELOW a root the walk could not open.
	// Collected rather than ignored: a permission error that silently yields
	// nothing is indistinguishable from "you do not have that repository",
	// and the second answer is the one den would print.
	unreadable []string
}

// scanRoots walks up to maxScanDepth levels below each root and reads the
// remotes of the directories that are git repositories. It returns the
// candidates and the directories it could not open.
//
// It STOPS descending at the first `.git`, and that prune is a correctness
// rule before it is a speed one. A `node_modules` or a `vendor` tree lives
// inside a checkout, so the walk never enters one; and a repository whose
// sibling worktrees or submodules sit under it would otherwise contribute
// several directories carrying ONE remote, turning a clean match into an
// ambiguity a human has to settle. The cost of the prune is that a submodule
// of a matched repository is never a candidate of its own — den mounts working
// repositories, and a submodule is part of one.
//
// Dot-directories are skipped for the same two reasons: `~/.cache`, `~/.local`
// and an agent's own worktrees under `.claude/` hold checkouts nobody works
// in, and they are what makes a home directory expensive to scan.
func scanRoots(ctx context.Context, git worktree.Git, roots []string) ([]candidate, []string, error) {
	s := &scan{git: git, seen: map[string]bool{}}
	// Sorted roots and sorted entries: the candidate lists a plan prints must
	// be identical between two runs on one machine.
	sorted := slices.Clone(roots)
	slices.Sort(sorted)
	for _, root := range sorted {
		if err := s.walk(ctx, root, 1); err != nil {
			return nil, nil, err
		}
	}
	slices.SortFunc(s.found, func(a, b candidate) int { return strings.Compare(a.path, b.path) })
	slices.Sort(s.unreadable)
	return s.found, s.unreadable, nil
}

// walk reads one directory, keeps the checkouts and recurses into the rest.
// depth is the level of the ENTRIES it is about to read, so the cap is
// compared before any work.
//
// ctx is checked here and not only in remoteIdentities: this walk is the one
// part of den that can spend seconds on a directory tree with no subprocess to
// interrupt, and root.go's signal.NotifyContext is what must end it on ^C.
func (s *scan) walk(ctx context.Context, dir string, depth int) error {
	if depth > maxScanDepth {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			// A root that does not exist is a typo or a machine that is laid
			// out differently — it makes repositories undiscovered, which the
			// plan already reports as absent. Refusing here would fail an
			// installation over a directory nobody needs.
			return nil
		}
		if depth > 1 {
			// Below a root the walk is fail-open — a home tree holds
			// directories the user cannot read, and none of them may fail an
			// installation — but never silent: the plan warns with the count.
			s.unreadable = append(s.unreadable, dir)
			return nil
		}
		return fmt.Errorf("scanning %s for repositories: %w", dir, err)
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if s.seen[path] {
			continue
		}
		s.seen[path] = true
		if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
			if err := s.walk(ctx, path, depth+1); err != nil {
				return err
			}
			continue // not a checkout: nothing to read, nothing to fork git for
		}
		remotes, err := remoteIdentities(ctx, s.git, path)
		if err != nil {
			// A directory with a .git that git will not answer for is
			// broken, not a discovery failure: it must not fail the whole
			// installation.
			continue
		}
		s.found = append(s.found, candidate{path: path, remotes: remotes})
	}
	return nil
}

// remoteIdentities returns the normalized identity of every remote of a
// checkout. All of them, not just origin: a teammate who works from a fork
// keeps the canonical repository under another remote name, and their
// checkout IS the repository the source declares.
func remoteIdentities(ctx context.Context, git worktree.Git, dir string) ([]string, error) {
	raw, err := git.Run(ctx, dir, "config", "--get-regexp", `^remote\..*\.url$`)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		_, url, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		if id := normalizeRemote(url); id != "" && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out, nil
}

// requireWorktree refuses an override that is not a git checkout.
func requireWorktree(ctx context.Context, git worktree.Git, dir string) error {
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("%s: %w", dir, err)
	}
	out, err := git.Run(ctx, dir, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		return fmt.Errorf(
			"%s is not a git working tree — den mounts working repositories, and a directory that "+
				"only looks like one would fail inside the sandbox, far from the file that named it", dir)
	}
	return nil
}

// namesRepo is the fallback identity: the directory is called like the key, or
// like the last segment of the declared URL. A guess, and treated as one —
// its only use is to give a human something to confirm.
func namesRepo(path string, req RepoRequirement) bool {
	base := filepath.Base(path)
	if base == req.Key {
		return true
	}
	id := normalizeRemote(req.URL)
	if id == "" {
		return false
	}
	return base == id[strings.LastIndex(id, "/")+1:]
}

// normalizeRemote delegates to internal/source, which owns the SOLE
// definition of "these two URLs are the same repository". Two definitions is
// exactly the divergence that would let discovery match a repository the
// namespace arbitration then treats as a different one.
func normalizeRemote(url string) string { return source.NormalizeRemoteURL(url) }
