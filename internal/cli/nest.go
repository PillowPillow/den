package cli

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
	"github.com/spf13/cobra"
)

func newNestCmd(denHome *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "nest",
		Short: "Inspecter les nests déclarés",
	}
	cmd.AddCommand(newNestLsCmd(denHome), newNestShowCmd(denHome))
	return cmd
}

func newNestLsCmd(denHome *string) *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "Liste les nests déclarés",
		Args:  aucunArgument,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}
			nests, casses, err := nest.ListNests(home)
			if err != nil {
				return err
			}
			if len(nests) == 0 && len(casses) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "aucun nest déclaré dans %s/nests\n", home)
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NEST\tSTACK\tREPOS\tPORTS")
			for _, n := range nests {
				base := "auto"
				if n.Ports.Base > 0 {
					base = fmt.Sprint(n.Ports.Base)
				}
				fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", n.Name, n.Stack, len(n.Repos), base)
			}
			if err := w.Flush(); err != nil {
				return err
			}
			avertitDesNestsMasques(cmd, nests)

			if len(casses) > 0 {
				fmt.Fprintln(cmd.OutOrStdout())
				for _, c := range casses {
					fmt.Fprintf(cmd.OutOrStdout(), "! %s : %v\n", c.Nom, c.Err)
				}
				return fmt.Errorf("%d nest(s) illisible(s) sur %d", len(casses), len(nests)+len(casses))
			}
			return nil
		},
	}
}

// avertitDesNestsMasques signale les nests qui portent le nom d'une
// sous-commande. Ceux-là sont déclarés, listés et résolus par `den nest show`,
// mais `den <nom>` lancera TOUJOURS la sous-commande : cobra la trouve avant que
// l'argument n'atteigne le RunE de la racine. Ils ne sont donc jamais
// spawnables, et sans cet avertissement rien ne le dit — den nommerait un objet
// qu'il refuse ensuite d'adresser, le défaut trouvé en T3 avec `-api`.
//
// C'est la contrepartie de la suggestion de `den doctr` : celle-ci aide quand le
// nest N'EXISTE PAS et que le nom ressemble à une commande ; celle-là quand le
// nest existe et que le nom EST une commande. Le choix « la racine est la
// commande de spawn » (spec §11) crée les deux, il faut les tenir tous les deux.
//
// Sur stderr, pour que `den nest ls | …` reste tuyautable. Rien du tout quand il
// n'y a pas de collision : un avertissement permanent ne se lit plus.
//
// La comparaison porte sur les noms ET les alias de root.Commands(), c'est-à-dire
// exactement ce que cobra consulte pour router un argument — et jamais sur une
// liste en dur, qui divergerait au prochain AddCommand. La correspondance est
// EXACTE : un nest « l » n'est pas masqué par `ls` (vérifié, cobra ne fait pas
// de correspondance par préfixe pour router).
func avertitDesNestsMasques(cmd *cobra.Command, nests []*nest.Nest) {
	commandes := map[string]bool{}
	for _, sous := range cmd.Root().Commands() {
		commandes[sous.Name()] = true
		for _, alias := range sous.Aliases {
			commandes[alias] = true
		}
	}
	for _, n := range nests {
		if commandes[n.Name] {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"avertissement : le nest %q est masqué par la sous-commande `den %s` — "+
					"`den %s` lancera la commande, jamais ce nest. Renomme-le pour pouvoir le spawner.\n",
				n.Name, n.Name, n.Name)
		}
	}
}

func newNestShowCmd(denHome *string) *cobra.Command {
	var opts nest.Options
	cmd := &cobra.Command{
		Use:   "show <nest>",
		Short: "Affiche un nest entièrement résolu",
		Args:  exactementUnArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}
			g, err := config.LoadGlobal(home)
			if err != nil {
				return err
			}
			stacks, err := config.LoadStacks(home)
			if err != nil {
				return err
			}
			n, err := nest.LoadNest(home, args[0])
			if err != nil {
				return err
			}
			r, err := nest.Resolve(home, g, stacks, n, opts)
			if err != nil {
				return err
			}
			ecrisResolution(cmd.OutOrStdout(), r)
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.Agent, "agent", "", "agent à utiliser (défaut : defaults.agent)")
	cmd.Flags().StringSliceVar(&opts.Without, "without", nil, "exclure ces repos optionnels")
	cmd.Flags().StringSliceVar(&opts.Only, "only", nil, "ne garder que ces repos optionnels")
	return cmd
}

func ecrisResolution(w io.Writer, r *nest.Resolved) {
	fmt.Fprintf(w, "nest:   %s\n", r.Nest.Name)
	fmt.Fprintf(w, "stack:  %s (image %s)\n", r.Stack.Name, r.Stack.Image)
	fmt.Fprintf(w, "agent:  %s\n", r.AgentName)
	fmt.Fprintf(w, "  config_dir: %s\n", r.AgentConfigDir)
	fmt.Fprintf(w, "  update:     %s\n", r.Agent.Update)
	fmt.Fprintf(w, "ssh:    %s\n", r.SSHMode)
	fmt.Fprintf(w, "worktrees: %s (%s)\n", r.WorktreeRoot, r.WorktreeLayout)

	fmt.Fprintln(w, "repos:")
	for _, repo := range r.Repos {
		statut := "requis"
		if repo.Optional {
			statut = "optionnel"
		}
		fmt.Fprintf(w, "  - %s (%s)\n", repo.Path, statut)
	}

	fmt.Fprintf(w, "egress (%d):\n", len(r.Egress))
	for _, h := range r.Egress {
		fmt.Fprintf(w, "  - %s\n", h)
	}

	if len(r.Env) > 0 {
		fmt.Fprintln(w, "env (résolu):")
		// L'ordre d'itération des maps Go n'est pas déterministe : tout ce qui
		// s'affiche est trié.
		for _, k := range slices.Sorted(maps.Keys(r.Env)) {
			fmt.Fprintf(w, "  %s=%s\n", k, r.Env[k])
		}
	}

	if len(r.Nest.Ports.Publish) > 0 {
		fmt.Fprintln(w, "ports déclarés:")
		for _, p := range r.Nest.Ports.Publish {
			marques := []string{}
			if p.Open {
				marques = append(marques, "open")
			}
			if p.LoopbackLock {
				marques = append(marques, "loopback-locked")
			}
			suffixe := ""
			if len(marques) > 0 {
				suffixe = " [" + strings.Join(marques, ", ") + "]"
			}
			fmt.Fprintf(w, "  - %s -> %d%s\n", p.Name, p.Container, suffixe)
		}
	}
}
