package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, body string) string {
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

const minimalNest = `schema: 1
nests:
  am:
    template: b
    profile: p
    repos: [{path: ./r}]
`

func mustFail(t *testing.T, body string, global bool, want ...string) {
	t.Helper()
	p := writeFile(t, t.TempDir(), "den.yaml", body)
	_, err := Load(p, global)
	if err == nil {
		t.Fatalf("Load succeeded, want an error containing %q", want)
	}
	for _, w := range append(want, p) {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error %q lacks %q", err, w)
		}
	}
}

func TestLoadRejectsAnUnknownKey(t *testing.T) {
	mustFail(t, strings.Replace(minimalNest, "profile: p", "profile: p\n    egres: x", 1), false, "egres")
}

func TestLoadRejectsGlobalKeysInAProjectFile(t *testing.T) {
	for _, key := range []string{"worktrees: {layout: central}", "env: {A: b}", "network: {allow: [x.io]}"} {
		mustFail(t, minimalNest+key+"\n", false, strings.SplitN(key, ":", 2)[0], "move it to")
	}
}

func TestLoadAbsolutizesRelativePaths(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "proj/den.yaml", `schema: 1
nests:
  am:
    template: b
    profile: p
    repos: [{path: ../x}]
    layers: [./l.yaml]
    kits: ["../k", "./kits/env", "git+https://h/r#dir=k", "oci://r/k:1"]
`)
	l, err := Load(p, false)
	if err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(dir, "proj")
	n := l.File.Nests["am"]
	checks := map[string][2]string{
		"repo":       {n.Repos[0].Path, filepath.Join(dir, "x")},
		"layer":      {n.Layers[0], filepath.Join(proj, "l.yaml")},
		"kit":        {n.Kits[1], filepath.Join(proj, "kits/env")},
		"upkit":      {n.Kits[0], filepath.Join(dir, "k")},
		"git kit":    {n.Kits[2], "git+https://h/r#dir=k"},
		"oci kit":    {n.Kits[3], "oci://r/k:1"},
		"Loaded.Dir": {l.Dir, proj},
	}
	for name, c := range checks {
		if c[0] != c[1] {
			t.Errorf("%s = %q, want %q", name, c[0], c[1])
		}
	}
}

