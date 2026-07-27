package nest

import "testing"

func TestUnionEgress(t *testing.T) {
	cas := []struct {
		nom     string
		listes  [][]string
		attendu []string
	}{
		{"vide", nil, []string{}},
		{"une seule liste triée", [][]string{{"b.com", "a.com"}}, []string{"a.com", "b.com"}},
		{
			"cascade global stack nest",
			[][]string{{"api.anthropic.com", "github.com"}, {"gitlab.digitaleo.com"}, {"10.22.11.54:27017"}},
			[]string{"10.22.11.54:27017", "api.anthropic.com", "github.com", "gitlab.digitaleo.com"},
		},
		{
			"doublons entre niveaux dedupliques",
			[][]string{{"github.com"}, {"github.com"}, {"github.com", "a.com"}},
			[]string{"a.com", "github.com"},
		},
		{"listes vides ignorees", [][]string{nil, {"a.com"}, {}}, []string{"a.com"}},
		{"chaines vides ignorees", [][]string{{"", "a.com", ""}}, []string{"a.com"}},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			got := UnionEgress(c.listes...)
			if len(got) != len(c.attendu) {
				t.Fatalf("UnionEgress = %v, attendu %v", got, c.attendu)
			}
			for i := range got {
				if got[i] != c.attendu[i] {
					t.Fatalf("UnionEgress = %v, attendu %v", got, c.attendu)
				}
			}
		})
	}
}

func TestUnionEgressRenvoieToujoursUneSliceNonNil(t *testing.T) {
	// Le rendu YAML du mixin distingue `allow: []` de `allow: null`.
	if got := UnionEgress(); got == nil {
		t.Error("attendu une slice vide non-nil")
	}
}
