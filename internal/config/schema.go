package config

// FileName is the config file's name, in the global den home and in a project folder.
const FileName = "den.yaml"

// Schema is the only den.yaml schema version this den reads.
const Schema = 1

// File is one den.yaml. Worktrees, Repos, Env, Network, Profiles and Agents at top level exist in
// the global file only; a project file carrying them is refused.
type File struct {
	Schema    int                 `yaml:"schema"`
	Worktrees *Worktrees          `yaml:"worktrees"`
	Repos     map[string]string   `yaml:"repos"`
	Env       map[string]string   `yaml:"env"`
	Network   Network             `yaml:"network"`
	Profiles  map[string][]string `yaml:"profiles"`
	Agents    map[string]Agent    `yaml:"agents"`
	Profile   string              `yaml:"profile"`
	Bases     map[string]Base     `yaml:"bases"`
	Nests     map[string]Nest     `yaml:"nests"`

	Templates       map[string]TemplateEntry `yaml:"templates"`
	DefaultTemplate string                   `yaml:"default_template"`
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

// Agent overrides what den ships for one agent; Image replaces the digest-pinned image of the den-claude kit.
type Agent struct {
	Image string `yaml:"image"`
}

type Worktrees struct {
	Layout string `yaml:"layout"`
	Root   string `yaml:"root"`
}

type Network struct {
	Allow []string `yaml:"allow"`
}

type Base struct {
	Image   string   `yaml:"image"`
	From    string   `yaml:"from"`
	Kits    []string `yaml:"kits"`
	Network Network  `yaml:"network"`
	Build   Build    `yaml:"build"`
}

type Build struct {
	Includes []string `yaml:"includes"`
	Steps    []string `yaml:"steps"`
	Network  Network  `yaml:"network"`
}

type Nest struct {
	Base      string            `yaml:"base"`
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
