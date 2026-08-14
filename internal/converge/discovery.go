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
func DiscoverRepos(ctx context.Context, git worktree.Git, reqs []RepoRequirement, a Answers) ([]RepoMatch, error) {
	// Scanned ONCE for all requirements: a developer's ~/Development holds
	// dozens of repositories, and re-reading every remote per key would fork
	// git n×m times for one answer.
	candidates, err := scanRoots(ctx, git, a.RepositoryRoots)
	if err != nil {
		return nil, err
	}

	out := make([]RepoMatch, 0, len(reqs))
	for _, req := range reqs {
		match := RepoMatch{Requirement: req, Kind: MatchAbsent}

		if chosen, ok := a.Repos[req.Key]; ok {
			// Verified, not trusted: an override naming a directory that is not
			// a git worktree would otherwise reach `sbx create` as a workspace
			// and fail inside the VM, far from the file that named it.
			if err := requireWorktree(ctx, git, chosen); err != nil {
				return nil, fmt.Errorf("repo %q: %w", req.Key, err)
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
	return out, nil
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

// scanRoots lists the DIRECT children of each root and reads the remotes of
// those that are git repositories.
//
// Direct children only, and that is a deliberate limit rather than a
// simplification: a recursive walk would descend into node_modules, vendor
// trees and unrelated checkouts, take minutes on a home directory, and find
// candidates the user never thinks of as their repositories.
func scanRoots(ctx context.Context, git worktree.Git, roots []string) ([]candidate, error) {
	var out []candidate
	seen := map[string]bool{}
	// Sorted roots and sorted entries: the candidate lists a plan prints must
	// be identical between two runs on one machine.
	sorted := slices.Clone(roots)
	slices.Sort(sorted)
	for _, root := range sorted {
		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				// A root that does not exist is a typo or a machine that is
				// laid out differently — it makes repositories undiscovered,
				// which the plan already reports as absent. Refusing here would
				// fail an installation over a directory nobody needs.
				continue
			}
			return nil, fmt.Errorf("scanning %s for repositories: %w", root, err)
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			path := filepath.Join(root, e.Name())
			if seen[path] {
				continue
			}
			seen[path] = true
			if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
				continue // not a checkout: nothing to read, nothing to fork git for
			}
			remotes, err := remoteIdentities(ctx, git, path)
			if err != nil {
				// A directory with a .git that git will not answer for is
				// broken, not a discovery failure: it must not fail the whole
				// installation.
				continue
			}
			out = append(out, candidate{path: path, remotes: remotes})
		}
	}
	slices.SortFunc(out, func(a, b candidate) int { return strings.Compare(a.path, b.path) })
	return out, nil
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

// normalizeRemote reduces a git URL to "host/owner/name", the identity two
// spellings of one repository share.
//
// Every form a team really uses must converge:
//
//	git@gitlab.example.com:team/api.git
//	ssh://git@gitlab.example.com/team/api.git
//	https://gitlab.example.com/team/api.git
//
// The owner path is KEPT: two repositories called "api" under different
// groups are different repositories, and dropping the group would map one onto
// the other. Credentials embedded in an https URL are dropped — they are not
// part of the identity, and they must never reach a plan, a log or a receipt.
func normalizeRemote(url string) string {
	s := strings.TrimSpace(url)
	if s == "" {
		return ""
	}
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	} else if at := strings.Index(s, "@"); at >= 0 && strings.Contains(s[at:], ":") {
		// scp-like syntax: user@host:path — the colon is a separator, not a
		// port, and only this form uses it that way.
		s = s[at+1:]
		s = strings.Replace(s, ":", "/", 1)
	}
	// Credentials of an https URL (user:token@host), dropped after the scheme
	// so the scp-like branch above cannot be confused by them.
	if at := strings.Index(s, "@"); at >= 0 {
		s = s[at+1:]
	}
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	host, rest, ok := strings.Cut(s, "/")
	if !ok {
		return strings.ToLower(s)
	}
	// The host is case-insensitive; the path is not — git forges distinguish
	// "Team/API" from "team/api" on Linux, and normalizing it would merge two
	// real repositories.
	return strings.ToLower(host) + "/" + rest
}
