// Package repos reads what a clone on disk says about itself.
package repos

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/PillowPillow/den/v2/internal/origin"
)

// Remote is one recognized `url` of a `[remote "<Name>"]` section.
type Remote struct {
	Name     string
	Identity string
}

// maxConfigSize bounds a .git/config read; git's own files stay far below it.
const maxConfigSize = 1 << 20

// Remotes lists the remotes of <cloneDir>/.git/config in file order, one per
// recognized url line. A url origin.Identity refuses is skipped.
func Remotes(cloneDir string) ([]Remote, error) {
	// A hand-rolled read, not `git remote get-url`: the file is rewritable by a
	// VM, and git would apply insteadOf and follow includes to a path den never chose.
	data, err := readConfig(filepath.Join(cloneDir, ".git", "config"))
	if err != nil {
		return nil, err
	}
	var out []Remote
	remote := ""
	inRemote := false
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			end := strings.IndexByte(line, ']')
			if end < 0 {
				inRemote = false
				continue
			}
			remote, inRemote = remoteSection(line[1:end])
			line = strings.TrimSpace(line[end+1:])
			if line == "" || line[0] == '#' || line[0] == ';' {
				continue
			}
		}
		if !inRemote {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "url") {
			continue
		}
		id, err := origin.Identity(configValue(value))
		if err != nil {
			continue
		}
		out = append(out, Remote{Name: remote, Identity: id})
	}
	return out, nil
}

// readConfig refuses anything but a regular file of at most maxConfigSize bytes: a VM can make
// .git/config a FIFO or a link to /dev/zero, and den scan and den up would hang on it. The type is
// checked on the opened file, not before the open, so the VM cannot swap the file in between.
func readConfig(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|configOpenFlags, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxConfigSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxConfigSize {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, maxConfigSize)
	}
	return data, nil
}

// Valid reports whether the folder at path is a clone with a remote carrying
// identity. An absent or unreadable folder is simply not valid.
func Valid(path, identity string) bool {
	remotes, err := Remotes(path)
	if err != nil {
		return false
	}
	for _, r := range remotes {
		if r.Identity == identity {
			return true
		}
	}
	return false
}

// remoteSection parses a section header body: the section name is
// case-insensitive, the quoted subsection is not.
func remoteSection(header string) (string, bool) {
	name, sub, ok := strings.Cut(strings.TrimSpace(header), " ")
	if !ok || !strings.EqualFold(name, "remote") {
		return "", false
	}
	sub = strings.TrimSpace(sub)
	if len(sub) < 2 || sub[0] != '"' || sub[len(sub)-1] != '"' {
		return "", false
	}
	sub = sub[1 : len(sub)-1]
	sub = strings.ReplaceAll(sub, `\"`, `"`)
	sub = strings.ReplaceAll(sub, `\\`, `\`)
	return sub, true
}

// configValue strips optional double quotes, or else a trailing # / ; comment.
func configValue(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, `"`) {
		if end := strings.IndexByte(v[1:], '"'); end >= 0 {
			return v[1 : 1+end]
		}
		return v[1:]
	}
	if i := strings.IndexAny(v, "#;"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}
