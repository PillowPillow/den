package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
	"gopkg.in/yaml.v3"
)

func mixinExemple(t *testing.T) Mixin {
	t.Helper()
	fraicheur, err := CommandeFraicheur("claude", agentClaude())
	if err != nil {
		t.Fatalf("CommandeFraicheur : %v", err)
	}
	return Mixin{
		NomSandbox: "api.feat12",
		Env: map[string]string{
			"CLAUDE_CONFIG_DIR": "/home/moi/.den/agents/claude",
			"SOME_VAR":          "value",
		},
		Egress:    []string{"api.anthropic.com", "github.com"},
		Fraicheur: fraicheur,
	}
}

func TestRendMixinPorteLesTroisCharges(t *testing.T) {
	out, err := RendMixin(mixinExemple(t))
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	rendu := string(out)

	// Le schéma RÉEL de sbx : caps.network.allow et environment.variables.
	// Le spec d'origine écrivait network.allow et env — c'était faux.
	for _, attendu := range []string{
		"schemaVersion: 2",
		"kind: mixin",
		"caps:",
		"network:",
		"allow:",
		"- api.anthropic.com",
		"environment:",
		"variables:",
		"CLAUDE_CONFIG_DIR: /home/moi/.den/agents/claude",
		"SOME_VAR: value",
		"commands:",
		"startup:",
	} {
		if !strings.Contains(rendu, attendu) {
			t.Errorf("le mixin doit contenir %q ; obtenu :\n%s", attendu, rendu)
		}
	}
}

// La fraîcheur est fail-closed et le dispatcher sbx sort au premier échec :
// elle doit être la DERNIÈRE startup command du DERNIER kit.
func TestRendMixinMetLaFraicheurEnDerniereStartup(t *testing.T) {
	m := mixinExemple(t)
	out, err := RendMixin(m)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	rendu := string(out)

	iStartup := strings.Index(rendu, "startup:")
	iUpdate := strings.Index(rendu, "claude update")
	if iStartup < 0 || iUpdate < 0 || iUpdate < iStartup {
		t.Fatalf("la commande de fraîcheur doit apparaître sous commands.startup ; obtenu :\n%s", rendu)
	}
	// Et le $HOME des bin_dirs traverse intact jusque dans le YAML.
	if !strings.Contains(rendu, "$HOME/.local/bin") {
		t.Errorf("le $HOME des bin_dirs doit survivre au rendu YAML ; obtenu :\n%s", rendu)
	}
}

// Déterminisme : deux rendus successifs doivent être identiques, sinon le
// golden file est un piège à faux positifs et le mixin change à chaque spawn.
func TestRendMixinEstDeterministe(t *testing.T) {
	m := mixinExemple(t)
	for i := 0; i < 20; i++ {
		a, err := RendMixin(m)
		if err != nil {
			t.Fatalf("erreur inattendue : %v", err)
		}
		b, err := RendMixin(m)
		if err != nil {
			t.Fatalf("erreur inattendue : %v", err)
		}
		if string(a) != string(b) {
			t.Fatalf("rendu non déterministe à l'itération %d :\n%s\n---\n%s", i, a, b)
		}
	}
}

// Le nom d'un kit ne peut pas porter le séparateur de nom de sandbox.
func TestRendMixinNommeLeKitSansPoint(t *testing.T) {
	out, err := RendMixin(mixinExemple(t))
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !strings.Contains(string(out), "name: den-api-feat12") {
		t.Errorf("le nom du kit doit être den-api-feat12 ; obtenu :\n%s", out)
	}
}

// Un nest sans egress ni env ne doit pas produire de sections vides : une
// `allow: []` vide vaut « rien d'autorisé » et non « pas de contrainte ».
func TestRendMixinOmetLesSectionsVides(t *testing.T) {
	fraicheur, err := CommandeFraicheur("claude", agentClaude())
	if err != nil {
		t.Fatalf("CommandeFraicheur : %v", err)
	}
	out, err := RendMixin(Mixin{NomSandbox: "api", Fraicheur: fraicheur})
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	rendu := string(out)
	if strings.Contains(rendu, "caps:") {
		t.Errorf("aucun egress ⇒ pas de section caps ; obtenu :\n%s", rendu)
	}
	if strings.Contains(rendu, "environment:") {
		t.Errorf("aucune variable ⇒ pas de section environment ; obtenu :\n%s", rendu)
	}
}

