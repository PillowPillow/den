package ports

// Two guard assertions locking the layering the spec (HANDOFF §8) and the
// spawn design (2026-07-28-den-plan2-spawn.md) both rely on:
//
//  1. internal/spawn never depends, directly or transitively, on
//     internal/ports — publication stays on demand: no spawn path may bind a
//     host port itself, only `den ports` does. Mirrors C18: `go list -deps
//     ./internal/spawn | grep -c den/internal/ports` (expects 0).
//  2. internal/cli's non-test files import neither `net`, nor `hash/fnv`,
//     nor `os/exec` — every port decision (window, offsets, shift, refusals)
//     and every raw system access for ports lives in internal/ports, never
//     in the CLI wiring/display layer. Mirrors C19: `go list -f '{{join
//     .Imports "\n"}}' ./internal/cli | grep -qE
//     '^(net|hash/fnv|os/exec)$'` (expects no match).
//
// SYNTAX ANALYSIS (go/build + go/parser), NOT a shell-out to `go list` —
// modeled on internal/worktree/hermeticity_test.go: `go/build.ImportDir`
// applies this machine's build constraints before returning which files
// belong to a package (so a platform-restricted file cannot hide behind an
// unevaluated `//go:build` tag), and `go/parser` reads only the import
// declarations of those files, never the whole call graph — enough to answer
// "what does this file import" without the extra dependency
// (golang.org/x/tools/go/packages) this project does not allow (stdlib +
// cobra + yaml.v3 only).
//
// Documented limit: build.ImportDir applies THIS machine's GOOS/GOARCH, so a
// platform-restricted file (an internal/cli/ports_linux.go importing `net`,
// say) would be invisible to this guard when run on another platform. Same
// limitation as C19's own `go list -f '{{.Imports}}'` script — test and
// criterion agree exactly, neither is stronger than the other.
import (
	"errors"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// moduleRoot climbs from the current directory up to the one holding go.mod.
// `go test` runs each package from ITS OWN directory, not the module root, so
// this walk finds it rather than assuming a fixed relative path.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found walking up from %s", dir)
		}
		dir = parent
	}
}

// modulePath reads the `module` directive from go.mod, so import paths under
// it can be turned back into directories on disk (and vice versa) without
// hardcoding "github.com/PillowPillow/den" in this file.
func modulePath(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			return strings.TrimSpace(rest)
		}
	}
	t.Fatal("go.mod: no `module` line found")
	return ""
}

// importsOfDir returns the import paths declared by the non-test .go files
// THIS MACHINE would actually compile for the package at dir (build
// constraints evaluated by go/build.ImportDir, never a plain os.ReadDir plus
// a suffix filter), read via go/parser rather than a substring/regexp scan.
// filesParsed lets callers assert the scan found something to parse at all —
// a silently empty package would make an "absence of X" assertion vacuous.
func importsOfDir(t *testing.T, dir string) (imports []string, filesParsed int) {
	t.Helper()

	pkg, err := build.ImportDir(dir, 0)
	if err != nil {
		var noGoSource *build.NoGoError
		if errors.As(err, &noGoSource) {
			return nil, 0
		}
		t.Fatalf("analyzing package at %s: %v", dir, err)
	}

	fset := token.NewFileSet()
	seen := map[string]bool{}
	for _, name := range pkg.GoFiles { // GoFiles only: never TestGoFiles/XTestGoFiles.
		path := filepath.Join(dir, name)
		fileAST, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		filesParsed++
		for _, imp := range fileAST.Imports {
			value, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("unquoting import in %s: %v", path, err)
			}
			if !seen[value] {
				seen[value] = true
				imports = append(imports, value)
			}
		}
	}
	return imports, filesParsed
}

// TestSpawnDoesNotTransitivelyDependOnPorts locks C18 / HANDOFF's "publication
// stays on demand": internal/spawn must never import internal/ports, directly
// or through any of its own internal/ dependencies, because that would mean
// some spawn path binds a host port itself instead of leaving publication to
// `den ports`.
func TestSpawnDoesNotTransitivelyDependOnPorts(t *testing.T) {
	root := moduleRoot(t)
	mod := modulePath(t, root)

	visited := map[string]bool{}
	var walk func(importPath string)
	walk = func(importPath string) {
		if visited[importPath] {
			return
		}
		visited[importPath] = true
		rel := strings.TrimPrefix(importPath, mod+"/")
		dir := filepath.Join(root, filepath.FromSlash(rel))
		imports, _ := importsOfDir(t, dir)
		for _, imp := range imports {
			if strings.HasPrefix(imp, mod+"/") {
				walk(imp)
			}
		}
	}
	walk(mod + "/internal/spawn")
	delete(visited, mod+"/internal/spawn")

	// Positive floor: prove the walk actually walked somewhere before
	// trusting that it found no internal/ports — an empty/broken walk (a
	// wrong dir join, say) would make the check below pass for the wrong
	// reason. internal/spawn is known to depend on internal/worktree
	// (worktree.Ensure backs `den <nest> -w`).
	if !visited[mod+"/internal/worktree"] {
		t.Fatalf("scan did not reach internal/spawn's known dependency internal/worktree — either the walk itself is broken (not the layering it is meant to guard), or spawn's dependency set changed and this anchor needs re-pointing to a package spawn really imports; visited=%v", visited)
	}

	if visited[mod+"/internal/ports"] {
		t.Errorf("internal/spawn transitively depends on internal/ports: publication must stay on demand, no spawn may bind a host port itself; visited=%v", visited)
	}
}

// TestCliImportsNoRawPortOrSystemAccess locks C19 / HANDOFF §8: internal/cli
// stays wiring and display only. Every port decision (window, offsets, shift,
// refusals) and every raw system access for ports lives in internal/ports, so
// internal/cli's non-test files must never import `net`, `hash/fnv`, or
// `os/exec` themselves.
func TestCliImportsNoRawPortOrSystemAccess(t *testing.T) {
	root := moduleRoot(t)
	dir := filepath.Join(root, "internal", "cli")

	imports, filesParsed := importsOfDir(t, dir)

	// Positive floor: prove files were actually parsed, or "no forbidden
	// import found" below would be vacuously true rather than meaningful.
	if filesParsed == 0 {
		t.Fatalf("no non-test .go files found under %s — the scan itself is broken", dir)
	}

	forbidden := map[string]bool{"net": true, "hash/fnv": true, "os/exec": true}
	for _, imp := range imports {
		if forbidden[imp] {
			t.Errorf("internal/cli imports %q directly: port decisions and raw system access must live in internal/ports (HANDOFF §8), not in cli", imp)
		}
	}
}
