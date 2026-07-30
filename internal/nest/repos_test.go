package nest

import (
	"slices"
	"strings"
	"testing"
)

func repos() []Repo {
	return []Repo{
		{Path: "/dev/api"},                    // required
		{Path: "/dev/front", Optional: true},  // optional
		{Path: "/dev/worker", Optional: true}, // optional
	}
}

func names(rs []Repo) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Name())
	}
	return out
}

func TestSelectReposNominalCases(t *testing.T) {
	cases := []struct {
		name     string
		without  []string
		only     []string
		expected []string
	}{
		{"no filter: everything", nil, nil, []string{"api", "front", "worker"}},
		{"without one optional", []string{"front"}, nil, []string{"api", "worker"}},
		{"without several", []string{"front", "worker"}, nil, []string{"api"}},
		{"only one optional: required ones stay", nil, []string{"front"}, []string{"api", "front"}},
		{"only one required: optional ones drop", nil, []string{"api"}, []string{"api"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := selectRepos(repos(), c.without, c.only)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !slices.Equal(names(got), c.expected) {
				t.Errorf("SelectRepos = %v, expected %v", names(got), c.expected)
			}
		})
	}
}

func TestSelectReposErrors(t *testing.T) {
	cases := []struct {
		name     string
		without  []string
		only     []string
		expected string
	}{
		{"without and only together", []string{"front"}, []string{"worker"}, "mutually exclusive"},
		{"without a required repo", []string{"api"}, nil, "required"},
		{"without an unknown repo", []string{"ghost"}, nil, "ghost"},
		{"only an unknown repo", nil, []string{"ghost"}, "ghost"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := selectRepos(repos(), c.without, c.only)
			if err == nil {
				t.Fatalf("expected an error mentioning %q", c.expected)
			}
			if !strings.Contains(err.Error(), c.expected) {
				t.Errorf("error = %q, expected a mention of %q", err.Error(), c.expected)
			}
		})
	}
}

func TestSelectReposDoesNotMutateInput(t *testing.T) {
	in := repos()
	if _, err := selectRepos(in, []string{"front"}, nil); err != nil {
		t.Fatal(err)
	}
	if len(in) != 3 {
		t.Errorf("the input was mutated: %d repos instead of 3", len(in))
	}
}
