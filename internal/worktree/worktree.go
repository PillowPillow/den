// Package worktree propagates a git worktree across the repos of a nest. It is
// the only den module that drives git; like sbx, it does so behind an interface
// to stay substitutable.
package worktree

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

// Git is access to the git CLI, injected to stay substitutable.
type Git interface {
	Run(ctx context.Context, dir string, args ...string) ([]byte, error)
	// RunWithInput additionally feeds git's standard input. It exists for the
	// subcommands that only accept an UNQUOTED path list through `--stdin`:
	// `check-ignore -z` answers for a thousand directories in one fork, and git
	// refuses `-z` outside of `--stdin`.
	RunWithInput(ctx context.Context, dir string, input []byte, args ...string) ([]byte, error)
}

type gitExec struct{}

// NewGit returns real access to the git found on PATH.
func NewGit() Git { return gitExec{} }

// RedirectingVars are the environment variables that designate the target
// repository and TAKE PRECEDENCE over the current directory: while they are
// set, cmd.Dir isolates nothing and dirtyFiles would judge a deletion from
// another repository's state. They are REMOVED, not blanked — an empty value
// makes git fail with `not a git repository` on every command.
//
// Exported so that packages whose tests run real git (internal/cli in
// particular) neutralize the SAME set as production code.
var RedirectingVars = []string{
	"GIT_DIR",
	"GIT_COMMON_DIR",
	"GIT_WORK_TREE",
	"GIT_INDEX_FILE",
	"GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES",
	"GIT_NAMESPACE",
}

// NeutralizeGitEnvironment makes the TEST PROCESS environment hermetic to the
// machine's git configuration and to the variables that redirect git to another
// repository (RedirectingVars).
//
// TestMain ONLY: it mutates the whole process environment, not one test's. It
// lives in a production file, not a _test.go, so that another package's
// TestMain can import it — _test.go symbols are only visible in their own
// package. Same reason sbx.Fake lives in its production package.
func NeutralizeGitEnvironment() {
	os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	os.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	// Neutralizing both files is not enough: git also accepts configuration
	// through the environment.
	os.Unsetenv("GIT_CONFIG_COUNT")
	os.Unsetenv("GIT_CONFIG_PARAMETERS")
	for _, v := range RedirectingVars {
		os.Unsetenv(v)
	}
}

// neutralEnvironment returns the current environment stripped of the variables
// that would divert git to a repository other than the requested one.
func neutralEnvironment() []string {
	raw := os.Environ()
	clean := make([]string, 0, len(raw))
	for _, v := range raw {
		name, _, _ := strings.Cut(v, "=")
		if slices.Contains(RedirectingVars, name) {
			continue
		}
		clean = append(clean, v)
	}
	return clean
}

func (g gitExec) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return g.RunWithInput(ctx, dir, nil, args...)
}

func (gitExec) RunWithInput(ctx context.Context, dir string, input []byte, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = neutralEnvironment()
	// Stdin left nil (hence /dev/null) when there is nothing to write: a
	// subcommand reading standard input would otherwise block forever.
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// The CONTEXT is den's, the DETAIL stays git's, verbatim: translating
		// would mean recognizing git's messages — an open set that changes every
		// release — and an approximate message is worth less than the exact one,
		// searchable as is. Where git's wording would be the ONLY thing the user
		// reads, den names the outcome itself (see Ensure and Remove).
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return stdout.Bytes(), fmt.Errorf("git %s (in %s): %s", strings.Join(args, " "), dir, detail)
	}
	return stdout.Bytes(), nil
}

// Path computes where the worktree wt of a repo lives, per the layout
// (spec §13.5).
//
//	central  : <root>/<wt>/<repo>     — default; all worktrees of one wt sit
//	                                    side by side, which makes multi-repo
//	                                    co-mounting readable
//	per-repo : <repo>/.den/<wt>       — for those who prefer worktrees next to
//	                                    their repository
func Path(layout, root, wt, repoPath string) string {
	if layout == "per-repo" {
		return filepath.Join(repoPath, ".den", wt)
	}
	return filepath.Join(root, wt, filepath.Base(repoPath))
}

// Orphan is a worktree directory den created that the caller's list of repos
// does not account for — typically the worktree of a repo passed on the command
// line, deliberately absent from the sandbox identity (spec §4.3), or one whose
// entry was deleted from the nest's `repos:` since the spawn.
type Orphan struct {
	Dir      string // <root>/<wt>/<name>
	RepoPath string // the repository, recovered from Dir's own .git
}

// Orphans enumerates the worktree directories of wt under root that `known`
// does not account for, recovering each one's repository from its own `.git`.
// It is the INVERSE of Path, and the reason den can clean up what it never
// stored: den keeps no state beyond the sandbox name, but the directory itself
// remembers which repository it belongs to.
//
// CENTRAL LAYOUT ONLY. In per-repo, Path puts the worktree at <repo>/.den/<wt>:
// without the repo path there is no directory to list, so there is nothing to
// enumerate — the caller warns instead.
//
// Three returns, because three outcomes are genuinely different:
//   - the entries den may remove;
//   - one error per entry deliberately SKIPPED. Not a failure: an entry den
//     cannot vouch for is left alone, and the caller warns. Removing on a
//     guess would be worse than the leftover this exists to clear;
//   - a hard error, only when the enumeration ITSELF fails. An absent
//     <root>/<wt> is the nominal "no worktree" case, not a failure.
func Orphans(ctx context.Context, g Git, root, wt string, known []string) ([]Orphan, []error, error) {
	// Path("central", root, "", repo) is <root>/<repo>, so an empty wt would
	// enumerate worktree_root ITSELF and offer the worktrees of every other
	// nest for removal. Same refusal as removeParentDir's, for the same reason.
	if wt == "" {
		return nil, nil, nil
	}

	parent := filepath.Join(root, wt)
	entries, err := os.ReadDir(parent)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil // no worktree directory: nothing to recover
		}
		return nil, nil, fmt.Errorf("listing %s: %w", parent, err)
	}

	var found []Orphan
	var skipped []error
	// os.ReadDir returns its entries SORTED, which is what keeps the caller's
	// output — and its goldens — stable from one run to the next.
	for _, e := range entries {
		dir := filepath.Join(parent, e.Name())
		if accountedFor(root, wt, dir, known) {
			continue // the caller already handles this one: silent, not skipped
		}
		orphan, reason := recoverOrphan(ctx, g, root, wt, dir, e.IsDir())
		if reason != nil {
			skipped = append(skipped, reason)
			continue
		}
		found = append(found, orphan)
	}
	return found, skipped, nil
}

// accountedFor asks the question through Path — the function that PLACED the
// directory — rather than by comparing basenames: the comparison is then exact
// by construction, and it keeps following Path if the layout ever changes.
func accountedFor(root, wt, dir string, known []string) bool {
	for _, repo := range known {
		if samePath(Path("central", root, wt, repo), dir) {
			return true
		}
	}
	return false
}

