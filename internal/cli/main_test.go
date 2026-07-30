package cli

import (
	"os"
	"testing"

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
