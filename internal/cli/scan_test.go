package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/v2/internal/prompt"
	"github.com/PillowPillow/den/v2/internal/repos"
)

func newScanFixture(t *testing.T) *upFixture {
	t.Helper()
	f := newUpFixture(t)
	t.Setenv("HOME", t.TempDir())
	return f
}

func (f *upFixture) scan(args ...string) error { return f.up2("scan", args...) }

func scanClone(t *testing.T, dir string, urls ...string) string {
	t.Helper()
	var b strings.Builder
	for i, u := range urls {
		name := "origin"
		if i > 0 {
			name = "upstream"
		}
		b.WriteString("[remote \"" + name + "\"]\n\turl = " + u + "\n")
	}
	writeTestFile(t, filepath.Join(dir, ".git", "config"), b.String())
	return dir
}

func gl(name string) string { return "git@gitlab.com:dig/" + name + ".git" }

func (f *upFixture) table(t *testing.T) repos.Table {
	t.Helper()
	tb, err := repos.ReadTable(f.home)
	if err != nil {
		t.Fatal(err)
	}
	return tb
}

func (f *upFixture) seedTable(t *testing.T, entries map[string]string) {
	t.Helper()
	tb := repos.Table{Repos: map[string]repos.Entry{}}
	for id, p := range entries {
		tb.Repos[id] = repos.Entry{Path: p}
	}
	if err := repos.WriteTable(f.home, tb); err != nil {
		t.Fatal(err)
	}
}

func readOrEmpty(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

func TestScanWritesAdditionsWithYes(t *testing.T) {
	f := newScanFixture(t)
	api := scanClone(t, filepath.Join(f.src, "dev", "api"), gl("api"))
	if err := f.scan(f.src, "--yes"); err != nil {
		t.Fatal(err)
	}
	if got := f.table(t).Repos["gitlab.com/dig/api"].Path; got != api {
		t.Errorf("table maps gitlab.com/dig/api to %q, want %q", got, api)
	}
	if len(f.prompt.Confirms) != 0 {
		t.Errorf("asked %v with --yes and additions only", f.prompt.Confirms)
	}
}

func TestScanAsksOnATerminal(t *testing.T) {
	f := newScanFixture(t)
	f.tty = true
	f.prompt.ConfirmAnswers = []bool{true}
	api := scanClone(t, filepath.Join(f.src, "api"), gl("api"))
	if err := f.scan(f.src); err != nil {
		t.Fatal(err)
	}
	want := "write 1 addition and 0 replacements to " + repos.TablePath(f.home) + "?"
	if len(f.prompt.Confirms) != 1 || f.prompt.Confirms[0].Question != want {
		t.Fatalf("confirms %+v, want one %q", f.prompt.Confirms, want)
	}
	if got := f.table(t).Repos["gitlab.com/dig/api"].Path; got != api {
		t.Errorf("table maps gitlab.com/dig/api to %q, want %q", got, api)
	}
}

func TestScanSpellsOneOfEachInTheSingular(t *testing.T) {
	f := newScanFixture(t)
	scanClone(t, filepath.Join(f.src, "api"), gl("api"))
	err := f.scan(f.src)
	if want := "den scan found 1 change;"; err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error %v, want %q", err, want)
	}
	if err := f.scan(f.src, "--yes"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"and 1 other clone\n", "wrote 1 entry to " + repos.TablePath(f.home) + "\n"} {
		if !strings.Contains(f.out.String(), want) {
			t.Errorf("out %q lacks %q", f.out.String(), want)
		}
	}
	if want := " 1 clone\n"; !strings.Contains(f.err.String(), want) {
		t.Errorf("err %q lacks %q", f.err.String(), want)
	}
}

func TestScanWritesNothingWhenRefused(t *testing.T) {
	f := newScanFixture(t)
	f.tty = true
	f.prompt.ConfirmAnswers = []bool{false}
	scanClone(t, filepath.Join(f.src, "api"), gl("api"))
	if err := f.scan(f.src); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(repos.TablePath(f.home)); !os.IsNotExist(err) {
		t.Errorf("table written after a refusal (stat: %v)", err)
	}
	if !strings.Contains(f.out.String(), "nothing written") {
		t.Errorf("out %q lacks `nothing written`", f.out.String())
	}
}

func TestScanRefusesWithoutTerminalOrYes(t *testing.T) {
	f := newScanFixture(t)
	scanClone(t, filepath.Join(f.src, "api"), gl("api"))
	scanClone(t, filepath.Join(f.src, "web"), gl("web"))
	err := f.scan(f.src)
	want := "den scan found 2 changes; run it in a terminal, or with --yes to write the additions"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error %v, want %q", err, want)
	}
	if _, err := os.Stat(repos.TablePath(f.home)); !os.IsNotExist(err) {
		t.Errorf("table written without a terminal or --yes (stat: %v)", err)
	}
}

