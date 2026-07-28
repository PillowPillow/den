// Package cli câble les commandes cobra de den. Aucune logique métier ici :
// tout ce qui se teste vit dans internal/config, internal/nest, internal/doctor.
package cli

import (
	"fmt"

	"github.com/PillowPillow/den/internal/doctor"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/spawn"
	"github.com/spf13/cobra"
)

// Version est injectée au build (-ldflags "-X .../internal/cli.Version=...").
var Version = "dev"

// NewRootCmd construit un arbre de commandes neuf avec les accès système
// réels. Renvoyer une nouvelle instance à chaque appel (plutôt qu'un
// singleton) est ce qui rend les commandes testables.
func NewRootCmd() *cobra.Command {
	return NewRootCmdAvec(doctor.DepsSysteme(), sbx.NewExec(""))
}

// NewRootCmdAvec prend ses accès au monde en paramètre : c'est ce qui permet
// aux tests d'exercer `den ls` (et `den <nest>`) sans que sbx soit installé.
//
// denHome est déclaré ICI, pas au niveau du paquet : deux arbres de commandes
// construits dans le même processus doivent pouvoir porter deux --den-home
// différents. Les sous-commandes en reçoivent l'adresse, la valeur n'étant
// remplie qu'au parsing des flags.
//
// runner alimente à la fois `den ls` et spawn.Deps.Sbx : les deux doivent
// parler au même sbx pour qu'un Fake scripté sur `ls --json` réponde de façon
// cohérente qu'on passe par `den ls` ou par le settle-loop du spawn. Le reste
// de spawn.Deps (Git, Policy) vient de spawn.DepsSysteme() : ces accès-là ne
// sont pas encore paramétrables depuis la racine, donc réels même sous test.
// Sans risque ici : aucun test qui passe par cette fonction ne crée de
// worktree ni ne déclare d'egress (ça reste le rôle de spawn_test.go, qui
// appelle configureSpawn directement avec ses propres deps factices).
func NewRootCmdAvec(deps doctor.Deps, runner sbx.Runner) *cobra.Command {
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
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "den %s\n", Version)
			return nil
		},
	})
	root.AddCommand(newNestCmd(&denHome))
	root.AddCommand(newDoctorCmd(&denHome, deps))
	root.AddCommand(newLsCmd(&denHome, runner))

	spawnDeps := spawn.DepsSysteme()
	spawnDeps.Sbx = runner
	// En DERNIER : configureSpawn pose Args sur la racine, ce qui n'a de sens
	// qu'une fois les sous-commandes enregistrées.
	configureSpawn(root, &denHome, spawnDeps)
	return root
}

// Execute est le point d'entrée appelé par main.
func Execute() error {
	return NewRootCmd().Execute()
}