// recoverOrphan decides whether dir is a worktree den may remove, and of which
// repository. It returns a REASON, never an error to abort on.
//
// Deriving the repository FROM the directory makes checkOwnership a tautology:
// we would ask git whose the directory is, then assert that it is theirs. The
// guard that makes "den only removes directories it placed itself" true has to
// be rebuilt here, out of the checks below — each one closes a case where the
// tautology would have said yes.
func recoverOrphan(ctx context.Context, g Git, root, wt, dir string, isDir bool) (Orphan, error) {
	if !isDir {
		return Orphan{}, fmt.Errorf("%s is not a directory: left in place", dir)
	}

	wtRoot, common, err := identify(ctx, g, dir)
	if err != nil {
		return Orphan{}, fmt.Errorf(
			"%s is not a git worktree, or its repository is gone: left in place (%v)", dir, err)
	}

	// git answers for the first repository found walking UP: without this, a
	// plain directory the user parked under worktree_root would pass for a
	// worktree of whatever repository happens to contain worktree_root.
	if !samePath(wtRoot, dir) {
		return Orphan{}, fmt.Errorf(
			"%s is not the root of a git worktree — git answers for %s: left in place", dir, wtRoot)
	}

	repoPath := repoDir(common)

	// dir IS the main worktree of its own repository: someone cloned a repo
	// under worktree_root. Sending it to the trash would be far worse than the
	// leftover this enumeration exists to clear.
	if samePath(repoPath, dir) {
		return Orphan{}, fmt.Errorf(
			"%s is a repository itself, not a worktree den created: left in place", dir)
	}

	// Remove RECOMPUTES the path from RepoPath (see Target) — deliberately, so
	// that no caller can send it anywhere. When the repo is ITSELF a linked
	// worktree, repoDir walks up to the MAIN worktree, whose basename may
	// differ from the directory we just enumerated. Remove would then stat a
	// path that does not exist, take the "already gone" branch, and report
	// success while the directory stays on disk. Say so instead of no-oping.
	if !samePath(Path("central", root, wt, repoPath), dir) {
		return Orphan{}, fmt.Errorf(
			"%s belongs to %s, whose directory name differs — den would clean up elsewhere: "+
				"remove %s by hand", dir, repoPath, dir)
	}

	// The strongest available proof that this path really is a worktree OF that
	// repository, and not a directory that merely looks like one.
	registered, _, err := worktreeEntry(ctx, g, repoPath, dir)
	if err != nil {
		return Orphan{}, fmt.Errorf("%s left in place: %v", dir, err)
	}
	if !registered {
		return Orphan{}, fmt.Errorf(
			"%s is not a registered worktree of %s: left in place", dir, repoPath)
	}

	return Orphan{Dir: dir, RepoPath: repoPath}, nil
}

// Name carries the TWO names of a worktree, which do not always coincide.
//
// Dir is the flattened component (config.FlattenSandboxComponent): it names the
// worktree directory AND the sandbox name suffix. It MUST stay a flat path
// component — den keeps no state beyond the sandbox name, and `den rm` recovers
// the directory from that name alone, through Path.
//
// Branch is the real git branch name, as the user typed it: it is what shows up
// in `git log`, in the forge and in the PR, so flattening it would rename their
// work.
type Name struct {
	Dir    string
	Branch string
}

// Ensure guarantees the existence of worktree wt for this repo and returns its
// path. Idempotent: an existing worktree ON THE RIGHT BRANCH and BELONGING TO
// THIS REPO is left alone.
//
// An existing worktree on ANOTHER branch is an error, never a silent checkout:
// switching the branch of a worktree the user is working in would move their
// work unasked. That check also covers the flattening collision: "feat/try" and
// "feat-try" aim at the same directory, and only the branch comparison tells
// them apart.
func Ensure(ctx context.Context, g Git, layout, root string, wt Name, repoPath string) (string, error) {
	if err := checkRepo(repoPath); err != nil {
		return "", err
	}

	worktreePath := Path(layout, root, wt.Dir, repoPath)

	// In per-repo the worktree is born INSIDE the repository: without an
	// exclusion it leaves a permanent "?? .den/" in the user's git status.
	if layout == "per-repo" {
		if err := excludeDenDir(ctx, g, repoPath); err != nil {
			return "", err
		}
	}

	if _, err := os.Stat(worktreePath); err == nil {
		if err := checkOwnership(ctx, g, worktreePath, repoPath); err != nil {
			return "", err
		}
		current, err := currentBranch(ctx, g, worktreePath)
		if err != nil {
			return "", fmt.Errorf(
				"%s already exists but is not a usable git worktree: %w", worktreePath, err)
		}
		if current != wt.Branch {
			return "", fmt.Errorf(
				"worktree %s is on branch %q, not %q — pick another worktree name "+
					"or switch that directory to %q by hand", worktreePath, current, wt.Branch, wt.Branch)
		}
		return worktreePath, nil // already in place: idempotent
	}

	// Directory gone but the registration still alive: git would refuse with a
	// "fatal: ... missing but already registered worktree" and nothing of den
	// would come out of it. We name the outcome ourselves.
	registered, _, err := worktreeEntry(ctx, g, repoPath, worktreePath)
	if err != nil {
		return "", err
	}
	if registered {
		return "", fmt.Errorf(
			"worktree %s is still registered in %s but its directory is gone — "+
				"run `den rm` on this nest to clear the registration, then spawn again",
			worktreePath, repoPath)
	}

	// `git worktree add <path> <branch>` if the branch already exists,
	// `-b <branch>` otherwise: git refuses to recreate an existing branch.
	args := []string{"worktree", "add", worktreePath, wt.Branch}
	if !branchExists(ctx, g, repoPath, wt.Branch) {
		// A HEAD that does not resolve gives the new branch no start point. The
		// check lives HERE and not higher up because it is the only branch such a
		// repository can reach — the requested branch cannot exist in that state,
		// so branchExists is necessarily false — and putting it up front would
		// charge a `rev-parse` to the idempotent path.
		//
		// Without it git answers exactly from its point of view and unactionably
		// from ours, on a virgin repository:
		//
		//	No possible source branch, inferring '--orphan'
		//	fatal: options '--orphan' and '--track' cannot be used together
		//
		// The message states what the probe MEASURES — HEAD does not resolve —
		// and not what we would like to infer: "the repository has no commit" is
		// false on an orphan branch, where commits exist and only HEAD is at
		// fault. Both possible causes are named, without picking one.
		if !headResolvable(ctx, g, repoPath) {
			return "", fmt.Errorf(
				"creating worktree %q: HEAD of %s points at no commit (empty repository, "+
					"or an orphan branch with nothing committed yet) — git has no start point "+
					"to give the new branch; commit on this repository first, then retry",
				wt.Branch, repoPath)
		}
		// --no-track: the start point is a tracking ref, and without it git would
		// make the work branch track origin/<default> — `git push` would then fail
		// by offering to push onto the default branch.
		args = []string{"worktree", "add", "--no-track", "-b", wt.Branch, worktreePath}
		// Spec §13.4-3: the branch starts from the repo's default branch. Fall
		// back to the current HEAD when the repository has no origin/HEAD — a
		// purely local repository is perfectly legitimate, and that is then the
		// only start point we can name.
		if startPoint, ok := defaultBranch(ctx, g, repoPath); ok {
			args = append(args, startPoint)
		}
	}
	if _, err := g.Run(ctx, repoPath, args...); err != nil {
		return "", fmt.Errorf("creating worktree %q of %s: %w", wt.Branch, repoPath, err)
	}
	return worktreePath, nil
}

