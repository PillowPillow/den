package cli

// La porte d'injection ajoutée au fix I2 (deps.Spawn propagé tel quel à
// configureSpawn, sans être re-câblé sur spawn.DepsSysteme() en interne)
// n'était verrouillée par AUCUN test : remplacer, dans NewRootCmdAvec,
//
//	spawnDeps := deps.Spawn
//
// par
//
//	spawnDeps := spawn.DepsSysteme()
//
// laissait toute la suite verte — l'injectabilité était réelle mais rien
// n'empêchait de la retirer au prochain refactor. Ces deux tests verrouillent
// respectivement Git et Policy en prouvant, par une erreur reconnaissable
// SEULEMENT possible si l'accès injecté a servi, qu'ils atteignent bien le
// spawn quand on passe par NewRootCmdAvec.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/doctor"
	"github.com/PillowPillow/den/internal/policy"
	"github.com/PillowPillow/den/internal/worktree"
)

// gitFactice est un double minimal de worktree.Git : il enregistre ses appels
// et refuse SYSTÉMATIQUEMENT avec un message reconnaissable. Sa seule utilité
// est de prouver, par cette signature d'erreur, qu'il a bien été sollicité —
// et donc qu'aucun git réel n'a été atteint par ce chemin.
type gitFactice struct {
	appels [][]string
}

func (g *gitFactice) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	g.appels = append(g.appels, args)
	return nil, fmt.Errorf("git factice : appel refusé pour %v", args)
}

var _ worktree.Git = (*gitFactice)(nil)

// TestNewRootCmdAvecPropageGit vérifie que deps.Spawn.Git fourni à
// NewRootCmdAvec est bien celui qui sert au spawn de bout en bout, pas un
// git recâblé en interne sur spawn.DepsSysteme().
func TestNewRootCmdAvecPropageGit(t *testing.T) {
	home := denHomeSpawnable(t)
	_, spawnDeps := depsSpawnFactices()
	git := &gitFactice{}
	spawnDeps.Git = git

	deps := Deps{Doctor: doctor.DepsSysteme(), Sbx: spawnDeps.Sbx, Spawn: spawnDeps}
	root := NewRootCmdAvec(deps)

	// -w déclenche worktree.Assure, seul point du spawn qui consulte Git.
	_, err := executeCmd(t, root, "--den-home", home, "api", "-w", "feat")
	if err == nil {
		t.Fatal("attendu une erreur : le Git factice refuse systématiquement")
	}
	if !strings.Contains(err.Error(), "git factice") {
		t.Errorf("l'erreur ne vient pas du Git INJECTÉ ; obtenu : %v", err)
	}
	if len(git.appels) == 0 {
		t.Error("deps.Spawn.Git n'a reçu aucun appel : l'injection n'atteint pas le spawn")
	}
}

// TestNewRootCmdAvecPropagePolicy vérifie que deps.Spawn.Policy fourni à
// NewRootCmdAvec est bien celui qui sert au settle-loop. policy.Options.valide
// est vérifiée INCONDITIONNELLEMENT par Settle, même sans egress déclaré
// (settle.go:134, avant le raccourci sur allowlist vide) : une Policy
// délibérément invalide suffit donc à le prouver sans faire tourner la
// boucle ni dépendre d'un `sbx policy check` scripté.
func TestNewRootCmdAvecPropagePolicy(t *testing.T) {
	home := denHomeSpawnable(t)
	_, spawnDeps := depsSpawnFactices()
	spawnDeps.Policy = policy.Options{} // Timeout=0 : rejetée par valide()

	deps := Deps{Doctor: doctor.DepsSysteme(), Sbx: spawnDeps.Sbx, Spawn: spawnDeps}
	root := NewRootCmdAvec(deps)

	_, err := executeCmd(t, root, "--den-home", home, "api", "--detach")
	if err == nil {
		t.Fatal("attendu une erreur : Policy délibérément invalide")
	}
	if !strings.Contains(err.Error(), "options de settle inutilisables") {
		t.Errorf("l'erreur ne vient pas de la Policy INJECTÉE ; obtenu : %v", err)
	}
}
