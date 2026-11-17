// Package build builds a template: a throwaway VM, provisioned step by step, saved as an sbx
// template, then recorded so `den up` can tell which build it runs.
package build

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/instance"
	"github.com/PillowPillow/den/internal/prompt"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/sbxenv"
	"github.com/PillowPillow/den/internal/template"
)

// Deps adds to instance.Deps the one access only a build needs: whether a human can answer.
type Deps struct {
	instance.Deps
	IsTTY func() bool
}

// Options is what the command line asks of one build.
type Options struct {
	Pull  bool // re-clone a repo location instead of building its copy
	Yes   bool // trust a project-declared URL without asking
	Force bool // replace a record built from another source
}

// Build resolves value as `den up` would from d.Cwd, then builds that template. Every refusal it
// can decide from config, records and `sbx ls` comes before the first VM, and a failure leaves the
// previous copy, record and sbx template as they were.
func Build(ctx context.Context, d Deps, value string, o Options) error {
	if err := instance.CheckSbx(ctx, d.Sbx); err != nil {
		return err
	}
	global, err := config.LoadGlobal(d.DenHome)
	if err != nil {
		return err
	}
	project, err := loadProject(d.Cwd, d.DenHome)
	if err != nil {
		return err
	}
	c, err := template.Lookup(project, global, value)
	if err != nil {
		return err
	}
	if c.IsImage() {
		return fmt.Errorf("no template is named %q in %s, so it is the image %s, which has no build — "+
			"declare it under templates: there, or use the image directly as template: or default_template",
			value, strings.Join(c.Searched, " or "), c.Image)
	}
	return build(ctx, d, global, c, "", o)
}

func loadProject(cwd, denHome string) (*config.Loaded, error) {
	path, ok, err := config.FindProject(cwd, denHome)
	if err != nil || !ok {
		return nil, err
	}
	return config.Load(path, false)
}

// start is what the build VM starts from and what the record keeps of it.
type start struct {
	template   string        // sbx template tag; "" for an image
	kitImage   string        // the den-claude kit's image
	from       template.From // Digest stays empty until the VM is up when readDigest is set
	readDigest bool
}

// child names the template whose build asked for this one, "" when the user did.
func build(ctx context.Context, d Deps, global *config.Loaded, c template.Choice, child string, o Options) error {
	name, tag := c.Name(), c.Tag()
	if err := checkName(c, child); err != nil {
		return err
	}
	src := template.SourceOf(c)
	prev, err := template.ReadRecord(d.DenHome, name)
	hasPrev := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if hasPrev && prev.Source != src && child != "" {
		return fmt.Errorf("template %q was built from %s, but template %q starts from a sibling of that name in %s, "+
			"and a child's build never replaces another source's template — run `den build %s --force` "+
			"(declare %s under templates: with that source first if no den.yaml does)",
			name, template.DescribeSource(prev.Source), child, template.DescribeSource(src), name, name)
	}
	if hasPrev && prev.Source != src && !o.Force {
		return fmt.Errorf("template %q was built from %s, but templates.%s in %s now declares %s — "+
			"run `den build %s --force` to replace it", name, template.DescribeSource(prev.Source),
			c.Key, c.DeclaredIn.Path, template.DescribeSource(src), c.Value)
	}

	root, commit := c.Entry.Dir, ""
	staging := ""
	if src.Kind == "repo" {
		copyDir := template.CopyDir(d.DenHome, name)
		// The copy holds what the record says only when the record names this same source: after a
		// --force to another repo or ref, building it would record B over A's files.
		if _, statErr := os.Stat(copyDir); statErr == nil && !o.Pull && hasPrev && prev.Source == src {
			root = copyDir
			if commit, err = template.Head(ctx, d.Git, copyDir); err != nil {
				return err
			}
		} else {
			if err := confirmSource(ctx, d, c, o); err != nil {
				return err
			}
			staging = template.StagingDir(d.DenHome, name)
			// Removed on every way out: after a success it was renamed into the copy and is gone already.
			defer os.RemoveAll(staging)
			if err := os.RemoveAll(staging); err != nil {
				return fmt.Errorf("clearing %s: %w", staging, err)
			}
			if err := os.MkdirAll(filepath.Dir(staging), 0o755); err != nil {
				return fmt.Errorf("creating %s: %w", filepath.Dir(staging), err)
			}
			if commit, err = template.Clone(ctx, d.Git, src.URL, src.Ref, staging); err != nil {
				return err
			}
			root = staging
		}
	}

	defs, err := config.LoadDefinitions(root)
	if err != nil {
		return err
	}
	def, ok := defs.File.Templates[name]
	if !ok {
		return fmt.Errorf("%s declares no template %q, which templates.%s in %s names — add it there, or fix the entry",
			defs.Path, name, c.Key, c.DeclaredIn.Path)
	}
	// Hashed before the root moves, on the files this build reads.
	hash, err := template.DefinitionHash(defs, name)
	if err != nil {
		return err
	}
	prov, err := ReadProvisioning(def.Build.Includes, def.Build.Steps)
	if err != nil {
		return err
	}
	vm := name + "-build"
	boxes, err := sbx.Ls(ctx, d.Sbx)
	if err != nil {
		return err
	}
	// The deferred env rm below would remove a VM den did not create.
	if sbx.Find(boxes, vm) != nil {
		return fmt.Errorf("a sandbox named %s already exists, and den would remove it when the build ends — "+
			"run `sbx rm %s` if it is a leftover, then `den build %s` again", vm, vm, c.Value)
	}

	st, err := startOf(ctx, d, global, c, defs, def, commit, staging != "", o)
	if err != nil {
		return err
	}

	fmt.Fprintf(d.Err, "den: building template %s (%s)\n", name, tag)
	from, err := runVM(ctx, d, name, vm, tag, def, prov, st)
	if err != nil {
		return err
	}

	if staging != "" {
		copyDir := template.CopyDir(d.DenHome, name)
		if err := os.RemoveAll(copyDir); err != nil {
			return fmt.Errorf("replacing %s: %w", copyDir, err)
		}
		if err := os.Rename(staging, copyDir); err != nil {
			return fmt.Errorf("replacing %s: %w", copyDir, err)
		}
	}
	return template.WriteRecord(d.DenHome, template.Record{
		Template: name, Tag: tag, Source: src, Commit: commit, DefinitionHash: hash, From: from,
		BuiltAt: time.Now().UTC(),
	})
}