func TestLoadExpandsTildeInPaths(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	p := writeFile(t, t.TempDir(), "den.yaml", strings.Replace(minimalNest, "./r", "~/src/r", 1))
	l, err := Load(p, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := l.File.Nests["am"].Repos[0].Path; got != "/home/u/src/r" {
		t.Errorf("repo path = %q", got)
	}
}

func TestLoadRejectsDotStartInGlobal(t *testing.T) {
	body := strings.Replace(minimalNest, "profile: p", "profile: p\n    start: .", 1)
	mustFail(t, body, true, "nests.am.start")
	p := writeFile(t, t.TempDir(), "den.yaml", body)
	if _, err := Load(p, false); err != nil {
		t.Errorf("dot start in a project file: %v", err)
	}
}

func TestLoadRejectsBadStart(t *testing.T) {
	for _, s := range []string{"../x", "r/../../x", "/abs"} {
		mustFail(t, strings.Replace(minimalNest, "profile: p", "profile: p\n    start: "+s, 1), false, "nests.am.start")
	}
}

func TestLoadRejectsDuplicateSandboxPort(t *testing.T) {
	body := strings.Replace(minimalNest, "profile: p", "profile: p\n    ports:\n      - {name: a, sandbox: 80}\n      - {name: b, sandbox: 80, host: 8080}", 1)
	mustFail(t, body, false, "nests.am.ports[1].sandbox")
}

func TestLoadRejectsPortOutOfRange(t *testing.T) {
	body := strings.Replace(minimalNest, "profile: p", "profile: p\n    ports:\n      - {sandbox: 70000}\n      - {sandbox: 80, host: -1}", 1)
	mustFail(t, body, false, "nests.am.ports[0].sandbox", "nests.am.ports[1].host")
}

func TestLoadAcceptsEphemeralHostPort(t *testing.T) {
	body := strings.Replace(minimalNest, "profile: p", "profile: p\n    ports:\n      - {sandbox: 80, host: 0}", 1)
	p := writeFile(t, t.TempDir(), "den.yaml", body)
	if _, err := Load(p, false); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRejectsRepoWithPathAndKey(t *testing.T) {
	mustFail(t, strings.Replace(minimalNest, "[{path: ./r}]", "[{path: ./r, key: k}]", 1), false, "nests.am.repos[0]")
	mustFail(t, strings.Replace(minimalNest, "[{path: ./r}]", "[{optional: true}]", 1), false, "nests.am.repos[0]")
}

func TestLoadAcceptsRepoKeyWithoutCheckingTheGlobalMap(t *testing.T) {
	p := writeFile(t, t.TempDir(), "den.yaml", strings.Replace(minimalNest, "{path: ./r}", "{key: unknown}", 1))
	if _, err := Load(p, false); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRejectsIncompleteNest(t *testing.T) {
	mustFail(t, "schema: 1\nnests:\n  am: {}\n", false,
		"nests.am.repos")
}

func TestLoadRejectsBadNamesAndLayout(t *testing.T) {
	mustFail(t, "schema: 1\nnests:\n  a.b: {template: b, profile: p, repos: [{key: k}]}\n", false, "nests.a.b")
	mustFail(t, "schema: 1\ntemplates:\n  x/y: {dir: /d}\n", false, "templates.x/y")
	mustFail(t, strings.Replace(minimalNest, "profile: p", "profile: a/b", 1), false, "nests.am.profile")
	mustFail(t, "schema: 1\nworktrees: {layout: flat}\n", true, "worktrees.layout")
}

func TestLoadRejectsWrongSchema(t *testing.T) {
	mustFail(t, strings.Replace(minimalNest, "schema: 1", "schema: 2", 1), false, "schema")
	mustFail(t, "", false, "schema")
}

func TestLoadReportsEveryProblemTogether(t *testing.T) {
	mustFail(t, "schema: 1\ntemplates:\n  b: {}\nnests:\n  am: {}\n", false, "templates.b: set exactly one of repo and dir", "nests.am.repos")
}

func TestLoadMakesARelativePathAbsolute(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "den.yaml", minimalNest)
	wd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	l, err := Load("den.yaml", false)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(l.Path) || !filepath.IsAbs(l.Dir) {
		t.Errorf("Path=%q Dir=%q, want absolute", l.Path, l.Dir)
	}
}

func TestLoadGlobalAbsentIsEmpty(t *testing.T) {
	home := t.TempDir()
	l, err := LoadGlobal(home)
	if err != nil {
		t.Fatal(err)
	}
	if !l.Global || l.Path != GlobalFile(home) || len(l.File.Nests) != 0 {
		t.Errorf("unexpected Loaded: %+v", l)
	}
}

func TestLoadGlobalReadsTheFile(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, FileName, "schema: 1\nrepos: {a: /x}\n")
	l, err := LoadGlobal(home)
	if err != nil {
		t.Fatal(err)
	}
	if l.File.Repos["a"].Path != "/x" {
		t.Errorf("repos = %v", l.File.Repos)
	}
}

func TestLoadMissingFileNamesIt(t *testing.T) {
	p := filepath.Join(t.TempDir(), "den.yaml")
	_, err := Load(p, false)
	if err == nil || !strings.Contains(err.Error(), p) || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("err = %v", err)
	}
}

