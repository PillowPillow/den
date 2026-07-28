// Package sbx pilote la CLI `sbx` : nommage des sandboxes, assemblage des
// arguments, exécution derrière une interface mockable.
package sbx

import (
	"fmt"
	"strings"

	"github.com/PillowPillow/den/internal/config"
)

// SeparateurNom sépare le nest du worktree dans un nom de sandbox.
//
// `sbx create --name` autorise le point, et den l'interdit dans les noms de nest
// et de worktree : la décomposition est donc EXACTE, sans consulter la liste des
// nests. Avec un « - » comme séparateur, `mon-api-feat` serait ambigu (nest
// `mon-api`+wt `feat`, ou nest `mon`+wt `api-feat`) et il faudrait un
// plus-long-préfixe contre les nests déclarés — une sandbox deviendrait
// inattribuable dès la suppression de son nest.
const SeparateurNom = "."

// NomSandbox construit le nom de sandbox d'un nest, éventuellement worktreeé.
// Ce nom est l'unique porteur d'état de den : `--label` n'existe pas dans sbx.
func NomSandbox(nest, worktree string) (string, error) {
	if err := config.ValiderComposantSandbox("nest", nest); err != nil {
		return "", err
	}
	if worktree == "" {
		return nest, nil
	}
	if err := config.ValiderComposantSandbox("worktree", worktree); err != nil {
		return "", err
	}
	return nest + SeparateurNom + worktree, nil
}

// DecomposeNom est l'inverse de NomSandbox. Fonction TOTALE : elle ne valide
// rien et n'échoue jamais, parce qu'elle s'applique aussi aux sandboxes créées
// hors den que `sbx ls` remonte. Un nom sans séparateur est un nest sans
// worktree.
func DecomposeNom(nom string) (nest, worktree string) {
	nest, worktree, _ = strings.Cut(nom, SeparateurNom)
	return nest, worktree
}

// ValiderNomSandbox contrôle qu'un nom est bien celui que den aurait construit.
//
// Source UNIQUE du verdict, exportée et consommée par tous ceux qui
// transforment un nom en chemin hôte ou en argument de `sbx` : la validation
// composant par composant a existé en double, et les deux copies ont divergé.
//
// Elle procède par aller-retour à travers le constructeur validant plutôt que
// de redéfinir un charset — config.ValiderComposantSandbox en reste la seule
// source — puis compare le nom reconstruit à l'original. C'est cette dernière
// comparaison qui attrape ce que la validation par composant laisse passer :
// « api. » se décompose en « api » + worktree vide, deux composants valides, et
// se reconstruirait en « api ». sbx accepterait ce nom, et `sbx ls` le
// redécomposerait en « api » : deux noms pour une même sandbox.
func ValiderNomSandbox(nom string) error {
	nest, worktree := DecomposeNom(nom)
	reconstruit, err := NomSandbox(nest, worktree)
	if err != nil {
		return err
	}
	if reconstruit != nom {
		return fmt.Errorf("nom de sandbox %q : forme non canonique (se reconstruit en %q)", nom, reconstruit)
	}
	return nil
}
