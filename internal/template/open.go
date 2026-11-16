package template

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/sbx"
)

// Resolved is a Choice `den up` can run: for a template, the record of its build and the definition
// read from the files that build used.
type Resolved struct {
	Choice   Choice
	Defs     *config.LoadedDefinitions // nil for an image
	Def      config.Definition         // zero for an image
	Record   *Record                   // nil for an image
	KitImage string                    // image: c.Image; template: sbx.ImageWithDigest(Record.From.Ref, Record.From.Digest)

	root string
}

// Root is the path as the choice names it, not Defs.Root, which has its symlinks resolved.
// It is where the definition's files live: the copy of a repo, the directory of a dir, "" for an image.
func (r *Resolved) Root() string {
	return r.root
}

func DescribeSource(s Source) string {
	if s.Kind == "repo" {
		if s.Ref != "" {
			return fmt.Sprintf("repo %s at %s", s.URL, s.Ref)
		}
		return "repo " + s.URL
	}
	return "dir " + s.Dir
}

func notBuilt(c Choice, why string) error {
	msg := fmt.Sprintf("template %q %s — run `den build %s`", c.Name(), why, c.Name())
	if d := c.DeclaredIn; d != nil && !d.Global {
		msg += " from " + d.Dir
	}
	return errors.New(msg)
}

// Open never builds and never reaches the network: a template with no usable build is refused with
// the `den build` remedy, so `den up` cannot start a VM from an image it cannot name by digest.
func Open(denHome string, c Choice) (*Resolved, error) {
	if c.IsImage() {
		return &Resolved{Choice: c, KitImage: c.Image}, nil
	}
	name := c.Name()
	rec, err := ReadRecord(denHome, name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, notBuilt(c, "is not built")
	}
	if err != nil {
		return nil, err
	}
	if built, want := rec.Source, SourceOf(c); built != want {
		return nil, fmt.Errorf("template %q was built from %s, but %s now declares %s — run `den build %s --force` to rebuild it from there",
			name, DescribeSource(built), c.Origin, DescribeSource(want), name)
	}
	root := c.Entry.Dir
	if c.Entry.Repo != "" {
		root = CopyDir(denHome, name)
		if _, err := os.Stat(root); err != nil {
			return nil, notBuilt(c, fmt.Sprintf("has no local copy at %s", root))
		}
	}
	defs, err := config.LoadDefinitions(root)
	if err != nil {
		return nil, err
	}
	def, ok := defs.File.Templates[name]
	if !ok {
		return nil, notBuilt(c, fmt.Sprintf("is not declared in %s", filepath.Join(defs.Root, config.DefinitionFileName)))
	}
	if rec.From.Digest == "" {
		return nil, notBuilt(c, fmt.Sprintf("was built without a digest for its image %s", rec.From.Ref))
	}
	return &Resolved{Choice: c, Defs: defs, Def: def, Record: &rec,
		KitImage: sbx.ImageWithDigest(rec.From.Ref, rec.From.Digest), root: root}, nil
}
