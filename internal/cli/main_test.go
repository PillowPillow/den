package cli

import (
	"os"
	"testing"

	"github.com/PillowPillow/den/internal/worktree"
)

// TestMain neutralise l'environnement git de la machine pour tout le paquet.
//
// den rm exerce du git RÉEL (TestRmNeDetruitPasLaSandboxSiUnWorktreeEstSale
// et consorts), et sans cette neutralisation les mêmes variables que celles
// qu'internal/worktree/worktree_test.go ferme pour lui-même détournent aussi
// ces tests-ci : sous GIT_DIR + GIT_WORK_TREE désignant un dépôt tiers,
// mesuré, un `go test ./internal/cli/ -run TestRm` a réellement ajouté un
// commit dans CE dépôt au lieu de son t.TempDir() — l'incident que
// worktree_test.go documente et ferme pour son propre paquet, réintroduit ici
// par un paquet voisin.
//
// La liste réutilisée est celle du paquet worktree
// (worktree.VariablesRedirigeantes) plutôt qu'une copie : deux listes ne
// peuvent pas diverger si l'une n'existe pas.
func TestMain(m *testing.M) {
	os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	os.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	// Neutraliser les deux fichiers ne suffit pas : git accepte aussi de la
	// configuration par l'environnement (GIT_CONFIG_COUNT/KEY_n/VALUE_n).
	os.Unsetenv("GIT_CONFIG_COUNT")
	os.Unsetenv("GIT_CONFIG_PARAMETERS")
	for _, v := range worktree.VariablesRedirigeantes {
		os.Unsetenv(v)
	}
	os.Exit(m.Run())
}
