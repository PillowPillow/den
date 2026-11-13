package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/registry"
)

func (f *upFixture) ls() error { return f.up2("ls") }

func TestLsGolden(t *testing.T) {
	f := newUpFixture(t)
	f.record(t, registry.Record{Instance: "zeta", Nest: "dg", Project: "/p/dg"})
	f.record(t, registry.Record{Instance: "am.feat", Nest: "am", Project: "/p/am",
		Worktree: &registry.Worktree{Branch: "feat/1"}})
	f.record(t, registry.Record{Instance: "glob", Nest: "tools", Worktree: &registry.Worktree{Branch: "main"}})
	f.fake.Responses["ls --json"] = respJSON(`[{"name":"zeta","status":"running"},{"name":"am.feat","status":"stopped"},{"name":"glob","status":"running"}]`)
	if err := f.ls(); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/ls.golden")
	if err != nil {
		t.Fatal(err)
	}
	if f.out.String() != string(want) {
		t.Errorf("got:\n%s\nwant:\n%s", f.out.String(), want)
	}
}

func TestLsMarksAMissingVM(t *testing.T) {
	f := newUpFixture(t)
	f.record(t, registry.Record{Instance: "am", Nest: "am", Project: "/p"})
	if err := f.ls(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.out.String(), "missing") {
		t.Errorf("out %q lacks missing", f.out.String())
	}
}

func TestLsWarnsOnABrokenRecord(t *testing.T) {
	f := newUpFixture(t)
	f.record(t, registry.Record{Instance: "am", Nest: "am"})
	path, _ := registry.Path(f.home, "bad")
	writeTestFile(t, path, "schema: 99\n")
	if err := f.ls(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.err.String(), "warning: unreadable record "+path+": ") {
		t.Errorf("stderr %q", f.err.String())
	}
	if !strings.Contains(f.out.String(), "am") {
		t.Errorf("stdout %q lacks the readable record", f.out.String())
	}
}
