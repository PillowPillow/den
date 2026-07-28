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
			// den ls ne retourne JAMAIS d'erreur et ne masque JAMAIS une VM
			// à cause d'un problème de nests : un nest cassé (YAML invalide,
			// clé inconnue) ou une racine nests/ illisible sont signalés sur
			// stderr, nommément, mais n'empêchent ni le chargement des nests
			// sains ni l'affichage des sandboxes. Cette propriété tient tant
			// que ListNests continue de séparer les nests cassés (2e valeur,
			// non bloquante, testé par ListNests lui-même) des échecs
			// structurels (3e valeur) plutôt que de les fondre dans une
			// erreur bloquante unique.
			nests, casses, err := nest.ListNests(home)
			for _, c := range casses {
				fmt.Fprintf(cmd.ErrOrStderr(), "nest %s illisible : %v\n", c.Nom, c.Err)
			}
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "liste des nests : %v\n", err)
			}
			declares := map[string]bool{}
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
