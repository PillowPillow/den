package build

// The guard assertion behind graph.go's package doc: "Nothing here spawns a
// process: den runs each stack's provisioning through sbx.Runner (Execute), an
// interface injected exactly as internal/ports isolates the socket bind behind
// Scanner."
//
// That sentence is this branch's HEADLINE INVARIANT — it is the whole reason
// the per-stack `build.sh` could be deleted. The old model's justification for
// the `Script`/`ExecScript` interface was circular (spec §6: "elle est
// intestable *parce qu'*elle est arbitraire"), and what replaced it is den
// owning five argv forms through sbx.Runner, with sbx.Fake already a production
// file for exactly that. If internal/build ever spawns a process itself, the
// package stops being replayable on a temporary directory and the untestable
// surface starts growing again with every user script — silently, since the
// existing tests would all still pass.
//
// internal/ports and internal/worktree each lock their own layering this way;
// this package had prose only.
//
// WHAT IS SCANNED, and why it is not `go list -deps`: internal/build's OWN
// non-test files' import declarations. `os/exec` is legitimately reachable
// TRANSITIVELY, through internal/sbx — which is the one package allowed to
// spawn sbx, and the point of the injection. Verified 2026-08-03:
// `go list -f '{{.Imports}}' ./internal/build` carries no os/exec, while
// `go list -deps ./internal/build` does. A deps-shaped guard would therefore be
// red on a correct tree, and the only way to green it would be to weaken it
// into meaninglessness.
//
// SYNTAX ANALYSIS (go/build + go/parser), not a shell-out to `go list` —
// modeled on internal/ports/hermeticity_test.go and internal/worktree's, and
// for the same reasons: `go/build.ImportDir` applies this machine's build
// constraints before returning which files belong to the package (so a
// platform-restricted file cannot hide behind an unevaluated `//go:build` tag),
// and `go/parser` reads only the import declarations, never the whole call
// graph — enough to answer "what does this file import" without the extra
// dependency (golang.org/x/tools/go/packages) this project does not allow
// (stdlib + cobra + yaml.v3 only). The helpers below are DUPLICATED from those
// two files rather than factored into a shared testutil package: they are ten
// lines each, and a shared helper would make three independent guards fail
// together on one refactor of the helper.
//
// Documented limit, identical to the sibling guards: build.ImportDir applies
// THIS machine's GOOS/GOARCH, so a platform-restricted file (an
// internal/build/execute_linux.go importing os/exec, say) would be invisible to
// this guard when run on another platform.
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

// importsOfDir returns the import paths declared by the non-test .go files THIS
// MACHINE would actually compile for the package at dir. filesParsed lets the
// caller assert the scan found something to parse at all — a silently empty
// package would make an "absence of X" assertion vacuous.
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

// TestBuildSpawnsNoProcessItself locks graph.go's package doc.
//
// The forbidden set, and what each one would mean:
//
//   - os/exec — den running something itself instead of going through
//     sbx.Runner. The whole sequence would stop being assertable against
//     sbx.Fake, which is the property that let the per-stack build.sh go.
//   - os/signal — a package that spawns nothing has no child to relay a signal
//     to; wanting one means a process appeared.
//   - syscall / golang.org/x/sys/unix — the same escape one layer down, which
//     an os/exec ban alone does not close.
//   - net / net/http — internal/build must not reach the network either. The
//     image inventory arrives through sbx.Runner (SbxImages), and everything a
//     BUILD downloads is fetched from INSIDE the VM, under the egress policy
//     (spec §6). A fetch on the HOST would bypass that policy entirely.
func TestBuildSpawnsNoProcessItself(t *testing.T) {
	root := moduleRoot(t)
	dir := filepath.Join(root, "internal", "build")

	imports, filesParsed := importsOfDir(t, dir)

	// Positive floor: prove files were actually parsed, or "no forbidden import
	// found" below would be vacuously true rather than meaningful.
	if filesParsed == 0 {
		t.Fatalf("no non-test .go files found under %s — the scan itself is broken", dir)
	}
	// Second floor, on the CONTENT of the scan: internal/build is known to
	// import internal/sbx (Execute's whole sequence goes through sbx.Runner). A
	// scan that parsed files but read their imports wrongly would pass the count
	// check above and still assert nothing.
	if !strings.Contains(strings.Join(imports, "\n"), "/internal/sbx") {
		t.Fatalf("scan did not find internal/build's known import of internal/sbx — "+
			"the scan itself is broken, not the layering it guards; imports=%v", imports)
	}

	forbidden := map[string]string{
		"os/exec":               "den must run every process through sbx.Runner, never itself",
		"os/signal":             "a package that spawns nothing has no child process to signal",
		"syscall":               "the same process escape one layer below os/exec",
		"golang.org/x/sys/unix": "the same process escape one layer below os/exec",
		"net":                   "a build fetches from INSIDE the VM, under the egress policy",
		"net/http":              "a build fetches from INSIDE the VM, under the egress policy",
	}
	for _, imp := range imports {
		if why, bad := forbidden[imp]; bad {
			t.Errorf("internal/build imports %q directly: %s (graph.go's package doc: "+
				"\"Nothing here spawns a process\")", imp, why)
		}
	}
}
