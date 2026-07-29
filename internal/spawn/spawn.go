// Package spawn orchestre la séquence complète de `den <nest>` (spec §6).
//
// Il vit hors de internal/cli à dessein : c'est la logique la plus dense du
// projet, et elle doit être testable sans cobra ni tty.
package spawn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/PillowPillow/den/internal/agent"
	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
	"github.com/PillowPillow/den/internal/policy"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/worktree"
)

// Deps injecte les accès au monde, pour que la séquence entière soit testable
// sans microVM.
type Deps struct {
	Sbx    sbx.Runner
	Git    worktree.Git
	Policy policy.Options
	Sortie io.Writer
}

// Il n'y a PAS de DepsSysteme ici, et c'est délibéré. Une version antérieure en
// portait une (`sbx.NewExec("") + worktree.NewGit() + OptionsDefaut + os.Stdout`)
// dont la godoc affirmait qu'elle existait « pour que le câblage cobra puisse
// recevoir ses accès en paramètre ». C'était faux : `internal/cli/root.go`
// assemble spawn.Deps champ par champ, depuis les accès de `cli.Deps`, et ne
// l'appelait jamais — mesuré, `go build ./cmd/den` réussit après l'avoir
// renommée. Son seul consommateur était un helper de test.
//
// Elle était de plus le DERNIER endroit de l'arbre à construire un second
// `sbx.NewExec("")` — exactement le câblage que `root_deps_test.go` existe pour
// interdire (`cli.Deps` ne porte qu'un seul Sbx, partagé entre `den ls` et le
// spawn ; la godoc de ce fichier nomme `spawn.DepsSysteme()` comme LA forme du
// refactor à empêcher). Garder un constructeur tout prêt pour ce câblage-là,
// avec une godoc qui l'encourage, était une arme chargée.
//
// La porte d'injection, elle, reste entière : Deps est publique et tous ses
// champs le sont. C'est l'appelant qui dit ce qu'il branche.

// Options porte les flags de `den <nest>`.
type Options struct {
	Nest     string
	Worktree string
	Agent    string
	Without  []string
	Only     []string
	Detach   bool
}

