package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"golang.org/x/mod/semver"
)

// releasesBase is the single place the download host is spelled. install.sh
// spells it too; the CI smoke of §8 is what keeps the two honest.
const releasesBase = "https://github.com/PillowPillow/den/releases"

// LatestURL is the redirect den reads the newest tag from. NOT api.github.com:
// 60 unauthenticated requests per hour, and a JSON dependency the target
// machines may not have — the same decision install.sh already took.
const LatestURL = releasesBase + "/latest"

// TagFromURL reads the tag out of the URL /releases/latest redirected to. It
// refuses anything that does not end in a v-prefixed segment rather than
// normalizing: den's tags carry the leading v, and a guessed tag would produce
// a valid-looking archive name under an invalid URL.
func TagFromURL(finalURL string) (string, error) {
	tag := finalURL[strings.LastIndex(finalURL, "/")+1:]
	if !strings.HasPrefix(tag, "v") || !semver.IsValid(tag) {
		return "", fmt.Errorf("could not read a release tag from %q — download an archive from %s instead",
			finalURL, releasesBase)
	}
	return tag, nil
}

// ArchiveName mirrors .goreleaser.yaml's archives.name_template, which uses the
// version WITHOUT the leading v. install.sh recomposes the same name; the CI
// smoke job is what proves both still match the published release.
func ArchiveName(tag, goos, goarch string) string {
	return fmt.Sprintf("den_%s_%s_%s.tar.gz", strings.TrimPrefix(tag, "v"), goos, goarch)
}

func DownloadURL(tag, file string) string {
	return fmt.Sprintf("%s/download/%s/%s", releasesBase, tag, file)
}

// ExpectedSum finds one archive's digest in checksums.txt ("<sha256>  <name>").
// A missing entry is a changed release layout, not a user mistake, and the
// message says so.
func ExpectedSum(checksums []byte, archive string) (string, error) {
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == archive {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("checksums.txt has no entry for %s — the release layout changed; "+
		"report this at https://github.com/PillowPillow/den/issues", archive)
}

// VerifySum proves INTEGRITY, never authenticity: checksums.txt travels the
// same unsigned TLS channel as the archive, so this catches a corrupted or
// truncated download and cannot catch a compromised release. The message claims
// only what the check proves — the same wording discipline as install.sh.
func VerifySum(archive []byte, expected string) error {
	sum := sha256.Sum256(archive)
	if got := hex.EncodeToString(sum[:]); got != expected {
		return fmt.Errorf("checksum mismatch: the download is corrupted or incomplete — "+
			"re-run `den update`, and report it if this persists (expected %s, got %s)", expected, got)
	}
	return nil
}

// ExtractBinary pulls the `den` entry out of the release archive. Everything
// else in the tarball (LICENSE, README) is ignored on purpose: den replaces one
// file, and unpacking more would write files nobody asked for next to the
// binary.
func ExtractBinary(targz []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(targz))
	if err != nil {
		return nil, fmt.Errorf("the downloaded archive is not readable (%v) — re-run `den update`", err)
	}
	defer func() { _ = zr.Close() }()

	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("the downloaded archive is not readable (%v) — re-run `den update`", err)
		}
		if hdr.Name != "den" {
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("the downloaded archive is truncated (%v) — re-run `den update`", err)
		}
		return body, nil
	}
	return nil, fmt.Errorf("the release archive carries no `den` entry — the release layout changed; " +
		"report this at https://github.com/PillowPillow/den/issues")
}

// NeedsUpdate compares two CANONICAL versions. The caller has already refused a
// non-release current version (IsUpdatableVersion), so both sides here are
// clean vX.Y.Z and semver.Compare answers the whole question — including the
// "local is newer" case, where den does nothing.
func NeedsUpdate(current, latest string) bool {
	return semver.Compare(current, latest) < 0
}
