package nest

import (
	"os"
	"path/filepath"
	"testing"
)

func ecrisNest(t *testing.T, denHome, nom, contenu string) {
	t.Helper()
	dir := filepath.Join(denHome, "nests")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, nom+".yaml"), []byte(contenu), 0o644); err != nil {
		t.Fatal(err)
	}
}

const nestComplet = `
name: fullstack
stack: dgdevx
env:
  SOME_VAR: value
egress:
  - 10.22.11.54:27017
repos:
  - { path: ~/dev/review-mgmt }
  - { path: ~/dev/front-app, optional: true }
ports:
  base: 9100
  publish:
    - { name: vite, container: 5173, open: true }
    - { name: cdp, container: 9223, loopback_lock: true }
agents:
  claude: ~/.den/agents/claude-fullstack
`

func TestLoadNest(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	denHome := t.TempDir()
	ecrisNest(t, denHome, "fullstack", nestComplet)

	n, err := LoadNest(denHome, "fullstack")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if n.Name != "fullstack" || n.Stack != "dgdevx" {
		t.Errorf("nest = %+v", n)
	}
	if n.Env["SOME_VAR"] != "value" {
		t.Errorf("Env = %v", n.Env)
	}
	if len(n.Repos) != 2 {
		t.Fatalf("attendu 2 repos, obtenu %d", len(n.Repos))
	}
	if want := filepath.Join(home, "dev/review-mgmt"); n.Repos[0].Path != want {
		t.Errorf("Repos[0].Path = %q, attendu %q (tilde expansé)", n.Repos[0].Path, want)
	}
	if n.Repos[0].Optional {
		t.Error("Repos[0] doit être requis")
	}
	if !n.Repos[1].Optional {
		t.Error("Repos[1] doit être optionnel")
	}
	if got := n.Repos[0].Name(); got != "review-mgmt" {
		t.Errorf("Name() = %q, attendu %q", got, "review-mgmt")
	}
	if n.Ports.Base != 9100 || len(n.Ports.Publish) != 2 {
		t.Errorf("Ports = %+v", n.Ports)
	}
	if !n.Ports.Publish[1].LoopbackLock {
		t.Error("le port cdp doit être loopback_lock")
	}
	if want := filepath.Join(home, ".den/agents/claude-fullstack"); n.Agents["claude"] != want {
		t.Errorf("Agents[claude] = %q, attendu %q", n.Agents["claude"], want)
	}
}

func TestLoadNestNomDeduitDuFichier(t *testing.T) {
	denHome := t.TempDir()
	ecrisNest(t, denHome, "review", "stack: devx\n")
	n, err := LoadNest(denHome, "review")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if n.Name != "review" {
		t.Errorf("Name = %q, attendu %q déduit du fichier", n.Name, "review")
	}
}

func TestLoadNestAbsent(t *testing.T) {
	if _, err := LoadNest(t.TempDir(), "fantome"); err == nil {
		t.Fatal("attendu une erreur pour un nest absent")
	}
}

func TestListNestsTriParNom(t *testing.T) {
	denHome := t.TempDir()
	ecrisNest(t, denHome, "web", "stack: devx\n")
	ecrisNest(t, denHome, "api", "stack: devx\n")
	ecrisNest(t, denHome, "review", "stack: devx\n")
	// un fichier non-YAML ne doit pas être ramassé
	if err := os.WriteFile(filepath.Join(denHome, "nests", "NOTES.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	nests, err := ListNests(denHome)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	var noms []string
	for _, n := range nests {
		noms = append(noms, n.Name)
	}
	attendu := []string{"api", "review", "web"}
	if len(noms) != 3 || noms[0] != attendu[0] || noms[1] != attendu[1] || noms[2] != attendu[2] {
		t.Errorf("noms = %v, attendu %v (trié)", noms, attendu)
	}
}

// TestListNestsTriDivergeDeLOrdreFichier verrouille le tri explicite de ListNests.
// os.ReadDir renvoie déjà ses entrées triées par nom de FICHIER : avec des noms de
// nests qui partagent tous le même suffixe ".yaml" (cas de TestListNestsTriParNom
// ci-dessus), l'ordre des fichiers et l'ordre des noms de nests coïncident toujours,
// et un test bâti uniquement sur ce cas ne distinguerait pas « ListNests trie » de
// « ReadDir trie déjà » — on pourrait retirer sort.Strings sans faire échouer la suite.
// Ici "web-2.yaml" et "web.yaml" divergent : '-' (0x2D) précède '.' (0x2E) en ASCII, donc
// ReadDir renvoie "web-2.yaml" avant "web.yaml", alors qu'une fois le suffixe ".yaml"
// retiré, l'ordre trié des noms de nests place "web" avant "web-2" (préfixe plus court).
func TestListNestsTriDivergeDeLOrdreFichier(t *testing.T) {
	denHome := t.TempDir()
	ecrisNest(t, denHome, "web", "stack: devx\n")
	ecrisNest(t, denHome, "web-2", "stack: devx\n")
	ecrisNest(t, denHome, "api", "stack: devx\n")

	nests, err := ListNests(denHome)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	var noms []string
	for _, n := range nests {
		noms = append(noms, n.Name)
	}
	attendu := []string{"api", "web", "web-2"}
	if len(noms) != 3 || noms[0] != attendu[0] || noms[1] != attendu[1] || noms[2] != attendu[2] {
		t.Errorf("noms = %v, attendu %v (trié par nom de nest, pas par nom de fichier)", noms, attendu)
	}
}

func TestListNestsDossierAbsent(t *testing.T) {
	nests, err := ListNests(t.TempDir())
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if len(nests) != 0 {
		t.Errorf("attendu 0 nest, obtenu %d", len(nests))
	}
}
