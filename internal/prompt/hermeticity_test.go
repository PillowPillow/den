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

// moduleRootLabel names the module's root package (embed.go, "package den")
// in error messages below. It never collides with a walk-derived package
// path — those are always filepath.Rel results at least one segment deep —
// so a reader can tell at a glance which failure is the root package's.
const moduleRootLabel = "."

// requiredRoots names the top-level directories den's own packages live
// under. Declared separately from the walk's own root list below — not
// reused from it — so that if a future edit narrows what the walk actually
// visits (drops "cmd" from that list, say), this list still expects every
// entry here to have contributed at least one package. That mismatch then
// fails BY NAME instead of being absorbed into the overall scanned<10 floor,
// where it would show up only as a smaller-than-expected number, not a
// named root nobody is checking any more. A single shared list would defeat
// this on its own: dropping a root from it would remove the root from the
// walk AND from the floor check in the same edit, so the floor would never
// notice anything missing.
//
// The protection this buys is ONE-DIRECTIONAL, not a general reconciliation
// between the two lists: a root added to the walk's list without a matching
// entry here is scanned and still gets both assertions applied, but
// perRootScanned carries an entry this floor never reads, so a later,
// silent break of that root's walk would go uncaught. Not live today — both
// lists hold the same two literals, and no third top-level directory in
// this module carries Go files — but whoever adds a third root must add it
// to BOTH lists, and nothing here will remind them to touch this one.
var requiredRoots = []string{"internal", "cmd"}

func TestOnlyTheAdapterKnowsTheLibrary(t *testing.T) {
	root := moduleRoot(t)
	module := modulePath(t, root)
	adapterImport := module + "/" + huhuiPackage

	// huhuiImportsLibrary is the positive floor for assertion 1: it proves
	// libraryPrefix still describes something real. Without it, if den ever
	// moved off charmbracelet, assertion 1 would pass forever while
	// protecting nothing — silently, unlike every other drift this file
	// catches. Mirrors internal/ports/hermeticity_test.go's own anchor on
	// internal/worktree (that file's TestSpawnDoesNotTransitivelyDependOnPorts).
	huhuiImportsLibrary := false

	// check applies both assertions to one package's imports, named by pkg
	// (a module-relative slash path, or moduleRootLabel for the module root).
	// Shared by the module-root check below and the internal/+cmd walk, so
	// the module root gets the exact same two assertions as everything else.
	check := func(pkg string, imports []string) {
		for _, imp := range imports {
			if strings.HasPrefix(imp, libraryPrefix) {
				if pkg == huhuiPackage {
					huhuiImportsLibrary = true
				} else {
					t.Errorf("%s imports %s: only %s may name the TUI library — "+
						"speak to prompt.Prompter instead, and let %s render",
						pkg, imp, huhuiPackage, huhuiPackage)
				}
			}
			if imp == adapterImport && pkg != cliPackage {
				t.Errorf("%s imports %s: only %s wires the adapter — "+
					"importing it here drags 26 modules into a package that "+
					"only needs the prompt.Prompter interface",
					pkg, imp, cliPackage)
			}
		}
	}

	var scanned int

	// The module root package sits above internal/ and cmd/, so the walk
	// below never visits it — yet it is not a stray corner: go:embed cannot
	// climb out of its own directory (embed.go's own comment explains why
	// ExampleDenHome had to move there), and internal/cli/init.go imports it,
	// putting it inside the dependency closure of the one package the design
	// trusts with huhui. Checked once, non-recursively — importsOfDir already
	// reads a single directory's files, never a subtree, so this is one call,
	// not a root-anchored WalkDir (which would also sweep in the full shadow
	// checkouts under .claude/worktrees/, per CLAUDE.md).
	rootImports, rootFiles := importsOfDir(t, root)
	if rootFiles > 0 {
		scanned++
		check(moduleRootLabel, rootImports)
	}

	// perRootScanned records how many packages the walk found under each
	// root it actually visited. Checked below against requiredRoots — a
	// SEPARATE list — rather than against whatever this range happened to
	// iterate, on purpose (see requiredRoots's own comment).
	perRootScanned := map[string]int{}
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
			perRootScanned[top]++
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				t.Fatalf("relativizing %s: %v", path, relErr)
			}
			pkg := filepath.ToSlash(rel)
			check(pkg, imports)
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", top, err)
		}
	}

	// Guard on the guard, per root: the overall scanned<10 floor below only
	// proves the walk found packages SOMEWHERE — it would still pass with,
	// say, 19 packages under internal/ and zero under cmd/ if the cmd/
	// iteration silently disappeared (a wrong path join, a typo, an edit
	// that narrowed the walk's root list). Each entry in requiredRoots must
	// have contributed at least one package on its own.
	for _, top := range requiredRoots {
		if perRootScanned[top] == 0 {
			t.Fatalf("no packages scanned under %s — the walk over that root is not finding den's tree", top)
		}
	}

	if !huhuiImportsLibrary {
		t.Fatalf("%s does not import anything under %s — either the adapter "+
			"stopped using the library, or libraryPrefix is stale and this "+
			"guard is no longer protecting anything it claims to",
			huhuiPackage, libraryPrefix)
	}

	// Guard on the guard: a walk that parsed nothing would make both
	// assertions above vacuously true.
	if scanned < 10 {
		t.Fatalf("only %d packages scanned — the walk is not finding den's tree", scanned)
	}
}
