package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/sbx"
)

// Le nom scripté "api.feat12" contient déjà les sous-chaînes "api" et
// "feat12" : une assertion par strings.Contains sur la sortie ENTIÈRE serait
// donc vraie même si la décomposition NEST/WORKTREE était totalement cassée
// (la colonne NAME, seule, suffit à la satisfaire). Les assertions portent
// donc colonne par colonne, sur les champs de chaque ligne.
func TestLsAfficheLesColonnes(t *testing.T) {
	denHomeDeTest(t) // nest "api" y est déclaré

	f := &sbx.Fake{Reponses: map[string]sbx.Reponse{
		"ls --json": {Sortie: []byte(
			`{"sandboxes":[{"name":"api.feat12","agent":"shell","status":"running","workspaces":["/w/api","/p"]}]}`)},
	}}

	sortie, err := executeCmdAvecSbx(t, f, "ls")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	lignes := strings.Split(strings.TrimRight(sortie, "\n"), "\n")
	if len(lignes) < 2 {
		t.Fatalf("attendu un en-tête et une ligne de données ; obtenu :\n%s", sortie)
	}

	entete := strings.Fields(lignes[0])
	attenduEntete := []string{"NAME", "NEST", "WORKTREE", "STATUS", "WORKSPACES"}
	if len(entete) != len(attenduEntete) {
		t.Fatalf("en-tête = %v, attendu %v", entete, attenduEntete)
	}
	for i, col := range attenduEntete {
		if entete[i] != col {
			t.Errorf("en-tête colonne %d = %q, attendu %q ; en-tête complet : %v", i, entete[i], col, entete)
		}
	}

	champs := strings.Fields(lignes[1])
	// NAME reste le nom complet ; NEST et WORKTREE sont les morceaux
	// DÉCOMPOSÉS par sbx.Sandbox.Nest()/Worktree(), pas retrouvés par
	// accident dans NAME.
	attenduLigne := []string{"api.feat12", "api", "feat12", "running"}
	if len(champs) < len(attenduLigne) {
		t.Fatalf("ligne de données = %v, attendu au moins %v", champs, attenduLigne)
	}
	for i, val := range attenduLigne {
		if champs[i] != val {
			t.Errorf("colonne %d = %q, attendu %q ; ligne complète : %v", i, champs[i], val, champs)
		}
	}

	// L'âge n'existe pas dans sbx ls --json : ne jamais prétendre le connaître.
	if strings.Contains(strings.ToUpper(sortie), "AGE") {
		t.Errorf("aucune colonne d'âge ne doit exister ; obtenu :\n%s", sortie)
	}
}

// Une sandbox dont le nest n'est pas déclaré reste visible, mais marquée : la
// masquer ferait disparaître de la vue une VM bel et bien vivante sur la
// machine. Une sandbox dont le nest EST déclaré, elle, ne doit porter aucune
// marque — sinon la marque ne prouverait rien.
func TestLsMarqueLesSandboxesNonDeclarees(t *testing.T) {
	denHomeDeTest(t) // nest "api" y est déclaré, "inconnue" non

	f := &sbx.Fake{Reponses: map[string]sbx.Reponse{
		"ls --json": {Sortie: []byte(
			`{"sandboxes":[{"name":"api","status":"running"},{"name":"inconnue","status":"running"}]}`)},
	}}

	sortie, err := executeCmdAvecSbx(t, f, "ls")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !strings.Contains(sortie, "inconnue") {
		t.Errorf("une sandbox non déclarée doit rester visible ; obtenu :\n%s", sortie)
	}

	var ligneApi, ligneInconnue string
	for _, ligne := range strings.Split(sortie, "\n") {
		champs := strings.Fields(ligne)
		if len(champs) == 0 {
			continue
		}
		switch champs[0] {
		case "api":
			ligneApi = ligne
		case "inconnue":
			ligneInconnue = ligne
		}
	}
	if ligneApi == "" || ligneInconnue == "" {
		t.Fatalf("les deux sandboxes doivent apparaître chacune sur une ligne ; obtenu :\n%s", sortie)
	}
	if strings.Contains(ligneApi, "?") {
		t.Errorf("le nest déclaré ne doit porter aucune marque ; ligne : %q", ligneApi)
	}
	if !strings.Contains(ligneInconnue, "?") {
		t.Errorf("le nest non déclaré doit être marqué ; ligne : %q", ligneInconnue)
	}
}

// `den ls` ne doit ni échouer ni masquer une VM vivante à cause d'un nest
// cassé, mais il doit AVERTIR sur stderr en le nommant — c'est la dette
// relevée à la relecture de T13 : sans ça, un seul nest cassé fait marquer
// "?" sur toutes les sandboxes, y compris celles dont le nest est valide,
// sans le moindre signal. Le test vérifie les deux sens : le nom du nest
// cassé est sur stderr et PAS sur stdout ; la sandbox dont le nest est sain
// sort bien sur stdout, sans le marqueur "?".
func TestLsSignaleUnNestCasseSurStderrSansMasquerLesSains(t *testing.T) {
	dir := denHomeDeTest(t) // nest "api" y est déclaré, DEN_HOME pointé dessus
	if err := os.WriteFile(filepath.Join(dir, "nests", "casse.yaml"), []byte("egres: [x]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f := &sbx.Fake{Reponses: map[string]sbx.Reponse{
		"ls --json": {Sortie: []byte(
			`{"sandboxes":[{"name":"api","status":"running"}]}`)},
	}}

	stdout, stderr, err := executeCmdAvecSbxFluxSepares(t, f, "ls")
	if err != nil {
		t.Fatalf("den ls ne doit jamais retourner d'erreur pour un nest cassé : %v", err)
	}
	if !strings.Contains(stderr, "casse") {
		t.Errorf("le nest cassé doit être nommé sur stderr ; obtenu :\n%s", stderr)
	}
	if strings.Contains(stdout, "casse") {
		t.Errorf("le nom du nest cassé ne doit pas apparaître sur stdout ; obtenu :\n%s", stdout)
	}

	var ligneApi string
	for _, ligne := range strings.Split(stdout, "\n") {
		champs := strings.Fields(ligne)
		if len(champs) > 0 && champs[0] == "api" {
			ligneApi = ligne
		}
	}
	if ligneApi == "" {
		t.Fatalf("la sandbox 'api', dont le nest est sain, doit apparaître sur stdout ; obtenu :\n%s", stdout)
	}
	if strings.Contains(ligneApi, "?") {
		t.Errorf("le nest sain 'api' ne doit porter aucun marqueur '?' à cause du voisin cassé ; ligne : %q", ligneApi)
	}
}

func TestLsAucuneSandbox(t *testing.T) {
	denHomeDeTest(t)

	f := &sbx.Fake{Reponses: map[string]sbx.Reponse{
		"ls --json": {Sortie: []byte(`{"sandboxes":[]}`)},
	}}

	sortie, err := executeCmdAvecSbx(t, f, "ls")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !strings.Contains(sortie, "aucune sandbox") {
		t.Errorf("obtenu :\n%s", sortie)
	}
	// Aucun tableau vide : le message d'absence remplace l'en-tête, il ne
	// s'ajoute pas au-dessus d'un tableau à zéro ligne.
	if strings.Contains(sortie, "NAME") {
		t.Errorf("aucun en-tête de tableau ne doit apparaître quand la liste est vide ; obtenu :\n%s", sortie)
	}
}
