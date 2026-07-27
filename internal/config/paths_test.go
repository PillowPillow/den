package config

import (
	"os"
	"path/filepath"
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
