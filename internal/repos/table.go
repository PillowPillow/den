package repos

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/PillowPillow/den/v2/internal/config"
)

// TableSchema is refused on mismatch rather than decoded optimistically: a file written by another den is left untouched.
const TableSchema = 1

// Entry holds the clone path only. No URL field exists on purpose: the strict decode refuses one, so a
// credential embedded in a remote can never be copied into a den file.
type Entry struct {
	Path string `yaml:"path"`
}

// Table maps an origin identity (host/path) to the clone `den scan` found for it.
type Table struct {
	Schema int              `yaml:"schema"`
	Repos  map[string]Entry `yaml:"repos"`
}

func TablePath(denHome string) string {
	return filepath.Join(denHome, "state", "v2", "repos.yaml")
}

// ReadTable answers an empty table for a missing file: no scan yet is not an error.
func ReadTable(denHome string) (Table, error) {
	path := TablePath(denHome)
	content, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Table{Schema: TableSchema, Repos: map[string]Entry{}}, nil
	}
	// The table holds only what scans found, so a new scan rebuilds it.
	remedy := fmt.Sprintf("delete %s and run `den scan` on the folder that holds your clones (e.g. `den scan ~/dev`)", path)
	if err != nil {
		return Table{}, fmt.Errorf("reading %s: %w; %s", path, err, remedy)
	}
	var t Table
	if err := config.DecodeYAMLStrict(path, content, &t); err != nil {
		return Table{}, fmt.Errorf("%w; %s", err, remedy)
	}
	if t.Schema != TableSchema {
		return Table{}, fmt.Errorf(
			"%s: schema %d, but this den only understands schema %d — the file was written by "+
				"another version of den and is left untouched; run the den that wrote it, or %s",
			path, t.Schema, TableSchema, remedy)
	}
	if t.Repos == nil {
		t.Repos = map[string]Entry{}
	}
	return t, nil
}

// WriteTable stamps Schema itself.
func WriteTable(denHome string, t Table) error {
	t.Schema = TableSchema
	content, err := yaml.Marshal(t)
	if err != nil {
		return fmt.Errorf("rendering %s: %w", TablePath(denHome), err)
	}
	return config.WriteFileAtomic(TablePath(denHome), content)
}
