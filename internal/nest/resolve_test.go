package nest

import (
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/config"
)

func globalTest() *config.Global {
	return &config.Global{
		Agents: map[string]config.Agent{
			"claude": {
				ConfigDir: "/home/moi/.den/agents/claude",
				Env:       map[string]string{"CLAUDE_CONFIG_DIR": "{config_dir}"},
				BinDirs:   []string{"$HOME/.local/bin"},
				Update:    "claude update",
			},
			"codex": {ConfigDir: "/home/moi/.den/agents/codex", Update: "codex --upgrade"},
		},
		Defaults:       config.Defaults{Agent: "claude", Stack: "devx"},
		SSH:            config.SSH{Mode: "agent-forward"},
		WorktreeLayout: "central",
		WorktreeRoot:   "/home/moi/.den/worktrees",
		Egress:         []string{"api.anthropic.com", "github.com"},
	}
}

func stacksTest() map[string]*config.Stack {
	return map[string]*config.Stack{
		"devx":   {Name: "devx", Image: "devx:v1", Kit: "/den/stacks/devx/kit"},
		"dgdevx": {Name: "dgdevx", Image: "dgdevx:v1", Parent: "devx", Kit: "/den/stacks/dgdevx/kit", Egress: []string{"gitlab.digitaleo.com"}},
	}
}

func nestTest() *Nest {
	return &Nest{
		Name:   "fullstack",
		Stack:  "dgdevx",
		Egress: []string{"10.22.11.54:27017"},
		Repos:  []Repo{{Path: "/dev/api"}, {Path: "/dev/front", Optional: true}},
	}
}

func TestResolveAgentParDefaut(t *testing.T) {
	nom, a, dir, err := ResolveAgent(globalTest(), nestTest(), "")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if nom != "claude" {
		t.Errorf("nom = %q, attendu claude (defaults.agent)", nom)
	}
	if a.Update != "claude update" {
		t.Errorf("Update = %q", a.Update)
	}
	if dir != "/home/moi/.den/agents/claude" {
		t.Errorf("configDir = %q, attendu celui du registre global", dir)
	}
}

func TestResolveAgentFlagSurcharge(t *testing.T) {
	nom, _, dir, err := ResolveAgent(globalTest(), nestTest(), "codex")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if nom != "codex" || dir != "/home/moi/.den/agents/codex" {
		t.Errorf("nom = %q, dir = %q", nom, dir)
	}
}

func TestResolveAgentOverrideParNest(t *testing.T) {
	n := nestTest()
	n.Agents = map[string]string{"claude": "/perso/claude-fullstack"}
	_, _, dir, err := ResolveAgent(globalTest(), n, "")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if dir != "/perso/claude-fullstack" {
		t.Errorf("configDir = %q, attendu l'override du nest", dir)
	}
}

func TestResolveAgentOverrideNestCibleLeBonAgent(t *testing.T) {
	// Le nest surcharge codex ; l'agent actif est claude => l'override ne s'applique pas.
	n := nestTest()
	n.Agents = map[string]string{"codex": "/perso/codex"}
	_, _, dir, err := ResolveAgent(globalTest(), n, "")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if dir != "/home/moi/.den/agents/claude" {
		t.Errorf("configDir = %q, l'override codex n'aurait pas dû s'appliquer à claude", dir)
	}
}

// ResolveAgent doit accepter un nest nil : la garde `if n != nil` existe pour
// les appelants qui résolvent un agent hors contexte de nest (le futur
// `den doctor`/`den build`), et du code défensif non exercé n'est pas prouvé.
func TestResolveAgentSansNest(t *testing.T) {
	nom, a, dir, err := ResolveAgent(globalTest(), nil, "")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if nom != "claude" {
		t.Errorf("nom = %q, attendu claude (defaults.agent)", nom)
	}
	if a.Update != "claude update" {
		t.Errorf("Update = %q", a.Update)
	}
	if dir != "/home/moi/.den/agents/claude" {
		t.Errorf("configDir = %q, attendu celui du registre global", dir)
	}
}

