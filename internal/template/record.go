package template

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/PillowPillow/den/internal/config"
	"gopkg.in/yaml.v3"
)

// RecordSchema is refused on mismatch rather than decoded optimistically: a field whose
// meaning changed would make `den up` trust a build it cannot judge.
const RecordSchema = 1

const trustedSchema = 1

// Source says where a template came from: a git repository (at Ref) or a local directory.
type Source struct {
	Kind string `yaml:"kind"` // "repo" | "dir"
	URL  string `yaml:"url,omitempty"`
	Ref  string `yaml:"ref,omitempty"`
	Dir  string `yaml:"dir,omitempty"`
}

// From is the root image of the from chain, as the build VM reported it.
type From struct {
	Ref    string `yaml:"ref"`
	Digest string `yaml:"digest"`
}

// Record is what den built for one template, so `den up` can tell a stale image from a fresh one.
type Record struct {
	Schema         int       `yaml:"schema"`
	Template       string    `yaml:"template"`
	Tag            string    `yaml:"tag"`
	Source         Source    `yaml:"source"`
	Commit         string    `yaml:"commit,omitempty"`
	DefinitionHash string    `yaml:"definition_hash"`
	From           From      `yaml:"from"`
	BuiltAt        time.Time `yaml:"built_at"`
}

// SourceOf describes where the chosen template's files come from.
func SourceOf(c Choice) Source {
	if c.Entry.Repo != "" {
		return Source{Kind: "repo", URL: c.Entry.Repo, Ref: c.Entry.Ref}
	}
	return Source{Kind: "dir", Dir: c.Entry.Dir}
}

func stateDir(denHome string) string { return filepath.Join(denHome, "state", "v2") }

func recordPath(denHome, name string) string {
	return filepath.Join(stateDir(denHome), "templates", name+".yaml")
}

func validName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("template name %q cannot name a file under the den home", name)
	}
	return nil
}

// ReadRecord wraps fs.ErrNotExist through config.FileError, so "no record" can be told from "unreadable record".
func ReadRecord(denHome, name string) (Record, error) {
	if err := validName(name); err != nil {
		return Record{}, fmt.Errorf("reading template record: %w", err)
	}
	path := recordPath(denHome, name)
	content, err := os.ReadFile(path)
	if err != nil {
		return Record{}, fmt.Errorf("reading template record %s: %w", path, &config.FileError{Err: err})
	}
	var r Record
	if err := config.DecodeYAMLStrict(path, content, &r); err != nil {
		return Record{}, err
	}
	if r.Schema != RecordSchema {
		return Record{}, fmt.Errorf(
			"%s: schema %d, but this den only understands schema %d — the file was written by "+
				"another version of den and is left untouched", path, r.Schema, RecordSchema)
	}
	// A record copied or renamed by hand would make `den up x` trust the build of another template.
	if named := strings.TrimSuffix(filepath.Base(path), ".yaml"); r.Template != named {
		return Record{}, fmt.Errorf(
			"%s: records template %q, but its file name says %q — den leaves it untouched; "+
				"rename the file to %s.yaml or remove it", path, r.Template, named, r.Template)
	}
	return r, nil
}

// WriteRecord stamps RecordSchema itself and renames a temp file into place, so a crash never leaves a truncated record.
func WriteRecord(denHome string, r Record) error {
	if err := validName(r.Template); err != nil {
		return fmt.Errorf("writing template record: %w", err)
	}
	r.Schema = RecordSchema
	content, err := yaml.Marshal(r)
	if err != nil {
		return fmt.Errorf("rendering the record of template %s: %w", r.Template, err)
	}
	return writeAtomic(recordPath(denHome, r.Template), content)
}

func writeAtomic(path string, content []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".record-*.tmp")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

type trustedFile struct {
	Schema int      `yaml:"schema"`
	URLs   []string `yaml:"urls"`
}

func trustedPath(denHome string) string {
	return filepath.Join(stateDir(denHome), "template-sources.yaml")
}

func readTrusted(denHome string) (trustedFile, error) {
	path := trustedPath(denHome)
	content, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return trustedFile{Schema: trustedSchema}, nil
	}
	if err != nil {
		return trustedFile{}, fmt.Errorf("reading %s: %w", path, err)
	}
	var f trustedFile
	if err := config.DecodeYAMLStrict(path, content, &f); err != nil {
		return trustedFile{}, err
	}
	if f.Schema != trustedSchema {
		return trustedFile{}, fmt.Errorf("%s: schema %d, but this den only understands schema %d — the file is left untouched", path, f.Schema, trustedSchema)
	}
	return f, nil
}

// Trusted reports whether the user already confirmed cloning url.
func Trusted(denHome, url string) (bool, error) {
	f, err := readTrusted(denHome)
	if err != nil {
		return false, err
	}
	return slices.Contains(f.URLs, url), nil
}

// Trust remembers the user's confirmation for exactly this url.
func Trust(denHome, url string) error {
	f, err := readTrusted(denHome)
	if err != nil {
		return err
	}
	if slices.Contains(f.URLs, url) {
		return nil
	}
	f.URLs = append(f.URLs, url)
	content, err := yaml.Marshal(f)
	if err != nil {
		return fmt.Errorf("rendering %s: %w", trustedPath(denHome), err)
	}
	return writeAtomic(trustedPath(denHome), content)
}