// checkName refuses, before a trust entry or a clone, a name sbx would refuse only once the build
// runs: the build VM <name>-build must be a sandbox name sbx creates, and the tag <name>:v2 an image
// reference, which is lowercase.
func checkName(c template.Choice, child string) error {
	name := c.Name()
	err := sbx.ValidateCreatableSandboxName(name + "-build")
	if err == nil && strings.ToLower(name) != name {
		err = fmt.Errorf("its sbx template %s must be lowercase", c.Tag())
	}
	if err == nil {
		return nil
	}
	where := fmt.Sprintf("templates.%s in %s", c.Key, c.DeclaredIn.Path)
	if child != "" {
		where = fmt.Sprintf("the from: of template %q", child)
	}
	return fmt.Errorf("template name %q, from %s, cannot be built: %v — rename the template", name, where, err)
}

// confirmSource asks before den clones a URL that only a project file declares: the definition
// carries the egress allowlist and scripts that run in the VM, so cloning someone's project must
// not be enough to trust it. The global file is the user's own and needs no confirmation.
func confirmSource(ctx context.Context, d Deps, c template.Choice, o Options) error {
	if c.DeclaredIn == nil || c.DeclaredIn.Global {
		return nil
	}
	url := c.Entry.Repo
	trusted, err := template.Trusted(d.DenHome, url)
	if err != nil || trusted {
		return err
	}
	if !o.Yes {
		if d.IsTTY == nil || !d.IsTTY() {
			return fmt.Errorf("templates.%s in %s fetches template definitions from %s, which den has not fetched on this machine — "+
				"run `den build %s --yes` to trust it", c.Key, c.DeclaredIn.Path, url, c.Value)
		}
		yes, err := d.Prompt.Confirm(ctx, prompt.ConfirmRequest{Question: fmt.Sprintf("fetch template definitions from %s?", url)})
		if err != nil {
			return err
		}
		if !yes {
			return fmt.Errorf("not fetching %s: template %q is not built", url, c.Name())
		}
	}
	return template.Trust(d.DenHome, url)
}

