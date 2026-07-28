package nest

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/PillowPillow/den/internal/config"
)

// Options porte les surcharges issues des flags CLI (dernier niveau de la cascade).
type Options struct {
	Agent   string   // --agent
	Without []string // --without
	Only    []string // --only
}

// jetonConfigDir est le marqueur substitué dans les valeurs d'env de l'agent.
// Il vise un chemin HÔTE : sbx monte chaque workspace au MÊME chemin absolu
// dans la VM, donc le chemin hôte du profil est aussi son chemin in-VM.
const jetonConfigDir = "{config_dir}"

// Resolved est un nest entièrement résolu : plus rien à recalculer en aval.
// Le plan Spawn le consomme tel quel pour fabriquer le mixin et l'argv sbx create.
type Resolved struct {
	DenHome string // le mixin généré s'écrit sous <DenHome>/cache/mixins/

	Nest  *Nest
	Stack *config.Stack

	AgentName      string
	Agent          config.Agent
	AgentConfigDir string // override nest s'il existe, sinon registre global

	// Env est l'union PRÊTE À POSER : env de l'agent (avec {config_dir} déjà
	// substitué) ∪ env du nest, le nest gagnant. La substitution est une règle
	// de cascade, pas d'affichage : elle appartient ici, pas au mixin.
	Env map[string]string

	Egress []string // union triée baseline ∪ stack ∪ nest
	Repos  []Repo   // sélection appliquée, ordre de déclaration

	SSHMode        string
	SSHDir         string
	WorktreeLayout string
	WorktreeRoot   string
}

// fusionneEnv applique la cascade agent ← nest et substitue {config_dir}.
// Renvoie toujours une map non-nil : les consommateurs itèrent sans garde.
func fusionneEnv(agentEnv, nestEnv map[string]string, configDir string) map[string]string {
	out := make(map[string]string, len(agentEnv)+len(nestEnv))
	for k, v := range agentEnv {
		out[k] = strings.ReplaceAll(v, jetonConfigDir, configDir)
	}
	for k, v := range nestEnv {
		out[k] = v // le nest est plus bas dans la cascade : il gagne
	}
	return out
}

// ResolveAgent détermine l'agent actif et son config_dir.
// Priorité du nom : flag --agent > defaults.agent.
// Priorité du config_dir : override du nest pour CET agent > registre global.
func ResolveAgent(g *config.Global, n *Nest, flagAgent string) (string, config.Agent, string, error) {
	nom := flagAgent
	if nom == "" {
		nom = g.Defaults.Agent
	}

	a, ok := g.Agents[nom]
	if !ok {
		dispos := slices.Sorted(maps.Keys(g.Agents))
		return "", config.Agent{}, "", fmt.Errorf(
			"agent %q inconnu (agents déclarés : %v)", nom, dispos)
	}

	configDir := a.ConfigDir
	if n != nil {
		if override, ok := n.Agents[nom]; ok && override != "" {
			configDir = override
		}
	}
	return nom, a, configDir, nil
}

// Resolve applique la cascade complète global ← stack ← nest ← flags.
func Resolve(denHome string, g *config.Global, stacks map[string]*config.Stack, n *Nest, o Options) (*Resolved, error) {
	nomStack := n.Stack
	if nomStack == "" {
		nomStack = g.Defaults.Stack
	}
	s, ok := stacks[nomStack]
	if !ok {
		dispos := slices.Sorted(maps.Keys(stacks))
		return nil, fmt.Errorf(
			"nest %q : stack %q introuvable dans %s/stacks (stacks déclarées : %v)",
			n.Name, nomStack, denHome, dispos)
	}

	nomAgent, agent, configDir, err := ResolveAgent(g, n, o.Agent)
	if err != nil {
		return nil, fmt.Errorf("nest %q : %w", n.Name, err)
	}

	repos, err := SelectRepos(n.Repos, o.Without, o.Only)
	if err != nil {
		return nil, fmt.Errorf("nest %q : %w", n.Name, err)
	}

	return &Resolved{
		DenHome:        denHome,
		Nest:           n,
		Stack:          s,
		AgentName:      nomAgent,
		Agent:          agent,
		AgentConfigDir: configDir,
		Env:            fusionneEnv(agent.Env, n.Env, configDir),
		Egress:         UnionEgress(g.Egress, s.Egress, n.Egress),
		Repos:          repos,
		SSHMode:        g.SSH.Mode,
		SSHDir:         g.SSH.Dir,
		WorktreeLayout: g.WorktreeLayout,
		WorktreeRoot:   g.WorktreeRoot,
	}, nil
}
