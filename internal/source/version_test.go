package source

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installSource materializes an installed source under <denHome>/sources/<name>,
// with the manifest, its exported files, the personal configuration and the
// receipt a converged installation leaves behind.
func installSource(t *testing.T, denHome, name, version string) string {
	t.Helper()
	root := Dir(denHome, name)
	mkdirAll(t, root)
	tree := validManifestTree(t)
	if err := os.CopyFS(root, os.DirFS(tree)); err != nil {
		t.Fatalf("installing the source: %v", err)
	}
	rewriteManifest(t, root, strings.Replace(validManifestYAML, "version: 1.0.0", "version: "+version, 1))
	if err := WritePersonal(denHome, name, Personal{
		Version: version,
		Repos:   map[string]string{"api": "/dev/api"},
	}); err != nil {
		t.Fatalf("WritePersonal: %v", err)
	}
	if err := WriteReceipt(denHome, name, Receipt{
		Status:  StatusReady,
		Version: version,
		Commit:  "0123456789abcdef",
		Nests:   map[string]ReceiptNest{"leo": {Status: NestReady}},
	}); err != nil {
		t.Fatalf("WriteReceipt: %v", err)
	}
	return root
}

func TestRequireUsableReturnsTheSourceScopedMapping(t *testing.T) {
	home := t.TempDir()
	installSource(t, home, "dg", "1.0.0")

	active, err := RequireUsable(home, "dg")
	if err != nil {
		t.Fatalf("RequireUsable: %v", err)
	}
	if active.Manifest == nil || active.Manifest.Metadata.Version != "1.0.0" {
		t.Fatalf("manifest = %+v", active.Manifest)
	}
	if active.Repos["api"] != "/dev/api" {
		t.Errorf("repos = %#v, expected the source-scoped mapping", active.Repos)
	}
	if !active.Manifested() {
		t.Error("Manifested() = false on a source carrying den-source.yaml")
	}
}

// A source without den-source.yaml stays usable exactly as before: legacy
// sources predate this contract, and nothing about them may start refusing
// (spec §9.3). Its mapping is nil — the caller then uses config.yaml.repos.
func TestRequireUsableAcceptsALegacySource(t *testing.T) {
	home := t.TempDir()
	root := Dir(home, "corp")
	mkdirAll(t, filepath.Join(root, "nests"))
	writeFile(t, filepath.Join(root, "nests", "api.yaml"), "stack: devx\n")

	active, err := RequireUsable(home, "corp")
	if err != nil {
		t.Fatalf("RequireUsable on a legacy source: %v", err)
	}
	if active.Manifested() {
		t.Error("Manifested() = true without den-source.yaml")
	}
	if active.Repos != nil {
		t.Errorf("repos = %#v, expected nil so the caller keeps the global mapping", active.Repos)
	}
}

// The receipt's manifest digest is what a MANUAL `git pull` in sources/<name>/
// can move without touching metadata.version: den never sees the new commit
// (spec §11.3 covers only version disagreement), so RequireUsable must
// compare content, not just the version string, to catch it.
func TestRequireUsableAcceptsAMatchingManifestDigest(t *testing.T) {
	home := t.TempDir()
	root := installSource(t, home, "dg", "1.0.0")
	digest, err := ManifestDigest(root)
	if err != nil {
		t.Fatalf("ManifestDigest: %v", err)
	}
	if err := WriteReceipt(home, "dg", Receipt{
		Status:         StatusReady,
		Version:        "1.0.0",
		Commit:         "0123456789abcdef",
		ManifestDigest: digest,
		Nests:          map[string]ReceiptNest{"leo": {Status: NestReady}},
	}); err != nil {
		t.Fatalf("WriteReceipt: %v", err)
	}

	if _, err := RequireUsable(home, "dg"); err != nil {
		t.Fatalf("RequireUsable: %v", err)
	}
}

