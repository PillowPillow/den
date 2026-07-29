package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHomePriorites(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("le flag gagne sur tout", func(t *testing.T) {
		t.Setenv("DEN_HOME", "/depuis/env")
		got, err := Home("/depuis/flag")
		if err != nil {
			t.Fatal(err)
		}
		if got != "/depuis/flag" {
			t.Errorf("Home = %q, attendu %q", got, "/depuis/flag")
		}
	})

	t.Run("sinon DEN_HOME", func(t *testing.T) {
		t.Setenv("DEN_HOME", "/depuis/env")
		got, err := Home("")
		if err != nil {
			t.Fatal(err)
		}
		if got != "/depuis/env" {
			t.Errorf("Home = %q, attendu %q", got, "/depuis/env")
		}
	})

	// Les chemins issus de Home() partent vers `git worktree` et `sbx create` au
	// plan suivant, avec un cwd qui n'est plus garanti : ils doivent être absolus
	// dès leur résolution, jamais plus tard.
	t.Run("un flag relatif est absolutise", func(t *testing.T) {
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		got, err := Home("denh")
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(cwd, "denh"); got != want {
			t.Errorf("Home = %q, attendu %q", got, want)
		}
	})

	t.Run("un DEN_HOME relatif est absolutise", func(t *testing.T) {
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("DEN_HOME", "denh")
		got, err := Home("")
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(cwd, "denh"); got != want {
			t.Errorf("Home = %q, attendu %q", got, want)
		}
	})

	t.Run("sinon ~/.den", func(t *testing.T) {
		t.Setenv("DEN_HOME", "")
		got, err := Home("")
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(home, ".den")
		if got != want {
			t.Errorf("Home = %q, attendu %q", got, want)
		}
	})
}

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	cas := []struct {
		nom  string
		in   string
		want string
	}{
		{"tilde seul", "~", home},
		{"tilde en tete", "~/dev/projet", filepath.Join(home, "dev/projet")},
		{"chemin absolu inchange", "/opt/truc", "/opt/truc"},
		{"chemin relatif inchange", "dev/projet", "dev/projet"},
		{"vide inchange", "", ""},
		// $HOME vise le home DE LA VM : den ne doit jamais le resoudre cote hote.
		{"dollar HOME preserve", "$HOME/.local/bin", "$HOME/.local/bin"},
		// un tilde qui n'est pas en tete n'est pas un home.
		{"tilde milieu preserve", "/opt/~/truc", "/opt/~/truc"},
		{"tilde utilisateur non supporte", "~bob/x", "~bob/x"},
	}
	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			got, err := ExpandPath(c.in)
			if err != nil {
				t.Fatalf("erreur inattendue : %v", err)
			}
			if got != c.want {
				t.Errorf("ExpandPath(%q) = %q, attendu %q", c.in, got, c.want)
			}
		})
	}
}

// $HOME absent de l'environnement : den doit dire CE QU'IL cherchait et
// COMMENT s'en passer. Trouvé en exerçant le binaire assemblé (tâche 17c).
//
// Sortie RÉELLE avant correctif, `env -u HOME den nest ls` :
//
//	den: $HOME is not defined
//
// Le message vient tel quel d'os.UserHomeDir : en anglais dans une CLI
// francophone, et surtout SANS CONTEXTE — il ne dit ni que den cherchait le
// dossier de configuration, ni que --den-home et DEN_HOME existent et
// résolvent le problème sur-le-champ. Le cas se produit pour de bon sous
// systemd, dans un conteneur, ou dans un cron.
func TestHomeSansHOMEDitCeQuIlCherchaitEtCommentSEnPasser(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("DEN_HOME", "")

	_, err := Home("")
	if err == nil {
		t.Skip("os.UserHomeDir résout un home malgré HOME vide : cas non exerçable ici")
	}
	msg := err.Error()
	for _, attendu := range []string{"~/.den", "DEN_HOME", "--den-home"} {
		if !strings.Contains(msg, attendu) {
			t.Errorf("le message doit mentionner %q — c'est la sortie de secours ; obtenu : %s",
				attendu, msg)
		}
	}
	// La cause d'origine reste inspectable : le message la reformule, il ne la
	// remplace pas.
	if !strings.Contains(msg, "HOME") {
		t.Errorf("la cause d'origine doit survivre dans le message ; obtenu : %s", msg)
	}
}

// Même exigence pour ExpandPath, l'autre porte d'entrée du home : elle expanse
// les « ~ » des config_dir, ssh.dir et repos. Sans contexte, l'utilisateur lit
// « $HOME is not defined » sans savoir quel champ de sa configuration porte le
// tilde fautif.
func TestExpandPathSansHOMENommeLeCheminFautif(t *testing.T) {
	t.Setenv("HOME", "")

	_, err := ExpandPath("~/.den/agents/claude")
	if err == nil {
		t.Skip("os.UserHomeDir résout un home malgré HOME vide : cas non exerçable ici")
	}
	if !strings.Contains(err.Error(), "~/.den/agents/claude") {
		t.Errorf("le message doit citer le chemin à expanser ; obtenu : %v", err)
	}
	if !strings.Contains(err.Error(), "HOME") {
		t.Errorf("la cause d'origine doit survivre ; obtenu : %v", err)
	}
}
