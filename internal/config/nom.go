package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// ValiderNom rejette les noms d'objets qui ne désignent pas un enfant direct de
// ~/.den. Un nom de nest ou de stack est un identifiant, pas un chemin : sans
// cette garde, `den nest show ../../../../etc/passwd` construit un chemin qui
// sort du den home. L'impact est faible tant que den ne fait que lire les
// fichiers de l'utilisateur, mais ce nom devient ensuite un nom de sandbox et
// la graine du hash de la fenêtre de ports : il doit être propre à la source.
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

// motifComposantSandbox : ce qu'un composant de nom de sandbox peut contenir.
//
// `sbx create --name` accepte « letters, numbers, hyphens, periods, plus signs
// and minus signs ». den est PLUS strict d'un cran : le point est exclu, parce
// qu'il sert de séparateur dans `<nest>.<worktree>` et que la décomposition doit
// rester exacte sans consulter la liste des nests déclarés.
var motifComposantSandbox = regexp.MustCompile(`^[A-Za-z0-9+-]+$`)

// ValiderComposantSandbox contrôle qu'un nom peut devenir un composant de nom de
// sandbox. Le message nomme le premier caractère fautif : « nom invalide » seul
// force l'utilisateur à deviner lequel.
func ValiderComposantSandbox(genre, nom string) error {
	if nom == "" {
		return fmt.Errorf("%s : le nom est vide", genre)
	}
	if motifComposantSandbox.MatchString(nom) {
		return nil
	}
	for _, r := range nom {
		if !strings.ContainsRune(
			"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+-", r) {
			return fmt.Errorf(
				"%s %q : le caractère %q est interdit — ce nom devient un nom de sandbox, "+
					"limité à lettres, chiffres, « - » et « + » (le « . » est réservé au "+
					"séparateur <nest>.<worktree>)", genre, nom, string(r))
		}
	}
	return fmt.Errorf("%s %q : nom invalide", genre, nom)
}
