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
	"gopkg.in/yaml.v3"
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
	Name   string            `yaml:"name"`
	Stack  string            `yaml:"stack"`
	Env    map[string]string `yaml:"env"`
	Egress []string          `yaml:"egress"`
	Repos  []Repo            `yaml:"repos"`
	Ports  Ports             `yaml:"ports"`
	Agents map[string]string `yaml:"agents"` // override du config_dir par agent
}

// LoadNest lit <denHome>/nests/<name>.yaml.
func LoadNest(denHome, name string) (*Nest, error) {
	chemin := filepath.Join(denHome, "nests", name+".yaml")

	brut, err := os.ReadFile(chemin)
	if err != nil {
		return nil, fmt.Errorf("nest %q : lecture de %s : %w", name, chemin, err)
	}

	var n Nest
	if err := yaml.Unmarshal(brut, &n); err != nil {
		return nil, fmt.Errorf("%s : YAML invalide : %w", chemin, err)
	}

	if n.Name == "" {
		n.Name = name // le nom de fichier fait foi
	}
	for i, r := range n.Repos {
		if n.Repos[i].Path, err = config.ExpandPath(r.Path); err != nil {
			return nil, fmt.Errorf("nest %q, repo %q : %w", n.Name, r.Path, err)
		}
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

// ListNests charge tous les nests déclarés, triés par nom. Dossier absent = liste vide.
func ListNests(denHome string) ([]*Nest, error) {
	racine := filepath.Join(denHome, "nests")
	entrees, err := os.ReadDir(racine)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("lecture de %s : %w", racine, err)
	}

	var noms []string
	for _, e := range entrees {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		noms = append(noms, strings.TrimSuffix(e.Name(), ".yaml"))
	}
	sort.Strings(noms)

	nests := make([]*Nest, 0, len(noms))
	for _, nom := range noms {
		n, err := LoadNest(denHome, nom)
		if err != nil {
			return nil, err
		}
		nests = append(nests, n)
	}
	return nests, nil
}
