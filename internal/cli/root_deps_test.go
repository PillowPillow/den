package cli

// The injection door added by fix I2 (Git and Policy supplied by the caller,
// not hard-wired into NewRootCmdWith) was locked by NO test at the first fix
// round: replacing, in NewRootCmdWith,
//
//	spawnDeps := deps.Spawn
//
// with
//
//	spawnDeps := spawn.SystemDeps()
//
// left the whole suite green — injectability was real but nothing stopped a
// future refactor from removing it. These two tests lock Git and Policy
// respectively, by proving, through an error recognizable ONLY IF the
// injected access was used, that they do reach the spawn when going through
// NewRootCmdWith.
//
// A second fix round showed the same question applied to Sbx: as long as
// cli.Deps embedded a whole spawn.Deps (with its own Sbx field),
// NewRootCmdWith had to OVERWRITE spawnDeps.Sbx = deps.Sbx for `den ls` and
// the spawn to agree — a line a refactor could remove unnoticed by any test
// (measured). Deps was restructured to carry a single Sbx (see root.go): the
// divergence is now impossible rather than merely tested.
// TestNewRootCmdWithSharesOneSbxBetweenLsAndSpawn stays useful nonetheless: it
// locks that this single-Sbx structure is indeed the one NewRootCmdWith
// assembles, not bypassed by a future wiring that reintroduced a second
// sbx.Runner on the spawn side (a hardcoded sbx.NewExec(""), say).
//
// NOTE: `spawn.SystemDeps()`, cited above as THE refactor shape to prevent,
// NO LONGER EXISTS — it was dead in production (measured: `go build
// ./cmd/den` succeeded without it) and was nothing more than a ready-made
// constructor for exactly that wiring, with a godoc that encouraged it. The
// paragraph is kept in the past tense because it documents what these tests
// lock; it is now a handwritten `sbx.NewExec("")` that is the shape to watch
// for.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PillowPillow/den/internal/doctor"
	"github.com/PillowPillow/den/internal/policy"
	"github.com/PillowPillow/den/internal/sshagent"
	"github.com/PillowPillow/den/internal/worktree"
)

// fakeGit is a minimal double of worktree.Git: it records its calls and
// refuses SYSTEMATICALLY with a recognizable message. Its only purpose is to
// prove, through that error signature, that it was indeed used — and
// therefore that no real git was reached through this path.
//
// deadlines additionally records each call's context deadline, when it
// carries one (nothing is added otherwise) — den rm (rm_test.go) reuses it to
// check that git probes are bounded, rather than duplicating this double.
type fakeGit struct {
	calls     [][]string
	deadlines []time.Time
}

func (g *fakeGit) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return g.RunWithInput(ctx, dir, nil, args...)
}

func (g *fakeGit) RunWithInput(ctx context.Context, _ string, _ []byte, args ...string) ([]byte, error) {
	g.calls = append(g.calls, args)
	if d, ok := ctx.Deadline(); ok {
		g.deadlines = append(g.deadlines, d)
	}
	return nil, fmt.Errorf("fake git: call refused for %v", args)
}

var _ worktree.Git = (*fakeGit)(nil)

// TestNewRootCmdWithPropagatesGit checks that deps.Git handed to
// NewRootCmdWith is indeed the one the spawn uses end to end, not a git
// rewired internally.
func TestNewRootCmdWithPropagatesGit(t *testing.T) {
	home := denHomeSpawnable(t)
	_, spawnDeps := fakeSpawnDeps()
	git := &fakeGit{}

	deps := Deps{
		Doctor:    doctor.SystemDeps(),
		Sbx:       spawnDeps.Sbx,
		Git:       git,
		Policy:    spawnDeps.Policy,
		Freshness: spawnDeps.Freshness,
	}
	root := NewRootCmdWith(deps)

	// -w triggers worktree.Ensure, the spawn's only point that consults Git.
	_, err := executeCmd(t, root, "--den-home", home, "up", "api", "-w", "feat")
	if err == nil {
		t.Fatal("expected an error: the fake Git refuses systematically")
	}
	if !strings.Contains(err.Error(), "fake git") {
		t.Errorf("the error does not come from the INJECTED Git; got: %v", err)
	}
	if len(git.calls) == 0 {
		t.Error("deps.Git received no call: the injection does not reach the spawn")
	}
}

// TestNewRootCmdWithPropagatesPolicy checks that deps.Policy handed to
// NewRootCmdWith is indeed the one that feeds the settle-loop.
// policy.Options.validate is checked UNCONDITIONALLY by Settle, even without
// a declared egress (settle.go:134, before the empty-allowlist shortcut): a
// deliberately invalid Policy is therefore enough to prove it, without
// running the loop or depending on a scripted `sbx policy check`.
func TestNewRootCmdWithPropagatesPolicy(t *testing.T) {
	home := denHomeSpawnable(t)
	_, spawnDeps := fakeSpawnDeps()

	deps := Deps{
		Doctor:    doctor.SystemDeps(),
		Sbx:       spawnDeps.Sbx,
		Git:       spawnDeps.Git,
		Policy:    policy.Options{}, // Timeout=0: rejected by validate()
		Freshness: spawnDeps.Freshness,
	}
	root := NewRootCmdWith(deps)

	_, err := executeCmd(t, root, "--den-home", home, "up", "api", "--detach")
	if err == nil {
		t.Fatal("expected an error: deliberately invalid Policy")
	}
	if !strings.Contains(err.Error(), "unusable settle options") {
		t.Errorf("the error does not come from the INJECTED Policy; got: %v", err)
	}
}

