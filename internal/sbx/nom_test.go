package sbx

import (
	"strings"
	"testing"
)

func TestNomSandbox(t *testing.T) {
	cas := []struct {
		nest, worktree, attendu string
	}{
		{"api", "", "api"},
		{"api", "feat12", "api.feat12"},
		{"mon-api", "feat", "mon-api.feat"},
	}
	for _, c := range cas {
		got, err := NomSandbox(c.nest, c.worktree)
		if err != nil {
			t.Errorf("NomSandbox(%q,%q) : erreur inattendue %v", c.nest, c.worktree, err)
			continue
		}
		if got != c.attendu {
			t.Errorf("NomSandbox(%q,%q) = %q, attendu %q", c.nest, c.worktree, got, c.attendu)
		}
	}
}

// genreAttendu verrouille QUEL composant a été rejeté : une inversion des
// arguments à l'intérieur de NomSandbox (par exemple valider le worktree avec
// le genre "nest") laisserait passer un test qui ne vérifie que la présence
// d'une erreur, tout en rendant un message trompeur à l'appelant.
func TestNomSandboxRefuseComposantsIllegaux(t *testing.T) {
	cas := []struct{ nest, worktree, genreAttendu string }{
		{"mon.api", "feat", "nest"},        // point dans le nest
		{"api", "feature/123", "worktree"}, // slash dans le worktree (cas réel : nom de branche)
		{"api", "feat.12", "worktree"},     // point dans le worktree
		{"", "feat", "nest"},               // nest vide
	}
	for _, c := range cas {
		_, err := NomSandbox(c.nest, c.worktree)
		if err == nil {
			t.Errorf("NomSandbox(%q,%q) doit échouer", c.nest, c.worktree)
			continue
		}
		if !strings.Contains(err.Error(), c.genreAttendu) {
			t.Errorf("NomSandbox(%q,%q) : erreur %q doit mentionner le genre %q",
				c.nest, c.worktree, err.Error(), c.genreAttendu)
		}
	}
}

// L'aller-retour est l'invariant central : sans --label, le nom EST l'état.
func TestDecomposeNomEstLInverseExact(t *testing.T) {
	cas := []struct{ nest, worktree string }{
		{"api", ""},
		{"api", "feat12"},
		{"mon-api", "feat-2"},
		{"a+b", "c-d"},
	}
	for _, c := range cas {
		nom, err := NomSandbox(c.nest, c.worktree)
		if err != nil {
			t.Fatalf("NomSandbox(%q,%q) : %v", c.nest, c.worktree, err)
		}
		nest, wt := DecomposeNom(nom)
		if nest != c.nest || wt != c.worktree {
			t.Errorf("aller-retour de %q : obtenu (%q,%q), attendu (%q,%q)",
				nom, nest, wt, c.nest, c.worktree)
		}
	}
}

// Une sandbox créée à la main hors den ne doit pas faire paniquer la décomposition.
func TestDecomposeNomEtranger(t *testing.T) {
	nest, wt := DecomposeNom("sandbox-cree-a-la-main")
	if nest != "sandbox-cree-a-la-main" || wt != "" {
		t.Errorf("obtenu (%q,%q)", nest, wt)
	}
	// Deux points : seul le premier sépare, le reste appartient au worktree —
	// den ne produit jamais ça, mais den ls doit rester total.
	nest, wt = DecomposeNom("a.b.c")
	if nest != "a" || wt != "b.c" {
		t.Errorf("obtenu (%q,%q), attendu (a, b.c)", nest, wt)
	}
}

// ValiderNomSandbox est la source unique du verdict « ce nom est-il celui que
// den aurait construit ? ». Elle est exportée pour que `internal/agent` et
// l'assemblage de l'argv ne puissent pas diverger : ils ont divergé, et le cas
// « api. » passait d'un côté et pas de l'autre.
func TestValiderNomSandbox(t *testing.T) {
	for _, nom := range []string{"api", "api.feat12", "mon-api.feat-2", "api2", "a"} {
		if err := ValiderNomSandbox(nom); err != nil {
			t.Errorf("%q doit être accepté : %v", nom, err)
		}
	}

	// Les formes non canoniques : elles se reconstruiraient en autre chose
	// qu'elles-mêmes, donc deux noms distincts désigneraient la même sandbox.
	refuses := []string{
		"",             // vide
		"api.",         // se reconstruit en « api » — le trou de la validation par composant
		".feat12",      // nest vide
		"api..feat",    // worktree « .feat », séparateur en tête
		"api.feat.sup", // le séparateur ne se répète pas
		"mon_api",      // charset
		"-api",         // indiscernable d'un flag
	}
	for _, nom := range refuses {
		if err := ValiderNomSandbox(nom); err == nil {
			t.Errorf("%q doit être refusé", nom)
		}
	}
}

// La propriété qui tient l'ensemble : ValiderNomSandbox accepte EXACTEMENT
// l'image de NomSandbox, ni plus ni moins.
func TestValiderNomSandboxAccepteLImageDeNomSandbox(t *testing.T) {
	for _, nest := range []string{"api", "mon-api", "a1", "x+y"} {
		for _, worktree := range []string{"", "feat", "feat-2", "mon_wt", "-wt", ""} {
			nom, err := NomSandbox(nest, worktree)
			if err != nil {
				continue // NomSandbox a déjà refusé : rien à croiser
			}
			if err := ValiderNomSandbox(nom); err != nil {
				t.Errorf("NomSandbox(%q,%q) = %q mais ValiderNomSandbox le refuse : %v",
					nest, worktree, nom, err)
			}
		}
	}
}
