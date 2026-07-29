// Package worktree propage un worktree git sur les repos d'un nest. C'est le
// seul module de den qui pilote git ; comme sbx, il le fait derrière une
// interface pour rester substituable.
package worktree

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

// Git est l'accès à la CLI git, injecté pour rester substituable.
type Git interface {
	Run(ctx context.Context, dir string, args ...string) ([]byte, error)
	// RunAvecEntree alimente en plus l'entrée standard de git. Elle existe pour
	// les sous-commandes qui n'acceptent une liste de chemins SANS CITATION que
	// par `--stdin` : `check-ignore -z` répond pour mille dossiers en un fork
	// là où l'argv en exigeait mille, et git refuse `-z` hors de `--stdin`.
	RunAvecEntree(ctx context.Context, dir string, entree []byte, args ...string) ([]byte, error)
}

type gitExec struct{}

// NewGit renvoie l'accès réel au git du PATH.
func NewGit() Git { return gitExec{} }

// VariablesRedirigeantes sont les variables d'environnement qui désignent le
// dépôt cible et qui sont PRIORITAIRES sur le répertoire courant : tant qu'elles
// sont posées, cmd.Dir n'isole rien. den est fait pour tourner sous des agents
// et depuis des hooks git, où elles sont couramment exportées ; sans ce
// filtrage, fichiersSales déciderait d'une suppression d'après l'état d'un autre
// dépôt. On les RETIRE de l'environnement : les poser à vide ne les neutralise
// pas — git échoue alors sur un `not a git repository` pour chaque commande.
//
// Exportée : les paquets dont les tests lancent du git réel (internal/cli en
// particulier) doivent neutraliser le MÊME jeu de variables que le code de
// production, sous peine d'écrire dans un dépôt tiers désigné par
// l'environnement — voir le TestMain de ce paquet, et celui d'internal/cli.
var VariablesRedirigeantes = []string{
	"GIT_DIR",
	"GIT_COMMON_DIR",
	"GIT_WORK_TREE",
	"GIT_INDEX_FILE",
	"GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES",
	"GIT_NAMESPACE",
}

// NeutraliseEnvironnementGit rend l'environnement du PROCESSUS DE TEST
// hermétique à la configuration git de la machine et aux variables qui
// redirigent git vers un autre dépôt (VariablesRedirigeantes) — à appeler
// depuis le TestMain de tout paquet dont les tests lancent du git réel, que ce
// soit via ce paquet ou en direct (exec.Command("git", …)).
//
// Vit dans un fichier de PRODUCTION, pas dans un _test.go, à dessein : un
// TestMain d'un autre paquet (internal/cli, internal/spawn) l'importe comme
// n'importe quelle dépendance normale — les symboles d'un _test.go ne sont
// visibles que dans leur propre paquet. Même raison que sbx.Fake vit dans le
// paquet de production (voir son commentaire).
//
// Née d'un défaut apparu DEUX FOIS après sa première fermeture : ce paquet
// avait fermé le trou pour lui-même (voir son propre TestMain), puis
// internal/cli l'a rouvert sans le savoir, puis internal/spawn — mesuré : un
// `go test ./internal/spawn/...` lancé sous GIT_DIR/GIT_WORK_TREE désignant un
// dépôt tiers y a ajouté 32 commits. Centraliser la neutralisation ici ne
// l'IMPOSE à aucun paquet ; TestExigeNeutraliseEnvironnementGitSiGitEstLanceEnClair,
// dans hermetisme_test.go, ferme structurellement le trou plutôt que de
// compter sur la mémoire d'un futur auteur de test.
//
// RÉSERVÉE À UN TestMain (aucun garde-fou qui l'empêche d'être appelée
// ailleurs, ni depuis du code de production den lui-même) : elle modifie
// l'environnement du PROCESSUS ENTIER, pas celui d'un test isolé. L'appeler
// hors d'un TestMain, ou plusieurs fois, ne casse rien en soi (chaque appel
// est idempotent — Setenv/Unsetenv sur les mêmes clés), mais poser ces
// variables pour tout le reste du processus (y compris un binaire den réel
// qui importerait ce paquet) serait un effet de bord que rien ne justifie.
func NeutraliseEnvironnementGit() {
	os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	os.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	// Neutraliser les deux fichiers ne suffit pas : git accepte aussi de la
	// configuration par l'environnement.
	os.Unsetenv("GIT_CONFIG_COUNT")
	os.Unsetenv("GIT_CONFIG_PARAMETERS")
	for _, v := range VariablesRedirigeantes {
		os.Unsetenv(v)
	}
}

// environnementNeutre rend l'environnement courant privé des variables qui
// détourneraient git vers un autre dépôt que celui demandé.
func environnementNeutre() []string {
	brut := os.Environ()
	propre := make([]string, 0, len(brut))
	for _, v := range brut {
		nom, _, _ := strings.Cut(v, "=")
		if slices.Contains(VariablesRedirigeantes, nom) {
			continue
		}
		propre = append(propre, v)
	}
	return propre
}

func (g gitExec) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return g.RunAvecEntree(ctx, dir, nil, args...)
}

func (gitExec) RunAvecEntree(ctx context.Context, dir string, entree []byte, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = environnementNeutre()
	// Stdin laissé nil (donc /dev/null) quand il n'y a rien à écrire : une
	// sous-commande qui lirait l'entrée standard bloquerait sinon sans borne.
	if entree != nil {
		cmd.Stdin = bytes.NewReader(entree)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// Le CONTEXTE est en français, le DÉTAIL reste celui de git, donc en
		// anglais : « … : fatal: … ». C'est une décision de projet, tenue par
		// tout le module. Traduire supposerait de reconnaître les messages de
		// git — un jeu ouvert, qui change à chaque version — et un message
		// approximatif vaut moins que le message exact, cherchable tel quel.
		// Là où l'anglais de git serait la SEULE chose que l'utilisateur lise,
		// den nomme lui-même la sortie (voir Assure et Retire).
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return stdout.Bytes(), fmt.Errorf("git %s (dans %s) : %s", strings.Join(args, " "), dir, detail)
	}
	return stdout.Bytes(), nil
}

// Chemin calcule où vit le worktree wt du repo, selon le layout (spec §13.5).
//
//	central  : <root>/<wt>/<repo>     — défaut, tous les worktrees d'un même
//	                                    wt voisins, ce qui rend le co-montage
//	                                    multi-repo lisible
//	per-repo : <repo>/.den/<wt>       — pour qui préfère garder ses worktrees
//	                                    près de leur dépôt
func Chemin(layout, root, wt, cheminRepo string) string {
	if layout == "per-repo" {
		return filepath.Join(cheminRepo, ".den", wt)
	}
	return filepath.Join(root, wt, filepath.Base(cheminRepo))
}

// Nom porte les DEUX noms d'un worktree, qui ne coïncident pas toujours.
//
// Dossier est le composant aplati (config.AplatitComposantSandbox) : il nomme
// le dossier du worktree ET le suffixe du nom de sandbox. Il DOIT rester un
// composant de chemin plat — den n'a pas d'autre état que le nom de sandbox, et
// `den rm` retrouve le dossier à partir de ce seul nom, par Chemin. Un
// « feature/123 » en dossier creuserait un sous-dossier introuvable par ce
// chemin-là.
//
// Branche est le nom réel de la branche git, tel que l'utilisateur l'a tapé.
// C'est le nom qu'il verra dans son `git log`, dans sa forge et dans sa PR :
// l'aplatir serait renommer son travail.
type Nom struct {
	Dossier string
	Branche string
}

