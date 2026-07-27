package config_test

import (
	"path/filepath"
	"testing"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
)

// L'exemple livré dans examples/den-home doit charger et valider sans erreur :
// c'est le point de départ que l'utilisateur recopie dans ~/.den.
func TestExempleDenHomeEstValide(t *testing.T) {
	home := filepath.Join("..", "..", "examples", "den-home")

	g, err := config.LoadGlobal(home)
	if err != nil {
		t.Fatalf("chargement de l'exemple : %v", err)
	}
	if errs := g.Validate(); len(errs) != 0 {
		t.Fatalf("l'exemple ne valide pas : %v", errs)
	}

	stacks, err := config.LoadStacks(home)
	if err != nil {
		t.Fatalf("chargement des stacks de l'exemple : %v", err)
	}
	if _, ok := stacks[g.Defaults.Stack]; !ok {
		t.Errorf("defaults.stack = %q absent des stacks de l'exemple", g.Defaults.Stack)
	}

	nests, err := nest.ListNests(home)
	if err != nil {
		t.Fatalf("chargement des nests de l'exemple : %v", err)
	}
	if len(nests) == 0 {
		t.Fatal("l'exemple ne déclare aucun nest")
	}
	for _, n := range nests {
		if _, err := nest.Resolve(g, stacks, n, nest.Options{}); err != nil {
			t.Errorf("nest %q de l'exemple ne se résout pas : %v", n.Name, err)
		}
	}
}
