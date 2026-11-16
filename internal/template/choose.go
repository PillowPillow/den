// Package template resolves what a nest runs on: a template declared in a
// den.yaml, or an image. It imports config and sbx, never instance, which
// imports it.
package template

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/PillowPillow/den/internal/config"
)

// OfficialRepository completes a short image name.
const OfficialRepository = "docker.io/docker/sandbox-templates"

type Choice struct {
	Value      string               // as written
	Key        string               // templates: key; "" for an image
	Entry      config.TemplateEntry // zero for an image
	DeclaredIn *config.Loaded       // file whose templates: holds Key
	Image      string               // completed reference; "" for a template
	Origin     string               // the key to fix, e.g. "`default_template` in /h/.den/den.yaml"
	Searched   []string             // the den.yaml files whose templates: were searched
}

func (c Choice) IsImage() bool { return c.Image != "" }

func (c Choice) Name() string { return c.Entry.TemplateName(c.Key) }

func (c Choice) Tag() string { return c.Name() + ":v2" }

type NoTemplateError struct {
	Nest  string
	Files []string
}

func (e *NoTemplateError) Error() string {
	return fmt.Sprintf("nest %q has no template: set template: on it, or default_template in %s",
		e.Nest, strings.Join(e.Files, " or "))
}

// files lists the loaded files once each: a nest declared in the global
// den.yaml has the same file as nestFile and global.
func files(first, global *config.Loaded) []*config.Loaded {
	var out []*config.Loaded
	for _, l := range []*config.Loaded{first, global} {
		if l == nil {
			continue
		}
		if len(out) == 1 && out[0].Path == l.Path {
			continue
		}
		out = append(out, l)
	}
	return out
}

func paths(ls []*config.Loaded) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = l.Path
	}
	return out
}

// Choose picks the nest's template, then its file's default_template, then the
// global one.
func Choose(nestFile, global *config.Loaded, nestName string) (Choice, error) {
	home := nestFile
	if home == nil {
		home = global
	}
	var value, origin string
	if home != nil {
		if n, ok := home.File.Nests[nestName]; ok && n.Template != "" {
			value, origin = n.Template, fmt.Sprintf("`template:` of nest %q in %s", nestName, home.Path)
		}
	}
	if value == "" {
		for _, l := range files(nestFile, global) {
			if l.File.DefaultTemplate != "" {
				value, origin = l.File.DefaultTemplate, fmt.Sprintf("`default_template` in %s", l.Path)
				break
			}
		}
	}
	if value == "" {
		return Choice{}, &NoTemplateError{Nest: nestName, Files: paths(files(nestFile, global))}
	}
	c, err := Lookup(nestFile, global, value)
	if err != nil {
		return Choice{}, fmt.Errorf("%s: %w", origin, err)
	}
	c.Origin = origin
	return c, nil
}

// Lookup searches first.templates, then global.templates, then reads value as
// an image. A project template masks a global one of the same key. first is
// nil outside any project.
func Lookup(first, global *config.Loaded, value string) (Choice, error) {
	ls := files(first, global)
	c := Choice{Value: value, Searched: paths(ls)}
	for _, l := range ls {
		if e, ok := l.File.Templates[value]; ok {
			c.Key, c.Entry, c.DeclaredIn = value, e, l
			return c, nil
		}
	}
	img, err := CompleteImage(value)
	if err != nil {
		return Choice{}, fmt.Errorf("%q is not a template of %s: %w", value, strings.Join(c.Searched, " or "), err)
	}
	c.Image = img
	return c, nil
}

var shortName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)

// CompleteImage reads a value without "/" as a short name of the official
// repository and a value with "/" as a full address. A tag on a short name
// (`ubuntu:22.04`) is refused: Docker Hub would read it as library/ubuntu.
func CompleteImage(value string) (string, error) {
	if strings.Contains(value, "/") {
		return value, nil
	}
	if !shortName.MatchString(value) {
		return "", fmt.Errorf("%q is not an official image name: write the full address (registry/repository:tag)", value)
	}
	return OfficialRepository + ":" + value, nil
}
