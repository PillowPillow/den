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

func gitInit(t *testing.T, dir string) string {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "-A"},
		{"-c", "user.email=t@example.test", "-c", "user.name=t", "commit", "-q", "-m", "source"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// A candidate is a clone den has NOT installed: the plan is computed from it,
// and <denHome>/sources stays untouched until the resources verify.
func TestAcquireCandidateStagesOutsideSources(t *testing.T) {
	denHome := t.TempDir()
	remote := gitInit(t, validManifestTree(t))

	c, err := AcquireCandidate(context.Background(), worktree.NewGit(), denHome, remote)
	if err != nil {
		t.Fatalf("AcquireCandidate: %v", err)
	}
	defer c.Close()

	if c.Manifest == nil || c.Manifest.Metadata.Version != "1.0.0" {
		t.Fatalf("manifest = %+v", c.Manifest)
	}
	if c.Commit == "" {
		t.Error("the candidate must carry the commit its receipt will attest")
	}
	// NOT under sources/: source.List enumerates every entry there, so a
	// staging directory would appear in `den source ls` as a phantom source.
	if strings.HasPrefix(c.Root, Root(denHome)) {
		t.Errorf("the candidate was staged under sources/: %s", c.Root)
	}
	if entries, err := os.ReadDir(Root(denHome)); err == nil && len(entries) > 0 {
		t.Errorf("sources/ is not empty before installation: %v", entries)
	}
	// But on the same filesystem as sources/, or the install rename below
	// could never be atomic.
	if !strings.HasPrefix(c.Root, denHome) {
		t.Errorf("the candidate is outside the den home (%s): the install rename would cross filesystems", c.Root)
	}

	if err := c.Install(denHome, "dg"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !HasManifest(Dir(denHome, "dg")) {
		t.Error("the installed source has no manifest")
	}
	// Close after an install must not remove the source it just placed.
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !HasManifest(Dir(denHome, "dg")) {
		t.Error("Close removed the installed source")
	}
}

// A source WITHOUT a manifest is acquired without error: `den source add`
// keeps installing it through the legacy path, and only the caller knows which
// command was typed (spec §9.3).
func TestAcquireCandidateAcceptsALegacySource(t *testing.T) {
	denHome := t.TempDir()
	tree := t.TempDir()
	mkdirAll(t, filepath.Join(tree, "stacks", "devx"))
	writeFile(t, filepath.Join(tree, "stacks", "devx", "stack.yaml"), "image: devx:v1\n")
	remote := gitInit(t, tree)

	c, err := AcquireCandidate(context.Background(), worktree.NewGit(), denHome, remote)
	if err != nil {
		t.Fatalf("AcquireCandidate: %v", err)
	}
	defer c.Close()
	if c.Manifest != nil {
		t.Error("a legacy source has no manifest")
	}
}

// The namespace: `--name` wins, the manifest recommends, and a name already
// held by ANOTHER remote is refused — installing over it would replace one
// team's nests with another's under references the user already wrote.
func TestResolveNamespace(t *testing.T) {
	denHome := t.TempDir()
	git := worktree.NewGit()
	remote := gitInit(t, validManifestTree(t))
	m, err := LoadManifest(remote)
	if err != nil {
		t.Fatal(err)
	}

	name, err := ResolveNamespace(context.Background(), git, denHome, remote, "", m)
	if err != nil || name != "dg" {
		t.Fatalf("ResolveNamespace = (%q, %v), want the manifest's recommendation", name, err)
	}
	name, err = ResolveNamespace(context.Background(), git, denHome, remote, "corp", m)
	if err != nil || name != "corp" {
		t.Fatalf("ResolveNamespace = (%q, %v), want the --name override", name, err)
	}

	// Install something else under "dg".
	other := gitInit(t, validManifestTree(t))
	c, err := AcquireCandidate(context.Background(), git, denHome, other)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Install(denHome, "dg"); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveNamespace(context.Background(), git, denHome, remote, "", m); err == nil {
		t.Fatal("expected a refusal: the namespace belongs to another remote")
	} else if !strings.Contains(err.Error(), "--name") {
		t.Errorf("error = %q, expected the remedy", err.Error())
	}
	// The SAME remote under that name is not a conflict: it is a
	// reinstallation of what is already there.
	if _, err := ResolveNamespace(context.Background(), git, denHome, other, "", m); err != nil {
		t.Errorf("ResolveNamespace refused the source already installed there: %v", err)
	}
}

// The digest is over the FILE, so two manifests that decode identically but
// differ in text are told apart — that is what a maintainer compares.
func TestManifestDigestCoversTheBytes(t *testing.T) {
	root := validManifestTree(t)
	first, err := ManifestDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first, "sha256:") {
		t.Fatalf("digest = %q", first)
	}
	rewriteManifest(t, root, validManifestYAML+"\n# a comment, no decoded change\n")
	second, err := ManifestDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Error("the digest must cover the file's bytes, not the decoded model")
	}
}