func TestScanNeverReplacesWithoutATerminal(t *testing.T) {
	f := newScanFixture(t)
	old := filepath.Join(f.src, "gone", "api")
	f.seedTable(t, map[string]string{"gitlab.com/dig/api": old})
	api := scanClone(t, filepath.Join(f.src, "dev", "api"), gl("api"))
	web := scanClone(t, filepath.Join(f.src, "dev", "web"), gl("web"))

	if err := f.scan(f.src, "--yes"); err != nil {
		t.Fatal(err)
	}
	tb := f.table(t)
	if got := tb.Repos["gitlab.com/dig/web"].Path; got != web {
		t.Errorf("addition: web maps to %q, want %q", got, web)
	}
	if got := tb.Repos["gitlab.com/dig/api"].Path; got != old {
		t.Errorf("replacement written without a terminal: api maps to %q, want the old %q", got, old)
	}
	line := "not written (needs a terminal): replace gitlab.com/dig/api: " + old + " → " + api
	if !strings.Contains(f.out.String(), line) {
		t.Errorf("out %q lacks %q", f.out.String(), line)
	}

	before := readOrEmpty(t, repos.TablePath(f.home))
	if err := f.scan(f.src, "--yes"); err != nil {
		t.Fatal(err)
	}
	if after := readOrEmpty(t, repos.TablePath(f.home)); after != before {
		t.Errorf("a plan of replacements only rewrote the table:\n%s\nthen\n%s", before, after)
	}
	if !strings.Contains(f.out.String(), line) {
		t.Errorf("out %q lacks %q", f.out.String(), line)
	}
	if len(f.prompt.Confirms) != 0 {
		t.Errorf("asked %v without a terminal", f.prompt.Confirms)
	}
}

func TestScanAsksBeforeReplacingEvenWithYes(t *testing.T) {
	f := newScanFixture(t)
	f.tty = true
	old := filepath.Join(f.src, "gone", "api")
	f.seedTable(t, map[string]string{"gitlab.com/dig/api": old})
	api := scanClone(t, filepath.Join(f.src, "dev", "api"), gl("api"))
	web := scanClone(t, filepath.Join(f.src, "dev", "web"), gl("web"))

	f.prompt.ConfirmAnswers = []bool{false}
	if err := f.scan(f.src, "--yes"); err != nil {
		t.Fatal(err)
	}
	if len(f.prompt.Confirms) != 1 || f.prompt.Confirms[0].Question != "replace 1 entry?" {
		t.Fatalf("confirms %+v, want one `replace 1 entry?`", f.prompt.Confirms)
	}
	tb := f.table(t)
	if tb.Repos["gitlab.com/dig/web"].Path != web || tb.Repos["gitlab.com/dig/api"].Path != old {
		t.Fatalf("after a refused replacement: %+v, want web added and api kept at %s", tb.Repos, old)
	}

	f.prompt.ConfirmAnswers = []bool{true}
	if err := f.scan(f.src, "--yes"); err != nil {
		t.Fatal(err)
	}
	if got := f.table(t).Repos["gitlab.com/dig/api"].Path; got != api {
		t.Errorf("accepted replacement: api maps to %q, want %q", got, api)
	}
}

func TestScanNeverPrintsOrStoresAToken(t *testing.T) {
	f := newScanFixture(t)
	writeTestFile(t, filepath.Join(f.proj, "den.yaml"), "schema: 1\nrepos:\n  ab: {origin: 'https://h/a/b'}\n")
	scanClone(t, filepath.Join(f.src, "one"), "https://u:s3cret@h/a/b")
	scanClone(t, filepath.Join(f.src, "two"), "https://u:s3cret@h/a/b")
	if err := f.scan(f.src, "--yes"); err != nil {
		t.Fatal(err)
	}
	if got := f.table(t).Repos["h/a/b"].Path; got != filepath.Join(f.src, "one") {
		t.Fatalf("table maps h/a/b to %q: the token remote was not recognized, so this test proves nothing", got)
	}
	for name, s := range map[string]string{
		"out": f.out.String(), "err": f.err.String(), "table": readOrEmpty(t, repos.TablePath(f.home)),
	} {
		if strings.Contains(s, "s3cret") {
			t.Errorf("%s carries the token:\n%s", name, s)
		}
	}
	if !strings.Contains(f.out.String(), "add h/a/b") || !strings.Contains(f.out.String(), "duplicate h/a/b") {
		t.Errorf("out %q: the identity lines were not printed, so the check above covers nothing", f.out.String())
	}
}

