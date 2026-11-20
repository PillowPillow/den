package plugin

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type marketplaceManifest struct {
	Name  string `json:"name"`
	Owner struct {
		Name string `json:"name"`
	} `json:"owner"`
	Plugins []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Source      struct {
			Source string `json:"source"`
			URL    string `json:"url"`
			Path   string `json:"path"`
			Ref    string `json:"ref"`
		} `json:"source"`
	} `json:"plugins"`
}

type pluginManifest struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
}

var (
	semverRe       = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	changelogRe    = regexp.MustCompile(`^## v(\d+\.\d+\.\d+) `)
	marketplaceRel = filepath.Join(".claude-plugin", "marketplace.json")
	pluginDirRel   = filepath.Join("plugins", "den")
)

// moduleRoot climbs from the current directory up to the one holding go.mod.
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

func decodeStrict(t *testing.T, path string, into any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
}

func readMarketplace(t *testing.T, root string) marketplaceManifest {
	t.Helper()
	var m marketplaceManifest
	decodeStrict(t, filepath.Join(root, marketplaceRel), &m)
	return m
}

func readPluginManifest(t *testing.T, root string) pluginManifest {
	t.Helper()
	var p pluginManifest
	decodeStrict(t, filepath.Join(root, pluginDirRel, ".claude-plugin", "plugin.json"), &p)
	return p
}

func readPluginVersion(t *testing.T, root string) string {
	t.Helper()
	return readPluginManifest(t, root).Version
}

func changelogVersion(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "CHANGELOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if m := changelogRe.FindStringSubmatch(line); m != nil {
			return m[1]
		}
	}
	t.Fatal("CHANGELOG.md has no `## vX.Y.Z ` section")
	return ""
}

func TestMarketplaceManifest(t *testing.T) {
	m := readMarketplace(t, moduleRoot(t))
	if m.Name != "den" {
		t.Errorf("name = %q, want den", m.Name)
	}
	if m.Owner.Name != "PillowPillow" {
		t.Errorf("owner.name = %q, want PillowPillow", m.Owner.Name)
	}
	if len(m.Plugins) != 1 {
		t.Fatalf("plugins = %d entries, want 1", len(m.Plugins))
	}
	p := m.Plugins[0]
	if p.Name != "den" {
		t.Errorf("plugins[0].name = %q, want den", p.Name)
	}
	if p.Source.Source != "git-subdir" {
		t.Errorf("source.source = %q, want git-subdir", p.Source.Source)
	}
	if p.Source.URL != "https://github.com/PillowPillow/den.git" {
		t.Errorf("source.url = %q", p.Source.URL)
	}
	if p.Source.Path != "plugins/den" {
		t.Errorf("source.path = %q, want plugins/den", p.Source.Path)
	}
}

func TestPluginManifest(t *testing.T) {
	p := readPluginManifest(t, moduleRoot(t))
	if p.Name != "den" {
		t.Errorf("name = %q, want den", p.Name)
	}
	if !semverRe.MatchString(p.Version) {
		t.Errorf("version = %q, want MAJOR.MINOR.PATCH", p.Version)
	}
	if p.Description == "" {
		t.Error("description is empty")
	}
}

func TestPluginVersionMatchesChangelog(t *testing.T) {
	root := moduleRoot(t)
	version := readPluginVersion(t, root)
	latest := changelogVersion(t, root)
	if version != latest {
		t.Errorf("plugin.json version %q differs from CHANGELOG.md's latest section v%s: bump plugins/den/.claude-plugin/plugin.json in the release commit", version, latest)
	}
}

func TestMarketplaceRefIsReleaseTag(t *testing.T) {
	root := moduleRoot(t)
	version := readPluginVersion(t, root)
	m := readMarketplace(t, root)
	if len(m.Plugins) != 1 {
		t.Fatalf("plugins = %d entries, want 1", len(m.Plugins))
	}
	if ref := m.Plugins[0].Source.Ref; ref != "v"+version {
		t.Errorf("marketplace.json ref %q is not v%s: the release commit must move it with plugin.json", ref, version)
	}
}

func TestSkillsHaveFrontmatter(t *testing.T) {
	root := moduleRoot(t)
	files, err := filepath.Glob(filepath.Join(root, pluginDirRel, "skills", "*", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		folder := filepath.Base(filepath.Dir(file))
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if !strings.HasPrefix(text, "---\n") {
			t.Errorf("%s: does not start with a --- frontmatter fence", file)
			continue
		}
		block, body, found := strings.Cut(text[len("---\n"):], "\n---")
		if !found {
			t.Errorf("%s: frontmatter is not closed by ---", file)
			continue
		}
		var fm struct {
			Name        string `yaml:"name"`
			Description string `yaml:"description"`
		}
		if err := yaml.Unmarshal([]byte(block), &fm); err != nil {
			t.Errorf("%s: decoding frontmatter: %v", file, err)
			continue
		}
		if fm.Name != folder {
			t.Errorf("%s: name %q differs from its folder %q", file, fm.Name, folder)
		}
		if fm.Description == "" {
			t.Errorf("%s: description is empty", file)
		}
		if !strings.Contains(body, "${CLAUDE_PLUGIN_ROOT}/rules.md") {
			t.Errorf("%s: body does not reference ${CLAUDE_PLUGIN_ROOT}/rules.md", file)
		}
	}
}

func TestNoGoFileInPlugin(t *testing.T) {
	root := moduleRoot(t)
	err := filepath.WalkDir(filepath.Join(root, pluginDirRel), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".go") {
			t.Errorf("%s: plugins/den must hold no Go file", path)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