// Assure garantit l'existence du worktree wt pour ce repo et renvoie son
// chemin. Idempotent : si le worktree existe déjà SUR LA BONNE BRANCHE et
// APPARTIENT BIEN À CE REPO, il est laissé tel quel.
//
// Un worktree existant sur une AUTRE branche est une erreur, jamais un checkout
// silencieux : basculer la branche d'un worktree où l'utilisateur travaille
// déplacerait son travail sans qu'il l'ait demandé. Ce contrôle porte AUSSI la
// collision d'aplatissement : « feat/essai » et « feat-essai » visent le même
// dossier, et c'est la comparaison de branches qui les distingue.
func Assure(ctx context.Context, g Git, layout, root string, wt Nom, cheminRepo string) (string, error) {
	if err := verifieRepo(cheminRepo); err != nil {
		return "", err
	}

	chemin := Chemin(layout, root, wt.Dossier, cheminRepo)

	// En per-repo, le worktree naît À L'INTÉRIEUR du dépôt : sans exclusion, il
	// laisse un « ?? .den/ » à demeure dans le git status de l'utilisateur.
	if layout == "per-repo" {
		if err := excludeDossierDen(ctx, g, cheminRepo); err != nil {
			return "", err
		}
	}

	if _, err := os.Stat(chemin); err == nil {
		if err := verifieAppartenance(ctx, g, chemin, cheminRepo); err != nil {
			return "", err
		}
		actuelle, err := brancheCourante(ctx, g, chemin)
		if err != nil {
			return "", fmt.Errorf(
				"%s existe déjà mais n'est pas un worktree git exploitable : %w", chemin, err)
		}
		if actuelle != wt.Branche {
			return "", fmt.Errorf(
				"le worktree %s est sur la branche %q, pas %q — choisis un autre nom de worktree "+
					"ou bascule ce dossier sur %q à la main", chemin, actuelle, wt.Branche, wt.Branche)
		}
		return chemin, nil // déjà en place : idempotent
	}

	// Dossier absent mais enregistrement toujours vivant : git refuserait avec
	// un « fatal: … missing but already registered worktree » anglais, et rien
	// dans den n'en sortirait. On nomme la sortie.
	enregistre, err := estEnregistre(ctx, g, cheminRepo, chemin)
	if err != nil {
		return "", err
	}
	if enregistre {
		return "", fmt.Errorf(
			"le worktree %s est encore enregistré dans %s alors que son dossier a disparu — "+
				"lance `den rm` sur ce nest pour effacer l'enregistrement, puis re-spawne",
			chemin, cheminRepo)
	}

	// `git worktree add <chemin> <branche>` si la branche existe déjà,
	// `-b <branche>` sinon : git refuse de recréer une branche existante.
	args := []string{"worktree", "add", chemin, wt.Branche}
	if !brancheExiste(ctx, g, cheminRepo, wt.Branche) {
		// Un HEAD qui ne se résout pas ne donne aucun point de départ à la
		// nouvelle branche. Le contrôle vit ICI et pas plus haut parce que c'est
		// la seule branche qu'un tel dépôt peut atteindre — la branche demandée
		// ne peut pas exister dans cet état, donc brancheExiste est forcément
		// faux — et que le placer en tête ferait payer un `rev-parse` au chemin
		// idempotent, où le worktree est déjà en place.
		//
		// Sans lui, git rend un message exact de son point de vue et
		// inactionnable du nôtre, mesuré sur un dépôt vierge :
		//
		//	No possible source branch, inferring '--orphan'
		//	fatal: options '--orphan' and '--track' cannot be used together
		//
		// L'utilisateur n'a écrit ni --orphan ni --track : le premier vient de
		// l'inférence de git, le second de ce que git range --no-track dans la
		// famille --track.
		//
		// Le message énonce ce que la sonde MESURE — HEAD ne se résout pas — et
		// non ce qu'on aimerait en déduire. Une version antérieure affirmait
		// « le dépôt n'a aucun commit » : vrai sur un `git init` vierge, FAUX
		// sur une branche orpheline, où le dépôt a des commits (mesuré :
		// `rev-list --all --count` rend 1) et où seul HEAD est en cause. Les
		// deux causes possibles sont donc nommées, sans trancher entre elles.
		if !headResolvable(ctx, g, cheminRepo) {
			return "", fmt.Errorf(
				"création du worktree %q : HEAD de %s ne pointe sur aucun commit (dépôt vierge, "+
					"ou branche orpheline non encore commitée) — git n'a aucun point de départ "+
					"à donner à la nouvelle branche ; commite d'abord sur ce dépôt, puis relance",
				wt.Branche, cheminRepo)
		}
		// --no-track : le point de départ est une ref de suivi, et sans lui git
		// ferait suivre origin/<défaut> à la branche de travail — `git push`
		// échouerait ensuite en proposant de pousser sur la branche par défaut.
		args = []string{"worktree", "add", "--no-track", "-b", wt.Branche, chemin}
		// Spec §13.4-3 : la branche part de la branche par défaut du repo. Repli
		// sur le HEAD courant si le dépôt n'a pas d'origin/HEAD — un dépôt
		// purement local est parfaitement légitime, et c'est le seul point de
		// départ qu'on puisse alors nommer.
		if depart, ok := brancheParDefaut(ctx, g, cheminRepo); ok {
			args = append(args, depart)
		}
	}
	if _, err := g.Run(ctx, cheminRepo, args...); err != nil {
		return "", fmt.Errorf("création du worktree %q de %s : %w", wt.Branche, cheminRepo, err)
	}
	return chemin, nil
}

// Cible désigne le worktree à retirer. Retire en DÉRIVE le chemin par Chemin
// plutôt que de le recevoir : la garde « ce dossier appartient-il bien à ce
// repo ? » reposait sinon entièrement sur l'appelant, et den n'a aucune raison
// de retirer un dossier qu'il n'aurait pas lui-même placé.
type Cible struct {
	DenHome string // racine de den ; la corbeille vit sous <DenHome>/trash
	Layout  string // « central » ou « per-repo » (spec §13.5)
	Root    string // worktree_root
	Nest    string // identité lisible du nest (« api », « api.feat12 ») : nomme l'entrée de corbeille
	// Worktree est le nom de DOSSIER du worktree (Nom.Dossier), et non celui de
	// sa branche : `den rm` ne dispose que du nom de sandbox, d'où il ne peut
	// tirer que le composant aplati. La branche n'est pas nécessaire ici — den
	// ne la supprime jamais, elle survit dans le dépôt.
	Worktree   string
	CheminRepo string
	Force      bool
}

