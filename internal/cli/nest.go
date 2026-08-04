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
	"github.com/PillowPillow/den/internal/source"
	"github.com/PillowPillow/den/internal/spawn"
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
			// Every installed source's own nests, prefixed "<source>:<name>"
			// — the same reference spawn, `den sh`/`rm`/`ports` and `den
			// nest show` all accept for that nest. A broken or unreadable
			// sources/ must not hide the local listing (srcNests doctrine
			// below).
			srcNests, srcBroken := listSourceNests(home)

			if len(nests) == 0 && len(broken) == 0 && len(srcNests) == 0 && len(srcBroken) == 0 {
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
			for _, n := range srcNests {
				base := "auto"
				if n.Ports.Base > 0 {
					base = fmt.Sprint(n.Ports.Base)
				}
				fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", n.Name, n.Stack, len(n.Repos), base)
			}
			if err := w.Flush(); err != nil {
				return err
			}
			// LOCAL nests only: a source nest is addressed "<source>:<name>",
			// which can never equal a bare subcommand name, so it never
			// shadows one.
			warnAboutShadowedNests(cmd, nests)

			allBroken := append(broken, srcBroken...)
			if len(allBroken) > 0 {
				fmt.Fprintln(cmd.OutOrStdout())
				for _, b := range allBroken {
					fmt.Fprintf(cmd.OutOrStdout(), "! %s: %v\n", b.Name, b.Err)
				}
				return fmt.Errorf("%d unreadable nest(s) out of %d",
					len(allBroken), len(nests)+len(srcNests)+len(allBroken))
			}
			return nil
		},
	}
}

// listSourceNests iterates every installed source (`os.ReadDir(source.Root)`)
// and returns its nests and broken nests, both named "<source>:<name>" — the
// same reference `den <nest>`, `den sh`/`rm`/`ports` and `den nest show` all
// accept for that nest. Renaming here, not at the call site: nest.ListNests
// itself knows nothing of sources, and every caller wants the SAME prefixed
// form, so there is one place to get it right.
//
// Fail-open: a missing or unreadable sources/ returns nothing rather than an
// error — `den nest ls`'s contract is to show what is LOCAL even when a
// source is unreachable, same doctrine as spawn.go's crossSourceCollision.
// One EXCEPTION: a source directory that exists but whose OWN nests/ cannot
// be read (permissions, not-a-directory) is reported as a single broken
// entry named after the source — silently dropping an installed source
// would be a worse surprise than naming it broken.
func listSourceNests(home string) (nests []*nest.Nest, broken []nest.BrokenNest) {
	entries, err := os.ReadDir(source.Root(home))
	if err != nil {
		return nil, nil
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		srcName := e.Name()
		srcNests, srcBroken, err := nest.ListNests(source.Dir(home, srcName))
		if err != nil {
			broken = append(broken, nest.BrokenNest{Name: srcName + ":", Err: err})
			continue
		}
		for _, n := range srcNests {
			n.Name = srcName + ":" + n.Name
			nests = append(nests, n)
		}
		for _, b := range srcBroken {
			broken = append(broken, nest.BrokenNest{Name: srcName + ":" + b.Name, Err: b.Err})
		}
	}
	return nests, broken
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
		Use:   "show <nest>",
		Short: "Show a fully resolved nest",
		Args:  exactlyOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}
			g, err := config.LoadGlobal(home)
			if err != nil {
				return err
			}

			// args[0] may be a source reference ("corp:api"): source.Locate
			// is the SOLE place that turns it into a root to load the nest
			// from — same mirror of internal/spawn.Spawn (spawn.go) kept
			// deliberately identical, so `den nest show` and `den <nest>`
			// never resolve the SAME reference to two different nests.
			nestRoot, srcName, bareNest, err := source.Locate(home, args[0])
			if err != nil {
				return err
			}
			n, err := nest.LoadNest(nestRoot, bareNest)
			if err != nil {
				return err
			}

			// Stack origin — through spawn.ResolveStack, the SAME function
			// internal/spawn.Spawn calls: both refusals it can raise (an
			// absent `stack:` inside a source, a prefixed one) must stay
			// word-identical between `den nest show` and `den <nest>`, or
			// the two would resolve the same reference to two different
			// diagnoses. Only the subject (args[0], what the user typed) is
			// this call site's own.
			stackRoot, _, ref, err := spawn.ResolveStack(home, g, nestRoot, srcName, bareNest, n, args[0])
			if err != nil {
				return err
			}
			n.Stack = ref
			stacks, err := config.LoadStacks(stackRoot)
			if err != nil {
				return err
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
		status := "required"
		if repo.Optional {
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
