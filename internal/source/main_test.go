// internal/source/main_test.go
package source

import (
	"os"
	"testing"

	"github.com/PillowPillow/den/internal/worktree"
)

// TestMain neutralizes the machine's git configuration and the redirecting
// variables, exactly as internal/cli does: this package's tests run REAL git
// against file:// remotes built in temp dirs, and an inherited GIT_DIR has
// already made suites commit into unrelated repos.
func TestMain(m *testing.M) {
	worktree.NeutralizeGitEnvironment()
	os.Exit(m.Run())
}
