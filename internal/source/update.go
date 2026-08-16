package source

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/PillowPillow/den/internal/worktree"
)

// UpdateAction is what `den source update` does with a fetched candidate.
// Three outcomes, because a source that publishes a version and a source that
// merely moved its branch are two different events with two different answers.
type UpdateAction string

const (
	// UpdateUnchanged: the machine is already at this exact version, on this
	// exact commit. Nothing to plan.
	UpdateUnchanged UpdateAction = "unchanged"
	// UpdateDrift: the same version, on a different commit. den does NOT
	// converge it — see DecideUpdate.
	UpdateDrift UpdateAction = "drift"
	// UpdateConverge: a greater version, or a source never configured here.
	UpdateConverge UpdateAction = "converge"
)

// DecideUpdate ranks the fetched candidate against what this machine is
// configured for (spec §11.2). It reads nothing and writes nothing: the
// version policy is one pure function so it can be exhaustively tested, and so
// the CLI cannot grow a second reading of it.
//
// configured is Personal.Version — the EXACT version whose resources were
// applied here — never the version of the checkout. The two disagree exactly
// while a convergence is unfinished, and that is the case RequireUsable
// refuses; deciding an update from the checkout would compare against a state
// nobody ever converged.
//
// A DOWNGRADE is refused rather than planned. den converges forward: the
// resources of the older version were, by construction, not applied to reach
// the newer one, and nothing in the receipt says what un-applying would even
// mean. The team publishing a lower version is the fault to fix.
//
// Equal versions on different commits are UpdateDrift, and den leaves the
// checkout alone: the manifest is the contract, and the same contract must
// describe the same machine state. Moving the checkout under an unchanged
// version would silently change the provision scripts a later build runs while
// every receipt on every teammate's machine still attests the same version.
func DecideUpdate(name, configured, candidate string, sameCommit bool) (UpdateAction, error) {
	if strings.TrimSpace(configured) == "" {
		// Installed but never configured here — the state `den source add`
		// leaves after a refused confirmation. Converging is what fixes it.
		return UpdateConverge, nil
	}
	from, ok := comparableVersion(configured)
	if !ok {
		return "", fmt.Errorf(
			"source %q: this machine is configured for version %q, which is not a version den can "+
				"compare — fix `version:` in %s, or reconverge with `den source configure %s`",
			name, configured, "the source's personal configuration", name)
	}
	to, ok := comparableVersion(candidate)
	if !ok {
		return "", fmt.Errorf(
			"source %q: the fetched update declares version %q, which is not a version den can "+
				"compare — the source must publish <major>.<minor>.<patch>", name, candidate)
	}
	switch c := semver.Compare(to, from); {
	case c < 0:
		return "", fmt.Errorf(
			"source %q: the fetched update is version %s, older than the %s this machine is "+
				"configured for — den converges forward only; nothing was changed, and the checkout "+
				"is untouched. The team must publish a version greater than %s",
			name, candidate, configured, configured)
	case c > 0:
		return UpdateConverge, nil
	case sameCommit:
		return UpdateUnchanged, nil
	default:
		return UpdateDrift, nil
	}
}

// comparableVersion puts a manifest version into x/mod's v-prefixed form.
// Exact versions only — the spelling LoadManifest already enforces.
func comparableVersion(v string) (string, bool) {
	trimmed := strings.TrimSpace(v)
	if !isExactVersion(trimmed) {
		return "", false
	}
	return "v" + trimmed, true
}

// FetchCandidate fetches the installed source's remote and returns the fetched
// content as a candidate, WITHOUT moving the installed checkout.
//
// A detached git worktree rather than a second clone: the objects are already
// in the installed repository after the fetch, so a clone would copy them
// again, and the worktree is what makes "plan from the update, keep running
// the current version" possible at all.
//
// The guards run in the SAME order as the legacy Update, and before the fetch:
// dirty, then upstream, then unpushed commits. That order is the safety
// property — a refusal reached after a fetch has orphaned local commits leaves
// the user with a remedy (`den source rm`) that destroys the very work the
// refusal was protecting.
//
// The fetch itself is not neutral (it moves remote-tracking refs and resets the
// staleness clock a spawn reads), and it cannot be deferred: the candidate's
// version is IN the fetched content. So "nothing is mutated before a decision"
// holds for the checkout and for the den home, not for the remote-tracking
// refs — and every refusal downstream says the checkout is untouched.
func FetchCandidate(ctx context.Context, git worktree.Git, denHome, name string) (*Candidate, error) {
	dir, err := requireInstalled(denHome, name)
	if err != nil {
		return nil, err
	}
	dirty, err := isDirty(ctx, git, dir)
	if err != nil {
		return nil, err
	}
	if dirty {
		return nil, fmt.Errorf(
			"source %q: the working tree at %s has local changes — commit or discard them first; "+
				"den never overwrites unpushed contributions", name, dir)
	}
	if err := requireUpstream(ctx, git, dir, name); err != nil {
		return nil, err
	}
	if err := aheadOfUpstream(ctx, git, dir, name); err != nil {
		return nil, err
	}
	if _, err := git.Run(ctx, dir, "fetch"); err != nil {
		return nil, err
	}

	staging := stagingDir(denHome)
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", staging, err)
	}
	temp, err := os.MkdirTemp(staging, "update-*")
	if err != nil {
		return nil, fmt.Errorf("preparing a temporary worktree: %w", err)
	}
	probe := filepath.Join(temp, "checkout")
	// Best effort, error ignored: prune only drops registrations whose
	// directory is already gone, so it cannot touch a live worktree. It clears
	// the debris a run killed between `add` and `remove` left behind.
	git.Run(ctx, dir, "worktree", "prune")
	// "@{u}" as a literal ref, never a pre-resolved sha reused later: it always
	// names THIS branch's own tracked ref, which requireUpstream guaranteed
	// exists (the reason mutate.go's Update refuses FETCH_HEAD).
	if _, err := git.Run(ctx, dir, "worktree", "add", "--detach", probe, "@{u}"); err != nil {
		os.RemoveAll(temp)
		return nil, err
	}

	c := &Candidate{Root: probe, temp: temp, cleanup: detachWorktree(ctx, git, dir, probe)}
	commit, err := git.Run(ctx, probe, "rev-parse", "HEAD")
	if err != nil {
		c.Close()
		return nil, err
	}
	c.Commit = strings.TrimSpace(string(commit))
	if HasManifest(probe) {
		m, err := LoadManifest(probe)
		if err != nil {
			c.Close()
			return nil, err
		}
		c.Manifest = m
	}
	return c, nil
}

// detachWorktree deregisters the probe from the installed clone.
//
// Removal BEFORE the directory is deleted, which is why this is a cleanup hook
// and not something Close could infer: `git worktree remove` on a directory
// that is already gone is a no-op, and the registration then stays in the
// clone's .git/worktrees where every later `den source` operation trips on it.
//
// context.WithoutCancel: this runs while unwinding, often BECAUSE the context
// was canceled (Ctrl-C), and a cleanup that inherited that cancellation would
// leave exactly the dangling registration it exists to prevent.
func detachWorktree(ctx context.Context, git worktree.Git, dir, probe string) func() error {
	return func() error {
		clean := context.WithoutCancel(ctx)
		if _, err := git.Run(clean, dir, "worktree", "remove", "--force", probe); err != nil {
			// The removal failed (a file lock, a permission): drop the directory
			// ourselves and let prune collect the registration.
			os.RemoveAll(probe)
			git.Run(clean, dir, "worktree", "prune")
		}
		return nil
	}
}
