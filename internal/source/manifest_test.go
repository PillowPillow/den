package source

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validManifestYAML is the Digitaleo tracer bullet, reduced to two nests. Every
// document-level test below starts from it and breaks exactly one thing, so a
// failure names the rule that fired and not the fixture.
const validManifestYAML = `schema_version: 1
kind: source

metadata:
  name: dg
  version: 1.0.0

requires:
  den: ">=1.7.0"
  sbx: ">=0.38.0"

exports:
  nests:
    - { name: leo, path: nests/leo.yaml }
    - { name: go-dgdev, path: nests/go-dgdev.yaml }
  stacks:
    - { name: base, path: stacks/base/stack.yaml }

inputs:
  credentials:
    gitlab_token:
      prompt: GitLab personal access token

resources:
  credentials:
    - { id: github, type: sbx_github, scope: global }
    - id: gitlab_registry
      type: sbx_registry
      scope: global
      host: gitlab.example.test:4567
      value_from: { credential: gitlab_token }
    - id: gitlab_http
      type: sbx_http_substitution
      scope: global
      host: gitlab.example.test
      environment: GITLAB_TOKEN
      value_from: { credential: gitlab_token }
  build_network:
    allow:
      - cdn.example.test
      - acli.example.test
  builds:
    - stack: base
`

const validStackYAML = `image: base:v1
base: claude
provision:
  steps: [provision/base.sh]
`

func nestYAML(stack string) string {
	return "stack: " + stack + "\nrepos:\n  - key: api\n    url: https://gitlab.example.test/team/api.git\n"
}

// writeManifest builds a root carrying nothing but den-source.yaml: enough for
// every judgment LoadManifest makes on the DOCUMENT alone.
func writeManifest(t *testing.T, yaml string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ManifestFile), []byte(yaml), 0o644); err != nil {
		t.Fatalf("writing the manifest: %v", err)
	}
	return root
}

