// Package cli câble les commandes cobra de den. Aucune logique métier ici :
// tout ce qui se teste vit dans internal/config, internal/nest, internal/doctor.
package cli

import (
	"fmt"

	"github.com/PillowPillow/den/internal/doctor"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/spawn"
	"github.com/spf13/cobra"
)

// Version est injectée au build (-ldflags "-X .../internal/cli.Version=...").
var Version = "dev"

// Deps regroupe TOUS les accès système que la racine cobra distribue à ses
// sous-commandes. Une construction par test permet de remplacer chaque accès
// individuellement (doctor, sbx, git, policy) sans qu'aucun ne retombe
// implicitement sur le monde réel : la version précédente de ce paquet
// laissait Git et Policy câblés en dur sur spawn.DepsSysteme() à l'intérieur
// de NewRootCmdAvec, avec un commentaire qui AFFIRMAIT qu'aucun test ne les
// atteignait — vrai par accident (le seul repo de la fixture de test
// n'existe pas sur disque), pas par construction. Un futur test de spawn
// passant par NewRootCmdAvec avec un repo réel aurait silencieusement
// atteint git réel. D'où ce champ : plus aucune garantie non vérifiable, la
// garantie est maintenant que l'appelant contrôle explicitement CE QU'IL
// FOURNIT.
type Deps struct {
	Doctor doctor.Deps
	Sbx    sbx.Runner
	Spawn  spawn.Deps
}

// DepsSysteme branche tous les accès système réels : sbx du PATH, git réel,
// et la patience par défaut du settle-loop de policy.
func DepsSysteme() Deps {
	return Deps{
		Doctor: doctor.DepsSysteme(),
		Sbx:    sbx.NewExec(""),
		Spawn:  spawn.DepsSysteme(),
	}
}

// NewRootCmd construit un arbre de commandes neuf avec les accès système
// réels. Renvoyer une nouvelle instance à chaque appel (plutôt qu'un
// singleton) est ce qui rend les commandes testables.
func NewRootCmd() *cobra.Command {
	return NewRootCmdAvec(DepsSysteme())
}

// NewRootCmdAvec prend ses accès au monde en paramètre : c'est ce qui permet
// aux tests d'exercer `den ls` et `den <nest>` sans que sbx (ni git) soit
// installé — CHAQUE accès système est fourni par l'appelant, aucun n'est
// câblé en dur ici.
//
// denHome est déclaré ICI, pas au niveau du paquet : deux arbres de commandes
// construits dans le même processus doivent pouvoir porter deux --den-home
// différents. Les sous-commandes en reçoivent l'adresse, la valeur n'étant
// remplie qu'au parsing des flags.
func NewRootCmdAvec(deps Deps) *cobra.Command {
	var denHome string

	root := &cobra.Command{
		Use:           "den",
		Short:         "Sandboxes sbx simples et répétables",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&denHome, "den-home", "",
		"dossier de config den (défaut : $DEN_HOME ou ~/.den)")

	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Affiche la version de den",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "den %s\n", Version)
			return nil
		},
	})
	root.AddCommand(newNestCmd(&denHome))
	root.AddCommand(newDoctorCmd(&denHome, deps.Doctor))
	root.AddCommand(newLsCmd(&denHome, deps.Sbx))

	// deps.Spawn.Sbx est ÉCRASÉ par deps.Sbx : c'est ce qui garantit que
	// `den ls` et le settle-loop du spawn parlent au même sbx.Runner — un
	// Fake scripté sur `ls --json` répond de façon cohérente qu'on passe par
	// `den ls` ou par `den <nest>`. deps.Spawn.Git et deps.Spawn.Policy, eux,
	// restent EXACTEMENT ce que l'appelant a fourni dans deps.Spawn : rien
	// n'est forcé au réel ici — DepsSysteme() les branche sur git/policy
	// réels, mais un test qui veut les isoler les fournit lui-même.
	spawnDeps := deps.Spawn
	spawnDeps.Sbx = deps.Sbx
	// En DERNIER : configureSpawn pose Args sur la racine, ce qui n'a de sens
	// qu'une fois les sous-commandes enregistrées.
	configureSpawn(root, &denHome, spawnDeps)
	return root
}

// Execute est le point d'entrée appelé par main.
func Execute() error {
	return NewRootCmd().Execute()
}
