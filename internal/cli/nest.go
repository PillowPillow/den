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
			// — the same reference spawn, `den exec`/`rm`/`ports` and `den
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

			allBroken := slices.Concat(broken, srcBroken)
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
// same reference `den spawn`, `den exec`/`rm`/`ports` and `den nest show` all
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
			broken = append(broken, nest.BrokenNest{Name: config.JoinSourceRef(srcName, ""), Err: err})
			continue
		}
		for _, n := range srcNests {
			n.Name = config.JoinSourceRef(srcName, n.Name)
			nests = append(nests, n)
		}
		for _, b := range srcBroken {
			broken = append(broken, nest.BrokenNest{Name: config.JoinSourceRef(srcName, b.Name), Err: b.Err})
		}
	}
	return nests, broken
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

			// args[0] may be a source reference ("corp:api"): source.Locate
			// is the SOLE place that turns it into a root to load the nest
			// from — same mirror of internal/spawn.Spawn (spawn.go) kept
			// deliberately identical, so `den nest show` and `den spawn`
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
			// word-identical between `den nest show` and `den spawn`, or
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
			// The dry-run of `den spawn <nest> [repo...]`: same resolution, no side
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
			// args[0], not r.Nest.Name: LoadNest sets Name to the bare
			// filename ("api", not "corp:api" — the filename is
			// authoritative, spec §2), so the header dropped the prefix the
			// user typed. On a den that also owns a LOCAL "api", that header
			// named a different nest than the one printed below it.
			writeResolution(cmd.OutOrStdout(), args[0], r)
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.Agent, "agent", "", "agent to use (default: defaults.agent)")
	cmd.Flags().StringSliceVar(&opts.Without, "without", nil, "exclude these optional repos")
	cmd.Flags().StringSliceVar(&opts.Only, "only", nil, "keep only these optional repos")
	return cmd
}

// ref is the reference the user typed, and it is what the header shows —
// see the call site for why r.Nest.Name is the wrong string. The STACK line
// stays bare: a source nest's `stack:` resolves inside that same source, so
// there is no second prefix for the reader to reconcile.
func writeResolution(w io.Writer, ref string, r *nest.Resolved) {
	fmt.Fprintf(w, "nest:   %s\n", ref)
	fmt.Fprintf(w, "stack:  %s (image %s)\n", r.Stack.Name, r.Stack.Image)
	fmt.Fprintf(w, "agent:  %s\n", r.AgentName)
	fmt.Fprintf(w, "  config_dir: %s\n", r.AgentConfigDir)
	fmt.Fprintf(w, "  update:     %s\n", r.Agent.Update)
	fmt.Fprintf(w, "ssh:    %s\n", r.SSHMode)
	fmt.Fprintf(w, "worktrees: %s (%s)\n", r.WorktreeRoot, r.WorktreeLayout)

	// Mounts belong in the dry-run for the same reason repos do, and more
	// sharply: they hand a host directory to the microVM, and `mounts:` is
	// GLOBAL, so nothing in the nest the user is inspecting mentions them. Left
	// out, this listing answers "what will this spawn receive" with the one part
	// of the answer the user did not write per-nest.
	//
	// The ssh.mode sugar appears here as an ordinary mount (Key "ssh.dir") — it
	// IS one after resolveMounts, and the `ssh:` line above names the mode
	// without ever saying which directory it exposes.
	if len(r.Mounts) > 0 {
		fmt.Fprintln(w, "mounts:")
		for _, m := range r.Mounts {
			mode := "rw"
			if m.RO {
				mode = "ro"
			}
			// A link-less mount is legitimate (config.Mount): it lands at its
			// host path inside the VM and the tool is pointed at it by an env
			// var. Saying so beats printing an empty arrow.
			target := "no link (reachable at the host path)"
			if m.Link != "" {
				target = "-> " + m.Link
			}
			fmt.Fprintf(w, "  - %s %s [%s, from %s]\n", m.Host, target, mode, m.Key)
		}
	}

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
