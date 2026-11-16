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
bases:
  b: {image: "b:v1"}
nests:
  am:
    base: b
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
	for _, key := range []string{"worktrees: {layout: central}", "repos: {a: /x}", "env: {A: b}", "network: {allow: [x.io]}"} {
		mustFail(t, minimalNest+key+"\n", false, strings.SplitN(key, ":", 2)[0], "move it to")
	}
}

func TestLoadAbsolutizesRelativePaths(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "proj/den.yaml", `schema: 1
bases:
  b:
    image: "b:v1"
    kits: ["./kits/env", "git+https://h/r#dir=k", "oci://r/k:1"]
    build:
      includes: [./inc.yaml]
      steps: [./b/node.sh]
nests:
  am:
    base: b
    profile: p
    repos: [{path: ../x}]
    layers: [./l.yaml]
    kits: ["../k"]
`)
	l, err := Load(p, false)
	if err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(dir, "proj")
	b, n := l.File.Bases["b"], l.File.Nests["am"]
	checks := map[string][2]string{
		"repo":       {n.Repos[0].Path, filepath.Join(dir, "x")},
		"step":       {b.Build.Steps[0], filepath.Join(proj, "b/node.sh")},
		"include":    {b.Build.Includes[0], filepath.Join(proj, "inc.yaml")},
		"layer":      {n.Layers[0], filepath.Join(proj, "l.yaml")},
		"kit":        {b.Kits[0], filepath.Join(proj, "kits/env")},
		"upkit":      {n.Kits[0], filepath.Join(dir, "k")},
		"git kit":    {b.Kits[1], "git+https://h/r#dir=k"},
		"oci kit":    {b.Kits[2], "oci://r/k:1"},
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

func TestLoadRejectsIncompleteNestAndBase(t *testing.T) {
	mustFail(t, "schema: 1\nnests:\n  am: {}\n", false,
		"nests.am.repos")
	mustFail(t, "schema: 1\nbases:\n  b: {}\n", false, "bases.b.image")
}

func TestLoadRejectsBadNamesAndLayout(t *testing.T) {
	mustFail(t, "schema: 1\nnests:\n  a.b: {base: b, profile: p, repos: [{key: k}]}\n", false, "nests.a.b")
	mustFail(t, "schema: 1\nbases:\n  x/y: {image: i}\n", false, "bases.x/y")
	mustFail(t, strings.Replace(minimalNest, "profile: p", "profile: a/b", 1), false, "nests.am.profile")
	mustFail(t, "schema: 1\nworktrees: {layout: flat}\n", true, "worktrees.layout")
}

func TestLoadRejectsWrongSchema(t *testing.T) {
	mustFail(t, strings.Replace(minimalNest, "schema: 1", "schema: 2", 1), false, "schema")
	mustFail(t, "", false, "schema")
}

func TestLoadReportsEveryProblemTogether(t *testing.T) {
	mustFail(t, "schema: 1\nbases:\n  b: {}\nnests:\n  am: {}\n", false, "bases.b.image", "nests.am.repos")
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
	if l.File.Repos["a"] != "/x" {
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

const digest64 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestLoadAcceptsAnAgentImageWithDigest(t *testing.T) {
	p := writeFile(t, t.TempDir(), "den.yaml", "schema: 1\nagents:\n  claude:\n    image: docker.io/docker/sandbox-templates@sha256:"+digest64+"\n")
	l, err := Load(p, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := l.File.Agents["claude"].Image; got != "docker.io/docker/sandbox-templates@sha256:"+digest64 {
		t.Errorf("image = %q", got)
	}
}

func TestLoadRefusesAnAgentImageWithoutDigest(t *testing.T) {
	mustFail(t, "schema: 1\nagents:\n  claude:\n    image: docker.io/docker/sandbox-templates:claude-code-docker\n", true, "agents.claude.image", "@sha256:")
}

func TestLoadRefusesAnUnknownAgent(t *testing.T) {
	mustFail(t, "schema: 1\nagents:\n  codex:\n    image: x.io/codex@sha256:"+digest64+"\n", true, "agents.codex", "claude")
}

func TestLoadRefusesAgentsInAProjectFile(t *testing.T) {
	mustFail(t, minimalNest+"agents:\n  claude:\n    image: x.io/c@sha256:"+digest64+"\n", false, "agents", "move it to")
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
