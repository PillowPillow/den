package instance

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/nest"
	"github.com/PillowPillow/den/internal/registry"
	"github.com/PillowPillow/den/internal/sbxenv"
)

// mapFixture declares repos a, b, c, d in the global repos: and a project nest am using a (required),
// b and c (optional); d belongs to the machine only.
func mapFixture(t *testing.T) (f *fixture, paths map[string]string) {
	t.Helper()
	f = newFixture(t)
	paths = map[string]string{}
	var b strings.Builder
	b.WriteString("schema: 1\nrepos:\n")
	for _, k := range []string{"a", "b", "c", "d"} {
		paths[k] = f.committedRepo(t, k)
		fmt.Fprintf(&b, "  %s: %s\n", k, paths[k])
	}
	writeFile(t, filepath.Join(f.home, "den.yaml"), b.String())
	f.project(t, map[string]string{"am": "repos: [{key: a}, {key: b, optional: true}, {key: c, optional: true}]"})
	return f, paths
}

func readRepoMap(t *testing.T, rec registry.Record) sbxenv.RepoMap {
	t.Helper()
	var m sbxenv.RepoMap
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(rec.GenDir, sbxenv.AgentName, sbxenv.RepoMapJSONFile))), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func mappedNames(rs []sbxenv.MappedRepo) []string {
	out := []string{}
	for _, r := range rs {
		out = append(out, r.Name)
	}
	return out
}

func TestCreateWritesTheRepoMap(t *testing.T) {
	f, paths := mapFixture(t)
	rec := mustCreate(t, f, mustPrepare(t, f, Options{Nest: "am", Selection: nest.Selection{Without: []string{"c"}}}))
	m := readRepoMap(t, rec)
	if got := [][]string{mappedNames(m.Mounted), mappedNames(m.NestNotMounted), mappedNames(m.Machine)}; !reflect.DeepEqual(got, [][]string{{"a", "b"}, {"c"}, {"d"}}) {
		t.Fatalf("mounted, nest not mounted, machine = %v", got)
	}
	if m.Mounted[1] != (sbxenv.MappedRepo{Name: "b", Key: "b", Path: paths["b"], Optional: true}) {
		t.Errorf("mounted b %+v", m.Mounted[1])
	}
	if m.Machine[0] != (sbxenv.MappedRepo{Name: "d", Path: paths["d"]}) {
		t.Errorf("machine d %+v", m.Machine[0])
	}
	if m.Schema != 1 || m.Instance != "am" || m.Start != rec.Start || m.ConfigFile != rec.ConfigFile {
		t.Errorf("header %+v, record %+v", m, rec)
	}
	rules := readFile(t, filepath.Join(rec.GenDir, sbxenv.AgentName, sbxenv.RepoMapRulesFile))
	if !strings.Contains(rules, "`den rm am`") {
		t.Errorf("rules file lacks the remedy:\n%s", rules)
	}
}

func TestCreateRepoMapShowsTheWorktreeBranch(t *testing.T) {
	f, _ := mapFixture(t)
	rec := mustCreate(t, f, mustPrepare(t, f, Options{Nest: "am", Branch: "feature/x"}))
	for _, r := range readRepoMap(t, rec).Mounted {
		if r.Branch != "feature/x" {
			t.Errorf("mounted %+v, want branch feature/x", r)
		}
	}
}

func TestCreateRepoMapListsAdHocRepoOnce(t *testing.T) {
	f, paths := mapFixture(t)
	rec := mustCreate(t, f, mustPrepare(t, f, Options{Nest: "am", Selection: nest.Selection{Repos: []string{paths["d"]}}}))
	m := readRepoMap(t, rec)
	last := m.Mounted[len(m.Mounted)-1]
	if last != (sbxenv.MappedRepo{Name: "d", Path: paths["d"]}) {
		t.Errorf("ad hoc repo %+v, want d with no key", last)
	}
	if len(m.Machine) != 0 {
		t.Errorf("machine %+v, want d listed once, under mounted", m.Machine)
	}
}

func TestRecreateRewritesTheRepoMap(t *testing.T) {
	f, _ := mapFixture(t)
	mustCreate(t, f, mustPrepare(t, f, Options{Nest: "am", Selection: nest.Selection{Without: []string{"c"}}}))
	p := mustPrepare(t, f, Options{Nest: "am"})
	if p.State != Recorded {
		t.Fatalf("state %v, want Recorded", p.State)
	}
	m := readRepoMap(t, mustCreate(t, f, p))
	if len(m.NestNotMounted) != 0 || !reflect.DeepEqual(mappedNames(m.Mounted), []string{"a", "b", "c"}) {
		t.Errorf("recreated map %+v", m)
	}
}

func TestEnvOfExportsDenReposWithoutMutatingResolved(t *testing.T) {
	f, _ := mapFixture(t)
	p := mustPrepare(t, f, Options{Nest: "am"})
	if got := envOf(p, nil).Env[sbxenv.RepoMapEnv]; got != sbxenv.RepoMapVMPath {
		t.Errorf("DEN_REPOS %q, want %q", got, sbxenv.RepoMapVMPath)
	}
	if _, ok := p.Resolved.Env[sbxenv.RepoMapEnv]; ok {
		t.Errorf("envOf wrote DEN_REPOS into Resolved.Env, which feeds configHash: %v", p.Resolved.Env)
	}
}
