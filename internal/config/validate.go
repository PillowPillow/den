package config

import (
	"fmt"
	"maps"
	"slices"
)

var (
	modesSSH        = []string{"agent-forward", "mount", "none"}
	layoutsWorktree = []string{"central", "per-repo"}
)

// Validate contrôle la cohérence interne de config.yaml et renvoie TOUTES les
// erreurs trouvées. Cumuler plutôt que s'arrêter à la première : `den doctor`
// doit montrer d'un coup tout ce qu'il y a à réparer.
func (g *Global) Validate() []error {
	var errs []error

	if len(g.Agents) == 0 {
		errs = append(errs, fmt.Errorf("agents : le registre est vide, déclare au moins un agent"))
	}

	noms := slices.Sorted(maps.Keys(g.Agents)) // déterminisme de l'ordre des erreurs

	for _, nom := range noms {
		a := g.Agents[nom]
		if a.ConfigDir == "" {
			errs = append(errs, fmt.Errorf("agents.%s.config_dir : requis", nom))
		}
		if a.Update == "" {
			errs = append(errs, fmt.Errorf(
				"agents.%s.update : requis — une sandbox ne doit jamais démarrer avec un agent périmé (spec §9.1)", nom))
		}
	}

	switch {
	case g.Defaults.Agent == "":
		errs = append(errs, fmt.Errorf("defaults.agent : requis"))
	default:
		if _, ok := g.Agents[g.Defaults.Agent]; !ok {
			errs = append(errs, fmt.Errorf(
				"defaults.agent : %q est absent du registre (agents déclarés : %v)", g.Defaults.Agent, noms))
		}
	}

	if g.Defaults.Stack == "" {
		errs = append(errs, fmt.Errorf("defaults.stack : requis"))
	}

	if !slices.Contains(modesSSH, g.SSH.Mode) {
		errs = append(errs, fmt.Errorf("ssh.mode : %q inconnu (attendu : %v)", g.SSH.Mode, modesSSH))
	}
	if g.SSH.Mode == "mount" && g.SSH.Dir == "" {
		errs = append(errs, fmt.Errorf("ssh.dir : requis quand ssh.mode vaut mount"))
	}

	if !slices.Contains(layoutsWorktree, g.WorktreeLayout) {
		errs = append(errs, fmt.Errorf(
			"worktree_layout : %q inconnu (attendu : %v)", g.WorktreeLayout, layoutsWorktree))
	}

	return errs
}
