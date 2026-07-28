package cli

import (
	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/spawn"
	"github.com/spf13/cobra"
)

// configureSpawn fait de la racine elle-même la commande de spawn : `den <nest>`
// n'est pas une sous-commande, c'est l'argument par défaut. cobra retombe sur le
// RunE de la racine quand args[0] ne correspond à aucune sous-commande.
//
// À appeler APRÈS les root.AddCommand : poser Args sur la racine désactive le
// legacyArgs de cobra (« unknown command »), et c'est cette bascule qui rend un
// nom de nest recevable en première position.
//
// deps est pris en paramètre plutôt que construit ici, comme newDoctorCmd :
// c'est ce qui rend vérifiable le branchement des flags sur spawn.Options — un
// flag débranché est silencieux — sans qu'un test tente d'exécuter le vrai `sbx`.
func configureSpawn(root *cobra.Command, denHome *string, deps spawn.Deps) {
	var o spawn.Options

	root.Use = "den <nest> [flags]"
	root.Args = cobra.MaximumNArgs(1)
	root.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		o.Nest = args[0]
		home, err := config.Home(*denHome)
		if err != nil {
			return err
		}
		// Copie locale : seule la Sortie est décidée ici, à l'exécution, parce
		// qu'elle seule dépend de la commande (et donc du SetOut des tests).
		d := deps
		d.Sortie = cmd.OutOrStdout()
		return spawn.Spawn(cmd.Context(), home, o, d)
	}

	root.Flags().StringVarP(&o.Worktree, "worktree", "w", "", "worktree à propager sur tous les repos")
	root.Flags().StringVar(&o.Agent, "agent", "", "agent à utiliser (défaut : defaults.agent)")
	root.Flags().StringSliceVar(&o.Without, "without", nil, "exclure ces repos optionnels")
	root.Flags().StringSliceVar(&o.Only, "only", nil, "ne garder que ces repos optionnels")
	root.Flags().BoolVar(&o.Detach, "detach", false, "ne pas attacher de shell après le spawn")
}
