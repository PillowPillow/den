package cli

import (
	"os"
	"testing"
	"time"

	"github.com/PillowPillow/den/internal/agent"
	"github.com/PillowPillow/den/internal/worktree"
)

// TestMain neutralizes the machine's git environment for the whole package,
// via worktree.NeutralizeGitEnvironment (see its godoc for why).
//
// `den rm` runs REAL git (TestRmDoesNotDestroyTheSandboxWhenAWorktreeIsDirty
// and friends): without this neutralization, under a GIT_DIR + GIT_WORK_TREE
// pointing at a third-party repo, `go test ./internal/cli/ -run TestRm` has
// actually added a commit to THAT repo instead of its t.TempDir().
func TestMain(m *testing.M) {
	worktree.NeutralizeGitEnvironment()
	os.Exit(m.Run())
}

// instantGate is the §9.1 agent-freshness gate with its real budget and a
// clock that only moves when the loop sleeps.
//
// Every hand-built spawn.Deps in this package needs it, for the reason
// spawn.Deps.Freshness states: the real options carry time.Sleep, and a fake
// sbx never answers anything a dispatcher journal can be read out of — so the
// gate would exhaust ninety real seconds per test before warning.
func instantGate() agent.GateOptions {
	o := agent.DefaultGateOptions()
	clock := time.Now()
	o.Sleep = func(d time.Duration) { clock = clock.Add(d) }
	o.Now = func() time.Time { return clock }
	return o
}
