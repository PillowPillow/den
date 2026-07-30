package worktree

import (
	"errors"
	"go/ast"
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

// TestRequiresNeutralizeGitEnvironmentWhenGitIsRunDirectly scans internal/ and
// cmd/: any package whose test files run git DIRECTLY must have a TestMain
// that CALLS NeutralizeGitEnvironment.
//
// This guard exists because the same class of bug reopened TWICE after its
// first fix (see NeutralizeGitEnvironment's godoc in worktree.go):
// internal/worktree closed the hole for itself, then internal/cli reopened it
// unknowingly, then internal/spawn.
//
// SYNTAX ANALYSIS (go/ast), NOT A REGEXP ON RAW TEXT — a substring search for
// "NeutralizeGitEnvironment" matches a COMMENT citing the helper's name just
// as well as a real CALL, so it cannot tell the two apart. `go/parser` +
// `go/ast` answers the two questions that matter without ever confusing code
// with text: (a) does the file REALLY call `exec.Command`/
// `exec.CommandContext` with "git" as a string literal (never a comment,
// never a built string)? (b) does a `TestMain` BODY REALLY call
// `NeutralizeGitEnvironment`?
//
// Documented limit: only `exec.Command`/`exec.CommandContext` with "git" as a
// literal are detected — an indirect call (variable, constant, third-party
// helper shelling out to git) would escape this guard. Deliberate trade-off:
// these two forms cover the three real occurrences seen so far
// (internal/worktree, internal/cli, internal/spawn), and resolving
// indirections would require full type analysis
// (golang.org/x/tools/go/packages), a dependency this project does not allow
// (stdlib + cobra + yaml.v3 only).
//
// THE FILES ANALYZED ARE THE ONES `go test` WOULD ACTUALLY COMPILE ON THIS
// MACHINE — `go/build.ImportDir` (not a plain `os.ReadDir` plus a suffix
// filter) applies build constraints (`//go:build`, `_linux.go` name suffixes,
// ...) for the current GOOS/GOARCH before returning the list. Otherwise a
// `TestMain` restricted to another platform (`//go:build darwin`, say) would
// make a package look protected on Linux when the actually-compiled test
// binary has no `TestMain` at all.
func TestRequiresNeutralizeGitEnvironmentWhenGitIsRunDirectly(t *testing.T) {
	root := moduleRoot(t)

	for _, subtree := range []string{"internal", "cmd"} {
		start := filepath.Join(root, subtree)
		entries, err := os.ReadDir(start)
		if err != nil {
			continue // subtree absent: nothing to scan
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			checkPackageIsHermetic(t, filepath.Join(start, e.Name()))
		}
	}
}

func checkPackageIsHermetic(t *testing.T, pkgDir string) {
	t.Helper()

	// build.ImportDir, not os.ReadDir: only the files THIS machine would
	// actually compile for this package (build constraints evaluated).
	pkg, err := build.ImportDir(pkgDir, 0)
	if err != nil {
		var noGoSource *build.NoGoError
		if errors.As(err, &noGoSource) {
			return // no .go files for this platform here: nothing to check
		}
		// Error we don't know how to interpret (malformed constraint,
		// inconsistent package, ...): fail-closed, like the rest of the
		// project — a lone t.Errorf on the offending package rather than a
		// t.Fatal that would cut the scan short before the other packages
		// get a chance to report.
		t.Errorf("analyzing package %s: %v", pkgDir, err)
		return
	}

	fset := token.NewFileSet()
	var rawGitCalls []string
	hasHelper := false

	names := make([]string, 0, len(pkg.TestGoFiles)+len(pkg.XTestGoFiles))
	names = append(names, pkg.TestGoFiles...)
	names = append(names, pkg.XTestGoFiles...)

	for _, name := range names {
		path := filepath.Join(pkgDir, name)
		fileAST, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			// Isolated to the offending file: the rest of the scan
			// continues, for a complete diagnostic in a single pass.
			t.Errorf("parsing %s: %v", path, err)
			continue
		}

		if execAlias, imported := aliasImport(fileAST, "os/exec"); imported {
			if callsGitDirectly(fileAST, execAlias) {
				rawGitCalls = append(rawGitCalls, path)
			}
		}
		if callsHelperFromTestMain(fileAST) {
			hasHelper = true
		}
	}

	if len(rawGitCalls) > 0 && !hasHelper {
		t.Errorf(
			"%s: runs git directly in its tests (%v) without a TestMain that "+
				"CALLS NeutralizeGitEnvironment — add a TestMain modeled on "+
				"internal/cli/main_test.go, or risk writing into the repo "+
				"designated by GIT_DIR/GIT_WORK_TREE when those variables are "+
				"exported (common under agents and git hooks)",
			pkgDir, rawGitCalls)
	}
}

// aliasImport returns the LOCAL name under which a file imports importPath,
// and whether that import is present. An explicit alias (`pkgexec "os/exec"`)
// wins; without one, the local name is the last segment of the path ("exec"
// for "os/exec").
func aliasImport(file *ast.File, importPath string) (alias string, ok bool) {
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path != importPath {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name, true
		}
		segments := strings.Split(path, "/")
		return segments[len(segments)-1], true
	}
	return "", false
}

// callsGitDirectly reports whether the file contains a REAL call (an
// *ast.CallExpr, never a comment or a string) to exec.Command("git", ...) or
// exec.CommandContext(ctx, "git", ...), with "git" as a string LITERAL — a
// variable or constant named otherwise escapes this on purpose (see the
// documented limit on TestRequiresNeutralizeGitEnvironmentWhenGitIsRunDirectly).
func callsGitDirectly(file *ast.File, execAlias string) bool {
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		base, ok := sel.X.(*ast.Ident)
		if !ok || base.Name != execAlias {
			return true
		}
		// exec.Command(name, arg...): "git" is the first argument.
		// exec.CommandContext(ctx, name, arg...): "git" is the second.
		var nameIndex int
		switch sel.Sel.Name {
		case "Command":
			nameIndex = 0
		case "CommandContext":
			nameIndex = 1
		default:
			return true
		}
		if len(call.Args) <= nameIndex {
			return true
		}
		lit, ok := call.Args[nameIndex].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if value, err := strconv.Unquote(lit.Value); err == nil && value == "git" {
			found = true
		}
		return true
	})
	return found
}

// callsHelperFromTestMain reports whether the file declares a function named
// TestMain whose BODY contains a REAL call to NeutralizeGitEnvironment —
// qualified (worktree.NeutralizeGitEnvironment, for any package that imports
// worktree) or not (the worktree package calling itself unqualified). A mere
// MENTION of the name — in a godoc, a comment, a string — does not count:
// that is exactly what the regexp version used to confuse with a call.
func callsHelperFromTestMain(file *ast.File) bool {
	found := false
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "TestMain" || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				if fun.Name == "NeutralizeGitEnvironment" {
					found = true
				}
			case *ast.SelectorExpr:
				if fun.Sel.Name == "NeutralizeGitEnvironment" {
					found = true
				}
			}
			return true
		})
	}
	return found
}
