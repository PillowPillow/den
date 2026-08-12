package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/manifest"
	"github.com/PillowPillow/den/internal/sbx"
)

// The scripted name "api.feat12" already contains the substrings "api" and
// "feat12": a strings.Contains assertion on the WHOLE output would therefore
// be true even if the NEST/WORKTREE split were completely broken (the NAME
// column alone would satisfy it). Assertions are therefore made column by
// column, on the fields of each line.
func TestLsPrintsTheColumns(t *testing.T) {
	testDenHome(t) // nest "api" is declared there

	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api.feat12","agent":"shell","status":"running","workspaces":["/w/api","/p"]}]}`)},
	}}

	out, err := executeCmdWithSbx(t, f, "ls")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected a header and a data line; got:\n%s", out)
	}

	header := strings.Fields(lines[0])
	expectedHeader := []string{"NAME", "NEST", "INSTANCE", "WORKTREE", "STATUS", "WORKSPACES"}
	if len(header) != len(expectedHeader) {
		t.Fatalf("header = %v, expected %v", header, expectedHeader)
	}
	for i, col := range expectedHeader {
		if header[i] != col {
			t.Errorf("header column %d = %q, expected %q; full header: %v", i, header[i], col, header)
		}
	}

	fields := strings.Fields(lines[1])
	// NAME stays the full name; NEST and INSTANCE are the pieces SPLIT by
	// sbx.Sandbox.Nest()/Instance(), not found by accident inside NAME. This
	// sandbox carries no creation record, so WORKTREE falls back to the same
	// flattened component as INSTANCE — the only string den has left.
	expectedLine := []string{"api.feat12", "api", "feat12", "feat12", "running"}
	if len(fields) < len(expectedLine) {
		t.Fatalf("data line = %v, expected at least %v", fields, expectedLine)
	}
	for i, val := range expectedLine {
		if fields[i] != val {
			t.Errorf("column %d = %q, expected %q; full line: %v", i, fields[i], val, fields)
		}
	}

	// Age does not exist in sbx ls --json: never claim to know it.
	if strings.Contains(strings.ToUpper(out), "AGE") {
		t.Errorf("no age column must exist; got:\n%s", out)
	}
}

// A sandbox whose nest is not declared stays visible, but marked: hiding it
// would make a genuinely live VM disappear from view. A sandbox whose nest IS
// declared, on the other hand, must carry no mark — otherwise the mark would
// prove nothing.
func TestLsMarksUndeclaredSandboxes(t *testing.T) {
	testDenHome(t) // nest "api" is declared there, "unknown" is not

	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running"},{"name":"unknown","status":"running"}]}`)},
	}}

	out, err := executeCmdWithSbx(t, f, "ls")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "unknown") {
		t.Errorf("an undeclared sandbox must stay visible; got:\n%s", out)
	}

	var lineAPI, lineUnknown string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "api":
			lineAPI = line
		case "unknown":
			lineUnknown = line
		}
	}
	if lineAPI == "" || lineUnknown == "" {
		t.Fatalf("both sandboxes must each appear on a line; got:\n%s", out)
	}
	if strings.Contains(lineAPI, "?") {
		t.Errorf("the declared nest must carry no mark; line: %q", lineAPI)
	}
	if !strings.Contains(lineUnknown, "?") {
		t.Errorf("the undeclared nest must be marked; line: %q", lineUnknown)
	}
}

