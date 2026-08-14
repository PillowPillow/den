package source

import "strings"

// NormalizeRemoteURL reduces a git URL to "host/owner/name", the identity two
// spellings of one repository share.
//
// Every form a team really uses must converge:
//
//	git@gitlab.example.com:team/api.git
//	ssh://git@gitlab.example.com/team/api.git
//	https://gitlab.example.com/team/api.git
//
// The owner path is KEPT: two repositories called "api" under different
// groups are different repositories, and dropping the group would map one onto
// the other. Credentials embedded in an https URL are dropped — they are not
// part of the identity, and they must never reach a plan, a log or a receipt.
//
// It lives HERE, in the package that owns sources, because two questions need
// one answer: "is this checkout the repository a nest declares" (discovery)
// and "is this url the source already installed under that name" (namespace
// arbitration). Two implementations of "the same repository" is a divergence
// nobody would notice until den refused to reinstall a source it had just
// cloned.
func NormalizeRemoteURL(url string) string {
	s := strings.TrimSpace(url)
	if s == "" {
		return ""
	}
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	} else if at := strings.Index(s, "@"); at >= 0 && strings.Contains(s[at:], ":") {
		// scp-like syntax: user@host:path — the colon is a separator, not a
		// port, and only this form uses it that way.
		s = s[at+1:]
		s = strings.Replace(s, ":", "/", 1)
	}
	// Credentials of an https URL (user:token@host), dropped after the scheme
	// so the scp-like branch above cannot be confused by them.
	if at := strings.Index(s, "@"); at >= 0 {
		s = s[at+1:]
	}
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	host, rest, ok := strings.Cut(s, "/")
	if !ok {
		return strings.ToLower(s)
	}
	// The host is case-insensitive; the path is not — git forges distinguish
	// "Team/API" from "team/api" on Linux, and normalizing it would merge two
	// real repositories.
	return strings.ToLower(host) + "/" + rest
}
