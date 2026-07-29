package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// denHomeDeTest fabrique un ~/.den complet et pointe DEN_HOME dessus.
func denHomeDeTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	ecris := func(rel, contenu string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(contenu), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ecris("config.yaml", `
agents:
  claude:
    config_dir: /tmp/den-agents/claude
    env: { CLAUDE_CONFIG_DIR: "{config_dir}" }
    bin_dirs: ["$HOME/.local/bin"]
    update: "claude update"
defaults:
  agent: claude
  stack: devx
egress:
  - api.anthropic.com
`)
	ecris("stacks/devx/stack.yaml", "image: devx:v1\n")
	ecris("stacks/dgdevx/stack.yaml", "image: dgdevx:v1\nparent: devx\negress: [gitlab.digitaleo.com]\n")
	ecris("nests/api.yaml", "stack: devx\nrepos:\n  - { path: /dev/api }\n")
	ecris("nests/fullstack.yaml", `
stack: dgdevx
egress: ["10.22.11.54:27017"]
repos:
  - { path: /dev/api }
  - { path: /dev/front, optional: true }
`)

	t.Setenv("DEN_HOME", dir)
	return dir
}

func TestNestLsListeLesNests(t *testing.T) {
	denHomeDeTest(t)
	out, err := run(t, "nest", "ls")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	for _, attendu := range []string{"api", "fullstack", "devx", "dgdevx"} {
		if !strings.Contains(out, attendu) {
			t.Errorf("sortie = %q, attendu contenant %q", out, attendu)
		}
	}
	// tri : api avant fullstack
	if strings.Index(out, "api") > strings.Index(out, "fullstack") {
		t.Errorf("sortie non triée : %q", out)
	}
}

