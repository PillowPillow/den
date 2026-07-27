package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ValiderNom rejette les noms d'objets qui ne désignent pas un enfant direct de
// ~/.den. Un nom de nest ou de stack est un identifiant, pas un chemin : sans
// cette garde, `den nest show ../../../../etc/passwd` construit un chemin qui
// sort du den home. L'impact est faible tant que den ne fait que lire les
// fichiers de l'utilisateur, mais ce nom devient ensuite un nom de sandbox, un
// label `den.nest` et la graine du hash de la fenêtre de ports : il doit être
// propre à la source.
//
// genre nomme l'objet dans le message d'erreur (« nest », « stack »).
func ValiderNom(genre, nom string) error {
	switch {
	case nom == "":
		return fmt.Errorf("%s : le nom est vide", genre)
	case strings.ContainsRune(nom, '/') || strings.ContainsRune(nom, filepath.Separator):
		return fmt.Errorf(
			"%s %q : un nom ne peut pas contenir de séparateur de chemin — c'est un identifiant "+
				"dans ~/.den, pas un chemin", genre, nom)
	case nom == "." || nom == "..":
		return fmt.Errorf("%s %q : nom réservé", genre, nom)
	}
	return nil
}
