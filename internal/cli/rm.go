package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/manifest"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/worktree"
	"github.com/spf13/cobra"
)

// gitProbeTimeout bounds EACH call to worktree.Remove (one per repo). Remove
// chains several sequential git probes, and a repo on a dead network mount
// would hang them all, so `den rm` would never return.
//
// Package variable, not const: a test must be able to shrink it to check the
// deadline is really set without waiting 30s.
var gitProbeTimeout = 30 * time.Second

// newRmCmd destroys a sandbox. The agent profile — mounted from config_dir —
// is NEVER touched: that is the whole point of a RW config_dir, and wiping it
// would force the user to /login again after every removal.
func newRmCmd(denHome *string, runner sbx.Runner, g worktree.Git) *cobra.Command {
	var keepWorktrees, force bool

	cmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "Destroy a sandbox (the agent profile persists)",
		Args:  exactlyOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			// The SANDBOX name is the flattened reference: ":" is not in
			// sbx's `--name` charset, so a nest loaded from a source never
			// spawns under its prefixed name (spawn.go) — the live VM this
			// command must find and destroy is already "corp-api", not
			// "corp:api". sandboxNameOf splits any ".<worktree>" suffix off
			// before flattening, so "corp:api.feat12" reaches the live
			// "corp-api.feat12" rather than the "corp-api-feat12" a whole-
			// argument flatten produced and spawn never creates.
			name, err := sandboxNameOf(args[0])
			if err != nil {
				return err
			}
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}

			boxes, err := sbx.Ls(cmd.Context(), runner)
			if err != nil {
				return err
			}
			if sbx.Find(boxes, name) == nil {
				return fmt.Errorf("sandbox %q not found (live: %v)", name, liveNames(boxes))
			}

			out := cmd.OutOrStdout()

			// Worktrees first: if one is dirty we stop BEFORE destroying the
			// sandbox. The reverse would leave the user with no VM and an error
			// about a directory.
			if keepWorktrees {
				// The record survives with the directories. Without it the
				// worktrees would be unreclaimable by anything but hand — and
				// `den doctor` is precisely what will offer to finish the job.
				if path, err := manifest.Path(home, name); err == nil {
					if _, err := os.Stat(path); err == nil {
						fmt.Fprintf(out, "worktrees kept, and so is their record (%s): "+
							"`den doctor` will report them, `den doctor --fix` reclaims them\n", path)
					}
				}
			} else if err := cleanWorktrees(cmd.Context(), home, args[0], name, g, force, out, cmd.ErrOrStderr()); err != nil {
				return err
			}

			if _, err := runner.Run(cmd.Context(), "rm", "--force", name); err != nil {
				return err
			}
			fmt.Fprintf(out, "sandbox %s destroyed (the agent profile is kept)\n", name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&keepWorktrees, "keep-worktrees", false,
		"keep the worktrees den created")
	cmd.Flags().BoolVar(&force, "force", false,
		"remove worktrees even when they carry uncommitted changes")
	return cmd
}

