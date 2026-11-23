package nest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/PillowPillow/den/v2/internal/config"
	"github.com/PillowPillow/den/v2/internal/prompt"
	"github.com/PillowPillow/den/v2/internal/template"
)

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func nestFile(name string) string {
	return "schema: 1\nnests:\n  " + name + ":\n    template: claude-code-docker\n    profile: p\n    repos: [{path: ./r}]\n"
}

func mustLoad(t *testing.T, path string, global bool) *config.Loaded {
	t.Helper()
	l, err := config.Load(path, global)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestFindPrefersTheProjectFile(t *testing.T) {
	home, proj := t.TempDir(), t.TempDir()
	write(t, home, "den.yaml", nestFile("other"))
	p := write(t, proj, "den.yaml", nestFile("api"))
	f, g, err := Find(home, proj, "api")
	if err != nil {
		t.Fatal(err)
	}
	if f.Path != p || g == nil || !g.Global {
		t.Fatalf("file %v global %v", f.Path, g)
	}
}

func TestFindFallsBackToTheIndexThenGlobal(t *testing.T) {
	home, proj, elsewhere := t.TempDir(), t.TempDir(), t.TempDir()
	write(t, home, "den.yaml", nestFile("glob"))
	pp := write(t, proj, "den.yaml", nestFile("api"))
	if _, _, err := Find(home, proj, "api"); err != nil {
		t.Fatal(err)
	}
	f, _, err := Find(home, elsewhere, "api")
	if err != nil || f.Path != pp {
		t.Fatalf("index: %v %v", f, err)
	}
	f, _, err = Find(home, elsewhere, "glob")
	if err != nil || !f.Global {
		t.Fatalf("global: %v %v", f, err)
	}
}

func TestFindListsKnownNestsWhenUnknown(t *testing.T) {
	home, proj := t.TempDir(), t.TempDir()
	g := write(t, home, "den.yaml", nestFile("glob"))
	p := write(t, proj, "den.yaml", nestFile("api"))
	_, _, err := Find(home, proj, "nope")
	if err == nil {
		t.Fatal("want error")
	}
	for _, want := range []string{`unknown nest "nope"`, "api", "glob", g, p} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

func TestClaimRefusesASecondDeclaringFile(t *testing.T) {
	home, a, b := t.TempDir(), t.TempDir(), t.TempDir()
	pa := write(t, a, "den.yaml", nestFile("api"))
	pb := write(t, b, "den.yaml", nestFile("api"))
	if err := Claim(home, mustLoad(t, pa, false)); err != nil {
		t.Fatal(err)
	}
	err := Claim(home, mustLoad(t, pb, false))
	if err == nil || !strings.Contains(err.Error(), pa) || !strings.Contains(err.Error(), pb) {
		t.Fatalf("got %v", err)
	}
	if err := Claim(home, mustLoad(t, pa, false)); err != nil {
		t.Fatalf("re-claim by the owner: %v", err)
	}
}

func TestClaimReplacesAStaleEntry(t *testing.T) {
	home, a, b := t.TempDir(), t.TempDir(), t.TempDir()
	pa := write(t, a, "den.yaml", nestFile("api"))
	pb := write(t, b, "den.yaml", nestFile("api"))
	if err := Claim(home, mustLoad(t, pa, false)); err != nil {
		t.Fatal(err)
	}
	write(t, a, "den.yaml", nestFile("renamed"))
	if err := Claim(home, mustLoad(t, pb, false)); err != nil {
		t.Fatalf("nest no longer declared: %v", err)
	}
	if err := os.Remove(pb); err != nil {
		t.Fatal(err)
	}
	pc := write(t, t.TempDir(), "den.yaml", nestFile("api"))
	if err := Claim(home, mustLoad(t, pc, false)); err != nil {
		t.Fatalf("file gone: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(home, "state", "v2", "nests.yaml"))
	if err != nil || !strings.Contains(string(raw), pc) {
		t.Fatalf("index %q %v", raw, err)
	}
}

const richGlobal = `schema: 1
repos:
  lib: ~/code/lib
env: {A: g, B: g}
network: {allow: [x.com, y.com]}
worktrees: {layout: per-repo, root: ./trees}
nests:
  api:
    template: claude-code-docker
    kits: [k1, k2, k1]
    env: {B: n, C: n}
    network: {allow: [y.com, z.com, w.com, z.com]}
    repos: [{path: ./app}, {key: lib, optional: true}]
`

func resolveRich(t *testing.T, body string) (*Resolved, error) {
	t.Helper()
	home := t.TempDir()
	p := write(t, home, "den.yaml", body)
	g := mustLoad(t, p, true)
	return Resolve(home, g, g, "api")
}

func TestResolveMergesAllowInOrderWithoutDuplicates(t *testing.T) {
	r, err := resolveRich(t, richGlobal)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"x.com", "y.com", "z.com", "w.com"}; !reflect.DeepEqual(r.Allow, want) {
		t.Fatalf("allow %v", r.Allow)
	}
	if want := []string{"k1", "k2"}; !reflect.DeepEqual(r.Kits, want) {
		t.Fatalf("kits %v", r.Kits)
	}
}

func TestResolveNestEnvWinsOverGlobal(t *testing.T) {
	r, err := resolveRich(t, richGlobal)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"A": "g", "B": "n", "C": "n"}; !reflect.DeepEqual(r.Env, want) {
		t.Fatalf("env %v", r.Env)
	}
}

func TestResolveRefusesAnUnmappedRequiredKey(t *testing.T) {
	body := strings.Replace(strings.Replace(richGlobal, "{key: lib, optional: true}", "{key: ghost}", 1), "repos:\n  lib: ~/code/lib\n", "", 1)
	_, err := resolveRich(t, body)
	if err == nil || !strings.Contains(err.Error(), "ghost") || !strings.Contains(err.Error(), "den.yaml") {
		t.Fatalf("got %v", err)
	}
}

func TestResolveDropsAnUnmappedOptionalKeyWithAWarning(t *testing.T) {
	body := strings.Replace(richGlobal, "{key: lib, optional: true}", "{key: ghost, optional: true}", 1)
	r, err := resolveRich(t, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Repos) != 1 || r.Repos[0].Name != "app" {
		t.Fatalf("repos %v", r.Repos)
	}
	if len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "ghost") {
		t.Fatalf("warnings %v", r.Warnings)
	}
}

func TestResolveRefusesTwoReposWithOneName(t *testing.T) {
	body := strings.Replace(richGlobal, "{path: ./app}", "{path: ./a/lib}", 1)
	_, err := resolveRich(t, body)
	if err == nil || !strings.Contains(err.Error(), `"lib"`) {
		t.Fatalf("got %v", err)
	}
}

func TestResolveNormalizesTheWorktreeRoot(t *testing.T) {
	home := t.TempDir()
	g := mustLoad(t, write(t, home, "den.yaml", richGlobal), true)
	r, err := Resolve(home, g, g, "api")
	if err != nil {
		t.Fatal(err)
	}
	if r.Layout != "per-repo" || r.Root != filepath.Join(home, "trees") {
		t.Fatalf("layout %q root %q", r.Layout, r.Root)
	}

	proj := t.TempDir()
	pbody := strings.Replace(nestFile("api"), "    repos:", "    worktrees: {root: ~/wt-nest}\n    repos:", 1)
	pf := mustLoad(t, write(t, proj, "den.yaml", pbody), false)
	t.Setenv("HOME", "/home/x")
	r, err = Resolve(home, g, pf, "api")
	if err != nil {
		t.Fatal(err)
	}
	if r.Layout != "per-repo" || r.Root != "/home/x/wt-nest" {
		t.Fatalf("nest override: layout %q root %q", r.Layout, r.Root)
	}

	pbody = strings.Replace(nestFile("api"), "    repos:", "    worktrees: {layout: central, root: ./w}\n    repos:", 1)
	pf = mustLoad(t, write(t, proj, "den.yaml", pbody), false)
	r, _ = Resolve(home, g, pf, "api")
	if r.Root != filepath.Join(proj, "w") {
		t.Fatalf("relative to the nest's file: %q", r.Root)
	}

	empty := mustLoad(t, write(t, t.TempDir(), "den.yaml", nestFile("api")), true)
	r, err = Resolve(home, empty, empty, "api")
	if err != nil || r.Layout != "central" || r.Root != filepath.Join(home, "wt") {
		t.Fatalf("default: %+v %v", r, err)
	}
}

func TestResolveNamesBothFilesWithoutATemplate(t *testing.T) {
	home := t.TempDir()
	g := mustLoad(t, write(t, home, "den.yaml", "schema: 1\n"), true)
	pf := mustLoad(t, write(t, t.TempDir(), "den.yaml", nestFile("api")), false)
	pf.File.Nests["api"] = func() config.Nest { n := pf.File.Nests["api"]; n.Template = ""; return n }()
	_, err := Resolve(home, g, pf, "api")
	var nt *template.NoTemplateError
	if !errors.As(err, &nt) || !strings.Contains(err.Error(), pf.Path) || !strings.Contains(err.Error(), g.Path) {
		t.Fatalf("got %v", err)
	}
}

func resolved() *Resolved {
	return &Resolved{Repos: []Repo{
		{Name: "app", Path: "/a/app"},
		{Name: "lib", Path: "/a/lib", Optional: true},
		{Name: "doc", Path: "/a/doc", Optional: true},
	}}
}

func names(rs []Repo) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Name)
	}
	return out
}

