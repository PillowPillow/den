package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ErreurFichier a UN travail : rendre en français le motif d'un échec de
// lecture, sans répéter le chemin que l'appelant vient de nommer.
//
// Le défaut mesuré sur le binaire avant ce type, sur l'erreur la plus banale
// qui soit (`den inconnu`) :
//
//	den: nest "inconnu" : lecture de <chemin> : open <chemin>: no such file or directory
//
// Le chemin absolu écrit DEUX fois, et la moitié droite en anglais — c'est le
// *fs.PathError de l'OS, qui porte déjà le chemin.
func TestErreurFichierNeRepetePasLeCheminEtParleFrancais(t *testing.T) {
	dir := t.TempDir()
	chemin := filepath.Join(dir, "absent.yaml")

	_, err := os.ReadFile(chemin)
	if err == nil {
		t.Fatal("précondition : la lecture d'un fichier absent doit échouer")
	}

	// Exactement la forme qu'emploient les appelants : eux nomment le chemin,
	// ErreurFichier ne dit plus que le motif.
	enveloppe := fmt.Errorf("lecture de %s : %w", chemin, &ErreurFichier{Err: err})
	message := enveloppe.Error()

	if n := strings.Count(message, chemin); n != 1 {
		t.Errorf("le chemin apparaît %d fois, attendu 1 ; message : %s", n, message)
	}
	for _, anglais := range []string{"no such file or directory", "open "} {
		if strings.Contains(message, anglais) {
			t.Errorf("reste d'anglais %q dans le message : %s", anglais, message)
		}
	}
	// La chaîne d'erreurs doit survivre : plusieurs appelants font
	// errors.Is(err, fs.ErrNotExist) sur ces erreurs-là (internal/nest,
	// internal/spawn), et c'est ce qui rend le type nécessaire plutôt qu'un
	// simple %v sur l'erreur nue.
	if !errors.Is(enveloppe, fs.ErrNotExist) {
		t.Errorf("fs.ErrNotExist doit rester repérable dans la chaîne ; err = %v", enveloppe)
	}
}

// Les motifs traduits, et le repli. Un message anglais EXACT vaut mieux qu'un
// message français faux — même politique que traduitErreurDeFlag
// (internal/cli/francais.go) : ce qui n'est pas reconnu remonte tel quel.
func TestErreurFichierTraduitLesMotifsCourantsEtReplieSurLeReste(t *testing.T) {
	cas := []struct {
		nom     string
		err     error
		attendu string
	}{
		{"absent", fs.ErrNotExist, "n'existe pas"},
		{"permission", fs.ErrPermission, "permission refusée"},
		// Non reconnu : le texte d'origine passe tel quel plutôt qu'une
		// traduction inventée.
		{"inconnu", errors.New("motif exotique de l'OS"), "motif exotique de l'OS"},
	}
	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			// Emballé dans un *fs.PathError, comme l'OS le rend vraiment : c'est
			// cette couche-là qui porte le chemin en double.
			err := &ErreurFichier{Err: &fs.PathError{Op: "open", Path: "/un/chemin", Err: c.err}}
			if got := err.Error(); !strings.Contains(got, c.attendu) {
				t.Errorf("Error() = %q, attendu contenir %q", got, c.attendu)
			}
			if strings.Contains(err.Error(), "/un/chemin") {
				t.Errorf("le chemin ne doit pas être répété ; obtenu : %s", err.Error())
			}
		})
	}
}

// Une erreur qui n'est PAS un *fs.PathError doit remonter telle quelle : le
// type est un traducteur de motif, pas un filtre qui avale ce qu'il ne comprend
// pas.
func TestErreurFichierLaisseIntacteUneErreurSansChemin(t *testing.T) {
	nue := errors.New("quelque chose d'autre")
	if got := (&ErreurFichier{Err: nue}).Error(); got != "quelque chose d'autre" {
		t.Errorf("Error() = %q, attendu le message d'origine", got)
	}
}
