package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/config"
)

func agentClaude() config.Agent {
	return config.Agent{
		ConfigDir: "/home/moi/.den/agents/claude",
		Env:       map[string]string{"CLAUDE_CONFIG_DIR": "{config_dir}"},
		BinDirs:   []string{"$HOME/.local/bin", "$HOME/.claude/local"},
		Update:    "claude update",
	}
}

// Le $HOME des bin_dirs vise le home DE LA VM : il doit traverser den INTACT.
// C'est l'invariant que le handoff §11.1 impose de verrouiller en premier.
func TestCommandeFraicheurNExpansePasHOME(t *testing.T) {
	argv, err := CommandeFraicheur("claude", agentClaude())
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if len(argv) != 3 || argv[0] != "bash" || argv[1] != "-c" {
		t.Fatalf("argv attendu [bash -c <script>], obtenu %q", argv)
	}
	script := argv[2]
	if !strings.Contains(script, `export PATH="$HOME/.local/bin:$HOME/.claude/local:$PATH"`) {
		t.Errorf("le script doit poser le PATH avec les bin_dirs LITTÉRAUX ; obtenu :\n%s", script)
	}
	if home, _ := os.UserHomeDir(); home != "" && strings.Contains(script, home) {
		t.Errorf("le home HÔTE %q a fuité dans le script destiné à la VM :\n%s", home, script)
	}
}

// Le dispatcher sbx sort au premier échec : la commande doit être fail-closed,
// avec des tentatives bornées pour absorber la propagation de la policy egress.
func TestCommandeFraicheurEstFailClosed(t *testing.T) {
	argv, err := CommandeFraicheur("claude", agentClaude())
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	script := argv[2]
	for _, attendu := range []string{"claude update", "exit 127", "exit 0", "sleep 10"} {
		if !strings.Contains(script, attendu) {
			t.Errorf("le script doit contenir %q ; obtenu :\n%s", attendu, script)
		}
	}

	// "exit 1" est une sous-chaîne de "exit 127", déjà émis plus haut : un
	// Contains ne mordrait pas. Ce qui compte est la position — la sortie
	// fail-closed est la DERNIÈRE chose que fait le script.
	if !strings.HasSuffix(script, "exit 1\n") {
		t.Errorf("le script doit se terminer sur la sortie fail-closed \"exit 1\" ; obtenu :\n%s", script)
	}

	// La boucle doit être BORNÉE : sans borne ni incrément, un agent dont
	// l'update échoue en boucle bloquerait le boot au lieu d'échouer.
	borne := fmt.Sprintf("while [ \"$tentative\" -le %d ]; do", tentativesFraicheur)
	if !strings.Contains(script, borne) {
		t.Errorf("la boucle doit être bornée par %q ; obtenu :\n%s", borne, script)
	}
	if !strings.Contains(script, "tentative=$((tentative + 1))") {
		t.Errorf("la boucle doit incrémenter le compteur, sinon la borne ne borne rien :\n%s", script)
	}
}

func TestCommandeFraicheurSansBinDirs(t *testing.T) {
	a := agentClaude()
	a.BinDirs = nil
	argv, err := CommandeFraicheur("claude", a)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if strings.Contains(argv[2], "export PATH") {
		t.Errorf("sans bin_dirs, aucune ligne export PATH ne doit être émise :\n%s", argv[2])
	}
}

func TestCommandeFraicheurRefuseUpdateVide(t *testing.T) {
	a := agentClaude()
	a.Update = ""
	if _, err := CommandeFraicheur("claude", a); err == nil {
		t.Fatal("un agent sans commande update doit être refusé (spec §9.1)")
	}
}

// Golden file : filet de régression sur le rendu exact.
func TestCommandeFraicheurGolden(t *testing.T) {
	argv, err := CommandeFraicheur("claude", agentClaude())
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	chemin := filepath.Join("testdata", "fraicheur-claude.golden")
	attendu, err := os.ReadFile(chemin)
	if err != nil {
		t.Fatalf("lecture du golden : %v", err)
	}
	if got := argv[2]; got != string(attendu) {
		t.Errorf("rendu != %s\n--- obtenu ---\n%s\n--- attendu ---\n%s", chemin, got, attendu)
	}
}
