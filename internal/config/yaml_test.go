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

// Un fichier MULTI-DOCUMENTS ne doit pas se laisser lire à moitié.
//
// yaml.Decoder.Decode ne lit QU'UN document ; tout ce qui suit un « --- » était
// silencieusement ignoré. C'est exactement le mode de défaillance que
// DecodeYAMLStrict existe pour empêcher — une allowlist qui ne s'applique pas
// sans un mot — simplement à la granularité du document au lieu de la clé.
//
// Mesuré sur le binaire assemblé (tâche 17c), sur un nest de deux documents :
// `den nest show api` rendait rc=0 et n'affichait que le premier, le `stack:` et
// tout un bloc `egress:` du second n'ayant jamais existé pour den.
func TestDecodeYAMLStrictRefuseUnFichierMultiDocuments(t *testing.T) {
	brut := []byte("egress:\n  - premier\n---\negress:\n  - second\n")
	var c cible
	err := DecodeYAMLStrict("/den/nests/api.yaml", brut, &c)
	if err == nil {
		t.Fatalf("multi-documents accepté : le second document est ignoré en silence "+
			"(egress lu = %v)", c.Egress)
	}
	msg := err.Error()
	// Le fichier à corriger, et le mot qui dit QUOI corriger : « YAML invalide »
	// seul enverrait chercher une faute de syntaxe, alors que le fichier est
	// syntaxiquement correct.
	if !strings.Contains(msg, "/den/nests/api.yaml") {
		t.Errorf("message = %q, doit nommer le fichier fautif", msg)
	}
	if !strings.Contains(msg, "document") {
		t.Errorf("message = %q, doit dire que le fichier porte plusieurs documents", msg)
	}
}

// Le pendant : un document unique suivi d'un marqueur de fin, ou un fichier qui
// ne contient qu'un document, restent acceptés. Sans ce test, un contrôle qui
// refuserait tout fichier contenant « --- » passerait le test ci-dessus — et
// casserait les configurations qui ouvrent par un marqueur de début de document,
// une forme parfaitement légale.
func TestDecodeYAMLStrictAccepteUnDocumentUniqueMemeAvecMarqueur(t *testing.T) {
	cas := []struct {
		nom  string
		brut string
	}{
		{"sans marqueur", "egress:\n  - a\n"},
		{"marqueur de debut", "---\negress:\n  - a\n"},
		{"marqueur de debut et de fin", "---\negress:\n  - a\n...\n"},
		{"document vide", ""},
		{"commentaires seuls", "# rien que des commentaires\n"},
	}
	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			var dest cible
			if err := DecodeYAMLStrict("/den/config.yaml", []byte(c.brut), &dest); err != nil {
				t.Errorf("forme légale refusée : %v", err)
			}
		})
	}
}

// Les formes qui ne perdent RIEN mais restent refusées.
//
// Mesuré, second `Decode` réussi mais document ré-encodé VIDE dans les trois
// cas : un « --- » final sans contenu derrière, la forme front-matter
// « ---\n…\n--- », et un « --- » suivi de commentaires seuls. Rien n'y est
// ignoré, contrairement à ce qu'affirmait la première version du message.
//
// Le refus est CONSERVÉ — il est fail-closed, « ... » est le vrai marqueur de
// fin de document, et l'ambiguïté ne vaut pas d'être tolérée — mais le message
// ne doit pas affirmer une perte qui n'a pas lieu. L'habitude front-matter est
// assez répandue pour que le cas se présente, et un utilisateur bloqué sur un
// fichier sans faute réelle AVEC une explication fausse est exactement ce que
// l'écart C de la tâche 17c existait pour supprimer.
func TestDecodeYAMLStrictRefuseSansAffirmerUnePerteQuiNAPasLieu(t *testing.T) {
	cas := []struct {
		nom  string
		brut string
	}{
		{"marqueur --- final sans contenu", "egress:\n  - a\n---\n"},
		{"front-matter (--- … ---)", "---\negress:\n  - a\n---\n"},
		{"--- suivi de commentaires seuls", "egress:\n  - a\n---\n# rien que des commentaires\n"},
	}
	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			var dest cible
			err := DecodeYAMLStrict("/den/nests/api.yaml", []byte(c.brut), &dest)
			// Fail-closed, assumé et verrouillé : ces formes RESTENT refusées.
			if err == nil {
				t.Fatal("forme multi-documents acceptée : le refus est délibérément fail-closed")
			}
			msg := err.Error()
			// L'AFFIRMATION FAUSSE, interdite : ici, rien n'est ignoré.
			if strings.Contains(msg, "ignorés en silence") {
				t.Errorf("le message affirme une perte qui n'a PAS lieu — le document suivant est "+
					"vide (mesuré) ; obtenu : %s", msg)
			}
			if !strings.Contains(msg, "/den/nests/api.yaml") {
				t.Errorf("le message doit nommer le fichier fautif ; obtenu : %s", msg)
			}
			// Et il doit dire QUOI faire, le fichier n'ayant aucune faute de syntaxe.
			if !strings.Contains(msg, "document") {
				t.Errorf("le message doit parler de documents ; obtenu : %s", msg)
			}
		})
	}
}