// startOf resolves the definition's from: a sibling (built first when it has no usable build or a
// stale one), an image, or, when from is empty, the global default_template if it names an image.
// commit is the checkout the child builds from, and fresh says it was cloned by this build.
func startOf(ctx context.Context, d Deps, global *config.Loaded, c template.Choice, defs *config.LoadedDefinitions, def config.Definition,
	commit string, fresh bool, o Options) (start, error) {
	name := c.Name()
	if defs.IsSibling(def.From) {
		sib := c
		sib.Entry.Name = def.From
		need, err := needsBuild(ctx, d, sib, defs, commit, fresh)
		if err != nil {
			return start{}, err
		}
		if need {
			fmt.Fprintf(d.Err, "den: building template %s first (template %s starts from it)\n", def.From, name)
			// A sibling's copy sits at the commit its record names, so only a fresh clone can reach the
			// child's. --force stays the user's to give per template: it would replace another source silently.
			so := Options{Pull: o.Pull || fresh, Yes: o.Yes}
			if err := build(ctx, d, global, sib, name, so); err != nil {
				return start{}, err
			}
		}
		rec, err := template.ReadRecord(d.DenHome, def.From)
		if err != nil {
			return start{}, err
		}
		// Building on a sibling of another commit would record a definition hash over files the image was not built from.
		if template.SourceOf(c).Kind == "repo" && rec.Commit != commit {
			return start{}, fmt.Errorf("template %q was built from commit %s, but template %q builds from commit %s — "+
				"run `den build %s --pull` to build both from one commit", def.From, rec.Commit, name, commit, c.Value)
		}
		// The kit image is pinned by digest: never render "<repository>@" from an empty one.
		if rec.From.Digest == "" {
			return start{}, fmt.Errorf("template %q was built without a digest for its image %s — run `den build %s --force`",
				def.From, rec.From.Ref, c.Value)
		}
		return start{template: sib.Tag(), kitImage: sbx.ImageWithDigest(rec.From.Ref, rec.From.Digest), from: rec.From}, nil
	}
	if def.From != "" {
		ref, err := template.CompleteImage(def.From)
		if err != nil {
			return start{}, fmt.Errorf("%s: templates.%s.from: %w", defs.Path, name, err)
		}
		return start{kitImage: ref, from: template.From{Ref: ref}, readDigest: true}, nil
	}
	ref, err := defaultImage(global, defs, name)
	if err != nil {
		return start{}, err
	}
	return start{kitImage: ref, from: template.From{Ref: ref}, readDigest: true}, nil
}

// needsBuild says whether a sibling has no build a child can start from: no record, a record from
// another source (its own build then refuses), a stale record, or no tag in sbx. A repo sibling is
// stale when it was built from another commit than the child's checkout, which only a fresh clone
// can fix: from a kept copy it answers false and startOf refuses with the --pull remedy. A dir
// sibling is stale when its definition hash moved.
func needsBuild(ctx context.Context, d Deps, sib template.Choice, defs *config.LoadedDefinitions, commit string, fresh bool) (bool, error) {
	rec, err := template.ReadRecord(d.DenHome, sib.Name())
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	src := template.SourceOf(sib)
	if rec.Source != src {
		return true, nil
	}
	if src.Kind == "repo" && rec.Commit != commit {
		return fresh, nil
	}
	if src.Kind == "dir" {
		hash, err := template.DefinitionHash(defs, sib.Name())
		if err != nil {
			return false, err
		}
		if hash != rec.DefinitionHash {
			return true, nil
		}
	}
	list, err := sbx.Templates(ctx, d.Sbx)
	if err != nil {
		return false, err
	}
	return sbx.FindTemplate(list, sib.Tag()) == nil, nil
}

// noFromError unwraps to a NoTemplateError so `den build` appends the official images, as `den up` does.
type noFromError struct {
	msg   string
	cause *template.NoTemplateError
}

func (e *noFromError) Error() string { return e.msg }
func (e *noFromError) Unwrap() error { return e.cause }

func defaultImage(global *config.Loaded, defs *config.LoadedDefinitions, name string) (string, error) {
	value := global.File.DefaultTemplate
	why := fmt.Sprintf("%s sets no default_template", global.Path)
	if value != "" {
		dc, err := template.Lookup(nil, global, value)
		if err != nil {
			return "", fmt.Errorf("`default_template` in %s: %w", global.Path, err)
		}
		if dc.IsImage() {
			return dc.Image, nil
		}
		why = fmt.Sprintf("default_template %q in %s is a template, not an image", value, global.Path)
	}
	return "", &noFromError{
		msg: fmt.Sprintf("template %q in %s has no from:, and %s — set from: to an image or a sibling template there, "+
			"or default_template to an image", name, defs.Path, why),
		cause: &template.NoTemplateError{Nest: name, Files: []string{global.Path}},
	}
}