// Target designates the worktree to remove. Remove DERIVES the path through
// Path rather than receiving it: the "does this directory really belong to this
// repo?" guard would otherwise rest entirely on the caller, and den has no
// reason to remove a directory it did not place itself.
type Target struct {
	DenHome string // den root; the trash lives under <DenHome>/trash
	Layout  string // "central" or "per-repo" (spec §13.5)
	Root    string // worktree_root
	Nest    string // readable nest identity ("api", "api.feat12"): names the trash entry
	// Worktree is the worktree's DIRECTORY name (Name.Dir), not its branch:
	// `den rm` only has the sandbox name, from which it can only derive the
	// flattened component. The branch is not needed here — den never deletes it,
	// it survives in the repository.
	Worktree string
	RepoPath string
	Force    bool
}

// Remove moves the worktree to the trash and returns the path of the entry it
// created — or "" if the directory was already gone. Refuses if the tree is
// dirty and Force is false (spec §14).
//
// den never DELETES, it moves. The enumeration of the ways `git status` hides
// work does not converge — git adds a cache mechanism per release (untracked
// cache in 2.8, fsmonitor hook in 2.16, `core.fsmonitor=true` in 2.37) — and
// the `git worktree remove` safety net falls WITH `status`, since it is the
// same code. There is no second net.
//
// The trash therefore changes the NATURE of the problem: whatever the verdict
// misses — future cache mechanisms, and the accepted blind spot of ignore rules
// (see dirtyFiles) — goes from "data loss" to "a directory the user brings back
// with one `mv`". dirtyFiles survives as ERGONOMICS: warn before acting, no
// longer protect.
//
// What the trash does NOT give back: the moved worktree keeps a `.git` file
// pointing at a now-pruned registration. Files are recovered, not a working
// worktree. Commits were never at stake — the branch survives in the repository.
func Remove(ctx context.Context, g Git, c Target) (string, error) {
	repoPath, worktreePath, force := c.RepoPath, Path(c.Layout, c.Root, c.Worktree, c.RepoPath), c.Force

	// BEFORE anything else: with nowhere to put the directory, den does nothing
	// at all. An earlier version refused only after the dirtiness check, and then
	// pointed the user at a relative path that designates nothing.
	if c.DenHome == "" {
		return "", fmt.Errorf(
			"removing worktree %s: no den_home given — den does not delete worktrees, "+
				"it moves them, so it needs somewhere to put them", worktreePath)
	}

	if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
		// The directory is gone (user's rm -rf, interrupted `add`) but the git
		// registration outlives it and would block any later Ensure. Returning nil
		// without doing anything would be pretending we cleaned up.
		if _, err := g.Run(ctx, repoPath, "worktree", "prune"); err != nil {
			return "", fmt.Errorf("pruning worktree registrations of %s: %w", repoPath, err)
		}
		// `prune` SILENTLY skips locked worktrees (rc=0, no output) — and `git
		// worktree lock` exists precisely for removable volumes and network
		// mounts, hence for the case where the directory legitimately disappears.
		// Without this re-check den would return nil claiming to have cleaned up,
		// and Ensure would later send the user back to `den rm` — the command that
		// just told them it succeeded.
		registered, _, err := worktreeEntry(ctx, g, repoPath, worktreePath)
		if err != nil {
			return "", err
		}
		if registered {
			return "", fmt.Errorf(
				"the registration of worktree %s survives in %s while its directory is gone — "+
					"it is probably locked: run `git worktree unlock %s` in %s, then retry",
				worktreePath, repoPath, worktreePath, repoPath)
		}
		removeParentDir(c, worktreePath)
		return "", nil
	}

	if err := checkOwnership(ctx, g, worktreePath, repoPath); err != nil {
		return "", err
	}

	// The lock is checked BEFORE moving. `git worktree lock` exists for removable
	// volumes and network mounts, and `prune` SILENTLY skips locked worktrees
	// (rc=0, no output): moving first would leave both a directory in the trash
	// AND a live registration blocking every later Ensure, with nothing saying so.
	_, locked, err := worktreeEntry(ctx, g, repoPath, worktreePath)
	if err != nil {
		return "", err
	}
	if locked {
		return "", fmt.Errorf(
			"worktree %s is locked in %s — run `git worktree unlock %s` in %s, then retry",
			worktreePath, repoPath, worktreePath, repoPath)
	}

	if !force {
		dirty, err := dirtyFiles(ctx, g, worktreePath)
		if err != nil {
			return "", err
		}
		if len(dirty) > 0 {
			return "", fmt.Errorf(
				"worktree %s holds uncommitted changes (%s) — commit them, or retry "+
					"with --force to send it to the trash %s, or with --keep-worktrees "+
					"to leave the directory in place",
				worktreePath, shortList(dirty), primaryTrash(c))
		}
	}

	dest, err := moveToTrash(c, worktreePath)
	if err != nil {
		return "", err
	}

	// The directory moved, so the registration became prunable. Without this
	// prune, Ensure would forever refuse to recreate this worktree.
	//
	// `prune` is repository-GLOBAL: it also clears stale registrations of other
	// nests targeting the same repo. Accepted degenerate case — what it clears is
	// by definition a registration whose directory is already gone, so no work is
	// at stake, and the other nest would re-spawn anyway. git offers no targeted
	// pruning.
	if _, err := g.Run(ctx, repoPath, "worktree", "prune"); err != nil {
		// The move itself did happen: staying silent would send the user looking
		// for their work where it no longer is.
		return dest, fmt.Errorf(
			"worktree %s is in the trash (%s) but its registration could not be "+
				"pruned in %s: %w", worktreePath, dest, repoPath, err)
	}

	removeParentDir(c, worktreePath)
	purgeTrash(filepath.Dir(dest), time.Now())
	return dest, nil
}

