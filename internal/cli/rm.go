package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	// What every OTHER record mounts, read once for the whole loop rather than
	// once per repo: `--as` (PR #68) lets two sandboxes share a worktree —
	// spawning `api -w feature/123` then `api -w feature/123 --as reco` reuses
	// the same directory, because worktree.Ensure is idempotent on the same
	// branch, and BOTH records end up naming it with Worktree: true. Without
	// this check `den rm api.reco` would move that directory to the trash
	// while `api.feature-123` is still running and mounting it.
	guard := newMountGuard(home, m.Sandbox)

	// Directories kept because a record den could NOT read might be the one
	// still mounting them — collected here, reported once after the loop.
	var stranded []string

	for _, r := range m.Repos {
		// The one bit that matters: den only ever reclaims what it created.
		// A repo mounted as-is is the user's own working directory.
		if !r.Worktree {
			continue
		}
		// A live sibling still naming this Mount outranks reclaiming it: the
		// record of THIS sandbox is still removed at the end of this
		// function regardless — den is removing THIS sandbox, not disowning
		// a directory another one holds.
		holder, unknown := guard.holderOf(r.Mount)
		if holder != "" {
			fmt.Fprintf(out, "worktree kept: %s is also mounted by sandbox %s\n", r.Mount, holder)
			continue
		}
		// No record den could read names this Mount — but a record it could
		// NOT read may name anything, including this one. An unknown sharer
		// therefore holds back every directory no readable record accounts
		// for: den leaves them on disk rather than moving a live sandbox's
		// workspace to the trash on a guess. Leaving a directory is
		// recoverable; trashing a running VM's workspace is not.
		//
		// The kept mounts are named after the loop, never counted: a user who
		// has to arbitrate between a directory and a live VM needs the paths.
		// A hand-edited record claiming a worktree with no Mount at all names
		// no directory to keep, so it contributes nothing to say.
		if unknown {
			if r.Mount != "" {
				stranded = append(stranded, r.Mount)
			}
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
	if len(stranded) > 0 {
		guard.nameUnknownSharers(out, stranded)
		// The record SURVIVES here, and this is where it is decided. The
		// review that ordered this scan first said the record still goes,
		// because `den rm` must never refuse (doctrine T13/T16) — but a
		// removal is not a refusal, and the rule two branches above already
		// governs this exact state: the record is the ONLY trace of the
		// directories still on disk, and deleting it strands them for good.
		// Nothing is reclaimed here, so this is a skipped reclaim like any
		// other. The sandbox is still destroyed, and `den rm` still refuses
		// nothing; the leftover record is a state `den ls` and `den doctor`
		// already exist to make addressable, and it is exactly what `den
		// doctor --fix` needs to finish the job on its next run.
		// The file is named because the user has to find it to act on it — but
		// only when there IS a name: manifest.Path refuses a sandbox name den
		// would never have written, and empty parentheses would say less than
		// nothing.
		where := ""
		if path, err := manifest.Path(home, m.Sandbox); err == nil {
			where = " (" + path + ")"
		}
		fmt.Fprintf(out, "the record of %s is kept too%s: `den doctor` reports these "+
			"worktrees, `den doctor --fix` reclaims them once den can read every record\n",
			m.Sandbox, where)
		return nil
	}
	// Removed only now, when everything it listed is reclaimed.
	return manifest.Remove(home, m.Sandbox)
}

// mountGuard answers the ONE question both reclaim paths have to answer the
// same way: who else still mounts this directory? Its verdict is shared —
// cleanFromManifest asks about a RECORDED mount, cleanWorktreesLegacy about a
// DERIVED one — so the two branches of `den rm` can never disagree about what
// den may move. What each of them PRINTS afterwards is its own business: the
// legacy path has no record to keep, so the sentence about what survives the
// run differs, and only that sentence does.
type mountGuard struct {
	// holders maps a mount to the sandbox naming it. Only mounts a record
	// den could ENUMERATE appear here.
	holders map[string]string
	// unreadable holds the records den could not read at all — the unknown
	// sharers, whose mounts nobody can enumerate.
	unreadable []manifest.Broken
}

// holderOf reports who still holds mount. holder names a sandbox whose record
// names it. unknown says no readable record does, while at least one record den
// could not read AT ALL exists and may name anything, this directory included.
//
// The two are ordered, never combined: a named holder is a fact, an unknown
// sharer is a possibility, and the caller has a different thing to say about
// each.
func (g mountGuard) holderOf(mount string) (holder string, unknown bool) {
	if h := g.holders[mount]; h != "" {
		return h, false
	}
	return "", len(g.unreadable) > 0
}

// nameUnknownSharers prints the records den could not read and the directories
// it left alone because of them. Shared by both reclaim paths so a hold-back
// reads identically wherever it happened.
//
// kept is passed rather than accumulated here: cleanFromManifest holds back
// recorded mounts and cleanWorktreesLegacy derived ones, and only the caller
// knows which of its directories the guard actually stopped.
func (g mountGuard) nameUnknownSharers(out io.Writer, kept []string) {
	for _, b := range g.unreadable {
		// The wording `den ls` and `den doctor` already print for this
		// exact state, on purpose: one dialect for "den left a record
		// alone", not a second one per command.
		fmt.Fprintf(out, "creation record %s unreadable: %v — den leaves it alone (it may "+
			"belong to another version of den); delete it by hand once its sandbox is gone\n",
			b.Path, b.Err)
	}
	for _, mount := range kept {
		fmt.Fprintf(out, "worktree kept: %s — an unreadable record may name it, "+
			"and den does not guess\n", mount)
	}
}

// newMountGuard maps every mount named by a record OTHER than self to the
// sandbox naming it, and collects separately the records den could not read at
// all.
//
// A holder's own Worktree bit is not checked: the danger is a live VM still
// mounting the directory, and that is true whether the other record believes
// it created that mount or merely mounted it as-is — a coincidence this narrow
// is not worth trusting either way.
//
// A List error is swallowed on purpose, not surfaced: den refuses rather than
// normalizing in silence everywhere else (spec §2), but `den rm` itself must
// NEVER refuse or hang over a records directory it merely could not enumerate
// (doctrine T13/T16) — the guard is simply unavailable for that one run, and
// reclaim proceeds exactly as it did before the guard existed.
//
// The BROKEN half is consulted too, and that is the point of this function: a
// sandbox whose record den refused to decode is live all the same, and the
// most common such record is a NEWER den's — refused on `schema` alone, and
// otherwise perfectly good YAML. Read for its mounts alone through the one
// deliberately lax reader (manifest.LaxMounts), it protects its worktrees like
// any other. Its SANDBOX name is not read from the file: manifest.Path is the
// sole place a record's file name is composed as "<sandbox>.yaml", so the
// basename recovers it — the same trim `den ls` does for the same reason.
//
// An empty Mount is never a key: two records describing no mount must not
// collide on "" and keep each other's nothing.
func newMountGuard(home, self string) mountGuard {
	others, broken, _ := manifest.List(home)

	// First naming wins, and both lists are sorted (manifest.List), so the
	// holder announced to the user is stable from one run to the next.
	holders := make(map[string]string)
	claim := func(mount, sandbox string) {
		if mount == "" {
			return
		}
		// An unnamed claimant is not stored, and the reason is the "seen"
		// test right below: holderOf reads an empty holder as "nobody named
		// this mount", so storing one would both fail to protect the
		// directory AND mask the next record's real claim on it — first
		// naming wins. Skipping protects nothing by itself; it only keeps a
		// nameless entry from silencing a named one. den never writes such a
		// record (manifest.Path refuses an empty sandbox name), so this is a
		// hand-edited file; the one shape of it den can recognize by name is
		// escalated to an unknown sharer below.
		if sandbox == "" {
			return
		}
		if _, seen := holders[mount]; !seen {
			holders[mount] = sandbox
		}
	}
	for _, o := range others {
		if o.Sandbox == self {
			continue
		}
		for _, r := range o.Repos {
			claim(r.Mount, o.Sandbox)
		}
	}

	var unreadable []manifest.Broken
	for _, b := range broken {
		// Self, skipped here as above, and it is not a detail: the legacy
		// reclaim path runs precisely when den could NOT read this sandbox's
		// own record, so that file is sitting in `broken` under this very
		// name. Counting it would make every such `den rm` hold back its own
		// worktrees forever, over a record den is about to leave behind
		// anyway. den is removing THIS sandbox; its own record never speaks
		// for a third party.
		sandbox := strings.TrimSuffix(filepath.Base(b.Path), ".yaml")
		if sandbox == self {
			continue
		}
		// manifest.List admits ANY entry ending in ".yaml", including a file
		// named exactly ".yaml", whose trim leaves no sandbox name at all.
		// Such a file names nobody, so it can hold no mount under a name the
		// user could act on — and it is not a record den wrote. It is counted
		// as an unknown sharer rather than read: the cautious path exists for
		// exactly the files den cannot account for, and the alternative (a
		// nameless claim) is the entry claim refuses to store above.
		if sandbox == "" {
			unreadable = append(unreadable, b)
			continue
		}
		mounts, err := manifest.LaxMounts(b.Path)
		if err != nil {
			// b, not the lax error: b.Err is what `den ls` and `den doctor`
			// print for this file, and the lax reader's own failure adds no
			// information the user can act on.
			unreadable = append(unreadable, b)
			continue
		}
		for _, mount := range mounts {
			claim(mount, sandbox)
		}
	}
	return mountGuard{holders: holders, unreadable: unreadable}
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
//
// `wt`, name component 2, is READ AS a flattened branch below — a directory
// name under WorktreeRoot (or a `.den/<wt>` suffix, per-repo layout) is
// derived from it. That reading stopped being universally true the day `--as`
// shipped: component 2 can now be an arbitrary instance LABEL that never
// named a worktree at all (`den spawn api --as reco`, no `-w`). This function
// only ever runs when there is NO creation record (see cleanWorktrees above)
// — a `--as` sandbox always has one, written before `sbx create` — so the
// only way to reach here with a label in `wt` is a record deleted by hand.
// That is rare enough, and this path is best-effort by contract already, so
// guessing a directory that turns out not to exist costs nothing worktree.Remove
// doesn't already tolerate — while REFUSING here would strand a live VM the
// user asked to destroy (doctrine T13/T16). Guessing wrong is recoverable;
// refusing is not.
//
// It consults the SAME mountGuard as the record path, for the same reason `--as`
// created: two sandboxes of one nest can share a worktree, and this branch would
// otherwise move a live sibling's workspace to the trash — the guard exists on
// one branch and the data loss on the other is the same data loss. A hold-back
// is announced on `out`, not on warnW, deliberately: it is the outcome of the
// run rather than a resolution that degraded, and cleanFromManifest already
// prints it there. The same event must not land on two different streams
// depending on which branch reclaimed.
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

	// Read ONCE for the whole loop, like the record path: every repo of this
	// nest is checked against the same set of records.
	//
	// self is this sandbox's own name, and skipping it matters more here than
	// there: this function also runs when den could not DECODE this sandbox's
	// record, and that file is then sitting among the broken ones.
	//
	// The guard is keyed on the mounts records NAME, while the path looked up
	// below is derived from today's configuration — the very drift this whole
	// path is best-effort about. A `worktree_root` moved since the spawn makes
	// the derivation miss, and the guard miss with it. Neither side is
	// normalized here: the same keys serve the record path, on records that
	// need no repair.
	guard := newMountGuard(home, sandboxName)

	// Directories left alone because a record den could NOT read might be the
	// one still mounting them — collected here, reported once after the loop.
	var stranded []string

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
		// The directory this repo's worktree would be at, derived exactly as
		// worktree.Remove derives it below — same worktree.Path, same
		// arguments. It is computed here for the guard alone and NOT passed as
		// Target.WorktreePath: that field means "the directory as recorded at
		// creation", and this branch is precisely the caller that has no
		// record.
		mount := worktree.Path(gl.WorktreeLayout, gl.WorktreeRoot, wt, path)
		// The guard is skipped for a directory that DEFINITIVELY does not
		// exist, and that is what keeps this path's tolerance intact. A path
		// nothing exists at cannot be a live sandbox's workspace, so nothing a
		// sharer holds slips through; and skipping worktree.Remove on it would
		// skip the stale registration `prune` and the locked-registration
		// re-check it does for a directory already gone. The derivation above
		// is a guess by contract: a guess that lands nowhere costs nothing
		// today, and holding back a directory that is not on disk would start
		// charging for it — with a message naming a path the user cannot even
		// go look at.
		//
		// ErrNotExist alone, never "Stat failed": an unreadable parent answers
		// "den cannot tell", and reading that as "nothing is there" would drop
		// the guard on a directory a live sibling may well be mounting. Any
		// other failure keeps the check, and worktree.Remove — which stats
		// again and refuses what it cannot attribute — has the last word.
		if _, err := os.Stat(mount); !os.IsNotExist(err) {
			// The same two verdicts as the record path, in the same order:
			// a named holder is a fact and outranks an unreadable file's
			// possibility. den is removing THIS sandbox, not disowning a
			// directory another one holds.
			holder, unknown := guard.holderOf(mount)
			if holder != "" {
				fmt.Fprintf(out, "worktree kept: %s is also mounted by sandbox %s\n", mount, holder)
				continue
			}
			// No readable record names it, but a record den could not read may
			// name anything. Leaving a directory is recoverable; moving a
			// running VM's workspace to the trash on a guess is not.
			if unknown {
				stranded = append(stranded, mount)
				continue
			}
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
	if len(stranded) > 0 {
		guard.nameUnknownSharers(out, stranded)
		// The record path ends this state by saying the record of the sandbox
		// survives, and `den doctor --fix` will finish the job. Here that
		// sentence would be false: there is no record den can read for this
		// sandbox — that absence is why this function ran at all — and the VM
		// is destroyed a moment later. `den doctor` replays records, so nothing
		// will ever offer these directories again. Said once, rather than left
		// for the user to discover as a pile under worktree_root.
		//
		// "no record den can REPLAY", not "no record": on the undecodable
		// branch a file does survive under this sandbox's name, den said so on
		// the way in, and `den ls` and `den doctor` report it. What no longer
		// exists is anything that could name these directories again.
		fmt.Fprintf(out, "den has no record it can replay for %s: `den doctor --fix` will not "+
			"reclaim these worktrees, so remove them by hand once you know no live sandbox "+
			"mounts them\n", sandboxName)
	}
	return nil
}