func TestExampleDenHomeIsValid(t *testing.T) {
	if _, err := Load(filepath.Join("..", "..", "examples", "den-home", "den.yaml"), true); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRefusesProfilesInAProjectFile(t *testing.T) {
	mustFail(t, minimalNest+"profiles:\n  perso: [./kits/perso]\n", false, "profiles", "global-only", "global den.yaml")
}

func TestLoadAcceptsANestWithoutProfile(t *testing.T) {
	body := strings.Replace(minimalNest, "    profile: p\n", "", 1)
	p := writeFile(t, t.TempDir(), "den.yaml", body)
	if _, err := Load(p, false); err != nil {
		t.Fatal(err)
	}
}

func TestLoadAbsolutizesProfileKitsAndValidatesNames(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "den.yaml", "schema: 1\nprofile: perso\nprofiles:\n  perso: [./kits/perso, \"docker.io/x/y:1\"]\n")
	l, err := Load(p, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := l.File.Profiles["perso"]; got[0] != filepath.Join(l.Dir, "kits", "perso") || got[1] != "docker.io/x/y:1" {
		t.Errorf("profile kits %v", got)
	}
	mustFail(t, "schema: 1\nprofiles:\n  a/b: []\n", true, "profiles.a/b")
	mustFail(t, "schema: 1\nprofile: a/b\n", true, "profile:")
}

const templatesBlock = `templates:
  x: {repo: "git@h:o/r.git", ref: main}
  y: {dir: ./tpl, name: other}
default_template: x
`

func TestLoadAcceptsTemplatesInBothFiles(t *testing.T) {
	for _, global := range []bool{true, false} {
		p := writeFile(t, t.TempDir(), "den.yaml", "schema: 1\n"+templatesBlock)
		l, err := Load(p, global)
		if err != nil {
			t.Fatalf("global=%v: %v", global, err)
		}
		if l.File.DefaultTemplate != "x" || l.File.Templates["x"].Ref != "main" {
			t.Errorf("global=%v: templates not decoded: %+v", global, l.File)
		}
		if got := l.File.Templates["y"].TemplateName("y"); got != "other" {
			t.Errorf("TemplateName = %q, want other", got)
		}
		if got := l.File.Templates["x"].TemplateName("x"); got != "x" {
			t.Errorf("TemplateName = %q, want x", got)
		}
	}
}

func TestLoadRefusesATemplateEntryWithoutOneLocation(t *testing.T) {
	mustFail(t, "schema: 1\ntemplates:\n  x: {repo: r, dir: /d}\n", true, "templates.x: set exactly one of repo and dir")
	mustFail(t, "schema: 1\ntemplates:\n  x: {}\n", true, "templates.x: set exactly one of repo and dir")
}

func TestLoadRefusesRefOnADir(t *testing.T) {
	mustFail(t, "schema: 1\ntemplates:\n  x: {dir: /d, ref: main}\n", true, "templates.x.ref: only a repo has a ref")
}

func TestLoadRefusesARepoStartingWithADash(t *testing.T) {
	mustFail(t, "schema: 1\ntemplates:\n  x: {repo: --upload-pack=x}\n", true, `templates.x.repo: must not start with "-"`)
}

func TestLoadAnchorsATemplateDir(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	dir := t.TempDir()
	p := writeFile(t, dir, "p/den.yaml", "schema: 1\ntemplates:\n  a: {dir: ./tpl}\n  b: {dir: \"~/t\"}\n")
	l, err := Load(p, false)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := l.File.Templates["a"].Dir, filepath.Join(dir, "p", "tpl"); got != want {
		t.Errorf("a.Dir = %q, want %q", got, want)
	}
	if got := l.File.Templates["b"].Dir; got != "/home/u/t" {
		t.Errorf("b.Dir = %q, want /home/u/t", got)
	}
}

func TestLoadAcceptsANestWithoutBase(t *testing.T) {
	p := writeFile(t, t.TempDir(), "den.yaml", "schema: 1\nnests:\n  dev:\n    template: x\n    repos: [{path: ./r}]\n")
	l, err := Load(p, false)
	if err != nil {
		t.Fatal(err)
	}
	if l.File.Nests["dev"].Template != "x" {
		t.Errorf("template = %q", l.File.Nests["dev"].Template)
	}
}

func TestLoadRefusesRemovedKeys(t *testing.T) {
	p := writeFile(t, t.TempDir(), "den.yaml", `schema: 1
bases:
  b: {image: "b:v1"}
agents:
  claude: {image: x}
nests:
  dev:
    base: b
    repos: [{path: ./r}]
`)
	_, err := Load(p, true)
	if err == nil {
		t.Fatal("want a refusal")
	}
	want := p + ": invalid den.yaml:\n" +
		"  bases: removed — declare the template in a den-template.yaml and point at it with templates.<name>.dir or .repo\n" +
		"  agents: removed — set default_template to an image, e.g. claude-code-docker\n" +
		"  nests.dev.base: renamed — use nests.dev.template"
	if err.Error() != want {
		t.Errorf("error\n%s\nwant\n%s", err, want)
	}
}

func loadRepos(t *testing.T, global bool, repos string) *Loaded {
	t.Helper()
	p := writeFile(t, t.TempDir(), "den.yaml", "schema: 1\nrepos:\n"+repos)
	l, err := Load(p, global)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestGlobalReposScalarStillLoads(t *testing.T) {
	l := loadRepos(t, true, "  js.box: /x/js.box\n")
	if got, want := l.File.Repos["js.box"], (RepoDecl{Path: "/x/js.box"}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestGlobalRepoMayCarryOriginAndPath(t *testing.T) {
	l := loadRepos(t, true, "  api: {origin: 'git@github.com:acme/api.git', path: /src/api}\n")
	if got := l.File.Repos["api"]; got.Origin != "git@github.com:acme/api.git" || got.Path != "/src/api" {
		t.Errorf("got %+v", got)
	}
}

func TestGlobalRepoNeedsPath(t *testing.T) {
	mustFail(t, "schema: 1\nrepos:\n  api: {origin: 'git@github.com:acme/api.git'}\n", true, "repos.api", "set `path`")
}

func TestProjectReposNeedExactlyOneOfOriginAndPath(t *testing.T) {
	for _, decl := range []string{"{}", "{origin: 'git@github.com:acme/api.git', path: ./api}"} {
		mustFail(t, "schema: 1\nrepos:\n  api: "+decl+"\n", false, "repos.api: set exactly one of `origin` and `path`")
	}
}

func TestProjectReposAcceptOriginOrPath(t *testing.T) {
	l := loadRepos(t, false, "  api: {origin: 'https://github.com/acme/api.git'}\n  scratch: ./s\n")
	if l.File.Repos["api"].Origin == "" || l.File.Repos["scratch"].Path != filepath.Join(l.Dir, "s") {
		t.Errorf("got %+v", l.File.Repos)
	}
}

func TestProjectReposRelativePathIsAnchored(t *testing.T) {
	l := loadRepos(t, false, "  scratch: {path: ../scratch}\n")
	if got, want := l.File.Repos["scratch"].Path, filepath.Join(filepath.Dir(l.Dir), "scratch"); got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}

func TestReposRefusesBadOrigin(t *testing.T) {
	mustFail(t, "schema: 1\nrepos:\n  api: {origin: /local}\n", false, "repos.api.origin: not a git remote URL")
}

func TestReposBadOriginSaysItOnce(t *testing.T) {
	p := writeFile(t, t.TempDir(), "den.yaml", "schema: 1\nrepos:\n  api: {origin: /local}\n")
	_, err := Load(p, false)
	if err == nil || strings.Count(err.Error(), "not a git remote URL") != 1 {
		t.Fatalf("error %v, want `not a git remote URL` once", err)
	}
}

func TestReposRefusesADuplicateField(t *testing.T) {
	for _, decl := range []string{
		"{origin: 'https://github.com/acme/a.git', origin: 'https://github.com/acme/b.git'}",
		"{path: ./a, path: ./b}",
	} {
		mustFail(t, "schema: 1\nrepos:\n  api: "+decl+"\n", false, "repos entry", "twice")
	}
}

func TestReposRefusesBadKey(t *testing.T) {
	for _, key := range []string{"a/b", ".x", ".."} {
		mustFail(t, "schema: 1\nrepos:\n  "+key+": /x\n", true, "repos."+key)
	}
}

func TestReposRefusesAKeyWithAControlOrFormatCharacter(t *testing.T) {
	for _, key := range []string{`"a\nb"`, `"a\e[2Jb"`, `"a\u202eb"`} {
		mustFail(t, "schema: 1\nrepos:\n  "+key+": {origin: 'git@github.com:acme/api.git'}\n", false, "holds a control, format or invalid character")
	}
}

func TestReposRefusesUnknownField(t *testing.T) {
	mustFail(t, "schema: 1\nrepos:\n  api: {origin: 'git@github.com:acme/api.git', url: x}\n", false, "url")
}

func TestLoadRefusesAutoTrustAtTheTopOfAProjectFile(t *testing.T) {
	mustFail(t, minimalNest+"auto_trust: false\n", false, "auto_trust", "global-only", "global den.yaml")
}

func TestLoadRefusesAddDirsAtTheTopOfAProjectFile(t *testing.T) {
	mustFail(t, minimalNest+"add_dirs: true\n", false, "add_dirs", "global-only", "global den.yaml")
}

func TestLoadReadsAddDirsOnANestOfAProjectFile(t *testing.T) {
	p := writeFile(t, t.TempDir(), "den.yaml", strings.Replace(minimalNest, "    profile: p\n", "    profile: p\n    add_dirs: true\n", 1))
	l, err := Load(p, false)
	if err != nil {
		t.Fatal(err)
	}
	if v := l.File.Nests["am"].AddDirs; v == nil || !*v {
		t.Errorf("nest add_dirs = %v, want true", v)
	}
}

func TestLoadReadsAutoTrustOnANestOfAProjectFile(t *testing.T) {
	p := writeFile(t, t.TempDir(), "den.yaml", strings.Replace(minimalNest, "    profile: p\n", "    profile: p\n    auto_trust: false\n", 1))
	l, err := Load(p, false)
	if err != nil {
		t.Fatal(err)
	}
	if v := l.File.Nests["am"].AutoTrust; v == nil || *v {
		t.Errorf("nest auto_trust = %v, want false", v)
	}
}

func nestWithLine(line string) string {
	return strings.Replace(minimalNest, "    profile: p\n", "    profile: p\n    "+line+"\n", 1)
}

func TestLoadRefusesResourcesAtTheTopOfAProjectFile(t *testing.T) {
	mustFail(t, minimalNest+"resources: {cpus: 2}\n", false, "resources", "global-only", "global den.yaml")
}

func TestLoadReadsResourcesOnANestAndAtTheTopOfTheGlobalFile(t *testing.T) {
	body := strings.Replace(nestWithLine("resources: {cpus: 4}"), "schema: 1\n", "schema: 1\nresources: {memory: 8g}\n", 1)
	l, err := Load(writeFile(t, t.TempDir(), "den.yaml", body), true)
	if err != nil {
		t.Fatal(err)
	}
	if r := l.File.Resources; r == nil || r.Memory != "8g" || r.CPUs != nil {
		t.Errorf("top-level resources = %+v, want memory 8g and no cpus", r)
	}
	if r := l.File.Nests["am"].Resources; r == nil || r.CPUs == nil || *r.CPUs != 4 || r.Memory != "" {
		t.Errorf("nest resources = %+v, want cpus 4 and no memory", r)
	}
}

func TestLoadRefusesABadCPUCount(t *testing.T) {
	for _, v := range []string{"0", "-1"} {
		t.Run(v, func(t *testing.T) {
			mustFail(t, nestWithLine("resources: {cpus: "+v+"}"), false, "nests.am.resources.cpus", "remove cpus")
		})
	}
	t.Run("fraction", func(t *testing.T) {
		mustFail(t, nestWithLine("resources: {cpus: 2.5}"), false, "2.5")
	})
}

func TestLoadRefusesABadMemorySize(t *testing.T) {
	for _, v := range []string{"8G", "8gb", "8", "0g", "1.5g", "'8 g'", "08g"} {
		t.Run(v, func(t *testing.T) {
			mustFail(t, nestWithLine("resources: {memory: "+v+"}"), false, "nests.am.resources.memory", "e.g. 8g")
		})
	}
}

func TestLoadRefusesABadMemorySizeAtTheTopOfTheGlobalFile(t *testing.T) {
	mustFail(t, minimalNest+"resources: {memory: 8GB}\n", true, "resources.memory", "e.g. 8g")
}

func TestLoadRefusesAnUnknownResourcesKey(t *testing.T) {
	mustFail(t, nestWithLine("resources: {memroy: 8g}"), false, "memroy")
}

func TestLoadAcceptsAWellFormedMemorySize(t *testing.T) {
	for _, v := range []string{"512m", "8g", "100000g"} {
		t.Run(v, func(t *testing.T) {
			if _, err := Load(writeFile(t, t.TempDir(), "den.yaml", nestWithLine("resources: {memory: "+v+"}")), false); err != nil {
				t.Errorf("Load refused memory %s: %v", v, err)
			}
		})
	}
}

func TestLoadRefusesACPUCountNotWrittenInDecimal(t *testing.T) {
	for _, v := range []string{"010", "0b11", "0x10", "1_000", "+4"} {
		t.Run(v, func(t *testing.T) {
			mustFail(t, nestWithLine("resources: {cpus: "+v+"}"), false, v, "decimal")
		})
	}
}
