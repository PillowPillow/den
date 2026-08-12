package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/doctor"
	"github.com/PillowPillow/den/internal/manifest"
	"github.com/PillowPillow/den/internal/nest"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/spf13/cobra"
)

// newLsCmd lists live sandboxes. Without labels on the sbx side, `den ls` is
// `sbx ls --json` with each name split into (nest, instance) — see
// sbx.Sandbox.Nest and sbx.Sandbox.Instance.
func newLsCmd(denHome *string, runner sbx.Runner) *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List live sandboxes",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}

			boxes, err := sbx.Ls(cmd.Context(), runner)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			// The creation records, read ONCE for the three things they serve:
			// the orphan line below, the "unreadable" warning right after this
			// block, and the two columns whose real value survives nowhere
			// else. Fail-open — an unreadable state/ leaves `den ls` doing
			// exactly what it did before records existed.
			manifests, brokenManifests, mErr := manifest.List(home)
			if mErr != nil {
				manifests = nil
			}

			// A file that failed to decode is NOT "no record" — see the
			// switch below — so it earns the same sentence `den doctor`
			// already prints for it, on stderr, rather than a second dialect
			// for the same state. unreadable is keyed by SANDBOX name, not
			// path: manifest.Path is the sole place a record's file name is
			// composed as "<sandbox>.yaml", so trimming the suffix recovers
			// exactly the string the switch below compares against b.Name.
			//
			// Named brokenManifests, not `broken`: nest.ListNests further down
			// returns its OWN broken list into a variable named `broken`, and
			// `:=` reuses an identifier already in scope — reusing this name
			// would have silently rebound this slice to nest.BrokenNest, a
			// different type, the moment that line was added.
			unreadable := make(map[string]bool, len(brokenManifests))
			for _, b := range brokenManifests {
				fmt.Fprintf(cmd.ErrOrStderr(), "creation record %s unreadable: %v — den leaves "+
					"it alone (it may belong to another version of den); delete it by hand once "+
					"its sandbox is gone\n", b.Path, b.Err)
				unreadable[strings.TrimSuffix(filepath.Base(b.Path), ".yaml")] = true
			}

			if len(boxes) == 0 {
				fmt.Fprintln(out, "no live sandbox")
				// The scan runs even here, and that is the whole point: "no
				// live sandbox, and four recorded worktrees still on disk" is
				// exactly the state worth reporting, and it is unreachable
				// from a return placed above it.
				reportOrphans(out, boxes, manifests)
				return nil
			}

			// Declared nests only MARK unknown sandboxes, they never filter
			// them: a live VM stays visible even if its nest was deleted from
			// ~/.den/nests. Broken nests and an unreadable nests/ root are
			// reported by name on stderr and never turn into an error here.
			nests, broken, err := nest.ListNests(home)
			for _, bn := range broken {
				fmt.Fprintf(cmd.ErrOrStderr(), "nest %s unreadable: %v\n", bn.Name, bn.Err)
			}
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "listing nests: %v\n", err)
			}
			declared := map[string]bool{}
			for _, n := range nests {
				declared[n.Name] = true
			}
			// Every installed source's nests fold in too, under the
			// FLATTENED reference: listSourceNests names them
			// "<source>:<name>", but a source sandbox never carries that
			// string — ":" is outside sbx's --name charset, so spawn
			// rewrote it to "-" (config.FlattenedSourceSeparator) before
			// `sbx create` ever saw it. Split with config.SplitSourceRef
			// and rejoin with the constant rather than a "-" literal: that
			// is the one guarantee that this side of the rewrite and
			// FlattenSandboxComponent's can never disagree.
			//
			// Fail-open, same contract as listSourceNests itself: a
			// missing sources/, or one source whose own nests/ cannot be
			// read, contributes nothing here — the sandbox keeps the mark
			// it carried before sources existed, never an error.
			srcNests, _ := listSourceNests(home)
			for _, n := range srcNests {
				src, name := config.SplitSourceRef(n.Name)
				declared[src+config.FlattenedSourceSeparator+name] = true
			}

			recorded := make(map[string]manifest.Manifest, len(manifests))
			for _, m := range manifests {
				recorded[m.Sandbox] = m
			}

			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tNEST\tINSTANCE\tWORKTREE\tSTATUS\tWORKSPACES")
			for _, b := range boxes {
				nestName := b.Nest()
				// The MARK is decided on the sandbox-derived name, before the
				// record replaces the displayed string: b.Nest() is exactly
				// the string `sbx ls` reports, and `declared` now holds both
				// the names of the files under <denHome>/nests AND every
				// installed source's nests under their flattened form (see
				// the loop above) — so a nest declared in a source is no
				// longer reported as undeclared. A source sandbox whose nest
				// is gone — the source removed, or the file deleted — still
				// earns the mark, same as a deleted local nest always has.
				undeclared := !declared[nestName]
				// The record, when there is one, is the ONLY place these two
				// strings survive: flattening rewrote the branch on its way
				// into the sandbox name, and the ":" of a source reference is
				// not in sbx's --name charset.
				//
				// The fallback on component 2 applies ONLY when there is
				// truly no record. It was written when that component could
				// only be a flattened branch; under --as it is a label, and
				// printing it as a branch would name something that does not
				// exist in any repository. An UNREADABLE record is not "no
				// record" — den does not know its worktree, and "?" says so
				// instead of guessing that the label is a branch. A record
				// WITHOUT a worktree block means "no worktree", and renders
				// as such.
				instance := b.Instance()
				wt := ""
				m, hasRecord := recorded[b.Name]
				switch {
				case unreadable[b.Name]:
					wt = "?"
				case !hasRecord:
					wt = instance
				case m.Worktree != nil:
					wt = m.Worktree.Branch
				}
				if hasRecord && m.Nest.Ref != "" {
					nestName = m.Nest.Ref
				}
				if undeclared {
					nestName += " ?" // not declared in ~/.den/nests
				}
				if instance == "" {
					instance = "-"
				}
				if wt == "" {
					wt = "-"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\n",
					b.Name, nestName, instance, wt, b.Status, len(b.Workspaces))
			}
			if err := w.Flush(); err != nil {
				return err
			}
			reportOrphans(out, boxes, manifests)
			return nil
		},
	}
}

