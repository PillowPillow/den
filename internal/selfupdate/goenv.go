package selfupdate

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// The go toolchain does not keep GOBIN and GOPATH in the process environment
// alone: `go env -w GOBIN=/somewhere` writes them into a config file, and every
// later `go install` obeys that file. Classify saw os.Getenv and nothing else,
// so a binary installed by a toolchain configured that way classified
// MethodArchive and `den update` overwrote a file the go toolchain still
// manages — the same harm as the GOPATH-splitting bug, reached by another road.
//
// Measured 2026-08-18, in an isolated GOENV so nothing on the machine moved:
// `go env -w GOBIN=/tmp/somewhere-else` writes the single line
// `GOBIN=/tmp/somewhere-else` into the file, `go env GOBIN` then answers
// /tmp/somewhere-else, and $GOBIN in the process environment stays EMPTY
// throughout. That gap is the whole false negative. Note the file need not
// exist: with none, `go env GOPATH` answers the built-in ~/go, which
// goBinDirs already covers through Env.Home.
//
// den reads the file rather than shelling out to `go env`: internal/cli must
// import no os/exec (internal/ports/hermeticity_test.go), `go` is not
// necessarily installed on a machine that runs den, and a subprocess per
// `den update` to read two strings is not a trade worth making.

// GoEnvPath derives the go toolchain's config file from the environment alone,
// so nothing here consults the real HOME. It mirrors cmd/go's own rule: $GOENV
// wins, the literal value `off` means the toolchain reads no file at all, and
// the default sits under the per-OS user config directory.
//
// os.UserConfigDir would answer the same path, and is NOT used: it reads the
// process environment directly, so a test would silently pick up the
// developer's own ~/Library/Application Support/go/env and classify differently
// on their machine than in CI.
func GoEnvPath(getenv func(string) string, goos string) string {
	if getenv == nil {
		return ""
	}
	switch env := getenv("GOENV"); env {
	case "":
	case "off":
		return ""
	default:
		return env
	}
	switch goos {
	case "windows":
		if dir := getenv("AppData"); dir != "" {
			return filepath.Join(dir, "go", "env")
		}
		return ""
	case "darwin", "ios":
		if home := getenv("HOME"); home != "" {
			return filepath.Join(home, "Library", "Application Support", "go", "env")
		}
		return ""
	default:
		if dir := getenv("XDG_CONFIG_HOME"); dir != "" {
			return filepath.Join(dir, "go", "env")
		}
		if home := getenv("HOME"); home != "" {
			return filepath.Join(home, ".config", "go", "env")
		}
		return ""
	}
}

// ParseGoEnv reads the two keys Classify cares about out of a go env file. The
// format is one KEY=VALUE per line, and the value runs to the end of the line —
// a path may contain `=`, so only the FIRST separator splits.
//
// An unreadable or malformed file answers empty strings rather than an error on
// purpose: this file only ever WIDENS the set of directories den refuses to
// write into, so failing to read it is a return to the previous behaviour, not
// a reason to refuse an update the user asked for.
func ParseGoEnv(content string) (gobin, gopath string) {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "GOBIN":
			gobin = value
		case "GOPATH":
			gopath = value
		}
	}
	return gobin, gopath
}

// EnvFromOS builds the Env from a getenv function, so the CLI never has to know
// which variables matter. A nil getenv answers an environment holding nothing —
// the same rule Deps.Getenv follows, so a test that wired nothing gets the
// documented defaults rather than the developer's own shell, and reads no file
// either since GoEnvPath answers "" without a getenv.
func EnvFromOS(getenv func(string) string) Env {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	goenvGobin, goenvGopath := ParseGoEnv(readGoEnv(getenv))
	return Env{
		HomebrewPrefix: getenv("HOMEBREW_PREFIX"),
		HomebrewCellar: getenv("HOMEBREW_CELLAR"),
		Gobin:          getenv("GOBIN"),
		Gopath:         getenv("GOPATH"),
		GoenvGobin:     goenvGobin,
		GoenvGopath:    goenvGopath,
		Home:           getenv("HOME"),
	}
}

// readGoEnv is the only filesystem access in the classification path, which is
// why it sits here and not in classify.go: everything there stays pure and
// testable without a temp directory.
func readGoEnv(getenv func(string) string) string {
	path := GoEnvPath(getenv, runtime.GOOS)
	if path == "" {
		return ""
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(body)
}
