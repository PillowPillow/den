package config

import (
	"strings"
	"testing"
)

func TestValiderNomAccepteLesNomsUsuels(t *testing.T) {
	for _, nom := range []string{"devx", "dgdevx", "web-2", "front_app", "api.v2"} {
		if err := ValiderNom("stack", nom); err != nil {
			t.Errorf("ValiderNom(%q) = %v, attendu accepté", nom, err)
		}
	}
}

func TestValiderNomRejetteCeQuiSortDeDenHome(t *testing.T) {
	cas := []struct {
		nom     string
		attendu string
	}{
		{"", "vide"},
		{"../../../../etc/passwd", "séparateur"},
		{"a/b", "séparateur"},
		{"..", "réservé"},
		{".", "réservé"},
	}
	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			err := ValiderNom("nest", c.nom)
			if err == nil {
				t.Fatalf("ValiderNom(%q) = nil, attendu un rejet", c.nom)
			}
			if !strings.Contains(err.Error(), c.attendu) {
				t.Errorf("erreur = %q, attendu une mention de %q", err.Error(), c.attendu)
			}
			if !strings.Contains(err.Error(), "nest") {
				t.Errorf("erreur = %q, attendu une mention du genre d'objet", err.Error())
			}
		})
	}
}
