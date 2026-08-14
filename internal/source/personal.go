package source

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"gopkg.in/yaml.v3"

	"github.com/PillowPillow/den/internal/config"
)

// PersonalSchema versions source-config/<name>.yaml. Independent of both
// ManifestSchema (the team contract's shape) and the installed source's
// functional version: this file's own shape changes when den changes, not
// when the source does.
const PersonalSchema = 1

// Personal is <denHome>/source-config/<name>.yaml: the durable, user-editable
// configuration of ONE installed source.
//
// It is deliberately small, and everything it does not hold is the point:
//
//   - no repository_roots — where a machine looked for repositories is an
//     answer to one execution, not a setting (spec §7.2);
//   - no selected nests — installing a source configures ALL its exports and
//     selects none (spec §2);
//   - no credential value, ever.
//
// Version is EXACT: not a range, not a channel. An ordinary command uses it
// without contacting the remote, and `den source update` is the only thing
// that changes it (spec §11.1).
//
// Repos is scoped to THIS source: the same key may map elsewhere in another
// source, which is exactly what the global config.yaml.repos mapping could
// never express.
type Personal struct {
	SchemaVersion int               `yaml:"schema_version"`
	Version       string            `yaml:"version"`
	Repos         map[string]string `yaml:"repos,omitempty"`
}

// SourceConfigDir is the SOLE definition of where personal source
// configuration lives. Separate from state/ on purpose: this directory is the
// user's to edit, state/ is den's to write.
func SourceConfigDir(denHome string) string { return filepath.Join(denHome, "source-config") }

// PersonalPath is the SOLE definition of one source's personal configuration
// file. Callers that only NAME the file (an error, a migration hint) may use
// it directly; the readers and writers below validate the name first, since
// only they compose a path they then open.
func PersonalPath(denHome, name string) string {
	return filepath.Join(SourceConfigDir(denHome), name+".yaml")
}

// LoadPersonal reads and strictly decodes one source's personal
// configuration.
//
// The absent case wraps os.ErrNotExist and is NOT a fault: it means this
// machine has not configured that source yet, and `den source configure` is
// the remedy — a caller must be able to tell it from an unreadable file.
func LoadPersonal(denHome, name string) (*Personal, error) {
	if err := config.ValidateSourceName(name); err != nil {
		return nil, err
	}
	p := PersonalPath(denHome, name)
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", p, &config.FileError{Err: err})
	}
	var out Personal
	if err := config.DecodeYAMLStrict(p, raw, &out); err != nil {
		return nil, err
	}
	if out.SchemaVersion != PersonalSchema {
		return nil, fmt.Errorf(
			"%s: schema_version: %d is unsupported — this den writes and reads %d",
			p, out.SchemaVersion, PersonalSchema)
	}
	if !isExactVersion(out.Version) {
		return nil, fmt.Errorf(
			"%s: version: %q is not an exact version — den compares this string with the "+
				"installed checkout's `metadata.version`, so a range or a channel would never "+
				"match; write <major>.<minor>.<patch>", p, out.Version)
	}
	for _, key := range slices.Sorted(maps.Keys(out.Repos)) {
		if out.Repos[key] == "" {
			return nil, fmt.Errorf(
				"%s: repos.%s: blank — this key is what a source nest's `key:` resolves to; "+
					"set a real path, or remove the entry", p, key)
		}
	}
	return &out, nil
}

// ExpandedRepos returns the mapping with "~" resolved, as a SEPARATE map.
//
// Separate is the whole contract: Personal keeps the paths as the user wrote
// them, because den rewrites this file (`den source configure` adds a
// discovered repository) and a round trip through an expanded value would
// silently replace the user's "~/Development/api" with an absolute path tied
// to one account. Resolution needs real paths; the file keeps the author's.
func (p *Personal) ExpandedRepos() (map[string]string, error) {
	if len(p.Repos) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(p.Repos))
	for _, key := range slices.Sorted(maps.Keys(p.Repos)) {
		expanded, err := config.ExpandPath(p.Repos[key])
		if err != nil {
			return nil, fmt.Errorf("repos.%s: %w", key, err)
		}
		out[key] = expanded
	}
	return out, nil
}

// WritePersonal replaces the file atomically, with private permissions. It
// stamps SchemaVersion itself so no caller can write a file claiming a
// version this den does not produce (same rule as manifest.Write).
func WritePersonal(denHome, name string, p Personal) error {
	if err := config.ValidateSourceName(name); err != nil {
		return err
	}
	p.SchemaVersion = PersonalSchema
	content, err := yaml.Marshal(p)
	if err != nil {
		return fmt.Errorf("rendering the configuration of source %q: %w", name, err)
	}
	return replacePrivateFile(PersonalPath(denHome, name), content)
}

// replacePrivateFile writes content where path is, atomically and privately:
// a temporary sibling, fsync'd and closed, then renamed over the target.
//
// The temporary file is a SIBLING, not a file in the system temp directory:
// rename is only atomic within one filesystem, and ~/.den can sit on a
// different one than /tmp — a cross-device rename fails outright, which would
// turn every write into a refusal.
//
// Sync before rename, and not for durability theatre: without it the rename
// can be visible while the content is not, which is precisely the state this
// function exists to make impossible — a receipt naming a version whose bytes
// never landed.
//
// A failure at any step leaves the PREVIOUS file untouched. That is what
// makes a killed `den init --source` recoverable: the personal configuration
// still names the version that really applies, and `den source configure`
// resumes from it.
func replacePrivateFile(path string, content []byte) error {
	dir := filepath.Dir(path)
	// 0700: this directory holds what den knows about the machine's sources.
	// Nothing here justifies being readable by every account on the box —
	// same reasoning as the mixin cache and the sandbox manifests.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("preparing the replacement of %s: %w", path, err)
	}
	// From here on every failure removes the temporary file: a leftover
	// dotfile beside the real one would be read by nothing and explain
	// nothing.
	defer os.Remove(tmp.Name())

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("securing the replacement of %s: %w", path, err)
	}
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("flushing %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing the replacement of %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}
