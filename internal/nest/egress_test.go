package nest

import "testing"

func TestUnionEgress(t *testing.T) {
	cases := []struct {
		name     string
		lists    [][]string
		expected []string
	}{
		{"empty", nil, []string{}},
		{"a single sorted list", [][]string{{"b.com", "a.com"}}, []string{"a.com", "b.com"}},
		{
			"global stack nest cascade",
			[][]string{{"api.anthropic.com", "github.com"}, {"gitlab.digitaleo.com"}, {"10.22.11.54:27017"}},
			[]string{"10.22.11.54:27017", "api.anthropic.com", "github.com", "gitlab.digitaleo.com"},
		},
		{
			"duplicates across levels deduped",
			[][]string{{"github.com"}, {"github.com"}, {"github.com", "a.com"}},
			[]string{"a.com", "github.com"},
		},
		{"empty lists ignored", [][]string{nil, {"a.com"}, {}}, []string{"a.com"}},
		{"empty strings ignored", [][]string{{"", "a.com", ""}}, []string{"a.com"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := unionEgress(c.lists...)
			if len(got) != len(c.expected) {
				t.Fatalf("UnionEgress = %v, expected %v", got, c.expected)
			}
			for i := range got {
				if got[i] != c.expected[i] {
					t.Fatalf("UnionEgress = %v, expected %v", got, c.expected)
				}
			}
		})
	}
}

func TestUnionEgressAlwaysReturnsANonNilSlice(t *testing.T) {
	// The mixin's YAML rendering distinguishes `allow: []` from `allow: null`.
	if got := unionEgress(); got == nil {
		t.Error("expected an empty, non-nil slice")
	}
}
