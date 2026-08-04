#!/bin/sh
# Installs den from the GitHub release archives — the no-brew path (Linux
# distros, WSL, macOS without Homebrew). The sha256 checksum is verified
# before anything is installed, and the script refuses to run without a
# checksum tool: an unverifiable binary is never installed silently, the same
# fail-closed stance as the rest of den.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/PillowPillow/den/main/install.sh | sh
#   curl -fsSL ... | DEN_VERSION=v1.0.1 sh      # pin a version
#   curl -fsSL ... | DEN_INSTALL_DIR=~/bin sh   # override the destination
# (the assignment binds to sh, not curl — each pipeline command gets its own
# environment, so a prefix on curl never reaches the script)
set -eu

REPO="PillowPillow/den"
INSTALL_DIR="${DEN_INSTALL_DIR:-$HOME/.local/bin}"

fail() {
    printf 'install.sh: %s\n' "$1" >&2
    exit 1
}

command -v curl >/dev/null 2>&1 || fail "curl is required — install it with your package manager and re-run"
command -v tar >/dev/null 2>&1 || fail "tar is required — install it with your package manager and re-run"

case "$(uname -s)" in
    Darwin) os=darwin ;;
    Linux) os=linux ;;
    *) fail "unsupported OS '$(uname -s)' — releases cover darwin and linux (amd64/arm64) only; see https://github.com/$REPO/releases" ;;
esac

case "$(uname -m)" in
    x86_64 | amd64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) fail "unsupported architecture '$(uname -m)' — releases cover amd64 and arm64 only; see https://github.com/$REPO/releases" ;;
esac

# Fail closed BEFORE any download: no checksum tool means no install, not an
# unverified one.
if command -v sha256sum >/dev/null 2>&1; then
    checksum_tool=sha256sum
elif command -v shasum >/dev/null 2>&1; then
    checksum_tool=shasum
else
    fail "neither sha256sum nor shasum is available — refusing to install an unverified binary; install coreutils and re-run"
fi

tag="${DEN_VERSION:-}"
if [ -z "$tag" ]; then
    # The /releases/latest redirect names the tag in its final URL. This
    # avoids api.github.com (60 unauthenticated requests/hour) and a jq
    # dependency the target machines may not have.
    final_url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest") \
        || fail "cannot resolve the latest release from github.com — check your network, or pin one with DEN_VERSION=v1.0.1"
    tag="${final_url##*/}"
    case "$tag" in
        v*) ;;
        *) fail "could not parse a release tag from '$final_url' — pin one with DEN_VERSION=v1.0.1" ;;
    esac
else
    # Refuse rather than normalize (spec §2): den's tags carry the leading v,
    # and `DEN_VERSION=1.0.1` would still spell a *valid* archive name under an
    # invalid download URL — the resulting 404 would blame the release for what
    # is a typo in the caller's environment.
    case "$tag" in
        v*) ;;
        *) fail "DEN_VERSION='$tag' must name a tag like v1.0.1 — the tags carry the leading v; see https://github.com/$REPO/releases" ;;
    esac
fi

# Archive names mirror .goreleaser.yaml's name_template, which uses the
# version WITHOUT the leading v. The install-script CI job smokes this
# contract against the real latest release.
archive="den_${tag#v}_${os}_${arch}.tar.gz"
base_url="https://github.com/$REPO/releases/download/$tag"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

printf 'Downloading den %s (%s/%s)...\n' "$tag" "$os" "$arch"
curl -fsSL -o "$tmp/$archive" "$base_url/$archive" \
    || fail "download failed for $base_url/$archive — does release $tag exist and ship $os/$arch?"
curl -fsSL -o "$tmp/checksums.txt" "$base_url/checksums.txt" \
    || fail "download failed for $base_url/checksums.txt"

grep " $archive\$" "$tmp/checksums.txt" > "$tmp/expected.txt" \
    || fail "checksums.txt of $tag has no entry for $archive — the release layout changed; report this at https://github.com/$REPO/issues"
case "$checksum_tool" in
    sha256sum) (cd "$tmp" && sha256sum -c expected.txt >/dev/null 2>&1) ;;
    shasum) (cd "$tmp" && shasum -a 256 -c expected.txt >/dev/null 2>&1) ;;
# checksums.txt travels the same unsigned TLS channel as the archive, so this
# catches a corrupted or truncated download and a bad mirror — it cannot catch
# a compromised release. The message claims only what the check proves.
esac || fail "checksum mismatch for $archive — the download is corrupted or was served by a bad mirror; re-run, and report it if this persists"

tar -xzf "$tmp/$archive" -C "$tmp" den \
    || fail "could not extract 'den' from $archive — the release layout changed; report this at https://github.com/$REPO/issues"

mkdir -p "$INSTALL_DIR" || fail "cannot create $INSTALL_DIR — pick another destination with DEN_INSTALL_DIR"
# `mkdir -p` succeeds on a directory that already exists but is not writable —
# /usr/local/bin without sudo is exactly that — so the guard above cannot cover
# the most common failure. Without this one, it surfaces as a bare install(1)
# error naming no remedy.
install -m 755 "$tmp/den" "$INSTALL_DIR/den" \
    || fail "cannot write $INSTALL_DIR/den — check the directory's permissions, or pick another destination with DEN_INSTALL_DIR"

printf 'Installed %s/den\n' "$INSTALL_DIR"
case ":$PATH:" in
    *":$INSTALL_DIR:"*) ;;
    *)
        printf 'warning: %s is not in your PATH — add this line to your shell rc (~/.bashrc, ~/.zshrc, ...):\n' "$INSTALL_DIR" >&2
        # SC2016 is exactly right about the mechanism and exactly wrong about
        # the intent: this line is copy-pasted into a shell rc, where $PATH
        # must still be the reader's PATH at *their* startup. Expanding it here
        # would freeze this run's PATH into their profile.
        # shellcheck disable=SC2016
        printf '  export PATH="%s:$PATH"\n' "$INSTALL_DIR" >&2
        ;;
esac

# Proof, not promise: the installed binary names the tag it was built from.
"$INSTALL_DIR/den" version
