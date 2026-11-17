package repos

import (
	"reflect"
	"testing"
)

func validPaths(paths ...string) func(string, string) bool {
	set := map[string]bool{}
	for _, p := range paths {
		set[p] = true
	}
	return func(path, _ string) bool { return set[path] }
}

func table(entries map[string]string) Table {
	t := Table{Schema: TableSchema, Repos: map[string]Entry{}}
	for id, p := range entries {
		t.Repos[id] = Entry{Path: p}
	}
	return t
}

func clone(path string, remotes ...Remote) Clone { return Clone{Path: path, Remotes: remotes} }

func originRemote(id string) Remote   { return Remote{Name: "origin", Identity: id} }
func upstreamRemote(id string) Remote { return Remote{Name: "upstream", Identity: id} }

func TestPlanAddsNewIdentities(t *testing.T) {
	clones := []Clone{
		clone("/w/b", originRemote("h/b")),
		clone("/w/a", originRemote("h/a"), upstreamRemote("h/c")),
		clone("/w/bare"),
	}
	p := BuildPlan(clones, table(nil), validPaths())
	want := []Change{
		{Identity: "h/a", Path: "/w/a"},
		{Identity: "h/b", Path: "/w/b"},
		{Identity: "h/c", Path: "/w/a"},
	}
	if !reflect.DeepEqual(p.Adds, want) {
		t.Fatalf("Adds = %+v, want %+v", p.Adds, want)
	}
	if len(p.Replaces)+len(p.Duplicates)+len(p.Stale) != 0 {
		t.Fatalf("unexpected plan %+v", p)
	}
}

func TestPlanKeepsAValidEntry(t *testing.T) {
	p := BuildPlan([]Clone{clone("/w/a", originRemote("h/a"))}, table(map[string]string{"h/a": "/w/a"}), validPaths("/w/a"))
	if len(p.Adds)+len(p.Replaces)+len(p.Duplicates)+len(p.Stale) != 0 {
		t.Fatalf("a rescan of a valid entry must plan nothing, got %+v", p)
	}
}

func TestPlanReportsASecondCloneOfAValidEntry(t *testing.T) {
	p := BuildPlan([]Clone{clone("/w/new", originRemote("h/a"))}, table(map[string]string{"h/a": "/w/old"}), validPaths("/w/old"))
	want := []Duplicate{{Identity: "h/a", Chosen: "/w/old", Others: []string{"/w/new"}}}
	if !reflect.DeepEqual(p.Duplicates, want) {
		t.Fatalf("Duplicates = %+v, want %+v", p.Duplicates, want)
	}
	if len(p.Adds)+len(p.Replaces)+len(p.Stale) != 0 {
		t.Fatalf("a valid entry must not change, got %+v", p)
	}
}

func TestPlanReplacesAnInvalidEntry(t *testing.T) {
	p := BuildPlan([]Clone{clone("/w/new", originRemote("h/a"))}, table(map[string]string{"h/a": "/w/gone"}), validPaths())
	want := []Change{{Identity: "h/a", Path: "/w/new", Old: "/w/gone"}}
	if !reflect.DeepEqual(p.Replaces, want) {
		t.Fatalf("Replaces = %+v, want %+v", p.Replaces, want)
	}
	if len(p.Adds)+len(p.Stale) != 0 {
		t.Fatalf("unexpected plan %+v", p)
	}
}

func TestPlanReportsStale(t *testing.T) {
	tbl := table(map[string]string{"h/z": "/w/z", "h/y": "/w/y", "h/ok": "/elsewhere/ok"})
	p := BuildPlan([]Clone{clone("/w/a", originRemote("h/a"))}, tbl, validPaths("/elsewhere/ok"))
	if want := []string{"h/y", "h/z"}; !reflect.DeepEqual(p.Stale, want) {
		t.Fatalf("Stale = %v, want %v", p.Stale, want)
	}
}

func TestPlanPrefersTheOriginRemote(t *testing.T) {
	clones := []Clone{
		clone("/a", originRemote("me/api"), upstreamRemote("dig/api")),
		clone("/z", originRemote("dig/api")),
	}
	p := BuildPlan(clones, table(nil), validPaths())
	var got Change
	for _, c := range p.Adds {
		if c.Identity == "dig/api" {
			got = c
		}
	}
	if got.Path != "/z" {
		t.Fatalf("dig/api went to %q, want /z", got.Path)
	}
	want := []Duplicate{{Identity: "dig/api", Chosen: "/z", Others: []string{"/a"}}}
	if !reflect.DeepEqual(p.Duplicates, want) {
		t.Fatalf("Duplicates = %+v, want %+v", p.Duplicates, want)
	}
}

func TestPlanBreaksTiesByPath(t *testing.T) {
	clones := []Clone{
		clone("/w/c", originRemote("h/a")),
		clone("/w/a", originRemote("h/a")),
		clone("/w/b", originRemote("h/a")),
	}
	p := BuildPlan(clones, table(nil), validPaths())
	if len(p.Adds) != 1 || p.Adds[0].Path != "/w/a" {
		t.Fatalf("Adds = %+v, want /w/a", p.Adds)
	}
	want := []Duplicate{{Identity: "h/a", Chosen: "/w/a", Others: []string{"/w/b", "/w/c"}}}
	if !reflect.DeepEqual(p.Duplicates, want) {
		t.Fatalf("Duplicates = %+v, want %+v", p.Duplicates, want)
	}
}

func TestPlanReportsADuplicateOfAValidEntry(t *testing.T) {
	clones := []Clone{
		clone("/w/a", originRemote("h/a")),
		clone("/w/b", originRemote("h/a")),
	}
	p := BuildPlan(clones, table(map[string]string{"h/a": "/w/b"}), validPaths("/w/b"))
	want := []Duplicate{{Identity: "h/a", Chosen: "/w/b", Others: []string{"/w/a"}}}
	if !reflect.DeepEqual(p.Duplicates, want) {
		t.Fatalf("Duplicates = %+v, want %+v", p.Duplicates, want)
	}
	if len(p.Adds)+len(p.Replaces) != 0 {
		t.Fatalf("a valid entry must not change, got %+v", p)
	}
}

func TestPlanCountsACloneOnceForTwoRemotesOfOneIdentity(t *testing.T) {
	p := BuildPlan([]Clone{clone("/w/a", originRemote("h/a"), upstreamRemote("h/a"))}, table(nil), validPaths())
	if len(p.Duplicates) != 0 || len(p.Adds) != 1 {
		t.Fatalf("got %+v", p)
	}
}

func TestApply(t *testing.T) {
	var tbl Table
	tbl.Apply(Change{Identity: "h/a", Path: "/w/a"})
	tbl.Apply(Change{Identity: "h/b", Path: "/w/b"}, Change{Identity: "h/a", Path: "/w/a2", Old: "/w/a"})
	want := map[string]Entry{"h/a": {Path: "/w/a2"}, "h/b": {Path: "/w/b"}}
	if !reflect.DeepEqual(tbl.Repos, want) {
		t.Fatalf("Repos = %+v, want %+v", tbl.Repos, want)
	}
}