// `den ls` must neither fail nor hide a live VM because of a broken nest, but
// it must WARN on stderr, naming it — the debt found while reviewing T13:
// without this, a single broken nest marks "?" on every sandbox, including
// those with a valid nest, with no signal at all. The test checks both
// directions: the broken nest's name is on stderr and NOT on stdout; the
// sandbox whose nest is healthy does appear on stdout, without the "?" mark.
func TestLsReportsABrokenNestOnStderrWithoutHidingHealthyOnes(t *testing.T) {
	dir := testDenHome(t) // nest "api" is declared there, DEN_HOME points at it
	if err := os.WriteFile(filepath.Join(dir, "nests", "broken.yaml"), []byte("egres: [x]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running"}]}`)},
	}}

	stdout, stderr, err := executeCmdWithSbxSeparateStreams(t, f, "ls")
	if err != nil {
		t.Fatalf("den ls must never return an error for a broken nest: %v", err)
	}
	// Exact string, not just "broken": LoadNest itself already names the file
	// in its decode error (.../nests/broken.yaml), which would make
	// Contains(stderr, "broken") true even if den ls omitted bn.Name.
	if !strings.Contains(stderr, "nest broken unreadable:") {
		t.Errorf("the broken nest must be named on stderr; got:\n%s", stderr)
	}
	if strings.Contains(stdout, "broken") {
		t.Errorf("the broken nest's name must not appear on stdout; got:\n%s", stdout)
	}

	var lineAPI string
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == "api" {
			lineAPI = line
		}
	}
	if lineAPI == "" {
		t.Fatalf("sandbox 'api', whose nest is healthy, must appear on stdout; got:\n%s", stdout)
	}
	if strings.Contains(lineAPI, "?") {
		t.Errorf("healthy nest 'api' must carry no '?' mark because of its broken neighbor; line: %q", lineAPI)
	}
}

// den ls must warn on stderr when the nests/ directory itself is unreadable
// (a STRUCTURAL failure, ListNests' 3rd return value): it is the only signal
// that distinguishes "these sandboxes are not declared" from "I could not
// read the directory" — without it, every live sandbox ends up marked "?"
// with no explanation at all.
func TestLsReportsAnUnreadableNestsRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("unreliable as root: permissions are ignored")
	}
	dir := testDenHome(t) // nest "api" is declared there; nests/ already exists
	root := filepath.Join(dir, "nests")
	if err := os.Chmod(root, 0o000); err != nil {
		t.Fatal(err)
	}
	// EMPIRICAL check, following TestListNestsUnreadableRoot's model: a chmod
	// 0o000 is not enough everywhere (root, containers, some CFS setups also
	// ignore permissions).
	if _, err := os.ReadDir(root); err == nil {
		if err := os.Chmod(root, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Skip("reading a 0o000 directory succeeds on this environment: unreliable here")
	}
	t.Cleanup(func() {
		// t.TempDir() must be able to clean up behind us.
		if err := os.Chmod(root, 0o755); err != nil {
			t.Fatal(err)
		}
	})

	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running"}]}`)},
	}}

	stdout, stderr, err := executeCmdWithSbxSeparateStreams(t, f, "ls")
	if err != nil {
		t.Fatalf("den ls must never return an error for an unreadable nests/ root: %v", err)
	}
	if !strings.Contains(stderr, "listing nests") {
		t.Errorf("the structural failure must be reported on stderr; got:\n%s", stderr)
	}
	if strings.Contains(stdout, "listing nests") {
		t.Errorf("the warning must not appear on stdout; got:\n%s", stdout)
	}
}

func TestLsNoSandbox(t *testing.T) {
	testDenHome(t)

	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(`{"sandboxes":[]}`)},
	}}

	out, err := executeCmdWithSbx(t, f, "ls")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "no live sandbox") {
		t.Errorf("got:\n%s", out)
	}
	// No empty table: the absence message replaces the header, it is not added
	// above a zero-row table.
	if strings.Contains(out, "NAME") {
		t.Errorf("no table header must appear when the list is empty; got:\n%s", out)
	}
}