// Retire met le worktree à la corbeille et rend le chemin de l'entrée créée —
// ou « » si le dossier avait déjà disparu. Refuse si l'arbre est sale et que
// Force est faux (spec §14).
//
// den ne SUPPRIME jamais, il déplace. Ce n'est pas de la prudence
// d'implémentation, c'est le résultat d'une mesure : cinq tours de relecture
// ont établi que l'énumération des façons dont `git status` cache du travail
// NE CONVERGE PAS — git ajoute un mécanisme de cache par version (untracked
// cache en 2.8, fsmonitor par hook en 2.16, `core.fsmonitor=true` en 2.37) — et
// que le filet de `git worktree remove` tombe AVEC celui de `status`, parce que
// c'est le même code. Il n'y a pas de second filet.
//
// La corbeille change donc la NATURE du problème plutôt que d'en fermer un
// membre de plus : tout ce que le verdict rate — les mécanismes de cache à
// venir, et l'angle mort assumé des règles d'ignorance (voir fichiersSales) —
// passe de « perte de données » à « un dossier que l'utilisateur remonte d'un
// `mv` ». fichiersSales subsiste, mais comme ERGONOMIE : prévenir avant
// d'agir, plus protéger.
//
// Ce que la corbeille ne rend PAS : le worktree déplacé garde un fichier `.git`
// qui pointe vers un enregistrement désormais élagué. On récupère des FICHIERS,
// pas un worktree opérationnel. Les commits, eux, n'ont jamais été en jeu — la
// branche survit dans le dépôt.
func Retire(ctx context.Context, g Git, c Cible) (string, error) {
	cheminRepo, cheminWorktree, force := c.CheminRepo, Chemin(c.Layout, c.Root, c.Worktree, c.CheminRepo), c.Force

	// AVANT tout le reste : sans emplacement où poser le dossier, den ne fait
	// rien du tout. Plus bas, ce refus arrivait APRÈS le contrôle de saleté, qui
	// invitait alors l'utilisateur à « envoyer à la corbeille trash » — un chemin
	// relatif qui ne désigne rien.
	if c.DenHome == "" {
		return "", fmt.Errorf(
			"retrait du worktree %s : aucun den_home fourni — den ne supprime pas les worktrees, "+
				"il les déplace, et il lui faut donc un emplacement où les poser", cheminWorktree)
	}

	if _, err := os.Stat(cheminWorktree); os.IsNotExist(err) {
		// Le dossier est parti (rm -rf de l'utilisateur, `add` interrompu) mais
		// l'enregistrement git lui survit et bloquerait tout Assure ultérieur.
		// Rendre nil sans rien faire, c'était prétendre avoir nettoyé.
		if _, err := g.Run(ctx, cheminRepo, "worktree", "prune"); err != nil {
			return "", fmt.Errorf("nettoyage des enregistrements de worktree de %s : %w", cheminRepo, err)
		}
		// `prune` saute SILENCIEUSEMENT les worktrees verrouillés (rc=0, aucune
		// sortie) — et `git worktree lock` existe précisément pour les volumes
		// amovibles et les montages réseau, donc pour le cas où le dossier
		// disparaît légitimement. Sans cette revérification, den rendrait nil en
		// prétendant avoir nettoyé, et Assure renverrait ensuite l'utilisateur
		// vers `den rm` — la commande qui vient de lui dire qu'elle a réussi.
		enregistre, err := estEnregistre(ctx, g, cheminRepo, cheminWorktree)
		if err != nil {
			return "", err
		}
		if enregistre {
			return "", fmt.Errorf(
				"l'enregistrement du worktree %s survit dans %s alors que son dossier a disparu — "+
					"il est probablement verrouillé : lance `git worktree unlock %s` dans %s, puis relance",
				cheminWorktree, cheminRepo, cheminWorktree, cheminRepo)
		}
		efaceDossierPorteur(c, cheminWorktree)
		return "", nil
	}

	if err := verifieAppartenance(ctx, g, cheminWorktree, cheminRepo); err != nil {
		return "", err
	}

	// Le verrou se contrôle AVANT de déplacer. `git worktree lock` existe pour
	// les volumes amovibles et les montages réseau, et `prune` saute
	// SILENCIEUSEMENT les worktrees verrouillés (rc=0, aucune sortie) : déplacer
	// d'abord laisserait un dossier en corbeille ET un enregistrement vivant qui
	// bloque tout Assure ultérieur, sans que rien ne le dise.
	verrouille, err := estVerrouille(ctx, g, cheminRepo, cheminWorktree)
	if err != nil {
		return "", err
	}
	if verrouille {
		return "", fmt.Errorf(
			"le worktree %s est verrouillé dans %s — lance `git worktree unlock %s` dans %s, puis relance",
			cheminWorktree, cheminRepo, cheminWorktree, cheminRepo)
	}

	if !force {
		sales, err := fichiersSales(ctx, g, cheminWorktree)
		if err != nil {
			return "", err
		}
		if len(sales) > 0 {
			return "", fmt.Errorf(
				"le worktree %s contient des modifications non commitées (%s) — commite-les, ou "+
					"relance avec --force pour l'envoyer à la corbeille %s, ou avec --keep-worktrees "+
					"pour garder le dossier en place",
				cheminWorktree, listeCourte(sales), corbeillePrincipale(c))
		}
	}

	dest, err := metALaCorbeille(c, cheminWorktree)
	if err != nil {
		return "", err
	}

	// Le dossier a bougé, donc l'enregistrement est devenu élaguable. Sans ce
	// prune, Assure refuserait pour toujours de recréer ce worktree.
	//
	// `prune` est GLOBAL au dépôt : il efface aussi les enregistrements
	// orphelins d'autres nests visant le même repo. Cas dégénéré assumé — ce
	// qu'il efface est par définition un enregistrement dont le dossier a déjà
	// disparu, donc aucun travail n'est en jeu ; et l'autre nest se
	// re-spawnerait de toute façon. git n'offre pas d'élagage ciblé.
	if _, err := g.Run(ctx, cheminRepo, "worktree", "prune"); err != nil {
		// Le déplacement, lui, a bien eu lieu : le taire enverrait l'utilisateur
		// chercher son travail là où il n'est plus.
		return dest, fmt.Errorf(
			"le worktree %s est à la corbeille (%s) mais son enregistrement n'a pas pu être "+
				"élagué dans %s : %w", cheminWorktree, dest, cheminRepo, err)
	}

	efaceDossierPorteur(c, cheminWorktree)
	purgeCorbeille(filepath.Dir(dest), time.Now())
	return dest, nil
}

// efaceDossierPorteur retire le dossier qui PORTAIT le worktree, s'il est
// devenu vide :
//
//	central  : <root>/<wt>     — partagé par tous les repos du nest
//	per-repo : <repo>/.den
//
// os.Remove et non un RemoveAll : le refus ENOTEMPTY est le mécanisme, pas un
// accident à contourner. Il garde le dossier tant qu'un autre repo du même nest
// y a encore son worktree — les Retire d'un `den rm` défilent repo par repo, et
// seul le dernier trouve le dossier vide — et il garde aussi ce que den n'a pas
// posé : un fichier de l'utilisateur, ou la corbeille de repli `<repo>/.den/
// .trash` quand le rename inter-systèmes de fichiers a dû s'y replier.
//
// L'erreur est ignorée, à dessein : le worktree, lui, EST retiré. Faire échouer
// `den rm` sur un dossier vide qui résiste ferait passer un détail cosmétique
// pour un échec de suppression.
//
// Le refus sur Worktree vide n'est pas de la superstition : Retire est
// exportée, et Chemin("central", root, "", repo) rend `<root>/<repo>`, dont le
// dossier porteur est worktree_root LUI-MÊME. Un appel sans nom de worktree
// effacerait donc la racine de l'utilisateur si elle se trouvait vide.
func efaceDossierPorteur(c Cible, cheminWorktree string) {
	if c.Worktree == "" {
		return
	}
	_ = os.Remove(filepath.Dir(cheminWorktree))
}

// corbeillePrincipale est l'emplacement nominal de la corbeille.
func corbeillePrincipale(c Cible) string { return filepath.Join(c.DenHome, "trash") }

// corbeilleDeRepli désigne un emplacement qui partage FORCÉMENT le système de
// fichiers du worktree, puisque c'est le dossier qui le porte :
//
//	central  : <worktree_root>/.trash
//	per-repo : <repo>/.den/.trash    — déjà exclu par excludeDossierDen
//
// Le point initial n'est pas cosmétique. Sans lui, `<worktree_root>/trash`
// entrerait en collision avec le worktree d'un nest lancé avec `-w trash` ; git
// refuse en revanche tout composant de ref commençant par un point, si bien
// que « .trash » ne peut pas être un nom de worktree.
func corbeilleDeRepli(c Cible) string {
	if c.Layout == "per-repo" {
		return filepath.Join(c.CheminRepo, ".den", ".trash")
	}
	return filepath.Join(c.Root, ".trash")
}

// metALaCorbeille déplace le worktree et rend le chemin de l'entrée créée.
//
// Le repli sur EXDEV n'est pas une commodité : den_home et worktree_root sont
// deux réglages indépendants, et rien n'oblige un worktree_root posé sur un
// disque rapide à partager le système de fichiers de ~/.den. Recopier octet à
// octet pour contourner serait long sur un gros worktree, et interruptible — au
// milieu, l'utilisateur aurait deux demi-copies. Le repli, lui, reste un
// rename : atomique ou rien.
func metALaCorbeille(c Cible, cheminWorktree string) (string, error) {
	base := nomEntreeCorbeille(time.Now(), c.Nest, c.CheminRepo)

	dest, err := deplaceVers(corbeillePrincipale(c), base, cheminWorktree)
	if err == nil {
		return dest, nil
	}
	if !errors.Is(err, syscall.EXDEV) {
		return "", err
	}
	repli, errRepli := deplaceVers(corbeilleDeRepli(c), base, cheminWorktree)
	if errRepli != nil {
		return "", fmt.Errorf("%w ; le repli sous %s a échoué lui aussi : %v",
			err, corbeilleDeRepli(c), errRepli)
	}
	return repli, nil
}

func deplaceVers(dirCorbeille, base, src string) (string, error) {
	if err := os.MkdirAll(dirCorbeille, 0o755); err != nil {
		return "", fmt.Errorf("création de la corbeille %s : %w", dirCorbeille, err)
	}
	dest, err := entreeLibre(dirCorbeille, base)
	if err != nil {
		return "", err
	}
	if err := os.Rename(src, dest); err != nil {
		return "", fmt.Errorf("déplacement de %s vers %s : %w", src, dest, err)
	}
	return dest, nil
}