// `den nest ls` affiche les nests sains ET signale les cassés nommément, mais
// retourne quand même une erreur (code de sortie non nul) : la liste est
// consultable, mais il reste quelque chose à réparer.
func TestNestLsSignaleLesCassesEtRetourneUneErreur(t *testing.T) {
	dir := denHomeAvecNest(t, "api")
	if err := os.WriteFile(filepath.Join(dir, "nests", "casse.yaml"), []byte("egres: [x]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "nest", "ls", "--den-home", dir)
	if err == nil {
		t.Fatal("attendu une erreur : un nest est cassé")
	}
	if !strings.Contains(out, "api") {
		t.Errorf("le nest sain doit rester listé ; obtenu :\n%s", out)
	}
	// Chaîne exacte, pas juste "casse" : LoadNest nomme déjà le fichier dans
	// son erreur de décodage, ce qui rendrait Contains(out, "casse") vrai même
	// si `den nest ls` omettait c.Nom.
	if !strings.Contains(out, "! casse :") {
		t.Errorf("le nest cassé doit être signalé nommément ; obtenu :\n%s", out)
	}
}

// Un ~/.den/nests ne contenant QUE des nests cassés ne doit pas afficher
// « aucun nest déclaré » : l'utilisateur a des nests, ils sont juste tous
// illisibles — un message d'absence serait un mensonge doublé d'un faux
// succès (code de sortie 0 alors qu'il y a quelque chose à réparer).
func TestNestLsNAffichePasAucunNestDeclareSiTousCasses(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "nests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nests", "casse.yaml"), []byte("egres: [x]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "nest", "ls", "--den-home", dir)
	if err == nil {
		t.Fatal("attendu une erreur : le seul nest présent est cassé")
	}
	if strings.Contains(out, "aucun nest déclaré") {
		t.Errorf("un nest cassé n'est pas une absence de nest ; obtenu :\n%s", out)
	}
}

func TestNestShowAfficheLaResolution(t *testing.T) {
	denHomeDeTest(t)
	out, err := run(t, "nest", "show", "fullstack")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	attendus := []string{
		"fullstack",
		"dgdevx:v1",              // image de la stack
		"claude",                 // agent résolu
		"/tmp/den-agents/claude", // config_dir résolu
		"10.22.11.54:27017",      // egress du nest
		"api.anthropic.com",      // egress baseline
		"gitlab.digitaleo.com",   // egress de la stack
		"/dev/front",             // repo optionnel listé
	}
	for _, a := range attendus {
		if !strings.Contains(out, a) {
			t.Errorf("sortie = %q, attendu contenant %q", out, a)
		}
	}
}

func TestNestShowNestInconnu(t *testing.T) {
	denHomeDeTest(t)
	if _, err := run(t, "nest", "show", "fantome"); err == nil {
		t.Fatal("attendu une erreur pour un nest inconnu")
	}
}

func TestNestShowAfficheLEnvSubstitue(t *testing.T) {
	denHomeDeTest(t)
	out, err := run(t, "nest", "show", "api")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !strings.Contains(out, "CLAUDE_CONFIG_DIR=/tmp/den-agents/claude") {
		t.Errorf("l'env affiché doit être substitué ; obtenu :\n%s", out)
	}
	if strings.Contains(out, "{config_dir}") {
		t.Errorf("le jeton {config_dir} ne doit jamais s'afficher ; obtenu :\n%s", out)
	}
}

func TestNestShowRespecteLesFlagsDeSelection(t *testing.T) {
	denHomeDeTest(t)
	out, err := run(t, "nest", "show", "fullstack", "--without", "front")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if strings.Contains(out, "/dev/front") {
		t.Errorf("le repo exclu apparaît encore : %q", out)
	}
	if !strings.Contains(out, "/dev/api") {
		t.Errorf("le repo requis a disparu : %q", out)
	}
}

// M-1 — un nest qui porte le nom d'une sous-commande est listé par `den nest ls`
// et résolu par `den nest show`, mais `den <nom>` lancera TOUJOURS la
// sous-commande : il n'est jamais spawnable. C'est le défaut trouvé en T3 avec
// `-api` — den nomme un objet qu'il refuse ensuite d'adresser — et la
// contrepartie exacte de la suggestion de D1, qui ne le tient que dans l'autre
// sens.
//
// L'avertissement part sur STDERR : la liste doit rester tuyautable sans qu'un
// avertissement s'y glisse, et c'est exactement ce qu'executeCmd (qui fusionne
// les deux flux) ne peut pas distinguer.
func TestNestLsAvertitDesNestsMasquesParUneSousCommande(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "nests"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, nom := range []string{"api", "ls", "version"} {
		if err := os.WriteFile(filepath.Join(dir, "nests", nom+".yaml"),
			[]byte("stack: devx\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	stdout, stderr, err := executeCmdFluxSepares(t, NewRootCmd(), "nest", "ls", "--den-home", dir)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	// La liste elle-même ne change pas : les nests masqués EXISTENT.
	for _, nom := range []string{"api", "ls", "version"} {
		if !strings.Contains(stdout, nom) {
			t.Errorf("le nest %q doit rester listé ; stdout = %q", nom, stdout)
		}
	}
	for _, masque := range []string{"ls", "version"} {
		if !strings.Contains(stderr, masque) {
			t.Errorf("le nest masqué %q doit être signalé ; stderr = %q", masque, stderr)
		}
	}
	// Et surtout PAS de faux positif : « api » n'est masqué par rien.
	if strings.Contains(stderr, "api") {
		t.Errorf("« api » n'est masqué par aucune sous-commande ; stderr = %q", stderr)
	}
	// L'avertissement ne doit pas polluer la sortie tuyautable.
	if strings.Contains(stdout, "masqué") || strings.Contains(stdout, "avertissement") {
		t.Errorf("l'avertissement doit partir sur stderr, pas sur stdout ; stdout = %q", stdout)
	}
}

// Sans nest masqué, stderr doit rester VIDE : un avertissement permanent est un
// avertissement qu'on n'écoute plus.
func TestNestLsNAvertitDeRienSansCollision(t *testing.T) {
	dir := denHomeAvecNest(t, "api")

	stdout, stderr, err := executeCmdFluxSepares(t, NewRootCmd(), "nest", "ls", "--den-home", dir)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !strings.Contains(stdout, "api") {
		t.Errorf("stdout = %q, attendu le nest listé", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, attendu vide : aucun nest n'est masqué", stderr)
	}
}
