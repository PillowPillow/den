// Package source manages team source repositories: git clones under
// <denHome>/sources/<name>/ carrying the den-home partial layout (stacks/,
// lib/, kits/, nests/ — spec 2026-08-04). No parallel registry, same doctrine
// as the sandbox truth coming from `sbx ls`: an installed source IS a
// directory that is a git clone; the URL lives in its remote, the freshness
// in its FETCH_HEAD mtime. Git runs behind worktree.Git, injected — this
// package must stay testable against file:// remotes.
package source

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/worktree"
)

// Root is the SOLE definition of where sources live.
func Root(denHome string) string { return filepath.Join(denHome, "sources") }

// Dir is the SOLE definition of one source's clone directory. Every message
// that names the directory to fix must go through this (paths.go doctrine).
func Dir(denHome, name string) string { return filepath.Join(Root(denHome), name) }

// Locate resolves a reference to (root, source, bare name). A bare ref is a
// local object: root is the den home itself. The existence check happens HERE,
// once, because every caller (spawn, nest show, build) would otherwise fail
// later with a bare "not found" that never says `den source add` is the fix.
func Locate(denHome, ref string) (root, src, name string, err error) {
	src, name = config.SplitSourceRef(ref)
	if src == "" {
		return denHome, "", name, nil
	}
	dir, err := requireInstalled(denHome, src)
	if err != nil {
		return "", "", "", err
	}
	return dir, src, name, nil
}

// requireInstalled validates a source name and returns its checkout
// directory, or the ONE refusal den gives for a source that is not there.
//
// Extracted so RequireUsable (version.go) cannot answer that same question in
// a second dialect: a user who typed `corp:api` and a user running `den build
// corp:base` must be told the same thing, with the same remedy.
func requireInstalled(denHome, name string) (string, error) {
	if err := config.ValidateSourceName(name); err != nil {
		return "", err
	}
	dir := Dir(denHome, name)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return "", fmt.Errorf(
			"source %q: not installed — expected %s; run `den source add <url> --name %s`",
			name, dir, name)
	}
	return dir, nil
}

// StaleAfter is the age past which the spawn hints at `den source update`.
// 7 days (spec 2026-08-04 §4): long enough that a VPN-less week of work stays
// quiet, short enough that a drifting team repo gets noticed.
const StaleAfter = 7 * 24 * time.Hour

// LastFetch reports when the source last talked to its remote: FETCH_HEAD's
// mtime, falling back on HEAD's for a fresh clone (clone writes HEAD but not
// FETCH_HEAD). ok=false when neither exists — not a git repo.
func LastFetch(denHome, name string) (time.Time, bool) {
	for _, f := range []string{"FETCH_HEAD", "HEAD"} {
		if fi, err := os.Stat(filepath.Join(Dir(denHome, name), ".git", f)); err == nil {
			return fi.ModTime(), true
		}
	}
	return time.Time{}, false
}

// Stale is the spawn-hint verdict. A source without git metadata is NOT
// stale: it is broken, and that is `den source ls`'s finding, not a freshness
// hint's.
func Stale(denHome, name string, now time.Time) bool {
	last, ok := LastFetch(denHome, name)
	return ok && now.Sub(last) > StaleAfter
}

// Names lists installed sources by name only, sorted — os.ReadDir already
// returns entries sorted by filename, so nothing here re-sorts. A missing
// sources/ directory is an empty list, not an error, the same doctrine List
// documents above.
//
// This exists because List is not free: `den source ls` pays a `source.Lint`
// plus a `git remote get-url` and a `git rev-parse` PER installed source,
// work that exists to answer "what is installed and is it healthy". A bare
// `den source update` needs only the names to iterate over — source.Update
// lints the fetched tree itself — so routing it through List would lint
// (and shell out to git for) every source twice per run for no reason.
func Names(denHome string) ([]string, error) {
	entries, err := os.ReadDir(Root(denHome))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", Root(denHome), err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// Info is one installed source as `den source ls` shows it.
type Info struct {
	Name      string
	URL       string
	Head      string
	LastFetch time.Time
	LintErrs  []error
}

// List reads the sources directory. A missing directory is an empty list —
// a den that never added a source is not an error. Git failures inside ONE
// source (a half-deleted clone) surface in that source's fields rather than
// failing the listing: `den source ls` is the tool that SHOWS broken sources.
func List(ctx context.Context, git worktree.Git, denHome string) ([]Info, error) {
	entries, err := os.ReadDir(Root(denHome))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", Root(denHome), err)
	}
	var out []Info
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		dir := Dir(denHome, name)
		info := Info{Name: name, LintErrs: Lint(dir)}
		if raw, err := git.Run(ctx, dir, "remote", "get-url", "origin"); err == nil {
			info.URL = strings.TrimSpace(string(raw))
		} else {
			info.URL = fmt.Sprintf("(unreadable: %v)", err)
		}
		if raw, err := git.Run(ctx, dir, "rev-parse", "--short", "HEAD"); err == nil {
			info.Head = strings.TrimSpace(string(raw))
		}
		info.LastFetch, _ = LastFetch(denHome, name)
		out = append(out, info)
	}
	return out, nil
}
