package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// GlobalFile is the global den.yaml inside denHome.
func GlobalFile(denHome string) string { return filepath.Join(denHome, FileName) }

// LoadGlobal loads <denHome>/den.yaml; an absent file is an empty config, not an error.
func LoadGlobal(denHome string) (*Loaded, error) {
	path := GlobalFile(denHome)
	l, err := Load(path, true)
	if err != nil && errors.Is(err, fs.ErrNotExist) {
		abs, absErr := filepath.Abs(path)
		if absErr != nil {
			return nil, absErr
		}
		return &Loaded{Path: abs, Dir: filepath.Dir(abs), Global: true}, nil
	}
	return l, err
}

// Load reads path strictly, validates it and makes its relative paths absolute.
func Load(path string, global bool) (*Loaded, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", abs, &FileError{Err: err})
	}
	var f File
	if err := DecodeYAMLStrict(abs, raw, &f); err != nil {
		return nil, err
	}
	if problems := validate(&f, global); len(problems) > 0 {
		return nil, fmt.Errorf("%s: invalid %s:\n  %s", abs, FileName, strings.Join(problems, "\n  "))
	}
	l := &Loaded{Path: abs, Dir: filepath.Dir(abs), Global: global, File: f}
	if err := l.absolutize(); err != nil {
		return nil, fmt.Errorf("%s: %w", abs, err)
	}
	return l, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

var agentImagePattern = regexp.MustCompile(`^[^@\s]+@sha256:[0-9a-f]{64}$`)

// validate returns one line per problem, so a file is fixed in one pass.
func validate(f *File, global bool) []string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }

	if f.Schema != Schema {
		add("schema: must be %d, got %d", Schema, f.Schema)
	}
	if !global {
		const move = "is global-only — move it to the global den.yaml"
		if f.Worktrees != nil {
			add("worktrees: %s", move)
		}
		if f.Repos != nil {
			add("repos: %s", move)
		}
		if f.Env != nil {
			add("env: %s", move)
		}
		if f.Network.Allow != nil {
			add("network: %s", move)
		}
		if f.Profiles != nil {
			add("profiles: %s", move)
		}
		if f.Agents != nil {
			add("agents: %s", move)
		}
	}
	for _, name := range sortedKeys(f.Agents) {
		if name != "claude" {
			add("agents.%s: unknown agent — this den knows `claude` only", name)
			continue
		}
		if img := f.Agents[name].Image; !agentImagePattern.MatchString(img) {
			add("agents.%s.image: %q must pin a digest, <ref>@sha256:<64 hex> — `docker buildx imagetools inspect <ref>` prints it", name, img)
		}
	}
	for _, name := range sortedKeys(f.Profiles) {
		if err := ValidateName("profile", name); err != nil {
			add("profiles.%s: %v", name, err)
		}
	}
	if f.Profile != "" {
		if err := ValidateName("profile", f.Profile); err != nil {
			add("profile: %v", err)
		}
	}
	validateWorktrees("worktrees", f.Worktrees, add)

	for _, key := range sortedKeys(f.Templates) {
		e := f.Templates[key]
		tk := "templates." + key
		if err := ValidateName("template", key); err != nil {
			add("%s: %v", tk, err)
		}
		if e.Name != "" {
			if err := ValidateName("template", e.Name); err != nil {
				add("%s.name: %v", tk, err)
			}
		}
		if (e.Repo == "") == (e.Dir == "") {
			add("%s: set exactly one of repo and dir", tk)
		}
		if e.Dir != "" && e.Ref != "" {
			add("%s.ref: only a repo has a ref", tk)
		}
		if strings.HasPrefix(e.Repo, "-") {
			add(`%s.repo: must not start with "-"`, tk)
		}
	}

	for _, name := range sortedKeys(f.Bases) {
		b := f.Bases[name]
		key := "bases." + name
		if err := ValidateName("base", name); err != nil {
			add("%s: %v", key, err)
		}
		if b.Image == "" {
			add("%s.image: required — the template tag `den build` saves", key)
		}
	}

	for _, name := range sortedKeys(f.Nests) {
		n := f.Nests[name]
		key := "nests." + name
		if err := ValidateSandboxComponent("nest", name); err != nil {
			add("%s: %v", key, err)
		}
		if n.Base != "" {
			if err := ValidateName("base", n.Base); err != nil {
				add("%s.base: %v", key, err)
			}
		}
		if n.Profile != "" {
			if err := ValidateName("profile", n.Profile); err != nil {
				add("%s.profile: %v", key, err)
			}
		}
		if len(n.Repos) == 0 {
			add("%s.repos: a nest needs at least one repo", key)
		}
		for i, r := range n.Repos {
			if (r.Path == "") == (r.Key == "") {
				add("%s.repos[%d]: set exactly one of `path` and `key`", key, i)
			}
		}
		for i, p := range n.Ports {
			pk := fmt.Sprintf("%s.ports[%d]", key, i)
			if p.Sandbox < 1 || p.Sandbox > 65535 {
				add("%s.sandbox: %d is outside 1-65535", pk, p.Sandbox)
			}
			if p.Host < 0 || p.Host > 65535 {
				add("%s.host: %d is outside 0-65535 (0 = ephemeral)", pk, p.Host)
			}
			for j := 0; j < i; j++ {
				if n.Ports[j].Sandbox == p.Sandbox {
					add("%s.sandbox: %d already published by ports[%d]", pk, p.Sandbox, j)
					break
				}
			}
		}
		if err := validateStart(n.Start, global); err != nil {
			add("%s.start: %v", key, err)
		}
		validateWorktrees(key+".worktrees", n.Worktrees, add)
	}
	return out
}

