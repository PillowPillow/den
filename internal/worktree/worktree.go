// Package worktree propage un worktree git sur les repos d'un nest. C'est le
// seul module de den qui pilote git ; comme sbx, il le fait derrière une
// interface pour rester substituable.
package worktree

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// Git est l'accès à la CLI git, injecté pour rester substituable.
type Git interface {
	Run(ctx context.Context, dir string, args ...string) ([]byte, error)
}

type gitExec struct{}

// NewGit renvoie l'accès réel au git du PATH.
func NewGit() Git { return gitExec{} }

// variablesRedirigeantes sont les variables d'environnement qui désignent le
// dépôt cible et qui sont PRIORITAIRES sur le répertoire courant : tant qu'elles
// sont posées, cmd.Dir n'isole rien. den est fait pour tourner sous des agents
// et depuis des hooks git, où elles sont couramment exportées ; sans ce
// filtrage, fichiersSales déciderait d'une suppression d'après l'état d'un autre
// dépôt. On les RETIRE de l'environnement : les poser à vide ne les neutralise
// pas — git échoue alors sur un `not a git repository` pour chaque commande.
var variablesRedirigeantes = []string{
	"GIT_DIR",
	"GIT_COMMON_DIR",
	"GIT_WORK_TREE",
	"GIT_INDEX_FILE",
	"GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES",
	"GIT_NAMESPACE",
}

// environnementNeutre rend l'environnement courant privé des variables qui
// détourneraient git vers un autre dépôt que celui demandé.
func environnementNeutre() []string {
	brut := os.Environ()
	propre := make([]string, 0, len(brut))
	for _, v := range brut {
		nom, _, _ := strings.Cut(v, "=")
		if slices.Contains(variablesRedirigeantes, nom) {
			continue
		}
		propre = append(propre, v)
	}
	return propre
}

func (gitExec) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = environnementNeutre()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
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

// Assure garantit l'existence du worktree wt pour ce repo et renvoie son
// chemin. Idempotent : si le worktree existe déjà SUR LA BONNE BRANCHE et
// APPARTIENT BIEN À CE REPO, il est laissé tel quel.
//
// Un worktree existant sur une AUTRE branche est une erreur, jamais un checkout
// silencieux : basculer la branche d'un worktree où l'utilisateur travaille
// déplacerait son travail sans qu'il l'ait demandé.
func Assure(ctx context.Context, g Git, layout, root, wt, cheminRepo string) (string, error) {
	if err := verifieRepo(cheminRepo); err != nil {
		return "", err
	}

	chemin := Chemin(layout, root, wt, cheminRepo)

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
		if actuelle != wt {
			return "", fmt.Errorf(
				"le worktree %s est sur la branche %q, pas %q — choisis un autre nom de worktree "+
					"ou bascule ce dossier sur %q à la main", chemin, actuelle, wt, wt)
		}
		return chemin, nil // déjà en place : idempotent
	}

	// Dossier absent mais enregistrement toujours vivant : git refuserait avec
	// un « fatal: … missing but already registered worktree » anglais, et rien
	// dans den n'en sortirait. On nomme la sortie.
	if estEnregistre(ctx, g, cheminRepo, chemin) {
		return "", fmt.Errorf(
			"le worktree %s est encore enregistré dans %s alors que son dossier a disparu — "+
				"lance `den rm` sur ce nest pour effacer l'enregistrement, puis re-spawne",
			chemin, cheminRepo)
	}

	// `git worktree add <chemin> <branche>` si la branche existe déjà,
	// `-b <branche>` sinon : git refuse de recréer une branche existante.
	args := []string{"worktree", "add", chemin, wt}
	if !brancheExiste(ctx, g, cheminRepo, wt) {
		// --no-track : le point de départ est une ref de suivi, et sans lui git
		// ferait suivre origin/<défaut> à la branche de travail — `git push`
		// échouerait ensuite en proposant de pousser sur la branche par défaut.
		args = []string{"worktree", "add", "--no-track", "-b", wt, chemin}
		// Spec §13.4-3 : la branche part de la branche par défaut du repo. Repli
		// sur le HEAD courant si le dépôt n'a pas d'origin/HEAD — un dépôt
		// purement local est parfaitement légitime, et c'est le seul point de
		// départ qu'on puisse alors nommer.
		if depart, ok := brancheParDefaut(ctx, g, cheminRepo); ok {
			args = append(args, depart)
		}
	}
	if _, err := g.Run(ctx, cheminRepo, args...); err != nil {
		return "", fmt.Errorf("création du worktree %q de %s : %w", wt, cheminRepo, err)
	}
	return chemin, nil
}

