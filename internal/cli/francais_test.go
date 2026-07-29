package cli

import (
	"strings"
	"testing"
)

// Tout le projet est en français, messages d'erreur compris — sauf la surface
// que cobra et pflag produisent eux-mêmes. Ces tests couvrent les deux chemins
// par lesquels de l'anglais ressort : les erreurs d'analyse de flags, et les
// gabarits d'aide.
//
// Chaque cas exerce la racine ET une sous-commande ET une sous-sous-commande :
// cobra remonte l'arbre pour trouver un gabarit ou un FlagErrorFunc, mais c'est
// une propriété de cobra, pas une garantie du projet. Mesurée ici, pas supposée.

func TestLesErreursDeFlagSontEnFrancais(t *testing.T) {
	cas := []struct {
		nom     string
		args    []string
		attendu []string
		anglais string
	}{
		{
			nom:     "flag long inconnu sur la racine",
			args:    []string{"--zz"},
			attendu: []string{"option inconnue", "--zz"},
			anglais: "unknown flag",
		},
		{
			nom:     "flag long inconnu sur une sous-commande",
			args:    []string{"ls", "--zz"},
			attendu: []string{"option inconnue", "--zz"},
			anglais: "unknown flag",
		},
		{
			nom:     "flag long inconnu sur une sous-sous-commande",
			args:    []string{"nest", "show", "--zz"},
			attendu: []string{"option inconnue", "--zz"},
			anglais: "unknown flag",
		},
		{
			// Le cas mesuré en T3 et T12, et celui qui a ouvert cette dette.
			nom:     "flag court inconnu dans un groupe",
			args:    []string{"nest", "show", "-api"},
			attendu: []string{"option courte inconnue", "a", "-api"},
			anglais: "unknown shorthand flag",
		},
		{
			nom:     "flag sans sa valeur",
			args:    []string{"--agent"},
			attendu: []string{"valeur manquante", "--agent"},
			anglais: "needs an argument",
		},
		{
			// Ces deux branches de traduction n'étaient couvertes par AUCUN
			// test : les supprimer toutes les deux laissait la suite verte.
			nom:     "valeur invalide pour le type du flag",
			args:    []string{"--detach=oui"},
			attendu: []string{"valeur invalide", "oui", "--detach", "attendu : bool"},
			anglais: "invalid argument",
		},
		{
			nom:     "syntaxe de flag invalide",
			args:    []string{"--=x"},
			attendu: []string{"syntaxe d'option invalide", "--=x"},
			anglais: "bad flag syntax",
		},
		{
			nom:     "tirets en trop",
			args:    []string{"---zz"},
			attendu: []string{"syntaxe d'option invalide", "---zz"},
			anglais: "bad flag syntax",
		},
	}
	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Setenv("DEN_HOME", t.TempDir())

			_, err := run(t, c.args...)
			if err == nil {
				t.Fatalf("%v doit être une erreur d'analyse de flags", c.args)
			}
			for _, attendu := range c.attendu {
				if !strings.Contains(err.Error(), attendu) {
					t.Errorf("message = %q, attendu contenant %q", err.Error(), attendu)
				}
			}
			// Sans cette assertion, un message qui CONCATÉNERAIT le français et
			// l'anglais passerait pour francisé.
			if strings.Contains(err.Error(), c.anglais) {
				t.Errorf("message = %q, il reste de l'anglais (%q)", err.Error(), c.anglais)
			}
		})
	}
}

