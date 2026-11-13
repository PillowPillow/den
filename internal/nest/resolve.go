// Package nest finds a V2 nest, claims its name, and resolves it against the global file.
package nest

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/PillowPillow/den/internal/config"
)

// Repo.Key is the global `repos:` key the path came from, "" for a path written in the nest.
type Repo struct {
	Name, Path string
	Optional   bool
	Key        string
}

// Resolved keeps GlobalFile and BaseFile so a refusal names the file that holds the value at fault.
type Resolved struct {
	Name        string
	File        *config.Loaded
	GlobalFile  string
	BaseName    string
	Base        config.Base
	BaseFile    *config.Loaded
	Profile     string
	ProfileKits []string
	Repos       []Repo
	Kits        []string
	Allow       []string
	Env         map[string]string
	Ports       []config.Port
	Start       string
	Layout      string
	Root        string
	Layers      []string
	Warnings    []string
}

func sortedNames[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func loadProject(denHome, cwd string) (*config.Loaded, error) {
	path, ok, err := config.FindProject(cwd, denHome)
	if err != nil || !ok {
		return nil, err
	}
	return config.Load(path, false)
}

// load reads the project file reached from cwd and the global file, claims their nests, and reads the index.
func load(denHome, cwd string) (project, global *config.Loaded, ix index, err error) {
	global, err = config.LoadGlobal(denHome)
	if err != nil {
		return nil, nil, index{}, err
	}
	project, err = loadProject(denHome, cwd)
	if err != nil {
		return nil, nil, index{}, err
	}
	for _, f := range []*config.Loaded{project, global} {
		if f != nil {
			if err := Claim(denHome, f); err != nil {
				return nil, nil, index{}, err
			}
		}
	}
	ix, err = readIndex(denHome)
	if err != nil {
		return nil, nil, index{}, err
	}
	return project, global, ix, nil
}

// Find returns the file declaring name and the global file.
func Find(denHome, cwd, name string) (file, global *config.Loaded, err error) {
	project, global, ix, err := load(denHome, cwd)
	if err != nil {
		return nil, nil, err
	}
	if project != nil {
		if _, ok := project.File.Nests[name]; ok {
			return project, global, nil
		}
	}
	if path, ok := ix.Nests[name]; ok && declares(path, name) && !isLoaded(path, project, global) {
		l, err := config.Load(path, false)
		if err != nil {
			return nil, nil, err
		}
		if _, ok := l.File.Nests[name]; ok {
			return l, global, nil
		}
	}
	if _, ok := global.File.Nests[name]; ok {
		return global, global, nil
	}
	return nil, nil, fmt.Errorf("unknown nest %q; %s", name, visible(project, global, ix))
}

// Visible describes the nests den can see from cwd, worded as Find's refusal of an unknown name lists them.
func Visible(denHome, cwd string) (string, error) {
	project, global, ix, err := load(denHome, cwd)
	if err != nil {
		return "", err
	}
	return visible(project, global, ix), nil
}

func visible(project, global *config.Loaded, ix index) string {
	known := []*config.Loaded{project, global}
	var unreadable []string
	for _, n := range sortedNames(ix.Nests) {
		path := ix.Nests[n]
		if !declares(path, n) || isLoaded(path, project, global) {
			continue
		}
		l, err := config.Load(path, false)
		if err != nil {
			unreadable = append(unreadable, fmt.Sprintf("%s (%s)", path, shortReason(path, err)))
			continue
		}
		known = append(known, l)
	}
	var names, files []string
	for _, f := range known {
		if f == nil {
			continue
		}
		names = append(names, sortedNames(f.File.Nests)...)
		if len(f.File.Nests) > 0 {
			files = append(files, f.Path)
		}
	}
	names, files = dedupe(names), dedupe(files)
	suffix := ""
	if len(unreadable) > 0 {
		suffix = "; unreadable: " + strings.Join(unreadable, ", ")
	}
	sort.Strings(names)
	if len(names) == 0 {
		return fmt.Sprintf("no nest is declared in %s%s", global.Path, suffix)
	}
	return fmt.Sprintf("nests den can see: %s (from %s)%s", strings.Join(names, ", "), strings.Join(files, ", "), suffix)
}

// FindBase looks name up in the project file reached from cwd, then in the global file.
func FindBase(denHome, cwd, name string) (config.Base, *config.Loaded, error) {
	project, err := loadProject(denHome, cwd)
	if err != nil {
		return config.Base{}, nil, err
	}
	global, err := config.LoadGlobal(denHome)
	if err != nil {
		return config.Base{}, nil, err
	}
	var tried []string
	for _, f := range []*config.Loaded{project, global} {
		if f == nil {
			continue
		}
		if b, ok := f.File.Bases[name]; ok {
			return b, f, nil
		}
		tried = append(tried, f.Path)
	}
	return config.Base{}, nil, fmt.Errorf("unknown base %q; looked in %s", name, strings.Join(tried, " and "))
}

func appendUnique(dst []string, in ...string) []string {
	for _, s := range in {
		dup := false
		for _, d := range dst {
			if d == s {
				dup = true
				break
			}
		}
		if !dup {
			dst = append(dst, s)
		}
	}
	return dst
}

// absPath expands ~ and anchors a relative path at dir.
func absPath(p, dir string) (string, error) {
	e, err := config.ExpandPath(p)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(e) {
		return filepath.Clean(e), nil
	}
	return filepath.Join(dir, e), nil
}

// Resolve merges global, base and nest into what an instance prepares from.
func Resolve(denHome string, global, file *config.Loaded, name string) (*Resolved, error) {
	n, ok := file.File.Nests[name]
	if !ok {
		return nil, fmt.Errorf("%s: nest %q is not declared there", file.Path, name)
	}
	r := &Resolved{Name: name, File: file, GlobalFile: global.Path, BaseName: n.Base, Profile: n.Profile,
		Start: n.Start, Ports: n.Ports, Layers: n.Layers}

	base, ok := file.File.Bases[n.Base]
	r.BaseFile = file
	if !ok {
		base, ok = global.File.Bases[n.Base]
		r.BaseFile = global
	}
	if !ok {
		where := file.Path
		if file != global {
			where += " nor " + global.Path
		}
		return nil, fmt.Errorf("nest %q: base %q is declared neither in %s; add it to one of them", name, n.Base, where)
	}
	r.Base = base

	seen := map[string]bool{}
	for _, ref := range n.Repos {
		path := ref.Path
		if ref.Key != "" {
			raw, ok := global.File.Repos[ref.Key]
			if !ok {
				if ref.Optional {
					r.Warnings = append(r.Warnings, fmt.Sprintf("optional repo key %q is not in repos: of %s; dropped", ref.Key, global.Path))
					continue
				}
				return nil, fmt.Errorf("nest %q: repo key %q is not in repos: of %s; add it there", name, ref.Key, global.Path)
			}
			var err error
			if path, err = absPath(raw, global.Dir); err != nil {
				return nil, fmt.Errorf("%s: repos.%s: %w", global.Path, ref.Key, err)
			}
		}
		repo := Repo{Name: filepath.Base(path), Path: path, Optional: ref.Optional, Key: ref.Key}
		if seen[repo.Name] {
			return nil, fmt.Errorf("nest %q in %s: two repos are named %q; the /den-git/<name> mount and `start:` need unique names", name, file.Path, repo.Name)
		}
		seen[repo.Name] = true
		r.Repos = append(r.Repos, repo)
	}

	if r.Profile == "" {
		r.Profile = file.File.Profile
	}
	if r.Profile != "" {
		kits, ok := global.File.Profiles[r.Profile]
		if ok {
			r.ProfileKits = kits
		} else {
			r.Warnings = append(r.Warnings, fmt.Sprintf("profile %q is not defined in profiles: of %s; no profile kits", r.Profile, global.Path))
		}
	}

	r.Kits = appendUnique(appendUnique(appendUnique(nil, base.Kits...), n.Kits...), r.ProfileKits...)
	r.Allow = appendUnique(appendUnique(appendUnique(nil, global.File.Network.Allow...), base.Network.Allow...), n.Network.Allow...)
	r.Env = map[string]string{}
	for k, v := range global.File.Env {
		r.Env[k] = v
	}
	for k, v := range n.Env {
		r.Env[k] = v
	}

	r.Layout, r.Root = "central", filepath.Join(denHome, "wt")
	for _, src := range []struct {
		w   *config.Worktrees
		dir string
	}{{global.File.Worktrees, global.Dir}, {n.Worktrees, file.Dir}} {
		if src.w == nil {
			continue
		}
		if src.w.Layout != "" {
			r.Layout = src.w.Layout
		}
		if src.w.Root != "" {
			p, err := absPath(src.w.Root, src.dir)
			if err != nil {
				return nil, fmt.Errorf("%s: worktrees.root: %w", src.dir, err)
			}
			r.Root = p
		}
	}
	return r, nil
}

func isLoaded(path string, loaded ...*config.Loaded) bool {
	for _, l := range loaded {
		if l != nil && l.Path == filepath.Clean(path) {
			return true
		}
	}
	return false
}

func dedupe(in []string) []string {
	sort.Strings(in)
	return appendUnique(nil, in...)
}

// shortReason keeps the first line of a load error; the path is already named beside it.
func shortReason(path string, err error) string {
	msg := strings.TrimPrefix(err.Error(), path+": ")
	if i := strings.Index(msg, "\n"); i >= 0 {
		msg = msg[:i]
	}
	return msg
}
