package nest

import (
	"strings"
	"testing"
)

func repos() []Repo {
	return []Repo{
		{Path: "/dev/api"},                    // requis
		{Path: "/dev/front", Optional: true},  // optionnel
		{Path: "/dev/worker", Optional: true}, // optionnel
	}
}

func noms(rs []Repo) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Name())
	}
	return out
}

func egal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSelectReposCasNominaux(t *testing.T) {
	cas := []struct {
		nom     string
		without []string
		only    []string
		attendu []string
	}{
		{"sans filtre : tout", nil, nil, []string{"api", "front", "worker"}},
		{"without un optionnel", []string{"front"}, nil, []string{"api", "worker"}},
		{"without plusieurs", []string{"front", "worker"}, nil, []string{"api"}},
		{"only un optionnel : les requis restent", nil, []string{"front"}, []string{"api", "front"}},
		{"only un requis : les optionnels tombent", nil, []string{"api"}, []string{"api"}},
	}
	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			got, err := SelectRepos(repos(), c.without, c.only)
			if err != nil {
				t.Fatalf("erreur inattendue : %v", err)
			}
			if !egal(noms(got), c.attendu) {
				t.Errorf("SelectRepos = %v, attendu %v", noms(got), c.attendu)
			}
		})
	}
}

func TestSelectReposErreurs(t *testing.T) {
	cas := []struct {
		nom     string
		without []string
		only    []string
		attendu string
	}{
		{"without et only ensemble", []string{"front"}, []string{"worker"}, "mutuellement exclusifs"},
		{"without un repo requis", []string{"api"}, nil, "requis"},
		{"without un repo inconnu", []string{"fantome"}, nil, "fantome"},
		{"only un repo inconnu", nil, []string{"fantome"}, "fantome"},
	}
	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			_, err := SelectRepos(repos(), c.without, c.only)
			if err == nil {
				t.Fatalf("attendu une erreur mentionnant %q", c.attendu)
			}
			if !strings.Contains(err.Error(), c.attendu) {
				t.Errorf("erreur = %q, attendu une mention de %q", err.Error(), c.attendu)
			}
		})
	}
}

func TestSelectReposNeMutePasLEntree(t *testing.T) {
	in := repos()
	if _, err := SelectRepos(in, []string{"front"}, nil); err != nil {
		t.Fatal(err)
	}
	if len(in) != 3 {
		t.Errorf("l'entrée a été mutée : %d repos au lieu de 3", len(in))
	}
}