// lsManifest records a sandbox with one den-created worktree at the given
// mount. No directory is created: `den ls` reads the record, never the disk.
func lsManifest(t *testing.T, denHome, sandbox, mount string) {
	t.Helper()
	writeManifest(t, denHome, manifest.Manifest{
		Sandbox:  sandbox,
		Nest:     manifest.Nest{Ref: "api", File: filepath.Join(denHome, "nests", "api.yaml")},
		Worktree: &manifest.Worktree{Name: "feat12", Branch: "feat12", Layout: "central", Root: filepath.Dir(filepath.Dir(mount))},
		Repos: []manifest.Repo{{
			Name: "api", Origin: manifest.OriginPath, Repo: "/dev/api", Mount: mount, Worktree: true,
		}},
	})
}

// Proof 11 — `sbx create` failed after the worktrees existed. The record is
// the only trace, and `den ls` is where the user looks.
func TestLsSignalsAnOrphanedRecord(t *testing.T) {
	dir := testDenHome(t)
	mount := filepath.Join(dir, "worktrees", "feat12", "api")
	lsManifest(t, dir, "api.feat12", mount)

	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(`{"sandboxes":[{"name":"web","status":"running"}]}`)},
	}}

	out, err := executeCmdWithSbx(t, f, "ls")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "api.feat12") {
		t.Errorf("the orphaned record must be named; got:\n%s", out)
	}
	if !strings.Contains(out, mount) {
		t.Errorf("the directory left on disk must be named; got:\n%s", out)
	}
	// The live sandbox is still listed: the extra never replaces the table.
	if !strings.Contains(out, "web") {
		t.Errorf("the live sandbox must still be listed; got:\n%s", out)
	}
}

// "No live sandbox but four recorded worktrees" is exactly the state worth
// reporting, so the empty-list shortcut must not skip the scan.
func TestLsSignalsOrphansEvenWithNoLiveSandbox(t *testing.T) {
	dir := testDenHome(t)
	mount := filepath.Join(dir, "worktrees", "feat12", "api")
	lsManifest(t, dir, "api.feat12", mount)

	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(`{"sandboxes":[]}`)},
	}}

	out, err := executeCmdWithSbx(t, f, "ls")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "no live sandbox") {
		t.Errorf("the absence message must survive; got:\n%s", out)
	}
	if strings.Contains(out, "NAME") {
		t.Errorf("no table header must appear when the list is empty; got:\n%s", out)
	}
	if !strings.Contains(out, "api.feat12") || !strings.Contains(out, mount) {
		t.Errorf("the orphan must still be reported; got:\n%s", out)
	}
}

// `den ls` is the command you type when everything is broken. It must never
// fail over its own extra: fail-open, strictly.
func TestLsSurvivesACorruptRecord(t *testing.T) {
	dir := testDenHome(t)
	if err := os.MkdirAll(manifest.Dir(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifest.Dir(dir), "x.yaml"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(`{"sandboxes":[{"name":"api","status":"running"}]}`)},
	}}

	out, err := executeCmdWithSbx(t, f, "ls")
	if err != nil {
		t.Fatalf("den ls must never fail over a creation record: %v", err)
	}
	if !strings.Contains(out, "api") {
		t.Errorf("the live sandbox must still be listed; got:\n%s", out)
	}
}