func TestSelectWithoutKeepsTheRest(t *testing.T) {
	got, err := Select(context.Background(), resolved(), Selection{Without: []string{"lib"}}, &prompt.Fake{})
	if err != nil || !reflect.DeepEqual(names(got), []string{"app", "doc"}) {
		t.Fatalf("%v %v", names(got), err)
	}
	got, err = Select(context.Background(), resolved(), Selection{Only: []string{"doc"}}, &prompt.Fake{})
	if err != nil || !reflect.DeepEqual(names(got), []string{"app", "doc"}) {
		t.Fatalf("only: %v %v", names(got), err)
	}
	got, _ = Select(context.Background(), resolved(), Selection{}, &prompt.Fake{})
	if len(got) != 3 {
		t.Fatalf("no flag: %v", names(got))
	}
}

func TestSelectRefusesARequiredRepo(t *testing.T) {
	for _, s := range []Selection{
		{Without: []string{"app"}},
		{Only: []string{"app"}},
		{Only: []string{"nope"}},
		{Only: []string{"lib"}, Without: []string{"doc"}},
	} {
		_, err := Select(context.Background(), resolved(), s, &prompt.Fake{})
		if err == nil {
			t.Fatalf("%+v: want error", s)
		}
	}
	_, err := Select(context.Background(), resolved(), Selection{Only: []string{"app"}}, &prompt.Fake{})
	if !strings.Contains(err.Error(), "lib") || !strings.Contains(err.Error(), "doc") {
		t.Fatalf("error should list the optional repos: %v", err)
	}
}

