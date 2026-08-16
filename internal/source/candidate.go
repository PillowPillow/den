package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/worktree"
)

// Candidate is a source checkout den is CONSIDERING: a fresh clone that is not
// installed yet, or the installed checkout itself when nothing is being
// acquired.
//
// It exists so the whole plan — including the manifest, the resources and the
// exported nests — can be computed and confirmed BEFORE anything lands in
// <denHome>/sources. An installation that failed halfway used to leave a
// directory that `den source ls` would list and every later command would
// trip on; here the directory becomes a source only after its resources
// verify.
type Candidate struct {
	Root   string
	Commit string
	// URL is the remote the candidate came from, empty when the candidate IS
	// the installed checkout.
	URL      string
	Manifest *Manifest

	// temp is the staging directory Close removes. Empty for a BORROWED
	// candidate (the installed checkout): Close must never delete the user's
	// source.
	temp string
	// installed is set once the candidate has been moved into place, guarding
	// against a SECOND Install renaming an already-installed source out from
	// under its name: Install's own os.Stat(dest) refusal already catches a
	// repeat call under the SAME name, but a repeat call under a DIFFERENT
	// name would find dest free and happily os.Rename what is now a live
	// source (c.Root already points at it) into a second location — this
	// field is what refuses that instead.
	installed bool
	// cleanup runs BEFORE temp is removed, for a candidate that is more than a
	// directory: a fetched update is a registered git worktree, and its
	// registration must be dropped while its directory still exists (see
	// detachWorktree).
	cleanup func() error
}

// Close removes the staging directory. It is safe to call on a borrowed
// candidate — it then does nothing, which is what lets every caller
// `defer c.Close()` without knowing which kind it holds.
//
// It removes temp even AFTER a successful Install, and that is deliberate:
// Install renames the checkout OUT of temp (into <denHome>/sources/<name>), so
// what is left is an empty staging directory. Skipping it left one
// `cache/sources/candidate-*` behind per installation, forever — visible to
// anyone who looks, and growing.
func (c *Candidate) Close() error {
	if c == nil || c.temp == "" {
		return nil
	}
	if c.cleanup != nil {
		// Once: Close is routinely called explicitly AND deferred, and a second
		// `worktree remove` on an already-removed probe fails for a reason that
		// has nothing to do with this call.
		cleanup := c.cleanup
		c.cleanup = nil
		if err := cleanup(); err != nil {
			return err
		}
	}
	return os.RemoveAll(c.temp)
}

// stagingDir is where a candidate is cloned before it is installed.
//
// Under cache/, not under sources/, and that placement carries two
// requirements at once. It must be on the SAME FILESYSTEM as sources/ for the
// final rename to be atomic (a cross-device rename fails outright, and /tmp is
// routinely another filesystem). And it must not be under sources/, because
// List enumerates every entry there — a leftover staging directory would show
// up in `den source ls` as a phantom source. cache/ is den's own purgeable
// space, which is exactly what a half-finished clone is.
func stagingDir(denHome string) string { return filepath.Join(denHome, "cache", "sources") }

// AcquireCandidate clones url into a temporary directory and reads its
// manifest. It installs nothing.
//
// The clone is REQUIRED before the plan: the manifest, the exports and the
// resources are in the repository, so den cannot say what installing would do
// without reading it first. What den does not do is put it where a later
// command would find it.
func AcquireCandidate(ctx context.Context, git worktree.Git, denHome, url string) (*Candidate, error) {
	staging := stagingDir(denHome)
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", staging, err)
	}
	temp, err := os.MkdirTemp(staging, "candidate-*")
	if err != nil {
		return nil, fmt.Errorf("preparing a temporary clone: %w", err)
	}
	c := &Candidate{Root: filepath.Join(temp, "checkout"), URL: url, temp: temp}
	if _, err := git.Run(ctx, temp, "clone", "--", url, c.Root); err != nil {
		c.Close()
		return nil, err
	}
	commit, err := git.Run(ctx, c.Root, "rev-parse", "HEAD")
	if err != nil {
		c.Close()
		return nil, err
	}
	c.Commit = strings.TrimSpace(string(commit))

	if !HasManifest(c.Root) {
		// Deliberately NOT an error here: `den source add` still installs a
		// legacy source through its existing path, and only `den init --source`
		// requires a manifest. The caller decides, because only the caller
		// knows which command was typed (spec §9.3).
		return c, nil
	}
	m, err := LoadManifest(c.Root)
	if err != nil {
		c.Close()
		return nil, err
	}
	c.Manifest = m
	return c, nil
}

