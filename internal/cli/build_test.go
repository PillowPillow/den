package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/hub"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/template"
)

const testDigest = "sha256:4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a"

// devxTemplate declares devx in the project den.yaml, its location in the project folder.
func (f *upFixture) devxTemplate(t *testing.T, definition string) {
	t.Helper()
	f.fake.Responses["template ls --json"] = sbx.Response{Output: []byte(
		`[{"repository":"docker.io/docker/sandbox-templates","tag":"claude-code-docker","digest":"` + testDigest + `"}]`)}
	writeTestFile(t, filepath.Join(f.proj, "tpl", "step.sh"), "echo hi\n")
	writeTestFile(t, filepath.Join(f.proj, "tpl", "den-template.yaml"),
		"schema: 1\ntemplates:\n  devx:\n"+definition+"    build:\n      steps: [step.sh]\n")
	writeTestFile(t, filepath.Join(f.proj, "den.yaml"), "schema: 1\ntemplates:\n  devx:\n    dir: tpl\n")
}

func TestBuildCommandBuildsATemplate(t *testing.T) {
	f := newUpFixture(t)
	f.devxTemplate(t, "    from: claude-code-docker\n")
	if err := f.up2("build", "devx"); err != nil {
		t.Fatal(err)
	}
	if !f.fake.HasCalled("template", "save", "devx-build", "devx:v2") {
		t.Errorf("calls %v", f.fake.Calls)
	}
	if _, err := template.ReadRecord(f.home, "devx"); err != nil {
		t.Errorf("no record: %v", err)
	}
}

func TestBuildCommandRefusesAnImage(t *testing.T) {
	f := newUpFixture(t)
	err := f.up2("build", "nope")
	if err == nil || !strings.Contains(err.Error(), `"nope" is an image`) {
		t.Fatalf("err = %v", err)
	}
	for _, c := range f.fake.Calls {
		if strings.Join(c, " ") != "version" {
			t.Errorf("call %v", c)
		}
	}
}

func TestBuildCommandListsTheOfficialImagesWhenThereIsNothingToStartFrom(t *testing.T) {
	f := newUpFixture(t)
	f.hub = hub.Fake{Names: []string{"claude-code-docker", "shell"}}
	f.devxTemplate(t, "")
	err := f.up2("build", "devx")
	if err == nil || !strings.Contains(err.Error(), "\nofficial images: claude-code-docker, shell") {
		t.Fatalf("err = %v, want the official images on a line of their own", err)
	}
	if f.hasEnvCreate() {
		t.Error("env create after the refusal")
	}
}

func TestBuildCommandForceReplacesAnotherSource(t *testing.T) {
	f := newUpFixture(t)
	f.devxTemplate(t, "    from: claude-code-docker\n")
	if err := template.WriteRecord(f.home, template.Record{Template: "devx", Tag: "devx:v2",
		Source: template.Source{Kind: "dir", Dir: "/elsewhere"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.up2("build", "devx"); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v, want a refusal naming --force", err)
	}
	if err := f.up2("build", "devx", "--force"); err != nil {
		t.Fatal(err)
	}
	r, err := template.ReadRecord(f.home, "devx")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(f.proj, "tpl"); r.Source.Dir != want {
		t.Errorf("record source %+v, want dir %s", r.Source, want)
	}
}

func TestExplainNoTemplateLeavesOtherErrorsAlone(t *testing.T) {
	err := os.ErrNotExist
	if got := explainNoTemplate(t.Context(), Deps{Hub: hub.Fake{Names: []string{"shell"}}}, err); got != err {
		t.Errorf("got %v", got)
	}
}
