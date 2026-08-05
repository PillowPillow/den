// Package spawn orchestrates the full `den spawn` sequence (spec §6).
//
// It lives outside internal/cli on purpose: it's the densest logic in the
// project, and it must be testable without cobra or a tty.
package spawn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/PillowPillow/den/internal/agent"
	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/manifest"
	"github.com/PillowPillow/den/internal/nest"
	"github.com/PillowPillow/den/internal/policy"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/source"
	"github.com/PillowPillow/den/internal/sshagent"
	"github.com/PillowPillow/den/internal/worktree"
)

// Deps injects access to the world, so the whole sequence is testable
// without a microVM.
type Deps struct {
	Sbx    sbx.Runner
	Git    worktree.Git
	Policy policy.Options
	// Freshness parametrizes the §9.1 agent-freshness gate, on the same terms
	// as Policy and for the same reason: its clock is injected so the suite
	// never waits, and it is REQUIRED — a zero value is refused by
	// agent.WaitFreshness rather than filled in, because a gate with no
	// patience reads exactly like a gate that checks nothing.
	Freshness agent.GateOptions
	Out       io.Writer
	// Err carries diagnostics that are not part of the command's output.
	//
	// Spawn prints `warning:` lines on BOTH streams, so the rule that splits
	// them is stated here rather than left to be inferred from a call site: a
	// warning about DEN'S ENVIRONMENT — true of the host before this spawn
	// existed, fixable without it — goes to Err, so the stdout a caller might
	// pipe stays the spawn's own log; a warning ABOUT THE SANDBOX being
	// reported on is part of that log and stays on Out, next to the other
	// lines naming the same sandbox.
	//
	// Hence warnEmptySSHAgent writes here (the host's ssh-agent is den's
	// environment), while reportDrift and reportMissingGitDirs write to Out
	// (both describe the live sandbox they name). `den rm` splits its own two
	// streams on a different axis (cli.cleanWorktrees) — there stdout carries
	// only what actually succeeded — so its rule is not den-wide and does not
	// govern this one.
	//
	// Defaults to io.Discard when unset.
	Err io.Writer
	// In is what the `-i` checklist reads. Injected like every other side
	// effect of this package, so the selection is exercised without a tty
	// (interactive_test.go feeds it a strings.Reader).
	//
	// Only `-i` reads it: every other path of Spawn leaves it untouched, which
	// is why the dozens of hand-built Deps in this package can keep ignoring it.
	In io.Reader
	// IsTTY reports whether In is a terminal, and is the ONE thing about `-i`
	// no test covers — isolated into a one-liner (StdinIsTerminal) precisely so
	// that it, and not the selection logic, is what stays untested.
	//
	// Nil means NO terminal, deliberately: an unwired probe must take `-i`'s
	// clean refusal — which names --only/--without — rather than let the spawn
	// block on a read nobody will answer.
	IsTTY func() bool
	// SSHAgent reports the state of the forwarded SSH agent. Injected, and
	// nil-tolerant: a nil probe (every test that doesn't exercise SSH, plus the
	// wiring double) simply skips the warning rather than reaching for a real
	// ssh-add.
	SSHAgent func() sshagent.Result
	// GOOS names the operating system whose ssh-agent remedy the empty-agent
	// warning should quote; empty means runtime.GOOS. A parameter for the same
	// reason sshagent.FixCommand takes one: read directly, the darwin branch —
	// the only one carrying `--apple-use-keychain` — would be unassertable on the
	// Linux CI where this suite runs, so the message shipped to macOS users would
	// be the one no test ever exercises. Empty-tolerant like Out and Err, so the
	// many hand-built Deps of this package keep describing their own machine.
	GOOS string
	// Now clocks the source-staleness hint (source.Stale): nil SKIPS it
	// entirely, deliberately — the dozens of hand-built Deps in this
	// package's own tests touch no source and must owe nothing to the
	// clock. The wiring site (internal/cli/root.go) sets it to time.Now;
	// tests that DO exercise a source inject a fixed one instead, the same
	// pattern as Policy.Now and Freshness.Now.
	Now func() time.Time
}

// goos is Deps.GOOS with its documented default applied. Empty falls back to
// runtime.GOOS rather than to a hard-coded OS: a Deps built by hand — this
// package has dozens — must keep describing the machine it runs on, so only a
// test that OPTS IN gets another OS's remedy.
func (d Deps) goos() string {
	if d.GOOS == "" {
		return runtime.GOOS
	}
	return d.GOOS
}

// There is no SystemDeps constructor here, on purpose: it would let a call
// site build its own second sbx.Runner, defeating the single shared Sbx
// that cli.Deps enforces between `den ls` and spawn. Deps stays a plain
// struct with exported fields; the caller wires it explicitly.

// Options carries the flags of `den spawn`.
type Options struct {
	Nest     string
	Worktree string
	Agent    string
	Without  []string
	Only     []string
	Detach   bool
	// Interactive is `-i`: pick the nest's optional repos from a checklist
	// instead of naming them on the command line.
	Interactive bool
	// Repos are the repositories given as positionals: `den spawn <nest> ~/dev/a`.
	// Raw — tilde unexpanded, possibly relative; nest.Resolve normalizes them.
	// Additive to the nest's `repos:`, and placed AHEAD of them, so the first
	// one becomes the directory the attached shell starts in.
	Repos []string
}

