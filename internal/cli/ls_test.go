package cli

import (
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/sbx"
)

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
	for _, attendu := range []string{"NAME", "NEST", "WORKTREE", "STATUS", "api.feat12", "api", "feat12", "running"} {
		if !strings.Contains(sortie, attendu) {
			t.Errorf("la sortie doit contenir %q ; obtenu :\n%s", attendu, sortie)
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
