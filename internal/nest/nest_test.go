package nest

import (
	"errors"
	"io/fs"
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

// L'ABSENCE d'un nest est le seul échec de chargement qui veut dire « cet objet
// n'existe pas », et la CLI s'en sert pour décider de proposer une sous-commande
// proche (`den doctr` ⇒ `den doctor`). Elle doit donc être reconnaissable
// autrement qu'en lisant le message.
func TestLoadNestAbsentEstUnTypeReconnaissable(t *testing.T) {
	denHome := t.TempDir()
	ecrisNest(t, denHome, "api", "stack: devx\n") // le dossier nests/ existe

	_, err := LoadNest(denHome, "absent")
	var introuvable *ErreurNestIntrouvable
	if !errors.As(err, &introuvable) {
		t.Fatalf("errors.As(err, &ErreurNestIntrouvable) doit réussir ; err = %v (%T)", err, err)
	}
	if introuvable.Nom != "absent" {
		t.Errorf("Nom = %q, attendu %q", introuvable.Nom, "absent")
	}
	if attendu := filepath.Join(denHome, "nests", "absent.yaml"); introuvable.Chemin != attendu {
		t.Errorf("Chemin = %q, attendu %q", introuvable.Chemin, attendu)
	}
	// fs.ErrNotExist doit rester dans la chaîne : du code qui teste déjà
	// os.IsNotExist sur cette erreur ne doit pas cesser de marcher.
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("fs.ErrNotExist doit rester repérable dans la chaîne ; err = %v", err)
	}
	// Le message ne change pas : ce type change ce que le code inspecte, pas ce
	// que l'utilisateur lit.
	for _, attendu := range []string{`nest "absent"`, filepath.Join(denHome, "nests", "absent.yaml")} {
		if !strings.Contains(err.Error(), attendu) {
			t.Errorf("message = %q, attendu contenant %q", err.Error(), attendu)
		}
	}
}

