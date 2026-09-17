package sbx

import (
	"context"
	"strings"

	"golang.org/x/mod/semver"
)

// MinVersion is the oldest sbx den supports.
//
// Judged by `den doctor` ONLY (spec 2026-09-16-sbx-0.43-compat §5, HITL
// decision of 2026-09-16): sandbox-name rules, the tcp4 default of `sbx ports
// --publish`, `secret ls --json` and `last_used_at` all arrived between
// v0.42.0 and v0.43.0, and den assumes every one of them. No other command
// checks — `den rm` in particular must never refuse and strand a live VM over
// a version number (doctrine T13/T16), and a spawn on an older sbx fails with
// sbx's own message, which names the real cause.
const MinVersion = "v0.43.0"

// ParseVersion reads the version out of `sbx version`, whose output is one
// line — "sbx version: v0.43.0 <commit>" — unchanged from v0.38.0 (observed
// 2026-08-14) to v0.43.0 (re-measured 2026-09-16).
//
// It returns "" rather than an error when it cannot find one: an unreadable
// version is the CALLER's question — source.CheckCompatibility turns it into
// an UnknownVersionError, `den doctor` into a warning — and a version den
// cannot read must never be turned into a number it then compares.
func ParseVersion(output string) string {
	for _, line := range strings.Split(output, "\n") {
		_, rest, ok := strings.Cut(line, "sbx version:")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return ""
		}
		return fields[0]
	}
	return ""
}

// Version runs the PLAIN `sbx version` and parses it.
//
// Plain, not `--json`: the JSON form reports the daemon's state and asks it,
// while the plain one answers from embedded data without a daemon start
// (v0.43.0 release note, "returns embedded version information without full
// CLI startup"). A diagnostic must not start a daemon to read a number.
func Version(ctx context.Context, r Runner) (string, error) {
	out, err := r.Run(ctx, "version")
	if err != nil {
		return "", err
	}
	return ParseVersion(string(out)), nil
}

// ReleaseVersion normalizes an observed `sbx version` string to the release
// it was built from, so it compares against MinVersion on the same shape.
//
// `go build` of sbx from source stamps a semver-VALID prerelease such as
// "v0.43.0-dev" or "v0.43.0-3-gabc1234", and semver.Compare ranks a
// prerelease below its release — comparing that string as-is would FAIL a
// machine whose sbx is at or past the floor, on nothing but the fact that it
// carries local commits. ReleaseVersion cuts the prerelease so the compare
// judges the release instead. Build metadata is cut FIRST, not the
// prerelease: the SemVer grammar allows a "-" inside build metadata but
// never before it, so cutting the prerelease first would truncate metadata
// that happens to contain one (e.g. "v1.7.0-3-gabc+meta-x").
//
// ok is false when the input is not semver at all, prefixed or not (a bare
// "dev" from an even older build) — that case is the caller's WARN to raise,
// not ReleaseVersion's to normalize.
func ReleaseVersion(observed string) (string, bool) {
	v := strings.TrimSpace(observed)
	if v == "" {
		return "", false
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	if !semver.IsValid(v) {
		return "", false
	}
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v = v[:i]
	}
	return semver.Canonical(v), true
}
