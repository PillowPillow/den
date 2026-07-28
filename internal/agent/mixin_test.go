package agent

import (
	"io/fs"
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
	// La fraîcheur, elle, n'est PAS optionnelle : l'omission des sections vides
	// ne doit jamais déborder sur commands.
	if !strings.Contains(rendu, "commands:") {
		t.Errorf("commands ne s'omet jamais, même sans egress ni env ; obtenu :\n%s", rendu)
	}
}

// Une Fraicheur vide ne doit pas s'omettre comme une section vide : le mixin
// rendu serait valide et démarrerait une sandbox SANS contrôle de fraîcheur,
// exactement ce que CommandeFraicheur refuse (spec §9.1). Fail-closed.
func TestRendMixinRefuseUneFraicheurVide(t *testing.T) {
	m := mixinExemple(t)
	m.Fraicheur = nil
	if _, err := RendMixin(m); err == nil {
		t.Fatal("une Fraicheur vide doit être refusée, pas omise silencieusement")
	}
}

// Corollaire : tout mixin rendu avec succès porte sa commande de fraîcheur.
func TestRendMixinPorteToujoursLaFraicheur(t *testing.T) {
	for _, m := range []Mixin{
		mixinExemple(t),
		{NomSandbox: "api", Fraicheur: mixinExemple(t).Fraicheur},
	} {
		out, err := RendMixin(m)
		if err != nil {
			t.Fatalf("erreur inattendue : %v", err)
		}
		for _, attendu := range []string{"commands:", "startup:", "claude update"} {
			if !strings.Contains(string(out), attendu) {
				t.Errorf("un mixin rendu doit toujours contenir %q ; obtenu :\n%s", attendu, out)
			}
		}
	}
}

func resoluExemple(t *testing.T) *nest.Resolved {
	t.Helper()
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
	return r
}

func TestMixinDepuisAssembleLeNestResolu(t *testing.T) {
	m, err := MixinDepuis(resoluExemple(t), "api")
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

// Env et Egress sont exportés sur une struct exportée : les aliaser ferait
// d'une écriture sur le mixin une mutation silencieuse du nest résolu, que
// l'appelant continue d'utiliser après le spawn.
func TestMixinDepuisNAliasePasLeResolu(t *testing.T) {
	r := resoluExemple(t)
	m, err := MixinDepuis(r, "api")
	if err != nil {
		t.Fatalf("MixinDepuis : %v", err)
	}

	m.Env["INJECTE"] = "x"
	m.Egress[0] = "remplace.exemple.test"

	if _, present := r.Env["INJECTE"]; present {
		t.Errorf("écrire dans Mixin.Env a muté le nest résolu : %v", r.Env)
	}
	if r.Egress[0] == "remplace.exemple.test" {
		t.Errorf("écrire dans Mixin.Egress a muté le nest résolu : %v", r.Egress)
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

// spec.yaml porte environment.variables, c'est-à-dire de l'env utilisateur
// libre — exactement là où atterrissent une clé d'API ou une URI à credentials.
// Rien ne justifie qu'il soit lisible par tout le monde.
func TestEcrisMixinRestreintLesDroits(t *testing.T) {
	denHome := t.TempDir()
	dir, err := EcrisMixin(denHome, "api", mixinExemple(t))
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	infoDossier, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat du dossier : %v", err)
	}
	if got := infoDossier.Mode().Perm(); got != 0o700 {
		t.Errorf("droits du dossier = %o, attendu 700", got)
	}

	infoFichier, err := os.Stat(filepath.Join(dir, "spec.yaml"))
	if err != nil {
		t.Fatalf("stat de spec.yaml : %v", err)
	}
	if got := infoFichier.Mode().Perm(); got != 0o600 {
		t.Errorf("droits de spec.yaml = %o, attendu 600 — ce fichier porte l'env résolu", got)
	}
}

// EcrisMixin transforme un nom en chemin hôte : c'est ICI que la garde
// appartient. filepath.Join NETTOIE un « .. » en une vraie traversée au lieu de
// la rejeter, et sbx.DecomposeNom est totale et ne valide rien.
func TestEcrisMixinRefuseUnNomHorsCharset(t *testing.T) {
	denHome := t.TempDir()
	for _, nom := range []string{
		"", ".", "..", "../evade", "api/../../evade", "a/b", "-api", "api.feat.trop",
	} {
		if _, err := EcrisMixin(denHome, nom, mixinExemple(t)); err == nil {
			t.Errorf("le nom de sandbox %q doit être refusé", nom)
		}
	}

	// Contre-épreuve : aucune écriture n'a eu lieu, nulle part sous denHome.
	var ecrits []string
	if err := filepath.WalkDir(denHome, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			ecrits = append(ecrits, p)
		}
		return nil
	}); err != nil {
		t.Fatalf("parcours : %v", err)
	}
	if len(ecrits) != 0 {
		t.Errorf("un nom refusé ne doit rien écrire ; obtenu %v", ecrits)
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
	m := mixinExemple(t)
	out, err := RendMixin(m)
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
	// Le script doit traverser l'aller-retour YAML OCTET POUR OCTET : c'est lui
	// qui s'exécute en VM. Une sous-chaîne ne dirait rien d'un espacement perdu
	// ou d'un chomping qui mange la dernière ligne (« exit 1 »).
	for i, attendu := range m.Fraicheur {
		if relu.Commands.Startup[0].Command[i] != attendu {
			t.Errorf("argv[%d] relu ≠ rendu\n--- relu ---\n%s\n--- attendu ---\n%s",
				i, relu.Commands.Startup[0].Command[i], attendu)
		}
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