// Retire supprime un worktree. Refuse si l'arbre est sale et que force est
// faux : perdre du travail non commité serait le pire effet de bord d'un
// `den rm` (spec §14).
func Retire(ctx context.Context, g Git, cheminRepo, cheminWorktree string, force bool) error {
	if _, err := os.Stat(cheminWorktree); os.IsNotExist(err) {
		// Le dossier est parti (rm -rf de l'utilisateur, `add` interrompu) mais
		// l'enregistrement git lui survit et bloquerait tout Assure ultérieur.
		// Rendre nil sans rien faire, c'était prétendre avoir nettoyé.
		if _, err := g.Run(ctx, cheminRepo, "worktree", "prune"); err != nil {
			return fmt.Errorf("nettoyage des enregistrements de worktree de %s : %w", cheminRepo, err)
		}
		// `prune` saute SILENCIEUSEMENT les worktrees verrouillés (rc=0, aucune
		// sortie) — et `git worktree lock` existe précisément pour les volumes
		// amovibles et les montages réseau, donc pour le cas où le dossier
		// disparaît légitimement. Sans cette revérification, den rendrait nil en
		// prétendant avoir nettoyé, et Assure renverrait ensuite l'utilisateur
		// vers `den rm` — la commande qui vient de lui dire qu'elle a réussi.
		if estEnregistre(ctx, g, cheminRepo, cheminWorktree) {
			return fmt.Errorf(
				"l'enregistrement du worktree %s survit dans %s alors que son dossier a disparu — "+
					"il est probablement verrouillé : lance `git worktree unlock %s` dans %s, puis relance",
				cheminWorktree, cheminRepo, cheminWorktree, cheminRepo)
		}
		return nil
	}

	if err := verifieAppartenance(ctx, g, cheminWorktree, cheminRepo); err != nil {
		return err
	}

	if !force {
		sales, err := fichiersSales(ctx, g, cheminWorktree)
		if err != nil {
			return err
		}
		if len(sales) > 0 {
			return fmt.Errorf(
				"le worktree %s contient des modifications non commitées (%s) — commite-les, ou "+
					"relance avec --force pour les perdre, ou avec --keep-worktrees pour garder le dossier",
				cheminWorktree, listeCourte(sales))
		}
	}

	args := []string{"worktree", "remove", cheminWorktree}
	if force {
		args = append(args, "--force")
	}
	if _, err := g.Run(ctx, cheminRepo, args...); err != nil {
		return fmt.Errorf("suppression du worktree %s : %w", cheminWorktree, err)
	}
	return nil
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
func estEnregistre(ctx context.Context, g Git, cheminRepo, chemin string) bool {
	out, err := g.Run(ctx, cheminRepo, "worktree", "list", "--porcelain")
	if err != nil {
		return false
	}
	for _, ligne := range strings.Split(string(out), "\n") {
		enregistre, ok := strings.CutPrefix(strings.TrimSpace(ligne), "worktree ")
		if ok && memeChemin(enregistre, chemin) {
			return true
		}
	}
	return false
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
// Angle mort restant : un secret placé dans un dossier lui-même ignoré en bloc
// (`config/` ignoré, `config/.env` dedans) reste invisible, git ne l'énumérant
// pas séparément.
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
	var sales []string
	enregistrements := strings.Split(string(out), "\x00")
	for i := 0; i < len(enregistrements); i++ {
		e := enregistrements[i]
		if len(e) < 4 || e[2] != ' ' {
			continue
		}
		etat, chemin := e[:2], e[3:]
		// En -z, un renommage ou une copie occupe DEUX enregistrements : le
		// second porte le chemin source, sans préfixe d'état. On le consomme
		// pour ne pas le relire comme une entrée à part entière.
		if etat[0] == 'R' || etat[0] == 'C' {
			i++
		}
		if etat == "!!" && strings.HasSuffix(chemin, "/") && dossierIgnore(ctx, g, dir, chemin) {
			continue
		}
		sales = append(sales, chemin)
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

// dossierIgnore dit si le dossier LUI-MÊME est couvert par une règle d'ignorance,
// par opposition à un dossier simplement non suivi dont le contenu se trouve
// ignoré.
//
// Le « / » final est retiré avant d'interroger git, et ce n'est pas cosmétique :
// dans le wildmatch de git, `*` et `**` matchent la chaîne vide, et git applique
// le `.gitignore` du dossier lui-même au composant vide qui suit le « / ».
// Demander `conf/` répond donc « ignoré » dès que le .gitignore porte `conf/*`,
// `conf/**`, ou un `.gitignore` imbriqué valant `*` — trois idiomes courants —
// alors que l'utilisateur n'a jamais rendu `conf/` jetable. Un fichier
// d'exclusion malformé suffisait de la même façon à tout rouvrir.
//
// En cas de doute (git en erreur), on répond faux : l'entrée reste sale et la
// suppression est refusée. C'est le seul sens sûr pour une opération destructrice.
func dossierIgnore(ctx context.Context, g Git, dir, chemin string) bool {
	_, err := g.Run(ctx, dir, "check-ignore", "-q", "--", strings.TrimSuffix(chemin, "/"))
	return err == nil
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
	var candidats []string
	for _, e := range strings.Split(string(out), "\x00") {
		if len(e) < 3 || e[1] != ' ' {
			continue
		}
		drapeau, chemin := e[0], e[2:]
		if drapeau != 'S' && (drapeau < 'a' || drapeau > 'z') {
			continue
		}
		// Lstat et non Stat : Stat suit les liens, et un fichier suivi remplacé
		// par un lien symbolique cassé s'évanouissait ainsi du discriminant. La
		// comparaison d'empreintes le rattrape ensuite — git hache le texte du
		// lien, qui ne coïncide pas avec le contenu attendu.
		if _, err := os.Lstat(filepath.Join(dir, chemin)); err != nil {
			// Absent du disque : hors du cône d'un sparse-checkout, qui pose les
			// mêmes bits. Il n'y a rien là à protéger.
			continue
		}
		candidats = append(candidats, chemin)
	}

	return cheminsModifies(ctx, g, dir, candidats)
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
// modification (mesuré) — en contrepartie, un filtre `clean` qui masque un
// secret rend les deux empreintes identiques, angle mort assumé et de toute
// façon partagé avec git.
//
// En cas de panne d'une sonde, l'erreur remonte : une sonde muette ne doit
// jamais autoriser une destruction.
func cheminsModifies(ctx context.Context, g Git, dir string, chemins []string) ([]string, error) {
	var modifies []string
	for lot := range slices.Chunk(chemins, tailleLot) {
		index, err := empreintesIndex(ctx, g, dir, lot)
		if err != nil {
			return nil, err
		}
		disque, err := empreintesDisque(ctx, g, dir, lot)
		if err != nil {
			return nil, err
		}
		if len(disque) != len(lot) {
			return nil, fmt.Errorf("empreintes de %s : %d valeurs rendues pour %d fichiers",
				dir, len(disque), len(lot))
		}
		for i, chemin := range lot {
			if attendu, ok := index[chemin]; !ok || attendu != disque[i] {
				modifies = append(modifies, chemin)
			}
		}
	}
	return modifies, nil
}

// empreintesIndex lit les empreintes que l'index associe à ces chemins.
func empreintesIndex(ctx context.Context, g Git, dir string, chemins []string) (map[string]string, error) {
	out, err := g.Run(ctx, dir, append([]string{"ls-files", "-s", "-z", "--"}, chemins...)...)
	if err != nil {
		return nil, fmt.Errorf("empreintes d'index de %s : %w", dir, err)
	}
	empreintes := make(map[string]string, len(chemins))
	for _, e := range strings.Split(string(out), "\x00") {
		// « <mode> <empreinte> <étape>\t<chemin> »
		entete, chemin, ok := strings.Cut(e, "\t")
		if !ok {
			continue
		}
		if champs := strings.Fields(entete); len(champs) >= 2 {
			empreintes[chemin] = champs[1]
		}
	}
	return empreintes, nil
}

// empreintesDisque calcule l'empreinte du contenu réellement présent, dans
// l'ordre des chemins demandés.
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