func TestMixinDepuisAssembleLeNestResolu(t *testing.T) {
	g := &config.Global{
		Agents:         map[string]config.Agent{"claude": agentClaude()},
		Defaults:       config.Defaults{Agent: "claude", Stack: "devx"},
		SSH:            config.SSH{Mode: "agent-forward"},
		WorktreeLayout: "central",
		Egress:         []string{"github.com"},
	}
	stacks := map[string]*config.Stack{"devx": {Name: "devx", Image: "devx:v1"}}
	n := &nest.Nest{Name: "api", Stack: "devx", Egress: []string{"10.22.11.54:27017"}}

	r, err := nest.Resolve("/home/moi/.den", g, stacks, n, nest.Options{})
	if err != nil {
		t.Fatalf("Resolve : %v", err)
	}

	m, err := MixinDepuis(r, "api")
	if err != nil {
		t.Fatalf("MixinDepuis : %v", err)
	}
	if m.NomSandbox != "api" {
		t.Errorf("NomSandbox = %q", m.NomSandbox)
	}
	// L'egress vient de la cascade, déjà unionné et trié par nest.Resolve.
	if len(m.Egress) != 2 || m.Egress[0] != "10.22.11.54:27017" || m.Egress[1] != "github.com" {
		t.Errorf("Egress = %v, attendu la cascade unionnée et triée", m.Egress)
	}
	if m.Env["CLAUDE_CONFIG_DIR"] != "/home/moi/.den/agents/claude" {
		t.Errorf("Env = %v, {config_dir} doit être substitué", m.Env)
	}
	if len(m.Fraicheur) != 3 {
		t.Errorf("Fraicheur = %v, attendu un argv [bash -c script]", m.Fraicheur)
	}
}

func TestEcrisMixinEcritSousCache(t *testing.T) {
	denHome := t.TempDir()
	dir, err := EcrisMixin(denHome, "api.feat12", mixinExemple(t))
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	attendu := filepath.Join(denHome, "cache", "mixins", "api.feat12")
	if dir != attendu {
		t.Errorf("dir = %q, attendu %q", dir, attendu)
	}
	// C'est le DOSSIER qu'on passe à `--kit`, et sbx y cherche spec.yaml.
	if _, err := os.Stat(filepath.Join(dir, "spec.yaml")); err != nil {
		t.Errorf("spec.yaml doit exister dans %s : %v", dir, err)
	}
}

// Un spawn répété doit réécrire, pas empiler : le mixin est reconstructible et
// reflète la config COURANTE, jamais celle du spawn précédent.
func TestEcrisMixinEstIdempotent(t *testing.T) {
	denHome := t.TempDir()
	m := mixinExemple(t)

	if _, err := EcrisMixin(denHome, "api", m); err != nil {
		t.Fatalf("premier écrit : %v", err)
	}
	m.Egress = []string{"nouveau.exemple.test"}
	dir, err := EcrisMixin(denHome, "api", m)
	if err != nil {
		t.Fatalf("second écrit : %v", err)
	}

	contenu, err := os.ReadFile(filepath.Join(dir, "spec.yaml"))
	if err != nil {
		t.Fatalf("lecture : %v", err)
	}
	if !strings.Contains(string(contenu), "nouveau.exemple.test") {
		t.Errorf("le second écrit doit remplacer le premier ; obtenu :\n%s", contenu)
	}
	if strings.Contains(string(contenu), "api.anthropic.com") {
		t.Errorf("le contenu du premier écrit ne doit pas survivre ; obtenu :\n%s", contenu)
	}
}

func TestRendMixinEstDuYAMLRelisible(t *testing.T) {
	out, err := RendMixin(mixinExemple(t))
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	var relu struct {
		SchemaVersion int    `yaml:"schemaVersion"`
		Kind          string `yaml:"kind"`
		Caps          struct {
			Network struct {
				Allow []string `yaml:"allow"`
			} `yaml:"network"`
		} `yaml:"caps"`
		Commands struct {
			Startup []struct {
				Command []string `yaml:"command"`
			} `yaml:"startup"`
		} `yaml:"commands"`
	}
	if err := yaml.Unmarshal(out, &relu); err != nil {
		t.Fatalf("le mixin rendu doit être du YAML relisible : %v\n%s", err, out)
	}
	if relu.SchemaVersion != 2 || relu.Kind != "mixin" {
		t.Errorf("en-tête relu = %d/%q", relu.SchemaVersion, relu.Kind)
	}
	if len(relu.Caps.Network.Allow) != 2 {
		t.Errorf("allow relu = %v", relu.Caps.Network.Allow)
	}
	if len(relu.Commands.Startup) != 1 || len(relu.Commands.Startup[0].Command) != 3 {
		t.Fatalf("startup relu = %v", relu.Commands.Startup)
	}
	// Le script doit rester exécutable après l'aller-retour YAML.
	if !strings.Contains(relu.Commands.Startup[0].Command[2], "$HOME/.local/bin") {
		t.Errorf("le script relu a perdu ses bin_dirs :\n%s", relu.Commands.Startup[0].Command[2])
	}
}

func TestRendMixinGolden(t *testing.T) {
	out, err := RendMixin(mixinExemple(t))
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	chemin := filepath.Join("testdata", "mixin-complet.golden")
	attendu, err := os.ReadFile(chemin)
	if err != nil {
		t.Fatalf("lecture du golden : %v", err)
	}
	if string(out) != string(attendu) {
		t.Errorf("rendu != %s\n--- obtenu ---\n%s\n--- attendu ---\n%s", chemin, out, attendu)
	}
}
