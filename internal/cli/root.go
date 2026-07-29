// Package cli câble les commandes cobra de den. Aucune logique métier ici :
// tout ce qui se teste vit dans internal/config, internal/nest, internal/doctor.
package cli

import (
	"fmt"

	"github.com/PillowPillow/den/internal/doctor"
	"github.com/PillowPillow/den/internal/policy"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/spawn"
	"github.com/PillowPillow/den/internal/worktree"
	"github.com/spf13/cobra"
)

// Version est injectée au build (-ldflags "-X .../internal/cli.Version=...").
var Version = "dev"

// Deps regroupe TOUS les accès système que la racine cobra distribue à ses
// sous-commandes. Sbx est UNIQUE : `den ls` et le spawn le consomment tous
// les deux depuis CE champ, il n'existe nulle part ailleurs dans cette
// structure.
//
// Une version antérieure de ce type embarquait une spawn.Deps entière (avec
// son propre champ Sbx), et NewRootCmdAvec devait ÉCRASER
// spawnDeps.Sbx = deps.Sbx pour que les deux chemins restent d'accord — une
// ligne qu'un refactor pouvait supprimer sans qu'aucun test ne le remarque
// (mesuré : le retrait de cette ligne laissait la suite verte). La structure
// actuelle rend cette divergence impossible plutôt que de la tester : il n'y
// a structurellement qu'un seul Sbx à fournir.
type Deps struct {
	Doctor doctor.Deps
	Sbx    sbx.Runner
	Git    worktree.Git
	Policy policy.Options
}

// DepsSysteme branche tous les accès système réels : sbx du PATH, git réel,
// et la patience par défaut du settle-loop de policy.
func DepsSysteme() Deps {
	return Deps{
		Doctor: doctor.DepsSysteme(),
		Sbx:    sbx.NewExec(""),
		Git:    worktree.NewGit(),
		Policy: policy.OptionsDefaut(),
	}
}

// NewRootCmd construit un arbre de commandes neuf avec les accès système
// réels. Renvoyer une nouvelle instance à chaque appel (plutôt qu'un
// singleton) est ce qui rend les commandes testables.
func NewRootCmd() *cobra.Command {
	return NewRootCmdAvec(DepsSysteme())
}

// NewRootCmdAvec prend ses accès au monde en paramètre : c'est ce qui permet
// aux tests d'exercer `den ls` et `den <nest>` sans que sbx (ni git) soit
// installé — CHAQUE accès système est fourni par l'appelant, aucun n'est
// câblé en dur ici.
//
// denHome est déclaré ICI, pas au niveau du paquet : deux arbres de commandes
// construits dans le même processus doivent pouvoir porter deux --den-home
// différents. Les sous-commandes en reçoivent l'adresse, la valeur n'étant
// remplie qu'au parsing des flags.
func NewRootCmdAvec(deps Deps) *cobra.Command {
	var denHome string

	root := &cobra.Command{
		Use:           "den",
		Short:         "Sandboxes sbx simples et répétables",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&denHome, "den-home", "",
		"dossier de config den (défaut : $DEN_HOME ou ~/.den)")

	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Affiche la version de den",
		Args:  aucunArgument,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "den %s\n", Version)
			return nil
		},
	})
	root.AddCommand(newNestCmd(&denHome))
	root.AddCommand(newDoctorCmd(&denHome, deps.Doctor))
	root.AddCommand(newLsCmd(&denHome, deps.Sbx))
	// Même deps.Sbx que `den ls` et que le spawn : il n'y a qu'un seul
	// sbx.Runner dans cette structure, et c'est celui-là.
	root.AddCommand(newShCmd(deps.Sbx))
	// Idem pour deps.Git : le seul worktree.Git de cette structure, celui que
	// le spawn utilise pour `den <nest> -w`.
	root.AddCommand(newRmCmd(&denHome, deps.Sbx, deps.Git))

	// spawn.Deps est ASSEMBLÉE ici, à partir des mêmes champs que newLsCmd
	// vient de recevoir : deps.Sbx est la SEULE source, il n'y a pas de
	// second Sbx caché dans une spawn.Deps embarquée qu'il faudrait
	// synchroniser à la main. Sortie n'est pas renseignée : configureSpawn
	// l'écrase à chaque exécution avec cmd.OutOrStdout() (seule façon de
	// suivre le SetOut d'un test).
	//
	// En DERNIER : configureSpawn pose Args sur la racine, ce qui n'a de sens
	// qu'une fois les sous-commandes enregistrées.
	configureSpawn(root, &denHome, spawn.Deps{
		Sbx:    deps.Sbx,
		Git:    deps.Git,
		Policy: deps.Policy,
	})
	// En DERNIER aussi, et pour la même raison que configureSpawn : la
	// francisation parcourt l'arbre des commandes pour y poser l'usage du flag
	// --help, et ne verrait pas une commande ajoutée après elle.
	franciseCobra(root)
	return root
}

// Execute est le point d'entrée appelé par main.
func Execute() error {
	return NewRootCmd().Execute()
}