// Spawn runs the spec §6 sequence in order: resolve → select repos →
// worktrees → agent profile → mixin → sbx create (or attach if the
// sandbox is already live) → settle-loop → attach.
//
// The settle-loop runs BEFORE attach: attaching before the policy is in
// place would be the half-working state spec §7 forbids. Likewise,
// anything rejectable from config alone is checked before the first side
// effect, so an invalid sandbox name never leaves an orphaned worktree
// behind.
func Spawn(ctx context.Context, denHome string, o Options, d Deps) error {
	// A nil Out must not panic mid-sequence: by the first Fprintf the
	// caller already has a sandbox created and started behind them. Losing
	// the log is cheaper than that.
	if d.Out == nil {
		d.Out = io.Discard
	}
	// Err defaults like Out: the empty-agent warning is best-effort and must
	// never panic a spawn that left stderr unset.
	if d.Err == nil {
		d.Err = io.Discard
	}

	// 0. A contradiction on the command line, refused before anything is read:
	// `-i` and `--only`/`--without` are two answers to the same question.
	//
	// Refusing is the only one of the three possible readings a user cannot
	// misinterpret — taking the flags as the checklist's initial state, or
	// letting them win and ignoring `-i`, both leave someone convinced they
	// selected something they did not. The repo already refuses rather than
	// normalizing in silence (spec §2).
	if o.Interactive {
		if conflicting := selectionFlagsInPlay(o); conflicting != "" {
			return fmt.Errorf(
				"-i and %s both select repos, and they contradict each other — drop one: "+
					"%s is the non-interactive form of the checklist", conflicting, conflicting)
		}
	}

	// 1. Resolve the cascade.
	g, err := config.LoadGlobal(denHome)
	if err != nil {
		return err
	}
	// o.Nest may be a source reference ("corp:backend"): Locate is the SOLE
	// place that turns it into a root to load the nest from, and refuses
	// here — before anything else is read — when the source isn't
	// installed, naming `den source add` as the fix.
	nestRoot, srcName, bareNest, err := source.Locate(denHome, o.Nest)
	if err != nil {
		return err
	}
	n, err := nest.LoadNest(nestRoot, bareNest)
	if err != nil {
		return err
	}

	// Stack origin. `n.Stack` is a REFERENCE — bare inside a source,
	// optionally prefixed for a local nest, and for a LOCAL nest only,
	// falling back to the personal `g.Defaults.Stack` when absent — and
	// ResolveStack is the ONE place that turns it into a root to load stacks
	// from: nest.Resolve works on bare names within a SINGLE root and must
	// not learn about sources, so the caller owns reference resolution.
	stackRoot, stackSrcName, ref, err := ResolveStack(denHome, g, nestRoot, srcName, bareNest, n, o.Nest)
	if err != nil {
		return err
	}
	stacks, err := config.LoadStacks(stackRoot)
	if err != nil {
		return err
	}
	// Overwritten with the BARE name Resolve can look up in stackRoot: n.Stack
	// as loaded from disk may still carry a source prefix (the local-nest
	// case above) or be empty (the personal-default case), and Resolve has
	// no notion of sources at all.
	n.Stack = ref

	// Staleness hint (spec 2026-08-04 §4): printed at most once per DISTINCT
	// source this spawn touched — the nest's source and the stack's can be
	// the same, different, or the stack's alone (a local nest with a
	// prefixed `stack:`). Never a refusal and never a network call: Stale
	// reads FETCH_HEAD/HEAD's mtime off disk. d.Now == nil skips it outright
	// — the many hand-built Deps elsewhere in this package's tests touch no
	// source and owe nothing to the clock.
	if d.Now != nil {
		now := d.Now()
		hinted := make(map[string]bool, 2)
		for _, s := range []string{srcName, stackSrcName} {
			if s == "" || hinted[s] {
				continue
			}
			hinted[s] = true
			if source.Stale(denHome, s, now) {
				fmt.Fprintf(d.Err,
					"hint: source %q was last fetched more than %s ago — den source update %s\n",
					s, staleAfterWords(), s)
			}
		}
	}

	// `-i` feeds the SAME input as `--without`, and nothing more: the checklist
	// is a source of input placed in front of a selection rule that already
	// exists and is already tested (nest.Resolve). Nothing here reopens it.
	without := o.Without
	if o.Interactive {
		if without, err = interactiveWithout(d, n); err != nil {
			return err
		}
	}
	// The working directory is read HERE, once, and handed to internal/nest,
	// which stays pure: `den spawn scratch .` is then assertable without a test having
	// to chdir. os.Getwd is world access, like the os.Stat probes at step 2 —
	// this side of the boundary is where it belongs.
	//
	// Read only when there IS a positional: a spawn with none must not fail
	// because the process sits in a deleted directory.
	cwd := ""
	if len(o.Repos) > 0 {
		if cwd, err = os.Getwd(); err != nil {
			return fmt.Errorf(
				"reading the working directory, needed to resolve the repos given on "+
					"the command line: %w", err)
		}
	}
	r, err := nest.Resolve(denHome, g, stacks, n, nest.Options{
		Agent: o.Agent, Without: without, Only: o.Only, Repos: o.Repos, Cwd: cwd,
	})
	if err != nil {
		return err
	}

	// The name is computed before any side effect: a worktree den cannot
	// name is refused before anything is created.
	//
	// -w takes a BRANCH name, and "feature/123" is an ordinary one — but
	// neither a valid sandbox-name component nor a flat path component. den
	// flattens only the derived NAME; the branch keeps what was typed.
	//
	// Flattening happens here, upstream of sbx.SandboxName, and does not
	// loosen it: everything downstream that consumes the name — `sbx
	// create` argv, scoped policy, trash, `den rm` — still gets a strict
	// component.
	worktreeName := worktree.Name{}
	if o.Worktree != "" {
		flattened, err := config.FlattenSandboxComponent("worktree", o.Worktree)
		if err != nil {
			return err
		}
		worktreeName = worktree.Name{Dir: flattened, Branch: o.Worktree}
	}
	// Sandbox naming: ":" is not in sbx's `--name` charset, so a nest loaded
	// FROM a source cannot spawn under its prefixed reference verbatim — its
	// sandbox component is the FLATTENED reference ("corp:backend" →
	// "corp-backend"). A LOCAL nest keeps o.Nest unchanged: today's ordinary,
	// working sandbox name, and there is no source prefix to strip.
	//
	// Flattening rewrites exactly ONE character on this path: the ":"
	// separator. srcName is charset-validated by config.ValidateSourceName
	// and bareNest by LoadNest's config.ValidateSandboxComponent, so both
	// already satisfy the sandbox charset and nothing else in "srcName:
	// bareNest" changes — the flattened form is always literally
	// "srcName-bareNest". Two collisions are therefore possible, and only
	// two: a LOCAL nest whose file name equals that string, and ANOTHER
	// installed source whose own "otherSrc-otherNest" decomposition of the
	// same string also names a real nest file. Both are checked below,
	// before any side effect; neither is normalized in silence.
	nestComponent := o.Nest
	if srcName != "" {
		nestComponent, err = config.FlattenSandboxComponent("nest", o.Nest)
		if err != nil {
			return err
		}
		localPath := nest.FilePath(denHome, nestComponent)
		if _, statErr := os.Stat(localPath); statErr == nil {
			// The local file is named FIRST as the one to rename: the source
			// file lives in a team repo's git clone, and `den source update`
			// would silently revert a rename made there — advice that
			// survives is advice worth reading first.
			return fmt.Errorf(
				"nest %q: flattens to sandbox name %q, which collides with the local nest %s — "+
					"rename %s (or the source nest %s, though a `den source update` would revert that) "+
					"so attach, `den ls` and `den rm` are never ambiguous between them",
				o.Nest, nestComponent, localPath, localPath, nest.FilePath(nestRoot, bareNest))
		}
		if otherPath, found := crossSourceCollision(denHome, srcName, nestComponent); found {
			return fmt.Errorf(
				"nest %q: flattens to sandbox name %q, which collides with the source nest %s — "+
					"two DIFFERENT installed sources decompose the same sandbox name; rename the "+
					"source nest at %s or %s (renaming inside a source is reverted by its next "+
					"`den source update`, so the durable fix is usually the `den source add --name`) "+
					"so attach, `den ls` and `den rm` are never ambiguous between them",
				o.Nest, nestComponent, otherPath, nest.FilePath(nestRoot, bareNest), otherPath)
		}
	}
	sandboxName, err := sbx.SandboxName(nestComponent, worktreeName.Dir)
	if err != nil {
		return err
	}
	// Announced early: otherwise the user looks for "feature/123" in
	// `den ls` and never finds it — the sandbox carries the flattened name
	// there.
	if worktreeName.Dir != worktreeName.Branch {
		fmt.Fprintf(d.Out,
			"worktree %q: branch name kept, sandbox becomes %s\n",
			worktreeName.Branch, sandboxName)
	}

	// 2. All repos must exist before any create (spec §11).
	for _, repo := range r.Repos {
		if _, err := os.Stat(repo.Path); err != nil {
			// The remedy follows the ORIGIN. Sending someone to edit
			// nests/<n>.yaml over a path they typed by hand names a file that
			// has nothing to do with the failure — the wrong remedy is worse
			// than a bare error, because it is followed.
			if repo.AdHoc {
				// %q, not %s: a command-line path is deliberately never trimmed
				// (parseRepoArg, internal/nest/repos.go) so a directory legitimately
				// named with leading/trailing spaces survives intact — but that only
				// pays off if this check names it verbatim. Under %s the padding
				// blends into the surrounding text and `den spawn api " /dev/api "` prints
				// exactly like a clean path. And unlike the declared branch below,
				// this one names no remedy: the origin alone tells the user where the
				// path came from, not what to do about it.
				return fmt.Errorf(
					"repo not found: %q — given on the command line: check the path, or drop it "+
						"from the command line",
					repo.Path)
			}
			return fmt.Errorf(
				"nest %q: repo not found: %s — fix `repos:` in %s",
				o.Nest, repo.Path, nest.FilePath(nestRoot, bareNest))
		}
	}
	// 2bis. Under -w, git-ness is decided HERE, before any worktree exists.
	//
	// worktree.Ensure only os.Stat's its repo (worktree.checkRepo): a non-git
	// directory is not caught until `git worktree add` fails, at step 3, AFTER
	// the worktrees of the repos ahead of it were created — one orphaned
	// worktree per repo, left for the user to clean up by hand. That is the
	// regression this function's ordering exists to prevent, and it predates
	// ad-hoc repos: a declared `repos:` entry pointing at a non-git directory
	// has it too. Positionals just made it reachable in one keystroke.
	//
	// CommonGitDir is a pure read AND is exactly the value step 3 needs, so the
	// result is kept and reused there rather than asked of git twice.
	//
	// Keyed by repo.Path rather than by rank: two entries can name the same
	// repository (a clone and one of its worktrees), the case step 3 already
	// dedups on gitDirs. Keyed by path, the alias falls on the same probe and
	// the reuse does not reintroduce the call it removes.
	commonDirs := make(map[string]string, len(r.Repos))
	if o.Worktree != "" {
		for _, repo := range r.Repos {
			if _, known := commonDirs[repo.Path]; known {
				continue
			}
			commonDir, err := worktree.CommonGitDir(ctx, d.Git, repo.Path)
			if err != nil {
				// The remedy follows the ORIGIN, same as step 2's existence check
				// above: sending someone to edit nests/<n>.yaml over a path they
				// typed by hand names a file that has nothing to do with the
				// failure — the wrong remedy is worse than a bare error, because
				// it is followed.
				if repo.AdHoc {
					return fmt.Errorf(
						"%w — `-w` propagates a worktree to every repo of the spawn, and %s is not "+
							"a git repository: drop `-w`, or drop that path", err, repo.Path)
				}
				return fmt.Errorf(
					"%w — `-w` propagates a worktree to every repo of the spawn, and %s is not a "+
						"git repository: drop `-w`, or fix `repos:` in %s",
					err, repo.Path, nest.FilePath(denHome, o.Nest))
			}
			commonDirs[repo.Path] = commonDir
		}
	}

	// ssh.dir, in mount mode: it becomes a workspace, so it goes
	// VERBATIM into `sbx create`'s argv. den never passes sbx a path it
	// hasn't guaranteed exists — a missing directory would mount an empty
	// one where the user expects their keys.
	//
	// Checked here, alongside the repos, before worktrees or the agent
	// profile are created — a later refusal would leave the user to clean
	// up by hand.
	//
	// This also applies when RE-attaching to an already-live sandbox: if
	// ssh.dir or a kit disappears from disk, `den spawn` can no longer
	// attach even though none of this is re-read at attach time (the VM
	// keeps its `create`-time mounts). `den sh <name>` is the one path
	// that skips all of this: it only calls spawn.Attach and reads neither
	// config nor kits.
	if r.SSHMode == "mount" {
		if _, err := os.Stat(r.SSHDir); err != nil {
			return fmt.Errorf(
				"ssh.dir: %s not found — fix `ssh.dir` in %s: in \"mount\" mode this directory "+
					"is mounted in the sandbox, and a missing path would mount an empty directory "+
					"instead of your keys",
				r.SSHDir, config.GlobalPath(denHome))
		}
	}
	// Same invariant, same place: kits go into `sbx create`'s `--kit`
	// argv. `den doctor` already checks them, but only for whoever runs
	// it — without this, `den spawn` would exit 0 and let sbx fail
	// booting the microVM, leaving the user with a dead VM instead of a
	// den message.
	for _, k := range r.Stack.DeclaredKits() {
		if _, err := os.Stat(k); err != nil {
			return fmt.Errorf(
				"stack %q: kit not found: %s — fix `kit:` or `kits:` in %s",
				r.Stack.Name, k, filepath.Join(r.Stack.Dir, "stack.yaml"))
		}
	}

	// 2ter. In agent-forward, warn (never block) if the agent den is about to
	// forward holds no key. sbx transmits the socket faithfully, but an empty
	// agent forwards an empty agent: `git push` then dies on publickey inside
	// the VM, far from the cause, with no ~/.ssh to fall back to. Same probe as
	// `den doctor`; placed before `sbx create`, and applying to the attach
	// branch too — the forwarded socket is a live proxy, so the warning is just
	// as true when returning to a running sandbox.
	//
	// The socket is read HERE and passed in, rather than inside the warning:
	// os.Getenv is world access, and this function already owns the other two
	// (the repo and ssh.dir Stat probes above). It also keeps the warning
	// itself assertable from a test that sets nothing but its arguments.
	warnEmptySSHAgent(d.Err, r.SSHMode, os.Getenv("SSH_AUTH_SOCK"), d.SSHAgent, d.goos())

	// 2quater. Spawn-or-attach is decided HERE, before the first side effect, and
	// the stack image is checked on the create branch (spec §11).
	//
	// The reading used to sit at step 6, next to the create/attach fork it
	// feeds. It moved up because the image check has to be BOTH:
	//
	//   - conditional on creating. A live sandbox is attached to, and attaching
	//     needs no image — refusing there would refuse a `den spawn` that works,
	//     over an image the VM stopped needing the moment it was created.
	//   - upstream of the worktrees. A refusal at the old position would already
	//     have created a git worktree per repo and left the user to clean them
	//     up, which is the exact regression the ordering of this function exists
	//     to prevent.
	//
	// Nothing between here and step 6 touches sbx, so no call order changed —
	// only git work now happens after the reading instead of before it.
	//
	// What the move does widen is the window between this verdict and the `sbx
	// create` at step 6, which the worktree operations now sit inside and which
	// git can make slow. A concurrent `den` on the SAME sandbox name can create
	// it meanwhile, and this one would then create where attaching was the
	// correct answer; the duplicate then lands on sbx, which owns the name and
	// is the only thing that can arbitrate it. Not verified: what `sbx create`
	// answers on a name that already exists — den expects a refusal naming the
	// collision, and even so this is the cheap side of the trade. The regression
	// the old position produced was neither rare nor conditional on a race: one
	// orphaned git worktree per repo, on EVERY refusal, left to clean up by hand.
	//
	// The found Sandbox is KEPT, not reduced to a bool: only it carries the
	// real status and the workspaces the VM actually mounts.
	boxes, err := sbx.Ls(ctx, d.Sbx)
	if err != nil {
		return err
	}
	live := sbx.Find(boxes, sandboxName)
	if live == nil {
		// stackSrcName, not srcName: the stack's own origin. A LOCAL nest may
		// carry a prefixed `stack:`, and a source nest's stack always resolves
		// inside that same source — so the two differ, and only the stack's
		// says what `den build` must be handed.
		if err := checkStackImage(ctx, d, r.Stack,
			config.JoinSourceRef(stackSrcName, r.Stack.Name)); err != nil {
			return err
		}
	}

	// 3. Worktrees, if requested. The first workspace must stay the first
	// repo: sbx.Sandbox.Workdir depends on it for attach, and nothing at
	// its level can verify the list was built in this order.
	workspaces := make([]string, 0, 2*len(r.Repos)+2)
	// Common git dirs are collected separately and appended AFTER all
	// worktrees, so the repo list stays contiguous and first.
	var gitDirs []string
	for _, repo := range r.Repos {
		repoPath := repo.Path
		if o.Worktree != "" {
			repoPath, err = worktree.Ensure(ctx, d.Git, r.WorktreeLayout, r.WorktreeRoot, worktreeName, repo.Path)
			if err != nil {
				return err
			}
			fmt.Fprintf(d.Out, "worktree %s: %s\n", repo.Name(), repoPath)

			// Without this mount the worktree arrives in the VM with a
			// DEAD git: its `.git` is a file pointing at
			// `<repo>/.git/worktrees/<name>`, whose target belongs to the
			// main repo, which nothing mounted — every git command fails
			// with "fatal: not a git repository".
			//
			// The common git dir, not the whole repo: mounting the whole
			// repo would also fix git, but it re-exposes the main
			// worktree WRITABLE — exactly the isolation `-w` exists for.
			//
			// Writable, deliberately: mounted `:ro`, `status` and `log`
			// work but `commit` dies on "Unable to create .../index.lock:
			// Permission denied" — a VM that looks fine until the first
			// commit is worse than one that refuses outright.
			//
			// The `gitdir` symlink resolves as-is, unrewritten, because
			// sbx mounts at the SAME absolute path as the host (A11).
			// Read from the step-2 probe, never asked again: git already
			// answered this, and asking twice would let the two answers differ
			// under a concurrent checkout.
			commonDir := commonDirs[repo.Path]
			if !slices.Contains(gitDirs, commonDir) {
				gitDirs = append(gitDirs, commonDir)
			}
		}
		workspaces = append(workspaces, repoPath)
	}
	// Without -w, the whole repo is mounted, .git included: nothing to add.
	workspaces = append(workspaces, gitDirs...)

	// 4. Agent profile: mounted RW, it must exist — otherwise sbx mounts
	// an empty directory and the agent starts from scratch on every
	// spawn.
	if err := os.MkdirAll(r.AgentConfigDir, 0o755); err != nil {
		return fmt.Errorf("creating agent %s profile (%s): %w", r.AgentName, r.AgentConfigDir, err)
	}
	workspaces = append(workspaces, r.AgentConfigDir)
	// All three SSH modes are handled here, including the two that add
	// nothing to workspaces — an `if` with no `else` left "nothing to do,
	// by design" indistinguishable from "case forgotten".
	//
	//   - "mount": ssh.dir becomes a workspace. Checked in step 2; only
	//     its position in the list matters here (the first workspace
	//     becomes the attach's -w).
	//
	//   - "agent-forward" (the DEFAULT): nothing to add, here or
	//     elsewhere. It relies entirely on `sbx create` inheriting den's
	//     environment, SSH_AUTH_SOCK included — cmd.Env is left nil in
	//     internal/sbx/runner.go, and that inheritance is covered by
	//     TestExecRunTransmitsDenEnvironment. The socket has no place in
	//     the argv (no sbx flag takes it) nor in the mixin (a host socket
	//     value written into a kit would be stale by the next session).
	//     `den doctor` warns when the variable is absent — the one case
	//     this mode gives nothing. Not verified: that sbx forwards the
	//     socket into the microVM itself (spec A10).
	//
	//   - "none": nothing to add, by definition.
	//
	// That the last two produce the SAME list, and mount exactly one more
	// workspace, is covered by TestSpawnAddsNoWorkspaceOutsideMountMode.
	if r.SSHMode == "mount" {
		workspaces = append(workspaces, r.SSHDir)
	}

	// 5. Generate the mixin. r.DenHome, not denHome: Resolve guarantees
	// it's absolute, and this path goes straight into `sbx create --kit`,
	// where cwd is no longer guaranteed.
	mixin, err := agent.MixinFrom(r, sandboxName)
	if err != nil {
		return err
	}
	// The on-disk mixin is the REFERENCE for `create`: what the VM
	// actually received, and the only thing to compare today's config
	// against to detect drift (step 6). It's read here, and only
	// rewritten on the create branch below — rewriting it on every pass
	// would destroy the reference, making the comparison compare the
	// mixin to itself and never detect anything.
	previous, previousErr := agent.ReadMixin(r.DenHome, sandboxName)

	// 6. Spawn-or-attach: a name that's already live is not an error
	// (spec §11). `live` was read at step 2quater — the image check needed the
	// verdict before any worktree existed.

	// The attach workdir: the config's on the create branch (the VM will
	// mount exactly these workspaces), the VM's on the other.
	workdir := first(workspaces)

	if live != nil {
		// A name held by a VM den knows nothing about is not
		// spawn-or-attach. Same guard as `den sh`, and the same helper,
		// so the property can't be true on one side and forgotten on the
		// other.
		if err := live.CheckAttachable(); err != nil {
			return err
		}

		// -w comes from the workspaces the VM MOUNTS (its original
		// `create`), not from what the cascade recomputes now. If the
		// nest's first repo moved since, the recomputed path wouldn't
		// exist in the VM. Empty if the VM mounts nothing: Attach then
		// omits -w rather than inventing a path.
		workdir = live.Workdir()

		// Configuration drift. NOTHING reapplies a mixin to a running
		// VM: it keeps its create-time policy and env. We WARN without
		// refusing (refusing would break a `den spawn` that worked
		// yesterday over a harmless YAML change) and without recreating
		// (unrequested destruction of a VM that may carry work in
		// progress).
		reportDrift(d.Out, sandboxName, previous, previousErr, mixin)
		// Drift of a DIFFERENT kind, invisible to reportDrift: a sandbox
		// created before fix F1 is still running and doesn't mount the
		// git dirs. Its mixin hasn't changed, so the comparison above
		// stays silent. Without this, the user reattaches to a VM where
		// git is dead and only finds out on their first git command.
		reportMissingGitDirs(d.Out, sandboxName, live.Workspaces, gitDirs)
		// Two DIFFERENT drifts used to arrive as one. reportUnmountedRepos
		// compares today's configuration to what the VM mounts, which
		// fires both when the VM is missing something and when the nest
		// itself was edited since — indistinguishable, and only the second
		// has a remedy den can honestly name, since nothing is ever
		// remounted on a live VM.
		//
		// Read, not required: a sandbox created before records existed has
		// none, and attaching to it must keep working exactly as before.
		if recorded, err := manifest.Read(r.DenHome, sandboxName); err == nil {
			mounts := make([]string, 0, len(recorded.Repos))
			for _, rr := range recorded.Repos {
				mounts = append(mounts, rr.Mount)
			}
			reportNestChangedSinceCreation(d.Out, sandboxName, mounts, workspaces[:len(r.Repos)])
		}
		// The repos are the FIRST len(r.Repos) workspaces — step 3 appends
		// exactly one per repo, before the git dirs, the agent profile and
		// ssh.dir. Slicing there rather than recomputing keeps the comparison on
		// the paths the VM would actually have received, worktrees included.
		reportUnmountedRepos(d.Out, sandboxName, workdir, live.Workspaces, workspaces[:len(r.Repos)])

		// A SINGLE status line, naming which of the two cases this is.
		// "restarts on attach", not "resumed": under --detach den runs no
		// exec, so nothing restarts now — the next `den sh` does. True on
		// either side of this branch.
		if live.IsStopped() {
			fmt.Fprintf(d.Out, "sandbox %s stopped: it restarts on attach (its state is preserved)\n", sandboxName)
		} else {
			fmt.Fprintf(d.Out, "sandbox %s already live: attaching\n", sandboxName)
		}
	} else {
		// The creation record, written BEFORE `sbx create` (spec 2026-08-05
		// D3). The worktrees already exist at this point — step 3 created
		// them — so a `sbx create` that fails leaves directories on disk, and
		// this is the only position where that case still leaves a trace of
		// them. The accepted corollary is that a manifest can exist with no
		// sandbox; `den ls` and `den doctor` are what make that state
		// addressable.
		//
		// A write failure REFUSES, here, rather than being warned about: den
		// has just printed the path of every worktree it created
		// (`worktree %s: %s` above), so the refusal names them and the user
		// is not additionally left with a VM to destroy.
		if err := manifest.Write(r.DenHome, manifestOf(
			sandboxName, o.Nest, nest.FilePath(nestRoot, bareNest),
			worktreeName, r, workspaces[:len(r.Repos)], gitDirs,
		)); err != nil {
			return err
		}

		// The mixin is materialized ONLY here: the one moment it's
		// placed on a VM, and so the only time the file can claim to
		// describe what that VM carries.
		mixinDir, err := agent.WriteMixin(r.DenHome, sandboxName, mixin)
		if err != nil {
			return err
		}
		argv, err := sbx.CreateArgv(sbx.Create{
			Name:       sandboxName,
			Image:      r.Stack.Image,
			StackKits:  r.Stack.DeclaredKits(),
			MixinKit:   mixinDir,
			Workspaces: workspaces,
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(d.Out, "creating sandbox %s (image %s)...\n", sandboxName, r.Stack.Image)
		// Recontextualized: Exec.Run already prefixes its error with the
		// FULL argv — every --kit and workspace on one line — where the
		// failed step gets lost.
		if _, err := d.Sbx.Run(ctx, argv...); err != nil {
			return fmt.Errorf("creating sandbox %s: %w", sandboxName, err)
		}
	}

	// 7. Fail-closed settle-loop before any attach — even under
	// --detach: a sandbox marked "ready" without its policy in place is
	// the same half-start, just noticed later.
	if len(r.Egress) > 0 {
		fmt.Fprintf(d.Out, "waiting for network policy (%d host(s))...\n", len(r.Egress))
	}
	if err := policy.Settle(ctx, d.Sbx, sandboxName, r.Egress, d.Policy); err != nil {
		return err
	}

	// 8. The §9.1 agent-freshness gate.
	//
	// §9.1 promises "a sandbox never starts with a stale agent" and makes the
	// update fail-closed. Until now den enforced neither: it waited for the
	// network policy and for nothing in the kit dispatcher, so it printed
	// "ready" and exited 0 roughly 35 s before the freshness command finished —
	// and when that command FAILED (measured, with an agent whose update exits
	// non-zero), den said nothing, then or ever, not even on re-attach. The
	// promise was carried by no code at all.
	//
	// Waiting is not free: it is the difference between a 7.6 s spawn and a
	// ~42 s one, so where den waits was arbitrated rather than assumed.
	//
	//   - Attaching a shell: den WAITS. The user is about to run the agent —
	//     that is what the sandbox is for — and handing them a stale one is the
	//     exact failure §9.1 exists to prevent.
	//   - `--detach`: den does NOT wait. Nobody is at a prompt, the caller is
	//     usually a script that will not touch the agent, and 35 s on every
	//     spawn of a chain is a real cost against a risk the next attach
	//     catches anyway. It warns instead, on stderr, and names how to read
	//     the verdict.
	//
	// A FAILED gate refuses, on both paths: fail-closed is §9.1's word, and it
	// is the same discipline as the settle-loop, which already declines to
	// attach into a sandbox whose policy is not in place. A gate still silent
	// when the budget runs out only warns — den waited what it promised, and a
	// dispatcher still working is no evidence of a stale agent.
	//
	// SKIPPED on a sandbox den has decided to leave stopped: reading the
	// journal is an `sbx exec`, which restarts the VM, and waking one to
	// inspect it would contradict the line printed twenty lines below. Nothing
	// is lost — the dispatcher re-runs on the next restart (measured), so the
	// gate is evaluated again exactly when the sandbox comes back.
	staysStopped := live != nil && live.IsStopped() && o.Detach
	if !staysStopped {
		if err := checkFreshness(ctx, d, sandboxName, o.Detach); err != nil {
			return err
		}
	}

	// 9. Attach.
	if o.Detach {
		// "READY" IS A CLAIM, AND den ONLY MAKES IT WHERE IT HOLDS.
		//
		// On the attach branch of a STOPPED sandbox, nothing above restarts
		// anything: the mixin is not reapplied, no `sbx exec` runs, and the
		// settle-loop answers on a stopped VM too (smoke #2 §6 — `sbx policy
		// check` does not need it running). So den used to print "ready
		// (detached)" over a sandbox `sbx ls --json` still reported as
		// `stopped`, and the scripted follow-up `den X --detach && den ports X`
		// walked straight into an sbx 500 (#17, and #16 behind it).
		//
		// Waking it here was the other candidate and was rejected: sbx parks
		// idle sandboxes in about 45 s (measured), so the truth bought would
		// outlive the command by less than a minute, and --detach's whole
		// contract is NOT to enter the VM. The principle den applies to both
		// defects — wake only where the operation requires a live VM, never
		// claim an unverified state (internal/cli/ports.go, wakeForPorts) —
		// makes this half a sentence, not a call.
		//
		// The status is the one den READ, not one it inferred: `live` comes
		// from the `sbx ls --json` of step 1. On the create branch there is no
		// such reading, and none is taken — a sandbox `sbx create` has just
		// returned success for is running, and a second listing to re-assert it
		// would be a round trip buying a fact already established.
		if live != nil && live.IsStopped() {
			fmt.Fprintf(d.Out,
				"sandbox %s stays stopped (detached) — den started nothing: its configuration is "+
					"checked and its state preserved, and it restarts on the next attach "+
					"(`den sh %s`, or `den ports %s`, which starts it because publishing needs a "+
					"live endpoint)\n",
				sandboxName, sandboxName, sandboxName)
			return nil
		}
		fmt.Fprintf(d.Out, "sandbox %s ready (detached) — run `den sh %s` to enter\n",
			sandboxName, sandboxName)
		return nil
	}
	return Attach(ctx, d.Sbx, sandboxName, workdir)
}

// ResolveStack turns a LOADED nest's `stack:` field into a root to load
// stacks from and the bare reference to resolve within it. Exported and
// shared with `den nest show` (internal/cli/nest.go): both need the exact
// same two refusals below, and a second copy would be a second place for
// them to drift from each other — or from `den lint`'s checkNest
// (internal/lint/lint.go), which states the SAME rule sentences for the
// non-interactive form of this same check.
//
// nestRoot and srcName are Spawn's own `source.Locate(denHome, ref)` result
// for the NEST (not the stack) — the caller already computed them to load n
// in the first place, and passing them again here is cheaper and clearer
// than asking ResolveStack to re-derive srcName from n.Name, which carries
// no source information at all (LoadNest strips it, spec: "the filename is
// authoritative").
//
// subject is the identifier named in both refusals: the reference the USER
// TYPED (Spawn's o.Nest, `nest show`'s args[0]) — never n.Name, which
// LoadNest sets to the bare filename ("api", not "corp:api"): naming n.Name
// would point at a nest the user never typed, and, if they happen to own a
// same-named LOCAL nest, send them to edit the wrong file. The source file to
// fix is appended explicitly instead, since there is no lint-style frame here
// to supply it — hence bareNest as its own parameter, rather than read off
// n.Name: both call sites already hold it on the line that produced n
// (Spawn's own `bareNest`, `den nest show`'s `bareNest`), so there is nothing
// to re-derive, and no way for a caller that renamed n.Name for display
// (`den nest ls`'s "<source>:<name>" prefix) to feed this function a name
// nest.FilePath cannot turn back into a real path.
func ResolveStack(denHome string, g *config.Global, nestRoot, srcName, bareNest string, n *nest.Nest, subject string) (
	stackRoot, stackSrcName, ref string, err error) {
	if srcName != "" {
		// A source nest may NOT fall back on `g.Defaults.Stack`: that default
		// is personal to this machine, and a nest silently inheriting it
		// would spawn a different stack for every teammate — or, worse,
		// spawn the SOURCE's own stack of the same name in silence, which is
		// exactly the substitution den refuses rather than performs (spec
		// §2).
		if n.Stack == "" {
			return "", "", "", fmt.Errorf(
				"nest %q: no `stack:` — a source nest cannot fall back on the personal defaults.stack: "+
					"it must spawn identically on every machine — fix %s",
				subject, nest.FilePath(nestRoot, bareNest))
		}
		// A nest loaded FROM a source may only reference its stack BARE: a
		// prefixed reference would resolve differently on every machine
		// (whichever name the OTHER source happens to be installed under
		// there) and CI, which has installed neither, could not resolve it
		// at all.
		if prefix, _ := config.SplitSourceRef(n.Stack); prefix != "" {
			return "", "", "", fmt.Errorf(
				"nest %q: `stack: %s` is a prefixed reference — inside a source, references are bare "+
					"and resolve in the source itself: the install name is chosen per machine and CI "+
					"knows none — fix %s",
				subject, n.Stack, nest.FilePath(nestRoot, bareNest))
		}
		return nestRoot, srcName, n.Stack, nil
	}
	localRef := n.Stack
	if localRef == "" {
		localRef = g.Defaults.Stack
	}
	return source.Locate(denHome, localRef)
}

// staleAfterWords renders source.StaleAfter for the staleness hint's prose.
// Derived from the constant rather than a literal "7 days" duplicated here:
// source.StaleAfter is its SOLE definition, and a value changed there must
// not leave the hint quoting a number that's no longer true. Days, not a raw
// time.Duration.String() ("168h0m0s" would read as noise in a sentence a
// human is meant to read).
func staleAfterWords() string {
	days := int64(source.StaleAfter / (24 * time.Hour))
	if days == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", days)
}

// crossSourceCollision finds whether ANOTHER installed source (not exclude)
// has a nest whose OWN "otherSrc-otherNest" flattening equals flattened —
// the second of the two collisions a source nest's flattened sandbox name
// can hit (the first, a same-named LOCAL nest, is checked at the call site).
//
// It works backwards from the string alone: since flattening only ever
// rewrites the ":" separator (see the call site's comment), the flattened
// form of "s:n" is always exactly "s-n" once s and n are themselves
// charset-valid — which every installed source name and every loadable nest
// name already is. So for each OTHER installed source s2, flattened need
// only be tested against the single prefix "s2-": if it matches, the
// remainder is the ONLY candidate nest name that could possibly collide, and
// a real file at that name is a real collision, not a guess.
//
// Fail-open on a ReadDir error: this improves a message, and refusing a
// spawn because den's OWN sources/ listing failed would forbid nests that
// have nothing to do with the read that broke.
func crossSourceCollision(denHome, exclude, flattened string) (path string, found bool) {
	entries, err := os.ReadDir(source.Root(denHome))
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == exclude {
			continue
		}
		prefix := e.Name() + config.FlattenedSourceSeparator
		if !strings.HasPrefix(flattened, prefix) {
			continue
		}
		otherNest := flattened[len(prefix):]
		if otherNest == "" {
			continue
		}
		candidate := nest.FilePath(source.Dir(denHome, e.Name()), otherNest)
		if _, statErr := os.Stat(candidate); statErr == nil {
			return candidate, true
		}
	}
	return "", false
}

// checkFreshness runs the §9.1 gate and turns its verdict into den's behaviour.
// The arbitration behind "wait here, warn there" is at the call site; this is
// what each verdict costs the user.
//
// Under `--detach` it READS instead of waiting: den still wants the verdict —
// a gate that has already failed must refuse there too, and the re-attach case
// is exactly a journal that already carries one — it just will not stand and
// wait for one that has not arrived.
func checkFreshness(ctx context.Context, d Deps, sandboxName string, detach bool) error {
	read := func() (agent.GateVerdict, error) {
		if detach {
			return agent.ReadFreshness(ctx, d.Sbx, sandboxName)
		}
		return agent.WaitFreshness(ctx, d.Sbx, sandboxName, d.Freshness, func() {
			fmt.Fprintf(d.Out, "waiting for agent freshness...\n")
		})
	}
	verdict, err := read()
	if err != nil {
		return err
	}
	return reportFreshness(d.Out, sandboxName, verdict, pendingBecause(detach))
}

// CheckFreshnessOnReentry holds the §9.1 gate for a caller that re-enters an
// EXISTING sandbox and configures no spawn — `den sh` (internal/cli/sh.go).
//
// It exists because §9.1's promise is about a sandbox starting, not about the
// command that starts it, and den had been keeping that promise on one door out
// of two. `den spawn` refused a sandbox whose freshness command failed; the
// same sandbox handed out a shell in silence through `den sh`, which does not
// route through Spawn at all — measured on the bench after PR #26, issue #27.
// A guarantee held by one door is worse than none: it teaches the user that den
// checks, on a path where it did not.
//
// starting says whether this re-entry is STARTING the sandbox — `den sh` on a
// stopped one — and it decides between waiting and reading once. §9.2's
// arbitration is already written and applies unchanged: "il attache un shell →
// il attend, en l'annonçant".
//
//   - **stopped**: den WAITS. The read is `sbx exec … cat`, which restarts the
//     VM, and the dispatcher RE-RUNS on restart (measured, agent.KitLogPath).
//     ParseKitLog reads only the LAST block, so the fresh block is empty and a
//     single read would answer GatePending — a `note:` and a shell — while the
//     agent is mid-update, on a sandbox whose gate may be about to fail again.
//     That is #18's silence rebuilt inside the fix for #27, and it is the case
//     #27's own body names as the real one.
//   - **already running**: den reads ONCE. The journal already holds whatever
//     verdict exists, so standing at a prompt for one that has not arrived
//     would tax the ordinary re-entry to catch nothing.
//
// o is only consulted on the waiting branch; a caller that never starts a
// sandbox may pass a zero value, which agent.WaitFreshness would refuse rather
// than quietly complete.
func CheckFreshnessOnReentry(ctx context.Context, r sbx.Runner, out io.Writer, sandboxName string,
	starting bool, o agent.GateOptions) error {
	if !starting {
		verdict, err := agent.ReadFreshness(ctx, r, sandboxName)
		if err != nil {
			return err
		}
		return reportFreshness(out, sandboxName, verdict, reentryPending)
	}
	verdict, err := agent.WaitFreshness(ctx, r, sandboxName, o, func() {
		fmt.Fprintf(out, "waiting for agent freshness...\n")
	})
	if err != nil {
		return err
	}
	// pendingBecause(false): a budget that ran out here means the same thing it
	// means on the spawn attach path — the dispatcher is slower than den's
	// patience — and the two are the same wait, announced the same way.
	return reportFreshness(out, sandboxName, verdict, pendingBecause(false))
}

// reportFreshness turns a gate verdict into den's behaviour: what each verdict
// costs the user, in one place, so the spawn door and the `den sh` door cannot
// answer the same journal differently.
//
// pendingClause names why den stopped waiting — the one thing the two callers
// genuinely differ on.
func reportFreshness(out io.Writer, sandboxName string, verdict agent.GateVerdict, pendingClause string) error {
	switch verdict.State {
	case agent.GatePassed:
		// Silent. The gate passing is the ordinary outcome, and announcing it
		// on every spawn would bury the two lines below that matter.
	case agent.GateFailed:
		// FAIL-CLOSED, and the log line travels with the refusal: §9.1 says the
		// journal is what made the 2026-07-27 bug diagnosable, and a message
		// that says "the gate failed" without it sends the user back into the
		// VM to read what den has already read.
		return fmt.Errorf(
			"sandbox %s: the agent-freshness gate FAILED — %s.\n  %s\n"+
				"den does not open a sandbox whose agent it knows to be stale. Fix the agent's "+
				"`update:` command in the registry, then `den rm %s` and relaunch; the whole journal "+
				"is `sbx exec %s cat %s`",
			sandboxName, verdict.Reason, strings.TrimSpace(verdict.Line),
			sandboxName, sandboxName, agent.KitLogPath)
	case agent.GateAbsent:
		// Out, not Err: both warnings describe THE SANDBOX den is reporting on,
		// and Deps.Err's rule sends those to Out — stderr is for what is wrong
		// with den's environment (the SSH-agent warning), true of the host
		// before this spawn existed. reportDrift and reportMissingGitDirs, the
		// two other sandbox-level warnings, land here for the same reason.
		fmt.Fprintf(out,
			"warning: sandbox %s: %s — its agent is whatever the image carries, and den cannot say "+
				"how old that is; `den rm %s` and relaunch to get the freshness gate\n",
			sandboxName, verdict.Reason, sandboxName)
	default:
		// GatePending. On the attach path this means the budget ran out; under
		// --detach it is the ordinary case, since den made exactly one read —
		// the gate needs about 35 s and a detached spawn returns in about 7.
		//
		// "note:", NOT "warning:", and the distinction is load-bearing rather
		// than cosmetic. Under --detach this line prints on essentially EVERY
		// spawn; calling that a warning would put a warning on the happy path
		// and teach the reader to skip the ones that mean something — including
		// the refusal three lines above. Nothing is wrong here: den has no
		// verdict yet, says so, and says when it will have one.
		fmt.Fprintf(out,
			"note: sandbox %s: the agent-freshness gate has not reported yet — "+
				"den did not wait for it%s, so the agent may still be updating (or may have failed "+
				"to). den re-reads the verdict on the next attach; the journal is "+
				"`sbx exec %s cat %s`\n",
			sandboxName, pendingClause, sandboxName, agent.KitLogPath)
	}
	return nil
}

// pendingBecause names WHY den stopped waiting, because the two reasons call
// for opposite reactions: under --detach nothing is wrong and the next attach
// settles it, while a budget that ran out on the attach path means the
// dispatcher is slower than den's patience and deserves a look.
func pendingBecause(detach bool) string {
	if detach {
		return " under `--detach`, where nobody is waiting at a prompt"
	}
	return " beyond its budget"
}

// reentryPending is pendingBecause's third case, for the door that re-enters an
// existing sandbox. It is a constant rather than a branch of pendingBecause
// because nothing about it is a choice den made at that moment: a re-entry has
// no budget to exceed and no `--detach` to honour, it simply takes the journal
// as it stands.
const reentryPending = " on re-entry, where the sandbox is already up and the journal already " +
	"holds whatever verdict exists"

// warnEmptySSHAgent warns, on stderr, when `ssh.mode: agent-forward` would
// forward nothing usable: no socket at all, an SSH agent that holds no key
// (empty), or one nothing answers behind (unreachable).
//
// Stderr, unlike reportDrift and reportMissingGitDirs below, which warn on
// Out: see Deps.Err for the rule that splits the two.
//
// Non-blocking by design: HTTPS and read-only workflows need no SSH, and den
// has no way to know whether this spawn does — same call as `den doctor`, T12
// §6. Silent in every other case:
//
//   - modes `mount` and `none` don't forward the agent, so its state is
//     irrelevant;
//   - a nil probe (tests that don't exercise SSH, the wiring double) skips
//     rather than reaching for a real ssh-add;
//   - StateKeys is the healthy case and says nothing.
//
// socket is SSH_AUTH_SOCK as den's environment holds it, and it is judged
// BEFORE the probe, for two reasons doctor.Run already acts on
// (TestRunDoesNotQueryTheAgentWhenTheSocketIsAbsent):
//
//   - there is nothing to interrogate without a socket, so the probe would be
//     a wasted `ssh-add` — a fork on the mainline `den spawn` path, bounded
//     only by sshagent's 2 s timeout;
//   - the answer it comes back with is StateUnreachable, whose message says
//     SSH_AUTH_SOCK "points at" a dead socket. On a host that simply has no
//     agent, that sends the user hunting a socket they never set, while
//     `den doctor` tells them the opposite about the same machine.
//
// "absent OR EMPTY", like doctor: os.Getenv answers "" for both, den doesn't
// call LookupEnv, and naming only "absent" would report a cause it never
// observed.
//
// The two probe branches point at a fix that acts WITHOUT respawning den: the
// forwarded socket is a live proxy, so a key loaded host-side is visible in the
// running sandbox immediately. The absent-socket branch does NOT make that
// promise — the sandbox inherits den's environment at `sbx create`, so a socket
// that did not exist then does not appear in a VM already booted.
func warnEmptySSHAgent(w io.Writer, sshMode, socket string, probe func() sshagent.Result, goos string) {
	if sshMode != "agent-forward" || probe == nil {
		return
	}
	fix := sshagent.FixCommand(goos)
	if socket == "" {
		fmt.Fprintf(w,
			"warning: ssh.mode agent-forward, but SSH_AUTH_SOCK is absent or empty in den's "+
				"environment — there is no agent to forward, so this sandbox has no SSH access "+
				"and `git push` fails from inside it; start an agent on the host "+
				"(`eval $(ssh-agent)` then `%s`) and relaunch den, which forwards the socket at "+
				"creation time — %s\n", fix, sshagent.KeyNameCaveat)
		return
	}
	res := probe()
	switch res.State {
	case sshagent.StateEmpty:
		fmt.Fprintf(w,
			"warning: ssh.mode agent-forward, but the forwarded SSH agent holds no identity — "+
				"this sandbox is denied SSH access (publickey) and `git push` fails from inside it; "+
				"run `%s` on the host (the forwarded socket is a live proxy, so the key takes effect "+
				"without respawning den) — %s\n", fix, sshagent.KeyNameCaveat)
	case sshagent.StateUnreachable:
		fmt.Fprintf(w,
			"warning: ssh.mode agent-forward, but SSH_AUTH_SOCK points at an unreachable agent "+
				"(dead socket, no agent, or ssh-add absent from PATH) — this sandbox has no SSH access "+
				"and `git push` fails from inside it; start an agent and run `%s` on the host (the "+
				"forwarded socket is a live proxy, so it takes effect without respawning den) — %s\n",
			fix, sshagent.KeyNameCaveat)
	case sshagent.StateKeys:
		// The healthy case, silent — and it has to be NAMED now, not left to fall
		// off the end of the switch: with a default arm below, that fall-through
		// became "unrecognized state", i.e. a warning on every spawn with a
		// perfectly good agent. Measured, by the two tests that assert this
		// silence.
	default:
		// Without this arm a State this switch doesn't model printed NOTHING: the
		// spawn went silent about the agent, which reads as "nothing to report",
		// and the check disappearing is worse than any verdict it could give. Same
		// arm, same reasoning, as doctor.go's — the two surfaces must not diverge
		// on a state neither of them understands. The value seen is named, so a
		// state added to sshagent surfaces here instead of deleting the warning.
		fmt.Fprintf(w,
			"warning: ssh.mode agent-forward, but the SSH agent probe returned the unrecognized "+
				"state %d — den cannot tell whether a key will reach this sandbox; check the agent "+
				"by hand with `ssh-add -l`\n", int(res.State))
	}
}

// WarnEmptySSHAgentOnReentry is warnEmptySSHAgent for a command that only
// RE-ENTERS a sandbox someone else created — `den sh` (cli/sh.go), whose whole
// contract is that it reads no den home and creates nothing.
//
// It exists because the warning is just as true there: the forwarded socket is
// a live proxy, so re-entering a sandbox whose agent has since been emptied
// hits the same `git push` failure, just as silently. Without this the warning
// covered only the FIRST `den spawn` of the day, while `den sh` — the cheap
// re-entry, used far more often — said nothing on any OS.
//
// The one divergence is the ABSENT socket, and it is why this is a separate
// entry point rather than a second call to warnEmptySSHAgent: a live sandbox
// forwards the socket it inherited at its `sbx create`, from an environment
// that may no longer exist. A shell with no SSH_AUTH_SOCK therefore says
// nothing about what the VM actually holds, and the preflight's remedy — start
// an agent, relaunch den, which forwards the socket at creation time — names a
// step `den sh` does not have. Silence, before the probe: `ssh-add -l` with no
// socket answers StateUnreachable, whose message would claim SSH_AUTH_SOCK
// "points at" a dead socket the user never set.
//
// What it does NOT try to be: proof about the agent the VM really received.
// The probe interrogates the agent of the shell running `den sh`, which is the
// same one on a stable per-user socket (macOS launchd) and can differ from a
// per-shell `eval $(ssh-agent)` on Linux. That is the same approximation the
// attach branch of `den spawn` already makes, and the trade is deliberate: the
// cost of being wrong is one advisory line suggesting a harmless `ssh-add`, the
// cost of staying silent is the publickey failure this package exists to name.
func WarnEmptySSHAgentOnReentry(w io.Writer, sshMode, socket string, probe func() sshagent.Result, goos string) {
	if socket == "" {
		return
	}
	warnEmptySSHAgent(w, sshMode, socket, probe, goos)
}

// checkStackImage refuses a create whose stack image has never been built, so
// den can say what spec §11 promises — "run `den build <stack>`" — instead of
// relaying sbx's own refusal.
//
// That refusal, MEASURED against sbx v0.35.0 on 2026-07-31, is:
//
//	ERROR: request failed: 403 Forbidden: pull failed for image "denghost:v1"
//
// sbx treats an unknown template as a REGISTRY PULL, so what reaches the user
// speaks of authorization, never of a missing build. den cannot pattern-match
// its way out of that — a 403 is also what a genuinely unauthorized pull
// returns — which is why the check happens BEFORE `sbx create`, against
// `sbx template ls --json` (spec §14.0). That command is what unblocked issue
// #8; without it the only honest options were a container-runtime dependency
// or no check at all.
//
// THREE deliberate silences, and each prevents a refusal den could not justify:
//
//   - A stack that is NOT BUILDABLE (no `provision.steps`) is left alone.
//     `image:` may name a registry image sbx will happily pull, and den has no
//     remedy to offer for it — `den build` on a stack den cannot build is not
//     advice, it is a second error. Refusing there would turn a working
//     `den spawn` into a stop.
//   - An `image:` pinned by DIGEST is left alone. `sbx template ls` reports a
//     repository and a tag and no digest at all (sbx.IsDigestRef says so in
//     full), so the inventory can neither confirm nor deny the pin — and
//     reading its silence as "absent" would refuse a spawn over an image that
//     is present.
//   - A FAILING `sbx template ls` is fail-open. The check improves a message;
//     it guards nothing. sbx still refuses the create by itself if the image
//     really is absent, so turning den's inability to read an inventory into a
//     refusal would forbid spawns over a diagnostic that failed.
//
// stackRef is the spelling the REMEDY must print: the stack's reference as
// the user can type it, prefixed when the stack came from a source
// ("corp:devx"). s.Name is the bare name and is deliberately NOT used for
// the command, only for the subject. The two are the same string for a local
// stack and different for a source one, and interpolating the bare name there
// was worse than a refusing remedy: on a den that also owns a local `devx`,
// `den build devx` SUCCEEDS, builds a different image, and this spawn still
// refuses — a command that works, does something, and fixes nothing.
func checkStackImage(ctx context.Context, d Deps, s *config.Stack, stackRef string) error {
	// Buildability comes from config, which is the SOLE source of the verdict
	// (config.Stack.Buildable). It used to be a `os.Stat` on the stack's
	// build.sh, from internal/build — an edge that existed only to answer this
	// one question, and that made the spawn depend on the build package for a
	// file test. Spec §6 requires this silence and `den build`'s skip to agree;
	// reading the same method is what makes that structural.
	if !s.Buildable() {
		return nil
	}
	// Asked BEFORE the inventory is read, not after the lookup fails: a listing
	// that carries no digests cannot answer the question, so den does not spend
	// a process to be told nothing.
	if sbx.IsDigestRef(s.Image) {
		return nil
	}
	templates, err := sbx.Templates(ctx, d.Sbx)
	if err != nil {
		return nil
	}
	if sbx.FindTemplate(templates, s.Image) != nil {
		return nil
	}
	return fmt.Errorf(
		"stack %q: image %s is not built — run `den build %s`; "+
			"`sbx template ls` lists the images sbx already has",
		s.Name, s.Image, stackRef)
}

// reportDrift prints what changed between the mixin a sandbox received at
// its `create` and the one the current configuration would produce.
//
// Called only on the "already live" branch: on a create, the create
// itself lays down the mixin, so it can't have drifted from itself.
//
// A missing reference is reported too, not silenced: a purged cache/, a
// hand-created sandbox, or one from an older den are all "den doesn't
// know", never "nothing changed" — staying silent there would be
// fail-open exactly where drift detection is needed most.
func reportDrift(out io.Writer, sandboxName string, previous agent.Mixin, previousErr error, current agent.Mixin) {
	if previousErr != nil {
		// The message distinguishes the two causes — a purged cache and a
		// corrupt file call for different user action — but neither stays
		// silent.
		if errors.Is(previousErr, os.ErrNotExist) {
			fmt.Fprintf(out,
				"warning: no configuration reference for sandbox %s — drift can't be checked "+
					"(purged cache, or sandbox created outside this den); %v\n",
				sandboxName, previousErr)
			return
		}
		fmt.Fprintf(out, "warning: configuration drift can't be checked: %v\n", previousErr)
		return
	}
	diffs := agent.Differences(previous, current)
	if len(diffs) == 0 {
		return
	}
	fmt.Fprintf(out,
		"warning: sandbox %s is running with the mixin from its `sbx create`, not the current configuration:\n",
		sandboxName)
	for _, line := range diffs {
		fmt.Fprintf(out, "  - %s\n", line)
	}
	fmt.Fprintf(out,
		"  nothing reapplies a mixin to a running VM: `sbx rm --force %s` then relaunch to apply it.\n",
		sandboxName)
}

// reportMissingGitDirs warns when a LIVE sandbox doesn't mount the git
// dirs a requested worktree needs.
//
// The real case: a sandbox created before fix F1, still running, with
// dead git. Nothing remounts a running VM, so the only fix is
// destruction — hence a WARNING, not a refusal: it's the user's call.
//
// The `:ro` suffix is stripped before comparing: it's a mount option, not
// part of the path (same treatment as Sandbox.Workdir).
func reportMissingGitDirs(out io.Writer, sandboxName string, mounted, expected []string) {
	if len(expected) == 0 {
		return
	}
	present := make(map[string]bool, len(mounted))
	for _, w := range mounted {
		present[strings.TrimSuffix(w, ":ro")] = true
	}
	var missing []string
	for _, dir := range expected {
		if !present[dir] {
			missing = append(missing, dir)
		}
	}
	if len(missing) == 0 {
		return
	}
	fmt.Fprintf(out,
		"warning: sandbox %s doesn't mount its repos' git dir — git is dead there "+
			"(\"fatal: not a git repository\" on status, diff, commit and push):\n", sandboxName)
	for _, dir := range missing {
		fmt.Fprintf(out, "  - %s missing from the VM's workspaces\n", dir)
	}
	fmt.Fprintf(out,
		"  this sandbox predates the fix; nothing remounts a running VM: "+
			"`den rm %s` then relaunch.\n", sandboxName)
}

// reportUnmountedRepos warns that the sandbox does not mount every repo this
// command asked for, OR that the shell will not start in the one this command
// named first — TWO INDEPENDENT conditions, deliberately not one gating the
// other.
//
// The mount half is presence: is each expected path anywhere in what the VM
// mounts. The start-directory half is Decision 4 — the FIRST repo this
// invocation named wins the attach's `-w` — and it can fail on its own even
// when nothing is missing: `den spawn scratch ~/dev/a ~/dev/b` creates a VM
// with Workspaces = [a, b, …] and Workdir() = a; the next day, `den spawn
// scratch ~/dev/b` resolves to expected = [b], which the VM already mounts — nothing
// "missing" — yet the attach still runs with workdir = a, frozen at the
// original create. A presence-only check goes silent on exactly that ordinary
// "ask for a subset of what's already mounted" gesture, which is the harm
// this function exists to name in the first place: the user typed "I have
// come to work in b" and lands in a, told nothing.
//
// On a live sandbox NEITHER promise can be kept: `sbx create` takes
// workspaces as positionals, and den reapplies NOTHING to a running VM.
//
// Warn, never refuse, and never recreate: the same doctrine as reportDrift.
// Refusing would break a `den spawn` that worked yesterday over a path added
// today, and recreating would destroy work in progress in the VM.
//
// The mount half also covers a case that was silent before: a `repos:` entry
// added to the yaml after the sandbox was created. The mixin drift comparison
// cannot see it — workspaces are argv, not mixin content.
//
// ONE warning block covers both conditions — the header and the `den rm`
// remedy print at most once even when both fire together.
//
// workdir == "" (a VM that mounts nothing) suppresses the start-directory
// line specifically: den does not invent a directory to name. It does NOT
// suppress the mount half, which has nothing to do with where the shell
// starts.
func reportUnmountedRepos(out io.Writer, sandboxName, workdir string, mounted, expected []string) {
	present := make(map[string]bool, len(mounted))
	for _, w := range mounted {
		// The ":ro" suffix is a mount option, not part of the path — same
		// treatment as Sandbox.Workdir.
		present[strings.TrimSuffix(w, ":ro")] = true
	}
	var missing []string
	for _, p := range expected {
		if !present[p] {
			missing = append(missing, p)
		}
	}
	// movedStart is independent of `missing`: it can be true when every
	// expected repo IS mounted (the subset/reorder case above), and it must
	// stay false on an empty workdir — that is den's "nothing mounted" case,
	// not a moved start directory, and inventing one there would be worse
	// than staying silent.
	movedStart := workdir != "" && len(expected) > 0 && workdir != expected[0]
	if len(missing) == 0 && !movedStart {
		return // a permanent warning stops being read
	}
	// "does not fully match", not "does not mount every repo": the
	// movedStart-only case (every expected repo IS mounted, only the start
	// directory is stale) makes the narrower claim false, and this header
	// covers both triggers, together or alone.
	fmt.Fprintf(out,
		"warning: sandbox %s does not fully match what this command asked for — mounts and "+
			"start directory are both fixed at create time:\n", sandboxName)
	for _, p := range missing {
		fmt.Fprintf(out, "  - %s is not mounted\n", p)
	}
	if movedStart {
		fmt.Fprintf(out, "  the shell starts in %s, as it did at create time\n", workdir)
	}
	fmt.Fprintf(out, "  `den rm %s` then relaunch to change either.\n", sandboxName)
}

// reportNestChangedSinceCreation warns when the repos the configuration now
// resolves to are not the ones den mounted when it created this sandbox.
//
// The remedy is named because there is one and it is the only one: den does
// not touch a live VM's mounts, so the configuration takes effect at the next
// create, not at this attach. Silence here would let the user keep working in
// a sandbox that quietly does not match the nest they just edited.
func reportNestChangedSinceCreation(out io.Writer, sandboxName string, recorded, expected []string) {
	if slices.Equal(recorded, expected) {
		return
	}
	fmt.Fprintf(out,
		"nest changed since sandbox %s was created: it was created with %s, the configuration "+
			"now resolves to %s — a live sandbox keeps its create-time mounts, so this takes "+
			"effect after `den rm %s` and a respawn\n",
		sandboxName, strings.Join(recorded, ", "), strings.Join(expected, ", "), sandboxName)
}

// Attach opens an interactive shell in the sandbox.
//
// `sbx exec`, not `sbx run`: run attaches the image FLAVOR's command
// (often `claude`), has no flag to replace it, and its `-- ARGS` only
// appends arguments.
//
// -w stays BEFORE the sandbox name: in `sbx exec [flags] SANDBOX COMMAND
// [ARG...]`, placed after it would be read as a COMMAND argument and
// reach `bash -l` verbatim.
func Attach(ctx context.Context, r sbx.Runner, sandboxName, workdir string) error {
	argv := []string{"exec", "-it"}
	if workdir != "" {
		argv = append(argv, "-w", workdir)
	}
	argv = append(argv, sandboxName, "bash", "-l")
	return r.Attach(ctx, argv...)
}

func first(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return filepath.Clean(s[0])
}

// manifestOf assembles the creation record from what spawn just did — NOT
// from what the configuration says. mounts is workspaces[:len(r.Repos)], the
// slice step 3 filled one entry per repo, in declaration order: those are the
// paths `sbx create` is about to receive, worktrees included, and recording
// anything else would put the file straight back into the business of
// re-deriving that it exists to end.
func manifestOf(sandboxName, nestRef, nestFile string, wt worktree.Name,
	r *nest.Resolved, mounts, gitDirs []string) manifest.Manifest {

	m := manifest.Manifest{
		Sandbox: sandboxName,
		Nest:    manifest.Nest{Ref: nestRef, File: nestFile},
		Repos:   make([]manifest.Repo, 0, len(r.Repos)),
		GitDirs: gitDirs,
	}
	if wt.Dir != "" {
		m.Worktree = &manifest.Worktree{
			Name:   wt.Dir,
			Branch: wt.Branch,
			Layout: r.WorktreeLayout,
			Root:   r.WorktreeRoot,
		}
	}
	for i, repo := range r.Repos {
		// The three origins are exclusive and ordered: AdHoc first, because a
		// positional never carries a key, and Key before the plain path,
		// because a key entry HAS a path by now (Resolve filled it) and would
		// otherwise be indistinguishable from a declared `path:`.
		origin := manifest.OriginPath
		switch {
		case repo.AdHoc:
			origin = manifest.OriginCommandLine
		case repo.Key != "":
			origin = manifest.OriginKey
		}
		m.Repos = append(m.Repos, manifest.Repo{
			Name:   repo.Name(),
			Origin: origin,
			Key:    repo.Key,
			Repo:   repo.Path,
			Mount:  mounts[i],
			// den created this directory iff it spawned under -w. That single
			// bit is what `den rm` consults before touching anything: a repo
			// mounted as-is is the user's own working directory.
			Worktree: wt.Dir != "",
		})
	}
	return m
}
