package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Stack est une recette d'image buildable (spec §4.2).
type Stack struct {
	// Name vient du nom du dossier, JAMAIS du contenu : un objet a une seule
	// identité, non falsifiable (spec §2).
	Name   string `yaml:"-"`
	Image  string `yaml:"image"`
	Parent string `yaml:"parent"` // arête du DAG de build
	Kit    string `yaml:"kit"`    // relatif au dossier de la stack dans le YAML, absolu après chargement
	// Kits : kits transverses layerés AVANT Kit (ex. ssh-known-hosts).
	// Relatifs au dossier de la stack dans le YAML, absolus après chargement.
	// L'ORDRE EST SIGNIFIANT : c'est un ordre de layering sbx, pas un ensemble —
	// ne jamais le trier.
	Kits   []string `yaml:"kits"`
	Egress []string `yaml:"egress"`

	Dir string `yaml:"-"` // dossier de la stack, rempli au chargement
}

// LoadStack lit <denHome>/stacks/<name>/stack.yaml.
func LoadStack(denHome, name string) (*Stack, error) {
	if err := ValiderNom("stack", name); err != nil {
		return nil, err
	}
	dir := filepath.Join(denHome, "stacks", name)
	chemin := filepath.Join(dir, "stack.yaml")

	brut, err := os.ReadFile(chemin)
	if err != nil {
		// « déclare-la » et « répare les droits » sont deux gestes différents :
		// doctor relaie ce message tel quel, il doit trancher.
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("stack %q : introuvable — attendu %s", name, chemin)
		}
		return nil, fmt.Errorf("stack %q : lecture de %s impossible : %w", name, chemin, err)
	}

	var s Stack
	if err := DecodeYAMLStrict(chemin, brut, &s); err != nil {
		return nil, err
	}

	s.Name = name // le dossier fait foi, sans condition
	s.Dir = dir
	if s.Kit != "" && !filepath.IsAbs(s.Kit) {
		s.Kit = filepath.Join(dir, s.Kit)
	}
	for i, k := range s.Kits {
		if k != "" && !filepath.IsAbs(k) {
			s.Kits[i] = filepath.Join(dir, k)
		}
	}
	return &s, nil
}

// LoadStacks charge toutes les stacks déclarées. Un dossier sans stack.yaml est
// ignoré (brouillon), un dossier stacks/ absent donne une map vide : un den
// fraîchement créé n'est pas une erreur.
func LoadStacks(denHome string) (map[string]*Stack, error) {
	racine := filepath.Join(denHome, "stacks")
	entrees, err := os.ReadDir(racine)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]*Stack{}, nil
		}
		return nil, fmt.Errorf("lecture de %s : %w", racine, err)
	}

	stacks := make(map[string]*Stack)
	for _, e := range entrees {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(racine, e.Name(), "stack.yaml")); err != nil {
			continue
		}
		s, err := LoadStack(denHome, e.Name())
		if err != nil {
			return nil, err
		}
		stacks[e.Name()] = s // le dossier est l'identité, ici comme dans LoadStack
	}
	return stacks, nil
}