// reportOrphans names the sandboxes den recorded creating that no longer have
// a VM, and the directories they left behind.
//
// FAIL-OPEN, strictly: `den ls` is the command a user types when everything
// else is broken, and it must never fail over its own extra. A record that
// cannot be read is never in `manifests` — the caller's manifest.List sorts
// it into a separate list, warned about at the point that list is read, in
// the same words `den doctor` uses for the same file — so it cannot become an
// orphan line here, and this function stays about ONE thing: a record whose
// sandbox is gone.
//
// The comparison is free: this command already holds both the live list and
// the records.
func reportOrphans(out io.Writer, boxes []sbx.Sandbox, manifests []manifest.Manifest) {
	orphans := doctor.Orphans(doctor.LiveSandboxes{Known: true, Names: liveNames(boxes)}, manifests)
	if len(orphans) == 0 {
		return
	}
	fmt.Fprintln(out)
	for _, o := range orphans {
		if len(o.Worktrees) == 0 {
			// The record outlived its sandbox but den created nothing for it:
			// worth naming, since `den doctor --fix` will drop the record, but
			// there is no directory to point at.
			fmt.Fprintf(out, "orphan: %s — no live sandbox (nothing left on disk)\n", o.Sandbox)
			continue
		}
		fmt.Fprintf(out, "orphan: %s — no live sandbox, worktrees still on disk: %s\n",
			o.Sandbox, strings.Join(o.Worktrees, ", "))
	}
	fmt.Fprintln(out, "reclaim them with `den doctor --fix`")
}
