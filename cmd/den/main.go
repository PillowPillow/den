package main

import (
	"fmt"
	"os"

	"github.com/PillowPillow/den/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "den:", err)
		os.Exit(1)
	}
}