// Finding 5 (PR #68 review) — an UNREADABLE record is not "no record". Before
// this fix, a corrupt record for a --as sandbox made `den ls` fall through to
// the no-record branch and print the label under WORKTREE, as though "reco"
// were a branch that exists in some repository. den does not know the
// worktree of a record it could not read, and must say so ("?") rather than
// guess.
// api.feat12 carries NO record at all, next to api.reco's unreadable one, on
// purpose: a fix that turned "unreadable" into a blanket `len(broken) > 0`
// flag (rather than a per-sandbox lookup) would pass a single-sandbox test
// just as well, since ANY broken file in state/sandboxes/ would trip it. With
// a second, record-less sandbox in the same run, that mutation prints "?" for
// api.feat12 too and this test catches it — while also being finding 5's own
// second checklist item, "no record at all keeps today's fallback", in the
// one shape TestLsPrintsTheColumns/TestLsFallsBackToTheSandboxNameWithoutARecord
// do not cover: state/sandboxes/ populated with someone ELSE's broken file.
func TestLsMarksAnUnreadableRecordAsUnknownWorktree(t *testing.T) {
	dir := testDenHome(t) // nest "api" is declared there
	if err := os.MkdirAll(manifest.Dir(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	// Hand-written, not writeManifest: the point of this test is a file
	// writeManifest could never produce — one that fails to decode. The
	// filename is exactly what manifest.Path would compose for "api.reco",
	// which is the mapping newLsCmd relies on to know THIS record is the
	// unreadable one.
	if err := os.WriteFile(filepath.Join(manifest.Dir(dir), "api.reco.yaml"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api.reco","status":"running"},` +
				`{"name":"api.feat12","status":"running"}]}`)},
	}}

	stdout, stderr, err := executeCmdWithSbxSeparateStreams(t, f, "ls")
	if err != nil {
		t.Fatalf("den ls must never fail over an unreadable record: %v", err)
	}

	var lineReco, lineFeat12 string
	for _, l := range strings.Split(stdout, "\n") {
		fields := strings.Fields(l)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "api.reco":
			lineReco = l
		case "api.feat12":
			lineFeat12 = l
		}
	}
	if lineReco == "" || lineFeat12 == "" {
		t.Fatalf("both sandboxes must each appear on a line; got:\n%s", stdout)
	}

	fields := strings.Fields(lineReco)
	expectedLine := []string{"api.reco", "api", "reco", "?", "running"}
	if len(fields) < len(expectedLine) {
		t.Fatalf("data line = %v, expected at least %v", fields, expectedLine)
	}
	for i, val := range expectedLine {
		if fields[i] != val {
			t.Errorf("column %d = %q, expected %q; full line: %v", i, fields[i], val, fields)
		}
	}

	// The sibling with no record at all must keep today's fallback — the
	// flattened component under WORKTREE, exactly as if api.reco's broken
	// file were not sitting right next to it in state/sandboxes/.
	fields = strings.Fields(lineFeat12)
	expectedLine = []string{"api.feat12", "api", "feat12", "feat12", "running"}
	if len(fields) < len(expectedLine) {
		t.Fatalf("data line = %v, expected at least %v", fields, expectedLine)
	}
	for i, val := range expectedLine {
		if fields[i] != val {
			t.Errorf("column %d = %q, expected %q; full line: %v", i, fields[i], val, fields)
		}
	}

	// The message names the FILE (den doctor's own dialect, reused rather
	// than inventing a second one — see the comment above unreadable in
	// ls.go), not the sandbox: "api.reco.yaml" is the one substring both
	// spellings would share, so it also proves the sandbox-name recovery
	// from the path landed on the right file.
	if !strings.Contains(stderr, "creation record") || !strings.Contains(stderr, "api.reco.yaml unreadable:") {
		t.Errorf("the unreadable record must be named on stderr; got:\n%s", stderr)
	}
	if strings.Contains(stdout, "unreadable") {
		t.Errorf("the warning must not appear on stdout; got:\n%s", stdout)
	}
}

