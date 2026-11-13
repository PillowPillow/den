// Package cli wires den's cobra commands. No business logic here.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/PillowPillow/den/internal/prompt"
	"github.com/PillowPillow/den/internal/prompt/huhui"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/worktree"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Version is injected at build time (-ldflags "-X .../internal/cli.Version=...").
var Version = "dev"

// Deps holds every system access the cobra root hands down to its commands.
// Each real implementation touches the machine, so tests build Deps by hand
// and leave a field nil rather than inherit it.
type Deps struct {
	Sbx    sbx.Runner
	Git    worktree.Git
	Prompt prompt.Prompter
	IsTTY  func() bool
	Getwd  func() (string, error)
	Out    io.Writer
	Err    io.Writer
}

// SystemDeps wires the real system accesses.
func SystemDeps() Deps {
	return Deps{
		Sbx:    sbx.NewExec(""),
		Git:    worktree.NewGit(),
		Prompt: huhui.New(),
		IsTTY:  func() bool { return term.IsTerminal(int(os.Stdin.Fd())) },
		Getwd:  os.Getwd,
		Out:    os.Stdout,
		Err:    os.Stderr,
	}
}

// NewRoot builds a fresh command tree on every call, so two trees in one
// process can carry two different --den-home values.
func NewRoot(deps Deps) *cobra.Command {
	var denHome string

	root := &cobra.Command{
		Use:           "den",
		Short:         "Simple, repeatable sbx sandboxes",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	root.PersistentFlags().StringVar(&denHome, "den-home", "",
		"den config directory (default: $DEN_HOME or ~/.den)")
	if deps.Out != nil {
		root.SetOut(deps.Out)
	}
	if deps.Err != nil {
		root.SetErr(deps.Err)
	}

	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print den's version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "den %s\n", displayVersion())
			return nil
		},
	})
	root.AddCommand(newUpCmd(deps, &denHome))
	root.AddCommand(newExecCmd(deps, &denHome))
	return root
}

// Execute is the entry point called by main.
//
// signal.NotifyContext turns Ctrl-C and SIGTERM into an ordinary cancellation,
// so deferred cleanups run instead of the process dying on the spot. No re-arm
// on a second signal: sbx's Attach sets cmd.Cancel = nil so an in-shell Ctrl-C
// the tty already delivered must not kill den while sbx still holds the tty.
func Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return NewRoot(SystemDeps()).ExecuteContext(ctx)
}
