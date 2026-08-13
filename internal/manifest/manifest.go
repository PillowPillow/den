// Package manifest records what den ACTUALLY mounted when it created a
// sandbox, so no later reader has to re-derive it.
//
// Everything den does after a spawn — reclaiming worktrees at `den rm`,
// naming the branch in `den ls`, spotting a sandbox whose VM is gone — used
// to be deduced from today's configuration. That deduction is only right for
// as long as the configuration has not moved: a `repos:` line edited, a
// `worktree_root` relocated, a nest deleted, or a repo mounted from the
// command line (declared in no file at all) each make it aim somewhere else,
// silently. Creation is an event; this package is its trace.
//
// On the HOST, not in the VM (spec 2026-08-05 D1): the file has to be readable
// exactly when the VM is not — a sandbox that no longer boots, a stopped one,
// or one sbx has already lost. Worktrees are host artifacts anyway.
//
// Under state/, not cache/: spec §3 declares cache/ reconstructible, and a
// command-line mount is reconstructible from nothing. A future `den clean`
// emptying cache/ would erase the only trace of a worktree carrying
// uncommitted work. state/ is never purged automatically.
package manifest

import (
	"bytes"
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

// Schema is the version stamped into every manifest den writes. Read refuses
// anything else rather than decoding it optimistically: a field that changed
// MEANING between versions would send `worktree.Remove` at a directory the
// writer never created. Every reader treats that refusal as "no usable
// manifest" and falls back, so a newer den's file never blocks an older one.
const Schema = 1

// Origins of a repo, in the manifest's own vocabulary. They are recorded
// rather than recomputed because they are exactly what cannot be recovered
// later: OriginCommandLine names a mount that appears in no file on disk, and
// OriginKey names one whose path came from a personal mapping that may since
// have changed or vanished.
const (
	OriginKey         = "key"
	OriginPath        = "path"
	OriginCommandLine = "command-line"
)

// Manifest is one sandbox's creation record.
//
// Deliberately NO timestamp: den injects its clocks (Freshness, Policy), and
// a field here would thread a clock through all of Spawn for something no
// reader consults.
type Manifest struct {
	Schema  int    `yaml:"schema"`
	Sandbox string `yaml:"sandbox"`
	Nest    Nest   `yaml:"nest"`
	// A POINTER, so a spawn without -w renders no `worktree:` block at all
	// rather than a block of empty strings that reads like a worktree named "".
	Worktree *Worktree `yaml:"worktree,omitempty"`
	Repos    []Repo    `yaml:"repos"`
	GitDirs  []string  `yaml:"git_dirs,omitempty"`
}

// Nest records BOTH spellings, because they answer different questions: Ref is
// what the user typed (`corp:api`) and is the only form `den ls` can print
// back without lying, File is what was actually read and is what an "it
// changed since creation" comparison has to name.
type Nest struct {
	Ref  string `yaml:"ref"`
	File string `yaml:"file"`
}

// Worktree keeps the branch as TYPED next to the flattened component.
// Flattening is lossy — `-w feature/12` creates the sandbox `api.feat-12` —
// and the manifest is the only place the original survives once the spawn is
// over.
//
// Layout and Root are recorded even though Repo.Mount already carries the
// final path: worktree.Remove needs them for the trash fallback location and
// for the parent-directory cleanup, and re-reading them from config.yaml at
// rm time is the very dependency this file exists to sever.
type Worktree struct {
	Name   string `yaml:"name"`
	Branch string `yaml:"branch"`
	Layout string `yaml:"layout"`
	Root   string `yaml:"root"`
}

// Repo is one mounted repository.
//
// Mount is the path `sbx create` REALLY received; Repo is the repository it
// was derived from. Worktree says whether den created Mount — and therefore
// whether `den rm` may reclaim it. A repo mounted as-is (Worktree false) is
// the user's own working directory: den never touches it.
type Repo struct {
	Name     string `yaml:"name"`
	Origin   string `yaml:"origin"`
	Key      string `yaml:"key,omitempty"`
	Repo     string `yaml:"repo"`
	Mount    string `yaml:"mount"`
	Worktree bool   `yaml:"worktree"`
}

// Broken is a manifest file List could not decode. Named and returned rather
// than dropped: a caller that silently skipped it would report a sandbox as
// having no record when it has an unreadable one — two very different things
// for the user holding the leftover directories.
type Broken struct {
	Path string
	Err  error
}

// Dir and Path are the SOLE definition of where a manifest lives. Writing and
// reading must agree: had they composed the path separately and diverged, Read
// would forever return os.ErrNotExist and every reader would silently take its
// fallback path — a feature that is off, everywhere, with nothing failing.
// The same trap mixinDir/mixinPath documents, locked the same way, by
// TestReadRereadsWhatWriteWrote.
func Dir(denHome string) string {
	return filepath.Join(denHome, "state", "sandboxes")
}

// Path validates the name BEFORE composing a path with it. sbx.SplitName is
// deliberately total and validates nothing (it also serves sandboxes created
// outside den), and filepath.Join CLEANS a ".." into a real traversal instead
// of rejecting it — so `den rm` on a hostile name listed by `sbx ls` would
// otherwise read and delete a file outside state/. Defense in depth: Spawn
// already refuses these names upstream via sbx.SandboxName.
func Path(denHome, sandboxName string) (string, error) {
	if err := sbx.ValidateSandboxName(sandboxName); err != nil {
		return "", err
	}
	return filepath.Join(Dir(denHome), sandboxName+".yaml"), nil
}

// Write materializes the manifest. It stamps Schema itself, so no caller can
// write a file claiming a version this package does not produce.
//
// 0700/0600, like the mixin cache: the file lists every path den mounted, and
// nothing justifies making that readable by every account on the machine.
func Write(denHome string, m Manifest) error {
	path, err := Path(denHome, m.Sandbox)
	if err != nil {
		return fmt.Errorf("writing manifest: %w", err)
	}
	m.Schema = Schema
	content, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("rendering the manifest of %s: %w", m.Sandbox, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// Read decodes one manifest. STRICT (KnownFields), like every other decode in
// den: an unknown key is a load error, never a silence — the reason spec §12
// gives for the rule everywhere else applies here too, since a mistyped
// `worktre:` would leave rm with nothing to reclaim and no way to know.
//
// The error wraps os.ErrNotExist through config.FileError, so callers can tell
// "no manifest at all" (the legacy sandbox case, worth a mention) from
// "unreadable manifest" (worth a warning naming the file).
func Read(denHome, sandboxName string) (Manifest, error) {
	path, err := Path(denHome, sandboxName)
	if err != nil {
		return Manifest{}, fmt.Errorf("reading manifest: %w", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("reading manifest %s: %w", path, &config.FileError{Err: err})
	}
	m, err := decode(content)
	if err != nil {
		return Manifest{}, fmt.Errorf("reading manifest %s: %w", path, err)
	}
	return m, nil
}

func decode(content []byte) (Manifest, error) {
	dec := yaml.NewDecoder(bytes.NewReader(content))
	dec.KnownFields(true)
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, err
	}
	if m.Schema != Schema {
		return Manifest{}, fmt.Errorf(
			"schema %d, but this den only understands schema %d — the file was written by "+
				"another version of den", m.Schema, Schema)
	}
	return m, nil
}

// LaxMounts reads ONE record for its `repos[].mount` values alone, without
// KnownFields and without the schema check — the deliberate exception to the
// strict decoding rule the rest of den holds (spec §12). The only other
// non-strict reader is agent.ReadMixin, which rereads the mixin den itself
// generated under cache/; neither of the two loads configuration.
//
// It exists for the records Read and List REFUSE. `den rm` may only reclaim a
// worktree no other sandbox still mounts, and it answers that from the records
// alone (it probes no VM). A sibling den could not decode is therefore
// invisible to that guard, and the most common such file is the recoverable
// one: written by a NEWER den, perfectly good YAML, refused on `schema` only.
// Guessing wrong there moves a live sandbox's workspace to the trash, so this
// one field is read on its own terms rather than not at all.
//
// What it does NOT do: it never writes, never deletes, never repairs, and it
// never produces a Manifest. Nothing else in the file is consumed, so no field
// whose MEANING may have changed between versions can reach den's logic — the
// very risk the schema refusal exists to prevent.
//
// An error means "this file answers nothing": it will not parse, or it could
// not even be read (List reports that state as Broken too). A file that parses
// but names no mount is not an error — it genuinely names none. That includes
// a future den that renamed the key, which would parse here and yield no
// protection; nothing readable on disk can distinguish the two, and inventing
// a third state would only guess.
func LaxMounts(path string) ([]string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	// An anonymous type, declared here rather than beside Manifest: it is not a
	// second model of a record, it is one field of one file, and a named type
	// would invite a second reader to grow on it.
	var doc struct {
		Repos []struct {
			Mount string `yaml:"mount"`
		} `yaml:"repos"`
	}
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var mounts []string
	for _, r := range doc.Repos {
		if r.Mount != "" {
			mounts = append(mounts, r.Mount)
		}
	}
	return mounts, nil
}

// Remove deletes the manifest. An already-absent file is NOT an error: rm
// removes it after reclaiming what it listed, and failing there would refuse a
// `den rm` that did everything it was asked (doctrine T13/T16).
func Remove(denHome, sandboxName string) error {
	path, err := Path(denHome, sandboxName)
	if err != nil {
		return fmt.Errorf("removing manifest: %w", err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing %s: %w", path, err)
	}
	return nil
}

// List reads every manifest, for the orphan scan.
//
// A MISSING directory is not an error: a den home that has never spawned has
// no state/, and `den ls` must not report a problem over it.
//
// Results are sorted by sandbox name — os.ReadDir is already lexical, but the
// callers render them to a terminal and a golden cannot tolerate depending on
// that implementation detail.
func List(denHome string) ([]Manifest, []Broken, error) {
	entries, err := os.ReadDir(Dir(denHome))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", Dir(denHome), err)
	}
	var out []Manifest
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
		m, err := decode(content)
		if err != nil {
			broken = append(broken, Broken{Path: path, Err: err})
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Sandbox < out[j].Sandbox })
	sort.Slice(broken, func(i, j int) bool { return broken[i].Path < broken[j].Path })
	return out, broken, nil
}
