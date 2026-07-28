package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Agent décrit une entrée du registre d'agents (spec §4.1 et §9).
type Agent struct {
	ConfigDir string            `yaml:"config_dir"`
	Env       map[string]string `yaml:"env"`
	// BinDirs : chemins IN-VM ajoutés au PATH avant la commande de fraîcheur (spec §9.1).
	// Jamais expansés côté hôte.
	BinDirs []string `yaml:"bin_dirs"`
	// Update : commande de mise à jour de l'agent, jouée au boot de la VM.
	Update string `yaml:"update"`
}

// Defaults porte les choix par défaut quand ni le nest ni les flags ne tranchent.
type Defaults struct {
	Agent string `yaml:"agent"`
	Stack string `yaml:"stack"`
}

// SSH décrit le mode d'accès SSH dans la VM (spec §10).
type SSH struct {
	Mode string `yaml:"mode"` // agent-forward | mount | none
	Dir  string `yaml:"dir"`  // utilisé si mode=mount
}

// Global est le contenu de ~/.den/config.yaml, défauts appliqués et chemins expansés.
type Global struct {
	Agents         map[string]Agent `yaml:"agents"`
	Defaults       Defaults         `yaml:"defaults"`
	SSH            SSH              `yaml:"ssh"`
	WorktreeLayout string           `yaml:"worktree_layout"`
	WorktreeRoot   string           `yaml:"worktree_root"`
	Egress         []string         `yaml:"egress"`
}

// LoadGlobalSansValider lit <denHome>/config.yaml, applique les défauts et
// expanse les chemins hôte, SANS contrôler la cohérence du résultat.
//
// Réservé à `den doctor`, qui doit cumuler et afficher TOUTES les incohérences
// d'un coup (doctor.go) : s'il chargeait par LoadGlobal, il s'arrêterait à
// l'erreur de chargement et n'atteindrait plus jamais sa propre validation.
// Tout autre appelant doit passer par LoadGlobal — la validation n'est pas
// facultative sur le chemin qui construit une microVM.
func LoadGlobalSansValider(denHome string) (*Global, error) {
	chemin := filepath.Join(denHome, "config.yaml")
	brut, err := os.ReadFile(chemin)
	if err != nil {
		return nil, fmt.Errorf("lecture de %s : %w", chemin, err)
	}

	var g Global
	if err := DecodeYAMLStrict(chemin, brut, &g); err != nil {
		return nil, err
	}

	if g.SSH.Mode == "" {
		g.SSH.Mode = "agent-forward"
	}
	if g.WorktreeLayout == "" {
		g.WorktreeLayout = "central"
	}
	if g.WorktreeRoot == "" {
		g.WorktreeRoot = filepath.Join(denHome, "worktrees")
	}

	if g.WorktreeRoot, err = ExpandPath(g.WorktreeRoot); err != nil {
		return nil, err
	}
	if g.SSH.Dir, err = ExpandPath(g.SSH.Dir); err != nil {
		return nil, err
	}
	for nom, a := range g.Agents {
		if a.ConfigDir, err = ExpandPath(a.ConfigDir); err != nil {
			return nil, fmt.Errorf("agent %s : %w", nom, err)
		}
		g.Agents[nom] = a // les valeurs de map ne sont pas adressables
	}
	return &g, nil
}

// LoadGlobal charge <denHome>/config.yaml et REFUSE une configuration
// incohérente.
//
// Avant D1, Validate() n'avait qu'un appelant — `den doctor` — et toute config
// invalide traversait `den <nest>`, `den ls`, `den sh` et `den rm` sans être
// vue : un `worktree_layout: centrl` retombait silencieusement sur `central`,
// un `config_dir` vide atteignait la microVM. Valider ici plutôt que chez
// chaque appelant fait que le nom le plus court est le nom sûr : rester aveugle
// demande désormais d'écrire explicitement LoadGlobalSansValider.
func LoadGlobal(denHome string) (*Global, error) {
	g, err := LoadGlobalSansValider(denHome)
	if err != nil {
		return nil, err
	}
	if errs := g.Validate(); len(errs) > 0 {
		// Toutes les fautes, pas la première : un aller-retour par faute est
		// exactement ce que Validate() a été écrit pour éviter.
		var b strings.Builder
		fmt.Fprintf(&b, "configuration invalide dans %s :", filepath.Join(denHome, "config.yaml"))
		for _, e := range errs {
			fmt.Fprintf(&b, "\n  - %v", e)
		}
		return nil, errors.New(b.String())
	}
	return g, nil
}
