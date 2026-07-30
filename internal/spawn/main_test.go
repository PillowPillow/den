package spawn

import (
	"os"
	"testing"

	"github.com/PillowPillow/den/internal/worktree"
)

// TestMain neutralizes the machine's git environment for the whole package
// (see worktree.NeutralizeGitEnvironment's godoc for why).
//
// denTestSSH runs REAL git (`git init`/`config`/`commit`, spawn_test.go) to
// prepare a test repo: without this neutralization, under a GIT_DIR +
// GIT_WORK_TREE pointing at a third-party repo, `go test ./internal/spawn/...`
// has actually added commits to THAT repo instead of the tests' t.TempDir() —
// the same class of incident internal/worktree and internal/cli each already
// closed for themselves.
func TestMain(m *testing.M) {
	worktree.NeutralizeGitEnvironment()
	os.Exit(m.Run())
}