// Flattening is lossy: `-w feature/12` becomes the sandbox api.feat-12, and
// before the record there was nowhere left to read "feature/12" from. The NEST
// column has the same problem in reverse — a source nest spawns as "corp-api"
// while the user only ever typed "corp:api".
func TestLsShowsTheBranchAsTypedAndThePrefixedNest(t *testing.T) {
	dir := testDenHome(t)
	// The source nest itself, so it is DECLARED: `den nest ls` lists it as a
	// first-class object, and the mark must agree with that listing rather
	// than with which local file happens to share its flattened name.
	writeUnder(t, dir, filepath.Join("sources", "corp", "nests", "api.yaml"),
		"stack: devx\nrepos:\n  - { path: /dev/api }\n")
	writeManifest(t, dir, manifest.Manifest{
		Sandbox: "corp-api.feat-12",
		Nest:    manifest.Nest{Ref: "corp:api", File: filepath.Join(dir, "sources", "corp", "nests", "api.yaml")},
		Worktree: &manifest.Worktree{
			Name: "feat-12", Branch: "feature/12", Layout: "central",
			Root: filepath.Join(dir, "worktrees"),
		},
		Repos: []manifest.Repo{{
			Name: "api", Origin: manifest.OriginKey, Key: "api", Repo: "/dev/api",
			Mount: filepath.Join(dir, "worktrees", "feat-12", "api"), Worktree: true,
		}},
	})

	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"corp-api.feat-12","status":"running"}]}`)},
	}}

	out, err := executeCmdWithSbx(t, f, "ls")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var line string
	for _, l := range strings.Split(out, "\n") {
		if fields := strings.Fields(l); len(fields) > 0 && fields[0] == "corp-api.feat-12" {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("the sandbox must be listed; got:\n%s", out)
	}
	if !strings.Contains(line, "corp:api") {
		t.Errorf("the NEST column must show the reference the user typed; line: %q", line)
	}
	if !strings.Contains(line, "feature/12") {
		t.Errorf("the WORKTREE column must show the branch as typed; line: %q", line)
	}
	// The record changes what is DISPLAYED, not what is judged: the mark keeps
	// being decided on the sandbox-derived name ("corp-api"), but `declared`
	// now folds in every installed source's nests under that same flattened
	// form — and sources/corp/nests/api.yaml above is exactly such a nest, so
	// it must carry no mark at all.
	if strings.Contains(line, "?") {
		t.Errorf("a nest declared in an installed source must carry no mark; line: %q", line)
	}
}

// Same live sandbox and the same manifest as the test above, but the source
// nest file itself is absent — the source was never installed, or it was
// removed after the sandbox was created. The mark must still fire: a
// manifest record only changes what is DISPLAYED (spec doctrine, see the
// test above), it is not proof that the nest still exists.
func TestLsMarksASourceSandboxWhoseSourceNestIsGone(t *testing.T) {
	dir := testDenHome(t)
	writeManifest(t, dir, manifest.Manifest{
		Sandbox: "corp-api.feat-12",
		Nest:    manifest.Nest{Ref: "corp:api", File: filepath.Join(dir, "sources", "corp", "nests", "api.yaml")},
		Worktree: &manifest.Worktree{
			Name: "feat-12", Branch: "feature/12", Layout: "central",
			Root: filepath.Join(dir, "worktrees"),
		},
		Repos: []manifest.Repo{{
			Name: "api", Origin: manifest.OriginKey, Key: "api", Repo: "/dev/api",
			Mount: filepath.Join(dir, "worktrees", "feat-12", "api"), Worktree: true,
		}},
	})

	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"corp-api.feat-12","status":"running"}]}`)},
	}}

	out, err := executeCmdWithSbx(t, f, "ls")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var line string
	for _, l := range strings.Split(out, "\n") {
		if fields := strings.Fields(l); len(fields) > 0 && fields[0] == "corp-api.feat-12" {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("the sandbox must be listed; got:\n%s", out)
	}
	if !strings.Contains(line, "corp:api ?") {
		t.Errorf("a source sandbox whose source nest no longer exists must still be marked; line: %q", line)
	}
}