func TestResolveAgentInconnu(t *testing.T) {
	_, _, _, err := ResolveAgent(globalTest(), nestTest(), "gemini")
	if err == nil {
		t.Fatal("attendu une erreur pour un agent inconnu")
	}
	if !strings.Contains(err.Error(), "claude") {
		t.Errorf("erreur = %q, attendu la liste des agents disponibles", err.Error())
	}
}

func TestResolveCascadeComplete(t *testing.T) {
	r, err := Resolve("/d", globalTest(), stacksTest(), nestTest(), Options{})
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if r.Stack.Image != "dgdevx:v1" {
		t.Errorf("Stack.Image = %q", r.Stack.Image)
	}
	if r.AgentName != "claude" {
		t.Errorf("AgentName = %q", r.AgentName)
	}
	attendu := []string{"10.22.11.54:27017", "api.anthropic.com", "github.com", "gitlab.digitaleo.com"}
	if len(r.Egress) != len(attendu) {
		t.Fatalf("Egress = %v, attendu %v", r.Egress, attendu)
	}
	for i := range attendu {
		if r.Egress[i] != attendu[i] {
			t.Fatalf("Egress = %v, attendu %v", r.Egress, attendu)
		}
	}
	if len(r.Repos) != 2 {
		t.Errorf("Repos = %v, attendu les 2 repos", noms(r.Repos))
	}
	if r.SSHMode != "agent-forward" || r.WorktreeLayout != "central" {
		t.Errorf("SSH/worktree non hérités du global : %+v", r)
	}
}

func TestResolveAppliqueLaSelectionDeRepos(t *testing.T) {
	r, err := Resolve("/d", globalTest(), stacksTest(), nestTest(), Options{Without: []string{"front"}})
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if got := noms(r.Repos); len(got) != 1 || got[0] != "api" {
		t.Errorf("Repos = %v, attendu [api]", got)
	}
}

func TestResolveStackDuNestParDefautSiAbsente(t *testing.T) {
	n := nestTest()
	n.Stack = "" // le nest ne tranche pas => defaults.stack
	r, err := Resolve("/d", globalTest(), stacksTest(), n, Options{})
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if r.Stack.Name != "devx" {
		t.Errorf("Stack.Name = %q, attendu le défaut devx", r.Stack.Name)
	}
}

func TestResolveStackInconnue(t *testing.T) {
	n := nestTest()
	n.Stack = "fantome"
	_, err := Resolve("/d", globalTest(), stacksTest(), n, Options{})
	if err == nil {
		t.Fatal("attendu une erreur pour une stack inconnue")
	}
	if !strings.Contains(err.Error(), "fantome") {
		t.Errorf("erreur = %q, attendu une mention de la stack manquante", err.Error())
	}
}

func TestResolveFusionneEtSubstitueLEnv(t *testing.T) {
	g := &config.Global{
		Agents: map[string]config.Agent{
			"claude": {
				ConfigDir: "/home/moi/.den/agents/claude",
				Env:       map[string]string{"CLAUDE_CONFIG_DIR": "{config_dir}"},
				Update:    "claude update",
			},
		},
		Defaults:       config.Defaults{Agent: "claude", Stack: "devx"},
		SSH:            config.SSH{Mode: "agent-forward"},
		WorktreeLayout: "central",
		WorktreeRoot:   "/home/moi/.den/worktrees",
	}
	stacks := map[string]*config.Stack{"devx": {Name: "devx", Image: "devx:v1", Dir: "/d/stacks/devx"}}
	n := &Nest{Name: "api", Stack: "devx", Env: map[string]string{"SOME_VAR": "value"}}

	r, err := Resolve("/home/moi/.den", g, stacks, n, Options{})
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	if r.DenHome != "/home/moi/.den" {
		t.Errorf("DenHome = %q, attendu /home/moi/.den", r.DenHome)
	}
	// {config_dir} doit être résolu : le mixin ne sait pas le faire, et le
	// chemin visé est un chemin HÔTE (sbx monte au même chemin dans la VM).
	if got := r.Env["CLAUDE_CONFIG_DIR"]; got != "/home/moi/.den/agents/claude" {
		t.Errorf("CLAUDE_CONFIG_DIR = %q, attendu le config_dir résolu", got)
	}
	if got := r.Env["SOME_VAR"]; got != "value" {
		t.Errorf("SOME_VAR = %q, attendu value", got)
	}
}

