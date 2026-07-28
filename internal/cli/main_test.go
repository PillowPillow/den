package cli

import (
	"os"
	"testing"

	"github.com/PillowPillow/den/internal/worktree"
)

// TestMain neutralise l'environnement git de la machine pour tout le paquet,
// via worktree.NeutraliseEnvironnementGit (voir sa godoc pour le pourquoi et
// l'incident qui l'a fait naître).
//
// den rm exerce du git RÉEL (TestRmNeDetruitPasLaSandboxSiUnWorktreeEstSale et
// consorts) : sans cette neutralisation, sous GIT_DIR + GIT_WORK_TREE
// désignant un dépôt tiers, mesuré, un `go test ./internal/cli/ -run TestRm` a
// réellement ajouté un commit dans CE dépôt au lieu de son t.TempDir().
func TestMain(m *testing.M) {
	worktree.NeutraliseEnvironnementGit()
	os.Exit(m.Run())
}
