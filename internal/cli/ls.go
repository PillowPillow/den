package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/spf13/cobra"
)

// newLsCmd liste les sandboxes vivantes. Sans labels côté sbx, `den ls` est
// `sbx ls --json` dont chaque nom est décomposé (nest, worktree) — voir
// sbx.Sandbox.Nest et sbx.Sandbox.Worktree.
func newLsCmd(denHome *string, runner sbx.Runner) *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "Liste les sandboxes vivantes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}

			boxes, err := sbx.Ls(cmd.Context(), runner)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(boxes) == 0 {
				fmt.Fprintln(out, "aucune sandbox vivante")
				return nil
			}

			// Les nests déclarés servent uniquement à MARQUER les sandboxes
			// inconnues, jamais à les filtrer : une VM vivante doit rester
			// visible même si son nest a été supprimé depuis ~/.den/nests.
			//
			// L'erreur de ListNests est volontairement avalée : un ~/.den
			// cassé (YAML invalide, permissions) ne doit pas masquer des VM
			// bel et bien vivantes sur la machine — den ls doit rester la
			// commande qui marche même quand le reste de la config ne va
			// pas. Dette connue, reprise par la tâche 16 (ListNests
			// tolérante) qui distinguera un nest cassé d'un ~/.den absent.
			declares := map[string]bool{}
			nests, _ := nest.ListNests(home)
			for _, n := range nests {
				declares[n.Name] = true
			}

			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tNEST\tWORKTREE\tSTATUS\tWORKSPACES")
			for _, b := range boxes {
				nomNest := b.Nest()
				if !declares[nomNest] {
					nomNest += " ?" // non déclaré dans ~/.den/nests
				}
				wt := b.Worktree()
				if wt == "" {
					wt = "-"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\n", b.Nom, nomNest, wt, b.Statut, len(b.Workspaces))
			}
			return w.Flush()
		},
	}
}