// TestNewRootCmdWithSharesOneSbxBetweenLsAndSpawn locks that `den ls` and the
// spawn can never talk to two different sbx.Runner instances.
//
// The double is shared (same *sbx.Fake) between two command trees built from
// THE SAME Deps.Sbx: if newUpCmd received a different Runner than
// deps.Sbx (a hardcoded sbx.NewExec(""), say), the second call would never
// reach this Fake, and its call counter would not grow — or worse, it would
// try to reach the real `sbx`, absent from this machine.
func TestNewRootCmdWithSharesOneSbxBetweenLsAndSpawn(t *testing.T) {
	home := denHomeSpawnable(t)
	f, spawnDeps := fakeSpawnDeps()

	deps := Deps{
		Doctor:    doctor.SystemDeps(),
		Sbx:       f,
		Git:       spawnDeps.Git,
		Policy:    spawnDeps.Policy,
		Freshness: spawnDeps.Freshness,
	}

	if _, err := executeCmd(t, NewRootCmdWith(deps), "--den-home", home, "ls"); err != nil {
		t.Fatalf("den ls: unexpected error: %v", err)
	}
	callsAfterLs := len(f.Calls)
	if callsAfterLs == 0 {
		t.Fatal("den ls made no call to the Fake: nothing to compare")
	}

	if _, err := executeCmd(t, NewRootCmdWith(deps), "--den-home", home, "up", "api", "--detach"); err != nil {
		t.Fatalf("den up api --detach: unexpected error: %v", err)
	}
	if len(f.Calls) <= callsAfterLs {
		t.Errorf("the spawn made no new call to the SAME Fake as `den ls` "+
			"(%d calls before, %d after): Sbx diverged between the two paths",
			callsAfterLs, len(f.Calls))
	}
}

// TestNewRootCmdWithPropagatesTheSSHAgentProbe locks the LAST hop of the
// empty-agent warning, the one no other test crossed: deps.SSHAgent reaching
// the spawn (root.go), and the warning landing on the COMMAND's stderr
// (spawn.go's `d.Err = cmd.ErrOrStderr()`).
//
// Both lines were deletable with the whole suite green (measured): the spawn's
// own tests build spawn.Deps by hand — so they never go through
// NewRootCmdWith — and cli's tests merge the two streams through executeCmd,
// which cannot tell a warning printed on stdout from one printed on stderr.
// The failure a deletion produces is silent by nature: a nil probe SKIPS the
// warning (spawn.warnEmptySSHAgent) and a nil Err falls back to io.Discard, so
// den would spawn a sandbox denied every `git push` without saying a word.
//
// executeCmdSeparateStreams, not executeCmd, for exactly that reason. The
// probe is a counting double: the sbx.Fake never sees it, so its call count is
// the only proof the injected probe — and not some internal one — is what the
// warning was built from.
//
// --detach keeps the sbx.Fake path short (same shortcut as
// TestNewRootCmdWithSharesOneSbxBetweenLsAndSpawn): no attach to a VM that
// does not exist. The den home from denHomeSpawnable declares no `ssh:`, so
// the mode is the config's default, agent-forward — the only mode that
// forwards an agent and therefore the only one this warning applies to.
func TestNewRootCmdWithPropagatesTheSSHAgentProbe(t *testing.T) {
	// A socket must be in den's environment, or the spawn warns about an ABSENT
	// SSH_AUTH_SOCK and never reaches the probe — the branch this test is not
	// about. Set explicitly rather than inherited: left to the machine, the
	// assertion below would hold on a workstation running an agent and fail on a
	// bare CI runner. The path is never opened (the probe is the double above).
	t.Setenv("SSH_AUTH_SOCK", "/tmp/den-test/agent.sock")
	home := denHomeSpawnable(t)
	_, spawnDeps := fakeSpawnDeps()
	probes := 0

	deps := Deps{
		// Fake rather than System: the Doctor access is never reached on this
		// path, and a fake owes nothing to the machine running the suite.
		Doctor:    doctor.FakeDeps(),
		Sbx:       spawnDeps.Sbx,
		Git:       spawnDeps.Git,
		Policy:    spawnDeps.Policy,
		Freshness: spawnDeps.Freshness,
		SSHAgent: func() sshagent.Result {
			probes++
			return sshagent.Result{State: sshagent.StateEmpty}
		},
	}

	stdout, stderr, err := executeCmdSeparateStreams(t, NewRootCmdWith(deps),
		"--den-home", home, "up", "api", "--detach")
	if err != nil {
		t.Fatalf("den up api --detach: unexpected error: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	if probes == 0 {
		t.Fatal("deps.SSHAgent received no call: the probe does not reach the spawn")
	}
	if !strings.Contains(stderr, "the forwarded SSH agent holds no identity") {
		t.Errorf("stderr = %q, expected the empty-agent warning of the INJECTED probe", stderr)
	}
	// The remedy is half the warning's value: "your agent is empty" without
	// `ssh-add` leaves the user with a diagnosis and no move.
	if !strings.Contains(stderr, "ssh-add") {
		t.Errorf("stderr = %q, the warning must quote the fix to run on the host", stderr)
	}
	// A warning is a diagnostic, not output: on stdout it would land in
	// whatever a caller pipes den into.
	if strings.Contains(stdout, "warning") {
		t.Errorf("stdout = %q, the empty-agent warning belongs on stderr only", stdout)
	}
	// ... and the streams are not merely swapped: the spawn's own report is
	// still on stdout, so stderr carrying the warning means routing, not a
	// command writing everything to the same place.
	if !strings.Contains(stdout, "detached") {
		t.Errorf("stdout = %q, expected the detached spawn's report", stdout)
	}
}