// removeParentDir removes the directory that CARRIED the worktree, if it became
// empty:
//
//	central  : <root>/<wt>     — shared by all repos of the nest
//	per-repo : <repo>/.den
//
// os.Remove and not RemoveAll: the ENOTEMPTY refusal is the mechanism, not an
// accident to work around. It keeps the directory while another repo of the same
// nest still has its worktree there, and it also keeps whatever den did not put
// there: a user file, or the fallback trash `<repo>/.den/.trash` when a
// cross-filesystem rename had to fall back to it.
//
// The error is ignored on purpose: the worktree IS removed, and failing
// `den rm` over a stubborn empty directory would pass a cosmetic detail off as a
// deletion failure.
//
// The refusal on an empty Worktree is not superstition: Remove is exported, and
// Path("central", root, "", repo) yields `<root>/<repo>`, whose parent directory
// is worktree_root ITSELF — a call without a worktree name would erase the
// user's root if it happened to be empty.
func removeParentDir(c Target, worktreePath string) {
	if c.Worktree == "" {
		return
	}
	_ = os.Remove(filepath.Dir(worktreePath))
}

// primaryTrash is the nominal trash location.
func primaryTrash(c Target) string { return filepath.Join(c.DenHome, "trash") }

// fallbackTrash designates a location that NECESSARILY shares the worktree's
// filesystem, since it is the directory carrying it:
//
//	central  : <worktree_root>/.trash
//	per-repo : <repo>/.den/.trash    — already excluded by excludeDenDir
//
// The leading dot is not cosmetic: without it `<worktree_root>/trash` would
// collide with the worktree of a nest spawned with `-w trash`, whereas git
// refuses any ref component starting with a dot, so ".trash" cannot be a
// worktree name.
func fallbackTrash(c Target) string {
	if c.Layout == "per-repo" {
		return filepath.Join(c.RepoPath, ".den", ".trash")
	}
	return filepath.Join(c.Root, ".trash")
}

// moveToTrash moves the worktree and returns the path of the entry created.
//
// The EXDEV fallback is not a convenience: den_home and worktree_root are two
// independent settings, and nothing forces a worktree_root on a fast disk to
// share ~/.den's filesystem. Copying byte by byte would be slow on a large
// worktree and interruptible — mid-way the user would hold two half-copies. The
// fallback stays a rename: atomic or nothing.
func moveToTrash(c Target, worktreePath string) (string, error) {
	base := trashEntryName(time.Now(), c.Nest, c.RepoPath)

	dest, err := moveTo(primaryTrash(c), base, worktreePath)
	if err == nil {
		return dest, nil
	}
	if !errors.Is(err, syscall.EXDEV) {
		return "", err
	}
	fallback, fallbackErr := moveTo(fallbackTrash(c), base, worktreePath)
	if fallbackErr != nil {
		return "", fmt.Errorf("%w; the fallback under %s failed too: %v",
			err, fallbackTrash(c), fallbackErr)
	}
	return fallback, nil
}

func moveTo(trashDir, base, src string) (string, error) {
	if err := os.MkdirAll(trashDir, 0o755); err != nil {
		return "", fmt.Errorf("creating trash %s: %w", trashDir, err)
	}
	dest, err := freeEntry(trashDir, base)
	if err != nil {
		return "", err
	}
	if err := os.Rename(src, dest); err != nil {
		return "", fmt.Errorf("moving %s to %s: %w", src, dest, err)
	}
	return dest, nil
}

// trashEntryName names an entry "<timestamp>-<nest>-<repo>": without the nest
// and the repo, a trash holding several entries is an anonymous pile in which
// the user cannot find THEIR directory.
func trashEntryName(when time.Time, nest, repoPath string) string {
	return fmt.Sprintf("%s-%s-%s",
		when.Format(timestampFormat), safeComponent(nest), safeComponent(filepath.Base(repoPath)))
}

// safeComponent returns a string usable as a directory-name component. The nest
// name is validated elsewhere, but the repo basename comes from a configuration
// path, where ".." would designate the trash's parent.
func safeComponent(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '/' || r == filepath.Separator || r < ' ' {
			return '_'
		}
		return r
	}, s)
	if s == "" || s == "." || s == ".." {
		return "_"
	}
	return s
}

// timestampFormat prefixes trash entries. Sorted lexically it is sorted
// chronologically, which makes an `ls` of the trash readable.
const timestampFormat = "20060102-150405"

// trashRetention is the age beyond which an entry is purged.
//
// Thirty days, and the purge runs on every successful move to the trash rather
// than from a dedicated command: a trash nobody empties is a silent disk leak,
// and a `den gc` one must remember to run would only be run by those who do not
// need it. A purge that ran unasked would itself be a spontaneous deletion —
// exactly what the trash exists to remove. Accepted consequence: whoever never
// runs `den rm` again never purges.
const trashRetention = 30 * 24 * time.Hour

// freeEntry returns an unoccupied entry path under trashDir: two worktrees of
// the same nest and the same repo trashed in the SAME SECOND would otherwise
// carry the same name, and os.Rename would overwrite or fail.
//
// KNOWN TOCTOU, not closed: between this check and the caller's os.Rename a
// third party can create the entry, and `rename(2)` then SILENTLY overwrites a
// destination that is an EMPTY directory. Closing it would need
// `renameat2(RENAME_NOREPLACE)`, absent from the stdlib and from macOS; the
// window is microseconds wide and its only possible actor is a second den run on
// the same nest in the same second.
func freeEntry(trashDir, base string) (string, error) {
	const maxAttempts = 1000
	for i := 0; i < maxAttempts; i++ {
		candidate := filepath.Join(trashDir, base)
		if i > 0 {
			candidate = filepath.Join(trashDir, fmt.Sprintf("%s-%d", base, i+1))
		}
		// Lstat and not Stat: a trash entry that is a broken symlink occupies the
		// name just as much.
		_, err := os.Lstat(candidate)
		if os.IsNotExist(err) {
			return candidate, nil
		}
		if err != nil {
			return "", fmt.Errorf("inspecting trash entry %s: %w", candidate, err)
		}
	}
	return "", fmt.Errorf(
		"trash %s: %d entries already carry the name %q", trashDir, maxAttempts, base)
}

// purgeTrash deletes the entries whose retention has expired.
//
// The date read is the one in the NAME, written by den when it moved the
// directory, and not the directory's mtime: a worktree whose files are six
// months old would otherwise be purged the very day it was trashed. An entry
// whose name carries no readable timestamp is NEVER deleted — the user is
// allowed to store something there, and a purge is itself a deletion.
//
// Errors are ignored entry by entry: the worktree is already safe at this point,
// and failing on housekeeping would hand the caller an error for an operation
// that succeeded.
func purgeTrash(trashDir string, now time.Time) {
	entries, err := os.ReadDir(trashDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) <= len(timestampFormat) || name[len(timestampFormat)] != '-' {
			continue
		}
		when, err := time.ParseInLocation(timestampFormat, name[:len(timestampFormat)], time.Local)
		if err != nil {
			continue
		}
		if now.Sub(when) <= trashRetention {
			continue
		}
		_ = os.RemoveAll(filepath.Join(trashDir, name))
	}
}

