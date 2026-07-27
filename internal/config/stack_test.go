package config

import (
	"os"
	"path/filepath"
	"testing"
)

// ecrisStack crée <denHome>/stacks/<nom>/stack.yaml et renvoie denHome.
func ecrisStack(t *testing.T, denHome, nom, contenu string) string {
	t.Helper()
	dir := filepath.Join(denHome, "stacks", nom)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stack.yaml"), []byte(contenu), 0o644); err != nil {
		t.Fatal(err)
	}
	return denHome
}

func TestLoadStack(t *testing.T) {
	denHome := t.TempDir()
	ecrisStack(t, denHome, "dgdevx", `
name: dgdevx
image: dgdevx:v1
parent: devx
kit: ./kit
egress:
  - gitlab.digitaleo.com
`)

	s, err := LoadStack(denHome, "dgdevx")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if s.Name != "dgdevx" || s.Image != "dgdevx:v1" || s.Parent != "devx" {
		t.Errorf("stack = %+v", s)
	}
	if want := filepath.Join(denHome, "stacks", "dgdevx"); s.Dir != want {
		t.Errorf("Dir = %q, attendu %q", s.Dir, want)
	}
	if want := filepath.Join(denHome, "stacks", "dgdevx", "kit"); s.Kit != want {
		t.Errorf("Kit = %q, attendu un chemin absolu %q", s.Kit, want)
	}
	if len(s.Egress) != 1 || s.Egress[0] != "gitlab.digitaleo.com" {
		t.Errorf("Egress = %v", s.Egress)
	}
}

func TestLoadStackNomDeduitDuDossier(t *testing.T) {
	denHome := t.TempDir()
	// `name` absent du YAML : le nom du dossier fait foi.
	ecrisStack(t, denHome, "devx", "image: devx:v1\n")
	s, err := LoadStack(denHome, "devx")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if s.Name != "devx" {
		t.Errorf("Name = %q, attendu %q déduit du dossier", s.Name, "devx")
	}
}

func TestLoadStackAbsente(t *testing.T) {
	if _, err := LoadStack(t.TempDir(), "fantome"); err == nil {
		t.Fatal("attendu une erreur pour une stack absente")
	}
}

func TestLoadStacksToutes(t *testing.T) {
	denHome := t.TempDir()
	ecrisStack(t, denHome, "devx", "image: devx:v1\n")
	ecrisStack(t, denHome, "dgdevx", "image: dgdevx:v1\nparent: devx\n")
	// un dossier sans stack.yaml doit être ignoré silencieusement, pas planter
	if err := os.MkdirAll(filepath.Join(denHome, "stacks", "brouillon"), 0o755); err != nil {
		t.Fatal(err)
	}

	stacks, err := LoadStacks(denHome)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if len(stacks) != 2 {
		t.Fatalf("attendu 2 stacks, obtenu %d : %v", len(stacks), stacks)
	}
	if stacks["dgdevx"].Parent != "devx" {
		t.Errorf("parent de dgdevx = %q", stacks["dgdevx"].Parent)
	}
}

func TestLoadStacksDossierAbsent(t *testing.T) {
	// Pas de dossier stacks/ : ce n'est pas une erreur, c'est un den vide.
	stacks, err := LoadStacks(t.TempDir())
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if len(stacks) != 0 {
		t.Errorf("attendu 0 stack, obtenu %d", len(stacks))
	}
}
