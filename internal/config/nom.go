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

// caracteresAlphanumeriques : ce qu'un composant de nom de sandbox DOIT
// commencer par. Un nom qui commence par « - » ou « + » est indiscernable
// d'un flag, aussi bien pour `sbx create --name` que pour den lui-même —
// `den nest show -api` échoue sur un flag inconnu avant même d'atteindre la
// résolution du nest.
const caracteresAlphanumeriques = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// caracteresComposantSandbox : tout ce qu'un composant de nom de sandbox peut
// contenir, au-delà du premier caractère (voir caracteresAlphanumeriques).
//
// `sbx create --name` accepte « letters, numbers, hyphens, periods, plus signs
// and minus signs ». den est PLUS strict d'un cran : le point est exclu, parce
// qu'il sert de séparateur dans `<nest>.<worktree>` et que la décomposition doit
// rester exacte sans consulter la liste des nests déclarés.
//
// Seule définition du charset : ValiderComposantSandbox la contrôle caractère
// par caractère plutôt que de la dupliquer dans une regexp, pour qu'aucune des
// deux ne puisse diverger de l'autre.
const caracteresComposantSandbox = caracteresAlphanumeriques + "+-"

// AplatitComposantSandbox rend la version « nommable » d'un nom : chaque
// caractère absent du charset devient un « - ». C'est ce qui permet à
// `den <nest> -w feature/123` de travailler sur la vraie branche « feature/123 »
// tout en nommant sa sandbox « <nest>.feature-123 ».
//
// L'aplatissement vit ICI, en amont de ValiderComposantSandbox et de
// sbx.NomSandbox, et ne les assouplit pas d'un cran : ils restent la source
// unique du verdict, et tout ce qui consomme un nom de sandbox — argv de `sbx
// create`, policy, corbeille — continue de recevoir un composant strict.
//
// Deux choix, une seule raison : aplatir n'est pas inventer.
//   - Les séries ne fusionnent pas (« feat//x » → « feat--x ») : un caractère
//     tapé, un caractère rendu. Fusionner ferait collisionner davantage de noms
//     distincts sur un même dossier de worktree.
//   - Un premier caractère non alphanumérique est REFUSÉ, jamais préfixé
//     d'office : den ne choisit pas un nom à la place de l'utilisateur.
//
// L'aplatissement n'est pas injectif : « feat/essai » et « feat-essai » rendent
// le même composant, donc le même dossier de worktree. Ce n'est pas un trou —
// worktree.Assure compare la branche demandée à celle du dossier existant et
// refuse le second nom avec un message qui nomme les deux branches.
func AplatitComposantSandbox(genre, nom string) (string, error) {
	if nom == "" {
		return "", fmt.Errorf("%s : le nom est vide", genre)
	}
	var b strings.Builder
	for _, r := range nom {
		if strings.ContainsRune(caracteresComposantSandbox, r) {
			b.WriteRune(r)
			continue
		}
		b.WriteRune('-')
	}
	aplati := b.String()
	// Seul cas qu'aplatir ne peut pas réparer : la position 1. Le message est
	// celui de ValiderComposantSandbox, sur le nom D'ORIGINE — c'est celui que
	// l'utilisateur a tapé, et le lui rendre aplati le laisserait chercher dans
	// sa ligne de commande un nom qui n'y figure pas.
	if err := ValiderComposantSandbox(genre, aplati); err != nil {
		if origine := ValiderComposantSandbox(genre, nom); origine != nil {
			err = origine
		}
		return "", err
	}
	return aplati, nil
}

// ValiderComposantSandbox contrôle qu'un nom peut devenir un composant de nom de
// sandbox. Le message distingue deux fautes : un caractère absent du charset
// (« le caractère %q est interdit ») et un premier caractère mal placé (un « - »
// est autorisé en position 2+, dire qu'il est « interdit » en position 1 serait
// donc faux et déroutant).
func ValiderComposantSandbox(genre, nom string) error {
	if nom == "" {
		return fmt.Errorf("%s : le nom est vide", genre)
	}
	runes := []rune(nom)
	if !strings.ContainsRune(caracteresAlphanumeriques, runes[0]) {
		return fmt.Errorf(
			"%s %q : doit commencer par une lettre ou un chiffre, pas %q — un nom qui ne commence "+
				"pas par un alphanumérique est indiscernable d'un flag", genre, nom, string(runes[0]))
	}
	for _, r := range runes[1:] {
		if !strings.ContainsRune(caracteresComposantSandbox, r) {
			return fmt.Errorf(
				"%s %q : le caractère %q est interdit — ce nom devient un nom de sandbox, "+
					"limité à lettres, chiffres, « - » et « + » (le « . » est réservé au "+
					"séparateur <nest>.<worktree>)", genre, nom, string(r))
		}
	}
	return nil
}
