package main

import (
	"fmt"
	"os"

	"github.com/PillowPillow/den/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		// Two classes of failure, deliberately told apart: den's own, which
		// keep den's `den:` prefix and its status of 1, and a command that ran
		// inside the sandbox and failed on its own terms, whose status becomes
		// den's and whose output den does not talk over. cli.ExitStatus holds
		// the rule and the tests; here it must stay a single branch.
		code, denOwnsMessage := cli.ExitStatus(err)
		if denOwnsMessage {
			fmt.Fprintln(os.Stderr, "den:", err)
		}
		os.Exit(code)
	}
}
