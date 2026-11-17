package nest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/PillowPillow/den/v2/internal/config"
	"github.com/PillowPillow/den/v2/internal/repos"
)

// clone writes dir/.git/config by hand, one remote per url.
func clone(t *testing.T, dir string, urls ...string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("[core]\n\tbare = false\n")
	for i, u := range urls {
		fmt.Fprintf(&b, "[remote \"r%d\"]\n\turl = %s\n", i, u)
	}
	write(t, dir, ".git/config", b.String())
	return dir
}

// pair writes the global den.yaml under home and a project den.yaml under proj, then loads both.
func pair(t *testing.T, home, globalYAML, proj, projectYAML string) (global, project *config.Loaded) {
	t.Helper()
	write(t, home, "den.yaml", "schema: 1\n"+globalYAML)
	global, err := config.LoadGlobal(home)
	if err != nil {
		t.Fatal(err)
	}
	return global, mustLoad(t, write(t, proj, "den.yaml", "schema: 1\n"+projectYAML), false)
}

func writeTable(t *testing.T, home string, entries map[string]string) {
	t.Helper()
	tb := repos.Table{Repos: map[string]repos.Entry{}}
	for id, path := range entries {
		tb.Repos[id] = repos.Entry{Path: path}
	}
	if err := repos.WriteTable(home, tb); err != nil {
		t.Fatal(err)
	}
}

func nestOf(refs string) string {
	return "nests:\n  x:\n    template: claude-code-docker\n    repos: " + refs + "\n"
}

func TestKeyResolvesThroughTheTable(t *testing.T) {
	home, proj := t.TempDir(), t.TempDir()
	g, p := pair(t, home, "", proj, "repos:\n  api: {origin: git@gitlab.com:dig/api.git}\n"+nestOf("[{key: api}]"))
	writeTable(t, home, map[string]string{"gitlab.com/dig/api": "/c/api"})
	r, err := Resolve(home, g, p, "x")
	if err != nil {
		t.Fatal(err)
	}
	want := []Repo{{Name: "api", Path: "/c/api", Key: "api", Origin: "git@gitlab.com:dig/api.git", DeclaredIn: p.Path}}
	if !reflect.DeepEqual(r.Repos, want) {
		t.Fatalf("repos %+v, want %+v", r.Repos, want)
	}
}

func TestGlobalPathWinsOverTheTable(t *testing.T) {
	home, proj := t.TempDir(), t.TempDir()
	dir := clone(t, filepath.Join(t.TempDir(), "api"), "https://gitlab.com/dig/api.git")
	g, p := pair(t, home, "repos:\n  api: "+dir+"\n", proj, "repos:\n  api: {origin: git@gitlab.com:dig/api.git}\n"+nestOf("[{key: api}]"))
	writeTable(t, home, map[string]string{"gitlab.com/dig/api": "/c/elsewhere"})
	r, err := Resolve(home, g, p, "x")
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Repos[0]; got.Path != dir || got.DeclaredIn != g.Path || got.Name != "api" {
		t.Fatalf("repo %+v, want the global path %s declared in %s", got, dir, g.Path)
	}
}

func TestGlobalPathMustHoldTheProjectOrigin(t *testing.T) {
	home, proj := t.TempDir(), t.TempDir()
	dir := clone(t, filepath.Join(t.TempDir(), "other"), "git@gitlab.com:me/other.git")
	projectYAML := "repos:\n  api: {origin: git@gitlab.com:dig/api.git}\n" + nestOf("[{key: api}]")
	g, p := pair(t, home, "repos:\n  api: "+dir+"\n", proj, projectYAML)
	_, err := Resolve(home, g, p, "x")
	want := fmt.Sprintf("repo key %q: %s gives origin %s, but %s (set in %s) has no remote with that identity — "+
		"fix one of the two, or add origin: to the key in %s", "api", p.Path, "git@gitlab.com:dig/api.git", dir, g.Path, g.Path)
	if err == nil || err.Error() != want {
		t.Fatalf("err %v\nwant %s", err, want)
	}

	clone(t, dir, "git@gitlab.com:me/other.git", "https://gitlab.com/dig/api.git")
	r, err := Resolve(home, g, p, "x")
	if err != nil {
		t.Fatal(err)
	}
	if r.Repos[0].Path != dir {
		t.Fatalf("repo %+v, want %s", r.Repos[0], dir)
	}
}

