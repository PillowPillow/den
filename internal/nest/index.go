package nest

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/PillowPillow/den/v2/internal/config"
)

type index struct {
	Nests map[string]string `yaml:"nests"`
}

func indexPath(denHome string) string {
	return filepath.Join(denHome, "state", "v2", "nests.yaml")
}

func readIndex(denHome string) (index, error) {
	path := indexPath(denHome)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return index{Nests: map[string]string{}}, nil
	}
	if err != nil {
		return index{}, err
	}
	var ix index
	if err := config.DecodeYAMLStrict(path, raw, &ix); err != nil {
		return index{}, err
	}
	if ix.Nests == nil {
		ix.Nests = map[string]string{}
	}
	return ix, nil
}

func writeIndex(denHome string, ix index) error {
	path := indexPath(denHome)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw, err := yaml.Marshal(ix)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".nests-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// declares reads only the nest names of path, leniently: a file another den
// version wrote must not make the index refuse a claim.
func declares(path, name string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var f struct {
		Nests map[string]yaml.Node `yaml:"nests"`
	}
	if yaml.Unmarshal(raw, &f) != nil {
		return false
	}
	_, ok := f.Nests[name]
	return ok
}

// Claim records f as the declarer of each of its nests, refusing a name another live file already owns.
func Claim(denHome string, f *config.Loaded) error {
	ix, err := readIndex(denHome)
	if err != nil {
		return err
	}
	self := filepath.Clean(f.Path)
	changed := false
	for _, name := range sortedNames(f.File.Nests) {
		owner, ok := ix.Nests[name]
		if ok && filepath.Clean(owner) != self && declares(owner, name) {
			return fmt.Errorf("nest %q is declared by both %s and %s; rename one", name, owner, self)
		}
		if !ok || filepath.Clean(owner) != self {
			ix.Nests[name] = self
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return writeIndex(denHome, ix)
}
