package nest

import (
	"os"
	"path/filepath"
	"strings"
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

// Le nom d'un nest est le basename de son fichier — c'est le cas nominal, et le
// seul : il n'y a pas d'autre source d'identité.
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

// Un `name:` dans le contenu était une seconde source d'identité, divergente du
// nom de fichier : `den nest ls` cessait d'être trié (le tri porte sur les noms
// de fichiers) et `den nest show <nom-affiché>` cherchait un fichier qui
// n'existait pas. Le champ n'existe plus au schéma : le décodage strict le
// rejette, à la source, avec un message qui nomme la clé fautive.
func TestLoadNestRejetteUnNomDansLeContenu(t *testing.T) {
	denHome := t.TempDir()
	ecrisNest(t, denHome, "api", "name: fullstack\nstack: devx\n")
	_, err := LoadNest(denHome, "api")
	if err == nil {
		t.Fatal("attendu un rejet : l'identité d'un nest vient de son fichier, pas de son contenu")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("erreur = %q, attendu une mention de la clé `name`", err.Error())
	}
}

func TestLoadNestRejetteUneCleInconnue(t *testing.T) {
	denHome := t.TempDir()
	ecrisNest(t, denHome, "review", "stack: devx\negres:\n  - github.com\n")
	_, err := LoadNest(denHome, "review")
	if err == nil {
		t.Fatal("attendu une erreur sur la clé inconnue `egres`")
	}
	if !strings.Contains(err.Error(), "egres") {
		t.Errorf("erreur = %q, attendu une mention de la clé fautive", err.Error())
	}
	if !strings.Contains(err.Error(), filepath.Join(denHome, "nests", "review.yaml")) {
		t.Errorf("erreur = %q, attendu le chemin du fichier fautif", err.Error())
	}
}

func TestLoadNestFichierVide(t *testing.T) {
	denHome := t.TempDir()
	ecrisNest(t, denHome, "review", "")
	n, err := LoadNest(denHome, "review")
	if err != nil {
		t.Fatalf("un nest vide ne doit pas être une erreur de chargement : %v", err)
	}
	if n.Name != "review" {
		t.Errorf("Name = %q, attendu %q déduit du fichier", n.Name, "review")
	}
}

// Deux repos de meme basename ne sont pas honorables : --without/--only les
// designent par ce nom (un seul `--without api` en ferait disparaitre deux), et
// au plan 2 le layout worktree_root/<wt>/<repo> les ferait collisionner sur le
// meme dossier. On rejette la config a la source plutot que de servir un
// comportement surprenant.
func TestLoadNestRejetteDeuxReposHomonymes(t *testing.T) {
	denHome := t.TempDir()
	ecrisNest(t, denHome, "fullstack", "repos:\n  - { path: /tmp/x/api }\n  - { path: /tmp/y/api }\n")
	_, err := LoadNest(denHome, "fullstack")
	if err == nil {
		t.Fatal("attendu un rejet : deux repos partagent le basename `api`")
	}
	for _, attendu := range []string{"fullstack", "api", "/tmp/x/api", "/tmp/y/api"} {
		if !strings.Contains(err.Error(), attendu) {
			t.Errorf("erreur = %q, attendu une mention de %q", err.Error(), attendu)
		}
	}
}

// La collision doit etre detectee APRES expansion : deux chemins ecrits
// differemment peuvent designer le meme basename une fois `~` resolu.
func TestLoadNestRejetteLesHomonymesApresExpansion(t *testing.T) {
	denHome := t.TempDir()
	ecrisNest(t, denHome, "fullstack", "repos:\n  - { path: ~/dev/api }\n  - { path: /srv/api, optional: true }\n")
	if _, err := LoadNest(denHome, "fullstack"); err == nil {
		t.Fatal("attendu un rejet : les deux chemins expansés partagent le basename `api`")
	}
}

func TestLoadNestAbsent(t *testing.T) {
	if _, err := LoadNest(t.TempDir(), "fantome"); err == nil {
		t.Fatal("attendu une erreur pour un nest absent")
	}
}

// `den nest show ../../../../etc/passwd` construisait un chemin hors de
// DEN_HOME. L'impact est faible aujourd'hui (CLI locale, fichiers de
// l'utilisateur), mais au plan 2 ce nom devient un nom de sandbox, un label
// `den.nest` et la graine du hash de la fenêtre de ports : on le rejette à la
// source.
func TestLoadNestRefuseUnNomQuiSortDeDenHome(t *testing.T) {
	racine := t.TempDir()
	denHome := filepath.Join(racine, "home")
	// Un YAML de nest parfaitement valide, mais HORS du den home.
	if err := os.MkdirAll(filepath.Join(denHome, "nests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(racine, "dehors.yaml"), []byte("stack: devx\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// <denHome>/nests/../../dehors.yaml == <racine>/dehors.yaml
	if _, err := LoadNest(denHome, "../../dehors"); err == nil {
		t.Error("LoadNest a chargé un fichier situé hors du den home")
	}
	for _, nom := range []string{"a/b", "..", "."} {
		if _, err := LoadNest(denHome, nom); err == nil {
			t.Errorf("LoadNest(%q) = nil, attendu un rejet", nom)
		}
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
