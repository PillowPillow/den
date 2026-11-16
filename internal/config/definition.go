package config

import (
	"fmt"
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
	// A missing target keeps the lexical check only: the build reports the missing file.
	if resolved, err := filepath.EvalSymlinks(full); err == nil && !pathUnder(resolved, l.Root) {
		return "", fmt.Errorf("%q leaves %s through a symlink — a location may only cite its own files", p, l.Root)
	}
	return full, nil
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
