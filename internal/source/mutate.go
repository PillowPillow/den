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
	"strconv"
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
//
// The existing-directory check runs BEFORE any side effect: it is what
// guarantees Add only ever os.RemoveAll's a directory it created itself in
// THIS call (the post-clone lint failure below) — never a pre-existing
// directory that happens to occupy the same path for unrelated reasons.
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

// isDirty reports whether dir's working tree has uncommitted changes,
// tracked or untracked. `--untracked-files=normal` is not decorative: plain
// `--porcelain` HONOURS the repo's own status.showUntrackedFiles config
// rather than overriding it, so a clone with that set to "no" — in its
// LOCAL config, which NeutralizeGitEnvironment does not and should not
// touch — would report a clean tree while hiding a brand-new untracked
// file. den polices what git can see; a .gitignore in the team repo still
// hides matching files from --porcelain at ANY untracked mode, and that is
// an accepted, undetectable gap, not a bug in this check.
func isDirty(ctx context.Context, git worktree.Git, dir string) (bool, error) {
	status, err := git.Run(ctx, dir, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return false, err
	}
	return len(bytes.TrimSpace(status)) > 0, nil
}

// requireUpstream confirms the checked-out branch tracks a remote branch.
// Both Update's fetch target and the ahead-count guard below need "@{u}" to
// resolve to the CORRECT ref; a branch created without --track — exactly
// the shape a contributor's own work-in-progress branch has — has none.
// Refusing here, by name, beats letting "@{u}" fail two calls deeper with a
// bare git error nobody can act on.
func requireUpstream(ctx context.Context, git worktree.Git, dir, name string) error {
	if _, err := git.Run(ctx, dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); err != nil {
		return fmt.Errorf(
			"source %q: the checked-out branch at %s has no upstream configured — "+
				"`git branch --set-upstream-to=origin/<branch>` in the clone, or push it so git sets "+
				"one; den cannot tell what to fetch onto, or what is still unpushed, without it", name, dir)
	}
	return nil
}

// unpushedCommitCount runs a `rev-list --count` query and parses the single
// integer it prints — the shared plumbing behind Update's and Remove's
// ahead-count guards below, which differ only in WHICH commits count as
// "unpushed" for their purpose.
func unpushedCommitCount(ctx context.Context, git worktree.Git, dir string, args ...string) (int, error) {
	out, err := git.Run(ctx, dir, append([]string{"rev-list", "--count"}, args...)...)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("parsing ahead count %q from %s: %w", bytes.TrimSpace(out), dir, err)
	}
	return n, nil
}

// aheadOfUpstream is Update's unpushed-commit guard: commits on the
// checked-out branch that are not on ITS OWN upstream ("@{u}..HEAD"). Scoped
// to the current branch on purpose — the ff-only merge below only ever
// touches that branch, so that is the only history Update itself can put at
// risk, and requireUpstream above has already guaranteed "@{u}" resolves.
// Called before the fetch below so Update never even reaches the ff-only
// refusal that recommends `den source rm` on work that refusal would then
// have destroyed. Update has no --force: unlike Remove, there is no
// "delete it anyway" reading of "update anyway" that makes sense.
func aheadOfUpstream(ctx context.Context, git worktree.Git, dir, name string) error {
	n, err := unpushedCommitCount(ctx, git, dir, "@{u}..HEAD")
	if err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	plural := ""
	if n > 1 {
		plural = "s"
	}
	return fmt.Errorf(
		"source %q: %d unpushed commit%s at %s — push them first; "+
			"den never fast-forwards a clone out from under local commits it cannot restore",
		name, n, plural, dir)
}