// cleanWorktrees reclaims what den created for this sandbox.
//
// It REPLAYS the creation record (internal/manifest) rather than re-deriving
// it: the derivation assumed today's configuration still describes yesterday's
// spawn, and it aimed elsewhere the moment that stopped being true — a
// `worktree_root` moved, a key unmapped, a nest deleted, a `--without` at
// spawn, or a repo given on the command line that is declared in no file at
// all and was simply never reclaimed.
//
// Without a usable record it falls back on that derivation, saying so
// (cleanWorktreesLegacy). Never a refusal: a `den rm` that refuses leaves the
// user with a live VM they can no longer destroy (doctrine T13/T16).
func cleanWorktrees(ctx context.Context, home, ref, sandboxName string, g worktree.Git, force bool, out, warnW io.Writer) error {
	// Before the name is turned into a manifest path. sbx.Ls validates NOTHING
	// of what it reads, and manifest.Path refuses a hostile name for the same
	// reason the legacy body does — this check simply happens once, upstream of
	// both.
	if err := sbx.ValidateSandboxName(sandboxName); err != nil {
		return fmt.Errorf("cleaning up worktrees: %w", err)
	}
	_, wt := sbx.SplitName(sandboxName)
	m, err := manifest.Read(home, sandboxName)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// Legacy sandbox: created before den kept records, or outside den.
		// Mentioned, not warned about — it is an ordinary state, and this
		// path dies on its own as old sandboxes disappear.
		//
		// Said ONLY when the derivation is about to look for something: without
		// a worktree component den created no directory, so there is nothing
		// whose location could be stale, and announcing a fallback there would
		// report a problem to every user of every sandbox spawned without `-w`.
		if wt != "" {
			fmt.Fprintf(warnW, "sandbox %s has no creation record: falling back on the nest and "+
				"config.yaml to locate its worktrees, which is only accurate if neither changed "+
				"since the spawn\n", sandboxName)
		}
		return cleanWorktreesLegacy(ctx, home, ref, sandboxName, g, force, out, warnW)
	case err != nil:
		// The file is NOT deleted here, and that is deliberate: den could not
		// read it, so it cannot know it is worthless — a record written by a
		// NEWER den reaches this same branch (schema refusal), and deleting it
		// would destroy that den's only trace of a live sandbox. Hence the
		// remedy in the message rather than a silent removal.
		fmt.Fprintf(warnW, "%v — falling back on the nest and config.yaml to locate the "+
			"worktrees of %s; den leaves that file alone (it may belong to another version "+
			"of den), so delete it by hand once this sandbox is gone\n", err, sandboxName)
		return cleanWorktreesLegacy(ctx, home, ref, sandboxName, g, force, out, warnW)
	}
	return cleanFromManifest(ctx, home, m, g, force, out)
}

// cleanFromManifest reclaims exactly the directories den recorded creating.
// Shared with `den doctor --fix`, which reclaims the same set for a sandbox
// whose VM is already gone — one body, so the two can never disagree on what
// den is allowed to move.
func cleanFromManifest(ctx context.Context, home string, m manifest.Manifest, g worktree.Git, force bool, out io.Writer) error {
	for _, r := range m.Repos {
		// The one bit that matters: den only ever reclaims what it created.
		// A repo mounted as-is is the user's own working directory.
		if !r.Worktree {
			continue
		}
		// Layout, Root and the worktree NAME still come from the record, not
		// from config.yaml: worktree.Remove needs them for its trash fallback
		// and its parent-directory cleanup, and reading them from today's
		// config would reintroduce the drift the recorded Mount just removed.
		//
		// All three stay EMPTY on a record that claims a worktree without
		// describing one — only a hand-edited file can say that. Remove
		// tolerates it (the recorded Mount is what it acts on, the primary
		// trash needs only DenHome, and removeParentDir declines on an empty
		// worktree name), so den still reclaims rather than refusing over a
		// file the user can no longer fix without losing the paths it holds.
		var layout, root, wt string
		if m.Worktree != nil {
			layout, root, wt = m.Worktree.Layout, m.Worktree.Root, m.Worktree.Name
		}
		// One deadline PER repo, not one for the whole loop: a broken repo
		// must not eat the budget of the next ones.
		repoCtx, cancel := context.WithTimeout(ctx, gitProbeTimeout)
		dest, err := worktree.Remove(repoCtx, g, worktree.Target{
			DenHome:      home,
			Layout:       layout,
			Root:         root,
			Nest:         m.Sandbox,
			Worktree:     wt,
			RepoPath:     r.Repo,
			WorktreePath: r.Mount,
			Force:        force,
		})
		cancel()
		if err != nil {
			// The record SURVIVES a failed reclaim, deliberately: it is the
			// only trace of the directories still on disk, and deleting it
			// here would strand them permanently.
			return err
		}
		if dest != "" {
			fmt.Fprintf(out, "worktree moved to trash: %s\n", dest)
		}
	}
	// Removed only now, when everything it listed is reclaimed.
	return manifest.Remove(home, m.Sandbox)
}

