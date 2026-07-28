package cli

// La porte d'injection ajoutée au fix I2 (Git et Policy fournis par
// l'appelant, pas câblés en dur dans NewRootCmdAvec) n'était verrouillée par
// AUCUN test au premier fix round : remplacer, dans NewRootCmdAvec,
//
//	spawnDeps := deps.Spawn
//
// par
//
//	spawnDeps := spawn.DepsSysteme()
//
// laissait toute la suite verte — l'injectabilité était réelle mais rien
// n'empêchait de la retirer au prochain refactor. Ces deux tests
// verrouillent respectivement Git et Policy en prouvant, par une erreur
// reconnaissable SEULEMENT possible si l'accès injecté a servi, qu'ils
// atteignent bien le spawn quand on passe par NewRootCmdAvec.
//
// Un second fix round a montré que la même question se posait pour Sbx :
// tant que cli.Deps embarquait une spawn.Deps entière (avec son propre champ
// Sbx), NewRootCmdAvec devait ÉCRASER spawnDeps.Sbx = deps.Sbx pour que
// `den ls` et le spawn restent d'accord — une ligne qu'un refactor pouvait
// retirer sans qu'aucun test ne le remarque (mesuré). Deps a été restructurée
// pour ne plus porter qu'un seul Sbx (voir root.go) : la divergence est
// maintenant impossible plutôt que seulement testée.
// TestNewRootCmdAvecUnSeulSbxPartageEntreLsEtSpawn reste néanmoins utile : il
// verrouille que cette structure à Sbx unique est bien celle assemblée par
// NewRootCmdAvec, pas contournée par un futur câblage qui réintroduirait un
// second sbx.Runner du côté spawn (sbx.NewExec("") en dur, par exemple).

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

// gitFactice est un double minimal de worktree.Git : il enregistre ses appels
// et refuse SYSTÉMATIQUEMENT avec un message reconnaissable. Sa seule utilité
// est de prouver, par cette signature d'erreur, qu'il a bien été sollicité —
// et donc qu'aucun git réel n'a été atteint par ce chemin.
//
// echeances enregistre en plus l'échéance du contexte de chaque appel, quand
// il en porte une (rien n'est ajouté sinon) — den rm (rm_test.go) le
// réutilise pour vérifier que les sondes git sont bornées, plutôt que de
// dupliquer ce double.
type gitFactice struct {
	appels    [][]string
	echeances []time.Time
}

func (g *gitFactice) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return g.RunAvecEntree(ctx, dir, nil, args...)
}

func (g *gitFactice) RunAvecEntree(ctx context.Context, _ string, _ []byte, args ...string) ([]byte, error) {
	g.appels = append(g.appels, args)
	if d, ok := ctx.Deadline(); ok {
		g.echeances = append(g.echeances, d)
	}
	return nil, fmt.Errorf("git factice : appel refusé pour %v", args)
}

var _ worktree.Git = (*gitFactice)(nil)

// TestNewRootCmdAvecPropageGit vérifie que deps.Git fourni à NewRootCmdAvec
// est bien celui qui sert au spawn de bout en bout, pas un git recâblé en
// interne.
func TestNewRootCmdAvecPropageGit(t *testing.T) {
	home := denHomeSpawnable(t)
	_, spawnDeps := depsSpawnFactices()
	git := &gitFactice{}

	deps := Deps{
		Doctor: doctor.DepsSysteme(),
		Sbx:    spawnDeps.Sbx,
		Git:    git,
		Policy: spawnDeps.Policy,
	}
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
		t.Error("deps.Git n'a reçu aucun appel : l'injection n'atteint pas le spawn")
	}
}

// TestNewRootCmdAvecPropagePolicy vérifie que deps.Policy fourni à
// NewRootCmdAvec est bien celui qui sert au settle-loop. policy.Options.valide
// est vérifiée INCONDITIONNELLEMENT par Settle, même sans egress déclaré
// (settle.go:134, avant le raccourci sur allowlist vide) : une Policy
// délibérément invalide suffit donc à le prouver sans faire tourner la
// boucle ni dépendre d'un `sbx policy check` scripté.
func TestNewRootCmdAvecPropagePolicy(t *testing.T) {
	home := denHomeSpawnable(t)
	_, spawnDeps := depsSpawnFactices()

	deps := Deps{
		Doctor: doctor.DepsSysteme(),
		Sbx:    spawnDeps.Sbx,
		Git:    spawnDeps.Git,
		Policy: policy.Options{}, // Timeout=0 : rejetée par valide()
	}
	root := NewRootCmdAvec(deps)

	_, err := executeCmd(t, root, "--den-home", home, "api", "--detach")
	if err == nil {
		t.Fatal("attendu une erreur : Policy délibérément invalide")
	}
	if !strings.Contains(err.Error(), "options de settle inutilisables") {
		t.Errorf("l'erreur ne vient pas de la Policy INJECTÉE ; obtenu : %v", err)
	}
}

// TestNewRootCmdAvecUnSeulSbxPartageEntreLsEtSpawn verrouille qu'il n'est pas
// possible que `den ls` et le spawn parlent à deux sbx.Runner différents.
//
// Le double est partagé (même *sbx.Fake) entre deux arbres de commandes
// construits depuis LA MÊME Deps.Sbx : si configureSpawn recevait un autre
// Runner que deps.Sbx (un sbx.NewExec("") recâblé en dur, par exemple), le
// second appel n'atteindrait jamais ce Fake, et son compteur d'appels
// n'augmenterait pas — ou pire, tenterait de joindre le vrai `sbx`, absent de
// cette machine.
func TestNewRootCmdAvecUnSeulSbxPartageEntreLsEtSpawn(t *testing.T) {
	home := denHomeSpawnable(t)
	f, spawnDeps := depsSpawnFactices()

	deps := Deps{
		Doctor: doctor.DepsSysteme(),
		Sbx:    f,
		Git:    spawnDeps.Git,
		Policy: spawnDeps.Policy,
	}

	if _, err := executeCmd(t, NewRootCmdAvec(deps), "--den-home", home, "ls"); err != nil {
		t.Fatalf("den ls : erreur inattendue : %v", err)
	}
	appelsApresLs := len(f.Appels)
	if appelsApresLs == 0 {
		t.Fatal("den ls n'a fait aucun appel au Fake : rien à comparer")
	}

	if _, err := executeCmd(t, NewRootCmdAvec(deps), "--den-home", home, "api", "--detach"); err != nil {
		t.Fatalf("den api --detach : erreur inattendue : %v", err)
	}
	if len(f.Appels) <= appelsApresLs {
		t.Errorf("le spawn n'a fait aucun nouvel appel au MÊME Fake que `den ls` "+
			"(%d appels avant, %d après) : Sbx a divergé entre les deux chemins",
			appelsApresLs, len(f.Appels))
	}
}
