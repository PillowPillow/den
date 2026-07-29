package cli

import (
	"fmt"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/doctor"
	"github.com/spf13/cobra"
)

// newDoctorCmd prend ses accès système en paramètre plutôt que de câbler
// doctor.DepsSysteme() en dur : c'est ce qui permet au test d'exercer les deux
// branches du contrat de sortie sans dépendre de la machine qui l'exécute.
func newDoctorCmd(denHome *string, deps doctor.Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnostique la configuration den et l'environnement",
		Args:  aucunArgument,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "den home: %s\n\n", home)

			checks := doctor.Run(home, deps)
			echecs, avertissements := 0, 0
			for _, c := range checks {
				marque := "ok  "
				// Bloquant() et non une comparaison recopiée ici : c'est
				// doctor qui décide de ce qui pèse sur le code de sortie, et
				// il n'y a qu'un seul endroit où cette décision se prend.
				switch {
				case c.Bloquant():
					marque = "FAIL"
					echecs++
				case c.Niveau == doctor.NiveauAvertissement:
					marque = "warn"
					avertissements++
				}
				fmt.Fprintf(out, "[%s] %-16s %s\n", marque, c.Nom, c.Detail)
			}

			if echecs > 0 {
				return fmt.Errorf("%d diagnostic(s) en échec", echecs)
			}
			// Un avertissement ne change PAS le code de sortie — c'est tout son
			// intérêt — mais « tout est en ordre » sous une ligne [warn] se
			// lirait comme une contradiction, et l'utilisateur croirait à un
			// affichage résiduel plutôt qu'à un message pour lui.
			if avertissements > 0 {
				fmt.Fprintf(out, "\naucun échec, mais %d avertissement(s) : relis les lignes [warn]\n",
					avertissements)
				return nil
			}
			fmt.Fprintln(out, "\ntout est en ordre")
			return nil
		},
	}
}
