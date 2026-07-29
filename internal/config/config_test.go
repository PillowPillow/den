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

// configValide est le plus petit config.yaml que Validate accepte. Les tests de
// refus le concatènent avec la seule faute qu'ils instruisent, pour qu'un échec
// désigne cette faute-là et pas un manque collatéral.
const configValide = `agents:
  claude:
    config_dir: /tmp/den/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
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

// Les défauts s'appliquent AVANT le contrôle de cohérence : un config.yaml sans
// `worktree_layout:` ni `ssh:` est parfaitement valide, et LoadGlobal — qui
// valide désormais — ne doit pas le refuser au motif que ces champs sont vides.
func TestLoadGlobalDefautsAppliques(t *testing.T) {
	denHome := ecrisConfig(t, configValide)
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
	chemin := filepath.Join(denHome, "config.yaml")
	if !strings.Contains(err.Error(), chemin) {
		t.Errorf("erreur = %q, attendu le chemin complet du fichier manquant", err.Error())
	}
	// Une seule fois, et en français : le *fs.PathError de l'OS porte déjà le
	// chemin que cette enveloppe vient de nommer. C'est la première ligne que
	// voit un utilisateur dont ~/.den n'existe pas encore, sur `den doctor`
	// comme sur `den <nest>`.
	if n := strings.Count(err.Error(), chemin); n != 1 {
		t.Errorf("le chemin apparaît %d fois, attendu 1 ; message : %s", n, err.Error())
	}
	if strings.Contains(err.Error(), "no such file or directory") {
		t.Errorf("reste d'anglais dans le message : %s", err.Error())
	}
}

func TestLoadGlobalYamlInvalide(t *testing.T) {
	if _, err := LoadGlobal(ecrisConfig(t, "agents: [ceci n'est pas une map")); err == nil {
		t.Fatal("attendu une erreur sur YAML invalide")
	}
}

// Une clé mal orthographiée doit être une erreur, jamais un silence : `egres:`
// laisse l'allowlist vide, et la sandbox n'atteint plus api.anthropic.com sans
// que rien ne l'ait signalé. C'est exactement ce que `den doctor` doit attraper.
func TestLoadGlobalRejetteUneCleInconnue(t *testing.T) {
	denHome := ecrisConfig(t, "defaults:\n  agent: claude\n  stack: devx\negres:\n  - api.anthropic.com\n")
	_, err := LoadGlobal(denHome)
	if err == nil {
		t.Fatal("attendu une erreur sur la clé inconnue `egres`")
	}
	if !strings.Contains(err.Error(), "egres") {
		t.Errorf("erreur = %q, attendu une mention de la clé fautive", err.Error())
	}
	if !strings.Contains(err.Error(), filepath.Join(denHome, "config.yaml")) {
		t.Errorf("erreur = %q, attendu le chemin du fichier fautif", err.Error())
	}
}

// Un config.yaml vide est une config qui ne déclare rien, pas un fichier corrompu :
// Validate() dira ce qui manque en clair. yaml.v3 renvoie io.EOF sur un fichier
// vide, ce qui ne doit surtout pas remonter tel quel à l'utilisateur.
//
// Le sujet est LoadGlobalSansValider : c'est lui qui porte le contrat « lire
// n'est pas juger », dont `den doctor` dépend pour tout montrer d'un coup.
// LoadGlobal, lui, doit refuser — les deux moitiés sont vérifiées ici parce
// qu'elles ne se tiennent que l'une par l'autre.
func TestLoadGlobalSansValiderFichierVide(t *testing.T) {
	denHome := ecrisConfig(t, "")
	g, err := LoadGlobalSansValider(denHome)
	if err != nil {
		t.Fatalf("un config.yaml vide ne doit pas être une erreur de chargement : %v", err)
	}
	if g.SSH.Mode != "agent-forward" {
		t.Errorf("SSH.Mode = %q, attendu le défaut appliqué même sur fichier vide", g.SSH.Mode)
	}
	if errs := g.Validate(); len(errs) == 0 {
		t.Error("attendu que Validate signale une config vide")
	}
	if _, err := LoadGlobal(denHome); err == nil {
		t.Error("LoadGlobal doit refuser un config.yaml vide, sinon la validation reste facultative")
	}
}

// --- D1 : Validate() n'avait qu'UN appelant, doctor.go:59 -------------------
//
// Conséquence mesurée avant correctif : `den <nest>`, `den ls`, `den sh` et
// `den rm` chargeaient sans jamais valider. Les trois tests qui suivent
// verrouillent les trois dettes que cela laissait passer.

// 14ᵉ configuration hostile (T10) : `centrl` retombait SILENCIEUSEMENT sur
// `central` — LoadGlobal ne défaute que sur la chaîne vide, et personne
// n'appelait Validate sur ce chemin. Une faute de frappe changeait donc la
// disposition des worktrees sans un mot.
func TestLoadGlobalRefuseUnWorktreeLayoutInconnu(t *testing.T) {
	denHome := ecrisConfig(t, configValide+"worktree_layout: centrl\n")
	_, err := LoadGlobal(denHome)
	if err == nil {
		t.Fatal("attendu un refus de `worktree_layout: centrl`, obtenu nil")
	}
	if !strings.Contains(err.Error(), "centrl") {
		t.Errorf("erreur = %q, attendu la valeur fautive nommée", err.Error())
	}
	if !strings.Contains(err.Error(), filepath.Join(denHome, "config.yaml")) {
		t.Errorf("erreur = %q, attendu le chemin complet du fichier à corriger", err.Error())
	}
}

// T4-min-4 : un `config_dir` vide devient la chaîne vide dans `{config_dir}` et
// ATTEINT la microVM. Validate l'interdisait déjà ; le chemin de spawn ne
// l'appelait pas.
func TestLoadGlobalRefuseUnConfigDirVide(t *testing.T) {
	denHome := ecrisConfig(t, "agents:\n  claude:\n    config_dir: \"\"\n    update: \"claude update\"\n"+
		"defaults:\n  agent: claude\n  stack: devx\n")
	_, err := LoadGlobal(denHome)
	if err == nil {
		t.Fatal("attendu un refus d'un config_dir vide, obtenu nil")
	}
	if !strings.Contains(err.Error(), "agents.claude.config_dir") {
		t.Errorf("erreur = %q, attendu la clé fautive nommée", err.Error())
	}
}

// Refuser au chargement ne doit pas dégrader le diagnostic en « première faute
// trouvée » : l'utilisateur doit voir d'un coup tout ce qu'il a à réparer,
// exactement comme `den doctor`. Deux fautes indépendantes, les deux nommées.
func TestLoadGlobalCumuleToutesLesErreurs(t *testing.T) {
	denHome := ecrisConfig(t, configValide+"ssh:\n  mode: nfs\nworktree_layout: centrl\n")
	_, err := LoadGlobal(denHome)
	if err == nil {
		t.Fatal("attendu un refus, obtenu nil")
	}
	for _, attendu := range []string{"nfs", "centrl"} {
		if !strings.Contains(err.Error(), attendu) {
			t.Errorf("erreur = %q, attendu la mention de %q : LoadGlobal doit cumuler, pas s'arrêter à la première faute",
				err.Error(), attendu)
		}
	}
}