// cleanWorktreesLegacy removes, through worktree.Remove, the worktrees den
// created for this sandbox — one per repo of the nest. Best-effort on RESOLUTION (a
// nest deleted from ~/.den/nests must not prevent destroying a live sandbox);
// strict on REMOVAL (a dirty worktree stops everything — see worktree.Remove).
//
// out carries the success messages, warnW the best-effort warnings: a script
// reading only stdout must not see a successful `den rm` hiding a warning.
//
// worktree.Remove never deletes a worktree, it moves it to den_home's trash and
// returns the path of the entry it created. That path is the ONLY trace the
// user gets of where their work went, so it is announced for every worktree
// actually moved.
func cleanWorktreesLegacy(ctx context.Context, home, ref, sandboxName string, g worktree.Git, force bool, out, warnW io.Writer) error {
	nestName, wt := sbx.SplitName(sandboxName)
	if wt == "" {
		return nil // no worktree: nothing to clean up
	}

	// The sandbox name is validated by cleanWorktrees, upstream of both this
	// body and the manifest path: a sandbox listed by `sbx ls` may have been
	// created outside den, with a name sbx accepts but den would refuse
	// ("api../../evade"), and such a name travels as-is to worktree.Path and
	// sends Remove outside worktree_root.

	// A command validates what it USES: here exactly WorktreeLayout and
	// WorktreeRoot, both read below to build worktree.Remove's Target.
	//
	// The validating LoadGlobal would be wrong here: it would fail `den rm` on
	// a bad `agents.claude.update` — unrelated to worktrees — and leave the
	// user with a live VM they can no longer destroy (doctrine T13/T16). But
	// validating nothing would be just as wrong: LoadGlobalUnvalidated only
	// defaults the EMPTY layout, so a `centrl` survives and den would clean up
	// somewhere else SILENTLY. Hence the explicit check on those two fields.
	gl, err := config.LoadGlobalUnvalidated(home)
	if err != nil {
		return err
	}
	if errs := gl.ValidateWorktree(); len(errs) > 0 {
		return fmt.Errorf("cleaning up worktrees: %w", config.ConfigError(home, errs))
	}
	// DECODED, not read literally: for a sandbox spawned from a source nest
	// the component is the flattened reference ("corp-api"), which names no
	// file under <denHome>/nests — so a bare LoadNest here always failed, and
	// cleanup of every worktree'd source sandbox degraded to the warning
	// below while the directories stayed on disk. nestOfSandbox holds the
	// decode, shared with `den ports`.
	n, err := nestOfSandbox(home, ref, sandboxName)
	if err != nil {
		// wt and gl.WorktreeRoot are both available here: without them the user
		// learns a worktree was abandoned, but not where to go find it.
		where := filepath.Join(gl.WorktreeRoot, wt)
		if gl.WorktreeLayout == "per-repo" {
			where = fmt.Sprintf("every repo of the nest, under <repo>/.den/%s", wt)
		}
		fmt.Fprintf(warnW, "nest %q unreadable: worktree %q could not be cleaned up "+
			"(expected under %s): %v\n", nestName, wt, where, err)
		return nil
	}

	for _, repo := range n.Repos {
		// The KEY is resolved here, through the same personal mapping spawn
		// used when it created the worktree — LoadNest leaves a `key:` entry's
		// Path empty on purpose (only nest.Resolve fills it), and a source
		// nest declares its repos by key, since that is what makes it
		// shareable at all.
		//
		// An EMPTY path is skipped rather than passed on, and that is a safety
		// rule, not tidiness: worktree.Path("central", root, wt, "") joins to
		// root/<wt> — the whole sandbox's worktree DIRECTORY instead of one
		// repo's subdirectory — and "per-repo" yields a RELATIVE ".den/<wt>"
		// resolved against whatever cwd den was launched from. den does not
		// move a directory it cannot attribute to a repo.
		path := repo.Path
		if repo.Key != "" {
			path = gl.Repos[repo.Key]
		}
		if path == "" {
			fmt.Fprintf(warnW, "nest %q: repo key %q is not mapped on this machine, so den cannot "+
				"locate its worktree %q — it is left on disk; map it under `repos:` in %s and re-run "+
				"`den rm`, or remove the directory by hand\n",
				n.Name, repo.Key, wt, config.GlobalPath(home))
			continue
		}
		// One deadline PER repo, not one for the whole loop: a broken repo must
		// not eat the budget of the next repos of the same nest.
		repoCtx, cancel := context.WithTimeout(ctx, gitProbeTimeout)
		dest, err := worktree.Remove(repoCtx, g, worktree.Target{
			DenHome:  home,
			Layout:   gl.WorktreeLayout,
			Root:     gl.WorktreeRoot,
			Nest:     sandboxName,
			Worktree: wt,
			RepoPath: path,
			Force:    force,
		})
		cancel()
		if err != nil {
			return err
		}
		if dest == "" {
			continue // the directory was already gone: nothing to announce
		}
		fmt.Fprintf(out, "worktree moved to trash: %s\n", dest)
	}
	return nil
}