// La contrepartie : un nest PRÉSENT mais illisible n'est pas « introuvable ».
// Un dossier à la place du fichier, et non un chmod 0000 : la suite tourne en
// root, où les droits ne bloquent rien.
func TestLoadNestIllisibleNEstPasIntrouvable(t *testing.T) {
	denHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(denHome, "nests", "api.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := LoadNest(denHome, "api")
	if err == nil {
		t.Fatal("un nest illisible doit être une erreur de chargement")
	}
	var introuvable *ErreurNestIntrouvable
	if errors.As(err, &introuvable) {
		t.Errorf("un nest présent ne doit pas être rapporté introuvable ; err = %v", err)
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

	nests, casses, err := ListNests(denHome)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if len(casses) != 0 {
		t.Fatalf("aucun nest cassé attendu, obtenu %v", casses)
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

	nests, casses, err := ListNests(denHome)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if len(casses) != 0 {
		t.Fatalf("aucun nest cassé attendu, obtenu %v", casses)
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

// L'assertion mentionne le caractère fautif « _ » : sans ça, le test passerait
// aussi si LoadNest échouait pour une tout autre raison, sans rapport avec le
// charset de sandbox.
func TestLoadNestRefuseUnNomNonSandboxable(t *testing.T) {
	denHome := t.TempDir()
	ecrisNest(t, denHome, "mon_api", "stack: devx\nrepos: []\n")

	_, err := LoadNest(denHome, "mon_api")
	if err == nil {
		t.Fatal("un nom de nest non convertible en nom de sandbox doit être refusé au chargement")
	}
	if !strings.Contains(err.Error(), "_") {
		t.Errorf("erreur = %q, attendu une mention du caractère fautif « _ »", err.Error())
	}
}

func TestListNestsListeLesSainsEtSignaleLesCasses(t *testing.T) {
	denHome := t.TempDir()
	ecrisNest(t, denHome, "api", "stack: devx\nrepos: []\n")
	ecrisNest(t, denHome, "casse", "stack: devx\negres:\n  - typo.exemple.test\n")
	ecrisNest(t, denHome, "web", "stack: devx\nrepos: []\n")

	nests, casses, err := ListNests(denHome)
	if err != nil {
		t.Fatalf("un nest fautif ne doit pas être une erreur structurelle : %v", err)
	}

	if len(nests) != 2 || nests[0].Name != "api" || nests[1].Name != "web" {
		t.Errorf("les nests sains doivent être listés et triés ; obtenu %v", nomsDe(nests))
	}
	if len(casses) != 1 || casses[0].Nom != "casse" {
		t.Fatalf("le nest fautif doit être signalé ; obtenu %v", casses)
	}
	// Le diagnostic doit rester exploitable : fichier, ligne, clé.
	msg := casses[0].Err.Error()
	for _, attendu := range []string{"casse.yaml", "egres"} {
		if !strings.Contains(msg, attendu) {
			t.Errorf("le diagnostic doit contenir %q ; obtenu : %s", attendu, msg)
		}
	}
}

func TestListNestsToutSain(t *testing.T) {
	denHome := t.TempDir()
	ecrisNest(t, denHome, "api", "stack: devx\nrepos: []\n")

	nests, casses, err := ListNests(denHome)
	if err != nil || len(nests) != 1 || len(casses) != 0 {
		t.Errorf("nests=%v casses=%v err=%v", nomsDe(nests), casses, err)
	}
}

func TestListNestsDossierAbsent(t *testing.T) {
	nests, casses, err := ListNests(t.TempDir())
	if err != nil {
		t.Errorf("un ~/.den sans dossier nests n'est pas une erreur : %v", err)
	}
	if len(nests) != 0 || len(casses) != 0 {
		t.Errorf("nests=%v casses=%v", nests, casses)
	}
}

// TestListNestsRacineIllisible verrouille la distinction entre un nest cassé
// (2e valeur de retour) et un échec STRUCTUREL (3e valeur) : quand la racine
// nests/ elle-même est illisible, il n'y a rien à lister du tout, et
// ListNests doit le dire par une erreur nommant le chemin complet plutôt que
// de renvoyer une liste vide silencieuse.
func TestListNestsRacineIllisible(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("test non fiable en root : les permissions sont ignorées")
	}
	denHome := t.TempDir()
	racine := filepath.Join(denHome, "nests")
	if err := os.MkdirAll(racine, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(racine, 0o000); err != nil {
		t.Fatal(err)
	}
	// os.Geteuid() == 0 ne couvre pas tous les cas (conteneurs, CFS particuliers
	// qui ignorent aussi les permissions) : on vérifie EMPIRIQUEMENT que la
	// lecture échoue avant d'asserter quoi que ce soit, plutôt que de supposer
	// que 0o000 suffit sur ce poste.
	if _, err := os.ReadDir(racine); err == nil {
		if err := os.Chmod(racine, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Skip("la lecture d'un dossier 0o000 réussit sur cet environnement : test non fiable ici")
	}
	t.Cleanup(func() {
		// t.TempDir() doit pouvoir nettoyer derrière nous.
		if err := os.Chmod(racine, 0o755); err != nil {
			t.Fatal(err)
		}
	})

	nests, casses, err := ListNests(denHome)
	if err == nil {
		t.Fatal("attendu une erreur structurelle pour une racine nests/ illisible")
	}
	if nests != nil || casses != nil {
		t.Errorf("nests=%v casses=%v, attendu nil sur un échec structurel", nests, casses)
	}
	// Contains(racine) serait vrai par construction : le *fs.PathError brut de
	// os.ReadDir porte déjà le chemin absolu, wrap ou pas. HasPrefix sur le
	// préfixe propre à den prouve que ListNests ajoute bien SON contexte
	// (« lecture de <racine> : »), pas seulement que l'OS a nommé le chemin.
	if !strings.HasPrefix(err.Error(), "lecture de "+racine) {
		t.Errorf("erreur = %q, attendu le préfixe %q", err.Error(), "lecture de "+racine)
	}
}

// Un fichier littéralement nommé ".yaml" a un nom tronqué vide : sans repli,
// l'avertissement ne nommerait ni le nest ni le fichier, et l'utilisateur
// n'aurait aucun moyen de savoir quel fichier supprimer.
func TestListNestsFichierYamlSansNomRetombeSurLeNomDeFichier(t *testing.T) {
	denHome := t.TempDir()
	ecrisNest(t, denHome, "", "stack: devx\n")

	_, casses, err := ListNests(denHome)
	if err != nil {
		t.Fatalf("un nest fautif ne doit pas être une erreur structurelle : %v", err)
	}
	if len(casses) != 1 {
		t.Fatalf("attendu un seul nest cassé ; obtenu %v", casses)
	}
	if casses[0].Nom == "" {
		t.Errorf("le nom du nest cassé ne doit jamais être vide ; Nom=%q Err=%v", casses[0].Nom, casses[0].Err)
	}
	if casses[0].Nom != ".yaml" {
		t.Errorf("le nom doit retomber sur le nom de fichier complet %q ; obtenu %q", ".yaml", casses[0].Nom)
	}
}

// Demander UN nest précis reste dur : répondre « il est cassé » est la seule
// réponse honnête quand on a nommé celui-là.
func TestLoadNestResteDur(t *testing.T) {
	denHome := t.TempDir()
	ecrisNest(t, denHome, "casse", "egres: [x]\n")

	if _, err := LoadNest(denHome, "casse"); err == nil {
		t.Fatal("LoadNest doit rester dur sur un nest illisible")
	}
}

func nomsDe(nests []*Nest) []string {
	out := make([]string, 0, len(nests))
	for _, n := range nests {
		out = append(out, n.Name)
	}
	return out
}
