package config

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
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

// KitsDeclares rend les kits que cette stack déclare, dans l'ORDRE DE LAYERING
// de sbx : `kits:` (transverses) d'abord, `kit:` ensuite. Les entrées vides
// sont filtrées — une ligne sans contenu n'est pas un kit manquant.
//
// SOURCE UNIQUE de « quels kits cette stack déclare, et dans quel ordre ».
// Le filtre des entrées vides existait auparavant à trois endroits qui ne
// s'accordaient pas : sbx.ArgvCreate et doctor filtraient, la composition du
// chemin de spawn ne filtrait que le `kit:` SINGULIER vide. Mesuré, avec
// `kits: ["", "transverse"]` : `den doctor` rendait « tout est en ordre » et
// `den <nest>` refusait sur un « kit introuvable :  » au chemin vide. Deux
// juges d'un même champ doivent juger pareil ; ils n'y arrivent durablement
// qu'en n'étant qu'un seul.
//
// L'ORDRE EST UNE PROPRIÉTÉ DE SÛRETÉ, pas une commodité d'affichage : sbx
// layere les kits dans l'ordre des `--kit`, et le mixin — ajouté APRÈS cette
// liste par sbx.ArgvCreate — doit rester le dernier pour que sa commande de
// fraîcheur soit la dernière startup jouée (spec §9.1). Ne jamais trier cette
// liste, ne jamais en inverser les deux moitiés.
func (s *Stack) KitsDeclares() []string {
	kits := make([]string, 0, len(s.Kits)+1)
	for _, k := range s.Kits {
		if k != "" {
			kits = append(kits, k)
		}
	}
	if s.Kit != "" {
		kits = append(kits, s.Kit)
	}
	return kits
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

// StackCassee est une stack présente sur disque mais non chargeable.
type StackCassee struct {
	Nom string
	Err error
}

// Stacks porte le résultat du chargement de <denHome>/stacks : les stacks
// chargeables, et À PART celles qui ne le sont pas.
//
// Une stack cassée ne masque PAS les autres — même doctrine que ListNests
// (internal/nest/nest.go), et pour la même raison, mesurée sur le binaire
// assemblé en tâche 17c. Avant cette séparation, une faute de frappe dans une
// stack que PERSONNE n'utilise faisait échouer LoadStacks en bloc, et :
//
//	den api          → échec, alors que son nest référence une stack saine
//	den nest show api→ échec, idem
//	den doctor       → « stack "devx" introuvable » et « nest api : stack "devx"
//	                   introuvable », deux diagnostics FAUX désignant une stack
//	                   parfaitement valide
//
// Le pire n'était donc pas le blocage mais le mensonge : doctor envoyait
// l'utilisateur réparer devx, qui n'avait rien. Rendre les deux listes séparées
// fait que le seul objet nommé est celui qui est vraiment cassé.
type Stacks struct {
	Saines  map[string]*Stack
	Cassees []StackCassee
}

// Get rend la stack nommée, ou une erreur qui DISTINGUE les deux façons de ne
// pas l'avoir : elle est déclarée mais illisible, ou elle n'existe pas du tout.
//
// SOURCE UNIQUE de ce verdict. « introuvable » sur une stack qui existe mais ne
// se charge pas enverrait l'utilisateur créer un fichier qu'il a déjà, au lieu
// de corriger celui qu'il a — et c'est précisément le message que rendait la
// version précédente dès lors qu'une stack cassée n'était plus dans la map.
func (s Stacks) Get(nom string) (*Stack, error) {
	if st, ok := s.Saines[nom]; ok {
		return st, nil
	}
	for _, c := range s.Cassees {
		if c.Nom == nom {
			return nil, fmt.Errorf("stack %q : illisible : %w", nom, c.Err)
		}
	}
	return nil, fmt.Errorf("stack %q introuvable (stacks déclarées : %v)", nom, s.Noms())
}

// Noms rend les noms des stacks SAINES, triés. Destiné à l'affichage : une map
// Go n'est pas ordonnée, et un message d'erreur doit être reproductible.
func (s Stacks) Noms() []string {
	return slices.Sorted(maps.Keys(s.Saines))
}

// LoadStacks charge toutes les stacks déclarées. Un dossier sans stack.yaml est
// ignoré (brouillon), un dossier stacks/ absent donne un résultat vide : un den
// fraîchement créé n'est pas une erreur.
//
// L'erreur renvoyée est réservée aux échecs STRUCTURELS (dossier stacks/
// illisible) : là, il n'y a rien à charger du tout. Une stack qui ne se décode
// pas part dans Cassees, elle n'interrompt rien — voir la godoc de Stacks.
func LoadStacks(denHome string) (Stacks, error) {
	racine := filepath.Join(denHome, "stacks")
	entrees, err := os.ReadDir(racine)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Stacks{Saines: map[string]*Stack{}}, nil
		}
		return Stacks{}, fmt.Errorf("lecture de %s : %w", racine, err)
	}

	out := Stacks{Saines: make(map[string]*Stack)}
	// os.ReadDir trie déjà par nom : Cassees est donc déterministe sans tri
	// supplémentaire, et `den doctor` rend ses diagnostics dans le même ordre
	// d'une exécution à l'autre.
	for _, e := range entrees {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(racine, e.Name(), "stack.yaml")); err != nil {
			continue
		}
		s, err := LoadStack(denHome, e.Name())
		if err != nil {
			out.Cassees = append(out.Cassees, StackCassee{Nom: e.Name(), Err: err})
			continue
		}
		out.Saines[e.Name()] = s // le dossier est l'identité, ici comme dans LoadStack
	}
	return out, nil
}
