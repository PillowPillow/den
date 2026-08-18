package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
)

// tarGz builds an archive in memory: the suite never reads a fixture binary
// from disk, and never shells out to tar.
func tarGz(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for name, body := range entries {
		hdr := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestTagFromURL(t *testing.T) {
	tag, err := TagFromURL("https://github.com/PillowPillow/den/releases/tag/v1.8.1")
	if err != nil || tag != "v1.8.1" {
		t.Fatalf("TagFromURL = %q, %v; want v1.8.1, nil", tag, err)
	}
	if _, err := TagFromURL("https://github.com/PillowPillow/den/releases"); err == nil {
		t.Fatal("a URL with no v-prefixed tag must be refused")
	}
}

func TestArchiveNameAndURL(t *testing.T) {
	// Mirrors goreleaser's archives.name_template, which drops the leading v.
	if got := ArchiveName("v1.8.1", "darwin", "arm64"); got != "den_1.8.1_darwin_arm64.tar.gz" {
		t.Fatalf("ArchiveName = %q", got)
	}
	want := "https://github.com/PillowPillow/den/releases/download/v1.8.1/checksums.txt"
	if got := DownloadURL("v1.8.1", "checksums.txt"); got != want {
		t.Fatalf("DownloadURL = %q, want %q", got, want)
	}
}

func TestExpectedSum(t *testing.T) {
	// The real checksums.txt format: "<sha256>  <filename>".
	checksums := []byte("aaa  den_1.8.1_linux_amd64.tar.gz\nbbb  den_1.8.1_darwin_arm64.tar.gz\n")
	sum, err := ExpectedSum(checksums, "den_1.8.1_darwin_arm64.tar.gz")
	if err != nil || sum != "bbb" {
		t.Fatalf("ExpectedSum = %q, %v", sum, err)
	}
	if _, err := ExpectedSum(checksums, "den_1.8.1_windows_amd64.tar.gz"); err == nil {
		t.Fatal("a missing entry must be refused, not defaulted")
	}
}

func TestVerifySum(t *testing.T) {
	payload := []byte("hello")
	// sha256("hello")
	const good = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if err := VerifySum(payload, good); err != nil {
		t.Fatalf("VerifySum on a matching digest: %v", err)
	}
	err := VerifySum(payload, "0000000000000000000000000000000000000000000000000000000000000000")
	if err == nil {
		t.Fatal("a mismatching digest must be refused")
	}
	if !strings.Contains(err.Error(), "corrupted or incomplete") {
		t.Fatalf("the mismatch message must claim integrity only, got %q", err)
	}
}

func TestExtractBinary(t *testing.T) {
	body, err := ExtractBinary(tarGz(t, map[string]string{"den": "ELF", "LICENSE": "x"}))
	if err != nil || string(body) != "ELF" {
		t.Fatalf("ExtractBinary = %q, %v", body, err)
	}
	if _, err := ExtractBinary(tarGz(t, map[string]string{"LICENSE": "x"})); err == nil {
		t.Fatal("an archive without a den entry must be refused")
	}
	if _, err := ExtractBinary([]byte("not a gzip stream")); err == nil {
		t.Fatal("a truncated archive must be refused")
	}
}

func TestNeedsUpdate(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v1.8.0", "v1.8.1", true},
		{"v1.8.1", "v1.8.1", false},
		{"v1.9.0", "v1.8.1", false},
	}
	for _, c := range cases {
		if got := NeedsUpdate(c.current, c.latest); got != c.want {
			t.Errorf("NeedsUpdate(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}
