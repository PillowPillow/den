package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// FileName is the config file's name, in the global den home and in a project folder.
const FileName = "den.yaml"

// Schema is the only den.yaml schema version this den reads.
const Schema = 1

// File is one den.yaml. Worktrees, Env, Network, Profiles and AutoTrust at top level exist in the
// global file only; a project file carrying them is refused. Repos is accepted in both.
type File struct {
	Schema    int                 `yaml:"schema"`
	Worktrees *Worktrees          `yaml:"worktrees"`
	Repos     map[string]RepoDecl `yaml:"repos"`
	Env       map[string]string   `yaml:"env"`
	Network   Network             `yaml:"network"`
	Profiles  map[string][]string `yaml:"profiles"`
	Profile   string              `yaml:"profile"`
	AutoTrust *bool               `yaml:"auto_trust"`
	Nests     map[string]Nest     `yaml:"nests"`

	Templates       map[string]TemplateEntry `yaml:"templates"`
	DefaultTemplate string                   `yaml:"default_template"`
}

// RepoDecl is one `repos:` entry: a key's git identity (Origin), its folder on this machine
// (Path), or both. A bare scalar is a Path.
type RepoDecl struct {
	Origin string `yaml:"origin"`
	Path   string `yaml:"path"`
}

// UnmarshalYAML reads a scalar as Path and a mapping field by field, refusing an unknown field:
// a typo such as `url:` must not silently leave the entry empty.
func (d *RepoDecl) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Tag == "!!null" {
			return nil
		}
		var p string
		if err := n.Decode(&p); err != nil {
			return err
		}
		d.Path = p
		return nil
	case yaml.MappingNode:
		// A custom unmarshaler bypasses yaml's duplicate-key check: without this, the last value wins.
		seen := map[string]bool{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			var dst *string
			switch k.Value {
			case "origin":
				dst = &d.Origin
			case "path":
				dst = &d.Path
			default:
				return fmt.Errorf("line %d: field %s not found in a repos entry (known: origin, path)", k.Line, k.Value)
			}
			if seen[k.Value] {
				return fmt.Errorf("line %d: field %s is set twice in a repos entry", k.Line, k.Value)
			}
			seen[k.Value] = true
			if err := v.Decode(dst); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("line %d: a repos entry is a path or an {origin, path} mapping", n.Line)
}

// TemplateEntry locates one template's source: a git Repo (at Ref) or a local Dir, exactly one.
type TemplateEntry struct {
	Repo string `yaml:"repo"`
	Dir  string `yaml:"dir"`
	Name string `yaml:"name"`
	Ref  string `yaml:"ref"`
}

// TemplateName is the name the entry answers to: Name, or the key it is declared under.
func (e TemplateEntry) TemplateName(key string) string {
	if e.Name != "" {
		return e.Name
	}
	return key
}

type Worktrees struct {
	Layout string `yaml:"layout"`
	Root   string `yaml:"root"`
}

type Network struct {
	Allow []string `yaml:"allow"`
}

type Build struct {
	Includes []string `yaml:"includes"`
	Steps    []string `yaml:"steps"`
	Network  Network  `yaml:"network"`
}

type Nest struct {
	Template  string            `yaml:"template"`
	Profile   string            `yaml:"profile"`
	Repos     []RepoRef         `yaml:"repos"`
	Kits      []string          `yaml:"kits"`
	Network   Network           `yaml:"network"`
	Env       map[string]string `yaml:"env"`
	Ports     []Port            `yaml:"ports"`
	Start     string            `yaml:"start"`
	Worktrees *Worktrees        `yaml:"worktrees"`
	Layers    []string          `yaml:"layers"`
	AutoTrust *bool             `yaml:"auto_trust"`
}

// RepoRef names a repo by host path or by a key of the global repos map; exactly one is set.
type RepoRef struct {
	Path     string `yaml:"path"`
	Key      string `yaml:"key"`
	Optional bool   `yaml:"optional"`
}

// Port publishes Sandbox on Host; Host 0 means an ephemeral host port.
type Port struct {
	Name    string `yaml:"name"`
	Sandbox int    `yaml:"sandbox"`
	Host    int    `yaml:"host"`
}

// Loaded is a validated den.yaml whose paths are absolute.
type Loaded struct {
	Path   string
	Dir    string
	Global bool
	File   File
}
