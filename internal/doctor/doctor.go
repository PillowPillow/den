// Package doctor diagnostique une installation den : configuration cohérente,
// stacks et repos présents, sbx disponible. Aucun effet de bord, aucun réseau.
package doctor

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
)

// Check est le résultat d'un diagnostic unitaire.
type Check struct {
	Nom    string
	OK     bool
	Detail string
}

// Deps injecte les accès système, pour que les tests tournent sans sbx installé
// et sans dépendre de l'arborescence réelle de la machine.
type Deps struct {
	LookPath func(string) (string, error)
	Stat     func(string) (os.FileInfo, error)
}

// DepsSysteme renvoie les dépendances réelles.
func DepsSysteme() Deps {
	return Deps{LookPath: exec.LookPath, Stat: os.Stat}
}

// Run exécute tous les diagnostics et renvoie la liste complète, échecs compris.
// On ne s'arrête jamais au premier problème : l'utilisateur doit tout voir d'un coup.
func Run(denHome string, d Deps) []Check {
	var checks []Check
	ajoute := func(nom string, ok bool, format string, args ...any) {
		checks = append(checks, Check{Nom: nom, OK: ok, Detail: fmt.Sprintf(format, args...)})
	}

	// 1. sbx présent
	if chemin, err := d.LookPath("sbx"); err != nil {
		ajoute("sbx", false, "binaire sbx introuvable dans le PATH")
	} else {
		ajoute("sbx", true, "%s", chemin)
	}

	// 2. config.yaml chargeable
	//
	// LoadGlobalSansValider, et non LoadGlobal : le second refuse une config
	// incohérente, ce qui ferait sortir Run juste en dessous et rendrait
	// l'étape 3 — comme les stacks et les nests — inatteignable. doctor est le
	// seul endroit du projet où « charger » et « juger » doivent rester
	// séparés, parce qu'il est le seul dont le travail est de tout montrer.
	g, err := config.LoadGlobalSansValider(denHome)
	if err != nil {
		ajoute("config.yaml", false, "%v", err)
		return checks // sans config, tout le reste est indécidable
	}
	ajoute("config.yaml", true, "%s/config.yaml", denHome)

	// 3. cohérence interne de la config
	erreursConfig := g.Validate()
	for _, e := range erreursConfig {
		ajoute("config", false, "%v", e)
	}
	if len(erreursConfig) == 0 {
		ajoute("config", true, "cohérente")
	}

	// 4. stacks
	stacks, err := config.LoadStacks(denHome)
	if err != nil {
		ajoute("stacks", false, "%v", err)
		stacks = map[string]*config.Stack{}
	} else {
		ajoute("stacks", true, "%d déclarée(s)", len(stacks))
	}
	if g.Defaults.Stack != "" {
		if _, ok := stacks[g.Defaults.Stack]; !ok {
			ajoute("defaults.stack", false,
				"stack %q introuvable dans %s/stacks", g.Defaults.Stack, denHome)
		} else {
			ajoute("defaults.stack", true, "%s", g.Defaults.Stack)
		}
	}

	// 5. profils agents : le dossier peut ne pas exister encore (créé au premier spawn),
	// on ne signale que ce qui est structurellement faux, pas l'absence.
	// Tri des noms : toute liste destinée à l'affichage est déterministe (map Go non ordonnée).
	for _, nom := range slices.Sorted(maps.Keys(g.Agents)) {
		if g.Agents[nom].Update == "" {
			ajoute("agent "+nom, false, "aucune commande update déclarée (spec §9.1)")
		}
	}

	// 6. nests : stack référencée existante, repos présents sur disque
	nests, casses, err := nest.ListNests(denHome)
	if err != nil {
		ajoute("nests", false, "%v", err)
		return checks
	}
	// Un nest cassé est signalé nommément et n'empêche pas de diagnostiquer les
	// autres : c'est précisément le rôle de doctor.
	for _, c := range casses {
		ajoute("nest "+c.Nom, false, "illisible : %v", c.Err)
	}
	for _, n := range nests {
		nomStack := n.Stack
		if nomStack == "" {
			nomStack = g.Defaults.Stack
		}
		if _, ok := stacks[nomStack]; !ok {
			ajoute("nest "+n.Name, false, "stack %q introuvable", nomStack)
		}
		for _, r := range n.Repos {
			if _, err := d.Stat(r.Path); err != nil {
				ajoute("nest "+n.Name, false, "repo introuvable : %s", r.Path)
			}
		}
	}
	if len(nests) > 0 || len(casses) > 0 {
		ajoute("nests", len(casses) == 0, "%d déclaré(s), %d illisible(s)", len(nests), len(casses))
	}

	return checks
}
