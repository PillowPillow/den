// Package cli câble les commandes cobra de den. Aucune logique métier ici :
// tout ce qui se teste vit dans internal/config, internal/nest, internal/doctor.
package cli

import (
	"fmt"

	"github.com/PillowPillow/den/internal/doctor"
	"github.com/spf13/cobra"
)

// Version est injectée au build (-ldflags "-X .../internal/cli.Version=...").
var Version = "dev"

// denHome porte la valeur du flag global --den-home ; vide = résolution par défaut.
var denHome string

// NewRootCmd construit un arbre de commandes neuf. Renvoyer une nouvelle instance
// à chaque appel (plutôt qu'un singleton) est ce qui rend les commandes testables.
func NewRootCmd() *cobra.Command {
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
	root.AddCommand(newNestCmd())
	root.AddCommand(newDoctorCmd(&denHome, doctor.DepsSysteme()))
	return root
}

// Execute est le point d'entrée appelé par main.
func Execute() error {
	return NewRootCmd().Execute()
}