// checkRepo distinguishes absence from denied access: diagnosing "not found" on
// an EACCES would send the user chasing the wrong problem.
func checkRepo(repoPath string) error {
	if _, err := os.Stat(repoPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("repo not found: %s: %w", repoPath, err)
		}
		return fmt.Errorf("repo not accessible: %s: %w", repoPath, err)
	}
	return nil
}

// checkOwnership answers the only question that matters before trusting a
// directory: "is this really the root of a worktree OF THIS repo?".
//
// Two traps it closes at once:
//   - git answers for the first repository found WALKING UP, so an empty
//     directory under a repository (the systematic per-repo layout case) would
//     pass for a valid worktree;
//   - worktree_root is global and Path only keeps the repo basename, so two
//     nests targeting same-named repos land on the same directory and the second
//     would walk away with the first one's worktree.
//
// It does not — and CANNOT — close the case of two same-named repos that are the
// SAME repository (a clone and one of its worktrees, two paths to one
// `--git-common-dir`): the discriminator IS the common git directory, so it
// rightly declares them identical.
func checkOwnership(ctx context.Context, g Git, worktreePath, repoPath string) error {
	root, common, err := identify(ctx, g, worktreePath)
	if err != nil {
		return fmt.Errorf("%s already exists but is not a usable git worktree: %w", worktreePath, err)
	}
	if !samePath(root, worktreePath) {
		return fmt.Errorf(
			"%s already exists but is not the root of a git worktree — git answers for %s; "+
				"pick another worktree name or remove that directory", worktreePath, root)
	}
	repoCommon, err := commonDirOf(ctx, g, repoPath)
	if err != nil {
		return fmt.Errorf("identifying repository %s: %w", repoPath, err)
	}
	if !samePath(common, repoCommon) {
		return fmt.Errorf(
			"worktree %s belongs to repository %s, not to %s — two nests probably target the "+
				"same worktree_root with repos of the same name; pick another worktree name "+
				"or a distinct worktree_root", worktreePath, repoDir(common), repoPath)
	}
	return nil
}

// identify returns the root of the worktree containing worktreePath and the
// common git directory of the repository it belongs to.
func identify(ctx context.Context, g Git, worktreePath string) (root, common string, err error) {
	out, err := g.Run(ctx, worktreePath, "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-common-dir")
	if err != nil {
		return "", "", err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return "", "", fmt.Errorf("unexpected answer from git rev-parse: %q", string(out))
	}
	return strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1]), nil
}

// CommonGitDir returns a repository's common git directory — its `.git`, the one
// holding the admin dir, the objects and the refs.
//
// That is what `den <nest> -w` mounts in the microVM NEXT TO the worktree: a
// linked worktree's `.git` is only a file "gitdir: <repo>/.git/worktrees/<name>",
// and without that directory mounted every git command in the VM answers
// "fatal: not a git repository".
//
// The question goes through git rather than a `filepath.Join(repo, ".git")`
// because a nest's repo may itself be a linked worktree, where the join would
// yield the pointer file, which carries neither objects nor refs.
func CommonGitDir(ctx context.Context, g Git, repoPath string) (string, error) {
	common, err := commonDirOf(ctx, g, repoPath)
	if err != nil {
		return "", fmt.Errorf("identifying the git directory of %s: %w", repoPath, err)
	}
	return common, nil
}

func commonDirOf(ctx context.Context, g Git, dir string) (string, error) {
	out, err := g.Run(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// repoDir walks up from the common git directory (<repo>/.git) to the repository
// itself, so messages name what the user recognizes.
func repoDir(common string) string {
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common)
	}
	return common
}

// samePath compares two paths, resolving symlinks when possible: git returns the
// resolved path where den handles the one it was given.
func samePath(a, b string) bool {
	return resolvePath(a) == resolvePath(b)
}

// resolvePath returns the canonical form of a path, including when it NO LONGER
// EXISTS.
//
// That is the deciding case, not an edge one: the dead-end guards (stale
// registration, locked worktree) only run once the directory is gone.
// EvalSymlinks then fails on both sides of the comparison, which would fall back
// to two raw strings — the one den handles, reached through a symlink, and the
// one git recorded, resolved. On macOS $TMPDIR and worktree_root live under
// /var → private/var, so this is not Linux-only trivia.
//
// So we resolve the longest ancestor that still exists and reattach the rest of
// the path to it.
func resolvePath(p string) string {
	p = filepath.Clean(p)
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(resolved)
	}
	rest := ""
	for current := p; ; {
		parent := filepath.Dir(current)
		if parent == current {
			return p // walked up to the root without resolving anything
		}
		rest = filepath.Join(filepath.Base(current), rest)
		if resolved, err := filepath.EvalSymlinks(parent); err == nil {
			return filepath.Clean(filepath.Join(resolved, rest))
		}
		current = parent
	}
}

// worktreeEntry says what git knows about the worktree registered at this path:
// whether it is still registered (whether or not its directory exists) and
// whether it is locked.
//
// One question, one listing. `worktree list --porcelain` emits one block per
// worktree, terminated by a blank line; "locked" (alone, or followed by the
// reason) belongs to the block opened by the preceding "worktree <path>" line,
// hence the inBlock latch.
//
// The error propagates in both directions, and that is the point:
//   - swallowing it would make Remove return nil on a vanished directory under a
//     git failing on `worktree list` — claiming to have cleaned up without being
//     able to check anything, which is exactly what the neighbouring re-check
//     exists to prevent;
//   - not knowing whether the worktree is locked means not knowing whether
//     `prune` will do its job, and the only safe direction before a move is
//     abstention.
func worktreeEntry(ctx context.Context, g Git, repoPath, worktreePath string) (registered, locked bool, err error) {
	out, err := g.Run(ctx, repoPath, "worktree", "list", "--porcelain")
	if err != nil {
		return false, false, fmt.Errorf("listing worktrees of %s: %w", repoPath, err)
	}
	inBlock := false
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if path, ok := strings.CutPrefix(line, "worktree "); ok {
			inBlock = samePath(path, worktreePath)
			registered = registered || inBlock
			continue
		}
		if inBlock && (line == "locked" || strings.HasPrefix(line, "locked ")) {
			locked = true
		}
	}
	return registered, locked, nil
}

// denExcludeLine is what gets appended to .git/info/exclude in the per-repo
// layout.
const denExcludeLine = ".den/"

