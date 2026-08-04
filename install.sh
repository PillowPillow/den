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
command -v mktemp >/dev/null 2>&1 || fail "mktemp is required — install it with your package manager and re-run"
# install(1) is a coreutils/busybox binary, never a shell builtin, so it can be
# missing from a stripped image exactly like curl or tar. Guarded here so the
# remedy path is the same sentence for every missing tool, instead of a bare
# "install: not found" surfacing 80 lines later after a download already ran.
command -v install >/dev/null 2>&1 || fail "install is required — install it with your package manager and re-run"

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
# The second command covers the staging file of the two-step install below: it
# exists only between `install` and `mv`, but a signal landing in that window —
# or an `mv` that fails after `install` succeeded — would otherwise leave a
# stray .den.new.<pid> sitting in the user's bin directory forever.
#
# `rm -f`, not `rm -rf`, and deliberately a separate command: $INSTALL_DIR comes
# from the caller's DEN_INSTALL_DIR, so this is the one path in the trap that is
# user-controlled. The staging path is always a regular file, `-r` would buy
# nothing, and keeping it off means no interpolation of a caller-supplied value
# can turn cleanup into a recursive delete. $tmp keeps -rf: it is a directory,
# and it comes from mktemp.
trap 'rm -rf "$tmp"; rm -f "$INSTALL_DIR/.den.new.$$"' EXIT INT TERM

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
# proves integrity, never authenticity: it catches a corrupted or truncated
# download, and cannot catch a compromised release. The message claims only
# what the check proves.
esac || fail "checksum mismatch for $archive — the download is corrupted or incomplete; re-run, and report it if this persists"

tar -xzf "$tmp/$archive" -C "$tmp" den \
    || fail "could not extract 'den' from $archive — the release layout changed; report this at https://github.com/$REPO/issues"

mkdir -p "$INSTALL_DIR" || fail "cannot create $INSTALL_DIR — pick another destination with DEN_INSTALL_DIR"
# Two steps, not one. `install` straight onto $INSTALL_DIR/den writes into the
# live file: a reinstall while `den` is running fails with ETXTBSY on Linux, and
# an interrupt mid-copy leaves a truncated binary on PATH. Installing beside it
# and renaming makes the swap a single atomic rename(2) — the reader either gets
# the old den or the new one, never half of either.
#
# The failure message names the destination and a remedy but not a cause:
# `mkdir -p` succeeds on a directory that already exists but is not writable
# (/usr/local/bin without sudo is exactly that), so permissions are the *likely*
# reason and not the only one — a full filesystem and a read-only mount land
# here too. Asserting "permission denied" would send a full-disk user to chmod.
#
# SC2015 warns that `A && B || C` is not if-then-else because C also runs when A
# succeeded and B failed. That is precisely the wiring wanted here: a failed
# `install` and a failed `mv` are both "den did not reach its destination", and
# both must reach the same refusal.
# shellcheck disable=SC2015
install -m 755 "$tmp/den" "$INSTALL_DIR/.den.new.$$" \
    && mv -f "$INSTALL_DIR/.den.new.$$" "$INSTALL_DIR/den" \
    || fail "cannot install to $INSTALL_DIR/den — check the directory's permissions, or pick another destination with DEN_INSTALL_DIR"

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
# The guard is the one exec in the script, so this is the only place a
# wrong-arch binary or a noexec mount can surface — without it the raw exec
# error would be the last line, with no remedy named.
"$INSTALL_DIR/den" version \
    || fail "installed, but $INSTALL_DIR/den does not run on this machine — a noexec mount or a binary for another architecture; pick another destination with DEN_INSTALL_DIR, or see https://github.com/$REPO/releases"
