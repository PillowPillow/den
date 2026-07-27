package cli

import (
	"fmt"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/doctor"
	"github.com/spf13/cobra"
)

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnostique la configuration den et l'environnement",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := config.Home(denHome)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "den home: %s\n\n", home)

			checks := doctor.Run(home, doctor.DepsSysteme())
			echecs := 0
			for _, c := range checks {
				marque := "ok  "
				if !c.OK {
					marque = "FAIL"
					echecs++
				}
				fmt.Fprintf(out, "[%s] %-16s %s\n", marque, c.Nom, c.Detail)
			}

			if echecs > 0 {
				return fmt.Errorf("%d diagnostic(s) en échec", echecs)
			}
			fmt.Fprintln(out, "\ntout est en ordre")
			return nil
		},
	}
}