// nomEntreeCorbeille nomme une entrée « <horodatage>-<nest>-<repo> » : sans le
// nest ni le repo, une corbeille de plusieurs entrées est un tas anonyme dans
// lequel l'utilisateur ne retrouve pas SON dossier.
func nomEntreeCorbeille(quand time.Time, nest, cheminRepo string) string {
	return fmt.Sprintf("%s-%s-%s",
		quand.Format(formatHorodatage), composantSur(nest), composantSur(filepath.Base(cheminRepo)))
}

// composantSur rend une chaîne utilisable comme composant de nom de dossier.
// Le nom du nest est validé ailleurs, mais le basename du repo vient d'un
// chemin de configuration : « .. » y désignerait le parent de la corbeille.
func composantSur(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '/' || r == filepath.Separator || r < ' ' {
			return '_'
		}
		return r
	}, s)
	if s == "" || s == "." || s == ".." {
		return "_"
	}
	return s
}

// formatHorodatage préfixe les entrées de corbeille. Trié lexicalement il est
// trié chronologiquement, ce qui rend un `ls` de la corbeille lisible.
const formatHorodatage = "20060102-150405"

// retentionCorbeille est le délai au-delà duquel une entrée est purgée.
//
// Trente jours, et la purge se déclenche à chaque mise à la corbeille réussie
// plutôt que depuis une commande dédiée. Le raisonnement : une corbeille que
// personne ne vide est une fuite de disque silencieuse, et une commande `den
// gc` qu'il faut penser à lancer ne serait lancée que par ceux qui n'en ont pas
// besoin. Trente jours couvrent largement le délai réel entre « j'ai lancé
// `den rm` » et « il me manque quelque chose » ; en deçà, un mois de vacances
// suffirait à perdre le fichier que la corbeille existe pour garder.
//
// Conséquence assumée : qui ne relance jamais `den rm` ne purge jamais. C'est
// le bon sens du compromis — den ne tourne pas en tâche de fond, et une purge
// qui s'exécuterait sans qu'on ait rien demandé serait une suppression
// spontanée, exactement ce que la corbeille existe pour supprimer.
const retentionCorbeille = 30 * 24 * time.Hour

// entreeLibre rend un chemin d'entrée non occupé sous dirCorbeille : deux
// worktrees du même nest et du même repo mis à la corbeille dans la MÊME
// SECONDE portent sinon le même nom, et os.Rename écraserait ou échouerait.
//
// TOCTOU CONNU, non fermé : entre ce contrôle et le os.Rename de l'appelant, un
// tiers peut créer l'entrée. `rename(2)` écrase alors SILENCIEUSEMENT une
// destination qui serait un dossier VIDE. Le fermer demanderait
// `renameat2(RENAME_NOREPLACE)`, absent de la stdlib et de macOS ; la fenêtre
// est de l'ordre de la microseconde et n'a d'autre acteur possible qu'un second
// den lancé sur le même nest à la même seconde.
func entreeLibre(dirCorbeille, base string) (string, error) {
	const maxTentatives = 1000
	for i := 0; i < maxTentatives; i++ {
		candidat := filepath.Join(dirCorbeille, base)
		if i > 0 {
			candidat = filepath.Join(dirCorbeille, fmt.Sprintf("%s-%d", base, i+1))
		}
		// Lstat et non Stat : une entrée de corbeille qui serait un lien
		// symbolique cassé occupe le nom tout autant.
		_, err := os.Lstat(candidat)
		if os.IsNotExist(err) {
			return candidat, nil
		}
		if err != nil {
			return "", fmt.Errorf("inspection de l'entrée de corbeille %s : %w", candidat, err)
		}
	}
	return "", fmt.Errorf(
		"corbeille %s : %d entrées portent déjà le nom %q", dirCorbeille, maxTentatives, base)
}

// purgeCorbeille efface les entrées dont la rétention est dépassée.
//
// La date lue est celle du NOM, posée par den au moment du déplacement, et non
// la mtime du dossier : un worktree dont les fichiers datent de six mois serait
// purgé le jour même de sa mise à la corbeille. Une entrée dont le nom ne porte
// pas d'horodatage lisible n'est JAMAIS effacée — l'utilisateur a le droit de
// ranger quelque chose là, et une purge est elle-même une suppression.
//
// Les erreurs sont ignorées, entrée par entrée : le worktree est déjà à
// l'abri à ce stade, et échouer sur le ménage donnerait à l'appelant une erreur
// pour une opération qui a réussi. Une entrée qui résiste sera réexaminée à la
// prochaine mise à la corbeille.
func purgeCorbeille(dirCorbeille string, maintenant time.Time) {
	entrees, err := os.ReadDir(dirCorbeille)
	if err != nil {
		return
	}
	for _, e := range entrees {
		if !e.IsDir() {
			continue
		}
		nom := e.Name()
		if len(nom) <= len(formatHorodatage) || nom[len(formatHorodatage)] != '-' {
			continue
		}
		quand, err := time.ParseInLocation(formatHorodatage, nom[:len(formatHorodatage)], time.Local)
		if err != nil {
			continue
		}
		if maintenant.Sub(quand) <= retentionCorbeille {
			continue
		}
		_ = os.RemoveAll(filepath.Join(dirCorbeille, nom))
	}
}

// verifieRepo distingue l'absence du refus d'accès : diagnostiquer
// « introuvable » sur un EACCES enverrait l'utilisateur chercher le mauvais
// problème.
func verifieRepo(cheminRepo string) error {
	if _, err := os.Stat(cheminRepo); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("repo introuvable : %s : %w", cheminRepo, err)
		}
		return fmt.Errorf("repo inaccessible : %s : %w", cheminRepo, err)
	}
	return nil
}

// verifieAppartenance répond à la seule question qui compte avant de faire
// confiance à un dossier : « est-ce bien la racine d'un worktree DE CE repo ? ».
//
// Deux pièges qu'elle ferme d'un coup :
//   - git répond pour le premier dépôt trouvé EN REMONTANT ; un dossier vide
//     sous un dépôt (le cas systématique du layout per-repo) passerait donc pour
//     un worktree valide ;
//   - worktree_root est global et Chemin ne retient que le basename du repo :
//     deux nests visant des repos homonymes tombent sur le même dossier, et le
//     second repartirait avec le worktree du premier.
//
// Elle ne ferme pas — et ne PEUT pas fermer — le cas de deux repos homonymes
// qui sont le MÊME dépôt (un clone et l'un de ses worktrees, deux chemins vers
// un même `--git-common-dir`) : le discriminant EST le dossier git commun, donc
// il les déclare identiques, à raison. Cas dégénéré assumé ; l'utilisateur y
// obtient bien le worktree du dépôt qu'il a nommé, seul le chemin par lequel il
// l'a nommé diffère.
func verifieAppartenance(ctx context.Context, g Git, chemin, cheminRepo string) error {
	racine, commun, err := identifie(ctx, g, chemin)
	if err != nil {
		return fmt.Errorf("%s existe déjà mais n'est pas un worktree git exploitable : %w", chemin, err)
	}
	if !memeChemin(racine, chemin) {
		return fmt.Errorf(
			"%s existe déjà mais n'est pas la racine d'un worktree git — git répond pour %s ; "+
				"choisis un autre nom de worktree ou retire ce dossier", chemin, racine)
	}
	communRepo, err := communDe(ctx, g, cheminRepo)
	if err != nil {
		return fmt.Errorf("identification du dépôt %s : %w", cheminRepo, err)
	}
	if !memeChemin(commun, communRepo) {
		return fmt.Errorf(
			"le worktree %s appartient au dépôt %s, pas à %s — deux nests visent probablement le "+
				"même worktree_root avec des repos de même nom ; choisis un autre nom de worktree "+
				"ou un worktree_root distinct", chemin, dossierDuDepot(commun), cheminRepo)
	}
	return nil
}

// identifie rend la racine du worktree contenant chemin et le dossier git commun
// du dépôt auquel il appartient.
func identifie(ctx context.Context, g Git, chemin string) (racine, commun string, err error) {
	out, err := g.Run(ctx, chemin, "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-common-dir")
	if err != nil {
		return "", "", err
	}
	lignes := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lignes) < 2 {
		return "", "", fmt.Errorf("réponse inattendue de git rev-parse : %q", string(out))
	}
	return strings.TrimSpace(lignes[0]), strings.TrimSpace(lignes[1]), nil
}

