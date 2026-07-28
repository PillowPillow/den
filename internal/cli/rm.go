package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/worktree"
	"github.com/spf13/cobra"
)

// delaiSondesGit borne CHAQUE appel à worktree.Retire (un par repo). Retire
// enchaîne plusieurs sondes git séquentielles (worktree list, status,
// check-ignore, hash-object…) ; un dépôt sur un montage réseau mort les
// ferait toutes pendre indéfiniment, et `den rm` ne rendrait jamais la main.
//
// Trente secondes est un choix RAISONNÉ, pas une mesure du cas visé — aucune
// mesure d'un montage réseau mort n'est disponible sur ce poste. Attention en
// particulier à `hash-object` (internal/worktree/worktree.go, empreintesDisque) :
// il lit le contenu INTÉGRAL de chaque fichier marqué (skip-worktree /
// assume-unchanged) SANS AUCUNE BORNE — 499 ms mesurés en 15a pour 4 fichiers
// de 64 Mio marqués, débit du disque. Ce coût est nul sur un dépôt ordinaire
// (l'ensemble des fichiers marqués y est vide), mais un arbre marqué de
// plusieurs Gio peut légitimement dépasser 30 s : `den rm` échouerait alors
// sur un dépôt par ailleurs parfaitement sain, pas sur un montage mort.
// Compromis assumé faute de mieux : borner plus court romprait ce cas
// légitime, ne pas borner du tout expose au montage mort.
//
// Variable de PAQUET, pas const : un test doit pouvoir la réduire pour
// vérifier que l'échéance est bien posée sans attendre 30 s pour de vrai
// (même patron que Version dans root_test.go).
var delaiSondesGit = 30 * time.Second

// newRmCmd détruit une sandbox. Le profil agent — monté depuis config_dir —
// n'est JAMAIS touché : c'est tout l'intérêt d'un config_dir monté RW, et un
// `den rm` qui l'effacerait obligerait l'utilisateur à refaire /login à
// chaque suppression.
func newRmCmd(denHome *string, runner sbx.Runner, g worktree.Git) *cobra.Command {
	var garderWorktrees, force bool

	cmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "Détruit une sandbox (le profil agent persiste)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nom := args[0]
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}

			boxes, err := sbx.Ls(cmd.Context(), runner)
			if err != nil {
				return err
			}
			if sbx.Trouve(boxes, nom) == nil {
				// Déjà triées : sbx.Ls rend ses sandboxes par nom (verrouillé
				// par TestLsTriParNom) — retrier ici dupliquerait cette
				// connaissance sans rien garantir de plus (même choix que
				// `den sh`, cf. sh.go).
				noms := make([]string, 0, len(boxes))
				for _, b := range boxes {
					noms = append(noms, b.Nom)
				}
				return fmt.Errorf("sandbox %q introuvable (vivantes : %v)", nom, noms)
			}

			out := cmd.OutOrStdout()

			// Les worktrees d'abord : si l'un est sale, on s'arrête AVANT de
			// détruire la sandbox. L'inverse laisserait l'utilisateur sans VM
			// et avec un message d'erreur sur un dossier.
			if !garderWorktrees {
				if err := nettoieWorktrees(cmd.Context(), home, nom, g, force, out, cmd.ErrOrStderr()); err != nil {
					return err
				}
			}

			if _, err := runner.Run(cmd.Context(), "rm", "--force", nom); err != nil {
				return err
			}
			fmt.Fprintf(out, "sandbox %s détruite (le profil de l'agent est conservé)\n", nom)
			return nil
		},
	}
	cmd.Flags().BoolVar(&garderWorktrees, "keep-worktrees", false,
		"conserver les worktrees créés par den")
	cmd.Flags().BoolVar(&force, "force", false,
		"supprimer les worktrees même s'ils portent des modifications non commitées")
	return cmd
}

