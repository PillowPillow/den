package config

import (
	"errors"
	"io/fs"
	"syscall"
)

// ErreurFichier rend en français le MOTIF d'un échec de système de fichiers,
// sans répéter le chemin.
//
// Elle existe parce que les deux moitiés du problème sont irréconciliables
// autrement :
//
//   - le *fs.PathError de l'OS porte DÉJÀ le chemin absolu, et tous les
//     appelants de den le nomment eux-mêmes dans leur contexte. Mesuré sur le
//     binaire, sur l'erreur la plus banale du projet :
//     « nest "inconnu" : lecture de <chemin> : open <chemin>: no such file or
//     directory » — le chemin deux fois, et la moitié droite en anglais ;
//   - le simple `%v` sur l'erreur nue, qui supprimerait la répétition, casserait
//     la chaîne : `errors.Is(err, fs.ErrNotExist)` est réellement utilisé sur
//     ces erreurs-là (internal/nest/nest.go pour discriminer le nest absent,
//     internal/spawn/spawn.go pour distinguer « cache purgé » de « mixin
//     corrompu »), et deux tests l'exigent nommément.
//
// D'où le type : Error() ne rend que le motif, Unwrap garde la chaîne intacte.
//
// Le TRADUCTEUR est délibérément court. Ce qui n'est pas reconnu remonte tel
// quel — même politique que traduitErreurDeFlag (internal/cli/francais.go) : un
// message anglais exact vaut mieux qu'un message français faux.
type ErreurFichier struct{ Err error }

func (e *ErreurFichier) Error() string {
	// Le motif seul, quand l'erreur en porte un : c'est *fs.PathError qui
	// double le chemin, et c'est son champ Err qui dit ce qui s'est passé.
	motif := e.Err
	var pe *fs.PathError
	if errors.As(e.Err, &pe) {
		motif = pe.Err
	}
	switch {
	case errors.Is(motif, fs.ErrNotExist):
		return "le fichier n'existe pas"
	case errors.Is(motif, fs.ErrPermission):
		return "permission refusée"
	case errors.Is(motif, syscall.EISDIR):
		return "c'est un dossier, pas un fichier"
	case errors.Is(motif, syscall.ENOTDIR):
		return "un élément du chemin n'est pas un dossier"
	default:
		return motif.Error()
	}
}

// Unwrap rend l'erreur d'ORIGINE, *fs.PathError compris : c'est elle que
// errors.Is/errors.As doivent trouver, pas le motif isolé qu'Error affiche.
func (e *ErreurFichier) Unwrap() error { return e.Err }