// DossierGitCommun rend le dossier git commun d'un dépôt — son `.git`, celui
// qui porte l'admin dir, les objets et les refs.
//
// C'est ce que `den <nest> -w` monte dans la microVM À CÔTÉ du worktree : le
// `.git` d'un worktree lié n'est qu'un fichier « gitdir: <repo>/.git/worktrees/
// <nom> », et sans ce dossier-là monté, toute commande git de la VM répond
// « fatal: not a git repository » (mesuré, smoke du 2026-07-29).
//
// La question passe par git et non par un `filepath.Join(repo, ".git")` parce
// que le repo d'un nest peut lui-même être un worktree lié : le join rendrait
// alors le fichier de renvoi, qui ne porte ni objets ni refs.
func DossierGitCommun(ctx context.Context, g Git, cheminRepo string) (string, error) {
	commun, err := communDe(ctx, g, cheminRepo)
	if err != nil {
		return "", fmt.Errorf("identification du dossier git de %s : %w", cheminRepo, err)
	}
	return commun, nil
}

func communDe(ctx context.Context, g Git, dir string) (string, error) {
	out, err := g.Run(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// dossierDuDepot remonte du dossier git commun (<repo>/.git) au dépôt lui-même,
// pour nommer dans les messages ce que l'utilisateur reconnaît.
func dossierDuDepot(commun string) string {
	if filepath.Base(commun) == ".git" {
		return filepath.Dir(commun)
	}
	return commun
}

// memeChemin compare deux chemins en résolvant les liens symboliques quand
// c'est possible : git rend le chemin résolu là où den manipule celui qu'on lui
// a donné.
func memeChemin(a, b string) bool {
	return resout(a) == resout(b)
}

// resout rend la forme canonique d'un chemin, y compris quand il N'EXISTE PLUS.
//
// C'est le cas déterminant, pas un cas limite : les gardes du cul-de-sac
// (enregistrement orphelin, worktree verrouillé) ne s'exécutent QUE lorsque le
// dossier a disparu. EvalSymlinks échoue alors des deux côtés de la comparaison,
// qui retombait sur deux chaînes brutes — celle que den manipule, passée par un
// lien, et celle que git a enregistrée, résolue. La résolution était donc
// structurellement absente là où on en avait besoin, et toute la mitigation
// restait Linux-seulement : sur macOS, $TMPDIR et worktree_root vivent sous
// /var → private/var.
//
// On résout donc le plus long ancêtre qui existe encore, et on lui rattache le
// reste du chemin.
func resout(chemin string) string {
	chemin = filepath.Clean(chemin)
	if reel, err := filepath.EvalSymlinks(chemin); err == nil {
		return filepath.Clean(reel)
	}
	reste := ""
	for courant := chemin; ; {
		parent := filepath.Dir(courant)
		if parent == courant {
			return chemin // remonté jusqu'à la racine sans rien pouvoir résoudre
		}
		reste = filepath.Join(filepath.Base(courant), reste)
		if reel, err := filepath.EvalSymlinks(parent); err == nil {
			return filepath.Clean(filepath.Join(reel, reste))
		}
		courant = parent
	}
}

// estEnregistre dit si git connaît encore un worktree à ce chemin, que son
// dossier existe ou non.
//
// L'erreur remonte, comme dans estVerrouille. Elle était avalée : sous un git
// défaillant sur `worktree list`, Retire rendait alors nil sur un dossier
// disparu — il prétendait avoir nettoyé sans avoir rien pu vérifier, ce que la
// revérification voisine existe précisément pour empêcher. Deux gardes du même
// paragraphe ne peuvent pas avoir deux disciplines opposées.
func estEnregistre(ctx context.Context, g Git, cheminRepo, chemin string) (bool, error) {
	out, err := g.Run(ctx, cheminRepo, "worktree", "list", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("inventaire des worktrees de %s : %w", cheminRepo, err)
	}
	for _, ligne := range strings.Split(string(out), "\n") {
		enregistre, ok := strings.CutPrefix(strings.TrimSpace(ligne), "worktree ")
		if ok && memeChemin(enregistre, chemin) {
			return true, nil
		}
	}
	return false, nil
}

// estVerrouille dit si git a verrouillé ce worktree.
//
// `worktree list --porcelain` émet un bloc par worktree, terminé par une ligne
// vide ; « locked » (seul, ou suivi de la raison) y appartient au bloc ouvert
// par la ligne « worktree <chemin> » qui le précède.
//
// En cas de panne de git, l'erreur remonte et Retire refuse : ne pas savoir si
// le worktree est verrouillé, c'est ne pas savoir si `prune` va faire son
// travail — et le seul sens sûr avant un déplacement est l'abstention.
func estVerrouille(ctx context.Context, g Git, cheminRepo, chemin string) (bool, error) {
	out, err := g.Run(ctx, cheminRepo, "worktree", "list", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("inventaire des worktrees de %s : %w", cheminRepo, err)
	}
	dansLeBloc := false
	for _, ligne := range strings.Split(string(out), "\n") {
		ligne = strings.TrimSpace(ligne)
		if enregistre, ok := strings.CutPrefix(ligne, "worktree "); ok {
			dansLeBloc = memeChemin(enregistre, chemin)
			continue
		}
		if dansLeBloc && (ligne == "locked" || strings.HasPrefix(ligne, "locked ")) {
			return true, nil
		}
	}
	return false, nil
}

// ligneExclusionDen est ce qu'on ajoute à .git/info/exclude en layout per-repo.
const ligneExclusionDen = ".den/"

// excludeDossierDen empêche le worktree per-repo de salir durablement le dépôt
// de l'utilisateur. Idempotent : la ligne n'est ajoutée qu'une fois, et le
// contenu existant du fichier est préservé.
func excludeDossierDen(ctx context.Context, g Git, cheminRepo string) error {
	commun, err := communDe(ctx, g, cheminRepo)
	if err != nil {
		return fmt.Errorf("identification du dépôt %s : %w", cheminRepo, err)
	}
	fichier := filepath.Join(commun, "info", "exclude")

	contenu, err := os.ReadFile(fichier)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("lecture de %s : %w", fichier, err)
	}
	for _, ligne := range strings.Split(string(contenu), "\n") {
		if strings.TrimSpace(ligne) == ligneExclusionDen {
			return nil // déjà exclu
		}
	}

	if err := os.MkdirAll(filepath.Dir(fichier), 0o755); err != nil {
		return fmt.Errorf("création de %s : %w", filepath.Dir(fichier), err)
	}
	ajout := ligneExclusionDen + "\n"
	if len(contenu) > 0 && !bytes.HasSuffix(contenu, []byte("\n")) {
		ajout = "\n" + ajout
	}
	f, err := os.OpenFile(fichier, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("ouverture de %s : %w", fichier, err)
	}
	defer f.Close()
	if _, err := f.WriteString(ajout); err != nil {
		return fmt.Errorf("écriture de %s : %w", fichier, err)
	}
	return nil
}

func brancheCourante(ctx context.Context, g Git, dir string) (string, error) {
	out, err := g.Run(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func brancheExiste(ctx context.Context, g Git, cheminRepo, branche string) bool {
	_, err := g.Run(ctx, cheminRepo, "show-ref", "--verify", "--quiet", "refs/heads/"+branche)
	return err == nil
}

// headResolvable dit si HEAD du dépôt désigne un commit existant.
//
// Deux configurations distinctes rendent false, et le message d'erreur qui
// s'appuie dessus doit les couvrir TOUTES LES DEUX (mesuré) :
//   - un `git init` vierge : HEAD pointe sur une branche jamais commitée ;
//   - une branche ORPHELINE (`git checkout --orphan`) dans un dépôt qui a par
//     ailleurs des commits — le dépôt n'est pas vide, c'est HEAD qui ne se
//     résout pas.
//
// Nommée d'après ce qu'elle mesure — la résolution de HEAD — et non
// « aAuMoinsUnCommit » comme dans une première version : ce nom-là affirmait
// sur le DÉPÔT une propriété que la sonde ne teste pas, et c'est exactement
// l'erreur qu'il a induite dans le message rendu à l'utilisateur.
//
// --quiet pour que l'échec attendu ne pollue pas stderr, --verify pour que git
// sorte non-zéro au lieu de rendre la chaîne telle quelle.
func headResolvable(ctx context.Context, g Git, cheminRepo string) bool {
	_, err := g.Run(ctx, cheminRepo, "rev-parse", "--verify", "--quiet", "HEAD")
	return err == nil
}

// brancheParDefaut rend la ref de suivi de la branche par défaut du dépôt
// (« origin/main »), et false si le dépôt n'a pas d'origin/HEAD.
func brancheParDefaut(ctx context.Context, g Git, cheminRepo string) (string, bool) {
	out, err := g.Run(ctx, cheminRepo, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		return "", false
	}
	ref := strings.TrimSpace(string(out))
	return ref, ref != ""
}

// fichiersSales rend les chemins qui rendent le worktree sale.
//
// --ignored=traditional est indispensable : un .env ou une base sqlite locale
// sont ignorés par git, donc invisibles pour `status --porcelain` ET pour le
// filet de `git worktree remove` — ce sont pourtant exactement les fichiers
// qu'on ne commite pas ET qu'on ne retrouve pas.
//
// Parmi les entrées ignorées, on écarte les DOSSIERS RÉELLEMENT IGNORÉS :
// git réduit un dossier entièrement ignoré à une seule entrée (node_modules/,
// target/), qui est du cache régénérable ; refuser dessus rendrait `den rm`
// inutilisable sur tout projet JS ou Python. Le suffixe « / » ne suffit PAS à
// les reconnaître — git collapse de la même façon un dossier que l'utilisateur
// n'a jamais ignoré mais dont tout le contenu l'est (`.gitignore` = `*.env`,
// la façon la plus répandue d'ignorer des secrets, et `conf/prod.env` sort en
// `!! conf/`). D'où la question posée à git pour chaque entrée : le dossier
// LUI-MÊME est-il ignoré ?
//
// -z plutôt que `-c core.quotePath=false` : par défaut git cite et échappe les
// chemins « spéciaux », si bien qu'un cache nommé `données/` ou `mon cache/`
// ressort en `"donn\303\251es/"` — sans le « / » final, donc jugé sale, et
// affiché à l'utilisateur en octal. quotePath=false ne lève que l'échappement
// non-ASCII : les espaces, guillemets et retours à la ligne restent cités. La
// sortie NUL-séparée, elle, n'est jamais citée — le problème est fermé à la
// source plutôt que contourné.
//
// ANGLE MORT ASSUMÉ (S3) : un secret placé dans un dossier lui-même ignoré en
// bloc (`config/` ignoré, `config/.env` dedans) reste invisible, git ne
// l'énumérant pas séparément. Un `core.excludesFile` hostile — ou seulement
// malformé — étend le trou à volonté, puisqu'il décide de ce que « réellement
// ignoré » veut dire.
//
// C'est un arbitrage explicite, pas un oubli : le fermer voudrait dire refuser
// sur tout `node_modules/`, c'est-à-dire rendre `den rm` inutilisable. Le prix
// est payé une fois et pour de bon depuis que Retire déplace au lieu de
// supprimer : ce que ce verdict rate finit dans la corbeille, pas au néant.
func fichiersSales(ctx context.Context, g Git, dir string) ([]string, error) {
	// --untracked-files=normal est passé EXPLICITEMENT : den lit git sous la
	// config de l'utilisateur, et `status.showUntrackedFiles = no` — réglage de
	// performance répandu sur les gros dépôts — vide sinon la sortie de tout
	// travail non suivi, désarmant la garde entière du spec §14.
	//
	// core.fsmonitor est neutralisé pour la même raison, en pire : il délègue à un
	// démon la question « qu'est-ce qui a changé ». Un Watchman qui a perdu son
	// état, un démon redémarré ou un montage où inotify rate des événements
	// répondent « rien », et git les croit — le fichier modifié devient invisible
	// sans qu'aucun drapeau d'index ne le trahisse. Pire, l'appel de den amorçait
	// lui-même ce cache et aveuglait ensuite le filet de `git worktree remove`.
	out, err := g.Run(ctx, dir, "-c", "core.fsmonitor=", "status", "--porcelain",
		"--ignored=traditional", "--untracked-files=normal", "-z")
	if err != nil {
		return nil, fmt.Errorf("état de %s : %w", dir, err)
	}
	// Deux passes : on lit d'abord tous les enregistrements, puis on pose à git
	// UNE seule question pour tous les dossiers candidats. Décider entrée par
	// entrée coûtait un fork par entrée (voir dossiersIgnores).
	type enregistrement struct{ etat, chemin string }
	var lus []enregistrement
	enregistrements := strings.Split(string(out), "\x00")
	for i := 0; i < len(enregistrements); i++ {
		e := enregistrements[i]
		// La sortie -z se termine par un NUL : le dernier morceau du découpage
		// est vide, et c'est le seul enregistrement vide légitime.
		if e == "" {
			continue
		}
		// SAUTER un enregistrement illisible, c'est perdre une entrée sale sans
		// le dire — la direction fail-open exacte que tout le reste du module
		// refuse. Un format que den ne sait pas lire est un doute, donc un refus.
		if len(e) < 4 || e[2] != ' ' {
			return nil, fmt.Errorf(
				"état de %s : enregistrement illisible dans la sortie de git status : %q", dir, e)
		}
		etat, chemin := e[:2], e[3:]
		// En -z, un renommage ou une copie occupe DEUX enregistrements : le
		// second porte le chemin source, sans préfixe d'état. On le consomme
		// pour ne pas le relire comme une entrée à part entière.
		//
		// La détection a lieu dans l'INDEX (colonne X, ce que produit `git mv`)
		// OU dans l'ARBRE DE TRAVAIL (colonne Y : « ␣R », « DR », « ␣C », que
		// produit un `mv` suivi d'un `git add -N`). Ne regarder que X faisait
		// relire « ab cd.txt » comme un enregistrement d'état « ab » et de chemin
		// « cd.txt » : den nommait à l'utilisateur un fichier qui n'existe pas.
		// La direction restait sûre — une entrée de plus, jamais de moins ; c'est
		// le message qui mentait.
		if etat[0] == 'R' || etat[0] == 'C' || etat[1] == 'R' || etat[1] == 'C' {
			i++
		}
		lus = append(lus, enregistrement{etat, chemin})
	}

	var candidats []string
	for _, e := range lus {
		if estDossierIgnorable(e.etat, e.chemin) {
			candidats = append(candidats, strings.TrimSuffix(e.chemin, "/"))
		}
	}
	ignores := map[string]bool{}
	if len(candidats) > 0 {
		ignores = dossiersIgnores(ctx, g, dir, candidats)
	}

	var sales []string
	for _, e := range lus {
		if estDossierIgnorable(e.etat, e.chemin) && ignores[strings.TrimSuffix(e.chemin, "/")] {
			continue
		}
		sales = append(sales, e.chemin)
	}

	marques, err := fichiersMarques(ctx, g, dir)
	if err != nil {
		return nil, err
	}
	for _, m := range marques {
		if !slices.Contains(sales, m) {
			sales = append(sales, m)
		}
	}
	return sales, nil
}

// estDossierIgnorable dit si l'enregistrement est un dossier ignoré collapsé,
// donc un candidat à la question posée par dossiersIgnores.
func estDossierIgnorable(etat, chemin string) bool {
	return etat == "!!" && strings.HasSuffix(chemin, "/")
}

// dossiersIgnores rend, parmi les dossiers donnés, ceux qui sont couverts
// EUX-MÊMES par une règle d'ignorance — par opposition à un dossier simplement
// non suivi dont le contenu se trouve ignoré.
//
// Le « / » final est retiré par l'appelant, et ce n'est pas cosmétique : dans le
// wildmatch de git, `*` et `**` matchent la chaîne vide, et git applique le
// `.gitignore` du dossier lui-même au composant vide qui suit le « / ».
// Demander `conf/` répond donc « ignoré » dès que le .gitignore porte `conf/*`,
// `conf/**`, ou un `.gitignore` imbriqué valant `*` — trois idiomes courants —
// alors que l'utilisateur n'a jamais rendu `conf/` jetable.
//
// UN SEUL appel pour tout le lot. Un fork par entrée coûtait 183 ms pour
// 300 dossiers et 545 ms pour 1 000, croissance linéaire sans borne — le motif
// « un fork par entrée » qu'un correctif précédent venait de retirer ailleurs.
// `-z` est imposé par la citation (sans lui, `données/` ressort en octal et den
// ne le reconnaît plus), et `--stdin` est imposé par `-z`, que git refuse sur un
// argv (« fatal: -z only makes sense with --stdin », mesuré).
//
// En cas de doute, la table est vide et toutes les entrées restent sales : la
// mise à la corbeille est refusée. Le rc=1 de « aucun ne matche » tombe dans la
// même branche, et rend la même réponse — aucune des deux ne désigne un dossier
// jetable. C'est déjà le sens que la sonde par entrée appliquait.
func dossiersIgnores(ctx context.Context, g Git, dir string, chemins []string) map[string]bool {
	var entree strings.Builder
	for _, c := range chemins {
		entree.WriteString(c)
		entree.WriteByte(0)
	}
	ignores := map[string]bool{}
	out, err := g.RunAvecEntree(ctx, dir, []byte(entree.String()), "check-ignore", "-z", "--stdin")
	if err != nil {
		return ignores
	}
	for _, c := range strings.Split(string(out), "\x00") {
		if c != "" {
			ignores[c] = true
		}
	}
	return ignores
}

// fichiersMarques rend les fichiers suivis que l'index marque « ne regarde
// pas » : skip-worktree (drapeau `S`) et assume-unchanged (drapeau en
// minuscule). git ne rapporte alors AUCUNE modification les concernant — ni dans
// `status`, ni dans le filet de `git worktree remove`, mesuré : les deux
// laissent détruire le fichier sans un mot.
//
// Or ces bits existent précisément pour porter des modifications locales qu'on
// ne veut pas commiter. Mais leur seule présence ne suffit pas à conclure :
// `core.ignoreStat` les pose sur TOUT le dépôt, et le sparse-checkout sur tout
// ce qui est hors du cône. On ne retient donc que les fichiers dont le contenu
// diffère réellement de l'index — voir cheminsModifies.
func fichiersMarques(ctx context.Context, g Git, dir string) ([]string, error) {
	out, err := g.Run(ctx, dir, "ls-files", "-v", "-z")
	if err != nil {
		return nil, fmt.Errorf("drapeaux d'index de %s : %w", dir, err)
	}
	var sales, reguliers, liens []string
	for _, e := range strings.Split(string(out), "\x00") {
		if e == "" {
			continue // dernier morceau du découpage : la sortie -z finit par un NUL
		}
		if len(e) < 3 || e[1] != ' ' {
			return nil, fmt.Errorf(
				"drapeaux d'index de %s : enregistrement illisible dans la sortie de git ls-files : %q", dir, e)
		}
		drapeau, chemin := e[0], e[2:]
		if drapeau != 'S' && (drapeau < 'a' || drapeau > 'z') {
			continue
		}
		// Lstat et non Stat : Stat suit les liens, et un fichier suivi remplacé
		// par un lien symbolique cassé s'évanouissait ainsi du discriminant.
		infos, err := os.Lstat(filepath.Join(dir, chemin))
		if err != nil {
			// Absent du disque : hors du cône d'un sparse-checkout, qui pose les
			// mêmes bits. Il n'y a rien là à protéger.
			continue
		}
		switch {
		case infos.Mode()&os.ModeSymlink != 0:
			liens = append(liens, chemin)
		case infos.Mode().IsRegular():
			reguliers = append(reguliers, chemin)
		default:
			// Fifo, dossier, socket, périphérique : `git hash-object` ne doit
			// JAMAIS les voir. Sur un fifo il se bloque sans borne (mesuré : il ne
			// rend jamais la main, il n'échoue pas), sur un dossier il sort en
			// `fatal:` anglais. Le verdict reste donc celui de den.
			//
			// Un SOUS-MODULE tombe ici : son gitlink est un dossier sur le disque.
			// Marqué, il est donc refusé alors que `git status` ne rapporte rien
			// (mesuré sur git 2.53 : `update-index --skip-worktree sm` puis
			// `status --porcelain` vide, et den rend « sm »). C'est un faux
			// positif assumé, pas corrigé, et deux mesures encadrent le coût :
			//   - `git worktree remove --force` NE refuse PAS un worktree à
			//     sous-module sur git 2.53 (rc=0, vérifié) : contrairement à ce
			//     qu'on a longtemps cru, il n'y avait pas là de filet équivalent —
			//     et den ne l'appelle de toute façon plus ;
			//   - le refus se lève par --force, qui déplace au lieu de détruire.
			//     Le sous-module déplacé garde tous ses fichiers ; son `.git`
			//     porte en revanche un chemin RELATIF (mesuré :
			//     « gitdir: ../../parent/.git/worktrees/wt/modules/sm »), donc il
			//     n'est plus un dépôt exploitable depuis la corbeille. On récupère
			//     des fichiers, ce qui est exactement le contrat de la corbeille.
			sales = append(sales, chemin)
		}
	}
	if len(reguliers)+len(liens) == 0 {
		return sales, nil
	}

	index, err := empreintesIndex(ctx, g, dir)
	if err != nil {
		return nil, err
	}
	modifies, err := cheminsModifies(ctx, g, dir, index, reguliers)
	if err != nil {
		return nil, err
	}
	sales = append(sales, modifies...)
	modifies, err = liensModifies(ctx, g, dir, index, liens)
	if err != nil {
		return nil, err
	}
	return append(sales, modifies...), nil
}

// entreeIndex est ce que l'index retient d'un chemin.
type entreeIndex struct {
	mode      string
	empreinte string
}

// modeLien est le mode git d'un lien symbolique.
const modeLien = "120000"

// modeRegulier dit si le mode d'index est celui d'un fichier régulier.
//
// C'est le pendant du contrôle que liensModifies fait sur modeLien, et son
// absence était un FAUX NÉGATIF, pas un faux positif : un lien suivi (120000)
// remplacé sur le disque par un fichier régulier dont le contenu vaut le texte
// du lien produit deux empreintes IDENTIQUES — git hache le texte du lien comme
// contenu du blob. Le fichier passait donc pour propre et était détruit.
//
// 100644 et 100755 sont volontairement confondus : un `chmod +x` seul sur un
// fichier marqué reste invisible à den. C'est une perte de bit de permission,
// pas de contenu, et la distinguer coûterait un stat par fichier pour rendre
// « sale » un worktree dont pas un octet n'a bougé. Le mode réel, lui, vit dans
// l'index et survit à la mise à la corbeille.
func modeRegulier(mode string) bool { return mode == "100644" || mode == "100755" }

// liensModifies compare un lien symbolique sur son TEXTE.
//
// git stocke le texte du lien comme contenu de l'objet (mode 120000), alors que
// `hash-object` SUIT le lien et hacherait le contenu de la cible : les deux ne
// coïncident jamais. Sans cette distinction, un dépôt parfaitement propre
// portant un seul lien suivi était refusé à perpétuité dès qu'un bit d'index le
// marquait — le symptôme exact que la comparaison d'empreintes devait fermer.
func liensModifies(ctx context.Context, g Git, dir string, index map[string]entreeIndex, liens []string) ([]string, error) {
	var modifies []string
	for _, chemin := range liens {
		complet := filepath.Join(dir, chemin)
		entree, ok := index[chemin]
		if !ok || entree.mode != modeLien {
			// L'index n'attend pas un lien ici : un fichier suivi a été remplacé
			// par un lien, ce qui est bien une modification.
			modifies = append(modifies, chemin)
			continue
		}
		// Readlink vient après un Lstat réussi sur le MÊME chemin : seul un
		// remplacement concurrent peut le faire échouer, et cette fenêtre n'est
		// pas reproductible en test. L'erreur remonte quand même — ne pas avoir
		// pu lire le lien n'autorise pas à le déclarer propre.
		cible, err := os.Readlink(complet)
		if err != nil {
			return nil, fmt.Errorf("lecture du lien %s : %w", complet, err)
		}
		attendu, err := empreinteBlob(cible, len(entree.empreinte))
		if err != nil {
			return nil, fmt.Errorf("empreinte du lien %s : %w", complet, err)
		}
		if attendu != entree.empreinte {
			modifies = append(modifies, chemin)
		}
	}
	return modifies, nil
}

// empreinteBlob calcule l'empreinte que git donne au blob dont le contenu est
// exactement ce texte : hash("blob <n>\x00<texte>").
//
// Elle sert au TEXTE D'UN LIEN, que git stocke tel quel comme contenu de
// l'objet — aucun filtre de .gitattributes ne s'applique à un lien symbolique,
// et c'est ce qui rend le calcul local légitime là où il ne le serait pas pour
// un fichier régulier (voir cheminsModifies).
//
// Ce calcul remplace un fork `cat-file blob` PAR LIEN MARQUÉ, mesuré à 3,83 s
// pour 8 000 liens. Il supprime en prime la dépendance à l'object store : un
// lien dont le blob manque — clone partiel, objet promis non rapatrié — faisait
// échouer den à perpétuité.
//
// L'algorithme se déduit de la LONGUEUR de l'empreinte que porte l'index (40
// caractères hexadécimaux pour SHA-1, 64 pour SHA-256), ce qui évite un
// `git rev-parse --show-object-format` — indisponible avant git 2.29 — et toute
// hypothèse sur le format d'objets du dépôt. Une longueur inconnue est une
// erreur, pas un pari : comparer contre le mauvais algorithme rendrait
// « modifié » pour toujours.
func empreinteBlob(texte string, longueurEmpreinte int) (string, error) {
	var h hash.Hash
	switch longueurEmpreinte {
	case 2 * sha1.Size:
		// SHA-1 est ici le format d'objets de git, pas un choix cryptographique.
		h = sha1.New()
	case 2 * sha256.Size:
		h = sha256.New()
	default:
		return "", fmt.Errorf(
			"format d'objets git inconnu : l'index porte une empreinte de %d caractères hexadécimaux",
			longueurEmpreinte)
	}
	fmt.Fprintf(h, "blob %d\x00%s", len(texte), texte)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// tailleLot borne la longueur de l'argv des sondes d'empreinte.
const tailleLot = 256

// cheminsModifies rend, parmi les fichiers que l'index déclare « ne regarde
// pas », ceux dont le contenu du disque diffère RÉELLEMENT de l'index.
//
// Sans cette comparaison la garde était inutilisable : `core.ignoreStat = true`
// pose le bit assume-unchanged sur tout le dépôt, et ces bits SURVIVENT au
// retrait du réglage — un worktree parfaitement propre devenait donc
// définitivement non supprimable sans --force. Lire `core.ignoreStat` n'y
// suffirait pas, puisque le blocage persiste précisément quand le réglage a
// disparu.
//
// Aucune commande git ne réexamine ces entrées : `diff-files`, `diff-index` et
// `ls-files -m` sont tous aveugles dessus (mesuré). Il faut donc comparer les
// empreintes soi-même. `hash-object` applique les filtres de `.gitattributes`,
// si bien qu'un filtre `clean` ou `core.autocrlf` ne fabrique pas de fausse
// modification (mesuré). Deux contreparties, toutes deux assumées :
//
//   - un filtre `clean` qui MASQUE un secret rend les deux empreintes
//     identiques : le fichier passe pour propre, et il part. C'est exactement
//     le cas que la corbeille rend réversible — il part dans un dossier, pas
//     dans le néant (voir Retire) ;
//   - un filtre `clean` NON DÉTERMINISTE (qui injecte un horodatage, un
//     compteur, un aléa) rend une empreinte différente à chaque appel : le
//     fichier est alors sale à perpétuité et `den rm` exige `--force` pour
//     toujours. Faux positif, donc direction sûre, et git se comporte
//     identiquement hors marquage — un tel dépôt n'a jamais un `git status`
//     vide.
//
// En cas de panne d'une sonde, l'erreur remonte : une sonde muette ne doit
// jamais autoriser une destruction.
func cheminsModifies(ctx context.Context, g Git, dir string, index map[string]entreeIndex, chemins []string) ([]string, error) {
	var modifies []string
	for lot := range slices.Chunk(chemins, tailleLot) {
		disque, err := empreintesDisque(ctx, g, dir, lot)
		if err != nil {
			return nil, err
		}
		if len(disque) != len(lot) {
			return nil, fmt.Errorf("empreintes de %s : %d valeurs rendues pour %d fichiers",
				dir, len(disque), len(lot))
		}
		for i, chemin := range lot {
			// Pas de branche pour « absent de l'index » : la valeur zéro rendue
			// par la table porte un mode vide, que modeRegulier refuse. Un chemin
			// inconnu de l'index compte donc pour modifié par le même contrôle —
			// une condition de moins, et la même direction.
			attendu := index[chemin]
			if !modeRegulier(attendu.mode) || attendu.empreinte != disque[i] {
				modifies = append(modifies, chemin)
			}
		}
	}
	return modifies, nil
}

// empreintesIndex lit TOUT l'index en un seul appel.
//
// Interroger `ls-files -s` avec les chemins d'un lot rebalayait l'index entier à
// chaque lot : coût par appel linéaire en taille d'index, nombre d'appels
// linéaire en fichiers marqués — donc quadratique (mesuré : 1 915 ms de
// pathspecs sur 20 000 fichiers marqués, contre 6 ms pour cet appel unique).
//
// L'appel unique supprime en prime un faux positif : `ls-files` traite ses
// arguments comme des PATHSPECS et non comme des chemins littéraux, si bien
// qu'un fichier suivi nommé « :x.txt » manquait à la table et passait pour
// modifié sur un worktree propre.
func empreintesIndex(ctx context.Context, g Git, dir string) (map[string]entreeIndex, error) {
	out, err := g.Run(ctx, dir, "ls-files", "-s", "-z")
	if err != nil {
		return nil, fmt.Errorf("empreintes d'index de %s : %w", dir, err)
	}
	index := map[string]entreeIndex{}
	for _, e := range strings.Split(string(out), "\x00") {
		if e == "" {
			continue // dernier morceau du découpage : la sortie -z finit par un NUL
		}
		// « <mode> <empreinte> <étape>\t<chemin> »
		entete, chemin, ok := strings.Cut(e, "\t")
		champs := strings.Fields(entete)
		if !ok || len(champs) < 2 {
			return nil, fmt.Errorf(
				"empreintes d'index de %s : enregistrement illisible dans la sortie de git ls-files : %q", dir, e)
		}
		index[chemin] = entreeIndex{mode: champs[0], empreinte: champs[1]}
	}
	return index, nil
}

// empreintesDisque calcule l'empreinte du contenu réellement présent, dans
// l'ordre des chemins demandés.
//
// `hash-object` LIT LE CONTENU INTÉGRAL de chaque fichier, et rien ne le borne :
// mesuré à 499 ms pour 4 fichiers de 64 Mio marqués, soit le débit du disque.
// C'est assumé, faute de meilleure question à poser :
//   - le calcul ne peut pas migrer en Go comme celui du texte des liens, parce
//     que `hash-object` applique les filtres de .gitattributes — sans quoi un
//     dépôt en autocrlf verrait toutes ses lignes changer (voir cheminsModifies) ;
//   - comparer d'abord les TAILLES ne conclurait rien : un filtre `clean` ou
//     `core.autocrlf` fait légitimement différer la taille du blob de celle du
//     disque, donc une taille différente ne prouve pas la modification, et une
//     taille égale ne prouve pas la propreté ;
//   - la mémoire, elle, reste bornée : git streame, et den ne reçoit que des
//     empreintes.
//
// L'ensemble concerné est celui des fichiers MARQUÉS, vide sur un dépôt
// ordinaire. Le cas coûteux est `core.ignoreStat = true`, qui marque tout le
// dépôt — 20 000 petits fichiers y coûtent 70 ms au total, mesuré.
func empreintesDisque(ctx context.Context, g Git, dir string, chemins []string) ([]string, error) {
	out, err := g.Run(ctx, dir, append([]string{"hash-object", "--"}, chemins...)...)
	if err != nil {
		return nil, fmt.Errorf("empreintes du disque de %s : %w", dir, err)
	}
	return strings.Fields(string(out)), nil
}

// listeCourte nomme les fichiers en cause sans noyer le message.
func listeCourte(chemins []string) string {
	const max = 5
	if len(chemins) <= max {
		return strings.Join(chemins, ", ")
	}
	return fmt.Sprintf("%s et %d autres", strings.Join(chemins[:max], ", "), len(chemins)-max)
}