func validateWorktrees(key string, w *Worktrees, add func(string, ...any)) {
	if w == nil {
		return
	}
	switch w.Layout {
	case "", "central", "per-repo":
	default:
		add("%s.layout: %q is not one of central, per-repo", key, w.Layout)
	}
}

func validateStart(start string, global bool) error {
	if start == "" {
		return nil
	}
	if start == "." {
		if global {
			return errors.New(`"." means the project folder, which the global file does not have — start with <repo>[/<path>] instead`)
		}
		return nil
	}
	if filepath.IsAbs(start) {
		return fmt.Errorf("%q must be \".\" or <repo>[/<relative path>], not absolute", start)
	}
	for _, part := range strings.Split(filepath.ToSlash(start), "/") {
		if part == ".." {
			return fmt.Errorf("%q must not contain \"..\"", start)
		}
	}
	return nil
}

// absolutize resolves every path-like field against the file's directory, after ~ expansion.
func (l *Loaded) absolutize() error {
	var firstErr error
	abs := func(p string) string {
		if p == "" {
			return p
		}
		e, err := ExpandPath(p)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return p
		}
		if filepath.IsAbs(e) {
			return filepath.Clean(e)
		}
		return filepath.Join(l.Dir, e)
	}
	paths := func(in []string) {
		for i, p := range in {
			in[i] = abs(p)
		}
	}
	kits := func(in []string) {
		for i, k := range in {
			if strings.HasPrefix(k, "./") || strings.HasPrefix(k, "../") || strings.HasPrefix(k, "/") || strings.HasPrefix(k, "~") {
				in[i] = abs(k)
			}
		}
	}

	for _, list := range l.File.Profiles {
		kits(list)
	}
	for name, b := range l.File.Bases {
		kits(b.Kits)
		paths(b.Build.Includes)
		paths(b.Build.Steps)
		l.File.Bases[name] = b
	}
	for key, e := range l.File.Templates {
		e.Dir = abs(e.Dir)
		l.File.Templates[key] = e
	}
	for name, n := range l.File.Nests {
		kits(n.Kits)
		paths(n.Layers)
		for i := range n.Repos {
			n.Repos[i].Path = abs(n.Repos[i].Path)
		}
		l.File.Nests[name] = n
	}
	return firstErr
}