// runVM creates the build VM, reads the digest of its image when it starts from one (T1: right
// after create, before any step), provisions it and saves it as tag. The VM is removed on every way out.
func runVM(ctx context.Context, d Deps, name, vm, tag string, def config.Definition, prov Provisioning, st start) (template.From, error) {
	scratch := filepath.Join(d.DenHome, "build", name)
	if err := os.RemoveAll(scratch); err != nil {
		return template.From{}, fmt.Errorf("clearing %s: %w", scratch, err)
	}
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return template.From{}, fmt.Errorf("creating %s: %w", scratch, err)
	}
	// The VM writes into scratch, so the files sbx reads on the host live beside it, not in it.
	gen := filepath.Join(d.DenHome, "gen", ".build", name)
	if err := os.RemoveAll(gen); err != nil {
		return template.From{}, fmt.Errorf("clearing %s: %w", gen, err)
	}
	if err := instance.WriteGenFile(filepath.Join(gen, "network", "spec.yaml"), sbxenv.NetworkKit(def.Build.Network.Allow)); err != nil {
		return template.From{}, err
	}
	// A kit of its own, like each instance's: each template starts from its own image (T3).
	claudeKit := filepath.Join(gen, "den-claude")
	claudeSpec, err := sbxenv.DenClaudeBuildKit(st.kitImage)
	if err != nil {
		return template.From{}, err
	}
	if err := instance.WriteGenFile(filepath.Join(claudeKit, "spec.yaml"), claudeSpec); err != nil {
		return template.From{}, err
	}
	env, err := sbxenv.Render(sbxenv.Env{
		Name:      vm,
		Kits:      []string{claudeKit, filepath.Join(gen, "network")},
		Workspace: scratch,
		Template:  st.template,
	})
	if err != nil {
		return template.From{}, err
	}
	envFile := filepath.Join(gen, instance.EnvFileName)
	if err := instance.WriteGenFile(envFile, env); err != nil {
		return template.From{}, err
	}

	if err := d.Sbx.Stream(ctx, d.Err, "--no-input", "env", "create", "--force", "--skip-host-commands", envFile); err != nil {
		err = fmt.Errorf("creating the build VM %s: %w", vm, err)
		return template.From{}, removeVM(ctx, d, vm, envFile, err)
	}
	from := st.from
	if st.readDigest {
		digest, err := readDigest(ctx, d, from.Ref)
		if err != nil {
			return template.From{}, removeVM(ctx, d, vm, envFile, err)
		}
		from.Digest = digest
	}
	if err := removeVM(ctx, d, vm, envFile, provision(ctx, d, vm, tag, prov)); err != nil {
		return template.From{}, err
	}
	return from, nil
}

// readDigest is the digest sbx pulled for ref; den up pins the den-claude kit to it, so a build
// without one is refused rather than recorded by reference only.
func readDigest(ctx context.Context, d Deps, ref string) (string, error) {
	list, err := sbx.Templates(ctx, d.Sbx)
	if err != nil {
		return "", err
	}
	t := sbx.FindTemplate(list, ref)
	if t == nil || t.Digest == "" {
		return "", fmt.Errorf("`sbx template ls` reports no digest for %s, the image the build started from — "+
			"den records a template's image by digest; check that sbx is %s or later, then run den build again", ref, sbx.MinVersion)
	}
	return t.Digest, nil
}

func provision(ctx context.Context, d Deps, vm, tag string, prov Provisioning) error {
	for i, s := range prov.Steps {
		if err := d.Sbx.Stream(ctx, d.Out, "exec", vm, "--", "bash", "-lc", prov.Payload(i)); err != nil {
			return fmt.Errorf("step %d/%d %s: %w", i+1, len(prov.Steps), s.Path, err)
		}
	}
	if err := d.Sbx.Stream(ctx, d.Err, "stop", vm); err != nil {
		return fmt.Errorf("stopping %s: %w", vm, err)
	}
	if err := d.Sbx.Stream(ctx, d.Err, "template", "save", vm, tag); err != nil {
		return fmt.Errorf("saving %s as %s: %w", vm, tag, err)
	}
	return nil
}

// removeVM runs even when the context is cancelled, and never masks cause with its own failure.
func removeVM(ctx context.Context, d Deps, vm, envFile string, cause error) error {
	err := d.Sbx.Stream(context.WithoutCancel(ctx), d.Err, "--no-input", "env", "rm", "--force", "--skip-host-commands", envFile)
	if err != nil {
		fmt.Fprintf(d.Err, "warning: removing the build VM %s: %v — run `sbx rm %s`\n", vm, err, vm)
	}
	return cause
}