// Spawn exécute la séquence du spec §6, dans l'ordre :
// résolution → sélection repos → worktrees → profil agent → mixin →
// sbx create (ou attache si la sandbox vit déjà) → settle-loop → attache.
//
// L'ordre n'est pas une commodité : le settle-loop PRÉCÈDE l'attache parce
// qu'attacher avant que la policy soit posée produit exactement le « ça marche
// à moitié » que le spec §7 interdit. Symétriquement, tout ce qui peut être
// refusé sur la seule foi de la configuration l'est AVANT le premier effet de
// bord — un worktree créé puis abandonné parce que le nom de sandbox était
// invalide laisserait l'utilisateur nettoyer à la main.
func Spawn(ctx context.Context, denHome string, o Options, d Deps) error {
	// Une Sortie oubliée ne doit pas paniquer au milieu de la séquence :
	// l'appelant fautif a déjà, au premier Fprintf, une sandbox créée et
	// démarrée derrière lui. Perdre le journal coûte moins cher que ça.
	if d.Sortie == nil {
		d.Sortie = io.Discard
	}

	// 1. Résolution de la cascade.
	g, err := config.LoadGlobal(denHome)
	if err != nil {
		return err
	}
	stacks, err := config.LoadStacks(denHome)
	if err != nil {
		return err
	}
	n, err := nest.LoadNest(denHome, o.Nest)
	if err != nil {
		return err
	}
	r, err := nest.Resolve(denHome, g, stacks, n, nest.Options{
		Agent: o.Agent, Without: o.Without, Only: o.Only,
	})
	if err != nil {
		return err
	}

	// Le nom se calcule AVANT tout effet de bord : un worktree non
	// sandboxable (« feature/123 ») doit être refusé sans avoir rien créé.
	nomSandbox, err := sbx.NomSandbox(o.Nest, o.Worktree)
	if err != nil {
		return err
	}

	// 2. Les repos doivent tous exister AVANT le moindre create (spec §11).
	for _, repo := range r.Repos {
		if _, err := os.Stat(repo.Path); err != nil {
			return fmt.Errorf(
				"nest %q : repo introuvable : %s — corrige `repos:` dans %s",
				o.Nest, repo.Path, filepath.Join(denHome, "nests", o.Nest+".yaml"))
		}
	}
	// Même famille de contrôle : un config_dir vide deviendrait un workspace
	// vide, et le message d'un MkdirAll("") ne désignerait rien du tout.
	if r.AgentConfigDir == "" {
		return fmt.Errorf(
			"agent %q : aucun config_dir — déclare `agents.%s.config_dir` dans %s "+
				"(ou `agents.%s` dans le nest) : c'est le profil monté RW dans la sandbox",
			r.AgentName, r.AgentName, filepath.Join(denHome, "config.yaml"), r.AgentName)
	}
	// Et ssh.dir, en mode mount : il part en workspace, donc VERBATIM dans
	// l'argv de `sbx create`. den ne passe jamais à sbx un chemin qu'il n'a pas
	// garanti présent — un dossier inexistant deviendrait un mount vide qui
	// masque au shell de la VM les clés que l'utilisateur croit y avoir montées.
	//
	// Le contrôle est ici, avec ceux des repos, et non à l'endroit où le
	// workspace est composé : à ce moment-là les worktrees existent déjà et le
	// profil de l'agent a été créé, donc un refus laisserait l'utilisateur
	// nettoyer à la main. Le profil, lui, est créé par den et non contrôlé —
	// c'est la différence entre « den le peuple » et « den le reçoit ».
	//
	// CE QUE CE PLACEMENT COÛTE, en toutes lettres : ce contrôle — comme celui
	// des kits juste en dessous — précède l'embranchement spawn-or-attach
	// (étape 6). Il s'applique donc AUSSI quand la sandbox est déjà vivante :
	// si `ssh.dir` ou un kit disparaît du disque, `den <nest>` ne peut plus se
	// RATTACHER à une VM qui tourne, alors que rien de ce chemin-là n'est
	// relu au moment d'attacher (la VM garde les mounts de son `create`).
	//
	// C'est assumé, pas ignoré : déplacer ces contrôles après `sbx.Ls` les
	// ferait dépendre d'un appel réseau et compliquerait une garde dont tout
	// l'intérêt est de tomber avant le moindre effet de bord. La porte de
	// sortie existe et ne passe par aucun de ces contrôles : `den sh <nom>`
	// n'appelle que spawn.Attache (internal/cli/sh.go:38) et ne lit ni la
	// config ni les kits.
	if r.SSHMode == "mount" {
		if r.SSHDir == "" {
			return fmt.Errorf(
				"ssh.mode vaut « mount » mais ssh.dir n'est pas déclaré dans %s",
				filepath.Join(denHome, "config.yaml"))
		}
		if _, err := os.Stat(r.SSHDir); err != nil {
			return fmt.Errorf(
				"ssh.dir : %s introuvable — corrige `ssh.dir` dans %s : en mode « mount » ce dossier "+
					"est monté dans la sandbox, et un chemin absent y monterait un dossier vide "+
					"à la place de tes clés",
				r.SSHDir, filepath.Join(denHome, "config.yaml"))
		}
	}
	// Même invariant, même endroit : les kits partent en `--kit` dans l'argv de
	// `sbx create`. `den doctor` les contrôlait déjà, mais ce diagnostic n'est
	// obtenu que par qui le lance : sans ce contrôle-ci, `den <nest>` rendait
	// rc=0 et laissait sbx échouer au boot de la microVM, où l'utilisateur voit
	// une VM qui meurt et non un message de den.
	for _, k := range r.Stack.KitsDeclares() {
		if _, err := os.Stat(k); err != nil {
			return fmt.Errorf(
				"stack %q : kit introuvable : %s — corrige `kit:` ou `kits:` dans %s",
				r.Stack.Name, k, filepath.Join(r.Stack.Dir, "stack.yaml"))
		}
	}

	// 3. Worktrees, si demandés. Le premier workspace doit rester le premier
	// repo : sbx.Sandbox.Workdir en dépend pour l'attache, et rien à SON niveau
	// ne peut vérifier que cette liste a bien été composée dans cet ordre.
	workspaces := make([]string, 0, len(r.Repos)+2)
	for _, repo := range r.Repos {
		chemin := repo.Path
		if o.Worktree != "" {
			chemin, err = worktree.Assure(ctx, d.Git, r.WorktreeLayout, r.WorktreeRoot, o.Worktree, repo.Path)
			if err != nil {
				return err
			}
			fmt.Fprintf(d.Sortie, "worktree %s : %s\n", repo.Name(), chemin)
		}
		workspaces = append(workspaces, chemin)
	}

	// 4. Profil agent : monté RW, il doit exister — sinon sbx crée un dossier
	// vide au mount et l'agent repart de zéro à chaque spawn.
	if err := os.MkdirAll(r.AgentConfigDir, 0o755); err != nil {
		return fmt.Errorf("création du profil de l'agent %s (%s) : %w", r.AgentName, r.AgentConfigDir, err)
	}
	workspaces = append(workspaces, r.AgentConfigDir)
	// Les TROIS modes SSH sont traités ici, y compris les deux qui n'ajoutent
	// rien — l'`if` sans `else` d'avant ne permettait pas de distinguer « rien à
	// faire, c'est voulu » de « cas oublié », et c'est cette ambiguïté qui a
	// produit le diagnostic « toute sandbox spawnée par défaut sort sans accès
	// SSH ». Ce diagnostic était faux, mais il était indécidable ici.
	//
	// Ce qui suit est mesuré, pas supposé :
	//
	//   - « mount » : ssh.dir devient un workspace. Contrôlé à l'étape 2, avant
	//     tout effet de bord ; il ne reste ici que sa place dans la liste, qui
	//     est significative (le premier workspace devient le -w de l'attache).
	//
	//   - « agent-forward » (le DÉFAUT, config.LoadGlobalSansValider) : rien à
	//     ajouter, ni ici ni ailleurs. Il repose entièrement sur le fait que le
	//     process `sbx create` hérite de l'environnement de den, SSH_AUTH_SOCK
	//     compris — cmd.Env est laissé nil dans internal/sbx/runner.go, et cet
	//     héritage est tenu par TestExec{Run,Attach}TransmetLEnvironnementDeDen.
	//     Le socket n'a sa place NI dans l'argv (aucun flag sbx attesté ne le
	//     prend) NI dans le mixin (une valeur de socket hôte écrite dans un kit
	//     serait périmée dès la session suivante). `den doctor` avertit quand la
	//     variable est absente, seul cas où ce mode ne donne rien.
	//     Ce qui reste NON vérifié : que sbx propage ce socket jusque dans la
	//     microVM. C'est une hypothèse du spec (A10), pas un acquis.
	//
	//   - « none » : rien à ajouter, par définition.
	//
	// Que les deux derniers produisent bien la MÊME liste, et mount exactement
	// un workspace de plus, est tenu par
	// TestSpawnNAjouteAucunWorkspaceHorsDuModeMount.
	if r.SSHMode == "mount" {
		workspaces = append(workspaces, r.SSHDir)
	}

	// 5. Mixin généré. r.DenHome et non denHome : Resolve garantit qu'il est
	// absolu, et ce chemin repart tel quel vers `sbx create --kit`, où le cwd
	// n'est plus garanti.
	m, err := agent.MixinDepuis(r, nomSandbox)
	if err != nil {
		return err
	}
	// Le mixin sur disque est la RÉFÉRENCE du `create` : ce que la VM a
	// réellement reçu, et la seule chose à quoi comparer la configuration
	// d'aujourd'hui pour détecter une dérive (étape 6). Il se lit ici, et il ne
	// se réécrit QUE sur la branche create, plus bas.
	//
	// Le réécrire à chaque passage — ce que faisait la version précédente —
	// détruisait la référence : la comparaison portait alors sur le mixin et
	// lui-même, ne détectait jamais rien, et laissait la suite parfaitement
	// verte. Cantonner l'écriture à la branche qui la justifie rend ce défaut
	// impossible plutôt que seulement testé.
	ancien, errAncien := agent.LisMixin(r.DenHome, nomSandbox)

	// 6. Spawn-or-attach : un nom déjà vivant n'est pas une erreur (spec §11).
	//
	// La Sandbox trouvée est GARDÉE, pas réduite à un booléen : elle seule porte
	// le statut réel et les workspaces que la VM monte vraiment.
	boxes, err := sbx.Ls(ctx, d.Sbx)
	if err != nil {
		return err
	}
	vivante := sbx.Trouve(boxes, nomSandbox)

	// Le workdir de l'attache : celui de la config sur la branche create (la VM
	// va monter exactement ces workspaces-là), celui de la VM sur l'autre.
	workdir := premier(workspaces)

	if vivante != nil {
		// Un nom pris par une VM qui ne tourne pas n'est pas un
		// spawn-or-attach. Même garde que `den sh` — et le même helper, pour
		// que la propriété ne puisse pas être vraie d'un côté et oubliée de
		// l'autre.
		if err := vivante.VerifieEnMarche(); err != nil {
			return err
		}

		// Le -w vient des workspaces que la VM MONTE (ceux de son `create`
		// d'origine), pas de ceux que la cascade recalcule maintenant. Si le
		// premier repo du nest a changé de chemin depuis, le chemin recalculé
		// n'existe pas dans la VM. Vide si la VM ne monte rien : Attache omet
		// alors le -w plutôt que d'inventer un chemin.
		workdir = vivante.Workdir()

		// Dérive de configuration. RIEN ne réapplique un mixin à une VM en
		// marche : elle garde la policy et l'env de son create. On AVERTIT sans
		// refuser (refuser casserait un `den <nest>` qui marchait hier pour un
		// YAML anodin) et sans recréer (destruction non demandée d'une VM qui
		// porte peut-être du travail en cours).
		signaleDerive(d.Sortie, nomSandbox, ancien, errAncien, m)

		fmt.Fprintf(d.Sortie, "sandbox %s déjà vivante : attache\n", nomSandbox)
	} else {
		// Le mixin n'est matérialisé QUE là : c'est le seul moment où il est
		// posé sur une VM, et donc le seul où le fichier peut prétendre décrire
		// ce que cette VM porte.
		dirMixin, err := agent.EcrisMixin(r.DenHome, nomSandbox, m)
		if err != nil {
			return err
		}
		argv, err := sbx.ArgvCreate(sbx.Create{
			Nom:        nomSandbox,
			Image:      r.Stack.Image,
			KitsStack:  r.Stack.KitsDeclares(),
			KitMixin:   dirMixin,
			Workspaces: workspaces,
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(d.Sortie, "création de la sandbox %s (image %s)…\n", nomSandbox, r.Stack.Image)
		// Recontextualisé : Exec.Run préfixe déjà son message de l'argv COMPLET
		// — tous les --kit et tous les workspaces sur une seule ligne — où
		// l'étape qui a échoué se perd.
		if _, err := d.Sbx.Run(ctx, argv...); err != nil {
			return fmt.Errorf("création de la sandbox %s : %w", nomSandbox, err)
		}
	}

	// 7. Settle-loop fail-closed AVANT toute attache — y compris sous --detach :
	// une sandbox rendue « prête » sans policy posée est le même demi-démarrage,
	// simplement constaté plus tard.
	if len(r.Egress) > 0 {
		fmt.Fprintf(d.Sortie, "attente de la policy réseau (%d hôte(s))…\n", len(r.Egress))
	}
	if err := policy.Settle(ctx, d.Sbx, nomSandbox, r.Egress, d.Policy); err != nil {
		return err
	}

	// 8. Attache.
	if o.Detach {
		fmt.Fprintf(d.Sortie, "sandbox %s prête (détachée) — `den sh %s` pour y entrer\n",
			nomSandbox, nomSandbox)
		return nil
	}
	return Attache(ctx, d.Sbx, nomSandbox, workdir)
}

// signaleDerive rend, sur la sortie de la séquence, ce qui a changé entre le
// mixin que la sandbox a reçu à son `create` et celui que la configuration
// produirait maintenant.
//
// Appelée UNIQUEMENT sur la branche « sandbox vivante » : sur un create, c'est
// le create qui pose le mixin, il ne peut pas avoir dérivé de lui-même — et le
// cache/ d'une sandbox détruite survit à celle-ci (spec §3 : cache/
// reconstructible, den ne le purge pas), donc une comparaison hissée hors de
// cette branche crierait à la dérive sur une sandbox parfaitement à jour.
//
// Une référence ABSENTE s'annonce, au même titre qu'une référence illisible.
// Une version antérieure se taisait dessus, au motif d'un « premier spawn » qui
// ne passe JAMAIS ici : un premier spawn prend la branche create. Les cas
// réellement atteignables sont un cache/ purgé, une sandbox créée à la main, ou
// une sandbox créée par un den antérieur — tous des « den ne sait pas », jamais
// des « rien n'a changé ». Le silence était donc fail-OPEN dans les seuls cas où
// il se produisait : mesuré, un `rm -rf ~/.den/cache` — que le spec §3 déclare
// sûr — désactivait DÉFINITIVEMENT la détection pour cette sandbox, la branche
// attache ne reposant jamais la référence.
func signaleDerive(sortie io.Writer, nomSandbox string, ancien agent.Mixin, errAncien error, nouveau agent.Mixin) {
	if errAncien != nil {
		// Le message distingue les deux causes — l'utilisateur agit
		// différemment sur un cache purgé et sur un fichier corrompu — mais
		// AUCUNE des deux n'est silencieuse.
		if errors.Is(errAncien, os.ErrNotExist) {
			fmt.Fprintf(sortie,
				"attention : aucune référence de configuration pour la sandbox %s — dérive non vérifiable "+
					"(cache purgé, ou sandbox créée hors de ce den) ; %v\n",
				nomSandbox, errAncien)
			return
		}
		fmt.Fprintf(sortie, "attention : dérive de configuration non vérifiable : %v\n", errAncien)
		return
	}
	diffs := agent.Differences(ancien, nouveau)
	if len(diffs) == 0 {
		return
	}
	fmt.Fprintf(sortie,
		"attention : la sandbox %s tourne avec le mixin de son `sbx create`, pas avec la configuration actuelle :\n",
		nomSandbox)
	for _, ligne := range diffs {
		fmt.Fprintf(sortie, "  - %s\n", ligne)
	}
	fmt.Fprintf(sortie,
		"  rien ne réapplique un mixin à une VM en marche : `sbx rm --force %s` puis relance pour l'appliquer.\n",
		nomSandbox)
}

// Attache ouvre un shell interactif dans la sandbox.
//
// `sbx exec` et non `sbx run` : run attache la commande du FLAVOR de l'image
// (souvent `claude`), n'a aucun flag pour la remplacer, et son `-- ARGS` ne fait
// qu'ajouter des arguments.
//
// Le -w reste AVANT le nom de sandbox. `sbx exec [flags] SANDBOX COMMAND
// [ARG...]` : postposé, il serait lu comme un argument de la COMMAND et
// arriverait tel quel à `bash -l`.
func Attache(ctx context.Context, r sbx.Runner, nomSandbox, workdir string) error {
	argv := []string{"exec", "-it"}
	if workdir != "" {
		argv = append(argv, "-w", workdir)
	}
	argv = append(argv, nomSandbox, "bash", "-l")
	return r.Attach(ctx, argv...)
}

func premier(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return filepath.Clean(s[0])
}
