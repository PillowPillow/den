package template

import (
	"errors"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/config"
)

func loaded(path string, global bool, f config.File) *config.Loaded {
	return &config.Loaded{Path: path, Dir: path[:strings.LastIndex(path, "/")], Global: global, File: f}
}

func entries(keys ...string) map[string]config.TemplateEntry {
	m := map[string]config.TemplateEntry{}
	for _, k := range keys {
		m[k] = config.TemplateEntry{Dir: "/t/" + k}
	}
	return m
}

func TestChooseTakesTheNestThenTheDefaults(t *testing.T) {
	global := loaded("/h/.den/den.yaml", true, config.File{Templates: entries("legacy", "g", "p"), DefaultTemplate: "g"})
	proj := func(nest config.Nest, def string) *config.Loaded {
		return loaded("/p/den.yaml", false, config.File{Templates: entries("p"), DefaultTemplate: def, Nests: map[string]config.Nest{"dev": nest}})
	}
	for _, tc := range []struct {
		name string
		file *config.Loaded
		want string
	}{
		{"nest", proj(config.Nest{Template: "legacy"}, "p"), "legacy"},
		{"nest file default", proj(config.Nest{}, "p"), "p"},
		{"global default", proj(config.Nest{}, ""), "g"},
	} {
		c, err := Choose(tc.file, global, "dev")
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if c.Value != tc.want || c.Key != tc.want {
			t.Fatalf("%s: Value=%q Key=%q, want %q", tc.name, c.Value, c.Key, tc.want)
		}
	}
}

func TestChooseRecordsTheOrigin(t *testing.T) {
	global := loaded("/h/.den/den.yaml", true, config.File{Templates: entries("g"), DefaultTemplate: "g"})
	proj := func(nest config.Nest, def string) *config.Loaded {
		return loaded("/p/den.yaml", false, config.File{Templates: entries("l", "p"), DefaultTemplate: def, Nests: map[string]config.Nest{"dev": nest}})
	}
	for _, tc := range []struct {
		file *config.Loaded
		want string
	}{
		{proj(config.Nest{Template: "l"}, "p"), "`template:` of nest \"dev\" in /p/den.yaml"},
		{proj(config.Nest{}, "p"), "`default_template` in /p/den.yaml"},
		{proj(config.Nest{}, ""), "`default_template` in /h/.den/den.yaml"},
	} {
		c, err := Choose(tc.file, global, "dev")
		if err != nil {
			t.Fatal(err)
		}
		if c.Origin != tc.want {
			t.Fatalf("Origin = %q, want %q", c.Origin, tc.want)
		}
	}
}

func TestChooseRefusesWithoutAnyTemplate(t *testing.T) {
	global := loaded("/h/.den/den.yaml", true, config.File{})
	proj := loaded("/p/den.yaml", false, config.File{Nests: map[string]config.Nest{"dev": {}}})
	_, err := Choose(proj, global, "dev")
	var nt *NoTemplateError
	if !errors.As(err, &nt) {
		t.Fatalf("err = %v, want *NoTemplateError", err)
	}
	for _, want := range []string{`nest "dev" has no template`, "/p/den.yaml or /h/.den/den.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q lacks %q", err, want)
		}
	}
}

func TestChooseSearchesTheGlobalFileOnceForAGlobalNest(t *testing.T) {
	global := loaded("/h/.den/den.yaml", true, config.File{Templates: entries("g"), DefaultTemplate: "g", Nests: map[string]config.Nest{"dev": {}}})
	c, err := Choose(global, global, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Searched) != 1 || c.Origin != "`default_template` in /h/.den/den.yaml" {
		t.Fatalf("Searched=%v Origin=%q", c.Searched, c.Origin)
	}
	global.File.DefaultTemplate = ""
	_, err = Choose(global, global, "dev")
	if err == nil || strings.Count(err.Error(), "/h/.den/den.yaml") != 1 {
		t.Fatalf("err = %v, want the file named once", err)
	}
}

func TestLookupProjectMasksGlobal(t *testing.T) {
	global := loaded("/h/.den/den.yaml", true, config.File{Templates: entries("digitaleo")})
	proj := loaded("/p/den.yaml", false, config.File{Templates: entries("digitaleo")})
	c, err := Lookup(proj, global, "digitaleo")
	if err != nil {
		t.Fatal(err)
	}
	if c.DeclaredIn != proj || c.IsImage() || c.Name() != "digitaleo" || c.Tag() != "digitaleo:v2" {
		t.Fatalf("choice = %+v", c)
	}
}

func TestChooseNameFollowsTheEntryName(t *testing.T) {
	global := loaded("/h/.den/den.yaml", true, config.File{Templates: map[string]config.TemplateEntry{"k": {Dir: "/t", Name: "real"}}})
	c, err := Lookup(nil, global, "k")
	if err != nil {
		t.Fatal(err)
	}
	if c.Key != "k" || c.Name() != "real" || c.Tag() != "real:v2" {
		t.Fatalf("choice = %+v", c)
	}
}

func TestLookupFallsBackToAnImage(t *testing.T) {
	global := loaded("/h/.den/den.yaml", true, config.File{})
	for value, want := range map[string]string{
		"claude-code-docker": "docker.io/docker/sandbox-templates:claude-code-docker",
		"claude-code":        "docker.io/docker/sandbox-templates:claude-code",
		"ghcr.io/acme/x:1":   "ghcr.io/acme/x:1",
	} {
		c, err := Lookup(nil, global, value)
		if err != nil {
			t.Fatalf("%s: %v", value, err)
		}
		if !c.IsImage() || c.Image != want || c.Key != "" || c.DeclaredIn != nil || c.Value != value {
			t.Fatalf("%s: choice = %+v", value, c)
		}
	}
	proj := loaded("/p/den.yaml", false, config.File{Nests: map[string]config.Nest{"dev": {Template: "claude-code"}}})
	c, err := Choose(proj, global, "dev")
	if err != nil || c.Image != "docker.io/docker/sandbox-templates:claude-code" {
		t.Fatalf("Choose = %+v, %v", c, err)
	}
}

func TestLookupRefusesAValueThatIsNeither(t *testing.T) {
	global := loaded("/h/.den/den.yaml", true, config.File{})
	proj := loaded("/p/den.yaml", false, config.File{})
	_, err := Lookup(proj, global, "ubuntu:22.04")
	if err == nil {
		t.Fatal("want a refusal")
	}
	for _, want := range []string{"/p/den.yaml", "/h/.den/den.yaml", "write the full address", "ubuntu:22.04"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q lacks %q", err, want)
		}
	}
}

func TestLookupHandlesNoProjectFile(t *testing.T) {
	global := loaded("/h/.den/den.yaml", true, config.File{Templates: entries("g")})
	c, err := Lookup(nil, global, "g")
	if err != nil || c.DeclaredIn != global || len(c.Searched) != 1 || c.Searched[0] != "/h/.den/den.yaml" {
		t.Fatalf("choice = %+v, err = %v", c, err)
	}
	_, err = Lookup(nil, global, "nope:1")
	if err == nil || strings.Contains(err.Error(), "<nil>") {
		t.Fatalf("err = %v", err)
	}
}

func TestCompleteImage(t *testing.T) {
	for _, bad := range []string{"", "-x", "a:b", strings.Repeat("a", 129), "a b"} {
		if _, err := CompleteImage(bad); err == nil {
			t.Errorf("CompleteImage(%q) accepted", bad)
		}
	}
	if got, err := CompleteImage("_a.b-c"); err != nil || got != OfficialRepository+":_a.b-c" {
		t.Errorf("got %q, %v", got, err)
	}
}
