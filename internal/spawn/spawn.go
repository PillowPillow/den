// Package spawn orchestrates the full `den <nest>` sequence (spec §6).
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

	"github.com/PillowPillow/den/internal/agent"
	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
	"github.com/PillowPillow/den/internal/policy"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/sshagent"
	"github.com/PillowPillow/den/internal/worktree"
)

// Deps injects access to the world, so the whole sequence is testable
// without a microVM.
type Deps struct {
	Sbx    sbx.Runner
	Git    worktree.Git
	Policy policy.Options
	Out    io.Writer
	// Err carries diagnostics that are not part of the command's output — the
	// empty-SSH-agent warning goes here, on stderr, so it never pollutes the
	// stdout a caller might pipe. Defaults to io.Discard when unset.
	Err io.Writer
	// SSHAgent reports the state of the forwarded SSH agent. Injected, and
	// nil-tolerant: a nil probe (every test that doesn't exercise SSH, plus the
	// wiring double) simply skips the warning rather than reaching for a real
	// ssh-add.
	SSHAgent func() sshagent.Result
}

// There is no SystemDeps constructor here, on purpose: it would let a call
// site build its own second sbx.Runner, defeating the single shared Sbx
// that cli.Deps enforces between `den ls` and spawn. Deps stays a plain
// struct with exported fields; the caller wires it explicitly.

// Options carries the flags of `den <nest>`.
type Options struct {
	Nest     string
	Worktree string
	Agent    string
	Without  []string
	Only     []string
	Detach   bool
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

	// 1. Resolve the cascade.
	g, err := config.LoadGlobal(denHome)
	if err != nil {
		return err
	}
	stacks, err := config.LoadStacks(denHome)
	if err != nil {
		return err
	}
	n, err := nest.LoadNest(denHome, o.Nest)
	if err != nil {
		return err
	}
	r, err := nest.Resolve(denHome, g, stacks, n, nest.Options{
		Agent: o.Agent, Without: o.Without, Only: o.Only,
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
	sandboxName, err := sbx.SandboxName(o.Nest, worktreeName.Dir)
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
			return fmt.Errorf(
				"nest %q: repo not found: %s — fix `repos:` in %s",
				o.Nest, repo.Path, nest.FilePath(denHome, o.Nest))
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
	// ssh.dir or a kit disappears from disk, `den <nest>` can no longer
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
	// it — without this, `den <nest>` would exit 0 and let sbx fail
	// booting the microVM, leaving the user with a dead VM instead of a
	// den message.
	for _, k := range r.Stack.DeclaredKits() {
		if _, err := os.Stat(k); err != nil {
			return fmt.Errorf(
				"stack %q: kit not found: %s — fix `kit:` or `kits:` in %s",
				r.Stack.Name, k, filepath.Join(r.Stack.Dir, "stack.yaml"))
		}
	}

	// 2bis. In agent-forward, warn (never block) if the agent den is about to
	// forward holds no key. sbx transmits the socket faithfully, but an empty
	// agent forwards an empty agent: `git push` then dies on publickey inside
	// the VM, far from the cause, with no ~/.ssh to fall back to. Same probe as
	// `den doctor`; placed before `sbx create`, and applying to the attach
	// branch too — the forwarded socket is a live proxy, so the warning is just
	// as true when returning to a running sandbox.
	warnEmptySSHAgent(d.Err, r.SSHMode, d.SSHAgent)

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
			commonDir, err := worktree.CommonGitDir(ctx, d.Git, repo.Path)
			if err != nil {
				return err
			}
			// Two `repos:` entries can point at the same repository (a
			// clone and one of its worktrees): sbx would receive the same
			// positional twice.
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
	// (spec §11).
	//
	// The found Sandbox is KEPT, not reduced to a bool: only it carries
	// the real status and the workspaces the VM actually mounts.
	boxes, err := sbx.Ls(ctx, d.Sbx)
	if err != nil {
		return err
	}
	live := sbx.Find(boxes, sandboxName)

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
		// refusing (refusing would break a `den <nest>` that worked
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

	// 8. Attach.
	if o.Detach {
		fmt.Fprintf(d.Out, "sandbox %s ready (detached) — run `den sh %s` to enter\n",
			sandboxName, sandboxName)
		return nil
	}
	return Attach(ctx, d.Sbx, sandboxName, workdir)
}

// warnEmptySSHAgent warns, on stderr, when `ssh.mode: agent-forward` would
// forward an SSH agent that holds no key (empty) or that nothing answers
// behind (unreachable).
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
// The message points at a fix that acts WITHOUT respawning den: the forwarded
// socket is a live proxy, so a key loaded host-side is visible in the running
// sandbox immediately.
func warnEmptySSHAgent(w io.Writer, sshMode string, probe func() sshagent.Result) {
	if sshMode != "agent-forward" || probe == nil {
		return
	}
	fix := sshagent.FixCommand(runtime.GOOS)
	switch probe().State {
	case sshagent.StateEmpty:
		fmt.Fprintf(w,
			"warning: ssh.mode agent-forward, but the forwarded SSH agent holds no identity — "+
				"this sandbox is denied SSH access (publickey) and `git push` fails from inside it; "+
				"run `%s` on the host (the forwarded socket is a live proxy, so the key takes effect "+
				"without respawning den)\n", fix)
	case sshagent.StateUnreachable:
		fmt.Fprintf(w,
			"warning: ssh.mode agent-forward, but SSH_AUTH_SOCK points at an unreachable agent "+
				"(dead socket, no agent, or ssh-add absent from PATH) — this sandbox has no SSH access "+
				"and `git push` fails from inside it; start an agent and run `%s` on the host (the "+
				"forwarded socket is a live proxy, so it takes effect without respawning den)\n", fix)
	}
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
