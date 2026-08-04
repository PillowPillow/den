// internal/source/mutate.go
package source

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/lint"
	"github.com/PillowPillow/den/internal/worktree"
)

// DefaultName derives a source name from its URL: the last path component,
// stripped of a ".git" suffix. Exported for the CLI's help text to stay
// honest about what "default" means.
func DefaultName(url string) string {
	base := path.Base(strings.TrimSuffix(strings.TrimSuffix(url, "/"), ".git"))
	return base
}

// Add clones url under sources/<name> and lints the result. An invalid
// source is REMOVED, not kept: a clone that fails lint would sit there
// half-usable, visible to Locate, and every later refusal would blame the
// wrong command. Refusing at add time names the actual fault: the repo.
func Add(ctx context.Context, git worktree.Git, denHome, url, name string) (string, error) {
	if name == "" {
		name = DefaultName(url)
	}
	if err := config.ValidateSourceName(name); err != nil {
		return "", fmt.Errorf("%w — pass `--name <legal name>`", err)
	}
	dir := Dir(denHome, name)
	if _, err := os.Stat(dir); err == nil {
		return "", fmt.Errorf(
			"source %q: already installed at %s — `den source update %s` refreshes it, "+
				"`den source rm %s` removes it", name, dir, name, name)
	}
	if err := os.MkdirAll(Root(denHome), 0o755); err != nil {
		return "", fmt.Errorf("creating %s: %w", Root(denHome), err)
	}
	// Root(denHome) as cwd: `git clone url dir` needs no repo, only a directory.
	if _, err := git.Run(ctx, Root(denHome), "clone", "--", url, dir); err != nil {
		return "", err
	}
	if errs := lint.Run(dir); len(errs) > 0 {
		// Best-effort removal: the refusal below matters more than the cleanup's
		// own error, and a leftover directory is visible in `den source ls`.
		os.RemoveAll(dir)
		return "", lintRefusal(name, url, errs)
	}
	return name, nil
}

// lintRefusal assembles lint findings into one refusal, ConfigError-shaped:
// all faults at once, so the team repo gets one report instead of one per push.
func lintRefusal(name, where string, errs []error) error {
	var b strings.Builder
	fmt.Fprintf(&b, "source %q: %s is not a valid source:", name, where)
	for _, e := range errs {
		fmt.Fprintf(&b, "\n  - %v", e)
	}
	return errors.New(b.String())
}

// Update fetches and fast-forwards — with the lint gate BETWEEN the two
// (spec 2026-08-04 §3): the fetched tree is linted in a throwaway detached
// git worktree, and an invalid upstream leaves HEAD exactly where it was.
// Fail-closed is the point: a team member who pushed a typo must not be able
// to break every colleague's next spawn.
func Update(ctx context.Context, git worktree.Git, denHome, name string) error {
	if err := config.ValidateSourceName(name); err != nil {
		return err
	}
	dir := Dir(denHome, name)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("source %q: not installed — expected %s; `den source ls` shows what is", name, dir)
	}
	// Dirty check FIRST: den never touches unpushed contributions. `git status
	// --porcelain` is empty exactly when the tree is clean, untracked included.
	status, err := git.Run(ctx, dir, "status", "--porcelain")
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(status)) > 0 {
		return fmt.Errorf(
			"source %q: the working tree at %s has local changes — commit or discard them first; "+
				"den never overwrites unpushed contributions", name, dir)
	}
	if _, err := git.Run(ctx, dir, "fetch", "origin"); err != nil {
		return err
	}
	// Lint the FETCHED tree before moving HEAD. A detached worktree is the
	// one git-native way to materialize FETCH_HEAD without touching the
	// clone's own checkout; --force on removal because the throwaway tree is
	// ours and gone either way.
	tmp, err := os.MkdirTemp("", "den-source-lint-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	probe := filepath.Join(tmp, "tree")
	if _, err := git.Run(ctx, dir, "worktree", "add", "--detach", probe, "FETCH_HEAD"); err != nil {
		return err
	}
	lintErrs := lint.Run(probe)
	// The removal's own error is secondary to the lint verdict: swallowing the
	// verdict here would turn a safety-critical refusal into a bare git error,
	// and the temp dir is gone via defer either way. `worktree prune` clears
	// the .git/worktrees/ registration `os.RemoveAll(tmp)` cannot reach, so a
	// failed `remove` never leaves a stale entry in the user's clone.
	if _, err := git.Run(ctx, dir, "worktree", "remove", "--force", probe); err != nil {
		git.Run(ctx, dir, "worktree", "prune")
	}
	if len(lintErrs) > 0 {
		return fmt.Errorf("%w\nthe local clone stays on its last valid state — nothing changed",
			lintRefusal(name, "the fetched update", lintErrs))
	}
	if _, err := git.Run(ctx, dir, "merge", "--ff-only", "FETCH_HEAD"); err != nil {
		return fmt.Errorf(
			"source %q: cannot fast-forward — the team repo rewrote its history. "+
				"If you have no local work: `den source rm %s` then `den source add <url> --name %s` (%w)",
			name, name, name, err)
	}
	return nil
}

// Remove deletes the clone. The dirty refusal mirrors Update's and exists
// for the same reason; --porcelain again, untracked included: a file the
// user created is work, whether git tracks it or not.
func Remove(ctx context.Context, git worktree.Git, denHome, name string) error {
	if err := config.ValidateSourceName(name); err != nil {
		return err
	}
	dir := Dir(denHome, name)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("source %q: not installed — expected %s", name, dir)
	}
	status, err := git.Run(ctx, dir, "status", "--porcelain")
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(status)) > 0 {
		return fmt.Errorf(
			"source %q: the working tree at %s has local changes — push or discard them first; "+
				"`den source rm` never destroys unpushed contributions", name, dir)
	}
	return os.RemoveAll(dir)
}
