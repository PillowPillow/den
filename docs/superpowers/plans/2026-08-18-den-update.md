# `den update` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship `den update`, the update path for every install that Homebrew does not own — it refuses, naming the right command, when another package manager owns the binary, and otherwise replaces the running binary with the latest release after verifying its sha256.

**Architecture:** A new `internal/selfupdate` package holds everything: a pure half (install-method classification, version predicate, archive naming, checksum parsing, tar extraction, refusal messages) and one impure `Fetcher` implementation in `net/http`, injected through `cli.Deps.Updater` exactly like `Scanner`, `Open` and `SSHAgent`. `internal/cli` imports only `internal/selfupdate`, so `internal/ports/hermeticity_test.go` stays green.

**Tech Stack:** Go 1.26, cobra, `golang.org/x/mod/semver` (already a dependency), stdlib `net/http` / `crypto/sha256` / `archive/tar` / `compress/gzip`. Runner is go-task (`task check`), never `make`.

**Spec:** `docs/superpowers/specs/2026-08-18-den-update-command-design.md`

## Global Constraints

- The command surface is exactly `den update`. **No flags** — no `--check`, no `--version`, no `--force`. Pinning and rollback stay `DEN_VERSION=vX.Y.Z sh install.sh`.
- Repo is on branch `docs/den-update-design`; keep working there or branch from it. `main` is never committed to directly.
- `task check` (lint » typecheck » test, fail-fast) must pass before every commit. `gofmt` is enforced, not advisory.
- No test opens a socket, spawns a process, or calls `t.Parallel()`. Network code sits behind the `Fetcher` interface and is exercised only by the CI smoke job.
- `internal/cli` must not import `net`, `hash/fnv` or `os/exec` — locked by `internal/ports/hermeticity_test.go`. `net/http` lives in `internal/selfupdate` only.
- Code, comments and user-facing messages are **English**. Errors name the file or path to fix and the remedy.
- Goldens are compared by hand; there is **no `-update` flag**. Write them manually.
- Version strings carry the leading `v` everywhere (`v1.8.1`), matching goreleaser's `{{ .Tag }}`.
- Archive names follow goreleaser's `archives.name_template`: `den_<version-without-v>_<os>_<arch>.tar.gz`.
- Latest-tag resolution goes through the `https://github.com/PillowPillow/den/releases/latest` redirect, never `api.github.com`.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/selfupdate/classify.go` (new) | `Method`, `Env`, `Classify`, `IsUpdatableVersion` — the §4 table, pure |
| `internal/selfupdate/classify_test.go` (new) | tables for both, including the measured Caskroom path |
| `internal/selfupdate/errors.go` (new) | `MethodError`, `VersionError`, `WriteError` — every refusal text |
| `internal/selfupdate/errors_test.go` (new) | goldens of the refusal texts |
| `internal/selfupdate/release.go` (new) | `TagFromURL`, `ArchiveName`, `ExpectedSum`, `VerifySum`, `ExtractBinary`, `NeedsUpdate` — pure release plumbing |
| `internal/selfupdate/release_test.go` (new) | in-memory tar.gz fixtures, checksum and comparison tables |
| `internal/selfupdate/fetch.go` (new) | `Fetcher` interface + `HTTPFetcher`, the ONLY file touching the network |
| `internal/selfupdate/install.go` (new) | `ProbeWritable`, `swapBinary` — staging file + atomic rename |
| `internal/selfupdate/install_test.go` (new) | real `t.TempDir()` swap, residue cleanup |
| `internal/selfupdate/update.go` (new) | `Request`, `Run` — the §5 sequence, driven by an injected `Fetcher` |
| `internal/selfupdate/update_test.go` (new) | sequence tests with a fake fetcher |
| `internal/selfupdate/testdata/*.golden` (new) | refusal and success output goldens |
| `internal/cli/update.go` (new) | `newUpdateCmd` — cobra wiring, no logic |
| `internal/cli/update_test.go` (new) | command-level tests with a fake fetcher |
| `internal/cli/root.go` (modify) | `Deps.Updater` field, `SystemDeps` wiring, `root.AddCommand(newUpdateCmd(...))` |
| `README.md` (modify) | update line per install path + command table row |
| `.github/workflows/ci.yml` (modify) | `update-command` smoke job |

---

### Task 1: Install-method classification and the version predicate

**Files:**
- Create: `internal/selfupdate/classify.go`
- Create: `internal/selfupdate/classify_test.go`
- Test: `internal/selfupdate/classify_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `type Method int` with `MethodArchive`, `MethodHomebrew`, `MethodGoInstall`; `type Env struct { HomebrewPrefix, HomebrewCellar, Gobin, Gopath, Home string }`; `func Classify(execPath string, env Env) Method`; `func IsUpdatableVersion(v string) bool`.

- [ ] **Step 1: Write the failing tests**

```go
package selfupdate

import "testing"

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
		want Method
	}{
		{"caskroom macos", "/opt/homebrew/Caskroom/den/1.8.1/den", env, MethodHomebrew},
		{"cellar intel", "/usr/local/Cellar/den/1.8.1/bin/den", env, MethodHomebrew},
		{"linuxbrew", "/home/linuxbrew/.linuxbrew/Caskroom/den/1.8.1/den", env, MethodHomebrew},
		{"custom prefix via env", "/srv/brew/Caskroom/den/1.8.1/den",
			Env{HomebrewPrefix: "/srv/brew"}, MethodHomebrew},
		{"custom cellar via env", "/srv/kegs/den/1.8.1/bin/den",
			Env{HomebrewCellar: "/srv/kegs"}, MethodHomebrew},
		{"false positive guarded", "/Users/dev/MyCellar/den", env, MethodArchive},
		{"gobin wins over gopath", "/opt/bin/den",
			Env{Gobin: "/opt/bin", Gopath: "/Users/dev/go"}, MethodGoInstall},
		{"gopath bin", "/Users/dev/go/bin/den", env, MethodGoInstall},
		{"default go bin", "/Users/dev/go/bin/den", Env{Home: "/Users/dev"}, MethodGoInstall},
		{"local bin", "/Users/dev/.local/bin/den", env, MethodArchive},
		{"exotic install dir", "/srv/tools/den", env, MethodArchive},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Classify(c.path, c.env); got != c.want {
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
```

- [ ] **Step 2: Run the tests and verify they fail**

Run: `go test ./internal/selfupdate/ -count=1`
Expected: FAIL — the package does not exist yet (`no required module provides package`), or `undefined: Classify`.

- [ ] **Step 3: Write the implementation**

```go
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
```

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test ./internal/selfupdate/ -count=1`
Expected: PASS (`ok  github.com/PillowPillow/den/internal/selfupdate`).

- [ ] **Step 5: Run the full gate**

Run: `task check`
Expected: lint, typecheck and the whole suite pass.

- [ ] **Step 6: Commit**

```bash
git add internal/selfupdate/classify.go internal/selfupdate/classify_test.go
git commit -m "feat(selfupdate): classify the install method and gate non-release versions"
```

---

### Task 2: The refusal texts

**Files:**
- Create: `internal/selfupdate/errors.go`
- Create: `internal/selfupdate/errors_test.go`
- Create: `internal/selfupdate/testdata/refusal_homebrew.golden`
- Create: `internal/selfupdate/testdata/refusal_goinstall.golden`
- Create: `internal/selfupdate/testdata/refusal_version.golden`
- Create: `internal/selfupdate/testdata/refusal_write.golden`

**Interfaces:**
- Consumes: `Method`, `MethodHomebrew`, `MethodGoInstall` (Task 1).
- Produces: `func MethodRefusal(m Method, execPath string) error`; `type VersionError struct { Observed string }`; `type WriteError struct { Dir string; Err error }`. Both types implement `error`, and `WriteError` implements `Unwrap() error`.

- [ ] **Step 1: Write the failing tests**

```go
package selfupdate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// assertGolden compares against a file edited BY HAND: this repo has no
// -update flag for goldens, on purpose.
func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if got != string(want) {
		t.Fatalf("golden %s mismatch\n got: %q\nwant: %q", name, got, string(want))
	}
}

func TestMethodRefusalHomebrew(t *testing.T) {
	err := MethodRefusal(MethodHomebrew, "/opt/homebrew/Caskroom/den/1.8.1/den")
	assertGolden(t, "refusal_homebrew.golden", err.Error())
}

func TestMethodRefusalGoInstall(t *testing.T) {
	err := MethodRefusal(MethodGoInstall, "/Users/dev/go/bin/den")
	assertGolden(t, "refusal_goinstall.golden", err.Error())
}

func TestMethodRefusalArchiveIsNil(t *testing.T) {
	if err := MethodRefusal(MethodArchive, "/Users/dev/.local/bin/den"); err != nil {
		t.Fatalf("MethodArchive must not refuse, got %v", err)
	}
}

func TestVersionErrorText(t *testing.T) {
	err := &VersionError{Observed: "v1.5.0-17-g0ec48d8-dirty"}
	assertGolden(t, "refusal_version.golden", err.Error())
}

func TestWriteErrorTextAndUnwrap(t *testing.T) {
	err := &WriteError{Dir: "/usr/local/bin", Err: os.ErrPermission}
	assertGolden(t, "refusal_write.golden", err.Error())
	if !errors.Is(err, os.ErrPermission) {
		t.Fatal("WriteError must unwrap to the cause")
	}
}
```

- [ ] **Step 2: Write the goldens by hand**

`internal/selfupdate/testdata/refusal_homebrew.golden` (single line, no trailing newline):

```
den was installed by Homebrew (/opt/homebrew/Caskroom/den/1.8.1/den) — run `brew upgrade --cask den`; den does not touch a binary another package manager owns
```

`internal/selfupdate/testdata/refusal_goinstall.golden`:

```
den was installed by the go toolchain (/Users/dev/go/bin/den) — run `go install github.com/PillowPillow/den/cmd/den@latest`; den does not touch a binary another package manager owns
```

`internal/selfupdate/testdata/refusal_version.golden`:

```
den version "v1.5.0-17-g0ec48d8-dirty" is not a released build — `den update` replaces a release with a release; run `git pull && task build` for a checkout, or install a release with install.sh
```

`internal/selfupdate/testdata/refusal_write.golden`:

```
cannot write to /usr/local/bin — pick a writable destination and reinstall with `curl -fsSL https://raw.githubusercontent.com/PillowPillow/den/main/install.sh | DEN_INSTALL_DIR=~/.local/bin sh`: permission denied
```

- [ ] **Step 3: Run the tests and verify they fail**

Run: `go test ./internal/selfupdate/ -run 'Refusal|Error' -count=1`
Expected: FAIL with `undefined: MethodRefusal`.

- [ ] **Step 4: Write the implementation**

```go
package selfupdate

import (
	"fmt"
)

// The remedy sentences live in one place because they are the whole value of
// the refusal: den's errors name the fix, not just the fault (spec §2 of the
// CLI design). MethodArchive returns nil — it is the one method den updates.
func MethodRefusal(m Method, execPath string) error {
	switch m {
	case MethodHomebrew:
		return fmt.Errorf("den was installed by Homebrew (%s) — run `brew upgrade --cask den`; "+
			"den does not touch a binary another package manager owns", execPath)
	case MethodGoInstall:
		return fmt.Errorf("den was installed by the go toolchain (%s) — run "+
			"`go install github.com/PillowPillow/den/cmd/den@latest`; "+
			"den does not touch a binary another package manager owns", execPath)
	default:
		return nil
	}
}

// VersionError refuses a binary whose version is not a clean release — `dev`,
// or the `git describe` stamp `task build` produces. See IsUpdatableVersion for
// why a semver comparison alone cannot catch the second one.
type VersionError struct {
	Observed string
}

func (e *VersionError) Error() string {
	return fmt.Sprintf("den version %q is not a released build — `den update` replaces a release "+
		"with a release; run `git pull && task build` for a checkout, or install a release with install.sh",
		e.Observed)
}

// WriteError reports a destination den cannot write. It names the directory and
// a remedy but NOT a cause: a directory can be unwritable, full, or read-only,
// and asserting "permission denied" would send a full-disk user to chmod. The
// underlying error is appended verbatim and reachable through errors.Is.
type WriteError struct {
	Dir string
	Err error
}

func (e *WriteError) Error() string {
	return fmt.Sprintf("cannot write to %s — pick a writable destination and reinstall with "+
		"`curl -fsSL https://raw.githubusercontent.com/PillowPillow/den/main/install.sh | "+
		"DEN_INSTALL_DIR=~/.local/bin sh`: %v", e.Dir, e.Err)
}

func (e *WriteError) Unwrap() error { return e.Err }
```

- [ ] **Step 5: Run the tests and verify they pass**

Run: `go test ./internal/selfupdate/ -count=1`
Expected: PASS. If a golden mismatches, fix the GOLDEN or the message so both read as one sentence a user can act on — never widen the test.

- [ ] **Step 6: Commit**

```bash
git add internal/selfupdate/errors.go internal/selfupdate/errors_test.go internal/selfupdate/testdata
git commit -m "feat(selfupdate): refusal texts naming the remedy for every non-archive install"
```

---

### Task 3: Release plumbing — tag, archive name, checksum, extraction

**Files:**
- Create: `internal/selfupdate/release.go`
- Create: `internal/selfupdate/release_test.go`

**Interfaces:**
- Consumes: `IsUpdatableVersion` (Task 1).
- Produces: `func TagFromURL(finalURL string) (string, error)`; `func ArchiveName(tag, goos, goarch string) string`; `func DownloadURL(tag, file string) string`; `func ExpectedSum(checksums []byte, archive string) (string, error)`; `func VerifySum(archive []byte, expected string) error`; `func ExtractBinary(targz []byte) ([]byte, error)`; `func NeedsUpdate(current, latest string) bool`.

- [ ] **Step 1: Write the failing tests**

```go
package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
)

// tarGz builds an archive in memory: the suite never reads a fixture binary
// from disk, and never shells out to tar.
func tarGz(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for name, body := range entries {
		hdr := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestTagFromURL(t *testing.T) {
	tag, err := TagFromURL("https://github.com/PillowPillow/den/releases/tag/v1.8.1")
	if err != nil || tag != "v1.8.1" {
		t.Fatalf("TagFromURL = %q, %v; want v1.8.1, nil", tag, err)
	}
	if _, err := TagFromURL("https://github.com/PillowPillow/den/releases"); err == nil {
		t.Fatal("a URL with no v-prefixed tag must be refused")
	}
}

func TestArchiveNameAndURL(t *testing.T) {
	// Mirrors goreleaser's archives.name_template, which drops the leading v.
	if got := ArchiveName("v1.8.1", "darwin", "arm64"); got != "den_1.8.1_darwin_arm64.tar.gz" {
		t.Fatalf("ArchiveName = %q", got)
	}
	want := "https://github.com/PillowPillow/den/releases/download/v1.8.1/checksums.txt"
	if got := DownloadURL("v1.8.1", "checksums.txt"); got != want {
		t.Fatalf("DownloadURL = %q, want %q", got, want)
	}
}

func TestExpectedSum(t *testing.T) {
	// The real checksums.txt format: "<sha256>  <filename>".
	checksums := []byte("aaa  den_1.8.1_linux_amd64.tar.gz\nbbb  den_1.8.1_darwin_arm64.tar.gz\n")
	sum, err := ExpectedSum(checksums, "den_1.8.1_darwin_arm64.tar.gz")
	if err != nil || sum != "bbb" {
		t.Fatalf("ExpectedSum = %q, %v", sum, err)
	}
	if _, err := ExpectedSum(checksums, "den_1.8.1_windows_amd64.tar.gz"); err == nil {
		t.Fatal("a missing entry must be refused, not defaulted")
	}
}

func TestVerifySum(t *testing.T) {
	payload := []byte("hello")
	// sha256("hello")
	const good = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if err := VerifySum(payload, good); err != nil {
		t.Fatalf("VerifySum on a matching digest: %v", err)
	}
	err := VerifySum(payload, "0000000000000000000000000000000000000000000000000000000000000000")
	if err == nil {
		t.Fatal("a mismatching digest must be refused")
	}
	if !strings.Contains(err.Error(), "corrupted or incomplete") {
		t.Fatalf("the mismatch message must claim integrity only, got %q", err)
	}
}

func TestExtractBinary(t *testing.T) {
	body, err := ExtractBinary(tarGz(t, map[string]string{"den": "ELF", "LICENSE": "x"}))
	if err != nil || string(body) != "ELF" {
		t.Fatalf("ExtractBinary = %q, %v", body, err)
	}
	if _, err := ExtractBinary(tarGz(t, map[string]string{"LICENSE": "x"})); err == nil {
		t.Fatal("an archive without a den entry must be refused")
	}
	if _, err := ExtractBinary([]byte("not a gzip stream")); err == nil {
		t.Fatal("a truncated archive must be refused")
	}
}

func TestNeedsUpdate(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v1.8.0", "v1.8.1", true},
		{"v1.8.1", "v1.8.1", false},
		{"v1.9.0", "v1.8.1", false},
	}
	for _, c := range cases {
		if got := NeedsUpdate(c.current, c.latest); got != c.want {
			t.Errorf("NeedsUpdate(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run the tests and verify they fail**

Run: `go test ./internal/selfupdate/ -run 'Tag|Archive|Sum|Extract|Needs' -count=1`
Expected: FAIL with `undefined: TagFromURL`.

- [ ] **Step 3: Write the implementation**

```go
package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"golang.org/x/mod/semver"
)

// Repo is the single place the download host is spelled. install.sh spells it
// too; the CI smoke of §8 is what keeps the two honest.
const releasesBase = "https://github.com/PillowPillow/den/releases"

// LatestURL is the redirect den reads the newest tag from. NOT api.github.com:
// 60 unauthenticated requests per hour, and a JSON dependency the target
// machines may not have — the same decision install.sh already took.
const LatestURL = releasesBase + "/latest"

// TagFromURL reads the tag out of the URL /releases/latest redirected to. It
// refuses anything that does not end in a v-prefixed segment rather than
// normalizing: den's tags carry the leading v, and a guessed tag would produce
// a valid-looking archive name under an invalid URL.
func TagFromURL(finalURL string) (string, error) {
	tag := finalURL[strings.LastIndex(finalURL, "/")+1:]
	if !strings.HasPrefix(tag, "v") || !semver.IsValid(tag) {
		return "", fmt.Errorf("could not read a release tag from %q — download an archive from %s instead",
			finalURL, releasesBase)
	}
	return tag, nil
}

// ArchiveName mirrors .goreleaser.yaml's archives.name_template, which uses the
// version WITHOUT the leading v. install.sh recomposes the same name; the CI
// smoke job is what proves both still match the published release.
func ArchiveName(tag, goos, goarch string) string {
	return fmt.Sprintf("den_%s_%s_%s.tar.gz", strings.TrimPrefix(tag, "v"), goos, goarch)
}

func DownloadURL(tag, file string) string {
	return fmt.Sprintf("%s/download/%s/%s", releasesBase, tag, file)
}

// ExpectedSum finds one archive's digest in checksums.txt ("<sha256>  <name>").
// A missing entry is a changed release layout, not a user mistake, and the
// message says so.
func ExpectedSum(checksums []byte, archive string) (string, error) {
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == archive {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("checksums.txt has no entry for %s — the release layout changed; "+
		"report this at https://github.com/PillowPillow/den/issues", archive)
}

// VerifySum proves INTEGRITY, never authenticity: checksums.txt travels the
// same unsigned TLS channel as the archive, so this catches a corrupted or
// truncated download and cannot catch a compromised release. The message claims
// only what the check proves — the same wording discipline as install.sh.
func VerifySum(archive []byte, expected string) error {
	sum := sha256.Sum256(archive)
	if got := hex.EncodeToString(sum[:]); got != expected {
		return fmt.Errorf("checksum mismatch: the download is corrupted or incomplete — "+
			"re-run `den update`, and report it if this persists (expected %s, got %s)", expected, got)
	}
	return nil
}

// ExtractBinary pulls the `den` entry out of the release archive. Everything
// else in the tarball (LICENSE, README) is ignored on purpose: den replaces one
// file, and unpacking more would write files nobody asked for next to the
// binary.
func ExtractBinary(targz []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(targz))
	if err != nil {
		return nil, fmt.Errorf("the downloaded archive is not readable (%v) — re-run `den update`", err)
	}
	defer func() { _ = zr.Close() }()

	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("the downloaded archive is not readable (%v) — re-run `den update`", err)
		}
		if hdr.Name != "den" {
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("the downloaded archive is truncated (%v) — re-run `den update`", err)
		}
		return body, nil
	}
	return nil, fmt.Errorf("the release archive carries no `den` entry — the release layout changed; " +
		"report this at https://github.com/PillowPillow/den/issues")
}

// NeedsUpdate compares two CANONICAL versions. The caller has already refused a
// non-release current version (IsUpdatableVersion), so both sides here are
// clean vX.Y.Z and semver.Compare answers the whole question — including the
// "local is newer" case, where den does nothing.
func NeedsUpdate(current, latest string) bool {
	return semver.Compare(current, latest) < 0
}
```

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test ./internal/selfupdate/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/selfupdate/release.go internal/selfupdate/release_test.go
git commit -m "feat(selfupdate): tag resolution, archive naming, checksum and extraction"
```

---

### Task 4: The atomic swap and the write probe

**Files:**
- Create: `internal/selfupdate/install.go`
- Create: `internal/selfupdate/install_test.go`

**Interfaces:**
- Consumes: `WriteError` (Task 2).
- Produces: `func ProbeWritable(dir string) error`; `func SwapBinary(target string, body []byte) error`.

- [ ] **Step 1: Write the failing tests**

```go
package selfupdate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProbeWritableAcceptsATempDir(t *testing.T) {
	dir := t.TempDir()
	if err := ProbeWritable(dir); err != nil {
		t.Fatalf("ProbeWritable(%s) = %v", dir, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("the probe left %d file(s) behind: %v", len(entries), entries)
	}
}

func TestProbeWritableRefusesAMissingDir(t *testing.T) {
	err := ProbeWritable(filepath.Join(t.TempDir(), "nope"))
	if err == nil {
		t.Fatal("a missing directory must be refused before anything is downloaded")
	}
	var we *WriteError
	if !errors.As(err, &we) {
		t.Fatalf("want a *WriteError naming the remedy, got %T: %v", err, err)
	}
}

func TestSwapBinaryReplacesInPlace(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "den")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SwapBinary(target, []byte("new")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("target holds %q, want \"new\"", got)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode is %v, want 0755", info.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("the staging file survived the swap: %v", entries)
	}
}

func TestSwapBinaryLeavesNoResidueOnFailure(t *testing.T) {
	dir := t.TempDir()
	// The target is a DIRECTORY, so the rename fails after the staging file
	// was written — the window the cleanup exists for.
	target := filepath.Join(dir, "den")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SwapBinary(target, []byte("new")); err == nil {
		t.Fatal("renaming onto a directory must fail")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("a staging file survived the failure: %v", entries)
	}
}
```

The test file's import block is therefore:

```go
import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)
```

- [ ] **Step 2: Run the tests and verify they fail**

Run: `go test ./internal/selfupdate/ -run 'Probe|Swap' -count=1`
Expected: FAIL with `undefined: ProbeWritable`.

- [ ] **Step 3: Write the implementation**

```go
package selfupdate

import (
	"fmt"
	"os"
	"path/filepath"
)

// stagingName is the name of the file the new binary lands on before the
// rename. The pid keeps two concurrent updates from fighting, and every path
// out of SwapBinary removes it — the same reason install.sh carries a trap for
// its own .den.new.$$.
func stagingName(pid int) string {
	return fmt.Sprintf(".den.new.%d", pid)
}

// ProbeWritable creates and removes the staging file BEFORE anything is
// downloaded. It answers two questions at once — is the directory writable, and
// will the staging file share a filesystem with the target — on the correct
// side of the first side effect. Without it, "cannot write here" is discovered
// after several megabytes have been fetched.
func ProbeWritable(dir string) error {
	probe := filepath.Join(dir, stagingName(os.Getpid()))
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		return &WriteError{Dir: dir, Err: err}
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(probe)
		return &WriteError{Dir: dir, Err: err}
	}
	if err := os.Remove(probe); err != nil {
		return &WriteError{Dir: dir, Err: err}
	}
	return nil
}

// SwapBinary writes the new binary beside the target and renames it over.
//
// Two steps, not one, and for the reason install.sh spells out: writing
// straight onto the live file fails with ETXTBSY on Linux while den is running,
// and an interrupt mid-copy leaves a truncated binary on PATH. The rename is a
// single atomic operation — a reader gets the old den or the new one, never
// half of either — which is only true because the staging file sits in the
// SAME directory, hence on the same filesystem.
func SwapBinary(target string, body []byte) error {
	dir := filepath.Dir(target)
	staging := filepath.Join(dir, stagingName(os.Getpid()))
	// Cleanup on every path out: a signal landing between the write and the
	// rename would otherwise leave a stray .den.new.<pid> in the user's bin
	// directory forever.
	defer func() { _ = os.Remove(staging) }()

	if err := os.WriteFile(staging, body, 0o755); err != nil {
		return &WriteError{Dir: dir, Err: err}
	}
	if err := os.Rename(staging, target); err != nil {
		return &WriteError{Dir: dir, Err: err}
	}
	return nil
}
```

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test ./internal/selfupdate/ -count=1`
Expected: PASS.

**Not tested, and deliberately so:** a read-only mount and a full filesystem both reach
`ProbeWritable`'s `O_CREATE|O_EXCL` and surface as `EROFS` / `ENOSPC` inside `WriteError`, which
reports the system error verbatim (spec §7). Neither condition can be created inside a hermetic test
— mounting is a machine operation — so the coverage stops at "the error is wrapped and named", which
`TestProbeWritableRefusesAMissingDir` proves. This paragraph exists so a reviewer reads the gap as a
decision, not an omission.

- [ ] **Step 5: Commit**

```bash
git add internal/selfupdate/install.go internal/selfupdate/install_test.go
git commit -m "feat(selfupdate): write probe and atomic binary swap"
```

---

### Task 5: The fetcher and the update sequence

**Files:**
- Create: `internal/selfupdate/fetch.go`
- Create: `internal/selfupdate/update.go`
- Create: `internal/selfupdate/update_test.go`
- Create: `internal/selfupdate/testdata/update_success.golden`
- Create: `internal/selfupdate/testdata/update_uptodate.golden`

**Interfaces:**
- Consumes: everything from Tasks 1-4.
- Produces: `type Fetcher interface { ResolveLatest(ctx context.Context) (string, error); Get(ctx context.Context, url string) ([]byte, error) }`; `type HTTPFetcher struct{ Client *http.Client }`; `type Request struct { ExecPath, Current, GOOS, GOARCH string; Env Env }`; `func Run(ctx context.Context, f Fetcher, req Request, out io.Writer) error`.

- [ ] **Step 1: Write the failing tests**

```go
package selfupdate

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeFetcher answers from memory. No test in this repo opens a socket, so the
// whole sequence is exercised through this double — the real HTTPFetcher stays
// the smallest untested surface, exactly like ports.ListenScanner.
type fakeFetcher struct {
	tag      string
	tagErr   error
	bodies   map[string][]byte
	requests []string
}

func (f *fakeFetcher) ResolveLatest(context.Context) (string, error) {
	if f.tagErr != nil {
		return "", f.tagErr
	}
	return f.tag, nil
}

func (f *fakeFetcher) Get(_ context.Context, url string) ([]byte, error) {
	f.requests = append(f.requests, url)
	body, ok := f.bodies[url]
	if !ok {
		return nil, errors.New("404")
	}
	return body, nil
}

// releaseFixture builds a fake release: the archive, and the checksums.txt that
// matches it.
func releaseFixture(t *testing.T, tag, goos, goarch, binary string) map[string][]byte {
	t.Helper()
	archive := tarGz(t, map[string]string{"den": binary})
	name := ArchiveName(tag, goos, goarch)
	sum := sha256Hex(archive)
	return map[string][]byte{
		DownloadURL(tag, name):            archive,
		DownloadURL(tag, "checksums.txt"): []byte(sum + "  " + name + "\n"),
	}
}

func TestRunReplacesTheBinary(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "den")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := &fakeFetcher{tag: "v1.8.1", bodies: releaseFixture(t, "v1.8.1", "linux", "amd64", "NEW")}
	var out bytes.Buffer
	req := Request{ExecPath: target, Current: "v1.8.0", GOOS: "linux", GOARCH: "amd64"}
	if err := Run(context.Background(), f, req, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "NEW" {
		t.Fatalf("binary holds %q, want NEW", got)
	}
	assertGolden(t, "update_success.golden", strings.ReplaceAll(out.String(), target, "<path>"))
}

func TestRunSaysNothingToDoWhenCurrent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "den")
	if err := os.WriteFile(target, []byte("same"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := &fakeFetcher{tag: "v1.8.1"}
	var out bytes.Buffer
	req := Request{ExecPath: target, Current: "v1.8.1", GOOS: "linux", GOARCH: "amd64"}
	if err := Run(context.Background(), f, req, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(f.requests) != 0 {
		t.Fatalf("an up-to-date den downloaded %v", f.requests)
	}
	assertGolden(t, "update_uptodate.golden", out.String())
}

func TestRunRefusesBeforeAnyRequest(t *testing.T) {
	cases := []struct {
		name string
		req  Request
	}{
		{"homebrew", Request{ExecPath: "/opt/homebrew/Caskroom/den/1.8.1/den", Current: "v1.8.0"}},
		{"go install", Request{ExecPath: "/Users/dev/go/bin/den", Current: "v1.8.0",
			Env: Env{Gopath: "/Users/dev/go"}}},
		{"local build", Request{ExecPath: "/Users/dev/.local/bin/den", Current: "v1.5.0-17-g0ec48d8-dirty"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeFetcher{tag: "v1.8.1"}
			err := Run(context.Background(), f, c.req, &bytes.Buffer{})
			if err == nil {
				t.Fatal("want a refusal")
			}
			if len(f.requests) != 0 {
				t.Fatalf("the refusal came AFTER a download: %v", f.requests)
			}
		})
	}
}

func TestRunRefusesAMismatchedChecksum(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "den")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	bodies := releaseFixture(t, "v1.8.1", "linux", "amd64", "NEW")
	name := ArchiveName("v1.8.1", "linux", "amd64")
	bodies[DownloadURL("v1.8.1", "checksums.txt")] =
		[]byte("0000000000000000000000000000000000000000000000000000000000000000  " + name + "\n")
	f := &fakeFetcher{tag: "v1.8.1", bodies: bodies}
	req := Request{ExecPath: target, Current: "v1.8.0", GOOS: "linux", GOARCH: "amd64"}
	if err := Run(context.Background(), f, req, &bytes.Buffer{}); err == nil {
		t.Fatal("a checksum mismatch must refuse")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old" {
		t.Fatal("the binary was replaced despite a failed verification")
	}
}
```

Goldens, written by hand:

`internal/selfupdate/testdata/update_success.golden`:

```
den v1.8.0 → v1.8.1 (<path>)
```

`internal/selfupdate/testdata/update_uptodate.golden`:

```
den v1.8.1 is already the latest release
```

(Each golden ends with a single trailing newline, because the implementation prints one.)

- [ ] **Step 2: Add the sha256Hex helper the fixture uses**

In `internal/selfupdate/release.go`, beside `VerifySum`:

```go
// sha256Hex is VerifySum's digest, exported to the package so tests can build a
// checksums.txt that matches a fixture archive without duplicating the hashing.
func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
```

And rewrite `VerifySum`'s first lines to use it:

```go
func VerifySum(archive []byte, expected string) error {
	if got := sha256Hex(archive); got != expected {
```

- [ ] **Step 3: Run the tests and verify they fail**

Run: `go test ./internal/selfupdate/ -run TestRun -count=1`
Expected: FAIL with `undefined: Run`.

- [ ] **Step 4: Write the fetcher**

```go
package selfupdate

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Fetcher is the ONE seam between the update sequence and the network. Every
// test injects a double; the real implementation below is the only code in this
// package that speaks HTTP, kept as thin as ports.ListenScanner is, for the
// same reason: what cannot be tested hermetically must be the smallest possible
// surface, so everything around it stays tested.
type Fetcher interface {
	ResolveLatest(ctx context.Context) (string, error)
	Get(ctx context.Context, url string) ([]byte, error)
}

// HTTPFetcher is the real Fetcher. Untested by construction: any test here would
// reach github.com, which this suite forbids. The CI smoke job of ci.yml is
// what exercises it.
type HTTPFetcher struct {
	Client *http.Client
}

// NewHTTPFetcher builds the fetcher den ships with. The timeout is generous
// rather than tight: it covers the whole archive download on a slow link, and
// an update that hangs forever is worse than one that says the network failed.
func NewHTTPFetcher() HTTPFetcher {
	return HTTPFetcher{Client: &http.Client{Timeout: 5 * time.Minute}}
}

// ResolveLatest reads the tag out of the URL /releases/latest redirects to —
// the same trick install.sh uses, and for the same reason (no api.github.com
// rate limit, no JSON dependency).
func (f HTTPFetcher) ResolveLatest(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, LatestURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := f.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("cannot reach %s (%v) — check your network, or download an archive from %s",
			LatestURL, err, releasesBase)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s answered %s — download an archive from %s instead",
			LatestURL, resp.Status, releasesBase)
	}
	// resp.Request is the LAST request of the redirect chain, which is where
	// the tag is spelled.
	return TagFromURL(resp.Request.URL.String())
}

func (f HTTPFetcher) Get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := f.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot download %s (%v) — check your network, or download an archive from %s",
			url, err, releasesBase)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cannot download %s: %s — does this release ship your OS and architecture? see %s",
			url, resp.Status, releasesBase)
	}
	return io.ReadAll(resp.Body)
}

// client keeps a zero-value HTTPFetcher usable rather than panicking.
func (f HTTPFetcher) client() *http.Client {
	if f.Client == nil {
		return http.DefaultClient
	}
	return f.Client
}
```

- [ ] **Step 5: Write the sequence**

```go
package selfupdate

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
)

// Request is everything Run needs from the outside world. GOOS/GOARCH are
// parameters rather than runtime constants so a test can exercise a platform it
// is not running on — the same reason runtime.GOOS is named at the wiring site
// for `den exec`.
type Request struct {
	ExecPath string
	Current  string
	GOOS     string
	GOARCH   string
	Env      Env
}

// Run is the §5 sequence, and its ORDER is the contract: everything refusable
// is refused before the first byte is downloaded, so a refusal never leaves a
// half-updated binary or a stray staging file. The tests assert exactly that —
// a refusal that came after a request is a bug even if its message is right.
func Run(ctx context.Context, f Fetcher, req Request, out io.Writer) error {
	if err := MethodRefusal(Classify(req.ExecPath, req.Env), req.ExecPath); err != nil {
		return err
	}
	if !IsUpdatableVersion(req.Current) {
		return &VersionError{Observed: req.Current}
	}
	if err := ProbeWritable(filepath.Dir(req.ExecPath)); err != nil {
		return err
	}

	latest, err := f.ResolveLatest(ctx)
	if err != nil {
		return err
	}
	if !NeedsUpdate(req.Current, latest) {
		fmt.Fprintf(out, "den %s is already the latest release\n", req.Current)
		return nil
	}

	name := ArchiveName(latest, req.GOOS, req.GOARCH)
	archive, err := f.Get(ctx, DownloadURL(latest, name))
	if err != nil {
		return err
	}
	checksums, err := f.Get(ctx, DownloadURL(latest, "checksums.txt"))
	if err != nil {
		return err
	}
	expected, err := ExpectedSum(checksums, name)
	if err != nil {
		return err
	}
	if err := VerifySum(archive, expected); err != nil {
		return err
	}
	binary, err := ExtractBinary(archive)
	if err != nil {
		return err
	}
	if err := SwapBinary(req.ExecPath, binary); err != nil {
		return err
	}
	fmt.Fprintf(out, "den %s → %s (%s)\n", req.Current, latest, req.ExecPath)
	return nil
}
```

- [ ] **Step 6: Run the tests and verify they pass**

Run: `go test ./internal/selfupdate/ -count=1`
Expected: PASS, including `TestRunRefusesBeforeAnyRequest`.

- [ ] **Step 7: Run the full gate**

Run: `task check`
Expected: green.

- [ ] **Step 8: Commit**

```bash
git add internal/selfupdate
git commit -m "feat(selfupdate): the update sequence behind an injected fetcher"
```

---

### Task 6: The `den update` command and its wiring

**Files:**
- Create: `internal/cli/update.go`
- Create: `internal/cli/update_test.go`
- Modify: `internal/cli/root.go` (the `Deps` struct, `SystemDeps`, and the `AddCommand` block)

**Interfaces:**
- Consumes: `selfupdate.Fetcher`, `selfupdate.Run`, `selfupdate.Request`, `selfupdate.Env`, `selfupdate.NewHTTPFetcher` (Task 5); `displayVersion` (`internal/cli/version.go`).
- Produces: `func newUpdateCmd(d Deps) *cobra.Command`; `Deps.Updater selfupdate.Fetcher`; `Deps.Executable func() (string, error)`.

- [ ] **Step 1: Write the failing tests**

```go
package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubFetcher answers nothing: these tests only reach the refusal paths, which
// happen before any request. The happy path is covered in internal/selfupdate,
// against its own fake.
type stubFetcher struct{ called bool }

func (s *stubFetcher) ResolveLatest(context.Context) (string, error) {
	s.called = true
	return "", errors.New("the command must not reach the network here")
}

func (s *stubFetcher) Get(context.Context, string) ([]byte, error) {
	s.called = true
	return nil, errors.New("the command must not reach the network here")
}

func TestUpdateRefusesAHomebrewInstall(t *testing.T) {
	f := &stubFetcher{}
	deps := Deps{
		Updater:    f,
		DenVersion: func() string { return "v1.8.0" },
		Executable: func() (string, error) { return "/opt/homebrew/Caskroom/den/1.8.0/den", nil },
	}
	cmd := NewRootCmdWith(deps)
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"update"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("want a refusal")
	}
	if !strings.Contains(err.Error(), "brew upgrade --cask den") {
		t.Fatalf("the refusal must name the brew command, got %q", err)
	}
	if f.called {
		t.Fatal("the command reached the network before refusing")
	}
}

func TestUpdateRefusesALocalBuild(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "den")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	deps := Deps{
		Updater:    &stubFetcher{},
		DenVersion: func() string { return "v1.5.0-17-g0ec48d8-dirty" },
		Executable: func() (string, error) { return target, nil },
	}
	cmd := NewRootCmdWith(deps)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"update"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "not a released build") {
		t.Fatalf("want the non-release refusal, got %v", err)
	}
}

func TestUpdateTakesNoArguments(t *testing.T) {
	cmd := NewRootCmdWith(Deps{Updater: &stubFetcher{}})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"update", "v1.9.0"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("`den update` takes no arguments — pinning is install.sh's job")
	}
}
```

- [ ] **Step 2: Run the tests and verify they fail**

Run: `go test ./internal/cli/ -run TestUpdate -count=1`
Expected: a **compile** failure, not a test failure — `unknown field Updater in struct literal` (and
the same for `Executable`). The whole package fails to build, so no individual test result is
printed. That is the expected state at this step; the fields arrive in Step 3.

- [ ] **Step 3: Add the two Deps fields**

In `internal/cli/root.go`, inside the `Deps` struct, after `DenVersion`:

```go
	// Updater fetches release metadata and archives for `den update`. Injected
	// for the reason Scanner is: the real one SPEAKS HTTP, so a suite that
	// inherited it would reach github.com on every run, and its verdict would
	// depend on whatever release is public that day.
	Updater selfupdate.Fetcher
	// Executable answers where the running binary lives, so `den update` can
	// tell a Homebrew install from an archive one. Injected because the real
	// one (os.Executable + EvalSymlinks) answers the TEST BINARY's path under
	// `go test`, which classifies as neither of the cases worth testing.
	Executable func() (string, error)
```

In `SystemDeps()`, after `DenVersion: displayVersion,`:

```go
		Updater: selfupdate.NewHTTPFetcher(),
		// EvalSymlinks is not optional: on darwin os.Executable answers the
		// SYMLINK (measured 2026-08-18), and /opt/homebrew/bin/den is a symlink
		// into the Caskroom — without the resolution a brew install would be
		// classified as an archive one and overwritten.
		Executable: func() (string, error) {
			exe, err := os.Executable()
			if err != nil {
				return "", err
			}
			return filepath.EvalSymlinks(exe)
		},
```

Add `"path/filepath"` and `"github.com/PillowPillow/den/internal/selfupdate"` to root.go's imports.

Register the command in `NewRootCmdWith`, after the `newLintCmd()` line:

```go
	// `den update` gets its fetcher and its own path from Deps, for the same
	// reason `den ports` gets Scanner and Open: both real implementations touch
	// the machine — one speaks HTTP, the other reads where this process came
	// from.
	root.AddCommand(newUpdateCmd(deps))
```

- [ ] **Step 4: Write the command**

```go
package cli

import (
	"fmt"
	"runtime"

	"github.com/PillowPillow/den/internal/selfupdate"
	"github.com/spf13/cobra"
)

// newUpdateCmd is wiring and nothing else: the classification, the refusals,
// the download and the swap all live in internal/selfupdate, which is what
// keeps `net/http` out of this package (internal/ports/hermeticity_test.go).
//
// No flags, on purpose. `den update` moves this binary to the latest release or
// says why it will not; pinning a version and rolling back stay install.sh's
// job, where DEN_VERSION already does it.
func newUpdateCmd(d Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Update den to the latest release (not for Homebrew or go install)",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if d.Updater == nil {
				return fmt.Errorf("`den update` was wired without a fetcher — this is a den defect")
			}
			if d.Executable == nil {
				return fmt.Errorf("`den update` was wired without an executable probe — this is a den defect")
			}
			exe, err := d.Executable()
			if err != nil {
				return fmt.Errorf("cannot tell where den is installed (%v) — reinstall with install.sh, "+
					"or `brew upgrade --cask den` under Homebrew", err)
			}
			version := "dev"
			if d.DenVersion != nil {
				version = d.DenVersion()
			}
			req := selfupdate.Request{
				ExecPath: exe,
				Current:  version,
				GOOS:     runtime.GOOS,
				GOARCH:   runtime.GOARCH,
				Env:      selfupdate.EnvFromOS(d.Getenv),
			}
			return selfupdate.Run(cmd.Context(), d.Updater, req, cmd.OutOrStdout())
		},
	}
}
```

- [ ] **Step 5: Add EnvFromOS to the package**

In `internal/selfupdate/classify.go`, at the bottom:

```go
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
```

- [ ] **Step 6: Run the tests and verify they pass**

Run: `go test ./internal/cli/ ./internal/selfupdate/ -count=1`
Expected: PASS.

- [ ] **Step 7: Verify the hermeticity guard explicitly**

Run: `go test ./internal/ports/ -run Hermeticity -count=1 -v`
Expected: PASS — `internal/cli` still imports none of `net`, `hash/fnv`, `os/exec`.

- [ ] **Step 8: Run the full gate**

Run: `task check`
Expected: green.

- [ ] **Step 9: Commit**

```bash
git add internal/cli/update.go internal/cli/update_test.go internal/cli/root.go internal/selfupdate/classify.go
git commit -m "feat(cli): den update, refusing every install another manager owns"
```

---

### Task 7: Documentation

**Files:**
- Modify: `README.md` (Installation section, and the commands table)

**Interfaces:**
- Consumes: the command from Task 6.
- Produces: nothing consumed by later tasks.

- [ ] **Step 1: Add an "Updating" subsection to the README**

Insert it directly after the Installation section's last paragraph (the one ending "the documented tell that the build skipped `task`."):

```markdown
## Updating

Update the way you installed:

| Installed with | Update with |
|---|---|
| Homebrew | `brew upgrade --cask den` |
| `go install` | `go install github.com/PillowPillow/den/cmd/den@latest` |
| `install.sh`, or a release archive | `den update` — or re-run the same `curl … \| sh`, which updates in place |
| `task build` from a checkout | `git pull && task build` |

`den update` replaces the running binary with the latest release: it resolves the tag, verifies the
published sha256 (refusing on a mismatch, like `install.sh`), and swaps the binary through a single
atomic rename, so an update while den is running cannot leave a half-written file on your PATH.

It refuses, naming the right command, when another package manager owns the binary — Homebrew or the
go toolchain — because overwriting their file would leave them managing a version they no longer
manage. It also refuses to overwrite a build from a checkout. There are no flags: pin a version or
roll back with `DEN_VERSION=v1.0.1` on `install.sh`, as above.
```

- [ ] **Step 2: Add the command to the commands table**

In the `## Available commands` table, after the `den lint` row (keep the existing column style):

```markdown
| `den update` | updates den to the latest release; refuses when Homebrew or `go install` owns the binary, naming their command |
```

- [ ] **Step 3: Verify the claims against the code**

Run: `go run ./cmd/den update --help`
Expected: the `Short` string matches what the README promises. Read both side by side; the README must not describe a flag that does not exist.

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "docs: how to update den, per install path"
```

---

### Task 8: The CI smoke job

**Files:**
- Modify: `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: the built `den` binary and the published `v1.0.0` release.
- Produces: nothing consumed by later tasks.

- [ ] **Step 1: Add the job**

After the existing `install-script` job (keep its indentation style), add:

```yaml
  # The network twin of `install-script`, from the other side: that job proves
  # install.sh can still read the release layout, this one proves `den update`
  # can. Both would break on a change to .goreleaser.yaml's
  # archives.name_template, and nothing offline can catch that.
  update-command:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - name: install an older release, then update it
        run: |
          # v1.0.0 is PINNED and does not need bumping: it is the oldest
          # published release, so it stays older than whatever is latest, for
          # every release to come. It only becomes a decision again if its
          # archives disappear or stop being readable by install.sh — the two
          # cases where this job SHOULD fail loudly.
          DEN_INSTALL_DIR="$RUNNER_TEMP/upd" DEN_VERSION=v1.0.0 sh install.sh
          test "$("$RUNNER_TEMP/upd/den" version)" = "den v1.0.0"
          "$RUNNER_TEMP/upd/den" update
          # Resolved on both sides of the update, like the install-script job:
          # a release landing mid-run must not fail the assert.
          before=$(curl -fsSLI -o /dev/null -w '%{url_effective}' https://github.com/PillowPillow/den/releases/latest)
          before="${before##*/}"
          got=$("$RUNNER_TEMP/upd/den" version)
          after=$(curl -fsSLI -o /dev/null -w '%{url_effective}' https://github.com/PillowPillow/den/releases/latest)
          after="${after##*/}"
          if [ "$got" != "den $before" ] && [ "$got" != "den $after" ]; then
            echo "den version answered '$got', expected 'den $before' or 'den $after'"
            exit 1
          fi
      - name: refusal leaves the binary untouched
        run: |
          # A go-install-shaped path: the refusal must fire and change nothing.
          mkdir -p "$RUNNER_TEMP/go/bin"
          cp "$RUNNER_TEMP/upd/den" "$RUNNER_TEMP/go/bin/den"
          before=$(sha256sum "$RUNNER_TEMP/go/bin/den" | cut -d' ' -f1)
          if GOPATH="$RUNNER_TEMP/go" "$RUNNER_TEMP/go/bin/den" update; then
            echo "expected a refusal for a go-install path"; exit 1
          fi
          after=$(sha256sum "$RUNNER_TEMP/go/bin/den" | cut -d' ' -f1)
          test "$before" = "$after"
          # `test ! -e "<dir>/.den.new."*` would be a trap: an unmatched glob
          # reaches test unexpanded and PASSES for the wrong reason, and two
          # matches make it an argument error. ls decides on the exit status.
          if ls "$RUNNER_TEMP"/go/bin/.den.new.* >/dev/null 2>&1; then
            echo "a staging file survived the refusal"; exit 1
          fi
```

- [ ] **Step 2: Check the workflow parses**

Run: `python3 -c "import yaml,sys; yaml.safe_load(open('.github/workflows/ci.yml'))" && echo ok`
Expected: `ok`.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: smoke den update against the real release layout"
```

- [ ] **Step 4: Open the pull request**

```bash
git push -u origin docs/den-update-design
gh pr create --title "feat: den update, the update path outside Homebrew" \
  --body "Implements docs/superpowers/specs/2026-08-18-den-update-command-design.md"
```

Note for the executor: the CI smoke job only proves itself once the PR runs. If `update-command`
fails on the download step, check first whether a release landed mid-run — that is the documented
flake of its sibling job — and re-run before suspecting the code.