// A manual `git pull` that keeps metadata.version identical while changing
// the manifest's content (different egress hosts, different images) is
// exactly the drift the version-string checks above cannot see: refusing on
// digest mismatch is the only thing that stops a spawn from proceeding
// against resources den never applied.
func TestRequireUsableRefusesADriftedManifest(t *testing.T) {
	home := t.TempDir()
	installSource(t, home, "dg", "1.0.0")
	if err := WriteReceipt(home, "dg", Receipt{
		Status:         StatusReady,
		Version:        "1.0.0",
		Commit:         "0123456789abcdef",
		ManifestDigest: "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Nests:          map[string]ReceiptNest{"leo": {Status: NestReady}},
	}); err != nil {
		t.Fatalf("WriteReceipt: %v", err)
	}

	_, err := RequireUsable(home, "dg")
	if err == nil {
		t.Fatal("expected a refusal on a drifted manifest")
	}
	for _, want := range []string{"den-source.yaml", "changed", "den source configure dg"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, expected a mention of %q", err.Error(), want)
		}
	}
	// There IS no version disagreement here — both sides say 1.0.0 — so the
	// message must not borrow the version-mismatch wording above, which
	// would send the reader chasing a cause that does not exist.
	if strings.Contains(err.Error(), "while this machine is configured for") {
		t.Errorf("error = %q, wrongly claims a version disagreement", err.Error())
	}
}

// A receipt written by an older den carries no ManifestDigest at all. That is
// NOT a mismatch — it is the absence of a fact to compare — so the check must
// skip silently rather than refuse every installation on its first upgrade.
func TestRequireUsableAcceptsAnEmptyReceiptDigest(t *testing.T) {
	home := t.TempDir()
	root := installSource(t, home, "dg", "1.0.0")
	// installSource's receipt already carries no ManifestDigest. Change the
	// manifest's bytes (same version, same decoded content) so a real digest
	// mismatch exists underneath — proving the empty-digest receipt is
	// skipped, not accidentally matching.
	rewriteManifest(t, root, validManifestYAML+"# drifted after the receipt's den\n")

	if _, err := RequireUsable(home, "dg"); err != nil {
		t.Fatalf("RequireUsable: %v, expected an empty receipt digest to be skipped", err)
	}
}

func TestRequireUsableRefusesAnUninstalledSource(t *testing.T) {
	_, err := RequireUsable(t.TempDir(), "dg")
	if err == nil || !strings.Contains(err.Error(), "den source add") {
		t.Fatalf("RequireUsable = %v, expected the install remedy", err)
	}
}

// Divergence between the checkout, the configured version and the receipt is
// exactly the state a partial update leaves. A command that consumed the
// source there would mix a half-applied infrastructure with a newer catalogue
// (spec §11.3), so every one of them refuses — naming the pending version and
// the command that resumes it.
func TestRequireUsableRefusesVersionDivergence(t *testing.T) {
	cases := []struct {
		name    string
		break_  func(t *testing.T, home, root string)
		wantAll []string
	}{
		{
			name: "checkout ahead of the configured version",
			break_: func(t *testing.T, home, root string) {
				rewriteManifest(t, root, strings.Replace(validManifestYAML, "version: 1.0.0", "version: 1.1.0", 1))
			},
			wantAll: []string{"1.1.0", "1.0.0", "den source configure dg"},
		},
		{
			name: "applying receipt",
			break_: func(t *testing.T, home, root string) {
				if err := WriteReceipt(home, "dg", Receipt{
					Status:          StatusApplying,
					PreviousVersion: "1.0.0",
					TargetVersion:   "1.1.0",
					TargetCommit:    "fedcba",
				}); err != nil {
					t.Fatal(err)
				}
			},
			wantAll: []string{"1.1.0", "den source configure dg"},
		},
		{
			name: "receipt naming another version",
			break_: func(t *testing.T, home, root string) {
				if err := WriteReceipt(home, "dg", Receipt{Status: StatusReady, Version: "0.9.0"}); err != nil {
					t.Fatal(err)
				}
			},
			wantAll: []string{"0.9.0", "den source configure dg"},
		},
		{
			name: "no receipt at all",
			break_: func(t *testing.T, home, root string) {
				if err := os.Remove(ReceiptPath(home, "dg")); err != nil {
					t.Fatal(err)
				}
			},
			wantAll: []string{"den source configure dg"},
		},
		{
			name: "no personal configuration",
			break_: func(t *testing.T, home, root string) {
				if err := os.Remove(PersonalPath(home, "dg")); err != nil {
					t.Fatal(err)
				}
			},
			wantAll: []string{"den source configure dg"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			root := installSource(t, home, "dg", "1.0.0")
			c.break_(t, home, root)

			_, err := RequireUsable(home, "dg")
			if err == nil {
				t.Fatal("expected a refusal on a diverging source")
			}
			for _, want := range c.wantAll {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, expected a mention of %q", err.Error(), want)
				}
			}
		})
	}
}
