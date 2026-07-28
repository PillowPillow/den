// Package nest charge les nests (objets spawnables) et calcule les dérivations
// pures qui en découlent : sélection de repos, union d'egress, résolution d'agent.
package nest

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/PillowPillow/den/internal/config"
)

// Repo est un dépôt co-monté dans la sandbox.
type Repo struct {
	Path     string `yaml:"path"`
	Optional bool   `yaml:"optional"`
}

// Name est le nom court du repo (basename), utilisé par --without/--only.
func (r Repo) Name() string { return filepath.Base(r.Path) }

// PortDecl est un port déclaré par le nest (spec §8).
type PortDecl struct {
	Name         string `yaml:"name"`
	Container    int    `yaml:"container"`
	Open         bool   `yaml:"open"`
	LoopbackLock bool   `yaml:"loopback_lock"`
}

// Ports porte la fenêtre déclarée. Base == 0 => dérivée du hash du nom (plan Ports).
type Ports struct {
	Base    int        `yaml:"base"`
	Publish []PortDecl `yaml:"publish"`
}

// Nest est un objet spawnable (spec §4.3).
type Nest struct {
	// Name vient du basename du fichier, JAMAIS du contenu : un objet a une
	// seule identité, non falsifiable (spec §2).
	Name   string            `yaml:"-"`
	Stack  string            `yaml:"stack"`
	Env    map[string]string `yaml:"env"`
	Egress []string          `yaml:"egress"`
	Repos  []Repo            `yaml:"repos"`
	Ports  Ports             `yaml:"ports"`
	Agents map[string]string `yaml:"agents"` // override du config_dir par agent
}

// LoadNest lit <denHome>/nests/<name>.yaml.
func LoadNest(denHome, name string) (*Nest, error) {
	// ValiderNom AVANT ValiderComposantSandbox, dans cet ordre précis : les deux
	// se recouvrent (le charset sandbox rejette déjà "/", "." et ".."), mais
	// ValiderNom rend un message qui nomme l'INTENTION (« c'est un identifiant
	// dans ~/.den, pas un chemin ») pour ../../etc/passwd. Inverser l'ordre
	// ferait remonter « le caractère "/" est interdit », vrai mais qui manque
	// le vrai problème : une tentative de sortir du den home.
	if err := config.ValiderNom("nest", name); err != nil {
		return nil, err
	}
	// Le nom d'un nest devient un nom de sandbox (sbx n'a pas de --label) : le
	// refuser ici plutôt qu'au spawn fait remonter le problème dès `den nest ls`.
	if err := config.ValiderComposantSandbox("nest", name); err != nil {
		return nil, err
	}
	chemin := filepath.Join(denHome, "nests", name+".yaml")

	brut, err := os.ReadFile(chemin)
	if err != nil {
		return nil, fmt.Errorf("nest %q : lecture de %s : %w", name, chemin, err)
	}

	var n Nest
	if err := config.DecodeYAMLStrict(chemin, brut, &n); err != nil {
		return nil, err
	}

	n.Name = name // le nom de fichier fait foi, sans condition
	for i, r := range n.Repos {
		if n.Repos[i].Path, err = config.ExpandPath(r.Path); err != nil {
			return nil, fmt.Errorf("nest %q, repo %q : %w", n.Name, r.Path, err)
		}
	}
	// Après expansion : deux chemins écrits différemment peuvent converger.
	if err := verifieNomsUniques(n.Repos); err != nil {
		return nil, fmt.Errorf("nest %q : %w", n.Name, err)
	}
	for agent, dir := range n.Agents {
		expanse, err := config.ExpandPath(dir)
		if err != nil {
			return nil, fmt.Errorf("nest %q, agent %q : %w", n.Name, agent, err)
		}
		n.Agents[agent] = expanse
	}
	return &n, nil
}

// NestCasse est un nest présent sur disque mais non chargeable.
type NestCasse struct {
	Nom string
	Err error
}

// ListNests charge tous les nests déclarés, triés par nom.
//
// Un nest illisible ne masque PAS les autres : il est renvoyé à part. Le
// décodage strict rend un simple `egres:` fatal au chargement, et faire
// disparaître toute la liste pour une faute de frappe dans un fichier laisserait
// l'utilisateur sans le moyen de voir lequel — `den nest ls` et `den doctor`
// sont précisément les outils censés le lui dire.
//
// L'erreur renvoyée est réservée aux échecs STRUCTURELS (dossier nests/
// illisible) : là, il n'y a rien à lister du tout.
func ListNests(denHome string) ([]*Nest, []NestCasse, error) {
	racine := filepath.Join(denHome, "nests")
	entrees, err := os.ReadDir(racine)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("lecture de %s : %w", racine, err)
	}

	// candidat porte à la fois le nom tronqué (l'identité, utilisée pour le
	// tri et pour LoadNest) et le nom de fichier complet (repli d'affichage
	// pour un fichier ".yaml" dont le nom tronqué est vide, voir plus bas).
	type candidat struct{ nom, fichier string }
	var candidats []candidat
	for _, e := range entrees {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		candidats = append(candidats, candidat{
			nom:     strings.TrimSuffix(e.Name(), ".yaml"),
			fichier: e.Name(),
		})
	}
	// Trier par nom tronqué, PAS par nom de fichier : ils divergent dès que
	// deux noms partagent un préfixe ('-' précède '.' en ASCII), voir
	// TestListNestsTriDivergeDeLOrdreFichier.
	sort.Slice(candidats, func(i, j int) bool { return candidats[i].nom < candidats[j].nom })

	nests := make([]*Nest, 0, len(candidats))
	var casses []NestCasse
	for _, c := range candidats {
		n, err := LoadNest(denHome, c.nom)
		if err != nil {
			nomAffiche := c.nom
			if nomAffiche == "" {
				// Fichier littéralement nommé ".yaml" : le nom tronqué est
				// vide. Retomber sur le nom de fichier complet pour que
				// l'avertissement nomme quelque chose — sinon l'utilisateur
				// n'a aucun moyen de savoir quel fichier supprimer.
				nomAffiche = c.fichier
			}
			casses = append(casses, NestCasse{Nom: nomAffiche, Err: err})
			continue
		}
		nests = append(nests, n)
	}
	return nests, casses, nil
}