// Le NOMBRE d'arguments est le second producteur de messages anglais, et le
// plus fréquent des deux à l'usage : `den rm` tout court est une faute banale,
// là où `den --=x` demande de la mauvaise volonté. Les validateurs de cobra
// (ExactArgs, NoArgs, MaximumNArgs) rendent « accepts 1 arg(s), received 0 » et
// « unknown command "foo" for "den ls" ».
//
// Les HUIT sites du paquet sont exercés, un par cas : c'est le seul moyen de
// voir qu'un site a été oublié, un validateur français posé sur sept commandes
// sur huit étant indiscernable d'un travail fini.
func TestLesErreursDArgumentsSontEnFrancais(t *testing.T) {
	cas := []struct {
		nom     string
		args    []string
		attendu []string
	}{
		{"racine, trop d'arguments", []string{"api", "foo"}, []string{"den", "au plus un argument", "2 reçus"}},
		{"version, argument en trop", []string{"version", "foo"}, []string{"den version", "aucun argument attendu", "foo"}},
		{"doctor, argument en trop", []string{"doctor", "foo"}, []string{"den doctor", "aucun argument attendu", "foo"}},
		{"ls, argument en trop", []string{"ls", "foo"}, []string{"den ls", "aucun argument attendu", "foo"}},
		{"nest ls, argument en trop", []string{"nest", "ls", "foo"}, []string{"den nest ls", "aucun argument attendu", "foo"}},
		{"nest show, argument manquant", []string{"nest", "show"}, []string{"den nest show", "un argument attendu", "aucun reçu"}},
		{"nest show, deux arguments", []string{"nest", "show", "a", "b"}, []string{"den nest show", "un seul argument", "2 reçus"}},
		{"sh, argument manquant", []string{"sh"}, []string{"den sh", "un argument attendu", "aucun reçu"}},
		{"sh, deux arguments", []string{"sh", "a", "b"}, []string{"den sh", "un seul argument", "2 reçus"}},
		{"rm, argument manquant", []string{"rm"}, []string{"den rm", "un argument attendu", "aucun reçu"}},
	}
	// Les gabarits anglais de cobra, mot pour mot.
	anglais := []string{"accepts", "arg(s)", "received", "unknown command"}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Setenv("DEN_HOME", t.TempDir())

			_, err := run(t, c.args...)
			if err == nil {
				t.Fatalf("%v doit être refusé sur le nombre d'arguments", c.args)
			}
			for _, attendu := range c.attendu {
				if !strings.Contains(err.Error(), attendu) {
					t.Errorf("message = %q, attendu contenant %q", err.Error(), attendu)
				}
			}
			for _, mot := range anglais {
				if strings.Contains(err.Error(), mot) {
					t.Errorf("message = %q, il reste de l'anglais (%q)", err.Error(), mot)
				}
			}
			// Le message doit dire QUOI TAPER : sans la ligne d'utilisation,
			// « un argument attendu » n'apprend pas lequel.
			if !strings.Contains(err.Error(), "utilisation :") {
				t.Errorf("message = %q, attendu rappelant l'utilisation", err.Error())
			}
		})
	}
}

// Un flag d'analyse dont den ne connaît pas la forme doit remonter TEL QUEL
// plutôt que d'être rangé de force dans une des traductions : mieux vaut un
// message anglais exact qu'un message français faux. La garantie porte sur le
// fait que la traduction ne s'applique qu'aux formes qu'elle reconnaît.
func TestUneErreurNonReconnueRemonteIntacte(t *testing.T) {
	root := NewRootCmd()
	erreur := errTest("panne d'un genre inconnu")

	if got := traduitErreurDeFlag(root, erreur); got.Error() != erreur.Error() {
		t.Errorf("traduitErreurDeFlag = %q, attendu l'erreur inchangée %q", got, erreur)
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }

// Les gabarits d'aide de cobra sont en anglais par défaut. Les trois niveaux
// sont exercés : un gabarit posé sur la racine n'est hérité que parce que cobra
// remonte l'arbre, ce qui se mesure.
func TestLesGabaritsDAideSontEnFrancais(t *testing.T) {
	// « Global Flags: » n'apparaît que sur une commande qui hérite d'un flag
	// persistant, « Available Commands: » que sur une commande qui a des
	// enfants : les trois cibles ci-dessous couvrent ensemble les deux.
	cas := []struct {
		nom     string
		args    []string
		attendu []string
	}{
		{"racine", []string{"--help"}, []string{"Utilisation :", "Commandes disponibles :", "Options :"}},
		{"sous-commande sans enfant", []string{"ls", "--help"}, []string{"Utilisation :", "Options globales :"}},
		{"sous-commande avec enfants", []string{"nest", "--help"}, []string{"Utilisation :", "Commandes disponibles :", "Options globales :"}},
		{"sous-sous-commande", []string{"nest", "show", "--help"}, []string{"Utilisation :", "Options globales :"}},
	}
	// Les gabarits anglais de cobra, mot pour mot : c'est leur ABSENCE qui
	// prouve que le gabarit français a bien remplacé celui d'origine, là où la
	// seule présence de français prouverait juste qu'on a ajouté du texte.
	// « [flags] » est ajouté par cobra à la ligne d'usage elle-même, hors
	// gabarit : c'est le mot anglais que le gabarit seul ne suffit pas à
	// retirer, et la mutation qui limite la francisation à la racine le fait
	// réapparaître (en double, avec « [options] »).
	anglais := []string{"Usage:", "Available Commands:", "Flags:", "Global Flags:",
		"Additional help topics:", "for more information about a command", "[flags]"}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Setenv("DEN_HOME", t.TempDir())

			sortie, err := run(t, c.args...)
			if err != nil {
				t.Fatalf("erreur inattendue : %v", err)
			}
			for _, attendu := range c.attendu {
				if !strings.Contains(sortie, attendu) {
					t.Errorf("aide = %q, attendu contenant %q", sortie, attendu)
				}
			}
			for _, mot := range anglais {
				if strings.Contains(sortie, mot) {
					t.Errorf("l'aide contient encore le gabarit anglais %q ; aide = %q", mot, sortie)
				}
			}
		})
	}
}

