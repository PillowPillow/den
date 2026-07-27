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