// A sandbox named by --as carries a label in component 2, and a label is not a
// branch. With a record present, the WORKTREE column must read from the record
// alone: the pre-existing fallback on component 2 was written when that
// component could only ever be a flattened branch.
func TestLsDoesNotPrintTheInstanceAsAWorktree(t *testing.T) {
	denHome := testDenHome(t) // nest "api" is declared there
	// lsManifest is not used here: it hard-codes a Worktree block, and the
	// scenario this test proves is `--as` WITHOUT `-w` — the record spawn
	// itself writes for that case carries no Worktree block at all (see
	// TestInstanceWithoutWorktreeRecordsNoWorktreeBlock in internal/spawn).
	// A record claiming a worktree while Repos carries none is a shape only
	// a hand-edit could produce, so Repos here stays plain (Worktree: false).
	writeManifest(t, denHome, manifest.Manifest{
		Sandbox: "api.reco",
		Nest:    manifest.Nest{Ref: "api", File: filepath.Join(denHome, "nests", "api.yaml")},
		Repos: []manifest.Repo{{
			Name: "api", Origin: manifest.OriginPath, Repo: "/dev/api", Mount: "/w/api",
		}},
	})

	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api.reco","status":"running","workspaces":["/w/api"]}]}`)},
	}}

	out, err := executeCmdWithSbx(t, f, "ls")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	header := strings.Fields(lines[0])
	expectedHeader := []string{"NAME", "NEST", "INSTANCE", "WORKTREE", "STATUS", "WORKSPACES"}
	if len(header) != len(expectedHeader) {
		t.Fatalf("header = %v, expected %v", header, expectedHeader)
	}
	for i, col := range expectedHeader {
		if header[i] != col {
			t.Errorf("header column %d = %q, expected %q", i, header[i], col)
		}
	}

	fields := strings.Fields(lines[1])
	expectedLine := []string{"api.reco", "api", "reco", "-", "running"}
	if len(fields) < len(expectedLine) {
		t.Fatalf("data line = %v, expected at least %v", fields, expectedLine)
	}
	for i, val := range expectedLine {
		if fields[i] != val {
			t.Errorf("column %d = %q, expected %q; full line: %v", i, fields[i], val, fields)
		}
	}
}

// The flagship scenario of --as: a label AND a worktree together, on the same
// sandbox. INSTANCE and WORKTREE must show two DIFFERENT strings — the switch
// in newLsCmd has a third branch (hasRecord && m.Worktree != nil) that the two
// tests above never exercise, since one has no record and the other's record
// has no worktree block.
func TestLsShowsBothTheInstanceAndItsWorktree(t *testing.T) {
	denHome := testDenHome(t) // nest "api" is declared there
	writeManifest(t, denHome, manifest.Manifest{
		Sandbox: "api.reco",
		Nest:    manifest.Nest{Ref: "api", File: filepath.Join(denHome, "nests", "api.yaml")},
		Worktree: &manifest.Worktree{
			Name: "feature-123", Branch: "feature/123", Layout: "central",
			Root: filepath.Join(denHome, "worktrees"),
		},
		Repos: []manifest.Repo{{
			Name: "api", Origin: manifest.OriginPath, Repo: "/dev/api",
			Mount: filepath.Join(denHome, "worktrees", "feature-123", "api"), Worktree: true,
		}},
	})

	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api.reco","status":"running","workspaces":["/w/api"]}]}`)},
	}}

	out, err := executeCmdWithSbx(t, f, "ls")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	fields := strings.Fields(lines[1])
	expectedLine := []string{"api.reco", "api", "reco", "feature/123", "running"}
	if len(fields) < len(expectedLine) {
		t.Fatalf("data line = %v, expected at least %v", fields, expectedLine)
	}
	for i, val := range expectedLine {
		if fields[i] != val {
			t.Errorf("column %d = %q, expected %q; full line: %v", i, fields[i], val, fields)
		}
	}
}

// Fail-open: with no record den shows exactly what it showed before.
func TestLsFallsBackToTheSandboxNameWithoutARecord(t *testing.T) {
	testDenHome(t) // nest "api" is declared there

	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(`{"sandboxes":[{"name":"api.feat12","status":"running"}]}`)},
	}}

	out, err := executeCmdWithSbx(t, f, "ls")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "feat12") {
		t.Errorf("without a record the flattened component is all den has; got:\n%s", out)
	}
}