func TestSelectInteractiveAsksForOptionalOnly(t *testing.T) {
	p := &prompt.Fake{MultiSelectAnswers: [][]string{{"doc"}}}
	got, err := Select(context.Background(), resolved(), Selection{Interactive: true}, p)
	if err != nil || !reflect.DeepEqual(names(got), []string{"app", "doc"}) {
		t.Fatalf("%v %v", names(got), err)
	}
	if len(p.MultiSelects) != 1 {
		t.Fatalf("calls %d", len(p.MultiSelects))
	}
	req := p.MultiSelects[0]
	var vals []string
	for _, o := range req.Options {
		vals = append(vals, o.Value)
	}
	if !reflect.DeepEqual(vals, []string{"lib", "doc"}) || !req.Preselected {
		t.Fatalf("options %v preselected %v", vals, req.Preselected)
	}
}

func indexWithCorruptFile(t *testing.T) (home, elsewhere, good, bad string) {
	t.Helper()
	home, elsewhere = t.TempDir(), t.TempDir()
	write(t, home, "den.yaml", nestFile("glob"))
	a, b := t.TempDir(), t.TempDir()
	good = write(t, a, "den.yaml", nestFile("api"))
	bad = write(t, b, "den.yaml", nestFile("zzz"))
	for _, d := range []string{a, b} {
		if _, _, err := Find(home, d, map[string]string{a: "api", b: "zzz"}[d]); err != nil {
			t.Fatal(err)
		}
	}
	write(t, b, "den.yaml", nestFile("zzz")+"future_key: 1\n")
	return home, elsewhere, good, bad
}