// Les descriptions que cobra GÉNÈRE lui-même — le flag --help de chaque
// commande, les commandes `help` et `completion` ajoutées automatiquement —
// sont en anglais et n'appartiennent à aucun gabarit : elles ne sont pas
// couvertes par le test précédent, et c'est le premier écran que voit
// l'utilisateur qui tape `den`.
func TestLesTextesGeneresParCobraSontEnFrancais(t *testing.T) {
	t.Setenv("DEN_HOME", t.TempDir())

	sortie, err := run(t, "--help")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	for _, anglais := range []string{
		"help for den",                // usage du flag --help
		"Help about any command",      // Short de la commande `help`
		"Generate the autocompletion", // Short de la commande `completion`
		"the specified shell",         // suite du même Short
	} {
		if strings.Contains(sortie, anglais) {
			t.Errorf("l'aide contient encore %q ; aide = %q", anglais, sortie)
		}
	}
	// Et la contrepartie : les deux commandes doivent TOUJOURS être là. Les
	// faire disparaître franciserait l'aide en supprimant des fonctions.
	for _, commande := range []string{"help", "completion"} {
		if !strings.Contains(sortie, commande) {
			t.Errorf("la commande %q a disparu de l'aide ; aide = %q", commande, sortie)
		}
	}
}

// Les commandes par shell sont générées par cobra SOUS `completion`, et leur
// description s'affiche dès `den completion`. Elles échappent au test
// précédent, qui ne regarde que l'aide de la racine.
func TestLesCommandesDeCompletionSontDecritesEnFrancais(t *testing.T) {
	t.Setenv("DEN_HOME", t.TempDir())

	sortie, err := run(t, "completion", "--help")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if strings.Contains(sortie, "Generate the autocompletion script for") {
		t.Errorf("les commandes par shell sont restées en anglais ; aide = %q", sortie)
	}
	// --no-descriptions est posé par cobra sur les seules commandes de shell,
	// et n'apparaît donc dans aucune autre aide.
	zsh, err := run(t, "completion", "zsh", "--help")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if strings.Contains(zsh, "disable completion descriptions") {
		t.Errorf("l'usage de --no-descriptions est resté en anglais ; aide = %q", zsh)
	}
	if !strings.Contains(zsh, "--no-descriptions") {
		t.Errorf("le flag --no-descriptions a disparu ; aide = %q", zsh)
	}
	// Les quatre shells doivent toujours être proposés : les faire disparaître
	// ferait passer ce test sans rien franciser.
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		if !strings.Contains(sortie, shell) {
			t.Errorf("le shell %q a disparu de `den completion` ; aide = %q", shell, sortie)
		}
	}
}

// L'aide d'une sous-commande passe par le même chemin, et le flag --help y est
// construit séparément (cobra en pose un par commande).
func TestLAideDUneSousCommandeDecritSonFlagHelpEnFrancais(t *testing.T) {
	t.Setenv("DEN_HOME", t.TempDir())

	sortie, err := run(t, "nest", "show", "--help")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if strings.Contains(sortie, "help for") {
		t.Errorf("l'usage du flag --help est resté en anglais ; aide = %q", sortie)
	}
}
