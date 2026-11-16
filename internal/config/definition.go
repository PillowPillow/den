package config

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const DefinitionFileName = "den-template.yaml"

const DefinitionSchema = 1

// Definitions is one den-template.yaml: the templates a location offers.
type Definitions struct {
	Schema    int                   `yaml:"schema"`
	Templates map[string]Definition `yaml:"templates"`
}

// Definition is one template. From is a sibling template's name or an image.
type Definition struct {
	From    string   `yaml:"from"`
	Kits    []string `yaml:"kits"`
	Network Network  `yaml:"network"`
	Build   Build    `yaml:"build"`
}

// LoadedDefinitions is a den-template.yaml whose paths are absolute and confined to Root.
type LoadedDefinitions struct {
	Path string
	// Root is the directory of Path, symlinks resolved.
	Root string
	File Definitions
}

// IsSibling reports whether from names another template of the same file.
func (l *LoadedDefinitions) IsSibling(from string) bool {
	_, ok := l.File.Templates[from]
	return ok
}

// LoadDefinitions reads <root>/den-template.yaml strictly. Every kit path, include and step is made
// absolute and must stay under root: a location may only cite its own files.
func LoadDefinitions(root string) (*LoadedDefinitions, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	path := filepath.Join(abs, DefinitionFileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, &FileError{Err: err})
	}
	var f Definitions
	if err := DecodeYAMLStrict(path, raw, &f); err != nil {
		return nil, err
	}
	l := &LoadedDefinitions{Path: path, Root: abs, File: f}
	if f.Schema != DefinitionSchema {
		return nil, fmt.Errorf("%s: invalid %s:\n  schema: must be %d, got %d", path, DefinitionFileName, DefinitionSchema, f.Schema)
	}
	var problems []string
	for _, name := range sortedKeys(f.Templates) {
		d := f.Templates[name]
		field := "templates." + name
		for i, k := range d.Kits {
			if isKitReference(k) {
				continue
			}
			p, err := l.confine(k)
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s.kits[%d]: %v", field, i, err))
				continue
			}
			if err := l.confineKitLinks(p); err != nil {
				problems = append(problems, fmt.Sprintf("%s.kits[%d]: %v", field, i, err))
				continue
			}
			d.Kits[i] = p
		}
		for i, s := range d.Build.Includes {
			p, err := l.confine(s)
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s.build.includes[%d]: %v", field, i, err))
				continue
			}
			d.Build.Includes[i] = p
		}
		for i, s := range d.Build.Steps {
			p, err := l.confine(s)
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s.build.steps[%d]: %v", field, i, err))
				continue
			}
			d.Build.Steps[i] = p
		}
		if cycle := l.cycleFrom(name); cycle != nil {
			problems = append(problems, fmt.Sprintf("%s.from: cycle %s", field, strings.Join(cycle, " → ")))
		}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("%s: invalid %s:\n  %s", path, DefinitionFileName, strings.Join(problems, "\n  "))
	}
	return l, nil
}

// cycleFrom returns the chain name → … → name when following `from` among siblings comes back to name.
func (l *LoadedDefinitions) cycleFrom(name string) []string {
	chain := []string{name}
	seen := map[string]bool{name: true}
	for cur := l.File.Templates[name].From; l.IsSibling(cur); cur = l.File.Templates[cur].From {
		chain = append(chain, cur)
		if cur == name {
			return chain
		}
		if seen[cur] {
			// a cycle that does not contain name is reported on its own members
			return nil
		}
		seen[cur] = true
	}
	return nil
}

// confine makes a path written in the file absolute and refuses one that leaves Root, lexically or through a symlink.
func (l *LoadedDefinitions) confine(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") || filepath.IsAbs(p) {
		return "", fmt.Errorf("%q leaves %s — a location may only cite its own files", p, l.Root)
	}
	full := filepath.Join(l.Root, filepath.FromSlash(p))
	if !pathUnder(full, l.Root) {
		return "", fmt.Errorf("%q leaves %s — a location may only cite its own files", p, l.Root)
	}
	if resolved := resolveExisting(full, 0); !pathUnder(resolved, l.Root) {
		return "", fmt.Errorf("%q leaves %s through a symlink — a location may only cite its own files", p, l.Root)
	}
	return full, nil
}

// confineKitLinks refuses a link inside a kit that resolves outside Root: sbx reads a kit's files on
// the host, so a link a cloned repository planted there could hand the VM ~/.ssh. A link that stays
// under Root is kept, and a directory it points to is checked in turn.
func (l *LoadedDefinitions) confineKitLinks(kit string) error {
	start, err := filepath.EvalSymlinks(kit)
	if err != nil {
		// a missing kit is reported by the command that reads it
		return nil
	}
	seen := map[string]bool{}
	var walk func(dir string) error
	walk = func(dir string) error {
		if seen[dir] {
			return nil
		}
		seen[dir] = true
		return filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
			if err != nil || e.Type()&fs.ModeSymlink == 0 {
				return err
			}
			resolved := resolveExisting(p, 0)
			if !pathUnder(resolved, l.Root) {
				rel, _ := filepath.Rel(l.Root, p)
				target, _ := os.Readlink(p)
				return fmt.Errorf("link %s -> %s leaves %s — a kit may only link to files of its location", rel, target, l.Root)
			}
			if st, err := os.Stat(resolved); err == nil && st.IsDir() {
				return walk(resolved)
			}
			return nil
		})
	}
	return walk(start)
}

// isKitReference mirrors sbx.isRegistry (config cannot import sbx; keep the two in sync): a kit whose
// first `/` component is a registry host is a reference sbx resolves itself, anything else is a path.
func isKitReference(kit string) bool {
	first, _, hasSlash := strings.Cut(kit, "/")
	if !hasSlash || first == "." || first == ".." {
		return false
	}
	return first == "localhost" || strings.ContainsAny(first, ".:")
}

// pathUnder compares component-wise: /a/bc is not under /a/b.
func pathUnder(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolveExisting resolves every symlink on p's existing part and rejoins the missing tail, so a path through
// a dangling link or an in-root directory link to outside is judged by where it would land; a tail that is
// merely absent under an in-root directory still resolves in place and the build reports the missing file.
func resolveExisting(p string, depth int) string {
	if depth > 40 {
		return p
	}
	tail := ""
	for cur := p; ; cur = filepath.Dir(cur) {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(resolved, tail)
		}
		if st, err := os.Lstat(cur); err == nil && st.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(cur)
			if err != nil {
				return p
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(cur), target)
			}
			return resolveExisting(filepath.Join(target, tail), depth+1)
		}
		if filepath.Dir(cur) == cur {
			return p
		}
		tail = filepath.Join(filepath.Base(cur), tail)
	}
}
