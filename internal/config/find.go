package config

import (
	"os"
	"path/filepath"
)

// FindProject walks from cwd up to the root and returns the first den.yaml that
// is not the global file; the global one lives in denHome and is no project.
func FindProject(cwd, denHome string) (path string, ok bool, err error) {
	global := filepath.Clean(GlobalFile(denHome))
	dir, err := filepath.Abs(cwd)
	if err != nil {
		return "", false, err
	}
	for {
		candidate := filepath.Join(dir, FileName)
		// An unreadable ancestor is not a reason to stop looking higher up.
		if st, statErr := os.Stat(candidate); candidate != global && statErr == nil && st.Mode().IsRegular() {
			return candidate, true, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false, nil
		}
		dir = parent
	}
}