func TestGlobalPathWithOriginIsTrusted(t *testing.T) {
	home, proj := t.TempDir(), t.TempDir()
	dir := clone(t, filepath.Join(t.TempDir(), "alias"), "git@gitlab-perso:dig/api.git")
	g, p := pair(t, home, "repos:\n  api: {path: "+dir+", origin: git@gitlab.com:dig/api.git}\n",
		proj, "repos:\n  api: {origin: git@gitlab.com:dig/api.git}\n"+nestOf("[{key: api}]"))
	r, err := Resolve(home, g, p, "x")
	if err != nil {
		t.Fatal(err)
	}
	if r.Repos[0].Path != dir || r.Repos[0].Name != "api" {
		t.Fatalf("repo %+v, want api at %s", r.Repos[0], dir)
	}
}

func TestScalarGlobalKeyResolvesAsToday(t *testing.T) {
	home, proj := t.TempDir(), t.TempDir()
	globalYAML := "repos:\n" +
		"  js.box: /Users/me/Development/Digitaleo/js.box\n" +
		"  baseo.messengeo.net: /Users/me/Development/Digitaleo/baseo.messengeo.net\n" +
		"  den: /Users/me/Development/Pillow/den\n" +
		"  scratch: rel/scratch\n"
	g, p := pair(t, home, globalYAML, proj, nestOf("[{key: js.box}, {key: baseo.messengeo.net, optional: true}, {key: scratch}, {path: ./local}]"))
	r, err := Resolve(home, g, p, "x")
	if err != nil {
		t.Fatal(err)
	}
	want := []Repo{
		{Name: "js.box", Path: "/Users/me/Development/Digitaleo/js.box", Key: "js.box", DeclaredIn: g.Path},
		{Name: "baseo.messengeo.net", Path: "/Users/me/Development/Digitaleo/baseo.messengeo.net", Optional: true, Key: "baseo.messengeo.net", DeclaredIn: g.Path},
		{Name: "scratch", Path: filepath.Join(home, "rel", "scratch"), Key: "scratch", DeclaredIn: g.Path},
		{Name: "local", Path: filepath.Join(proj, "local")},
	}
	if !reflect.DeepEqual(r.Repos, want) {
		t.Fatalf("repos\n%+v\nwant\n%+v", r.Repos, want)
	}
	if len(r.Warnings) != 0 {
		t.Errorf("warnings %v", r.Warnings)
	}
}

func TestGlobalNestKeyResolvesThroughTheGlobalPath(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(t.TempDir(), "api")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := write(t, home, "den.yaml", "schema: 1\nrepos:\n  api: {path: "+dir+", origin: git@gitlab.com:dig/api.git}\n  tool: /c/tool\n"+nestOf("[{key: api}, {key: tool}]"))
	g := mustLoad(t, p, true)
	r, err := Resolve(home, g, g, "x")
	if err != nil {
		t.Fatal(err)
	}
	want := []Repo{
		{Name: "api", Path: dir, Key: "api", Origin: "git@gitlab.com:dig/api.git", DeclaredIn: g.Path},
		{Name: "tool", Path: "/c/tool", Key: "tool", DeclaredIn: g.Path},
	}
	if !reflect.DeepEqual(r.Repos, want) {
		t.Fatalf("repos %+v, want %+v", r.Repos, want)
	}
}

func TestConflictingOriginsRefuse(t *testing.T) {
	home, proj := t.TempDir(), t.TempDir()
	g, p := pair(t, home, "repos:\n  api: {path: /c/api, origin: git@gitlab.com:me/api.git}\n",
		proj, "repos:\n  api: {origin: git@gitlab.com:dig/api.git}\n"+nestOf("[{key: api}]"))
	_, err := Resolve(home, g, p, "x")
	want := fmt.Sprintf("repo key %q: %s says %s, %s says %s — one key names one repo; rename one",
		"api", p.Path, "git@gitlab.com:dig/api.git", g.Path, "git@gitlab.com:me/api.git")
	if err == nil || err.Error() != want {
		t.Fatalf("err %v\nwant %s", err, want)
	}
}

func TestConflictingPathsRefuse(t *testing.T) {
	home, proj := t.TempDir(), t.TempDir()
	g, p := pair(t, home, "repos:\n  api: /c/api\n", proj, "repos:\n  api: {path: ./api}\n"+nestOf("[{key: api}]"))
	_, err := Resolve(home, g, p, "x")
	want := fmt.Sprintf("repo key %q: %s says %s, %s says %s — one key names one repo; rename one",
		"api", p.Path, filepath.Join(proj, "api"), g.Path, "/c/api")
	if err == nil || err.Error() != want {
		t.Fatalf("err %v\nwant %s", err, want)
	}
}

