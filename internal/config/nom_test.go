package config

import (
	"path/filepath"
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

func TestAplatitComposantSandbox(t *testing.T) {
	cas := []struct{ nom, attendu string }{
		{"feature/123", "feature-123"}, // le cas réel : un nom de branche
		{"feat/essai", "feat-essai"},
		{"mon_wt", "mon-wt"},
		{"feat.12", "feat-12"}, // le point est le séparateur <nest>.<worktree>
		{"café", "caf-"},
		{"feat//x", "feat--x"}, // pas de fusion des séries : un caractère, un tiret
		{"feat12", "feat12"},   // déjà valide : rendu tel quel
		{"v1+beta", "v1+beta"},
		{"A-B", "A-B"},
	}
	for _, c := range cas {
		got, err := AplatitComposantSandbox("worktree", c.nom)
		if err != nil {
			t.Errorf("AplatitComposantSandbox(%q) : erreur inattendue %v", c.nom, err)
			continue
		}
		if got != c.attendu {
			t.Errorf("AplatitComposantSandbox(%q) = %q, attendu %q", c.nom, got, c.attendu)
		}
	}
}

// L'invariant qui justifie l'existence de cette fonction : ce qu'elle rend est
// toujours acceptable par ValiderComposantSandbox. C'est ce qui autorise le
// reste de den — sbx.NomSandbox, l'argv de `sbx create`, la policy — à rester
// STRICT : l'aplatissement vit en amont d'eux, il ne les assouplit pas.
func TestAplatitComposantSandboxRendToujoursUnComposantValide(t *testing.T) {
	for _, nom := range []string{
		"feature/123", "mon_wt", "feat.12", "café", "feat//x", "a b", "wt!", "é1",
		"api", "x", strings.Repeat("a/b", 20),
	} {
		got, err := AplatitComposantSandbox("worktree", nom)
		if err != nil {
			continue // refusé : rien à croiser
		}
		if err := ValiderComposantSandbox("worktree", got); err != nil {
			t.Errorf("AplatitComposantSandbox(%q) = %q, que ValiderComposantSandbox refuse : %v",
				nom, got, err)
		}
	}
}

// Corollaire du précédent, énoncé à part parce que c'est LUI qui protège
// worktree.Chemin : un composant qui garderait un séparateur de chemin ferait
// naître le worktree dans un sous-dossier — voire hors de worktree_root.
func TestAplatitComposantSandboxNeRendJamaisDeSeparateurDeChemin(t *testing.T) {
	for _, nom := range []string{"feature/123", "../evade", "a/b/c", `a\b`} {
		got, err := AplatitComposantSandbox("worktree", nom)
		if err != nil {
			continue
		}
		if strings.ContainsRune(got, '/') || strings.ContainsRune(got, filepath.Separator) {
			t.Errorf("AplatitComposantSandbox(%q) = %q : contient un séparateur de chemin", nom, got)
		}
	}
}

// Aplatir n'est pas inventer : un nom dont le premier caractère n'est pas
// alphanumérique est REFUSÉ, jamais préfixé d'office. L'utilisateur retrouve
// dans le message le nom qu'il a tapé, pas celui que den aurait choisi.
func TestAplatitComposantSandboxRefuseCeQuIlNePeutPasAplatir(t *testing.T) {
	for _, nom := range []string{"", "-wip", "/x", "_x", ".hidden"} {
		if got, err := AplatitComposantSandbox("worktree", nom); err == nil {
			t.Errorf("AplatitComposantSandbox(%q) = %q, attendu un refus", nom, got)
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
