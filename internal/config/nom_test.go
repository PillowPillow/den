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

func TestValiderComposantSandbox(t *testing.T) {
	valides := []string{"api", "mon-api", "api2", "v1+beta", "A-B"}
	for _, nom := range valides {
		if err := ValiderComposantSandbox("nest", nom); err != nil {
			t.Errorf("%q doit être accepté, refusé avec : %v", nom, err)
		}
	}

	// Le point est réservé au séparateur <nest>.<worktree> ; l'underscore et le
	// slash sont refusés par `sbx create --name` lui-même. Un nom qui commence
	// par « - » ou « + » est indiscernable d'un flag pour sbx comme pour den
	// (`den nest show -api` échoue sur un flag inconnu avant même d'atteindre
	// le nest) : -api, --, +, - sont donc tous refusés, alors qu'ils étaient
	// acceptés par l'ancien charset `[A-Za-z0-9+-]+` sans contrainte de position.
	invalides := []string{
		"", "mon.api", "mon_api", "feature/123", "mon api", "café",
		"-api", "--", "+", "-",
	}
	for _, nom := range invalides {
		if err := ValiderComposantSandbox("nest", nom); err == nil {
			t.Errorf("%q doit être refusé", nom)
		}
	}
}

// Le message doit nommer le caractère fautif : « invalide » sans dire quoi
// oblige l'utilisateur à deviner.
func TestValiderComposantSandboxMessageActionnable(t *testing.T) {
	err := ValiderComposantSandbox("worktree", "feature/123")
	if err == nil {
		t.Fatal("attendu une erreur")
	}
	for _, attendu := range []string{"worktree", "feature/123", "/"} {
		if !strings.Contains(err.Error(), attendu) {
			t.Errorf("le message doit contenir %q ; obtenu : %v", attendu, err)
		}
	}
}

// Un nom mal placé (« -api ») n'a pas de caractère interdit : « - » est
// autorisé ailleurs dans le nom. Dire « le caractère "-" est interdit » serait
// donc faux ; le message doit parler de position, pas de charset.
func TestValiderComposantSandboxMessagePremierCaractere(t *testing.T) {
	err := ValiderComposantSandbox("nest", "-api")
	if err == nil {
		t.Fatal("attendu une erreur")
	}
	for _, attendu := range []string{"nest", "-api"} {
		if !strings.Contains(err.Error(), attendu) {
			t.Errorf("le message doit contenir %q ; obtenu : %v", attendu, err)
		}
	}
	if strings.Contains(err.Error(), "est interdit") {
		t.Errorf(
			"« -api » ne doit pas dire que « - » est interdit, il est juste mal placé ; obtenu : %v",
			err.Error())
	}
}
