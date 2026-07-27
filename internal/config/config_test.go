package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ecrisConfig crée un DEN_HOME temporaire contenant le config.yaml fourni.
func ecrisConfig(t *testing.T, contenu string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(contenu), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

const configComplet = `
agents:
  claude:
    config_dir: ~/.den/agents/claude
    env: { CLAUDE_CONFIG_DIR: "{config_dir}" }
    bin_dirs: ["$HOME/.local/bin", "$HOME/.claude/local"]
    update: "claude update"
defaults:
  agent: claude
  stack: devx
ssh:
  mode: mount
  dir: ~/.ssh_sbx
worktree_layout: per-repo
worktree_root: ~/perso/wt
egress:
  - api.anthropic.com
  - github.com
`

func TestLoadGlobalChampsComplets(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	g, err := LoadGlobal(ecrisConfig(t, configComplet))
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	a, ok := g.Agents["claude"]
	if !ok {
		t.Fatal("agent claude absent du registre")
	}
	if want := filepath.Join(home, ".den/agents/claude"); a.ConfigDir != want {
		t.Errorf("ConfigDir = %q, attendu %q (tilde expansé)", a.ConfigDir, want)
	}
	if a.Update != "claude update" {
		t.Errorf("Update = %q, attendu %q", a.Update, "claude update")
	}
	// $HOME doit traverser intact : il sera résolu dans la VM.
	if len(a.BinDirs) != 2 || a.BinDirs[0] != "$HOME/.local/bin" {
		t.Errorf("BinDirs = %v, attendu $HOME préservé", a.BinDirs)
	}
	if a.Env["CLAUDE_CONFIG_DIR"] != "{config_dir}" {
		t.Errorf("Env = %v, attendu le placeholder {config_dir} intact", a.Env)
	}
	if g.Defaults.Agent != "claude" || g.Defaults.Stack != "devx" {
		t.Errorf("Defaults = %+v", g.Defaults)
	}
	if g.SSH.Mode != "mount" {
		t.Errorf("SSH.Mode = %q, attendu mount", g.SSH.Mode)
	}
	if want := filepath.Join(home, ".ssh_sbx"); g.SSH.Dir != want {
		t.Errorf("SSH.Dir = %q, attendu %q", g.SSH.Dir, want)
	}
	if g.WorktreeLayout != "per-repo" {
		t.Errorf("WorktreeLayout = %q", g.WorktreeLayout)
	}
	if want := filepath.Join(home, "perso/wt"); g.WorktreeRoot != want {
		t.Errorf("WorktreeRoot = %q, attendu %q", g.WorktreeRoot, want)
	}
	if len(g.Egress) != 2 {
		t.Errorf("Egress = %v, attendu 2 entrées", g.Egress)
	}
}

func TestLoadGlobalDefautsAppliques(t *testing.T) {
	denHome := ecrisConfig(t, "defaults:\n  agent: claude\n  stack: devx\n")
	g, err := LoadGlobal(denHome)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if g.SSH.Mode != "agent-forward" {
		t.Errorf("SSH.Mode = %q, attendu le défaut agent-forward", g.SSH.Mode)
	}
	if g.WorktreeLayout != "central" {
		t.Errorf("WorktreeLayout = %q, attendu le défaut central", g.WorktreeLayout)
	}
	// Le défaut est relatif AU den home courant, pas littéralement ~/.den/worktrees :
	// sur un DEN_HOME temporaire, les worktrees doivent rester dans ce home-là.
	if want := filepath.Join(denHome, "worktrees"); g.WorktreeRoot != want {
		t.Errorf("WorktreeRoot = %q, attendu le défaut %q", g.WorktreeRoot, want)
	}
}

func TestLoadGlobalFichierAbsent(t *testing.T) {
	denHome := t.TempDir()
	_, err := LoadGlobal(denHome)
	if err == nil {
		t.Fatal("attendu une erreur quand config.yaml est absent")
	}
	// Le message doit être actionnable : il nomme le chemin cherché.
	if !strings.Contains(err.Error(), filepath.Join(denHome, "config.yaml")) {
		t.Errorf("erreur = %q, attendu le chemin complet du fichier manquant", err.Error())
	}
}

func TestLoadGlobalYamlInvalide(t *testing.T) {
	if _, err := LoadGlobal(ecrisConfig(t, "agents: [ceci n'est pas une map")); err == nil {
		t.Fatal("attendu une erreur sur YAML invalide")
	}
}