// validManifestTree adds the files the manifest exports, so ValidateManifest
// and lint have something real to read.
func validManifestTree(t *testing.T) string {
	t.Helper()
	root := writeManifest(t, validManifestYAML)
	mkdirAll(t, filepath.Join(root, "stacks", "base", "provision"))
	mkdirAll(t, filepath.Join(root, "nests"))
	writeFile(t, filepath.Join(root, "stacks", "base", "stack.yaml"), validStackYAML)
	writeFile(t, filepath.Join(root, "stacks", "base", "provision", "base.sh"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(root, "nests", "leo.yaml"), nestYAML("base"))
	writeFile(t, filepath.Join(root, "nests", "go-dgdev.yaml"), nestYAML("base"))
	return root
}

func mkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func rewriteManifest(t *testing.T, root, yaml string) {
	t.Helper()
	writeFile(t, filepath.Join(root, ManifestFile), yaml)
}

func TestLoadManifestReadsTheDeclaredContract(t *testing.T) {
	m, err := LoadManifest(validManifestTree(t))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.Metadata.Name != "dg" || m.Metadata.Version != "1.0.0" {
		t.Errorf("metadata = %+v", m.Metadata)
	}
	if m.Requires.Den != ">=1.7.0" || m.Requires.Sbx != ">=0.38.0" {
		t.Errorf("requires = %+v", m.Requires)
	}
	if len(m.Exports.Nests) != 2 || m.Exports.Nests[0].Name != "leo" {
		t.Errorf("exports.nests = %+v", m.Exports.Nests)
	}
	if len(m.Exports.Stacks) != 1 || m.Exports.Stacks[0].Path != "stacks/base/stack.yaml" {
		t.Errorf("exports.stacks = %+v", m.Exports.Stacks)
	}
	if p := m.Inputs.Credentials["gitlab_token"].Prompt; p != "GitLab personal access token" {
		t.Errorf("inputs.credentials.gitlab_token.prompt = %q", p)
	}
	if len(m.Resources.Credentials) != 3 || m.Resources.Credentials[1].ValueFrom.Credential != "gitlab_token" {
		t.Errorf("resources.credentials = %+v", m.Resources.Credentials)
	}
	if len(m.Resources.BuildNetwork.Allow) != 2 {
		t.Errorf("resources.build_network.allow = %v", m.Resources.BuildNetwork.Allow)
	}
	if len(m.Resources.Builds) != 1 || m.Resources.Builds[0].Stack != "base" {
		t.Errorf("resources.builds = %+v", m.Resources.Builds)
	}
}

// snake_case is the schema, and strict decoding is what enforces it: a
// camelCase spelling is a key den would otherwise ignore in silence, leaving
// the source to install with an empty half of its contract (spec §4.2).
func TestLoadManifestRejectsCamelCase(t *testing.T) {
	root := writeManifest(t, `schemaVersion: 1
kind: source
metadata: { name: dg, version: 1.0.0 }
`)
	_, err := LoadManifest(root)
	if err == nil || !strings.Contains(err.Error(), "schemaVersion") {
		t.Fatalf("LoadManifest error = %v, expected the unknown key to be named", err)
	}
}

func TestLoadManifestRejectsUnknownCredentialType(t *testing.T) {
	root := validManifestTree(t)
	rewriteManifest(t, root, strings.Replace(validManifestYAML, "sbx_github", "shell_hook", 1))
	_, err := LoadManifest(root)
	if err == nil || !strings.Contains(err.Error(), `credential resource "github": type "shell_hook" is unsupported`) {
		t.Fatalf("LoadManifest error = %v", err)
	}
}

// Every row breaks the valid document in ONE way. The want string is the exact
// substring a user needs to find the line to fix.
func TestLoadManifestRefusesDocumentFaults(t *testing.T) {
	cases := []struct {
		name string
		old  string
		new  string
		want string
	}{
		{"unsupported schema", "schema_version: 1", "schema_version: 2", "schema_version: 2 is unsupported"},
		{"wrong kind", "kind: source", "kind: stack", `kind: "stack"`},
		{"invalid source name", "name: dg\n", "name: dg/prod\n", "metadata.name"},
		{"non-semver version", "version: 1.0.0", "version: 1.0", "metadata.version"},
		{"v-prefixed version", "version: 1.0.0", "version: v1.0.0", "metadata.version"},
		{"requirement without floor", `den: ">=1.7.0"`, `den: "1.7.0"`, "requires.den"},
		{"requirement with a range", `den: ">=1.7.0"`, `den: ">=1.7.0 <2.0.0"`, "requires.den"},
		{"duplicate export name", "{ name: go-dgdev, path: nests/go-dgdev.yaml }", "{ name: leo, path: nests/go-dgdev.yaml }", `exports.nests: "leo" is declared twice`},
		{"duplicate resource id", "id: gitlab_http", "id: gitlab_registry", `resources.credentials: "gitlab_registry" is declared twice`},
		{"undeclared credential input", "credential: gitlab_token }", "credential: unknown_token }", `credential resource "gitlab_registry": value_from.credential "unknown_token" is not declared`},
		{"registry without host", "      host: gitlab.example.test:4567\n", "", `credential resource "gitlab_registry": type "sbx_registry" requires "host"`},
		{"github with a host", "{ id: github, type: sbx_github, scope: global }", "{ id: github, type: sbx_github, scope: global, host: gitlab.example.test }", `credential resource "github": type "sbx_github" declares no "host"`},
		{"substitution without environment", "      environment: GITLAB_TOKEN\n", "", `credential resource "gitlab_http": type "sbx_http_substitution" requires "environment"`},
		{"unsupported scope", "{ id: github, type: sbx_github, scope: global }", "{ id: github, type: sbx_github, scope: sandbox }", `scope "sandbox" is unsupported`},
		{"build of an unexported stack", "- stack: base", "- stack: helper", `resources.builds: stack "helper" is not exported`},
		// NormalizeNetworkResource (internal/sbx/machine.go) appends ":443" to
		// anything without a colon: an empty entry would become ":443", and a
		// CIDR or a URL would become something sbx never accepts as a policy
		// rule. den refuses these at load rather than letting sbx reject a
		// mangled string deep inside Apply (spec §2).
		{"empty egress host", "- cdn.example.test\n", "- \"\"\n", "resources.build_network.allow"},
		{"whitespace-only egress host", "- cdn.example.test\n", "- \"   \"\n", "resources.build_network.allow"},
		// Padding is refused, not silently trimmed: sbx stores the string
		// verbatim, so a padded entry that slipped through would compare
		// unequal to what sbx actually holds.
		{"padded egress host", "- cdn.example.test\n", "- \"  cdn.example.test  \"\n", "resources.build_network.allow"},
		{"egress host with a scheme", "- cdn.example.test\n", "- https://cdn.example.test\n", "resources.build_network.allow"},
		{"egress host with a path", "- cdn.example.test\n", "- cdn.example.test/v1\n", "resources.build_network.allow"},
		// A CIDR is caught by the same path check: sbx's rule is a bare host
		// (optionally ported), never a range, and "/8" would become
		// "10.0.0.0/8:443" under NormalizeNetworkResource.
		{"CIDR egress host", "- cdn.example.test\n", "- 10.0.0.0/8\n", "resources.build_network.allow"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			yaml := strings.Replace(validManifestYAML, c.old, c.new, 1)
			if yaml == validManifestYAML {
				t.Fatalf("fixture defect: %q was not found in the valid manifest", c.old)
			}
			_, err := LoadManifest(writeManifest(t, yaml))
			if err == nil {
				t.Fatalf("expected a refusal mentioning %q", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, expected a mention of %q", err.Error(), c.want)
			}
		})
	}
}

func TestLoadManifestReportsAnAbsentFile(t *testing.T) {
	_, err := LoadManifest(t.TempDir())
	if err == nil {
		t.Fatal("expected an error on a root without den-source.yaml")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("error = %v, expected it to wrap os.ErrNotExist so callers can tell "+
			"a legacy source from a broken one", err)
	}
	if HasManifest(t.TempDir()) {
		t.Error("HasManifest is true on a root without den-source.yaml")
	}
	if !HasManifest(validManifestTree(t)) {
		t.Error("HasManifest is false on a manifested source")
	}
}

func TestValidateManifestAcceptsTheReferenceTree(t *testing.T) {
	root := validManifestTree(t)
	m, err := LoadManifest(root)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if errs := ValidateManifest(root, m); len(errs) != 0 {
		t.Fatalf("the reference tree does not validate: %v", errs)
	}
}

// The tree-level judgments: everything LoadManifest cannot see because it needs
// the filesystem. They accumulate like lint's, so one push fixes all of them.
func TestValidateManifestRefusesTreeFaults(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(t *testing.T, root string)
		want    string
	}{
		{
			name: "absolute path",
			prepare: func(t *testing.T, root string) {
				rewriteManifest(t, root, strings.Replace(validManifestYAML,
					"path: nests/leo.yaml", "path: /etc/leo.yaml", 1))
			},
			want: "is absolute",
		},
		{
			name: "path escaping the source",
			prepare: func(t *testing.T, root string) {
				rewriteManifest(t, root, strings.Replace(validManifestYAML,
					"path: nests/leo.yaml", "path: ../leo.yaml", 1))
			},
			want: "nests/leo.yaml",
		},
		{
			name: "path that is not where den loads the object",
			prepare: func(t *testing.T, root string) {
				rewriteManifest(t, root, strings.Replace(validManifestYAML,
					"path: nests/leo.yaml", "path: catalog/leo.yaml", 1))
				mkdirAll(t, filepath.Join(root, "catalog"))
				writeFile(t, filepath.Join(root, "catalog", "leo.yaml"), nestYAML("base"))
			},
			want: "nests/leo.yaml",
		},
		{
			name: "name disagreeing with the file",
			prepare: func(t *testing.T, root string) {
				rewriteManifest(t, root, strings.Replace(validManifestYAML,
					"{ name: leo, path: nests/leo.yaml }", "{ name: leo, path: nests/other.yaml }", 1))
				writeFile(t, filepath.Join(root, "nests", "other.yaml"), nestYAML("base"))
			},
			want: "nests/leo.yaml",
		},
		{
			name: "exported file that does not exist",
			prepare: func(t *testing.T, root string) {
				if err := os.Remove(filepath.Join(root, "nests", "leo.yaml")); err != nil {
					t.Fatal(err)
				}
			},
			want: "nests/leo.yaml",
		},
		{
			name: "symlink leaving the checkout",
			prepare: func(t *testing.T, root string) {
				outside := filepath.Join(t.TempDir(), "leo.yaml")
				writeFile(t, outside, nestYAML("base"))
				path := filepath.Join(root, "nests", "leo.yaml")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, path); err != nil {
					t.Fatal(err)
				}
			},
			want: "escapes the checkout",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := validManifestTree(t)
			c.prepare(t, root)
			m, err := LoadManifest(root)
			if err != nil {
				t.Fatalf("LoadManifest: %v", err)
			}
			errs := ValidateManifest(root, m)
			if len(errs) == 0 {
				t.Fatalf("expected a refusal mentioning %q", c.want)
			}
			var all []string
			for _, e := range errs {
				all = append(all, e.Error())
			}
			joined := strings.Join(all, " | ")
			if !strings.Contains(joined, c.want) {
				t.Errorf("errors = %q, expected a mention of %q", joined, c.want)
			}
		})
	}
}