func TestScanReportsMissingDeclaredKeys(t *testing.T) {
	f := newScanFixture(t)
	global := "schema: 1\nrepos:\n" +
		"  lib: {origin: '" + gl("lib") + "', path: /s/lib}\n" +
		"  shared: /s/shared\n"
	project := "schema: 1\nrepos:\n" +
		"  api: {origin: '" + gl("api") + "'}\n" +
		"  web: {origin: '" + gl("web") + "'}\n" +
		"  shared: {origin: '" + gl("shared") + "'}\n"
	globalFile, projectFile := filepath.Join(f.home, "den.yaml"), filepath.Join(f.proj, "den.yaml")
	writeTestFile(t, globalFile, global)
	writeTestFile(t, projectFile, project)
	indexed := filepath.Join(t.TempDir(), "den.yaml")
	writeTestFile(t, indexed, "schema: 1\nrepos:\n  tools: {origin: '"+gl("tools")+"'}\n")
	writeTestFile(t, filepath.Join(f.home, "state", "v2", "nests.yaml"), "nests:\n  t: "+indexed+"\n")
	scanClone(t, filepath.Join(f.src, "api"), gl("api"))

	if err := f.scan(f.src, "--yes"); err != nil {
		t.Fatal(err)
	}
	out := f.out.String()
	for _, want := range []string{
		"still missing: web (" + projectFile + "): git clone " + gl("web"),
		"still missing: tools (" + indexed + "): git clone " + gl("tools"),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("out %q lacks %q", out, want)
		}
	}
	for _, key := range []string{"api", "lib", "shared"} {
		if strings.Contains(out, "still missing: "+key+" ") {
			t.Errorf("out %q reports %s, which has a path", out, key)
		}
	}
	if readOrEmpty(t, globalFile) != global || readOrEmpty(t, projectFile) != project {
		t.Error("den scan rewrote a den.yaml")
	}
}

func TestScanSaysWhenRunInsideAClone(t *testing.T) {
	f := newScanFixture(t)
	api := scanClone(t, filepath.Join(f.src, "api"), gl("api"))
	f.cwd = filepath.Join(api, "sub")
	if err := os.MkdirAll(f.cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := f.scan("--yes"); err != nil {
		t.Fatal(err)
	}
	want := f.cwd + " is inside the clone " + api + "; to find its siblings, run `den scan " + f.src + "`"
	if !strings.Contains(f.err.String(), want) {
		t.Errorf("err %q lacks %q", f.err.String(), want)
	}
}

func TestScanSaysWhenRunFromACloneRoot(t *testing.T) {
	f := newScanFixture(t)
	api := scanClone(t, filepath.Join(f.src, "api"), gl("api"))
	scanClone(t, filepath.Join(f.src, "web"), gl("web"))
	f.cwd = api
	if err := f.scan("--yes"); err != nil {
		t.Fatal(err)
	}
	want := api + " is a clone; to find its siblings, run `den scan " + f.src + "`"
	if !strings.Contains(f.err.String(), want) {
		t.Errorf("err %q lacks %q", f.err.String(), want)
	}
	if got := f.table(t).Repos["gitlab.com/dig/api"].Path; got != api {
		t.Errorf("table maps gitlab.com/dig/api to %q, want the scanned clone %q", got, api)
	}
}

func TestScanSkipsTheDenHome(t *testing.T) {
	f := newScanFixture(t)
	root := t.TempDir()
	f.home = filepath.Join(root, "denhome")
	t.Setenv("HOME", filepath.Join(root, "u"))
	scanClone(t, filepath.Join(f.home, "build", "tpl"), gl("tpl"))
	scanClone(t, filepath.Join(root, "u", "Library", "cache"), gl("cache"))
	api := scanClone(t, filepath.Join(root, "dev", "api"), gl("api"))
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}

	for _, r := range []string{root, resolved} {
		if err := f.scan(r, "--yes"); err != nil {
			t.Fatal(err)
		}
		tb := f.table(t)
		if _, ok := tb.Repos["gitlab.com/dig/api"]; !ok {
			t.Fatalf("scan of %s: %+v lacks the ordinary clone %s", r, tb.Repos, api)
		}
		for _, id := range []string{"gitlab.com/dig/tpl", "gitlab.com/dig/cache"} {
			if _, ok := tb.Repos[id]; ok {
				t.Errorf("scan of %s recorded %s, under the den home or ~/Library", r, id)
			}
		}
	}
}

func TestScanRefusesARootInsideTheDenHome(t *testing.T) {
	f := newScanFixture(t)
	scanClone(t, filepath.Join(f.home, "run", "x"), gl("x"))
	resolvedHome, err := filepath.EvalSymlinks(f.home)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{f.home, filepath.Join(f.home, "run"), filepath.Join(resolvedHome, "run")} {
		err := f.scan(r, "--yes")
		if err == nil || !strings.Contains(err.Error(), f.home) ||
			!strings.Contains(err.Error(), "scan the folder that holds your clones") {
			t.Errorf("scan of %s: error %v, want a refusal naming %s and the remedy", r, err, f.home)
		}
	}
	if _, err := os.Stat(repos.TablePath(f.home)); !os.IsNotExist(err) {
		t.Errorf("table written by a refused scan (stat: %v)", err)
	}
}