// Cascade : global ← stack ← nest ← flags. Le nest gagne sur l'agent.
func TestResolveEnvDuNestGagneSurCelleDeLAgent(t *testing.T) {
	g := &config.Global{
		Agents: map[string]config.Agent{
			"claude": {
				ConfigDir: "/profil",
				Env:       map[string]string{"PARTAGEE": "agent", "PROPRE": "agent"},
				Update:    "claude update",
			},
		},
		Defaults:       config.Defaults{Agent: "claude", Stack: "devx"},
		SSH:            config.SSH{Mode: "agent-forward"},
		WorktreeLayout: "central",
	}
	stacks := map[string]*config.Stack{"devx": {Name: "devx", Image: "devx:v1"}}
	n := &Nest{Name: "api", Stack: "devx", Env: map[string]string{"PARTAGEE": "nest"}}

	r, err := Resolve("/d", g, stacks, n, Options{})
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if r.Env["PARTAGEE"] != "nest" {
		t.Errorf("PARTAGEE = %q, attendu nest (le nest est plus bas dans la cascade)", r.Env["PARTAGEE"])
	}
	if r.Env["PROPRE"] != "agent" {
		t.Errorf("PROPRE = %q, attendu agent", r.Env["PROPRE"])
	}
}

// L'override de config_dir par nest doit se propager DANS l'env substitué,
// sinon la VM pointerait sur le profil partagé alors que le nest a demandé
// l'isolation.
func TestResolveSubstitueLOverrideDeConfigDirDuNest(t *testing.T) {
	g := &config.Global{
		Agents: map[string]config.Agent{
			"claude": {
				ConfigDir: "/profil/partage",
				Env:       map[string]string{"CLAUDE_CONFIG_DIR": "{config_dir}"},
				Update:    "claude update",
			},
		},
		Defaults:       config.Defaults{Agent: "claude", Stack: "devx"},
		SSH:            config.SSH{Mode: "agent-forward"},
		WorktreeLayout: "central",
	}
	stacks := map[string]*config.Stack{"devx": {Name: "devx", Image: "devx:v1"}}
	n := &Nest{Name: "api", Stack: "devx", Agents: map[string]string{"claude": "/profil/isole"}}

	r, err := Resolve("/d", g, stacks, n, Options{})
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if r.Env["CLAUDE_CONFIG_DIR"] != "/profil/isole" {
		t.Errorf("CLAUDE_CONFIG_DIR = %q, attendu /profil/isole", r.Env["CLAUDE_CONFIG_DIR"])
	}
}

func TestResolveEnvJamaisNil(t *testing.T) {
	g := &config.Global{
		Agents:         map[string]config.Agent{"claude": {ConfigDir: "/p", Update: "u"}},
		Defaults:       config.Defaults{Agent: "claude", Stack: "devx"},
		SSH:            config.SSH{Mode: "agent-forward"},
		WorktreeLayout: "central",
	}
	stacks := map[string]*config.Stack{"devx": {Name: "devx", Image: "devx:v1"}}
	r, err := Resolve("/d", g, stacks, &Nest{Name: "api", Stack: "devx"}, Options{})
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if r.Env == nil {
		t.Error("Env doit être une map vide, jamais nil : le mixin itère dessus sans garde")
	}
}
