package cli

import (
	"fmt"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/doctor"
	"github.com/spf13/cobra"
)

// newDoctorCmd takes its system accesses as a parameter rather than hard-wiring
// doctor.SystemDeps(): that is what lets a test exercise both branches of the
// exit contract without depending on the machine it runs on.
func newDoctorCmd(denHome *string, deps doctor.Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose den's configuration and environment",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "den home: %s\n\n", home)

			checks := doctor.Run(home, deps)
			failures, warnings := 0, 0
			for _, c := range checks {
				mark := "ok  "
				// Blocking() rather than a comparison copied here: doctor owns
				// the decision of what weighs on the exit code.
				switch {
				case c.Blocking():
					mark = "FAIL"
					failures++
				case c.Level == doctor.LevelWarning:
					mark = "warn"
					warnings++
				}
				fmt.Fprintf(out, "[%s] %-16s %s\n", mark, c.Name, c.Detail)
			}

			// The ORDER of these two blocks carries the exit contract and is not
			// interchangeable: both end in a return, so whichever comes first
			// decides. Swapped, a failing check accompanied by a mere warning
			// would return nil — `den doctor` at 0 on a broken install, under
			// self-contradicting output.
			if failures > 0 {
				return fmt.Errorf("%d failing check(s)", failures)
			}
			// A warning does NOT change the exit code — that is its whole point
			// — but "all good" under a [warn] line would read as a
			// contradiction.
			if warnings > 0 {
				fmt.Fprintf(out, "\nno failure, but %d warning(s): review the [warn] lines\n",
					warnings)
				return nil
			}
			fmt.Fprintln(out, "\nall good")
			return nil
		},
	}
}