// excludeDenDir keeps the per-repo worktree from durably dirtying the user's
// repository. Idempotent: the line is added only once, and existing content is
// preserved.
func excludeDenDir(ctx context.Context, g Git, repoPath string) error {
	common, err := commonDirOf(ctx, g, repoPath)
	if err != nil {
		return fmt.Errorf("identifying repository %s: %w", repoPath, err)
	}
	file := filepath.Join(common, "info", "exclude")

	content, err := os.ReadFile(file)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading %s: %w", file, err)
	}
	for _, line := range strings.Split(string(content), "\n") {
		if strings.TrimSpace(line) == denExcludeLine {
			return nil // already excluded
		}
	}

	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(file), err)
	}
	addition := denExcludeLine + "\n"
	if len(content) > 0 && !bytes.HasSuffix(content, []byte("\n")) {
		addition = "\n" + addition
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening %s: %w", file, err)
	}
	defer f.Close()
	if _, err := f.WriteString(addition); err != nil {
		return fmt.Errorf("writing %s: %w", file, err)
	}
	return nil
}

func currentBranch(ctx context.Context, g Git, dir string) (string, error) {
	out, err := g.Run(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func branchExists(ctx context.Context, g Git, repoPath, branch string) bool {
	_, err := g.Run(ctx, repoPath, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// headResolvable says whether the repository's HEAD designates an existing
// commit.
//
// Two distinct setups make it false, and the error message resting on it must
// cover BOTH: a virgin `git init`, where HEAD points at a never-committed
// branch; and an ORPHAN branch (`git checkout --orphan`) in a repository that
// does have commits elsewhere. It is named after what it measures — HEAD
// resolution — and not after a property of the repository it does not test.
//
// --quiet so the expected failure does not pollute stderr, --verify so git exits
// non-zero instead of echoing the string back.
func headResolvable(ctx context.Context, g Git, repoPath string) bool {
	_, err := g.Run(ctx, repoPath, "rev-parse", "--verify", "--quiet", "HEAD")
	return err == nil
}

// defaultBranch returns the tracking ref of the repository's default branch
// ("origin/main"), and false when the repository has no origin/HEAD.
func defaultBranch(ctx context.Context, g Git, repoPath string) (string, bool) {
	out, err := g.Run(ctx, repoPath, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		return "", false
	}
	ref := strings.TrimSpace(string(out))
	return ref, ref != ""
}

// dirtyFiles returns the paths that make the worktree dirty.
//
// --ignored=traditional is essential: a .env or a local sqlite database is
// ignored by git, hence invisible to `status --porcelain` AND to the
// `git worktree remove` safety net — yet those are exactly the files one does
// not commit AND cannot get back.
//
// Among ignored entries we set aside the DIRECTORIES THAT ARE THEMSELVES
// IGNORED: git collapses a fully ignored directory to a single entry
// (node_modules/, target/), which is regenerable cache; refusing on those would
// make `den rm` unusable on any JS or Python project. The trailing "/" is NOT
// enough to recognize them — git collapses the same way a directory the user
// never ignored but whose entire content is (`.gitignore` = `*.env`, the most
// common way to ignore secrets, makes `conf/prod.env` come out as `!! conf/`).
// Hence the question asked of git for each entry: is the directory ITSELF
// ignored?
//
// -z rather than `-c core.quotePath=false`: by default git quotes and escapes
// "special" paths, so a cache named `café/` comes out as `"caf\303\251/"`
// — without its trailing "/", hence judged dirty, and shown to the user in
// octal. quotePath=false only lifts non-ASCII escaping; NUL-separated output is
// never quoted.
//
// ACCEPTED BLIND SPOT (S3): a secret placed inside a wholesale-ignored directory
// (`config/` ignored, `config/.env` inside) stays invisible, git not enumerating
// it separately. A hostile — or merely malformed — `core.excludesFile` widens
// the hole at will, since it decides what "really ignored" means.
//
// That is an explicit trade-off, not an oversight: closing it would mean
// refusing on every `node_modules/`, i.e. making `den rm` unusable. The price is
// paid once and for good now that Remove moves instead of deleting: what this
// verdict misses ends up in the trash, not in the void.
func dirtyFiles(ctx context.Context, g Git, dir string) ([]string, error) {
	// --untracked-files=normal is passed EXPLICITLY: den reads git under the
	// user's config, and `status.showUntrackedFiles = no` — a performance setting
	// common on large repositories — would otherwise empty the output of all
	// untracked work, disarming the whole spec §14 guard.
	//
	// core.fsmonitor is neutralized for the same reason, only worse: it delegates
	// "what changed" to a daemon. A Watchman that lost its state, a restarted
	// daemon or a mount where inotify drops events all answer "nothing", and git
	// believes them — the modified file becomes invisible with no index flag
	// betraying it. Worse, den's own call primed that cache and then blinded the
	// `git worktree remove` safety net.
	out, err := g.Run(ctx, dir, "-c", "core.fsmonitor=", "status", "--porcelain",
		"--ignored=traditional", "--untracked-files=normal", "-z")
	if err != nil {
		return nil, fmt.Errorf("status of %s: %w", dir, err)
	}
	// Two passes: read every record first, then ask git ONE single question for
	// all candidate directories. Deciding entry by entry cost one fork per entry
	// (see ignoredDirs).
	type record struct{ status, path string }
	var records []record
	entries := strings.Split(string(out), "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		// -z output ends with a NUL: the last chunk of the split is empty, and it
		// is the only legitimate empty record.
		if e == "" {
			continue
		}
		// SKIPPING an unreadable record would lose a dirty entry silently — the
		// exact fail-open direction the rest of this module refuses. A format den
		// cannot read is a doubt, hence a refusal.
		if len(e) < 4 || e[2] != ' ' {
			return nil, fmt.Errorf(
				"status of %s: unreadable record in git status output: %q", dir, e)
		}
		status, entryPath := e[:2], e[3:]
		// Under -z a rename or a copy takes TWO records: the second carries the
		// source path, with no status prefix. We consume it so it is not read back
		// as an entry of its own.
		//
		// Detection must look at BOTH columns: the INDEX (column X, what `git mv`
		// produces) and the WORKING TREE (column Y: " R", "DR", " C", produced by
		// an `mv` followed by `git add -N`). Reading X alone makes the source path
		// of a working-tree rename be parsed as a record of its own, and den then
		// names a file that does not exist.
		if status[0] == 'R' || status[0] == 'C' || status[1] == 'R' || status[1] == 'C' {
			i++
		}
		records = append(records, record{status, entryPath})
	}

	var candidates []string
	for _, e := range records {
		if isCollapsedIgnoredDir(e.status, e.path) {
			candidates = append(candidates, strings.TrimSuffix(e.path, "/"))
		}
	}
	ignored := map[string]bool{}
	if len(candidates) > 0 {
		ignored = ignoredDirs(ctx, g, dir, candidates)
	}

	var dirty []string
	for _, e := range records {
		if isCollapsedIgnoredDir(e.status, e.path) && ignored[strings.TrimSuffix(e.path, "/")] {
			continue
		}
		dirty = append(dirty, e.path)
	}

	marked, err := markedFiles(ctx, g, dir)
	if err != nil {
		return nil, err
	}
	for _, m := range marked {
		if !slices.Contains(dirty, m) {
			dirty = append(dirty, m)
		}
	}
	return dirty, nil
}

// isCollapsedIgnoredDir says whether the record is a collapsed ignored
// directory, hence a candidate for the question ignoredDirs asks.
func isCollapsedIgnoredDir(status, entryPath string) bool {
	return status == "!!" && strings.HasSuffix(entryPath, "/")
}

// nulRecords splits the NUL-separated output of a `-z` git command into its
// records. Empty chunks are dropped: `-z` output ENDS with a NUL, so the last
// chunk of the split is always empty and is the only legitimate empty record.
func nulRecords(out []byte) []string {
	var records []string
	for _, e := range strings.Split(string(out), "\x00") {
		if e != "" {
			records = append(records, e)
		}
	}
	return records
}

// ignoredDirs returns, among the given directories, those covered THEMSELVES by
// an ignore rule — as opposed to a merely untracked directory whose content
// happens to be ignored.
//
// The caller strips the trailing "/", and that is not cosmetic: in git's
// wildmatch `*` and `**` match the empty string, and git applies the directory's
// own `.gitignore` to the empty component following the "/". Asking for `conf/`
// would therefore answer "ignored" as soon as the .gitignore holds `conf/*`,
// `conf/**`, or a nested `.gitignore` of `*` — three common idioms — while the
// user never made `conf/` disposable.
//
// ONE single call for the whole batch: deciding entry by entry costs one fork
// per entry, linear in the number of directories and unbounded. `-z` is forced
// by quoting (without it `café/` comes out in octal and den no longer recognizes
// it), and `--stdin` is forced by `-z`, which git refuses on an argv.
//
// On doubt the table is empty and every entry stays dirty, so the move to the
// trash is refused. The rc=1 of "none matched" falls in the same branch and
// gives the same answer — neither designates a disposable directory.
func ignoredDirs(ctx context.Context, g Git, dir string, paths []string) map[string]bool {
	var input strings.Builder
	for _, p := range paths {
		input.WriteString(p)
		input.WriteByte(0)
	}
	ignored := map[string]bool{}
	out, err := g.RunWithInput(ctx, dir, []byte(input.String()), "check-ignore", "-z", "--stdin")
	if err != nil {
		return ignored
	}
	for _, p := range nulRecords(out) {
		ignored[p] = true
	}
	return ignored
}

// markedFiles returns the tracked files the index marks as "do not look":
// skip-worktree (flag `S`) and assume-unchanged (lowercase flag). git then
// reports NO modification about them — neither in `status` nor in the
// `git worktree remove` safety net: both let the file be destroyed silently.
//
// Those bits exist precisely to carry local modifications one does not want to
// commit. But their presence alone does not conclude: `core.ignoreStat` sets
// them on the WHOLE repository, and sparse-checkout on everything outside the
// cone. So only files whose content really differs from the index are kept —
// see modifiedPaths.
func markedFiles(ctx context.Context, g Git, dir string) ([]string, error) {
	out, err := g.Run(ctx, dir, "ls-files", "-v", "-z")
	if err != nil {
		return nil, fmt.Errorf("index flags of %s: %w", dir, err)
	}
	var dirty, regulars, symlinks []string
	for _, e := range nulRecords(out) {
		if len(e) < 3 || e[1] != ' ' {
			return nil, fmt.Errorf(
				"index flags of %s: unreadable record in git ls-files output: %q", dir, e)
		}
		flag, entryPath := e[0], e[2:]
		if flag != 'S' && (flag < 'a' || flag > 'z') {
			continue
		}
		// Lstat and not Stat: Stat follows symlinks, and a tracked file replaced by
		// a broken symlink would vanish from the discriminator.
		info, err := os.Lstat(filepath.Join(dir, entryPath))
		if err != nil {
			// Absent from disk: outside a sparse-checkout cone, which sets the same
			// bits. There is nothing to protect there.
			continue
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			symlinks = append(symlinks, entryPath)
		case info.Mode().IsRegular():
			regulars = append(regulars, entryPath)
		default:
			// Fifo, directory, socket, device: `git hash-object` must NEVER see
			// them. On a fifo it blocks forever (it never returns, it does not
			// fail), on a directory it exits with a `fatal:`. So the verdict stays
			// den's.
			//
			// A SUBMODULE lands here: its gitlink is a directory on disk. Marked, it
			// is therefore refused while `git status` reports nothing. Accepted false
			// positive: `git worktree remove --force` does NOT refuse a worktree with
			// a submodule anyway, and the refusal lifts with --force, which moves
			// instead of destroying. The moved submodule keeps all its files; its
			// `.git` carries a RELATIVE path, so it is no longer a usable repository
			// from the trash. Files are recovered, which is exactly the trash's
			// contract.
			dirty = append(dirty, entryPath)
		}
	}
	if len(regulars)+len(symlinks) == 0 {
		return dirty, nil
	}

	index, err := indexHashes(ctx, g, dir)
	if err != nil {
		return nil, err
	}
	modified, err := modifiedPaths(ctx, g, dir, index, regulars)
	if err != nil {
		return nil, err
	}
	dirty = append(dirty, modified...)
	modified, err = modifiedLinks(dir, index, symlinks)
	if err != nil {
		return nil, err
	}
	return append(dirty, modified...), nil
}

// indexEntry is what the index holds about a path.
type indexEntry struct {
	mode string
	hash string
}

// symlinkMode is git's mode for a symbolic link.
const symlinkMode = "120000"

// isRegularMode says whether the index mode is that of a regular file.
//
// It is the counterpart of the modifiedLinks check on symlinkMode, and the mode
// must be compared or the hash alone lies: a tracked symlink (120000) replaced
// on disk by a regular file whose content equals the link text produces two
// IDENTICAL hashes — git hashes the link text as the blob content — so the file
// would pass for clean.
//
// 100644 and 100755 are deliberately conflated: a bare `chmod +x` on a marked
// file stays invisible to den. That is a permission bit lost, not content, and
// telling them apart would cost one stat per file to call a worktree dirty when
// not a byte moved. The real mode lives in the index and survives the trash.
func isRegularMode(mode string) bool { return mode == "100644" || mode == "100755" }

// modifiedLinks compares a symbolic link on its TEXT.
//
// git stores the link text as the object content (mode 120000), whereas
// `hash-object` FOLLOWS the link and would hash the target's content: the two
// never coincide. Without that distinction a marked symlink is dirty forever,
// however clean the repository.
func modifiedLinks(dir string, index map[string]indexEntry, symlinks []string) ([]string, error) {
	var modified []string
	for _, entryPath := range symlinks {
		fullPath := filepath.Join(dir, entryPath)
		entry, ok := index[entryPath]
		if !ok || entry.mode != symlinkMode {
			// The index does not expect a symlink here: a tracked file was replaced
			// by a symlink, which is indeed a modification.
			modified = append(modified, entryPath)
			continue
		}
		// Readlink comes after a successful Lstat on the SAME path: only a
		// concurrent replacement can make it fail, and that window is not
		// reproducible in a test. The error propagates anyway — not having been
		// able to read the link does not license declaring it clean.
		target, err := os.Readlink(fullPath)
		if err != nil {
			return nil, fmt.Errorf("reading symlink %s: %w", fullPath, err)
		}
		expected, err := blobHash(target, len(entry.hash))
		if err != nil {
			return nil, fmt.Errorf("hashing symlink %s: %w", fullPath, err)
		}
		if expected != entry.hash {
			modified = append(modified, entryPath)
		}
	}
	return modified, nil
}

// blobHash computes the hash git gives to the blob whose content is exactly this
// text: hash("blob <n>\x00<text>").
//
// It serves the TEXT OF A SYMLINK, which git stores as is: no .gitattributes
// filter applies to a symbolic link, and that is what makes the local
// computation legitimate here where it would not be for a regular file (see
// modifiedPaths). It replaces one `cat-file blob` fork per marked link and drops
// the dependency on the object store — a link whose blob is missing (partial
// clone, unfetched promised object) would otherwise fail den forever.
//
// The algorithm is deduced from the LENGTH of the hash the index carries (40 hex
// characters for SHA-1, 64 for SHA-256), which avoids a
// `git rev-parse --show-object-format` — unavailable before git 2.29 — and any
// assumption about the repository's object format. An unknown length is an
// error, not a bet: comparing against the wrong algorithm would report "modified"
// forever.
func blobHash(text string, hashLength int) (string, error) {
	var h hash.Hash
	switch hashLength {
	case 2 * sha1.Size:
		// SHA-1 is git's object format here, not a cryptographic choice.
		h = sha1.New()
	case 2 * sha256.Size:
		h = sha256.New()
	default:
		return "", fmt.Errorf(
			"unknown git object format: the index carries a hash of %d hex characters",
			hashLength)
	}
	fmt.Fprintf(h, "blob %d\x00%s", len(text), text)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// batchSize bounds the argv length of the hash probes.
const batchSize = 256

// modifiedPaths returns, among the files the index declares "do not look", those
// whose on-disk content REALLY differs from the index.
//
// Without that comparison the guard is unusable: `core.ignoreStat = true` sets
// the assume-unchanged bit on the whole repository, and those bits SURVIVE the
// removal of the setting — a perfectly clean worktree would be permanently
// unremovable without --force. Reading `core.ignoreStat` would not help, since
// the block persists precisely once the setting is gone.
//
// No git command re-examines those entries: `diff-files`, `diff-index` and
// `ls-files -m` are all blind to them, so the hashes must be compared here.
// `hash-object` applies `.gitattributes` filters, so a `clean` filter or
// `core.autocrlf` does not fabricate a fake modification. Two accepted
// trade-offs:
//
//   - a `clean` filter that MASKS a secret makes both hashes identical: the file
//     passes for clean and goes. That is exactly the case the trash makes
//     reversible — it goes into a directory, not into the void (see Remove);
//   - a NON-DETERMINISTIC `clean` filter (injecting a timestamp, a counter,
//     randomness) yields a different hash on every call: the file is then dirty
//     forever and `den rm` demands `--force` for good. False positive, hence a
//     safe direction, and git behaves identically without the marking — such a
//     repository never has an empty `git status`.
//
// If a probe fails the error propagates: a mute probe must never authorize a
// destruction.
func modifiedPaths(ctx context.Context, g Git, dir string, index map[string]indexEntry, paths []string) ([]string, error) {
	var modified []string
	for batch := range slices.Chunk(paths, batchSize) {
		onDisk, err := diskHashes(ctx, g, dir, batch)
		if err != nil {
			return nil, err
		}
		if len(onDisk) != len(batch) {
			return nil, fmt.Errorf("hashes of %s: %d values returned for %d files",
				dir, len(onDisk), len(batch))
		}
		for i, entryPath := range batch {
			// No branch for "absent from the index": the zero value the map returns
			// carries an empty mode, which isRegularMode refuses. A path unknown to
			// the index therefore counts as modified through the same check.
			expected := index[entryPath]
			if !isRegularMode(expected.mode) || expected.hash != onDisk[i] {
				modified = append(modified, entryPath)
			}
		}
	}
	return modified, nil
}

// indexHashes reads the WHOLE index in a single call.
//
// Querying `ls-files -s` with a batch's paths rescans the entire index for every
// batch: cost per call linear in index size, number of calls linear in marked
// files — hence quadratic.
//
// The single call also removes a false positive: `ls-files` treats its arguments
// as PATHSPECS and not as literal paths, so a tracked file named ":x.txt" was
// missing from the table and passed for modified on a clean worktree.
func indexHashes(ctx context.Context, g Git, dir string) (map[string]indexEntry, error) {
	out, err := g.Run(ctx, dir, "ls-files", "-s", "-z")
	if err != nil {
		return nil, fmt.Errorf("index hashes of %s: %w", dir, err)
	}
	index := map[string]indexEntry{}
	for _, e := range nulRecords(out) {
		// "<mode> <hash> <stage>\t<path>"
		header, entryPath, ok := strings.Cut(e, "\t")
		fields := strings.Fields(header)
		if !ok || len(fields) < 2 {
			return nil, fmt.Errorf(
				"index hashes of %s: unreadable record in git ls-files output: %q", dir, e)
		}
		index[entryPath] = indexEntry{mode: fields[0], hash: fields[1]}
	}
	return index, nil
}

// diskHashes computes the hash of the content actually present, in the order of
// the requested paths.
//
// `hash-object` READS THE WHOLE CONTENT of each file, and nothing bounds it: the
// cost is the disk's throughput on large files. That is accepted, for want of a
// better question to ask:
//   - the computation cannot move to Go the way the link text one did, because
//     `hash-object` applies .gitattributes filters — without which an autocrlf
//     repository would see all its lines change (see modifiedPaths);
//   - comparing SIZES first would conclude nothing: a `clean` filter or
//     `core.autocrlf` legitimately makes the blob size differ from the disk's, so
//     a different size does not prove modification and an equal one does not
//     prove cleanliness;
//   - memory stays bounded: git streams, and den only receives hashes.
//
// The set concerned is the MARKED files, empty on an ordinary repository. The
// expensive case is `core.ignoreStat = true`, which marks the whole repository.
func diskHashes(ctx context.Context, g Git, dir string, paths []string) ([]string, error) {
	out, err := g.Run(ctx, dir, append([]string{"hash-object", "--"}, paths...)...)
	if err != nil {
		return nil, fmt.Errorf("disk hashes of %s: %w", dir, err)
	}
	return strings.Fields(string(out)), nil
}

// shortList names the offending files without drowning the message.
func shortList(paths []string) string {
	const max = 5
	if len(paths) <= max {
		return strings.Join(paths, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(paths[:max], ", "), len(paths)-max)
}
