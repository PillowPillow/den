package converge

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/source"
	"github.com/PillowPillow/den/internal/worktree"
)

// Normalization is what lets den recognize a repository a teammate cloned
// over ssh while the manifest names it over https: the two spellings are the
// same repository, and a discovery that compared them literally would find
// nothing on half the machines.
func TestNormalizeRemote(t *testing.T) {
	same := []string{
		"git@gitlab.example.com:team/api.git",
		"ssh://git@gitlab.example.com/team/api.git",
		"https://gitlab.example.com/team/api.git",
		"https://user:token@gitlab.example.com/team/api",
		"https://gitlab.example.com/team/api/",
	}
	want := normalizeRemote(same[0])
	if want != "gitlab.example.com/team/api" {
		t.Fatalf("normalizeRemote(%q) = %q", same[0], want)
	}
	for _, url := range same[1:] {
		if got := normalizeRemote(url); got != want {
			t.Errorf("normalizeRemote(%q) = %q, want %q", url, got, want)
		}
	}
	// The owner path is part of the identity: two repositories named "api"
	// under different groups are different repositories.
	if normalizeRemote("https://gitlab.example.com/other/api.git") == want {
		t.Error("two different owner paths normalized to the same identity")
	}
	// A credential in a URL must never survive into anything den compares,
	// prints or stores.
	if strings.Contains(normalizeRemote("https://user:token@gitlab.example.com/team/api"), "token") {
		t.Error("the normalized identity carries a credential")
	}
}

func initRepoWithRemote(t *testing.T, dir, remote string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", remote},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func requirement(key, url string, optional bool) RepoRequirement {
	r := RepoRequirement{Key: key, URL: url, RequiredBy: []string{"api"}}
	if optional {
		r.RequiredBy = nil
		r.OptionalFor = []string{"api"}
	}
	return r
}

func discover(t *testing.T, reqs []RepoRequirement, a Answers) []RepoMatch {
	t.Helper()
	matches, err := DiscoverRepos(context.Background(), worktree.NewGit(), reqs, a)
	if err != nil {
		t.Fatalf("DiscoverRepos: %v", err)
	}
	return matches
}

// The remote is authoritative: a directory renamed by its owner is still the
// repository the manifest declares, and den finds it.
func TestDiscoverReposPrefersNormalizedRemote(t *testing.T) {
	root := t.TempDir()
	repo := initRepoWithRemote(t, filepath.Join(root, "renamed-folder"), "git@gitlab.example.com:team/api.git")

	matches := discover(t,
		[]RepoRequirement{requirement("api", "https://gitlab.example.com/team/api.git", false)},
		Answers{RepositoryRoots: []string{root}})

	if len(matches) != 1 {
		t.Fatalf("matches = %+v", matches)
	}
	if matches[0].Kind != MatchRemote || matches[0].Path != repo {
		t.Fatalf("match = %+v, want a confirmed remote match on %s", matches[0], repo)
	}
	if !matches[0].Confirmed {
		t.Error("an exact remote match needs no confirmation: it is not a guess")
	}
}

// A directory whose NAME matches but whose remote says nothing is a guess.
// den reports it and waits for a human (or an answer file) — mounting the
// wrong repository silently is the failure this whole classification exists
// to prevent.
func TestDiscoverReposReportsNameOnlyMatchesAsUnconfirmed(t *testing.T) {
	root := t.TempDir()
	repo := initRepoWithRemote(t, filepath.Join(root, "api"), "git@gitlab.example.com:someone-else/fork.git")

	matches := discover(t,
		[]RepoRequirement{requirement("api", "https://gitlab.example.com/team/api.git", false)},
		Answers{RepositoryRoots: []string{root}})

	if matches[0].Kind != MatchName || matches[0].Path != repo {
		t.Fatalf("match = %+v, want an unconfirmed name match", matches[0])
	}
	if matches[0].Confirmed {
		t.Error("a name-only match is a guess: it must not be confirmed on its own")
	}
}

// Two candidates and no remote to arbitrate: den names both and confirms
// neither.
func TestDiscoverReposReportsAmbiguity(t *testing.T) {
	rootA, rootB := t.TempDir(), t.TempDir()
	a := initRepoWithRemote(t, filepath.Join(rootA, "api"), "git@example.com:one/other.git")
	b := initRepoWithRemote(t, filepath.Join(rootB, "api"), "git@example.com:two/other.git")

	matches := discover(t,
		[]RepoRequirement{requirement("api", "https://gitlab.example.com/team/api.git", false)},
		Answers{RepositoryRoots: []string{rootA, rootB}})

	if matches[0].Kind != MatchAmbiguous || matches[0].Confirmed {
		t.Fatalf("match = %+v, want an unconfirmed ambiguous match", matches[0])
	}
	if len(matches[0].Candidates) != 2 {
		t.Fatalf("candidates = %v, want both directories", matches[0].Candidates)
	}
	// Sorted: the same two candidates must be listed in the same order on
	// every run, or a plan would differ between identical invocations.
	if matches[0].Candidates[0] > matches[0].Candidates[1] {
		t.Errorf("candidates are not sorted: %v", matches[0].Candidates)
	}
	if matches[0].Candidates[0] != min(a, b) {
		t.Errorf("candidates = %v", matches[0].Candidates)
	}
}