// The binary floors. A den built by `task build` answers `git describe`, so its
// version carries a prerelease suffix on every commit past a tag and a "-dirty"
// on a modified checkout — neither may be read as "older than the tag", or the
// only documented way to build den would fail every floor it satisfies.
func TestCheckCompatibility(t *testing.T) {
	m, err := LoadManifest(validManifestTree(t))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	cases := []struct {
		name       string
		den, sbx   string
		wantErr    bool
		wantUnread bool
		want       string
	}{
		{name: "exact floors", den: "1.7.0", sbx: "0.38.0"},
		{name: "v prefix", den: "v1.7.0", sbx: "v0.38.0"},
		{name: "newer", den: "2.0.0", sbx: "0.40.1"},
		{name: "commits past the tag", den: "v1.7.0-3-gabc1234", sbx: "v0.38.0"},
		{name: "dirty checkout", den: "v1.7.0-dirty", sbx: "v0.38.0"},
		{name: "den too old", den: "1.6.9", sbx: "0.38.0", wantErr: true, want: "den 1.6.9"},
		{name: "sbx too old", den: "1.7.0", sbx: "0.37.9", wantErr: true, want: "sbx 0.37.9"},
		{name: "unreadable den version", den: "dev", sbx: "0.38.0", wantErr: true, wantUnread: true, want: "den"},
		{name: "unreadable sbx version", den: "1.7.0", sbx: "", wantErr: true, wantUnread: true, want: "sbx"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CheckCompatibility(m, c.den, c.sbx)
			if !c.wantErr {
				if err != nil {
					t.Fatalf("CheckCompatibility(%q, %q) = %v, expected it to pass", c.den, c.sbx, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckCompatibility(%q, %q) accepted an incompatible pair", c.den, c.sbx)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, expected a mention of %q", err.Error(), c.want)
			}
			// "too old" and "cannot be read" are two different remedies —
			// upgrade, versus build a stamped binary — and the CLI decides
			// what to do with each. It must not have to match text.
			var unreadable *UnknownVersionError
			if got := errors.As(err, &unreadable); got != c.wantUnread {
				t.Errorf("errors.As(*UnknownVersionError) = %v, want %v (error = %v)", got, c.wantUnread, err)
			}
		})
	}
}

