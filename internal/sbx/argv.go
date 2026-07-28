package sbx

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/PillowPillow/den/internal/config"
)

// AgentPositionnel est l'agent passé à `sbx create`.
//
// « shell » et non « claude » : `sbx create [flags] AGENT PATH [PATH...]` exige
// un agent positionnel (l'omettre donne « unknown agent »), mais la commande
// réellement attachée est décidée par le FLAVOR de l'image, pas par cet
// argument — une image snapshotée depuis la base claude lance `claude` quoi
// qu'on écrive ici. den attache de toute façon par `sbx exec … bash -l`, donc
// « shell » est le choix honnête : il ne promet rien qu'il ne tienne.
const AgentPositionnel = "shell"

// Create décrit un `sbx create` à assembler.
//
// KitMixin est un champ SÉPARÉ de KitsStack, et non le dernier élément d'une
// liste unique : le mixin est fail-closed et le dispatcher sbx fait `exit $rc`
// au premier échec, ce qui prive les kits suivants de leurs startup commands.
// Il DOIT être layeré en dernier. Deux champs rendent l'inversion impossible ;
// une liste unique ne ferait que la rendre improbable.
type Create struct {
	Nom       string   // nom de sandbox, cf. NomSandbox
	Image     string   // passée verbatim à --template
	KitsStack []string // kits transverses puis kit de la stack, ordre de layering
	KitMixin  string   // dossier du mixin généré — TOUJOURS le dernier --kit
	// Workspaces : chemins hôte montés, dans l'ordre. Le PREMIER doit être le
	// repo (ou son worktree) : Sandbox.Workdir en dépend pour l'attache.
	// Suffixe « :ro » accepté par sbx.
	Workspaces []string
}

// ArgvCreate assemble l'argv complet de `sbx create`, sans le nom du binaire.
func ArgvCreate(c Create) ([]string, error) {
	// Le nom porte le séparateur : on valide chaque composant, pas le tout.
	nest, worktree := DecomposeNom(c.Nom)
	if err := config.ValiderComposantSandbox("nom de sandbox", nest); err != nil {
		return nil, err
	}
	if worktree != "" {
		if err := config.ValiderComposantSandbox("nom de sandbox", worktree); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(c.Image) == "" {
		return nil, fmt.Errorf(
			"sandbox %q : aucune image — la stack doit déclarer `image:` dans stack.yaml", c.Nom)
	}
	if strings.TrimSpace(c.KitMixin) == "" {
		return nil, fmt.Errorf(
			"sandbox %q : mixin généré manquant — il porte l'egress, l'env et la commande "+
				"de fraîcheur, un create sans lui produirait une VM muette", c.Nom)
	}
	if len(c.Workspaces) == 0 {
		return nil, fmt.Errorf(
			"sandbox %q : aucun workspace à monter — `sbx create` exige au moins un chemin", c.Nom)
	}
	for i, w := range c.Workspaces {
		if err := valideWorkspace(c.Nom, i, w); err != nil {
			return nil, err
		}
	}

	argv := []string{"create", "--name", c.Nom, "--template", c.Image}
	for _, k := range c.KitsStack {
		if k == "" {
			continue
		}
		argv = append(argv, "--kit", k)
	}
	argv = append(argv, "--kit", c.KitMixin) // toujours en dernier, cf. doc du type
	argv = append(argv, AgentPositionnel)
	argv = append(argv, c.Workspaces...)
	return argv, nil
}

// valideWorkspace garde une entrée de Workspaces, désignée par sa position
// (indice 0) dans la liste.
//
// La garde est ici et non chez l'appelant parce que c'est ArgvCreate qui
// transforme ces valeurs en ligne de commande : un chemin relatif se
// résoudrait contre un répertoire courant que rien ne garantit au moment où
// sbx l'utilise, et un chemin commençant par « - » serait lu comme un flag —
// la même classe de panne que le charset des noms de sandbox referme un cran
// plus tôt dans l'argv.
func valideWorkspace(nomSandbox string, i int, w string) error {
	if strings.TrimSpace(w) == "" {
		return fmt.Errorf(
			"sandbox %q : workspace n°%d vide — `sbx create` recevrait un argument "+
				"positionnel vide, qui ne monte rien", nomSandbox, i+1)
	}
	// Le « :ro » est une option de montage de sbx, pas une partie du chemin :
	// on le retire pour juger, et il repart verbatim dans l'argv.
	chemin := strings.TrimSuffix(w, ":ro")
	if !filepath.IsAbs(chemin) {
		return fmt.Errorf(
			"sandbox %q : workspace n°%d (%q) n'est pas un chemin absolu — un chemin relatif "+
				"se résoudrait contre un répertoire courant qui n'est plus garanti au moment "+
				"où sbx l'utilise, et un chemin commençant par « - » serait lu comme un flag",
			nomSandbox, i+1, w)
	}
	return nil
}
