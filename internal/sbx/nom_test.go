package sbx

import "testing"

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

func TestNomSandboxRefuseComposantsIllegaux(t *testing.T) {
	cas := []struct{ nest, worktree string }{
		{"mon.api", "feat"},    // point dans le nest
		{"api", "feature/123"}, // slash dans le worktree (cas réel : nom de branche)
		{"api", "feat.12"},     // point dans le worktree
		{"", "feat"},           // nest vide
	}
	for _, c := range cas {
		if _, err := NomSandbox(c.nest, c.worktree); err == nil {
			t.Errorf("NomSandbox(%q,%q) doit échouer", c.nest, c.worktree)
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
