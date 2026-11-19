package origin

import (
	"errors"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	errNotRemote = errors.New("not a git remote URL (expected scheme://host/path or [user@]host:path)")
	errShell     = errors.New("a remote URL starts with a letter or a digit and holds no shell character " +
		"($, `, ;, |, quotes, parentheses…): den prints it in a `git clone` line meant to be pasted")
)

// Identity reduces a git remote URL to a lowercase host/path key.
// The error never echoes the input: it may carry a token.
func Identity(raw string) (string, error) {
	host, path, ok := split(raw)
	if !ok {
		return "", errNotRemote
	}
	path = strings.ToLower(strings.Trim(path, "/"))
	path = strings.TrimSuffix(path, ".git")
	path = strings.Trim(path, "/")
	host = strings.ToLower(host)
	id := host + "/" + path
	// An scp split at the first ':' can leave user:token@ inside the identity.
	if host == "" || path == "" || strings.ContainsAny(id, "@:") {
		return "", errNotRemote
	}
	// Checked on the output too: url.Parse decodes %0d into a CR and %E2%80%AE into a bidi override.
	// den scan prints identities on the screen the user confirms, and a VM-written remote must not be
	// able to redraw it.
	if !printable(raw) || !printable(id) {
		return "", errNotRemote
	}
	// The raw URL comes from a den.yaml a VM may have rewritten, and den up and den scan print it
	// as `git clone <origin>`: `$(…)@host:path` is a valid scp form and would run on paste.
	if !shellSafe(raw) || !shellSafe(id) {
		return "", errShell
	}
	return id, nil
}

// printable refuses invalid UTF-8 too: IsPrint answers true for the RuneError it decodes to.
func printable(s string) bool {
	return utf8.ValidString(s) &&
		!strings.ContainsFunc(s, func(r rune) bool { return !unicode.IsPrint(r) || unicode.IsSpace(r) })
}

// shellSafe holds for ASCII letters and digits, the URL punctuation below, and any printable
// non-ASCII rune; the first rune is a letter or a digit, so no URL reads as a flag or a tilde.
func shellSafe(s string) bool {
	for i, r := range s {
		switch {
		case r >= utf8.RuneSelf, 'a' <= r && r <= 'z', 'A' <= r && r <= 'Z', '0' <= r && r <= '9':
		case i > 0 && strings.ContainsRune("-._~:/@%+=,", r):
		default:
			return false
		}
	}
	return s != ""
}

func split(raw string) (host, path string, ok bool) {
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", "", false
		}
		switch strings.ToLower(u.Scheme) {
		case "ssh", "git", "http", "https":
			return u.Hostname(), u.Path, true
		}
		return "", "", false
	}
	if strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, ".") {
		return "", "", false
	}
	left, path, found := strings.Cut(raw, ":")
	if !found {
		return "", "", false
	}
	if i := strings.LastIndex(left, "@"); i >= 0 {
		left = left[i+1:]
	}
	if strings.ContainsAny(left, `/\`) {
		return "", "", false
	}
	return left, path, true
}
