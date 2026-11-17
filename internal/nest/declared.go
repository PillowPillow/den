package nest

import (
	"sort"

	"github.com/PillowPillow/den/v2/internal/config"
	"github.com/PillowPillow/den/v2/internal/origin"
)

// Declared is one `repos:` key carrying an origin, with the file that declares it.
type Declared struct {
	Key, Origin, Identity, File string
}

// DeclaredOrigins lists the origin-carrying repo keys of the project file reached from cwd, of the files
// the index names, and of the global file. It reads the index without claiming anything: a scan report
// must not write nests.yaml. An indexed file that is missing or broken is skipped, since it is not the
// file the user is working from; a broken global or cwd project file is returned.
func DeclaredOrigins(denHome, cwd string) ([]Declared, error) {
	global, err := config.LoadGlobal(denHome)
	if err != nil {
		return nil, err
	}
	project, err := loadProject(denHome, cwd)
	if err != nil {
		return nil, err
	}
	ix, err := readIndex(denHome)
	if err != nil {
		return nil, err
	}
	files := []*config.Loaded{global, project}
	for _, n := range sortedNames(ix.Nests) {
		path := ix.Nests[n]
		if isLoaded(path, project, global) {
			continue
		}
		if l, err := config.Load(path, false); err == nil {
			files = append(files, l)
		}
	}
	seen := map[[2]string]bool{}
	var out []Declared
	for _, f := range files {
		if f == nil {
			continue
		}
		for key, d := range f.File.Repos {
			id := [2]string{f.Path, key}
			if d.Origin == "" || seen[id] {
				continue
			}
			identity, err := origin.Identity(d.Origin)
			if err != nil {
				continue
			}
			seen[id] = true
			out = append(out, Declared{Key: key, Origin: d.Origin, Identity: identity, File: f.Path})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Key < out[j].Key
	})
	return out, nil
}