// A requirement that is absent is not a floor of zero: it is a source that does
// not constrain that binary. It must not be turned into a refusal on a version
// den could not read either.
func TestCheckCompatibilitySkipsAbsentRequirements(t *testing.T) {
	root := writeManifest(t, strings.Replace(validManifestYAML,
		"requires:\n  den: \">=1.7.0\"\n  sbx: \">=0.38.0\"\n", "", 1))
	m, err := LoadManifest(root)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if err := CheckCompatibility(m, "dev", ""); err != nil {
		t.Errorf("CheckCompatibility = %v, expected no floor to check", err)
	}
}

// Lint is the ONE entry every consumer uses — `den lint`, `den source add`
// (which refuses and deletes the clone), `den source update` (fail-closed) and
// `den source ls`. Tested here rather than through each caller: a manifest
// judged in only one of them is exactly the divergence a single judge exists
// to make impossible (CLAUDE.md, spec 2026-08-04 §5).
func TestLintValidatesTheManifestAndItsCatalogue(t *testing.T) {
	if errs := Lint(validManifestTree(t)); len(errs) != 0 {
		t.Fatalf("the reference source does not lint clean: %v", errs)
	}

	// A document fault reaches the same entry.
	root := validManifestTree(t)
	rewriteManifest(t, root, strings.Replace(validManifestYAML, "sbx_github", "shell_hook", 1))
	errs := Lint(root)
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "shell_hook") {
		t.Fatalf("Lint = %v, expected the unsupported credential type", errs)
	}

	// A tree fault reaches it too.
	root = validManifestTree(t)
	if err := os.Remove(filepath.Join(root, "nests", "leo.yaml")); err != nil {
		t.Fatal(err)
	}
	if errs := Lint(root); len(errs) == 0 {
		t.Fatal("Lint accepted a manifest exporting a file that does not exist")
	}
}

