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
	// GoenvGobin and GoenvGopath are the same two settings as written by
	// `go env -w`, which the toolchain keeps in a config file and NOT in the
	// process environment. goenv.go says why reading them matters; here they
	// are two more strings, so this struct stays a value a test can build by
	// hand without touching a filesystem.
	GoenvGobin  string
	GoenvGopath string
	Home        string
}

// Classify reads a RESOLVED executable path — the caller passes
// filepath.EvalSymlinks(os.Executable()), never argv[0]. On darwin
// os.Executable answers the symlink itself (measured 2026-08-18), and
// /opt/homebrew/bin/den is a symlink into the Caskroom: without the resolution
// a brew install would be classified MethodArchive and overwritten.
//
// resolve resolves a CANDIDATE DIRECTORY's symlinks, and is injected for the
// same reason every other system access in den is (cli.Deps): the comparison
// below needs the filesystem, and this package's tests must not. A nil resolve
// compares lexically, which is what the pure tests do.
func Classify(execPath string, env Env, resolve func(string) (string, error)) Method {
	// Bounded to the two directories brew actually installs into, NOT the
	// prefix as a whole. `brew shellenv` exports HOMEBREW_PREFIX=/usr/local on
	// an Intel Mac, so the whole-prefix test refused an install.sh install at
	// DEN_INSTALL_DIR=/usr/local/bin and sent that user to
	// `brew upgrade --cask den`, which answers "not installed" — a dead end,
	// since `den update` has no --force. Narrowing loses no brew coverage: a
	// real brew den resolves under Cellar/ or Caskroom/, which the component
	// scan below catches under any prefix. Spec §11 recorded the false
	// positive as accepted; it is now fixed, and §11 says so.
	if env.HomebrewPrefix != "" {
		for _, dir := range []string{"Cellar", "Caskroom"} {
			if underDir(execPath, filepath.Join(env.HomebrewPrefix, dir)) {
				return MethodHomebrew
			}
		}
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
	execDir := filepath.Dir(execPath)
	for _, dir := range goBinDirs(env) {
		if sameDir(execDir, dir, resolve) {
			return MethodGoInstall
		}
	}
	return MethodArchive
}

// sameDir reports whether execDir IS dir, comparing the lexical forms first and
// then the symlink-resolved one.
//
// The resolved comparison is not decoration: execPath arrives already
// EvalSymlinks'd while a configured GOBIN does not, so `GOBIN=~/bin` pointing
// at /Volumes/tools/bin left a go-install binary at /Volumes/tools/bin/den
// classified MethodArchive — a false negative, the direction that corrupts
// another manager's state.
// Only the candidate needs resolving, never execDir: EvalSymlinks resolves
// EVERY component, ancestors included, so filepath.Dir of an already-resolved
// executable is itself fully resolved (measured 2026-08-18 — a den reached
// through a symlinked parent directory answers the same directory on both
// sides). One resolution per candidate is therefore enough.
func sameDir(execDir, dir string, resolve func(string) (string, error)) bool {
	if execDir == filepath.Clean(dir) {
		return true
	}
	if resolve == nil {
		return false
	}
	// EvalSymlinks fails on a path that does not exist — ~/go/bin on a machine
	// with no Go at all, which is the common case for a candidate den never
	// installed into. That is not a classification failure and not a match: it
	// means there was nothing to resolve, so the lexical answer above stands.
	resolved, err := resolve(dir)
	if err != nil {
		return false
	}
	return execDir == filepath.Clean(resolved)
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

// goBinDirs lists every directory a `go install` of den could plausibly have
// written to. It is a UNION, deliberately not the go toolchain's precedence
// (GOBIN wins, then GOPATH/bin, then ~/go/bin).
//
// Precedence answers "where would a go install put den NOW"; classification
// must answer "could a go install have put THIS den here", and those differ
// whenever the environment changed after the install. `go env -w GOBIN=~/bin`
// run today does not move the den already sitting in ~/go/bin, and the old code
// — returning GOBIN alone — stopped covering it the moment GOBIN was set. A
// union only ever refuses MORE, which is the fail-safe direction: a false
// positive names a `go install` command that reinstalls a working den, a false
// negative corrupts the toolchain's state.
//
// GOPATH is os.PathListSeparator-separated, not a single path — `go env GOPATH`
// can legitimately answer `/a:/b`. filepath.Join(env.Gopath, "bin") on that
// string produced the nonsense path "/a:/b/bin", which matched nothing: a
// go-install binary living under the SECOND entry then classified
// MethodArchive and got overwritten.
//
// The tradeoff this widening accepts: a deliberate
// `DEN_INSTALL_DIR=~/go/bin sh install.sh` is now classified MethodGoInstall
// and sent to `go install`, which does reinstall a working den — the wrong
// remedy for that user, but a refusal rather than a corruption. The previous
// code already did this whenever GOBIN and GOPATH were both unset.
func goBinDirs(env Env) []string {
	var dirs []string
	add := func(dir string) {
		if dir != "" {
			dirs = append(dirs, dir)
		}
	}
	addGopath := func(gopath string) {
		for _, entry := range filepath.SplitList(gopath) {
			if entry != "" {
				add(filepath.Join(entry, "bin"))
			}
		}
	}
	add(env.Gobin)
	add(env.GoenvGobin)
	addGopath(env.Gopath)
	addGopath(env.GoenvGopath)
	if env.Home != "" {
		add(filepath.Join(env.Home, "go", "bin"))
	}
	return dirs
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