func TestScanWalksTwentyLevelsDeep(t *testing.T) {
	f := newScanFixture(t)
	dir := f.src
	for i := 1; i <= 19; i++ {
		dir = filepath.Join(dir, "d")
	}
	scanClone(t, filepath.Join(dir, "deep"), gl("deep"))
	scanClone(t, filepath.Join(dir, "d", "deeper"), gl("deeper"))
	if err := f.scan(f.src, "--yes"); err != nil {
		t.Fatal(err)
	}
	tb := f.table(t)
	if _, ok := tb.Repos["gitlab.com/dig/deep"]; !ok {
		t.Errorf("a clone 20 levels down was not found: %+v", tb.Repos)
	}
	if _, ok := tb.Repos["gitlab.com/dig/deeper"]; ok {
		t.Errorf("a clone 21 levels down was found: %+v", tb.Repos)
	}
}

func TestScanListsReplacementsThenDuplicatesThenDeclaredAdditions(t *testing.T) {
	f := newScanFixture(t)
	writeTestFile(t, filepath.Join(f.proj, "den.yaml"), "schema: 1\nrepos:\n  decl: {origin: '"+gl("decl")+"'}\n")
	gone, nothere := filepath.Join(f.src, "gone"), filepath.Join(f.src, "nothere")
	f.seedTable(t, map[string]string{"gitlab.com/dig/rep": gone, "gitlab.com/dig/stale": nothere})
	rep := scanClone(t, filepath.Join(f.src, "rep"), gl("rep"))
	dupa := scanClone(t, filepath.Join(f.src, "dupa"), gl("dup"))
	dupb := scanClone(t, filepath.Join(f.src, "dupb"), gl("dup"))
	decl := scanClone(t, filepath.Join(f.src, "decl"), gl("decl"))
	scanClone(t, filepath.Join(f.src, "o1"), gl("o1"))
	scanClone(t, filepath.Join(f.src, "o2"), gl("o2"))

	if err := f.scan(f.src, "--yes"); err != nil {
		t.Fatal(err)
	}
	out := f.out.String()
	last := -1
	for _, line := range []string{
		"replace gitlab.com/dig/rep: " + gone + " → " + rep,
		"duplicate gitlab.com/dig/dup: using " + dupa + ", not " + dupb,
		"add gitlab.com/dig/decl → " + decl,
		"and 3 other clones",
		"stale gitlab.com/dig/stale: " + nothere + " no longer holds it",
	} {
		i := strings.Index(out, line)
		if i < 0 {
			t.Fatalf("out %q lacks %q", out, line)
		}
		if i < last {
			t.Errorf("%q is out of order in\n%s", line, out)
		}
		last = i
	}
	if got := f.table(t).Repos["gitlab.com/dig/stale"].Path; got != nothere {
		t.Errorf("a write dropped the stale entry: it maps to %q, want %q kept", got, nothere)
	}
	for _, undeclared := range []string{"gitlab.com/dig/o1", "gitlab.com/dig/o2", "repos:"} {
		if strings.Contains(out, undeclared) {
			t.Errorf("out names %q, which belongs in the count:\n%s", undeclared, out)
		}
	}
}

func TestScanWarnsOfADuplicateNamingEveryPathWithoutAsking(t *testing.T) {
	f := newScanFixture(t)
	a := scanClone(t, filepath.Join(f.src, "a"), gl("dup"))
	b := scanClone(t, filepath.Join(f.src, "b"), gl("dup"))
	c := scanClone(t, filepath.Join(f.src, "c"), gl("dup"))
	if err := f.scan(f.src, "--yes"); err != nil {
		t.Fatal(err)
	}
	f.tty = true
	f.prompt = &prompt.Fake{}
	before := readOrEmpty(t, repos.TablePath(f.home))
	if err := f.scan(f.src); err != nil {
		t.Fatalf("a rescan with duplicates only: %v", err)
	}
	want := "duplicate gitlab.com/dig/dup: using " + a + ", not " + b + ", " + c
	if !strings.Contains(f.out.String(), want) {
		t.Errorf("out %q lacks %q", f.out.String(), want)
	}
	if len(f.prompt.Confirms) != 0 || readOrEmpty(t, repos.TablePath(f.home)) != before {
		t.Errorf("a duplicate asked %v or rewrote the table", f.prompt.Confirms)
	}
}
