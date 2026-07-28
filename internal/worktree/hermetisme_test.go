package worktree

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// racineModule remonte depuis le répertoire courant jusqu'au dossier qui
// porte go.mod. `go test` exécute chaque paquet depuis SON PROPRE dossier,
// pas depuis la racine du module : ce balayage doit donc la retrouver
// lui-même plutôt que de supposer un chemin relatif fixe.
func racineModule(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod introuvable en remontant depuis %s", dir)
		}
		dir = parent
	}
}

// gitBrutRe repère un appel git NON PASSÉ PAR L'INTERFACE Git de ce paquet,
// écrit en clair dans un fichier de test : exec.Command("git", …) ou
// exec.CommandContext(ctx, "git", …).
var gitBrutRe = regexp.MustCompile(`exec\.Command(?:Context\([^,]+,|\()\s*"git"`)

// testMainRe et neutraliseRe repèrent respectivement un TestMain déclaré et
// un appel à NeutraliseEnvironnementGit — QUALIFIÉ (worktree.…) ou non (le
// paquet worktree lui-même s'appelle sans qualifier).
var testMainRe = regexp.MustCompile(`(?m)^func TestMain\(m \*testing\.M\)`)
var neutraliseRe = regexp.MustCompile(`NeutraliseEnvironnementGit`)

// TestExigeNeutraliseEnvironnementGitSiGitEstLanceEnClair balaye internal/ et
// cmd/ : tout paquet dont un fichier de test lance git EN CLAIR doit avoir un
// TestMain qui appelle NeutraliseEnvironnementGit.
//
// Cette garde existe parce que la même classe de défaut s'est rouverte DEUX
// FOIS après sa première fermeture (voir la godoc de NeutraliseEnvironnementGit,
// dans worktree.go) : internal/worktree avait fermé le trou pour lui-même,
// puis internal/cli l'a rouvert sans le savoir, puis internal/spawn — mesuré :
// un `go test ./internal/spawn/...` lancé sous GIT_DIR/GIT_WORK_TREE désignant
// un dépôt tiers y a ajouté 32 commits, et rien dans la suite ne l'a signalé
// avant une revue humaine. Un helper partagé RÉDUIT le risque d'oubli ; cette
// garde le FERME — un quatrième paquet qui lancerait git en clair sans
// appeler le helper fait maintenant échouer `go test ./...`, pas seulement une
// revue qui pourrait ne pas y penser.
//
// Le test exige l'appel au helper PARTAGÉ, pas seulement UNE neutralisation
// quelconque : une réimplémentation locale (recopier les mêmes appels
// os.Unsetenv) passerait à côté de cette garde tout en risquant de diverger de
// l'original au prochain correctif — exactement le défaut que la centralisation
// visait à fermer.
func TestExigeNeutraliseEnvironnementGitSiGitEstLanceEnClair(t *testing.T) {
	racine := racineModule(t)

	for _, sousArbre := range []string{"internal", "cmd"} {
		depart := filepath.Join(racine, sousArbre)
		entrees, err := os.ReadDir(depart)
		if err != nil {
			continue // sous-arbre absent : rien à balayer
		}
		for _, e := range entrees {
			if !e.IsDir() {
				continue
			}
			verifiePaquetHermetique(t, filepath.Join(depart, e.Name()))
		}
	}
}

func verifiePaquetHermetique(t *testing.T, dirPaquet string) {
	t.Helper()
	fichiers, err := os.ReadDir(dirPaquet)
	if err != nil {
		t.Fatal(err)
	}

	var lanceGitBrut []string
	var aLeHelper bool

	for _, f := range fichiers {
		if f.IsDir() || !strings.HasSuffix(f.Name(), "_test.go") {
			continue
		}
		chemin := filepath.Join(dirPaquet, f.Name())
		contenu, err := os.ReadFile(chemin)
		if err != nil {
			t.Fatal(err)
		}
		if gitBrutRe.Match(contenu) {
			lanceGitBrut = append(lanceGitBrut, chemin)
		}
		if testMainRe.Match(contenu) && neutraliseRe.Match(contenu) {
			aLeHelper = true
		}
	}

	if len(lanceGitBrut) > 0 && !aLeHelper {
		t.Errorf(
			"%s : lance git en clair dans ses tests (%v) sans TestMain qui "+
				"appelle NeutraliseEnvironnementGit — ajoute un TestMain sur le "+
				"modèle d'internal/cli/main_test.go, sous peine d'écrire dans le "+
				"dépôt désigné par GIT_DIR/GIT_WORK_TREE quand ces variables sont "+
				"exportées (courant sous agents et hooks git)",
			dirPaquet, lanceGitBrut)
	}
}