// An explicit `repos:` entry ends every question: the user named the
// directory, so nothing is guessed and nothing is asked.
func TestDiscoverReposHonoursAnExplicitOverride(t *testing.T) {
	root := t.TempDir()
	initRepoWithRemote(t, filepath.Join(root, "api"), "git@gitlab.example.com:team/api.git")
	chosen := initRepoWithRemote(t, filepath.Join(t.TempDir(), "elsewhere"), "git@gitlab.example.com:team/api.git")

	matches := discover(t,
		[]RepoRequirement{requirement("api", "https://gitlab.example.com/team/api.git", false)},
		Answers{RepositoryRoots: []string{root}, Repos: map[string]string{"api": chosen}})

	if matches[0].Kind != MatchExplicit || matches[0].Path != chosen || !matches[0].Confirmed {
		t.Fatalf("match = %+v, want the explicit override confirmed", matches[0])
	}
}

// An override that is not a git worktree is refused: den would otherwise
// mount a directory that only looks like a repository, and the failure would
// surface inside the VM.
func TestDiscoverReposRefusesAnOverrideThatIsNotARepository(t *testing.T) {
	notARepo := t.TempDir()
	_, err := DiscoverRepos(context.Background(), worktree.NewGit(),
		[]RepoRequirement{requirement("api", "https://gitlab.example.com/team/api.git", false)},
		Answers{Repos: map[string]string{"api": notARepo}})
	if err == nil || !strings.Contains(err.Error(), notARepo) {
		t.Fatalf("DiscoverRepos = %v, expected a refusal naming the directory", err)
	}
}

// Nothing found is a normal outcome, not an error: the machine simply does not
// have that repository, and the nests needing it become not_ready (spec §7.2).
func TestDiscoverReposReportsAbsence(t *testing.T) {
	matches := discover(t,
		[]RepoRequirement{requirement("api", "https://gitlab.example.com/team/api.git", false)},
		Answers{RepositoryRoots: []string{t.TempDir()}})
	if matches[0].Kind != MatchAbsent || matches[0].Path != "" {
		t.Fatalf("match = %+v, want an absent match with no path", matches[0])
	}
}

// Only DIRECT children are scanned. A deep walk would cross node_modules,
// vendor trees and other people's checkouts, and take minutes on a developer's
// home directory.
func TestDiscoverReposScansDirectChildrenOnly(t *testing.T) {
	root := t.TempDir()
	initRepoWithRemote(t, filepath.Join(root, "deep", "api"), "git@gitlab.example.com:team/api.git")

	matches := discover(t,
		[]RepoRequirement{requirement("api", "https://gitlab.example.com/team/api.git", false)},
		Answers{RepositoryRoots: []string{root}})
	if matches[0].Kind != MatchAbsent {
		t.Fatalf("match = %+v, want absent: a nested repository is not a direct child", matches[0])
	}
}

// The requirements come from the exported nests, merged by key: a repository
// two nests need is discovered once, and both nests are named so a missing one
// can say what it blocks.
func TestCollectRepoRequirementsMergesAcrossExportedNests(t *testing.T) {
	root := manifestTreeWithNests(t, map[string]string{
		"leo": "stack: base\nrepos:\n  - { key: api, url: https://gitlab.example.com/team/api.git }\n" +
			"  - { key: crm, url: https://gitlab.example.com/team/crm.git, optional: true }\n",
		"go-dgdev": "stack: base\nrepos:\n  - { key: api, url: https://gitlab.example.com/team/api.git }\n",
	})
	m, err := source.LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	reqs, err := CollectRepoRequirements(root, m)
	if err != nil {
		t.Fatalf("CollectRepoRequirements: %v", err)
	}
	if len(reqs) != 2 {
		t.Fatalf("requirements = %+v, want one per key", reqs)
	}
	// Sorted by key: the plan's order may not depend on map iteration.
	if reqs[0].Key != "api" || reqs[1].Key != "crm" {
		t.Fatalf("requirements = %+v, expected them sorted by key", reqs)
	}
	if len(reqs[0].RequiredBy) != 2 || reqs[0].RequiredBy[0] != "go-dgdev" || reqs[0].RequiredBy[1] != "leo" {
		t.Errorf("api.RequiredBy = %v, want both nests sorted", reqs[0].RequiredBy)
	}
	// An optional-only repository never makes a nest not_ready.
	if len(reqs[1].RequiredBy) != 0 || len(reqs[1].OptionalFor) != 1 {
		t.Errorf("crm = %+v, want optional-only", reqs[1])
	}
}

// One key, two URLs, is a contradiction in the SOURCE: den cannot decide which
// repository the key names, and a wrong guess mounts someone else's code.
func TestCollectRepoRequirementsRefusesConflictingURLs(t *testing.T) {
	root := manifestTreeWithNests(t, map[string]string{
		"leo":      "stack: base\nrepos:\n  - { key: api, url: https://gitlab.example.com/team/api.git }\n",
		"go-dgdev": "stack: base\nrepos:\n  - { key: api, url: https://gitlab.example.com/other/api.git }\n",
	})
	m, err := source.LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CollectRepoRequirements(root, m); err == nil ||
		!strings.Contains(err.Error(), "api") {
		t.Fatalf("CollectRepoRequirements = %v, expected the conflicting key to be named", err)
	}
}
