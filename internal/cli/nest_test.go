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
	if !strings.Contains(out, "casse") {
		t.Errorf("le nest cassé doit être signalé nommément ; obtenu :\n%s", out)
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
