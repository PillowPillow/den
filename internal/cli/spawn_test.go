package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// La racine devient la commande de spawn : les sous-commandes existantes ne
// doivent surtout pas être avalées comme des noms de nest.
//
// DEN_HOME est épinglé sur un dossier vide dans TOUS les tests de ce fichier :
// si la racine capturait un jeton qu'elle ne devrait pas, le spawn partirait
// sur le ~/.den RÉEL de la machine — et sur le vrai `sbx`.
func TestLesSousCommandesRestentPrioritaires(t *testing.T) {
	t.Setenv("DEN_HOME", t.TempDir())

	sortie, err := run(t, "version")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !strings.HasPrefix(sortie, "den ") {
		t.Errorf("`den version` doit rester la commande version ; obtenu : %q", sortie)
	}
}

func TestDenSansArgumentAfficheLAide(t *testing.T) {
	t.Setenv("DEN_HOME", t.TempDir())

	sortie, err := run(t)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !strings.Contains(sortie, "nest") {
		t.Errorf("`den` seul doit afficher l'aide ; obtenu : %q", sortie)
	}
}

// Le câblage de bout en bout : args[0] devient le nest, et --den-home est bien
// celui que le spawn consulte. Un den home vide fait échouer la toute première
// étape (lecture de config.yaml), ce qui suffit à nommer le dossier consulté
// sans que `sbx` — absent de cette machine — soit jamais sollicité.
func TestDenNestRouteVersLeSpawn(t *testing.T) {
	t.Setenv("DEN_HOME", t.TempDir())
	dir := t.TempDir()

	if _, err := run(t, "api", "--den-home", dir); err == nil {
		t.Fatal("un den home vide doit faire échouer le spawn")
	} else if !strings.Contains(err.Error(), filepath.Join(dir, "config.yaml")) {
		t.Errorf("le spawn doit consulter le --den-home donné ; obtenu : %v", err)
	}
}

// Sans flag, la résolution du den home doit passer par config.Home (donc par
// DEN_HOME, puis ~/.den). Ce cas est celui qui distingue « on appelle
// config.Home » de « on passe la valeur brute du flag » : brute, elle vaut ""
// et le spawn irait lire un « config.yaml » relatif au cwd.
func TestDenNestSansFlagPasseParDenHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEN_HOME", dir)

	if _, err := run(t, "api"); err == nil {
		t.Fatal("un den home vide doit faire échouer le spawn")
	} else if !strings.Contains(err.Error(), filepath.Join(dir, "config.yaml")) {
		t.Errorf("le spawn doit résoudre le den home via DEN_HOME ; obtenu : %v", err)
	}
}