// A stack present in the checkout but absent from exports may not be
// referenced by an exported nest: the catalogue is the contract, and a
// teammate resolves nothing else.
func TestLintRefusesAnUnexportedStackReference(t *testing.T) {
	root := validManifestTree(t)
	mkdirAll(t, filepath.Join(root, "stacks", "helper"))
	writeFile(t, filepath.Join(root, "stacks", "helper", "stack.yaml"), "image: helper:v1\nbase: claude\n")
	writeFile(t, filepath.Join(root, "nests", "leo.yaml"), nestYAML("helper"))

	errs := Lint(root)
	if len(errs) == 0 {
		t.Fatal("Lint accepted an exported nest resolving an unexported stack")
	}
	var joined []string
	for _, e := range errs {
		joined = append(joined, e.Error())
	}
	if !strings.Contains(strings.Join(joined, " | "), "helper") {
		t.Errorf("errors = %v, expected the unexported stack to be named", errs)
	}
}

// A source without den-source.yaml keeps the current behavior, unchanged:
// legacy sources predate this contract and must keep installing and updating
// (spec §9.3).
func TestLintKeepsLegacySourcesOnTheDirectoryScan(t *testing.T) {
	root := t.TempDir()
	mkdirAll(t, filepath.Join(root, "stacks", "devx"))
	mkdirAll(t, filepath.Join(root, "nests"))
	writeFile(t, filepath.Join(root, "stacks", "devx", "stack.yaml"), "image: devx:v1\nbase: claude\n")
	writeFile(t, filepath.Join(root, "nests", "api.yaml"), "stack: devx\nrepos:\n  - { key: api }\n")

	if errs := Lint(root); len(errs) != 0 {
		t.Fatalf("a legacy source must keep linting through the directory scan: %v", errs)
	}
}