// InstalledCandidate borrows the installed checkout as a candidate, so
// `den source configure` runs the same planner as an installation without
// cloning anything (spec §9.3).
func InstalledCandidate(ctx context.Context, git worktree.Git, denHome, name string) (*Candidate, error) {
	root, err := requireInstalled(denHome, name)
	if err != nil {
		return nil, err
	}
	c := &Candidate{Root: root}
	if commit, err := git.Run(ctx, root, "rev-parse", "HEAD"); err == nil {
		c.Commit = strings.TrimSpace(string(commit))
	}
	// A commit den cannot read is not fatal: a source checkout that is not a
	// git repository is broken in a way `den source ls` reports, and refusing
	// here would block the very command that reconfigures it.
	if HasManifest(root) {
		m, err := LoadManifest(root)
		if err != nil {
			return nil, err
		}
		c.Manifest = m
	}
	return c, nil
}

// Install moves a candidate into <denHome>/sources/<name>.
//
// A rename, not a copy: it is atomic within a filesystem, so no other command
// can ever observe a half-populated source directory. The staging area is
// chosen to make that possible (see stagingDir).
func (c *Candidate) Install(denHome, name string) error {
	if c.installed {
		return fmt.Errorf(
			"candidate already installed at %s: Install runs once per candidate — acquire a fresh "+
				"one to install again", c.Root)
	}
	if err := config.ValidateSourceName(name); err != nil {
		return err
	}
	dest := Dir(denHome, name)
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("source %q: already installed at %s", name, dest)
	}
	if err := os.MkdirAll(Root(denHome), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", Root(denHome), err)
	}
	if err := os.Rename(c.Root, dest); err != nil {
		return fmt.Errorf("installing source %q into %s: %w", name, dest, err)
	}
	c.Root = dest
	c.installed = true
	return nil
}

// InstalledURL reports the remote of an installed source, normalized for
// comparison. Empty when the checkout has no origin — a source installed by
// hand, which the namespace arbitration then cannot contradict.
func InstalledURL(ctx context.Context, git worktree.Git, denHome, name string) string {
	out, err := git.Run(ctx, Dir(denHome, name), "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return NormalizeRemoteURL(strings.TrimSpace(string(out)))
}

// ResolveNamespace decides the name a source is installed under.
//
// `--name` wins; otherwise the manifest's recommendation (spec §5.1). A name
// already taken by a source with a DIFFERENT remote is refused: installing
// over it would silently replace another team's nests and stacks with
// same-named ones, and every reference in the user's config would then resolve
// somewhere else. The same remote under the same name is not a conflict — it
// is the reinstallation the user asked for.
func ResolveNamespace(ctx context.Context, git worktree.Git, denHome, url, flagName string, m *Manifest) (string, error) {
	name := flagName
	if name == "" && m != nil {
		name = m.Metadata.Name
	}
	if name == "" {
		name = DefaultName(url)
	}
	if err := config.ValidateSourceName(name); err != nil {
		return "", fmt.Errorf("%w — pass `--name <legal name>`", err)
	}
	dir := Dir(denHome, name)
	if _, err := os.Stat(dir); err != nil {
		return name, nil // free
	}
	installed := InstalledURL(ctx, git, denHome, name)
	if installed != "" && installed == NormalizeRemoteURL(url) {
		return name, nil
	}
	return "", fmt.Errorf(
		"source name %q is already used by %s on this machine — install this one under another name "+
			"with `--name <name>`, or remove the other with `den source rm %s`",
		name, dir, name)
}

// ManifestDigest is the fingerprint the receipt records: sha256 over the exact
// bytes of den-source.yaml.
//
// Over the FILE, not over the decoded model: what a receipt attests is the
// contract den read, and two manifests that decode identically but differ in
// text are two different files a maintainer may have to tell apart.
func ManifestDigest(root string) (string, error) {
	raw, err := os.ReadFile(ManifestPath(root))
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", ManifestPath(root), err)
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
