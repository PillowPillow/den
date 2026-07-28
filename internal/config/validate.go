package config

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"
)

var (
	modesSSH        = []string{"agent-forward", "mount", "none"}
	layoutsWorktree = []string{"central", "per-repo"}
)

// valideBinDir contrôle qu'une entrée de bin_dirs est bien un CHEMIN.
//
// agent.CommandeFraicheur les injecte littéralement dans un `export PATH=%q`,
// délibérément sans échappement : le `$HOME` qu'elles contiennent vise le home
// DE LA VM et doit être expansé par le bash de la VM, pas par den (invariant 1
// de CommandeFraicheur). Échapper pour se protéger tuerait donc la seule chose
// que ce champ doit savoir faire.
//
// Le défaut n'est pas un trou de sécurité — c'est la config de l'utilisateur,
// et `update:` est du shell arbitraire par contrat — mais un défaut de TYPAGE :
// `bin_dirs` est documenté comme une liste de chemins. On refuse donc ce qui
// n'en est pas un, et seulement cela.
//
// Chaque cas ci-dessous a été mesuré sur bash à partir de la ligne réellement
// générée :
//
//	"/opt/$(id -un)"  → /opt/agent   (substitution EXÉCUTÉE au boot de la VM)
//	"/opt/`id -un`"   → /opt/agent   (idem)
//	"/opt/a\tb"       → /opt/a\tb    (%q rend la tabulation en \t LITTÉRAL)
//	""                → PATH=":…"    (un élément vide vaut « répertoire courant »)
//	"$HOME/.local/bin"→ /home/…/bin  (attendu : doit traverser INTACT)
func valideBinDir(d string) error {
	if d == "" {
		return fmt.Errorf("entrée vide — un élément vide dans PATH y ajoute le répertoire courant")
	}
	if strings.Contains(d, "$(") {
		return fmt.Errorf(
			"%q contient une substitution de commande $( — bin_dirs est une liste de chemins, "+
				"et le bash de la VM l'exécuterait au démarrage", d)
	}
	if strings.Contains(d, "`") {
		return fmt.Errorf(
			"%q contient un backtick ` — bin_dirs est une liste de chemins, "+
				"et le bash de la VM l'exécuterait au démarrage", d)
	}
	// `$HOME` et `${HOME}` restent légitimes : ce sont eux la raison d'être du
	// champ. Seule la substitution de COMMANDE est refusée, pas l'expansion de
	// variable.
	for _, r := range d {
		if unicode.IsControl(r) {
			return fmt.Errorf(
				"%q contient un caractère de contrôle (%q) — il partirait échappé en littéral "+
					"dans le PATH de la VM, et le chemin serait corrompu", d, r)
		}
	}
	return nil
}

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
		// TrimSpace et non `== ""` : agent.CommandeFraicheur juge sur TrimSpace.
		// Tant que ce test-ci était plus laxiste, un `update: "   "` passait
		// `den doctor` en vert et n'échouait qu'au spawn — le plus tard et le
		// moins lisible des deux moments. Deux juges d'un même champ doivent
		// juger pareil, et c'est le plus strict qui fait foi.
		if strings.TrimSpace(a.Update) == "" {
			errs = append(errs, fmt.Errorf(
				"agents.%s.update : requis — une sandbox ne doit jamais démarrer avec un agent périmé (spec §9.1)", nom))
		}
		for i, d := range a.BinDirs {
			if err := valideBinDir(d); err != nil {
				// Clé INDEXÉE : jusqu'à plusieurs bin_dirs par agent, et
				// « bin_dirs » seul ne dirait pas lequel corriger.
				errs = append(errs, fmt.Errorf("agents.%s.bin_dirs[%d] : %w", nom, i, err))
			}
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
