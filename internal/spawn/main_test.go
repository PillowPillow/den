package spawn

import (
	"os"
	"testing"

	"github.com/PillowPillow/den/internal/worktree"
)

// TestMain neutralise l'environnement git de la machine pour tout le paquet,
// via worktree.NeutraliseEnvironnementGit (voir sa godoc pour le pourquoi et
// l'incident qui l'a fait naître).
//
// denTestSSH lance du git RÉEL (`git init`/`config`/`commit`, spawn_test.go)
// pour préparer un dépôt de test : sans cette neutralisation, sous GIT_DIR +
// GIT_WORK_TREE désignant un dépôt tiers, mesuré, un
// `go test ./internal/spawn/...` a réellement ajouté 32 commits dans CE dépôt
// au lieu des t.TempDir() des tests — la même classe d'incident que
// internal/worktree et internal/cli avaient déjà fermée chacun pour
// eux-mêmes, rouverte ici avant cette correction.
func TestMain(m *testing.M) {
	worktree.NeutraliseEnvironnementGit()
	os.Exit(m.Run())
}
