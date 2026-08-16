package source

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/worktree"
)

// git runs git for FIXTURE BUILDING only, in dir. Production code goes through
// worktree.Git.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.email=t@example.test", "-c", "user.name=t"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// installedFromRemote builds a source remote holding validManifestTree, clones
// it into denHome/sources/<name>, and returns the remote's directory so a test
// can publish new versions into it.
func installedFromRemote(t *testing.T, denHome, name string) string {
	t.Helper()
	remote := gitInit(t, validManifestTree(t))
	if err := os.MkdirAll(Root(denHome), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, filepath.Dir(remote), "clone", "-q", "--", remote, Dir(denHome, name))
	return remote
}

// publish rewrites the remote's manifest at a new version and commits it, the
// way a team publishes one.
func publish(t *testing.T, remote, version string) {
	t.Helper()
	raw, err := os.ReadFile(ManifestPath(remote))
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(raw), "version: 1.0.0", "version: "+version, 1)
	if updated == string(raw) {
		t.Fatalf("the fixture manifest no longer carries version 1.0.0:\n%s", raw)
	}
	writeFile(t, ManifestPath(remote), updated)
	git(t, remote, "add", "-A")
	git(t, remote, "commit", "-q", "-m", "publish "+version)
}

// FetchCandidate brings the remote's content in WITHOUT moving the installed
// checkout: the plan is computed from a detached worktree, and HEAD only moves
// after a confirmation.
func TestFetchCandidateLeavesTheInstalledCheckoutWhereItWas(t *testing.T) {
	denHome := t.TempDir()
	remote := installedFromRemote(t, denHome, "dg")
	dir := Dir(denHome, "dg")
	before := git(t, dir, "rev-parse", "HEAD")
	publish(t, remote, "2.0.0")

	c, err := FetchCandidate(context.Background(), worktree.NewGit(), denHome, "dg")
	if err != nil {
		t.Fatalf("FetchCandidate: %v", err)
	}
	defer c.Close()

	if c.Manifest == nil || c.Manifest.Metadata.Version != "2.0.0" {
		t.Fatalf("the candidate does not carry the published version: %+v", c.Manifest)
	}
	if c.Commit == "" || c.Commit == before {
		t.Errorf("candidate commit = %q, expected the fetched one (installed is %q)", c.Commit, before)
	}
	if got := git(t, dir, "rev-parse", "HEAD"); got != before {
		t.Errorf("the installed checkout moved during a fetch: %s -> %s", before, got)
	}

	// Closing must deregister the detached worktree BEFORE removing its
	// directory, or the installed clone keeps a dangling registration.
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if out := git(t, dir, "worktree", "list"); strings.Contains(out, stagingDir(denHome)) {
		t.Errorf("Close left a worktree registered:\n%s", out)
	}
	if entries, err := os.ReadDir(stagingDir(denHome)); err == nil && len(entries) > 0 {
		t.Errorf("Close left %d staged entrie(s) behind", len(entries))
	}
}

// The guards of the legacy update are the guards of this one, in the same
// order and BEFORE the fetch: reaching a refusal after a fetch has orphaned
// local commits leaves the user with a remedy that destroys their work.
func TestFetchCandidateRefusesADirtyOrUnpushedCheckout(t *testing.T) {
	t.Run("dirty", func(t *testing.T) {
		denHome := t.TempDir()
		installedFromRemote(t, denHome, "dg")
		writeFile(t, filepath.Join(Dir(denHome, "dg"), "scratch.txt"), "work in progress\n")

		_, err := FetchCandidate(context.Background(), worktree.NewGit(), denHome, "dg")
		if err == nil || !strings.Contains(err.Error(), "local changes") {
			t.Fatalf("error = %v, expected the dirty refusal", err)
		}
	})

	t.Run("unpushed", func(t *testing.T) {
		denHome := t.TempDir()
		installedFromRemote(t, denHome, "dg")
		dir := Dir(denHome, "dg")
		writeFile(t, filepath.Join(dir, "nests", "local.yaml"), nestYAML("base"))
		git(t, dir, "add", "-A")
		git(t, dir, "commit", "-q", "-m", "local work")

		_, err := FetchCandidate(context.Background(), worktree.NewGit(), denHome, "dg")
		if err == nil || !strings.Contains(err.Error(), "unpushed") {
			t.Fatalf("error = %v, expected the unpushed refusal", err)
		}
	})
}

// DecideUpdate is the whole version policy of `den source update`, in one
// pure function: equal, drifted, greater, lower.
func TestDecideUpdateRanksVersionsExactly(t *testing.T) {
	for _, tc := range []struct {
		name       string
		configured string
		candidate  string
		sameCommit bool
		want       UpdateAction
		refuses    string
	}{
		{name: "same version, same commit", configured: "1.2.0", candidate: "1.2.0",
			sameCommit: true, want: UpdateUnchanged},
		{name: "same version, new commit", configured: "1.2.0", candidate: "1.2.0",
			want: UpdateDrift},
		{name: "greater version", configured: "1.2.0", candidate: "1.3.0", want: UpdateConverge},
		{name: "never configured", configured: "", candidate: "1.0.0", want: UpdateConverge},
		{name: "lower version", configured: "2.0.0", candidate: "1.9.9", refuses: "older"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecideUpdate("dg", tc.configured, tc.candidate, tc.sameCommit)
			if tc.refuses != "" {
				if err == nil || !strings.Contains(err.Error(), tc.refuses) {
					t.Fatalf("error = %v, expected a refusal mentioning %q", err, tc.refuses)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecideUpdate: %v", err)
			}
			if got != tc.want {
				t.Errorf("action = %q, want %q", got, tc.want)
			}
		})
	}
}

// A version den cannot rank is a refusal, never a guess: acting on it would
// mean converging in a direction nobody chose.
func TestDecideUpdateRefusesAnUnrankableVersion(t *testing.T) {
	if _, err := DecideUpdate("dg", "1.0.0", "next", false); err == nil {
		t.Fatal("expected a refusal: \"next\" is not a version den can compare")
	}
}
