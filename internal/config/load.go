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
	"unicode"
	"unicode/utf8"

	"github.com/PillowPillow/den/v2/internal/origin"
	"gopkg.in/yaml.v3"
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
	if removed := removedKeys(raw); len(removed) > 0 {
		return nil, fmt.Errorf("%s: invalid %s:\n  %s", abs, FileName, strings.Join(removed, "\n  "))
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

// removedKeys reads raw leniently for the keys templates replaced, so the error names the replacement
// instead of the strict decoder's "unknown key". YAML it cannot parse is left to the strict decoder.
func removedKeys(raw []byte) []string {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil || len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	var out []string
	if mappingValue(root, "bases") != nil {
		out = append(out, "bases: removed — declare the template in a den-template.yaml and point at it with templates.<name>.dir or .repo")
	}
	if mappingValue(root, "agents") != nil {
		out = append(out, "agents: removed — set default_template to an image, e.g. claude-code-docker")
	}
	if nests := mappingValue(root, "nests"); nests != nil && nests.Kind == yaml.MappingNode {
		var names []string
		for i := 0; i+1 < len(nests.Content); i += 2 {
			if mappingValue(nests.Content[i+1], "base") != nil {
				names = append(names, nests.Content[i].Value)
			}
		}
		sort.Strings(names)
		for _, n := range names {
			out = append(out, fmt.Sprintf("nests.%s.base: renamed — use nests.%s.template", n, n))
		}
	}
	return out
}

// mappingValue returns the value node of key in mapping n, nil when n is not a mapping or lacks key.
func mappingValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

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
		if f.Env != nil {
			add("env: %s", move)
		}
		if f.Network.Allow != nil {
			add("network: %s", move)
		}
		if f.Profiles != nil {
			add("profiles: %s", move)
		}
		if f.AutoTrust != nil {
			add("auto_trust: %s", move)
		}
		if f.AddDirs != nil {
			add("add_dirs: %s", move)
		}
		if f.Sessions != nil {
			add("sessions: %s", move)
		}
		if f.Resources != nil {
			add("resources: %s", move)
		}
	}
	for _, key := range sortedKeys(f.Repos) {
		validateRepo(key, f.Repos[key], global, add)
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
	if global {
		validateResources("resources", f.Resources, add)
	}

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

	for _, name := range sortedKeys(f.Nests) {
		n := f.Nests[name]
		key := "nests." + name
		if err := ValidateSandboxComponent("nest", name); err != nil {
			add("%s: %v", key, err)
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
		validateResources(key+".resources", n.Resources, add)
	}
	return out
}

var memorySize = regexp.MustCompile(`^[1-9][0-9]*[mg]$`)

func validateResources(key string, r *Resources, add func(string, ...any)) {
	if r == nil {
		return
	}
	if r.CPUs != nil && *r.CPUs < 1 {
		add("%s.cpus: %d is not a CPU count — set 1 or more, or remove cpus to use every host CPU", key, *r.CPUs)
	}
	if r.Memory != "" && !memorySize.MatchString(r.Memory) {
		add("%s.memory: %q is not a memory size — write an integer then m (MiB) or g (GiB), e.g. 8g", key, r.Memory)
	}
}

// ValidateRepoKey refuses a repos: key that is not one folder name: it names a clone and ends up
// in a path and in the VM's repo map.
func ValidateRepoKey(key string) error {
	switch {
	case key == "":
		return errors.New("a repo key is empty")
	case strings.ContainsAny(key, `/\`):
		return fmt.Errorf("repo key %q cannot contain a path separator", key)
	case key == "." || key == "..":
		return fmt.Errorf("repo key %q is reserved", key)
	case strings.HasPrefix(key, "."):
		return fmt.Errorf("repo key %q cannot start with a dot", key)
	case !printableKey(key):
		// A key is a repo's name in /den-git/<key> and in the rules file of every VM: a newline
		// there would write lines of a project file's choosing into another VM's instructions.
		return fmt.Errorf("repo key %q holds a control, format or invalid character", key)
	}
	return nil
}

func printableKey(key string) bool {
	return utf8.ValidString(key) && !strings.ContainsFunc(key, func(r rune) bool { return !unicode.IsPrint(r) })
}

// validateRepo: the global file maps a key to a folder of this machine (origin is optional there);
// a project file travels between machines, so it carries either an origin or a project-relative path.
func validateRepo(key string, d RepoDecl, global bool, add func(string, ...any)) {
	rk := "repos." + key
	if !printableKey(key) {
		rk = fmt.Sprintf("repos.%q", key)
	}
	if err := ValidateRepoKey(key); err != nil {
		add("%s: %v", rk, err)
	}
	if d.Origin != "" {
		if _, err := origin.Identity(d.Origin); err != nil {
			add("%s.origin: %v", rk, err)
		}
	}
	switch {
	case global && d.Path == "":
		add("%s: set `path` (the global den.yaml maps a key to a folder on this machine; `origin` is optional)", rk)
	case !global && (d.Origin == "") == (d.Path == ""):
		add("%s: set exactly one of `origin` and `path`", rk)
	}
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
	for key, d := range l.File.Repos {
		d.Path = abs(d.Path)
		l.File.Repos[key] = d
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
