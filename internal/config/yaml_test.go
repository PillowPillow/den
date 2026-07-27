package config

import (
	"strings"
	"testing"
)

// cible est un schéma minimal, indépendant des vrais types de den : ce test
// porte sur le décodeur, pas sur la configuration.
type cible struct {
	Egress []string `yaml:"egress"`
}

func TestDecodeYAMLStrictFranciseLesClesInconnues(t *testing.T) {
	brut := []byte("egress:\n  - a\negres:\n  - b\n") // faute en ligne 3
	var c cible
	err := DecodeYAMLStrict("/den/config.yaml", brut, &c)
	if err == nil {
		t.Fatal("attendu une erreur sur la clé inconnue")
	}
	msg := err.Error()

	if !strings.Contains(msg, `clé inconnue "egres"`) {
		t.Errorf("message = %q, attendu une mention francisée de la clé fautive", msg)
	}
	// Le numéro de ligne est ce qui rend le message utile : il doit survivre.
	if !strings.Contains(msg, "line 3") {
		t.Errorf("message = %q, attendu la ligne de la clé fautive", msg)
	}
	// Le type Go interne n'a rien à faire sous les yeux de l'utilisateur.
	if strings.Contains(msg, "not found in type") || strings.Contains(msg, "config.cible") {
		t.Errorf("message = %q, le type Go interne ne doit plus apparaître", msg)
	}
	// Le format d'origine est conservé.
	if !strings.Contains(msg, "/den/config.yaml : YAML invalide :") {
		t.Errorf("message = %q, attendu le format `%%s : YAML invalide : %%w`", msg)
	}
}

// Plusieurs clés inconnues dans le même fichier : toutes francisées, aucune perdue.
func TestDecodeYAMLStrictFranciseChaqueCleInconnue(t *testing.T) {
	brut := []byte("egres:\n  - a\nworktre_root: /tmp\n")
	var c cible
	err := DecodeYAMLStrict("/den/config.yaml", brut, &c)
	if err == nil {
		t.Fatal("attendu une erreur")
	}
	for _, attendu := range []string{`clé inconnue "egres"`, `clé inconnue "worktre_root"`} {
		if !strings.Contains(err.Error(), attendu) {
			t.Errorf("message = %q, attendu %q", err.Error(), attendu)
		}
	}
}

// Une erreur YAML qui ne suit pas le motif « field X not found in type T » doit
// traverser INTACTE : un message imparfait vaut mieux qu'un message mutilé.
func TestDecodeYAMLStrictLaissePasserUnMessageNonReconnu(t *testing.T) {
	brut := []byte("egress: [ceci n'est pas une liste fermée")
	var c cible
	err := DecodeYAMLStrict("/den/config.yaml", brut, &c)
	if err == nil {
		t.Fatal("attendu une erreur sur YAML malformé")
	}
	if !strings.Contains(err.Error(), "/den/config.yaml : YAML invalide :") {
		t.Errorf("message = %q, attendu le format d'origine", err.Error())
	}
	// Rien n'a été réécrit : le diagnostic de yaml.v3 est le seul disponible.
	if strings.Contains(err.Error(), "clé inconnue") {
		t.Errorf("message = %q : ce n'est pas une erreur de clé inconnue", err.Error())
	}
	if len(err.Error()) <= len("/den/config.yaml : YAML invalide : ") {
		t.Errorf("message = %q : le diagnostic d'origine a été perdu", err.Error())
	}
}
