// Package selfupdate owns `den update`: which install path den is running
// from, whether that path is den's to touch, and — when it is — replacing the
// binary with the latest release.
//
// The package is split by testability, not by topic. Everything here is pure:
// the network lives in fetch.go alone, the filesystem swap in install.go, and
// both are reachable behind an interface, so `go test ./...` stays hermetic
// (no socket, no process) exactly as the rest of the suite does.
package selfupdate

import (
	"path/filepath"
	"strings"

	"golang.org/x/mod/semver"
)

// Method is where the running binary came from. It decides whether `den update`
// may write at all: den never overwrites a binary another package manager owns
// (spec §4), because doing so leaves that manager convinced it still manages a
// version it no longer manages.
type Method int

const (
	// MethodArchive covers install.sh, a hand-unpacked release archive and any
	// DEN_INSTALL_DIR — the SAME bytes from the same tarball, since the cask
	// and install.sh download one archive (spec §2a). It is the only updatable
	// method.
	MethodArchive Method = iota
	MethodHomebrew
	MethodGoInstall
)

// Env is the environment Classify reads, taken as a value so tests never touch
// the process's own. HomebrewPrefix and HomebrewCellar are what cover a brew
// installed under a custom prefix: no list of default paths can enumerate
// those, and the failure mode of missing one is the corruption this table
// exists to prevent (spec §4).
type Env struct {
	HomebrewPrefix string
	HomebrewCellar string
	Gobin          string
	Gopath         string
	Home           string
}

// Classify reads a RESOLVED executable path — the caller passes
// filepath.EvalSymlinks(os.Executable()), never argv[0]. On darwin
// os.Executable answers the symlink itself (measured 2026-08-18), and
// /opt/homebrew/bin/den is a symlink into the Caskroom: without the resolution
// a brew install would be classified MethodArchive and overwritten.
func Classify(execPath string, env Env) Method {
	if env.HomebrewPrefix != "" && underDir(execPath, env.HomebrewPrefix) {
		return MethodHomebrew
	}
	if env.HomebrewCellar != "" && underDir(execPath, env.HomebrewCellar) {
		return MethodHomebrew
	}
	// Path COMPONENTS, not substrings: `~/dev/MyCellar/den` is somebody's own
	// directory, and refusing it would send them to a brew command that fails.
	for _, part := range strings.Split(filepath.ToSlash(execPath), "/") {
		if part == "Caskroom" || part == "Cellar" {
			return MethodHomebrew
		}
	}
	for _, prefix := range defaultBrewPrefixes(env.Home) {
		if underDir(execPath, prefix) {
			return MethodHomebrew
		}
	}
	for _, dir := range goBinDirs(env) {
		if filepath.Dir(execPath) == filepath.Clean(dir) {
			return MethodGoInstall
		}
	}
	return MethodArchive
}

// defaultBrewPrefixes lists the documented install prefixes. They are
// heuristics, not measurements — only the macOS Caskroom path was observed on
// this machine — which is why the environment variables above are consulted
// first.
func defaultBrewPrefixes(home string) []string {
	prefixes := []string{"/opt/homebrew", "/usr/local/Homebrew", "/home/linuxbrew/.linuxbrew"}
	if home != "" {
		prefixes = append(prefixes, filepath.Join(home, ".linuxbrew"))
	}
	return prefixes
}

// goBinDirs mirrors the go toolchain's own precedence: GOBIN wins, then
// GOPATH/bin, then the default ~/go/bin.
func goBinDirs(env Env) []string {
	if env.Gobin != "" {
		return []string{env.Gobin}
	}
	if env.Gopath != "" {
		return []string{filepath.Join(env.Gopath, "bin")}
	}
	if env.Home != "" {
		return []string{filepath.Join(env.Home, "go", "bin")}
	}
	return nil
}

// underDir reports whether path sits inside dir. It compares cleaned paths with
// a trailing separator, so /opt/homebrew-scratch is not "under" /opt/homebrew.
func underDir(path, dir string) bool {
	dir = filepath.Clean(dir)
	return path == dir || strings.HasPrefix(filepath.Clean(path), dir+string(filepath.Separator))
}

// IsUpdatableVersion answers whether the RUNNING binary's version may be
// replaced by a release. Only an exactly canonical version qualifies.
//
// The strictness is not caution, it is a measurement: `task build` stamps
// `v1.5.0-17-g0ec48d8-dirty`, which x/mod/semver accepts as VALID and compares
// BELOW v1.8.1 (probed 2026-08-18). A comparison alone would therefore
// cheerfully overwrite a local build with a release. internal/source's
// releaseVersion is no help either — it TRUNCATES the prerelease and would
// answer v1.5.0, ok.
//
// Every shipped binary carries a clean tag (goreleaser stamps {{ .Tag }}, and
// `go install …@vX` answers a bare vX), so no legitimate install falls in here.
// A future pre-release tag would: that is assumed in the spec §10.
func IsUpdatableVersion(v string) bool {
	return semver.IsValid(v) && semver.Prerelease(v) == "" && semver.Build(v) == ""
}

// EnvFromOS builds the Env from a getenv function, so the CLI never has to know
// which variables matter. A nil getenv answers an environment holding nothing —
// the same rule Deps.Getenv follows, so a test that wired nothing gets the
// documented defaults rather than the developer's own shell.
func EnvFromOS(getenv func(string) string) Env {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	return Env{
		HomebrewPrefix: getenv("HOMEBREW_PREFIX"),
		HomebrewCellar: getenv("HOMEBREW_CELLAR"),
		Gobin:          getenv("GOBIN"),
		Gopath:         getenv("GOPATH"),
		Home:           getenv("HOME"),
	}
}
