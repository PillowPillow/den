package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
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
// Trente secondes : largement au-dessus du coût mesuré de ces sondes sur un
// dépôt sain (hash-object mesuré à 499 ms pour 4 fichiers de 64 Mio marqués,
// cf. internal/worktree/worktree.go), mais fini — un montage mort échoue avec
// un message plutôt que de pendre pour toujours.
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
				noms := make([]string, 0, len(boxes))
				for _, b := range boxes {
					noms = append(noms, b.Nom)
				}
				sort.Strings(noms)
				return fmt.Errorf("sandbox %q introuvable (vivantes : %v)", nom, noms)
			}

			out := cmd.OutOrStdout()

			// Les worktrees d'abord : si l'un est sale, on s'arrête AVANT de
			// détruire la sandbox. L'inverse laisserait l'utilisateur sans VM
			// et avec un message d'erreur sur un dossier.
			if !garderWorktrees {
				if err := nettoieWorktrees(cmd.Context(), home, nom, g, force, out); err != nil {
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
// worktree.Retire ne supprime jamais un worktree : il le déplace vers la
// corbeille de den_home et rend le chemin de l'entrée créée. Ce chemin est la
// SEULE trace que l'utilisateur aura de l'endroit où son travail est parti :
// il est donc annoncé pour chaque worktree effectivement déplacé.
func nettoieWorktrees(ctx context.Context, home, nomSandbox string, g worktree.Git, force bool, out io.Writer) error {
	nomNest, wt := sbx.DecomposeNom(nomSandbox)
	if wt == "" {
		return nil // pas de worktree : rien à nettoyer
	}

	gl, err := config.LoadGlobal(home)
	if err != nil {
		return err
	}
	n, err := nest.LoadNest(home, nomNest)
	if err != nil {
		fmt.Fprintf(out, "nest %q illisible : worktrees non nettoyés (%v)\n", nomNest, err)
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
