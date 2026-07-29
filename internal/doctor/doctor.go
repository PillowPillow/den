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
// Une sortie illisible est une ERREUR, pas un 0.0. Ce que cela change,
// exactement : **rien au verdict**. Run rend un check en échec dans les deux
// cas, et `den doctor` sort non-zéro dès qu'un check échoue — un git
// parfaitement bon dont le packageur aurait changé le format EST donc refusé,
// des deux façons.
//
// Ce que cela change, c'est le MESSAGE, et c'est là tout l'intérêt : l'erreur
// nomme la sortie réellement reçue, là où un 0.0 annoncerait « git 0.0 est trop
// ancien, den exige 2.31 » à quelqu'un qui a peut-être git 2.99, et l'enverrait
// chercher un problème de version qu'il n'a pas. Ce qui reste faux — le refus
// d'un git bon au format inattendu — se répare en élargissant le parseur, pas
// en devinant un numéro.
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
		// Échec STRUCTUREL seulement (dossier stacks/ illisible) : une stack qui
		// ne se décode pas n'atteint plus jamais cette branche, elle part dans
		// Cassees juste en dessous.
		ajoute("stacks", false, "%v", err)
		stacks = config.Stacks{Saines: map[string]*config.Stack{}}
	} else {
		// Même format et même verdict que la ligne « nests », sa voisine
		// immédiate à l'écran : deux totaux côte à côte qui ne comptent pas
		// pareil se lisent comme une contradiction, et c'est le total qu'on
		// parcourt en diagonale.
		ajoute("stacks", len(stacks.Cassees) == 0, "%d déclarée(s), %d illisible(s)",
			len(stacks.Saines), len(stacks.Cassees))
	}
	// Chaque stack cassée est nommée SÉPARÉMENT, comme les nests cassés plus
	// bas. Avant la séparation Saines/Cassees, une seule stack fautive faisait
	// échouer LoadStacks en bloc : doctor retombait sur une map vide et rendait
	// ensuite « defaults.stack introuvable » puis « nest X : stack introuvable »
	// pour des stacks parfaitement saines — des diagnostics FAUX qui envoyaient
	// réparer le mauvais fichier.
	for _, c := range stacks.Cassees {
		ajoute("stack "+c.Nom, false, "illisible : %v", c.Err)
	}
	if g.Defaults.Stack != "" {
		// Get et non un test d'appartenance : lui seul distingue « illisible » de
		// « pas déclarée », et c'est la source unique de ce verdict.
		if _, err := stacks.Get(g.Defaults.Stack); err != nil {
			// Sans suffixe : Get situe déjà ce qu'il faut situer, et le coller
			// derrière une erreur YAML multi-ligne la rendait trompeuse.
			ajoute("defaults.stack", false, "%v", err)
		} else {
			ajoute("defaults.stack", true, "%s", g.Defaults.Stack)
		}
	}

	// 4bis. kits des stacks : chaque chemin doit exister AVANT que sbx ne le
	// reçoive. Un kit manquant n'échoue pas au chargement de la config mais au
	// boot de la microVM, où le dispatcher fait `exit $rc` : l'utilisateur voit
	// une VM qui meurt, pas un message de den. Tri des noms de stacks : la liste
	// est destinée à l'affichage, une map Go n'est pas ordonnée.
	// Noms ne rend que les stacks SAINES : une stack cassée n'a pas de kits à
	// contrôler, et a déjà été signalée nommément juste au-dessus.
	for _, nomStack := range stacks.Noms() {
		// KitsDeclares est la source UNIQUE de « quels kits, dans quel ordre » :
		// elle rend `kits:` puis `kit:`, entrées vides filtrées. Recomposer la
		// liste ici — ce que faisait la version précédente — laissait doctor et
		// le chemin de spawn diverger sur les entrées vides, chacun restant vert
		// de son côté.
		for _, k := range stacks.Saines[nomStack].KitsDeclares() {
			if _, err := d.Stat(k); err != nil {
				ajoute("stack "+nomStack, false, "kit introuvable : %s", k)
			}
		}
	}

	// 4ter. ssh.dir, en mode mount seulement : c'est le seul mode où il est
	// monté en workspace et part donc dans l'argv de `sbx create`. Hors de ce
	// mode il n'est monté nulle part et son absence n'est pas une faute.
	// Validate() ne juge que « déclaré ou non » ; « déclaré mais absent du
	// disque » demande une sonde du système, d'où d.Stat.
	if g.SSH.Mode == "mount" && g.SSH.Dir != "" {
		if _, err := d.Stat(g.SSH.Dir); err != nil {
			ajoute("ssh.dir", false,
				"%s introuvable — en mode « mount » ce dossier est monté dans la sandbox, "+
					"et un chemin absent y monterait un dossier vide à la place des clés", g.SSH.Dir)
		} else {
			ajoute("ssh.dir", true, "%s", g.SSH.Dir)
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
		// Get : le nest doit apprendre si SA stack est illisible ou absente, deux
		// réparations différentes. Un test d'appartenance dirait « introuvable »
		// des deux, et c'est ce qui rendait doctor faux quand une autre stack
		// cassait le chargement entier.
		if _, err := stacks.Get(nomStack); err != nil {
			ajoute("nest "+n.Name, false, "%v", err)
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
