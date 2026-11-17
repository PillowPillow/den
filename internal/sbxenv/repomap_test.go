package sbxenv

import (
	"strings"
	"testing"
)

func sampleRepoMap() RepoMap {
	return RepoMap{
		Schema:     1,
		Instance:   "oauth",
		Nest:       "oauth",
		ConfigFile: "/p/dev-home/den.yaml",
		Template:   "digitaleo",
		Start:      "/p/dev-home",
		Mounted: []MappedRepo{
			{Name: "oauth.messengeo.net", Key: "oauth.messengeo.net", Path: "/p/oauth.messengeo.net"},
			{Name: "libeo", Key: "libeo", Path: "/p/libeo", Optional: true, Branch: "feature/x"},
			{Name: "public.mcp", Key: "js.public-mcp", Path: "/p/public.mcp"},
		},
		NestNotMounted: []MappedRepo{{Name: "js.box", Key: "js.box", Path: "/p/js.box", Optional: true}},
		Machine:        []MappedRepo{{Name: "php.flow", Path: "/p/php.flow"}},
	}
}

func TestRenderRepoMapMarkdownMatchesGolden(t *testing.T) {
	golden(t, "repomap.md.golden", RenderRepoMapMarkdown(sampleRepoMap()))
}

func TestRenderRepoMapJSONMatchesGolden(t *testing.T) {
	got, err := RenderRepoMapJSON(sampleRepoMap())
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "repomap.json.golden", got)
}

func TestRenderRepoMapEmptyListsRenderNone(t *testing.T) {
	m := RepoMap{Schema: 1, Instance: "x", Nest: "x", ConfigFile: "/p/den.yaml", Template: "t", Start: "/p"}
	md := string(RenderRepoMapMarkdown(m))
	for _, heading := range []string{"## Mounted repos", "## Repos of the nest not mounted", "## Other repos of this machine"} {
		if !strings.Contains(md, heading+"\n\n- (none)\n") {
			t.Errorf("no `- (none)` under %q:\n%s", heading, md)
		}
	}
	js, err := RenderRepoMapJSON(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"mounted": []`, `"nest_not_mounted": []`, `"machine": []`} {
		if !strings.Contains(string(js), want) {
			t.Errorf("JSON lacks %s:\n%s", want, js)
		}
	}
}

func TestRenderRepoMapMarkdownIsAgentNeutral(t *testing.T) {
	md := string(RenderRepoMapMarkdown(sampleRepoMap()))
	for _, banned := range []string{"Claude", "claude", "repos.local.json", "dev home"} {
		if strings.Contains(md, banned) {
			t.Errorf("Markdown names %q:\n%s", banned, md)
		}
	}
}

func TestRenderRepoMapRemedyKeepsTheWorktreeBranch(t *testing.T) {
	m := sampleRepoMap()
	m.Instance, m.WorktreeBranch = "oauth.feature-x", "feature/x"
	if md := string(RenderRepoMapMarkdown(m)); !strings.Contains(md, "`den rm oauth.feature-x`, then `den up oauth -w feature/x --repo <host path>`") {
		t.Errorf("remedy loses the worktree branch:\n%s", md)
	}
}
