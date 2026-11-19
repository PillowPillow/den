package repos

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

func cloneWith(t *testing.T, config string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRemotesReadsEveryRemote(t *testing.T) {
	dir := cloneWith(t, `[core]
	bare = false
[remote "origin"]
	url = git@gitlab.com:me/api.git
	fetch = +refs/heads/*:refs/remotes/origin/*
[remote "upstream"]
	url = https://gitlab.com/dig/api
`)
	got, err := Remotes(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []Remote{{"origin", "gitlab.com/me/api"}, {"upstream", "gitlab.com/dig/api"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestRemotesIgnoresInsteadOfAndInclude(t *testing.T) {
	dir := cloneWith(t, `[url "git@gitlab.com:"]
	insteadOf = gl:
[include]
	path = x
[remote "origin"]
	url = gl:dig/api
`)
	got, err := Remotes(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []Remote{{"origin", "gl/dig/api"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestRemotesSkipsUnrecognized(t *testing.T) {
	dir := cloneWith(t, "[remote \"origin\"]\n\turl = /local/path\n[remote \"file\"]\n\turl = file:///x/y\n")
	got, err := Remotes(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want none", got)
	}
}

func TestRemotesSyntax(t *testing.T) {
	dir := cloneWith(t, `# a comment
; another
[Remote "origin"]
	URL="https://gitlab.com/dig/api.git"
[remote "quoted"]
	url   =   "https://gitlab.com/dig/quoted" ; trailing
[remote "trailing"]
	url = https://gitlab.com/dig/trailing # trailing
[remote "multi"]
	url = https://gitlab.com/dig/one
	url = https://gitlab.com/dig/two
[remote "Case"]
	; url = https://gitlab.com/dig/commented
[remote "other"]
	pushurl = https://gitlab.com/dig/push
[branch "main"]
	url = https://gitlab.com/dig/notaremote
`)
	got, err := Remotes(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []Remote{
		{"origin", "gitlab.com/dig/api"},
		{"quoted", "gitlab.com/dig/quoted"},
		{"trailing", "gitlab.com/dig/trailing"},
		{"multi", "gitlab.com/dig/one"},
		{"multi", "gitlab.com/dig/two"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestRemotesUnreadable(t *testing.T) {
	if _, err := Remotes(t.TempDir()); err == nil {
		t.Fatal("want an error when .git/config is missing")
	}
}

func TestValid(t *testing.T) {
	dir := cloneWith(t, "[remote \"origin\"]\n\turl = git@gitlab.com:me/api.git\n[remote \"upstream\"]\n\turl = https://gitlab.com/dig/api\n")
	if !Valid(dir, "gitlab.com/dig/api") {
		t.Error("upstream remote should satisfy the identity")
	}
	if Valid(dir, "gitlab.com/dig/other") {
		t.Error("no remote carries this identity")
	}
	if Valid(filepath.Join(dir, "absent"), "gitlab.com/dig/api") {
		t.Error("absent folder is invalid")
	}
	if Valid(t.TempDir(), "gitlab.com/dig/api") {
		t.Error("folder without .git/config is invalid")
	}
}

func TestValidRefusesASymlinkedGitFolder(t *testing.T) {
	real := cloneWith(t, "[remote \"origin\"]\n\turl = https://gitlab.com/dig/api\n")
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(real, ".git"), filepath.Join(dir, ".git")); err != nil {
		t.Fatal(err)
	}
	if Valid(dir, "gitlab.com/dig/api") {
		t.Error("a .git link is not a clone")
	}
}

func TestRemotesSurvivesHostileConfig(t *testing.T) {
	dir := cloneWith(t, "[remote \"origin\"]\r\n\turl = git@gitlab.com:dig/api.git\r\n"+
		"[remote \"cr\"]\n\turl = git@host:dig/a\rpi\n"+
		"[remote \"esc\"]\n\turl = host:x/\x1b[1;1fover\n"+
		"[remote \"nul\"]\n\turl = https://host/a\x00b\n"+
		"[remote \"pct\"]\n\turl = https://host/a%0db\n"+
		"=\n"+
		"[remote \"open\"\n\turl = https://gitlab.com/dig/unterminated\n")
	got, err := Remotes(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Remote{{"origin", "gitlab.com/dig/api"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRemotesRefusesASymlinkedConfig(t *testing.T) {
	elsewhere := cloneWith(t, cfgOrigin)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(elsewhere, ".git", "config"), filepath.Join(dir, ".git", "config")); err != nil {
		t.Fatal(err)
	}
	if got, err := Remotes(dir); err == nil {
		t.Fatalf("got %v, want an error for a symlinked .git/config", got)
	}
	if Valid(dir, "gitlab.com/dig/api") {
		t.Error("a symlinked .git/config made the folder valid")
	}
}

func TestRemotesRefusesAFifoConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, ".git", "config"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := Remotes(dir); err == nil {
		t.Fatalf("got %v, want an error for a FIFO .git/config", got)
	}
	if Valid(dir, "gitlab.com/dig/api") {
		t.Error("a FIFO .git/config made the folder valid")
	}
}

func TestRemotesReadsAtMostOneMebibyte(t *testing.T) {
	pad := func(n int) string { return cfgOrigin + "#" + strings.Repeat("x", n-len(cfgOrigin)-2) + "\n" }
	if got, err := Remotes(cloneWith(t, pad(maxConfigSize))); err != nil || len(got) != 1 {
		t.Fatalf("a config of exactly %d bytes: got %v, %v", maxConfigSize, got, err)
	}
	dir := cloneWith(t, pad(maxConfigSize+1))
	if got, err := Remotes(dir); err == nil {
		t.Fatalf("got %v, want an error for a config over %d bytes", got, maxConfigSize)
	}
	if Valid(dir, "gitlab.com/dig/api") {
		t.Error("an oversize .git/config made the folder valid")
	}
}

const cfgOrigin = "[remote \"origin\"]\n\turl = https://gitlab.com/dig/api\n"