// nettoieWorktrees retire, via worktree.Retire, les worktrees que den a créés
// pour cette sandbox — un par repo du nest. Best-effort sur la RÉSOLUTION (un
// nest supprimé depuis ~/.den/nests ne doit pas empêcher de détruire une
// sandbox bel et bien vivante) ; strict sur la SUPPRESSION (un worktree sale
// arrête tout — cf. worktree.Retire).
//
// out reçoit les messages de succès (chemin de corbeille), avert les
// avertissements best-effort — deux flux distincts pour qu'un script qui
// n'examine que la sortie standard voie un `den rm` réussi sans y trouver un
// avertissement qui compte.
//
// worktree.Retire ne supprime jamais un worktree : il le déplace vers la
// corbeille de den_home et rend le chemin de l'entrée créée. Ce chemin est la
// SEULE trace que l'utilisateur aura de l'endroit où son travail est parti :
// il est donc annoncé pour chaque worktree effectivement déplacé.
func nettoieWorktrees(ctx context.Context, home, nomSandbox string, g worktree.Git, force bool, out, avert io.Writer) error {
	nomNest, wt := sbx.DecomposeNom(nomSandbox)
	if wt == "" {
		return nil // pas de worktree : rien à nettoyer
	}

	// sbx.Ls ne valide RIEN de ce qu'il lit : une sandbox listée par `sbx ls`
	// peut avoir été créée hors de den, avec un nom que sbx accepte mais que
	// den refuserait (« api../../evade », par exemple). Sans ce contrôle, ce
	// nom traverse tel quel jusqu'à worktree.Chemin et envoie Retire hors de
	// worktree_root — la Cible de 15a a été conçue pour rendre ce chemin
	// inexprimable, mais seulement si le nom qui l'alimente est déjà propre.
	// sbx.ValiderNomSandbox est la source UNIQUE de ce verdict dans le projet
	// (internal/sbx/nom.go) — sbx.ArgvCreate et policy.Settle la consultent
	// avant de transformer ce même genre de nom en chemin ou en argument.
	if err := sbx.ValiderNomSandbox(nomSandbox); err != nil {
		return fmt.Errorf("nettoyage des worktrees : %w", err)
	}

	// Une commande valide ce qu'elle UTILISE. Ici, ce sont exactement deux
	// champs : WorktreeLayout et WorktreeRoot, tous deux lus plus bas pour
	// composer la Cible de worktree.Retire.
	//
	// LoadGlobal (validant) serait un contresens à cet endroit : il ferait
	// échouer `den rm` sur un `agents.claude.update` fautif — sans le moindre
	// rapport avec les worktrees — et laisserait l'utilisateur avec une VM
	// vivante qu'il ne peut plus détruire. C'est la doctrine T13/T16 : un
	// ~/.den cassé ne bloque jamais l'accès à une VM vivante, et c'est déjà ce
	// que promet le « best-effort sur la RÉSOLUTION » de la godoc ci-dessus.
	//
	// Mais l'inverse — ne rien valider — rouvrirait la 14ᵉ configuration
	// hostile : LoadGlobalSansValider ne défaute que le layout VIDE, donc un
	// `centrl` survit, et den nettoierait à côté SANS RIEN DIRE. D'où le
	// contrôle explicite, restreint aux deux champs concernés.
	gl, err := config.LoadGlobalSansValider(home)
	if err != nil {
		return err
	}
	if errs := gl.ValideWorktree(); len(errs) > 0 {
		return fmt.Errorf("nettoyage des worktrees : %w", config.ErreurConfig(home, errs))
	}
	n, err := nest.LoadNest(home, nomNest)
	if err != nil {
		// wt et gl.WorktreeRoot sont tous deux disponibles ici : sans eux,
		// l'utilisateur apprend qu'un worktree a été abandonné, mais pas où
		// aller le chercher.
		ou := filepath.Join(gl.WorktreeRoot, wt)
		if gl.WorktreeLayout == "per-repo" {
			ou = fmt.Sprintf("chaque repo du nest, sous <repo>/.den/%s", wt)
		}
		fmt.Fprintf(avert, "nest %q illisible : le worktree %q n'a pas pu être nettoyé "+
			"(attendu sous %s) : %v\n", nomNest, wt, ou, err)
		return nil
	}

	for _, repo := range n.Repos {
		// Une échéance PAR repo, pas une seule pour toute la boucle : un
		// repo hors service ne doit pas grignoter le budget des repos
		// suivants du même nest.
		ctxRepo, cancel := context.WithTimeout(ctx, delaiSondesGit)
		dest, err := worktree.Retire(ctxRepo, g, worktree.Cible{
			DenHome:    home,
			Layout:     gl.WorktreeLayout,
			Root:       gl.WorktreeRoot,
			Nest:       nomSandbox,
			Worktree:   wt,
			CheminRepo: repo.Path,
			Force:      force,
		})
		cancel()
		if err != nil {
			return err
		}
		if dest == "" {
			continue // le dossier avait déjà disparu : rien à annoncer
		}
		fmt.Fprintf(out, "worktree envoyé à la corbeille : %s\n", dest)
	}
	return nil
}
