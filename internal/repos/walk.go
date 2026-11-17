package repos

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// Clone is a folder holding a `.git` folder, with the remotes its config declares.
type Clone struct {
	Path    string
	Remotes []Remote
}

// WalkOptions bounds a Walk. Skip holds absolute paths, compared after cleaning.
// Progress runs every 1000 folders visited and once at the end; it may be nil.
type WalkOptions struct {
	MaxDepth int
	Skip     []string
	Progress func(dirs, clones int)
}

const progressEvery = 1000

// skippedNames are never entered: dependency trees hold vendored clones that
// are not the user's, and a dot-folder is tooling state.
var skippedNames = map[string]bool{"node_modules": true, "vendor": true}

// Walk lists the clones under root, sorted by path. Only an unreadable root is
// an error; an unreadable folder below it is ignored.
func Walk(root string, o WalkOptions) ([]Clone, error) {
	root = filepath.Clean(root)
	if _, err := os.ReadDir(root); err != nil {
		return nil, err
	}
	w := &walker{opts: o, skip: map[string]bool{}}
	for _, s := range o.Skip {
		w.skip[filepath.Clean(s)] = true
	}
	w.visit(root, 0)
	if o.Progress != nil {
		o.Progress(w.dirs, len(w.clones))
	}
	sort.Slice(w.clones, func(i, j int) bool { return w.clones[i].Path < w.clones[j].Path })
	return w.clones, nil
}

type walker struct {
	opts   WalkOptions
	skip   map[string]bool
	dirs   int
	clones []Clone
}

func (w *walker) visit(dir string, depth int) {
	// A folder name a VM chose could carry an escape sequence into den scan's confirmation screen and
	// into every VM's repo map: nothing at or under such a path is listed.
	if strings.ContainsFunc(dir, unicode.IsControl) {
		return
	}
	w.dirs++
	if w.opts.Progress != nil && w.dirs%progressEvery == 0 {
		w.opts.Progress(w.dirs, len(w.clones))
	}
	if IsClone(dir) {
		// A clone whose config is unreadable or names no remote stays listed with
		// no Remotes: the caller decides what an unmatched clone means.
		remotes, _ := Remotes(dir)
		w.clones = append(w.clones, Clone{Path: dir, Remotes: remotes})
		return
	}
	if depth >= w.opts.MaxDepth {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		// DirEntry.Type() is the Lstat type: a symlink is never IsDir, so a link
		// to `/`, to an ancestor or to a clone is never entered.
		if !e.Type().IsDir() {
			continue
		}
		name := e.Name()
		if skippedNames[name] || name[0] == '.' {
			continue
		}
		child := filepath.Join(dir, name)
		if w.skip[child] {
			continue
		}
		w.visit(child, depth+1)
	}
}

// IsClone is true when dir holds a `.git` that is itself a folder: a file is a
// worktree's pointer and a symlink is not trusted.
func IsClone(dir string) bool {
	fi, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil && fi.IsDir()
}

// EnclosingClone returns the nearest strict ancestor of dir that is a clone.
func EnclosingClone(dir string) (string, bool) {
	dir = filepath.Clean(dir)
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		if IsClone(parent) {
			return parent, true
		}
		dir = parent
	}
}
