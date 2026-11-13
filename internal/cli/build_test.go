package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildCommandBuildsABase(t *testing.T) {
	f := newUpFixture(t)
	writeTestFile(t, filepath.Join(f.proj, "step.sh"), "echo hi\n")
	writeTestFile(t, filepath.Join(f.proj, "den.yaml"),
		"schema: 1\nbases:\n  devx:\n    image: \"devx:v1\"\n    build:\n      steps: [step.sh]\nnests: {}\n")
	if err := f.up2("build", "devx"); err != nil {
		t.Fatal(err)
	}
	if !f.fake.HasCalled("template", "save", "devx-build", "devx:v1") {
		t.Errorf("calls %v", f.fake.Calls)
	}
}

func TestBuildCommandRefusesAnUnknownBase(t *testing.T) {
	f := newUpFixture(t)
	f.project(t, [2]string{"am", "repos: [{path: /x}]"})
	err := f.up2("build", "nope")
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err = %v", err)
	}
	if len(f.fake.Calls) != 0 {
		t.Errorf("calls %v", f.fake.Calls)
	}
}