func TestSameOriginBothSidesIsNoConflict(t *testing.T) {
	home, proj := t.TempDir(), t.TempDir()
	g, p := pair(t, home, "repos:\n  api: {path: /c/api, origin: https://gitlab.com/dig/api.git}\n",
		proj, "repos:\n  api: {origin: git@gitlab.com:dig/api.git}\n"+nestOf("[{key: api}]"))
	r, err := Resolve(home, g, p, "x")
	if err != nil {
		t.Fatal(err)
	}
	if r.Repos[0].Path != "/c/api" || r.Repos[0].DeclaredIn != g.Path {
		t.Fatalf("repo %+v, want /c/api declared in %s", r.Repos[0], g.Path)
	}
}

func TestMissingKeysAreListedTogether(t *testing.T) {
	home, proj := t.TempDir(), t.TempDir()
	g, p := pair(t, home, "", proj, "repos:\n"+
		"  front-api: {origin: git@gitlab.com:dig/front/api.git}\n"+
		"  back-api: {origin: git@gitlab.com:dig/back/api.git}\n"+
		"nests:\n  oauth:\n    template: claude-code-docker\n    repos: [{key: front-api}, {key: back-api}]\n")
	_, err := Resolve(home, g, p, "oauth")
	var missing *MissingReposError
	if !errors.As(err, &missing) {
		t.Fatalf("err %v, want a *MissingReposError", err)
	}
	wantMissing := []MissingRepo{
		{Key: "front-api", Origin: "git@gitlab.com:dig/front/api.git", DeclaredIn: p.Path},
		{Key: "back-api", Origin: "git@gitlab.com:dig/back/api.git", DeclaredIn: p.Path},
	}
	if missing.Nest != "oauth" || !reflect.DeepEqual(missing.Missing, wantMissing) {
		t.Fatalf("missing %+v", missing)
	}
	want := "nest \"oauth\": 2 repos have no folder on this machine:\n" +
		"  - front-api: git clone git@gitlab.com:dig/front/api.git   (declared in " + p.Path + ")\n" +
		"  - back-api: git clone git@gitlab.com:dig/back/api.git   (declared in " + p.Path + ")\n" +
		"already cloned? run `den scan <folder that holds your clones>`"
	if err.Error() != want {
		t.Fatalf("message\n%s\nwant\n%s", err, want)
	}
}

func TestOneMissingKeyIsWordedInTheSingular(t *testing.T) {
	err := &MissingReposError{Nest: "x", Missing: []MissingRepo{{Key: "api", Origin: "git@gitlab.com:dig/api.git", DeclaredIn: "/p/den.yaml"}}}
	want := "nest \"x\": 1 repo has no folder on this machine:\n" +
		"  - api: git clone git@gitlab.com:dig/api.git   (declared in /p/den.yaml)\n" +
		"already cloned? run `den scan <folder that holds your clones>`"
	if err.Error() != want {
		t.Fatalf("message\n%s\nwant\n%s", err, want)
	}
}

func TestMissingOptionalKeyWarnsAndIsDropped(t *testing.T) {
	home, proj := t.TempDir(), t.TempDir()
	g, p := pair(t, home, "repos:\n  api: /c/api\n", proj, "repos:\n  extra: {origin: git@gitlab.com:dig/extra.git}\n"+
		nestOf("[{key: api}, {key: extra, optional: true}]"))
	r, err := Resolve(home, g, p, "x")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Repos) != 1 || r.Repos[0].Key != "api" {
		t.Fatalf("repos %+v, want api only", r.Repos)
	}
	if len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], `optional repo key "extra"`) ||
		!strings.Contains(r.Warnings[0], "git clone git@gitlab.com:dig/extra.git") || !strings.Contains(r.Warnings[0], p.Path) {
		t.Fatalf("warnings %v", r.Warnings)
	}
}

