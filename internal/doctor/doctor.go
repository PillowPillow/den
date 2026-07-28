// Package doctor diagnostique une installation den : configuration cohérente,
// stacks et repos présents, sbx disponible. Aucun effet de bord, aucun réseau.
package doctor

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"

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
	// VersionGit rend la sortie brute de `git --version`. Injectée au même
	// titre que LookPath et Stat : sans elle, le diagnostic de version
	// rendrait un verdict différent selon le git du poste, et le plancher ne
	// serait vérifiable sur aucune machine en particulier.
	VersionGit func() (string, error)
}

// DepsSysteme renvoie les dépendances réelles.
func DepsSysteme() Deps {
	return Deps{LookPath: exec.LookPath, Stat: os.Stat, VersionGit: versionGitSysteme}
}

// versionGitSysteme exécute `git --version`. Seul appel de doctor à lancer un
// processus : il ne lit aucun dépôt, n'écrit rien, et ne dépend pas du cwd.
func versionGitSysteme() (string, error) {
	out, err := exec.Command("git", "--version").Output()
	return string(out), err
}

// Plancher de version git. `git rev-parse --path-format=absolute` est le seul
// appel de den qui l'impose — internal/worktree/worktree.go:599 (identifie) et
// :611 (communDe), relevés par grep sur "path-format" — et cette option est
// apparue dans git 2.31. En dessous, git rejette l'option et l'utilisateur
// récolte le message de git au premier worktree, jamais un diagnostic de den.
//
// La version d'apparition vient des notes de version de git, pas d'une mesure :
// ce poste n'a qu'un seul git installé et ne peut pas l'établir. Ce qui EST
// mesuré ici, c'est quels appels de den portent l'option.
const (
	gitMajeurMin = 2
	gitMineurMin = 31
)

// analyseVersionGit extrait le couple majeur.mineur de la sortie de
// `git --version`. Les distributions suffixent librement — « 2.39.5 (Apple
// Git-154) », « 2.45.2.windows.1 » — donc on ne lit que les deux premiers
// nombres et on ignore le reste.
//
// Une sortie illisible est une ERREUR, pas un 0.0 : la traiter comme une
// version nulle ferait refuser un git parfaitement bon dont le packageur a
// changé le format.
func analyseVersionGit(sortie string) (majeur, mineur int, err error) {
	illisible := func() (int, int, error) {
		return 0, 0, fmt.Errorf("sortie de `git --version` illisible : %q", strings.TrimSpace(sortie))
	}
	champs := strings.Fields(sortie)
	if len(champs) < 3 || champs[0] != "git" || champs[1] != "version" {
		return illisible()
	}
	nombres := strings.Split(champs[2], ".")
	if len(nombres) < 2 {
		return illisible()
	}
	if majeur, err = strconv.Atoi(nombres[0]); err != nil {
		return illisible()
	}
	if mineur, err = strconv.Atoi(nombres[1]); err != nil {
		return illisible()
	}
	return majeur, mineur, nil
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

	// 1bis. git assez récent. Diagnostiqué à côté de sbx : ce sont les deux
	// binaires sans lesquels den ne peut rien, et aucun des deux ne dépend de
	// la configuration — un den home vide ne doit pas priver l'utilisateur de
	// ces deux réponses-là.
	if sortie, err := d.VersionGit(); err != nil {
		ajoute("git", false, "version de git indéterminable : %v", err)
	} else if majeur, mineur, err := analyseVersionGit(sortie); err != nil {
		ajoute("git", false, "%v", err)
	} else if majeur < gitMajeurMin || (majeur == gitMajeurMin && mineur < gitMineurMin) {
		// La version LUE et la version EXIGÉE : sans les deux, l'utilisateur
		// sait qu'il doit agir mais pas jusqu'où monter.
		ajoute("git", false,
			"%s est trop ancien : den exige git %d.%d ou plus — `git rev-parse --path-format=absolute`, "+
				"par lequel den situe tout worktree, n'existe pas avant",
			strings.TrimSpace(sortie), gitMajeurMin, gitMineurMin)
	} else {
		ajoute("git", true, "%s", strings.TrimSpace(sortie))
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

	// 4bis. kits des stacks : chaque chemin doit exister AVANT que sbx ne le
	// reçoive. Un kit manquant n'échoue pas au chargement de la config mais au
	// boot de la microVM, où le dispatcher fait `exit $rc` : l'utilisateur voit
	// une VM qui meurt, pas un message de den. Tri des noms de stacks : la liste
	// est destinée à l'affichage, une map Go n'est pas ordonnée.
	for _, nomStack := range slices.Sorted(maps.Keys(stacks)) {
		s := stacks[nomStack]
		// `kits:` d'abord, puis `kit:` : l'ordre du diagnostic suit l'ordre de
		// layering de sbx, pour que l'utilisateur lise ses kits comme il les
		// a écrits.
		cheminsKits := append(append([]string{}, s.Kits...), s.Kit)
		for _, k := range cheminsKits {
			if k == "" {
				continue // aucun kit déclaré : ce n'est pas une faute (spec §4.2)
			}
			if _, err := d.Stat(k); err != nil {
				ajoute("stack "+nomStack, false, "kit introuvable : %s", k)
			}
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
