// Package deninit materializes a fresh den home from a template tree, for
// `den init`. It owns no I/O source of its own — see Run — so it is testable
// against a fake tree without touching the real examples/den-home.
package deninit

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/PillowPillow/den/internal/config"
)

// Run copies the tree in src to denHome, refusing if a config.yaml is
// already there.
//
// src is a PARAMETER, not a package-level embed.FS: the real one
// (den.ExampleDenHome, rooted to examples/den-home by the caller) lives at the
// module root, one level cli/ can import but this package's tests must not
// depend on — a test that owed its result to the real examples/ tree would
// stop catching a regression there the moment that tree changed to match
// whatever bug it was supposed to catch. A fstest.MapFS built in the test
// owes nothing to it either way.
//
// The refusal probes config.GlobalPath(denHome) — the file, not the
// directory — because a directory that merely exists is not evidence of a
// previous `den init`: a user who ran `mkdir ~/.den` themselves (to set
// permissions, or by habit) must not be turned away. It is also the ONLY
// check: no --force, no per-file "create what's missing" merge. The
// alternative was rejected because it resurrects files a user deliberately
// removed — delete nests/example.yaml and a later `den init` would recreate
// it, and a resurrected example nest is not inert: it shows up in `den nest
// ls` and fails `den doctor` on "repo not found".
func Run(denHome string, src fs.FS, out io.Writer) error {
	globalPath := config.GlobalPath(denHome)
	if _, err := os.Stat(globalPath); err == nil {
		return fmt.Errorf("already initialized: %s", globalPath)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("checking %s: %w", globalPath, err)
	}

	var files []string
	if err := fs.WalkDir(src, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, path)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("reading the embedded example: %w", err)
	}
	// Sorted rather than walk order: WalkDir already yields lexical order per
	// directory, but sorting the flat list makes the printed listing (and any
	// test asserting on it) independent of that implementation detail.
	sort.Strings(files)

	// No traversal guard on `rel` below. `src` being a PARAMETER does not
	// reopen this: fs.WalkDir only ever yields names src itself reports for
	// ".", and every real caller is a compile-time go:embed (rooted through
	// fs.Sub in internal/cli/init.go) — nothing upstream of Run reads a name
	// off the command line or a network request to build that tree. Under a
	// denHome already resolved to an absolute path by config.Home (paths.go),
	// these are FIXED relative names known at build time, unlike the
	// user-controlled sandboxName internal/agent/mixin.go must sanitize.
	for _, rel := range files {
		dest := filepath.Join(denHome, filepath.FromSlash(rel))
		// 0o755 dirs / 0o644 files: den's other lazy creators — spawn.go's
		// agent profile dir, worktree.go's trash dir — use the same pair for
		// content that, like this one, has no reason to be private. mixin.go's
		// 0o700/0o600 is for the mixin cache specifically and does not apply
		// here.
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", filepath.Dir(dest), err)
		}
		content, err := fs.ReadFile(src, rel)
		if err != nil {
			return fmt.Errorf("reading embedded %s: %w", rel, err)
		}
		if err := os.WriteFile(dest, content, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", dest, err)
		}
		fmt.Fprintf(out, "created %s\n", dest)
	}

	fmt.Fprintln(out, "\nnext steps:")
	fmt.Fprintf(out, "  1. edit %s to point at your own repo\n",
		filepath.Join(denHome, "nests", "example.yaml"))
	fmt.Fprintln(out, "  2. run `den doctor` to check the result")
	return nil
}