func TestUndeclaredKeyNamesBothFiles(t *testing.T) {
	home, proj := t.TempDir(), t.TempDir()
	g, p := pair(t, home, "repos:\n  api: /c/api\n", proj, nestOf("[{key: api}, {key: nope}]"))
	_, err := Resolve(home, g, p, "x")
	want := fmt.Sprintf("nest %q: repo key %q is not in repos: of %s or of %s; add it to one of them", "x", "nope", p.Path, g.Path)
	if err == nil || err.Error() != want {
		t.Fatalf("err %v\nwant %s", err, want)
	}

	g, p = pair(t, home, "repos:\n  api: /c/api\n", proj, nestOf("[{key: api}, {key: nope, optional: true}]"))
	r, err := Resolve(home, g, p, "x")
	if err != nil {
		t.Fatal(err)
	}
	wantWarn := fmt.Sprintf("optional repo key %q is not in repos: of %s or of %s; dropped", "nope", p.Path, g.Path)
	if !reflect.DeepEqual(r.Warnings, []string{wantWarn}) {
		t.Fatalf("warnings %v, want %s", r.Warnings, wantWarn)
	}
}

func TestKeyNamesTheRepo(t *testing.T) {
	home, proj := t.TempDir(), t.TempDir()
	g, p := pair(t, home, "repos:\n  go-dgdev: /c/go.dgdev\n", proj, nestOf("[{key: go-dgdev}]"))
	r, err := Resolve(home, g, p, "x")
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Repos[0]; got.Name != "go-dgdev" || got.Path != "/c/go.dgdev" {
		t.Fatalf("repo %+v, want go-dgdev at /c/go.dgdev", got)
	}
}

func TestTwoKeysEndingInApiCoexist(t *testing.T) {
	home, proj := t.TempDir(), t.TempDir()
	g, p := pair(t, home, "repos:\n  front-api: /a/api\n  back-api: /b/api\n", proj, nestOf("[{key: front-api}, {key: back-api}]"))
	r, err := Resolve(home, g, p, "x")
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{r.Repos[0].Name, r.Repos[1].Name}; !reflect.DeepEqual(got, []string{"front-api", "back-api"}) {
		t.Fatalf("names %v", got)
	}
}

func TestTwoKeysOnOneFolderRefuse(t *testing.T) {
	home, proj := t.TempDir(), t.TempDir()
	g, p := pair(t, home, "repos:\n  a: /c/x\n  b: /c/x/\n", proj, nestOf("[{key: a}, {key: b}]"))
	_, err := Resolve(home, g, p, "x")
	want := fmt.Sprintf("nest %q in %s: repos key %q and key %q are both the folder %s; a folder is mounted once — drop one of the two",
		"x", p.Path, "a", "b", "/c/x")
	if err == nil || err.Error() != want {
		t.Fatalf("err %v\nwant %s", err, want)
	}

	g, p = pair(t, home, "repos:\n  a: "+filepath.Join(proj, "x")+"\n", proj, nestOf("[{path: ./x}, {key: a}]"))
	_, err = Resolve(home, g, p, "x")
	want = fmt.Sprintf("nest %q in %s: repos path: %s and key %q are both the folder %s; a folder is mounted once — drop one of the two",
		"x", p.Path, filepath.Join(proj, "x"), "a", filepath.Join(proj, "x"))
	if err == nil || err.Error() != want {
		t.Fatalf("err %v\nwant %s", err, want)
	}
}

func TestMachineReposAddTableEntriesOnce(t *testing.T) {
	home, proj, clones := t.TempDir(), t.TempDir(), t.TempDir()
	mk := func(name string) string {
		d := filepath.Join(clones, name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		return d
	}
	shared, zeta, alpha := mk("shared"), mk("zeta"), mk("alpha")
	g, p := pair(t, home, "repos:\n  mine: "+shared+"\n  gone: "+filepath.Join(clones, "gone")+"\n",
		proj, nestOf("[{key: mine}]"))
	writeTable(t, home, map[string]string{
		"gitlab.com/dig/shared":      shared,
		"github.com/me/shared-fork":  shared,
		"gitlab.com/dig/zeta":        zeta,
		"gitlab.com/dig/alpha":       alpha,
		"gitlab.com/dig/moved-clone": filepath.Join(clones, "moved"),
	})
	r, err := Resolve(home, g, p, "x")
	if err != nil {
		t.Fatal(err)
	}
	want := []MachineRepo{{Name: "mine", Path: shared}, {Name: "alpha", Path: alpha}, {Name: "zeta", Path: zeta}}
	if !reflect.DeepEqual(r.MachineRepos, want) {
		t.Fatalf("machine repos\n%+v\nwant\n%+v", r.MachineRepos, want)
	}
}
