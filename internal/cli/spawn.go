package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
	"github.com/PillowPillow/den/internal/spawn"
	"github.com/spf13/cobra"
)

// configureSpawn fait de la racine elle-même la commande de spawn : `den <nest>`
// n'est pas une sous-commande, c'est l'argument par défaut. cobra retombe sur le
// RunE de la racine quand args[0] ne correspond à aucune sous-commande.
//
// À appeler APRÈS les root.AddCommand : poser Args sur la racine désactive le
// legacyArgs de cobra (« unknown command »), et c'est cette bascule qui rend un
// nom de nest recevable en première position.
//
// deps est pris en paramètre plutôt que construit ici, comme newDoctorCmd :
// c'est ce qui rend vérifiable le branchement des flags sur spawn.Options — un
// flag débranché est silencieux — sans qu'un test tente d'exécuter le vrai `sbx`.
func configureSpawn(root *cobra.Command, denHome *string, deps spawn.Deps) {
	var o spawn.Options

	// Sans « [flags] » : la mention est ajoutée en français par le gabarit
	// d'usage, et DisableFlagsInUseLine empêche cobra d'y remettre le sien.
	root.Use = "den <nest>"
	root.Args = auPlusUnArgument
	// Explicite, parce que cobra ne l'applique PAS sur ce chemin : le défaut de
	// 2 est posé dans findSuggestions(), qui sert la branche « unknown command »
	// — celle que den ne prend jamais, la racine ayant un RunE. Appelé
	// directement, SuggestionsFor lit ce champ tel quel : laissé à 0, il ne
	// retiendrait que les noms exacts et les préfixes, et `den doctr` ne
	// suggérerait rien (mesuré). La valeur est celle de cobra, pas une nôtre.
	root.SuggestionsMinimumDistance = 2
	root.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		o.Nest = args[0]
		home, err := config.Home(*denHome)
		if err != nil {
			return err
		}
		// Copie locale : seule la Sortie est décidée ici, à l'exécution, parce
		// qu'elle seule dépend de la commande (et donc du SetOut des tests).
		d := deps
		d.Sortie = cmd.OutOrStdout()
		return avecSuggestion(root, o.Nest, spawn.Spawn(cmd.Context(), home, o, d))
	}

	root.Flags().StringVarP(&o.Worktree, "worktree", "w", "", "worktree à propager sur tous les repos")
	root.Flags().StringVar(&o.Agent, "agent", "", "agent à utiliser (défaut : defaults.agent)")
	root.Flags().StringSliceVar(&o.Without, "without", nil, "exclure ces repos optionnels")
	root.Flags().StringSliceVar(&o.Only, "only", nil, "ne garder que ces repos optionnels")
	root.Flags().BoolVar(&o.Detach, "detach", false, "ne pas attacher de shell après le spawn")
}

// avecSuggestion ajoute « vouliez-vous dire … ? » à l'échec d'un spawn quand le
// nom demandé n'existe pas comme nest ET ressemble à une sous-commande.
// `den doctr` (faute de frappe pour `doctor`) part en spawn d'un nest « doctr »
// : c'est la contrepartie assumée du choix « la racine EST la commande de
// spawn » (spec §11), et l'erreur seule ne dit rien de la faute de frappe.
//
// La suggestion s'ajoute à l'ÉCHEC DE RÉSOLUTION, et uniquement là. Refuser en
// amont tout argument proche d'une sous-commande — la solution évidente —
// casserait un nest légitimement nommé « doctr » : den le listerait dans
// `den nest ls` puis refuserait de l'adresser, le défaut trouvé en T3 avec
// `-api`. Ici, un nest qui EXISTE ne rencontre jamais ce code.
//
// nest.ErreurNestIntrouvable, et non errors.Is(err, fs.ErrNotExist) : dans un
// den home vide, l'absence de config.yaml est elle aussi un fs.ErrNotExist, et
// l'on collerait une suggestion sur une erreur qui ne parle pas du nest.
//
// Les noms proposés viennent de root.Commands(), via le SuggestionsFor de cobra
// (distance de Levenshtein ≤ SuggestionsMinimumDistance, plus les préfixes) :
// aucune liste en dur, qui divergerait au prochain root.AddCommand.
func avecSuggestion(root *cobra.Command, nom string, err error) error {
	var introuvable *nest.ErreurNestIntrouvable
	if !errors.As(err, &introuvable) {
		return err
	}
	// Le nom rapporté doit être celui que l'utilisateur a tapé : si un jour la
	// séquence de spawn chargeait un AUTRE nest, son absence à lui ne dirait
	// rien d'une faute de frappe sur la ligne de commande.
	if introuvable.Nom != nom {
		return err
	}
	proches := root.SuggestionsFor(nom)
	if len(proches) == 0 {
		return err
	}
	citees := make([]string, len(proches))
	for i, p := range proches {
		citees[i] = fmt.Sprintf("`den %s`", p)
	}
	// Sur une ligne à part : le message d'échec du nest reste lisible tel quel,
	// et la suggestion ne se fait pas passer pour la cause de l'erreur.
	return fmt.Errorf("%w\nvouliez-vous dire %s ? (un premier argument inconnu est lu "+
		"comme un nom de nest, jamais comme une commande)", err, strings.Join(citees, " ou "))
}
