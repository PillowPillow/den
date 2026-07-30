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
		Doctor: doctor.SystemDeps(),
		Sbx:    spawnDeps.Sbx,
		Git:    git,
		Policy: spawnDeps.Policy,
	}
	root := NewRootCmdWith(deps)

	// -w triggers worktree.Ensure, the spawn's only point that consults Git.
	_, err := executeCmd(t, root, "--den-home", home, "api", "-w", "feat")
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
		Doctor: doctor.SystemDeps(),
		Sbx:    spawnDeps.Sbx,
		Git:    spawnDeps.Git,
		Policy: policy.Options{}, // Timeout=0: rejected by validate()
	}
	root := NewRootCmdWith(deps)

	_, err := executeCmd(t, root, "--den-home", home, "api", "--detach")
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
// THE SAME Deps.Sbx: if configureSpawn received a different Runner than
// deps.Sbx (a hardcoded sbx.NewExec(""), say), the second call would never
// reach this Fake, and its call counter would not grow — or worse, it would
// try to reach the real `sbx`, absent from this machine.
func TestNewRootCmdWithSharesOneSbxBetweenLsAndSpawn(t *testing.T) {
	home := denHomeSpawnable(t)
	f, spawnDeps := fakeSpawnDeps()

	deps := Deps{
		Doctor: doctor.SystemDeps(),
		Sbx:    f,
		Git:    spawnDeps.Git,
		Policy: spawnDeps.Policy,
	}

	if _, err := executeCmd(t, NewRootCmdWith(deps), "--den-home", home, "ls"); err != nil {
		t.Fatalf("den ls: unexpected error: %v", err)
	}
	callsAfterLs := len(f.Calls)
	if callsAfterLs == 0 {
		t.Fatal("den ls made no call to the Fake: nothing to compare")
	}

	if _, err := executeCmd(t, NewRootCmdWith(deps), "--den-home", home, "api", "--detach"); err != nil {
		t.Fatalf("den api --detach: unexpected error: %v", err)
	}
	if len(f.Calls) <= callsAfterLs {
		t.Errorf("the spawn made no new call to the SAME Fake as `den ls` "+
			"(%d calls before, %d after): Sbx diverged between the two paths",
			callsAfterLs, len(f.Calls))
	}
}
