// Package registry records what den V2 created per instance, so `den ls`,
// `den shell` and `den rm` replay it instead of re-deriving it from a config
// that may have moved since.
//
// Under state/v2/, beside v1's state/sandboxes/: a V2 den never reads, lists
// or overwrites a v1 manifest, and v1 never sees these files.
package registry

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/sbx"
	"gopkg.in/yaml.v3"
)

// Schema is refused on mismatch rather than decoded optimistically: a field
// whose meaning changed would send `den rm` at a worktree the writer never created.
const Schema = 2

// schemaBeforeTemplates is the last schema that did not record the template.
const schemaBeforeTemplates = 1

type Record struct {
	Schema     int       `yaml:"schema"`
	Instance   string    `yaml:"instance"`
	Nest       string    `yaml:"nest"`
	Template   string    `yaml:"template"`
	Tag        string    `yaml:"tag,omitempty"` // the sbx template tag; empty for an image
	ConfigFile string    `yaml:"config_file"`
	Project    string    `yaml:"project,omitempty"`
	RunDir     string    `yaml:"run_dir"`
	GenDir     string    `yaml:"gen_dir"`
	EnvFiles   []string  `yaml:"env_files"`
	EnvHashes  []string  `yaml:"env_hashes"` // sha256 per EnvFiles entry: `den rm` hands sbx only files nothing rewrote
	ConfigHash string    `yaml:"config_hash"`
	Start      string    `yaml:"start"`
	Worktree   *Worktree `yaml:"worktree,omitempty"`
	Repos      []Repo    `yaml:"repos"`
}

// Worktree keeps the branch as typed; Layout and Root are recorded so removal
// never re-reads the config they came from.
type Worktree struct {
	Branch string `yaml:"branch"`
	Dir    string `yaml:"dir"`
	Layout string `yaml:"layout"`
	Root   string `yaml:"root"`
}

// Repo.Worktree says whether den created Mount, hence whether `den rm` may reclaim it.
type Repo struct {
	Name     string `yaml:"name"`
	Repo     string `yaml:"repo"`
	Mount    string `yaml:"mount"`
	Target   string `yaml:"target"`
	Worktree bool   `yaml:"worktree"`
}

// Broken is a record List could not decode, returned rather than dropped: an
// unreadable record is not the same thing as no record.
type Broken struct {
	Path string
	Err  error
}

func Dir(denHome string) string {
	return filepath.Join(denHome, "state", "v2", "instances")
}

// Path validates the name first: filepath.Join cleans ".." into a real traversal.
func Path(denHome, instance string) (string, error) {
	if err := sbx.ValidateSandboxName(instance); err != nil {
		return "", err
	}
	return filepath.Join(Dir(denHome), instance+".yaml"), nil
}

// Write stamps Schema itself and renames a temp file into place, so a crash
// never leaves a truncated record that List would report as broken.
func Write(denHome string, r Record) error {
	path, err := Path(denHome, r.Instance)
	if err != nil {
		return fmt.Errorf("writing record: %w", err)
	}
	r.Schema = Schema
	content, err := yaml.Marshal(r)
	if err != nil {
		return fmt.Errorf("rendering the record of %s: %w", r.Instance, err)
	}
	return config.WriteFileAtomic(path, content)
}

// Read wraps fs.ErrNotExist through config.FileError, so "no record" can be told from "unreadable record".
func Read(denHome, instance string) (Record, error) {
	path, err := Path(denHome, instance)
	if err != nil {
		return Record{}, fmt.Errorf("reading record: %w", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return Record{}, fmt.Errorf("reading record %s: %w", path, &config.FileError{Err: err})
	}
	return decode(path, content)
}

func decode(path string, content []byte) (Record, error) {
	var r Record
	if err := config.DecodeYAMLStrict(path, content, &r); err != nil {
		return Record{}, err
	}
	if r.Schema == schemaBeforeTemplates {
		// The remedy rides in the error because `den rm` of a sibling surfaces it through checkSiblings.
		return Record{}, fmt.Errorf(
			"%s: schema %d was written by a den before templates; this den cannot act on it — "+
				"remove the VM with `sbx rm %s`, then delete %s",
			path, r.Schema, strings.TrimSuffix(filepath.Base(path), ".yaml"), path)
	}
	if r.Schema != Schema {
		return Record{}, fmt.Errorf(
			"%s: schema %d, but this den only understands schema %d — the file was written by "+
				"another version of den and is left untouched", path, r.Schema, Schema)
	}
	// The file name is what Path derives from the instance: a record copied or
	// renamed by hand would hand `den rm x` the run dir and VM of another instance.
	if named := strings.TrimSuffix(filepath.Base(path), ".yaml"); r.Instance != named {
		return Record{}, fmt.Errorf(
			"%s: records instance %q, but its file name says %q — den leaves it untouched; "+
				"rename the file to %s.yaml or remove it", path, r.Instance, named, r.Instance)
	}
	return r, nil
}

// Remove treats an absent file as success: `den rm` removes the record last.
func Remove(denHome, instance string) error {
	path, err := Path(denHome, instance)
	if err != nil {
		return fmt.Errorf("removing record: %w", err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing %s: %w", path, err)
	}
	return nil
}

// List tolerates a missing state dir: a den home that never created an instance has none.
func List(denHome string) ([]Record, []Broken, error) {
	entries, err := os.ReadDir(Dir(denHome))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", Dir(denHome), err)
	}
	var out []Record
	var broken []Broken
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(Dir(denHome), e.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			broken = append(broken, Broken{Path: path, Err: err})
			continue
		}
		r, err := decode(path, content)
		if err != nil {
			broken = append(broken, Broken{Path: path, Err: err})
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Instance < out[j].Instance })
	sort.Slice(broken, func(i, j int) bool { return broken[i].Path < broken[j].Path })
	return out, broken, nil
}
