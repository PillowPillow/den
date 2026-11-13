package nest

import (
	"context"
	"fmt"
	"strings"

	"github.com/PillowPillow/den/internal/prompt"
)

type Selection struct {
	Only, Without []string
	Interactive   bool
}

// Select returns the repos to mount: every required one plus the optional ones the flags or the checklist keep.
func Select(ctx context.Context, r *Resolved, s Selection, p prompt.Prompter) ([]Repo, error) {
	var optional []Repo
	for _, repo := range r.Repos {
		if repo.Optional {
			optional = append(optional, repo)
		}
	}
	names := func() string {
		if len(optional) == 0 {
			return "none"
		}
		var out []string
		for _, o := range optional {
			out = append(out, o.Name)
		}
		return strings.Join(out, ", ")
	}
	if len(s.Only) > 0 && len(s.Without) > 0 {
		return nil, fmt.Errorf("--only and --without cannot be combined")
	}
	isOptional := func(name string) bool {
		for _, o := range optional {
			if o.Name == name {
				return true
			}
		}
		return false
	}
	for flag, list := range map[string][]string{"--only": s.Only, "--without": s.Without} {
		for _, n := range list {
			if !isOptional(n) {
				return nil, fmt.Errorf("%s %q: not an optional repo of nest %q; optional repos: %s", flag, n, r.Name, names())
			}
		}
	}

	keep := map[string]bool{}
	switch {
	case len(s.Only) > 0:
		for _, n := range s.Only {
			keep[n] = true
		}
	case len(s.Without) > 0:
		for _, o := range optional {
			keep[o.Name] = true
		}
		for _, n := range s.Without {
			delete(keep, n)
		}
	case s.Interactive && len(optional) > 0:
		opts := make([]prompt.Option, len(optional))
		for i, o := range optional {
			opts[i] = prompt.Option{Value: o.Name, Label: o.Name, Description: o.Path}
		}
		picked, err := p.MultiSelect(ctx, prompt.MultiSelectRequest{
			Title:       fmt.Sprintf("Optional repos of %s", r.Name),
			Options:     opts,
			Preselected: true,
		})
		if err != nil {
			return nil, err
		}
		for _, n := range picked {
			keep[n] = true
		}
	default:
		return append([]Repo(nil), r.Repos...), nil
	}

	var out []Repo
	for _, repo := range r.Repos {
		if !repo.Optional || keep[repo.Name] {
			out = append(out, repo)
		}
	}
	return out, nil
}
