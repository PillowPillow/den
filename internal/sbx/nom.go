// Package sbx pilote la CLI `sbx` : nommage des sandboxes, assemblage des
// arguments, exécution derrière une interface mockable.
package sbx

import (
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
