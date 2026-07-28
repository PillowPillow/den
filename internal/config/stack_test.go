package config

import (
	"os"
	"path/filepath"
	"strings"
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

// Le nom d'une stack est le nom de son dossier — cas nominal et unique.
func TestLoadStackNomDeduitDuDossier(t *testing.T) {
	denHome := t.TempDir()
	ecrisStack(t, denHome, "devx", "image: devx:v1\n")
	s, err := LoadStack(denHome, "devx")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if s.Name != "devx" {
		t.Errorf("Name = %q, attendu %q déduit du dossier", s.Name, "devx")
	}
}

// Même règle que pour les nests : une stack ne porte pas son nom dans son
// contenu. LoadStacks indexait sa map par ce `name:`, alors que LoadStack
// cherche par nom de dossier — deux clés pour un même objet.
func TestLoadStackRejetteUnNomDansLeContenu(t *testing.T) {
	denHome := t.TempDir()
	ecrisStack(t, denHome, "devx", "name: autre\nimage: devx:v1\n")
	_, err := LoadStack(denHome, "devx")
	if err == nil {
		t.Fatal("attendu un rejet : l'identité d'une stack vient de son dossier, pas de son contenu")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("erreur = %q, attendu une mention de la clé `name`", err.Error())
	}
}

// LoadStacks doit indexer par le nom de dossier, la seule identité qui existe :
// c'est par cette clé que defaults.stack et nest.stack sont résolus.
func TestLoadStacksIndexeParLeNomDeDossier(t *testing.T) {
	denHome := t.TempDir()
	ecrisStack(t, denHome, "devx", "image: devx:v1\n")

	stacks, err := LoadStacks(denHome)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	s, ok := stacks["devx"]
	if !ok {
		t.Fatalf("stacks = %v, attendu une entrée sous le nom de dossier %q", stacks, "devx")
	}
	if s.Name != "devx" {
		t.Errorf("Name = %q, attendu %q", s.Name, "devx")
	}
}

func TestLoadStackRejetteUneCleInconnue(t *testing.T) {
	denHome := t.TempDir()
	ecrisStack(t, denHome, "devx", "image: devx:v1\negres: [github.com]\n")
	_, err := LoadStack(denHome, "devx")
	if err == nil {
		t.Fatal("attendu une erreur sur la clé inconnue `egres`")
	}
	if !strings.Contains(err.Error(), "egres") {
		t.Errorf("erreur = %q, attendu une mention de la clé fautive", err.Error())
	}
}

func TestLoadStackFichierVide(t *testing.T) {
	denHome := t.TempDir()
	ecrisStack(t, denHome, "devx", "")
	s, err := LoadStack(denHome, "devx")
	if err != nil {
		t.Fatalf("un stack.yaml vide ne doit pas être une erreur de chargement : %v", err)
	}
	if s.Name != "devx" {
		t.Errorf("Name = %q, attendu %q déduit du dossier", s.Name, "devx")
	}
}

// Une stack absente et une stack illisible appellent deux gestes différents :
// « déclare-la » contre « répare les droits ». `doctor` relaie ce message tel
// quel, il doit donc trancher.
func TestLoadStackAbsente(t *testing.T) {
	denHome := t.TempDir()
	_, err := LoadStack(denHome, "fantome")
	if err == nil {
		t.Fatal("attendu une erreur pour une stack absente")
	}
	if !strings.Contains(err.Error(), "introuvable") {
		t.Errorf("erreur = %q, attendu un message d'absence explicite", err.Error())
	}
	if !strings.Contains(err.Error(), filepath.Join(denHome, "stacks", "fantome")) {
		t.Errorf("erreur = %q, attendu le chemin attendu de la stack", err.Error())
	}
}

func TestLoadStackIllisible(t *testing.T) {
	denHome := t.TempDir()
	// stack.yaml présent mais illisible (ici : c'est un dossier) — ce n'est pas
	// une absence, et le message ne doit pas le prétendre.
	if err := os.MkdirAll(filepath.Join(denHome, "stacks", "devx", "stack.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := LoadStack(denHome, "devx")
	if err == nil {
		t.Fatal("attendu une erreur pour une stack illisible")
	}
	if strings.Contains(err.Error(), "introuvable") {
		t.Errorf("erreur = %q : la stack existe, elle est illisible", err.Error())
	}
	if !strings.Contains(err.Error(), "lecture") {
		t.Errorf("erreur = %q, attendu un message d'erreur de lecture", err.Error())
	}
}

func TestLoadStackRefuseUnNomQuiSortDeDenHome(t *testing.T) {
	racine := t.TempDir()
	denHome := filepath.Join(racine, "home")
	// Une stack parfaitement valide, mais HORS du den home.
	ecrisStack(t, racine, "dehors", "image: dehors:v1\n")

	// <denHome>/stacks/../../stacks/dehors == <racine>/stacks/dehors
	if _, err := LoadStack(denHome, "../../stacks/dehors"); err == nil {
		t.Error("LoadStack a chargé une stack située hors du den home")
	}
	if _, err := LoadStack(denHome, ".."); err == nil {
		t.Error("LoadStack(\"..\") = nil, attendu un rejet")
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

func TestLoadStackResoutLesKitsTransverses(t *testing.T) {
	denHome := t.TempDir()
	ecrisStack(t, denHome, "devx", `image: devx:v1
kit: ./kit
kits:
  - ../../kits/ssh-known-hosts
  - /absolu/deja
`)

	s, err := LoadStack(denHome, "devx")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	attendus := []string{
		filepath.Join(denHome, "kits", "ssh-known-hosts"),
		"/absolu/deja",
	}
	if len(s.Kits) != len(attendus) {
		t.Fatalf("Kits = %v, attendu %d entrées", s.Kits, len(attendus))
	}
	for i, a := range attendus {
		if s.Kits[i] != a {
			t.Errorf("Kits[%d] = %q, attendu %q", i, s.Kits[i], a)
		}
	}
}

// L'ordre est un ordre de LAYERING : le trier casserait la sémantique.
func TestLoadStackPreserveLOrdreDesKits(t *testing.T) {
	denHome := t.TempDir()
	ecrisStack(t, denHome, "devx", `image: devx:v1
kits: [./z-dernier, ./a-premier]
`)

	s, err := LoadStack(denHome, "devx")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if filepath.Base(s.Kits[0]) != "z-dernier" || filepath.Base(s.Kits[1]) != "a-premier" {
		t.Errorf("l'ordre déclaré doit être préservé ; obtenu %v", s.Kits)
	}
}

func TestLoadStackSansKits(t *testing.T) {
	denHome := t.TempDir()
	ecrisStack(t, denHome, "devx", "image: devx:v1\n")

	s, err := LoadStack(denHome, "devx")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if len(s.Kits) != 0 {
		t.Errorf("Kits = %v, attendu vide", s.Kits)
	}
}