func TestFindIgnoresAnUnrelatedCorruptIndexedFile(t *testing.T) {
	home, elsewhere, good, _ := indexWithCorruptFile(t)
	f, _, err := Find(home, elsewhere, "api")
	if err != nil || f.Path != good {
		t.Fatalf("%v %v", f, err)
	}
}

func TestFindNamesACorruptIndexedFileWhenUnknown(t *testing.T) {
	home, elsewhere, good, bad := indexWithCorruptFile(t)
	_, _, err := Find(home, elsewhere, "nope")
	if err == nil {
		t.Fatal("want error")
	}
	for _, want := range []string{`unknown nest "nope"`, "api", "glob", good, "unreadable: " + bad} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

const profileGlobal = `schema: 1
profile: filep
profiles:
  filep: [./kits/f]
  nestp: [./kits/n, k1]
nests:
  api:
    template: claude-code-docker
    kits: [k1, k2]
    repos: [{path: ./app}]
`

func TestResolveUsesTheFileProfileByDefault(t *testing.T) {
	r, err := resolveRich(t, profileGlobal)
	if err != nil {
		t.Fatal(err)
	}
	if r.Profile != "filep" || len(r.ProfileKits) != 1 || !strings.HasSuffix(r.ProfileKits[0], "kits/f") {
		t.Fatalf("profile %q kits %v", r.Profile, r.ProfileKits)
	}
}

func TestResolveNestProfileWinsOverTheFileProfile(t *testing.T) {
	r, err := resolveRich(t, strings.Replace(profileGlobal, "    template: claude-code-docker\n", "    template: claude-code-docker\n    profile: nestp\n", 1))
	if err != nil {
		t.Fatal(err)
	}
	if r.Profile != "nestp" || len(r.ProfileKits) != 2 {
		t.Fatalf("profile %q kits %v", r.Profile, r.ProfileKits)
	}
}

func TestResolveAppendsProfileKitsAfterNestKits(t *testing.T) {
	r, err := resolveRich(t, strings.Replace(profileGlobal, "    template: claude-code-docker\n", "    template: claude-code-docker\n    profile: nestp\n", 1))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(r.GlobalFile)
	if want := []string{"k1", "k2", filepath.Join(dir, "kits", "n")}; !reflect.DeepEqual(r.Kits, want) {
		t.Fatalf("kits %v, want %v", r.Kits, want)
	}
}

func TestResolveWarnsOnAnUndefinedProfile(t *testing.T) {
	r, err := resolveRich(t, strings.Replace(profileGlobal, "    template: claude-code-docker\n", "    template: claude-code-docker\n    profile: ghost\n", 1))
	if err != nil {
		t.Fatal(err)
	}
	want := `profile "ghost" is not defined in profiles: of ` + r.GlobalFile + "; no profile kits"
	if len(r.Warnings) != 1 || r.Warnings[0] != want || len(r.ProfileKits) != 0 || !reflect.DeepEqual(r.Kits, []string{"k1", "k2"}) {
		t.Fatalf("warnings %v kits %v", r.Warnings, r.Kits)
	}
}

func TestAddReposAppendsInTypedOrderAndResolvesFromCwd(t *testing.T) {
	picked := []Repo{{Name: "app", Path: "/a/app"}}
	got, err := AddRepos(resolved(), picked, []string{"/b/zed", "../c/box"}, "/w/here")
	if err != nil {
		t.Fatal(err)
	}
	want := []Repo{{Name: "app", Path: "/a/app"}, {Name: "zed", Path: "/b/zed", AdHoc: true}, {Name: "box", Path: "/w/c/box", AdHoc: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestAddReposSkipsAMountedPathAndRestoresADroppedOptional(t *testing.T) {
	picked := []Repo{{Name: "app", Path: "/a/app"}}
	got, err := AddRepos(resolved(), picked, []string{"/a/app/", "/a/doc"}, "/w")
	if err != nil {
		t.Fatal(err)
	}
	want := []Repo{{Name: "app", Path: "/a/app"}, {Name: "doc", Path: "/a/doc", Optional: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestAddReposRefusesAFolderNameAlreadyMounted(t *testing.T) {
	picked := []Repo{{Name: "app", Path: "/a/app"}}
	for _, raw := range [][]string{{"/other/app"}, {"/x/zed", "/y/zed"}} {
		_, err := AddRepos(resolved(), picked, raw, "/w")
		if err == nil || !strings.Contains(err.Error(), "--repo") || !strings.Contains(err.Error(), "unique") {
			t.Fatalf("%v: error %v, want the name clash refusal", raw, err)
		}
	}
}

func TestResolveTakesKitsAndAllowFromTheDefinition(t *testing.T) {
	home := t.TempDir()
	write(t, home, "tpl/den-template.yaml", "schema: 1\ntemplates:\n  legacy:\n    from: claude-code-docker\n"+
		"    kits: [docker.io/acme/k1:1, ./kits/k3]\n    network: {allow: [y.com, z.com]}\n    build: {network: {allow: [build.only]}}\n")
	g := mustLoad(t, write(t, home, "den.yaml", `schema: 1
network: {allow: [x.com, y.com]}
profile: p
profiles:
  p: [docker.io/acme/k1:1, docker.io/acme/kp:1]
templates:
  legacy: {dir: ./tpl}
nests:
  api:
    template: legacy
    kits: [k2]
    network: {allow: [z.com, w.com]}
    repos: [{path: ./app}]
`), true)
	c, err := template.Choose(g, g, "api")
	if err != nil {
		t.Fatal(err)
	}
	if err := template.WriteRecord(home, template.Record{Template: "legacy", Tag: "legacy:v2", Source: template.SourceOf(c),
		DefinitionHash: "h", From: template.From{Ref: "docker.io/docker/sandbox-templates:claude-code-docker", Digest: "sha256:" + strings.Repeat("4a", 32)}}); err != nil {
		t.Fatal(err)
	}
	r, err := Resolve(home, g, g, "api")
	if err != nil {
		t.Fatal(err)
	}
	if r.Template == nil || r.Template.Choice.Name() != "legacy" || r.Template.Record == nil {
		t.Fatalf("template %+v", r.Template)
	}
	k3 := filepath.Join(r.Template.Defs.Root, "kits", "k3")
	if want := []string{"docker.io/acme/k1:1", k3, "k2", "docker.io/acme/kp:1"}; !reflect.DeepEqual(r.Kits, want) {
		t.Errorf("kits %v, want %v", r.Kits, want)
	}
	if want := []string{"x.com", "y.com", "z.com", "w.com"}; !reflect.DeepEqual(r.Allow, want) {
		t.Errorf("allow %v, want %v", r.Allow, want)
	}
}

func TestResolveExposesEveryMachineRepo(t *testing.T) {
	home := t.TempDir()
	abs := filepath.Join(t.TempDir(), "a")
	for _, d := range []string{abs, filepath.Join(home, "rel", "b")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p := write(t, home, "den.yaml", "schema: 1\nrepos:\n  b: rel/b\n  a: "+abs+"\nnests:\n  api:\n    template: claude-code-docker\n    repos: [{key: a}]\n")
	g := mustLoad(t, p, true)
	r, err := Resolve(home, g, g, "api")
	if err != nil {
		t.Fatal(err)
	}
	want := []MachineRepo{{Name: "a", Path: abs}, {Name: "b", Path: filepath.Join(home, "rel", "b")}}
	if !reflect.DeepEqual(r.MachineRepos, want) {
		t.Fatalf("machine repos %v, want %v", r.MachineRepos, want)
	}
}

func TestResolveAutoTrust(t *testing.T) {
	const nestLine = "    template: claude-code-docker\n"
	for _, c := range []struct {
		name, global, nest string
		want               bool
	}{
		{"default", "", "", true},
		{"global false", "auto_trust: false\n", "", false},
		{"nest wins over global", "auto_trust: false\n", "    auto_trust: true\n", true},
		{"nest false alone", "", "    auto_trust: false\n", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			body := strings.Replace(profileGlobal, nestLine, nestLine+c.nest, 1)
			body = strings.Replace(body, "schema: 1\n", "schema: 1\n"+c.global, 1)
			r, err := resolveRich(t, body)
			if err != nil {
				t.Fatal(err)
			}
			if r.AutoTrust != c.want {
				t.Errorf("AutoTrust = %t, want %t", r.AutoTrust, c.want)
			}
		})
	}
}
