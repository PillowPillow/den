package selfupdate

import (
	"errors"
	"os"
	"testing"
)

// fakeResolver stands in for filepath.EvalSymlinks. A path the map does not
// name answers an error, exactly as EvalSymlinks does for one that does not
// exist — no test in this package touches a real symlink.
func fakeResolver(links map[string]string) func(string) (string, error) {
	return func(path string) (string, error) {
		if target, ok := links[path]; ok {
			return target, nil
		}
		return "", errors.New("no such file or directory")
	}
}

func TestClassify(t *testing.T) {
	// The macOS Caskroom path is the one MEASURED on 2026-08-18
	// (/opt/homebrew/bin/den -> /opt/homebrew/Caskroom/den/1.8.1/den); the
	// others are documented defaults, which is why HOMEBREW_PREFIX and
	// HOMEBREW_CELLAR are consulted first — see the spec §4.
	env := Env{Gopath: "/Users/dev/go", Home: "/Users/dev"}
	cases := []struct {
		name string
		path string
		env  Env
		// resolve is nil in every lexical case; the symlink cases below wire a
		// map so the test never touches a real filesystem.
		resolve func(string) (string, error)
		want    Method
	}{
		{"caskroom macos", "/opt/homebrew/Caskroom/den/1.8.1/den", env, nil, MethodHomebrew},
		{"cellar intel", "/usr/local/Cellar/den/1.8.1/bin/den", env, nil, MethodHomebrew},
		{"linuxbrew", "/home/linuxbrew/.linuxbrew/Caskroom/den/1.8.1/den", env, nil, MethodHomebrew},
		{"custom prefix via env", "/srv/brew/Caskroom/den/1.8.1/den",
			Env{HomebrewPrefix: "/srv/brew"}, nil, MethodHomebrew},
		{"custom cellar via env", "/srv/kegs/den/1.8.1/bin/den",
			Env{HomebrewCellar: "/srv/kegs"}, nil, MethodHomebrew},
		{"false positive guarded", "/Users/dev/MyCellar/den", env, nil, MethodArchive},
		{"gobin wins over gopath", "/opt/bin/den",
			Env{Gobin: "/opt/bin", Gopath: "/Users/dev/go"}, nil, MethodGoInstall},
		{"gopath bin", "/Users/dev/go/bin/den", env, nil, MethodGoInstall},
		// GOPATH is os.PathListSeparator-separated, not a single path — `go
		// env GOPATH` can answer a multi-entry list. A binary living under the
		// SECOND entry's bin dir must still classify MethodGoInstall; before
		// the fix, filepath.Join(env.Gopath, "bin") on the raw multi-entry
		// string produced a path that matched nothing.
		{"multi-entry gopath, second entry", "/Users/dev/gopath2/bin/den",
			Env{Gopath: "/Users/dev/gopath1" + string(os.PathListSeparator) + "/Users/dev/gopath2"},
			nil, MethodGoInstall},
		{"default go bin", "/Users/dev/go/bin/den", Env{Home: "/Users/dev"}, nil, MethodGoInstall},
		// `go env -w GOBIN=…` never reaches os.Getenv: the toolchain keeps it
		// in its own config file. Before goenv.go this classified
		// MethodArchive and den overwrote a binary the go toolchain manages.
		{"gobin from the go env file", "/Users/dev/bin/den",
			Env{GoenvGobin: "/Users/dev/bin"}, nil, MethodGoInstall},
		{"gopath from the go env file", "/Users/dev/goconf/bin/den",
			Env{GoenvGopath: "/Users/dev/goconf"}, nil, MethodGoInstall},
		// goBinDirs is a UNION, not the toolchain's precedence: moving GOBIN
		// today does not move the den installed yesterday, and returning GOBIN
		// alone stopped covering it.
		{"default go bin still covered once GOBIN moved", "/Users/dev/go/bin/den",
			Env{Gobin: "/opt/bin", Home: "/Users/dev"}, nil, MethodGoInstall},
		{"gopath bin still covered once GOBIN moved", "/Users/dev/gp/bin/den",
			Env{Gobin: "/opt/bin", Gopath: "/Users/dev/gp"}, nil, MethodGoInstall},
		// The executable path arrives EvalSymlinks'd, a configured GOBIN does
		// not. Without resolving the candidate directory this is a false
		// negative — the dangerous direction.
		{"symlinked gobin", "/Volumes/tools/bin/den",
			Env{Gobin: "/Users/dev/bin"},
			fakeResolver(map[string]string{"/Users/dev/bin": "/Volumes/tools/bin"}),
			MethodGoInstall},
		// EvalSymlinks answers an error for a directory that does not exist,
		// which is the normal case for a candidate den was never installed
		// into. That must stay a non-match, not a fault.
		{"unresolvable candidate falls back on the lexical answer", "/srv/tools/den",
			Env{Gobin: "/Users/dev/bin"}, fakeResolver(nil), MethodArchive},
		// `brew shellenv` exports HOMEBREW_PREFIX=/usr/local on an Intel Mac.
		// An install.sh install at DEN_INSTALL_DIR=/usr/local/bin is NOT a brew
		// install, and refusing it sent that user to `brew upgrade --cask den`,
		// which answers "not installed" — a dead end, since there is no
		// --force. Spec §11 recorded this; it is fixed.
		{"install.sh under an intel homebrew prefix", "/usr/local/bin/den",
			Env{HomebrewPrefix: "/usr/local"}, nil, MethodArchive},
		{"a cask under that same prefix is still refused", "/usr/local/Caskroom/den/1.8.1/den",
			Env{HomebrewPrefix: "/usr/local"}, nil, MethodHomebrew},
		{"a keg under that same prefix is still refused", "/usr/local/Cellar/den/1.8.1/bin/den",
			Env{HomebrewPrefix: "/usr/local"}, nil, MethodHomebrew},
		{"local bin", "/Users/dev/.local/bin/den", env, nil, MethodArchive},
		{"exotic install dir", "/srv/tools/den", env, nil, MethodArchive},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Classify(c.path, c.env, c.resolve); got != c.want {
				t.Fatalf("Classify(%q) = %v, want %v", c.path, got, c.want)
			}
		})
	}
}

func TestIsUpdatableVersion(t *testing.T) {
	// The three refused strings are the ones MEASURED against x/mod/semver on
	// 2026-08-18: `v1.5.0-17-g0ec48d8-dirty` is VALID semver and compares BELOW
	// v1.8.1, so a plain semver comparison would overwrite a local build.
	cases := []struct {
		version string
		want    bool
	}{
		{"v1.8.1", true},
		{"v0.1.0", true},
		{"dev", false},
		{"v1.5.0-17-g0ec48d8-dirty", false},
		{"v1.8.1-dirty", false},
		{"v1.9.0-rc1", false},
		{"v1.8.1+build5", false},
		{"", false},
		{"1.8.1", false},
	}
	for _, c := range cases {
		if got := IsUpdatableVersion(c.version); got != c.want {
			t.Errorf("IsUpdatableVersion(%q) = %v, want %v", c.version, got, c.want)
		}
	}
}