// unpushedAnywhere is Remove's unpushed-commit guard. Remove destroys the
// WHOLE directory, every local branch in it, not only the checked-out one —
// so, unlike Update, the guard must look past the current branch. It also
// must NOT require an upstream on the current branch: `den source rm` is the
// documented escape hatch the ff-only refusal itself names, and a
// contributor's own untracked work-in-progress branch (exactly the shape
// the fetch-ref fix above exists for) would make that hatch unreachable if
// Remove refused on missing "@{u}" the same way Update does. "--branches
// --not --remotes" sidesteps this entirely: it asks whether ANY local
// branch holds a commit absent from every known remote-tracking ref,
// without resolving an upstream for any single branch. Known, accepted gap
// (same class as isDirty's .gitignore note above, not solved here): a
// commit on a DETACHED HEAD belongs to no branch, so "--branches" does not
// see it either — den polices local branches, not every reachable commit.
// Same class again: a stash is invisible to BOTH this check and isDirty —
// neither `git status` nor `--branches --not --remotes` sees stashed work,
// so `den source rm` can silently drop a stash. Undocumented until now,
// accepted for the same reason as the other two: den polices what git
// tracks as history or working-tree state, not everything git can hold.
//
// "--branches --not --remotes" answers "is this commit on some remote-
// tracking ref", NOT "did the user push it" — an upstream history rewrite
// (`git fetch` force-updating a remote-tracking ref past a commit that was
// already fast-forwarded from it) orphans a commit exactly the same way an
// unpushed one looks, and den CANNOT tell the two apart from local state
// alone. Guessing which one it is would trade a safe refusal for a silent
// deletion, so this refuses either way — but the refusal must be escapable,
// which is what force is for below.
func unpushedAnywhere(ctx context.Context, git worktree.Git, dir, name string) error {
	n, err := unpushedCommitCount(ctx, git, dir, "--branches", "--not", "--remotes")
	if err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	plural := ""
	if n > 1 {
		plural = "s"
	}
	return fmt.Errorf(
		"source %q: %d commit%s at %s not reachable from any remote-tracking ref — this can be "+
			"genuinely unpushed work, or history the team repo rewrote out from under this clone; "+
			"`git -C %s log --branches --not --remotes` shows exactly what they are; push what is "+
			"worth keeping, or `den source rm --force %s` deletes it anyway",
		name, n, plural, dir, dir, name)
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
	// Dirty check FIRST: den never touches unpushed contributions.
	dirty, err := isDirty(ctx, git, dir)
	if err != nil {
		return err
	}
	if dirty {
		return fmt.Errorf(
			"source %q: the working tree at %s has local changes — commit or discard them first; "+
				"den never overwrites unpushed contributions", name, dir)
	}
	if err := requireUpstream(ctx, git, dir, name); err != nil {
		return err
	}
	if err := aheadOfUpstream(ctx, git, dir, name); err != nil {
		return err
	}
	// Bare `git fetch`, no explicit remote: it fetches whatever
	// branch.<name>.remote names for the CHECKED-OUT branch, which
	// requireUpstream above has already guaranteed is set. Hardcoding
	// "origin" here was its own bug, independent of the FETCH_HEAD one
	// below — a branch tracking a differently-named remote would have its
	// real remote left untouched while "origin" (irrelevant to this branch)
	// got fetched, and the merge below would then read "@{u}" pointing at
	// data that was never refreshed: a silent no-op indistinguishable from
	// "already up to date", with FETCH_HEAD's mtime bump still resetting the
	// 7-day staleness clock as if a real check had happened.
	if _, err := git.Run(ctx, dir, "fetch"); err != nil {
		return err
	}
	// Lint the fetched tree before moving HEAD, on the checked-out branch's
	// OWN upstream ("@{u}") — never FETCH_HEAD. `git fetch` with no refspec
	// writes one FETCH_HEAD line per advertised remote branch and marks
	// "for merge" only the one branch git considers explicitly requested;
	// on a clone whose checked-out branch has no upstream (a contributor's
	// own local work-in-progress branch, say) that marking does not point
	// at THIS branch's remote at all — den would lint and then attempt to
	// fast-forward onto whatever branch happened to be listed first.
	// requireUpstream above already refuses that case, so it cannot reach
	// here; "@{u}", passed as the literal ref string to both `worktree add`
	// and `merge --ff-only` (never pre-resolved to a SHA and reused),
	// always names THIS branch's own tracked ref instead.
	tmp, err := os.MkdirTemp("", "den-source-lint-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	probe := filepath.Join(tmp, "tree")
	if _, err := git.Run(ctx, dir, "worktree", "add", "--detach", probe, "@{u}"); err != nil {
		// Nothing was registered if `add` itself failed: no worktree to prune.
		return err
	}
	lintErrs := lint.Run(probe)
	// Remove the directory, THEN prune — in that order. `worktree prune`
	// only drops a registration whose directory is already gone; pruning
	// before clearing the directory is a no-op, and the deferred
	// os.RemoveAll(tmp) above only runs at function return, too late to help
	// here. A failed `worktree remove` must not leave a dangling entry in
	// `git worktree list`, on THIS path specifically — the ff-only merge
	// below reads history from `dir`, and a stale worktree registration is
	// exactly the kind of thing a future `den source` operation would trip on.
	if _, err := git.Run(ctx, dir, "worktree", "remove", "--force", probe); err != nil {
		os.RemoveAll(probe)
		git.Run(ctx, dir, "worktree", "prune")
	}
	if len(lintErrs) > 0 {
		return fmt.Errorf("%w\nthe local clone stays on its last valid state — nothing changed",
			lintRefusal(name, "the fetched update", lintErrs))
	}
	if _, err := git.Run(ctx, dir, "merge", "--ff-only", "@{u}"); err != nil {
		return fmt.Errorf(
			"source %q: cannot fast-forward — the team repo rewrote its history. A fetch just "+
				"orphaned any local commit that was only fast-forwarded from the old history, so "+
				"`den source rm %s` may itself refuse naming those same commits — "+
				"`den source rm --force %s` then `den source add <url> --name %s` if you have "+
				"nothing there worth keeping (%w)",
			name, name, name, name, err)
	}
	return nil
}

// Remove deletes the clone, or — with force — skips BOTH safety checks
// below and deletes it regardless of what they would have found. That
// escape hatch matters because it is the one the ff-only refusal above
// itself names: a `git fetch` that discovers the team repo rewrote history
// orphans any local commit that was only reachable via the OLD history (it
// is no longer on any remote-tracking ref, exactly what unpushedAnywhere
// looks for), on a clone the user never touched. Without --force that
// leaves no way out of a state Update itself created — a manual `rm -rf`
// would be the only remaining option, and this package exists specifically
// so users never need one.
//
// The dirty refusal mirrors Update's and exists for the same reason;
// --untracked-files=normal again, untracked included: a file the user
// created is work, whether git tracks it or not. Its ahead-count guard is
// unpushedAnywhere, deliberately NOT Update's aheadOfUpstream/
// requireUpstream pair: Remove is the documented escape hatch the ff-only
// refusal above names, and requiring an upstream here would make that
// hatch unreachable on exactly the branch shape (a contributor's untracked
// local branch) the fetch-ref fix above exists to handle — the same
// failure from the other direction.
func Remove(ctx context.Context, git worktree.Git, denHome, name string, force bool) error {
	if err := config.ValidateSourceName(name); err != nil {
		return err
	}
	dir := Dir(denHome, name)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("source %q: not installed — expected %s", name, dir)
	}
	if force {
		return os.RemoveAll(dir)
	}
	dirty, err := isDirty(ctx, git, dir)
	if err != nil {
		return err
	}
	if dirty {
		return fmt.Errorf(
			"source %q: the working tree at %s has local changes — `git -C %s status --porcelain "+
				"--untracked-files=normal` shows what (plain `git status` can lie here: a LOCAL "+
				"status.showUntrackedFiles=no hides untracked files from it); push or discard them "+
				"first, or `den source rm --force %s` to delete anyway; `den source rm` never "+
				"destroys unpushed contributions without --force", name, dir, dir, name)
	}
	if err := unpushedAnywhere(ctx, git, dir, name); err != nil {
		return err
	}
	return os.RemoveAll(dir)
}
