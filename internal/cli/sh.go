package cli

import (
	"fmt"

	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/spawn"
	"github.com/spf13/cobra"
)

// newShCmd ouvre un shell dans une sandbox déjà vivante.
//
// Aucun den home consulté : `den sh` ne travaille QUE sur ce que `sbx ls --json`
// remonte. Une sandbox dont le nest a été supprimé de ~/.den doit rester
// joignable, et un ~/.den cassé ne doit pas priver l'utilisateur de son shell.
func newShCmd(runner sbx.Runner) *cobra.Command {
	return &cobra.Command{
		Use:   "sh <name>",
		Short: "Ouvre un shell dans une sandbox existante",
		Args:  exactementUnArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			nom := args[0]
			boxes, err := sbx.Ls(cmd.Context(), runner)
			if err != nil {
				return err
			}
			if b := sbx.Trouve(boxes, nom); b != nil {
				// Même garde que `den <nest>`, par le même helper : les deux
				// chemins finissent par un `sbx exec`, et un shell ouvert dans
				// une VM dont den ne sait rien est aussi faux ici que là-bas.
				// Une VM ARRÊTÉE passe : `sbx exec` la redémarre.
				if err := b.VerifieAttachable(); err != nil {
					return err
				}
				if b.EstArretee() {
					fmt.Fprintf(cmd.OutOrStdout(),
						"sandbox %s arrêtée : elle redémarre à l'attache (son état est conservé)\n", b.Nom)
				}
				// Le workdir vient du premier workspace REMONTÉ PAR LA VM,
				// jamais d'un chemin recalculé depuis la config : sans lui,
				// l'utilisateur atterrit dans le home de la VM, pas dans son
				// code.
				return spawn.Attache(cmd.Context(), runner, b.Nom, b.Workdir())
			}

			// Déjà triés : sbx.Ls rend ses sandboxes par nom, et c'est
			// verrouillé par TestLsTriParNom. Retrier ici dupliquerait cette
			// connaissance sans rien garantir de plus.
			noms := make([]string, 0, len(boxes))
			for _, b := range boxes {
				noms = append(noms, b.Nom)
			}
			if len(noms) == 0 {
				return fmt.Errorf("sandbox %q introuvable — aucune sandbox ne tourne", nom)
			}
			return fmt.Errorf("sandbox %q introuvable (vivantes : %v)", nom, noms)
		},
	}
}
