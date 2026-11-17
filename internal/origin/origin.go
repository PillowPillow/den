package origin

import (
	"errors"
	"net/url"
	"strings"
	"unicode"
)

var errNotRemote = errors.New("not a git remote URL (expected scheme://host/path or [user@]host:path)")

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
	// Checked on the output too: url.Parse decodes %0d into a CR. den scan prints identities on the
	// screen the user confirms, and a VM-written remote must not be able to redraw it.
	if !printable(raw) || !printable(id) {
		return "", errNotRemote
	}
	return id, nil
}

func printable(s string) bool {
	return !strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) })
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
