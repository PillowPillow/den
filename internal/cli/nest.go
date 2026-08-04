package cli

import (
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
	"github.com/spf13/cobra"
)

func newNestCmd(denHome *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "nest",
		Short: "Inspect the declared nests",
	}
	cmd.AddCommand(newNestLsCmd(denHome), newNestShowCmd(denHome))
	return cmd
}

func newNestLsCmd(denHome *string) *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List the declared nests",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}
			nests, broken, err := nest.ListNests(home)
			if err != nil {
				return err
			}
			if len(nests) == 0 && len(broken) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "no nest declared in %s/nests\n", home)
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NEST\tSTACK\tREPOS\tPORTS")
			for _, n := range nests {
				base := "auto"
				if n.Ports.Base > 0 {
					base = fmt.Sprint(n.Ports.Base)
				}
				fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", n.Name, n.Stack, len(n.Repos), base)
			}
			if err := w.Flush(); err != nil {
				return err
			}
			warnAboutShadowedNests(cmd, nests)

			if len(broken) > 0 {
				fmt.Fprintln(cmd.OutOrStdout())
				for _, b := range broken {
					fmt.Fprintf(cmd.OutOrStdout(), "! %s: %v\n", b.Name, b.Err)
				}
				return fmt.Errorf("%d unreadable nest(s) out of %d", len(broken), len(nests)+len(broken))
			}
			return nil
		},
	}
}

// warnAboutShadowedNests reports the nests that carry a subcommand's name.
// Those are declared, listed and resolved by `den nest show`, but `den <name>`
// will ALWAYS run the subcommand: cobra finds it before the argument reaches
// the root's RunE. They can never be spawned, and nothing else says so.
//
// On stderr, so `den nest ls | ...` stays pipeable, and nothing at all when
// there is no collision: a permanent warning stops being read.
//
// The comparison covers the names AND the aliases of root.Commands(), exactly
// what cobra consults to route an argument, and never a hardcoded list. The
// match is EXACT: a nest named "l" is not shadowed by `ls`.
func warnAboutShadowedNests(cmd *cobra.Command, nests []*nest.Nest) {
	commands := map[string]bool{}
	for _, sub := range cmd.Root().Commands() {
		commands[sub.Name()] = true
		for _, alias := range sub.Aliases {
			commands[alias] = true
		}
	}
	for _, n := range nests {
		if commands[n.Name] {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"warning: nest %q is shadowed by the `den %s` subcommand — "+
					"`den %s` will run the command, never this nest. Rename it to be able to spawn it.\n",
				n.Name, n.Name, n.Name)
		}
	}
}

func newNestShowCmd(denHome *string) *cobra.Command {
	var opts nest.Options
	cmd := &cobra.Command{
		Use:   "show <nest> [repo...]",
		Short: "Show a fully resolved nest",
		Args:  atLeastOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}
			g, err := config.LoadGlobal(home)
			if err != nil {
				return err
			}
			stacks, err := config.LoadStacks(home)
			if err != nil {
				return err
			}
			n, err := nest.LoadNest(home, args[0])
			if err != nil {
				return err
			}
			// The dry-run of `den <nest> [repo...]`: same resolution, no side
			// effect. Reading the working directory here mirrors internal/spawn
			// — internal/nest never reads it itself.
			opts.Repos = args[1:]
			if len(opts.Repos) > 0 {
				if opts.Cwd, err = os.Getwd(); err != nil {
					return fmt.Errorf(
						"reading the working directory, needed to resolve the repos given on "+
							"the command line: %w", err)
				}
			}
			r, err := nest.Resolve(home, g, stacks, n, opts)
			if err != nil {
				return err
			}
			writeResolution(cmd.OutOrStdout(), r)
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.Agent, "agent", "", "agent to use (default: defaults.agent)")
	cmd.Flags().StringSliceVar(&opts.Without, "without", nil, "exclude these optional repos")
	cmd.Flags().StringSliceVar(&opts.Only, "only", nil, "keep only these optional repos")
	return cmd
}

func writeResolution(w io.Writer, r *nest.Resolved) {
	fmt.Fprintf(w, "nest:   %s\n", r.Nest.Name)
	fmt.Fprintf(w, "stack:  %s (image %s)\n", r.Stack.Name, r.Stack.Image)
	fmt.Fprintf(w, "agent:  %s\n", r.AgentName)
	fmt.Fprintf(w, "  config_dir: %s\n", r.AgentConfigDir)
	fmt.Fprintf(w, "  update:     %s\n", r.Agent.Update)
	fmt.Fprintf(w, "ssh:    %s\n", r.SSHMode)
	fmt.Fprintf(w, "worktrees: %s (%s)\n", r.WorktreeRoot, r.WorktreeLayout)

	fmt.Fprintln(w, "repos:")
	for _, repo := range r.Repos {
		// A repo given on the command line is neither required nor optional —
		// those words describe a `repos:` declaration and --without/--only,
		// which never address it. Naming its origin instead is what makes this
		// listing a usable dry-run.
		status := "required"
		switch {
		case repo.AdHoc:
			status = "command line"
		case repo.Optional:
			status = "optional"
		}
		fmt.Fprintf(w, "  - %s (%s)\n", repo.Path, status)
	}

	fmt.Fprintf(w, "egress (%d):\n", len(r.Egress))
	for _, h := range r.Egress {
		fmt.Fprintf(w, "  - %s\n", h)
	}

	if len(r.Env) > 0 {
		fmt.Fprintln(w, "env (resolved):")
		// Go map iteration order is not deterministic: everything printed is
		// sorted.
		for _, k := range slices.Sorted(maps.Keys(r.Env)) {
			fmt.Fprintf(w, "  %s=%s\n", k, r.Env[k])
		}
	}

	if len(r.Nest.Ports.Publish) > 0 {
		fmt.Fprintln(w, "declared ports:")
		for _, p := range r.Nest.Ports.Publish {
			marks := []string{}
			if p.Open {
				marks = append(marks, "open")
			}
			if p.LoopbackLock {
				marks = append(marks, "loopback-locked")
			}
			suffix := ""
			if len(marks) > 0 {
				suffix = " [" + strings.Join(marks, ", ") + "]"
			}
			fmt.Fprintf(w, "  - %s -> %d%s\n", p.Name, p.Container, suffix)
		}
	}
}
