package prompt

// This guard holds den's dependency shape for the huh work, and it asserts TWO
// things because either alone is worthless:
//
//  1. Only internal/prompt/huhui imports github.com/charmbracelet/*.
//  2. Only internal/cli imports internal/prompt/huhui.
//
// Without (2), internal/spawn could import the adapter directly and the whole
// reason internal/prompt exists as a leaf — keeping the checklist's package
// free of a 26-module graph — would be an aspiration no test defends. The
// promise in the design is "one package in den knows the name huh"; (1) alone
// promises only "one package says it out loud".
//
// SYNTAX ANALYSIS (go/build + go/parser), not a shell-out to `go list`, and the
// same documented limit as internal/ports/hermeticity_test.go: build.ImportDir
// applies THIS machine's GOOS/GOARCH, so a platform-restricted file would be
// invisible to this guard when run elsewhere.
import (
	"errors"
	"go/build"
	"go/parser"
	"go/token"
	"io/fs"
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

// huhuiPackage is the one package allowed to name the library, and cliPackage
// the one allowed to name huhui. Written as import paths relative to the
// module, resolved against modulePath so this file never hardcodes
// "github.com/PillowPillow/den".
const (
	huhuiPackage  = "internal/prompt/huhui"
	cliPackage    = "internal/cli"
	libraryPrefix = "github.com/charmbracelet/"
)

func TestOnlyTheAdapterKnowsTheLibrary(t *testing.T) {
	root := moduleRoot(t)
	module := modulePath(t, root)
	adapterImport := module + "/" + huhuiPackage

	var scanned int
	for _, top := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				return nil
			}
			// testdata holds fixtures, not den's own packages, and some of
			// them are deliberately malformed.
			if d.Name() == "testdata" {
				return fs.SkipDir
			}
			imports, files := importsOfDir(t, path)
			if files == 0 {
				return nil
			}
			scanned++
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				t.Fatalf("relativizing %s: %v", path, relErr)
			}
			pkg := filepath.ToSlash(rel)
			for _, imp := range imports {
				if strings.HasPrefix(imp, libraryPrefix) && pkg != huhuiPackage {
					t.Errorf("%s imports %s: only %s may name the TUI library — "+
						"speak to prompt.Prompter instead, and let %s render",
						pkg, imp, huhuiPackage, huhuiPackage)
				}
				if imp == adapterImport && pkg != cliPackage {
					t.Errorf("%s imports %s: only %s wires the adapter — "+
						"importing it here drags 26 modules into a package that "+
						"only needs the prompt.Prompter interface",
						pkg, imp, cliPackage)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", top, err)
		}
	}
	// Guard on the guard: a walk that parsed nothing would make both
	// assertions above vacuously true.
	if scanned < 10 {
		t.Fatalf("only %d packages scanned — the walk is not finding den's tree", scanned)
	}
}
