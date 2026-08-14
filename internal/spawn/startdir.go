package spawn

import (
	"path/filepath"
	"strings"

	"github.com/PillowPillow/den/internal/worktree"
)

// StartDir decides the directory the attached shell — or the command of `den
// exec` — starts in. It is a PURE judge: cwd is a parameter, read with
// os.Getwd() at the caller's edge and handed in, the same doctrine already
// written where `den spawn scratch .` resolves its positionals. Nothing here
// touches the world except symlink resolution, which is a read of paths the
// caller already named.
//
// Precedence, in order:
//
//  1. override — `--workdir`, a host path, "I know better", passed through
//     VERBATIM: no Clean, no symlink resolution. Unchanged semantics means
//     unchanged, and a caller who names a path den rewrites has not been
//     obeyed.
//  2. the DEEPEST mounted workspace containing cwd ⇒ the answer is cwd itself.
//     This is the whole point of the function: den already knew where the user
//     was standing, and used to throw it away.
//  3. mounts[0] — what Sandbox.Workdir() answered alone until now.
//  4. "" — no `-w` at all, the VM decides. A VM that mounts nothing gets no
//     invented directory.
//
// WHY cwd IS A VALID VM PATH, with no translation layer: sbx mounts a
// workspace at the SAME absolute path inside the VM (measured, spec §14.1
// A11, 2026-07-29). That property is already load-bearing — Sandbox.Workdir()
// returns a host path and hands it straight to `sbx exec -w`. So a host cwd
// under a mounted workspace is, verbatim, a path the VM carries.
//
// ONE judge, called by all three decision sites (the create and attach
// branches of Spawn, and `den exec`). Same doctrine as spawn.Enter being the
// only builder of an `sbx exec` argv, and internal/lint being the single
// checkout validator: two copies of a rule are two places for it to drift.
//
// NO MATCH IS SILENT. Spawning from outside every mount is ordinary — from
// ~, from another project — and a warning there would fire on the common
// case, which is how a warning stops being read.
//
// CONSIDERED AND REJECTED:
//
//   - a `start_dir:` key in the config cascade. `--workdir` already covers "I
//     know better", and a per-nest default for something the shell's own cwd
//     answers is a setting the user must keep true forever.
//   - `--no-cwd`. Same objection: `--workdir <mount>` expresses it with a path
//     instead of a negation.
//
// NOT COVERED, deliberately: the `-w` worktree rewrite. With a worktree den
// mounts the WORKTREE, not the source repo, so a cwd inside the source repo
// matches no mount and falls back to rule 3 — the worktree root, which is the
// right answer at the repo root and only wrong for a subdirectory. The reverse
// mapping exists (manifest.Repo.Repo is the host source, .Mount is what was
// mounted) but reading the record here would cost `den exec` a reader it does
// not have, for a case that is one `cd` away.
func StartDir(override, cwd string, mounts []string) string {
	if override != "" {
		return override
	}
	// An unreadable cwd (rule 2 skipped entirely) is not an error here: the
	// caller has already decided whether it was worth refusing over. Never
	// prefix-match against "" — every absolute path is "under" it.
	if cwd != "" {
		if dir := startUnderMount(cwd, mounts); dir != "" {
			return dir
		}
	}
	if len(mounts) == 0 {
		return ""
	}
	// mounts[0] canonicalized the way the two callers already did it: `first`
	// Cleans, Sandbox.Workdir strips the `:ro` mount option. Both, here, so
	// the fallback is identical whichever door called — a `mounts:` entry can
	// be first only on a nest that declares no repo, but the suffix is a mount
	// option in every position, never part of the path.
	//
	// An empty first entry stays empty rather than becoming filepath.Clean("")
	// == ".", which would hand the VM a relative path meaning nothing there.
	head := strings.TrimSuffix(mounts[0], ":ro")
	if head == "" {
		return ""
	}
	return filepath.Clean(head)
}

// startUnderMount returns cwd expressed under the deepest mount that contains
// it, or "" when no mount does.
//
// TWO PASSES, lexical first. The lexical one is the truth: the mount is the
// string den handed `sbx create`, and the VM carries THAT path. Symlink
// resolution is the fallback for the case where the two sides simply spell the
// same directory differently — on darwin os.Getwd() answers under /private
// while the declared mount goes through /tmp or /var. A resolved match
// therefore returns filepath.Join(declared, rel), NOT the resolved cwd: the
// resolved path is a host fact, and handing it to `sbx exec -w` would name a
// directory the VM never mounted.
func startUnderMount(cwd string, mounts []string) string {
	if dir := deepestMatch(cwd, mounts, func(p string) string { return p }); dir != "" {
		return dir
	}
	return deepestMatch(cwd, mounts, worktree.ResolvePath)
}

// deepestMatch compares in the space canon maps into, and answers in the
// declared one.
//
// LONGEST PREFIX WINS, not the first match. Nested declarations are ordinary —
// `repos: {mono: ~/dev/mono, api: ~/dev/mono/packages/api}` — and first-match
// would land a cwd inside api in the parent, silently.
func deepestMatch(cwd string, mounts []string, canon func(string) string) string {
	canonCwd := canon(filepath.Clean(cwd))
	best, bestLen := "", -1
	for _, m := range mounts {
		// The `:ro` suffix is a mount option, not part of the path — same
		// treatment as Sandbox.Workdir. And Clean on the declared side is
		// load-bearing rather than cosmetic: sbx normalizes the workspaces it
		// echoes, lexically (measured 2026-08-10, v0.37.1, spec §14.0), while
		// a declared `repos:` entry is only ever tilde-expanded and never
		// cleaned (nest/repos.go:53-59). A trailing slash must not cost a
		// match.
		declared := strings.TrimSuffix(m, ":ro")
		if declared == "" {
			continue
		}
		declared = filepath.Clean(declared)
		rel, ok := relUnder(canon(declared), canonCwd)
		if !ok {
			continue
		}
		if n := len(canon(declared)); n > bestLen {
			best, bestLen = filepath.Join(declared, rel), n
		}
	}
	return best
}

// relUnder reports whether path sits under root — the root itself included —
// and returns the remainder.
//
// COMPONENT-AWARE, never a bare strings.HasPrefix: `/dev/api-v2` must not
// match a mount at `/dev/api`. Both arguments are expected Clean.
func relUnder(root, path string) (string, bool) {
	if root == path {
		return "", true // an exact match is not a special case: rel is empty
	}
	// TrimSuffix before appending the separator so a root of "/" builds "/"
	// and not "//" — the one root that already ends in the separator.
	prefix := strings.TrimSuffix(root, string(filepath.Separator)) + string(filepath.Separator)
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	return path[len(prefix):], true
}
