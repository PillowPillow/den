// Package spawn orchestre la séquence complète de `den <nest>` (spec §6).
//
// Il vit hors de internal/cli à dessein : c'est la logique la plus dense du
// projet, et elle doit être testable sans cobra ni tty.
package spawn

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/PillowPillow/den/internal/agent"
	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
	"github.com/PillowPillow/den/internal/policy"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/worktree"
)

// Deps injecte les accès au monde, pour que la séquence entière soit testable
// sans microVM.
type Deps struct {
	Sbx    sbx.Runner
	Git    worktree.Git
	Policy policy.Options
	Sortie io.Writer
}

// DepsSysteme branche la séquence sur le monde réel : le binaire `sbx` du PATH,
// git, et la patience par défaut du settle-loop.
//
// Elle existe pour que le câblage cobra puisse recevoir ses accès en paramètre
// plutôt que de les construire en dur — même raison que doctor.DepsSysteme :
// sans cette injection, le branchement des flags de `den <nest>` sur Options
// n'est vérifiable nulle part, et un test qui atteindrait `sbx create`
// tenterait d'exécuter le vrai binaire.
func DepsSysteme() Deps {
	return Deps{
		Sbx:    sbx.NewExec(""),
		Git:    worktree.NewGit(),
		Policy: policy.OptionsDefaut(),
		Sortie: os.Stdout,
	}
}

// Options porte les flags de `den <nest>`.
type Options struct {
	Nest     string
	Worktree string
	Agent    string
	Without  []string
	Only     []string
	Detach   bool
}

// Spawn exécute la séquence du spec §6, dans l'ordre :
// résolution → sélection repos → worktrees → profil agent → mixin →
// sbx create (ou attache si la sandbox vit déjà) → settle-loop → attache.
//
// L'ordre n'est pas une commodité : le settle-loop PRÉCÈDE l'attache parce
// qu'attacher avant que la policy soit posée produit exactement le « ça marche
// à moitié » que le spec §7 interdit. Symétriquement, tout ce qui peut être
// refusé sur la seule foi de la configuration l'est AVANT le premier effet de
// bord — un worktree créé puis abandonné parce que le nom de sandbox était
// invalide laisserait l'utilisateur nettoyer à la main.
func Spawn(ctx context.Context, denHome string, o Options, d Deps) error {
	// Une Sortie oubliée ne doit pas paniquer au milieu de la séquence :
	// l'appelant fautif a déjà, au premier Fprintf, une sandbox créée et
	// démarrée derrière lui. Perdre le journal coûte moins cher que ça.
	if d.Sortie == nil {
		d.Sortie = io.Discard
	}

	// 1. Résolution de la cascade.
	g, err := config.LoadGlobal(denHome)
	if err != nil {
		return err
	}
	stacks, err := config.LoadStacks(denHome)
	if err != nil {
		return err
	}
	n, err := nest.LoadNest(denHome, o.Nest)
	if err != nil {
		return err
	}
	r, err := nest.Resolve(denHome, g, stacks, n, nest.Options{
		Agent: o.Agent, Without: o.Without, Only: o.Only,
	})
	if err != nil {
		return err
	}

	// Le nom se calcule AVANT tout effet de bord : un worktree non
	// sandboxable (« feature/123 ») doit être refusé sans avoir rien créé.
	nomSandbox, err := sbx.NomSandbox(o.Nest, o.Worktree)
	if err != nil {
		return err
	}

	// 2. Les repos doivent tous exister AVANT le moindre create (spec §11).
	for _, repo := range r.Repos {
		if _, err := os.Stat(repo.Path); err != nil {
			return fmt.Errorf(
				"nest %q : repo introuvable : %s — corrige `repos:` dans %s",
				o.Nest, repo.Path, filepath.Join(denHome, "nests", o.Nest+".yaml"))
		}
	}
	// Même famille de contrôle : un config_dir vide deviendrait un workspace
	// vide, et le message d'un MkdirAll("") ne désignerait rien du tout.
	if r.AgentConfigDir == "" {
		return fmt.Errorf(
			"agent %q : aucun config_dir — déclare `agents.%s.config_dir` dans %s "+
				"(ou `agents.%s` dans le nest) : c'est le profil monté RW dans la sandbox",
			r.AgentName, r.AgentName, filepath.Join(denHome, "config.yaml"), r.AgentName)
	}

	// 3. Worktrees, si demandés. Le premier workspace doit rester le premier
	// repo : sbx.Sandbox.Workdir en dépend pour l'attache, et rien à SON niveau
	// ne peut vérifier que cette liste a bien été composée dans cet ordre.
	workspaces := make([]string, 0, len(r.Repos)+2)
	for _, repo := range r.Repos {
		chemin := repo.Path
		if o.Worktree != "" {
			chemin, err = worktree.Assure(ctx, d.Git, r.WorktreeLayout, r.WorktreeRoot, o.Worktree, repo.Path)
			if err != nil {
				return err
			}
			fmt.Fprintf(d.Sortie, "worktree %s : %s\n", repo.Name(), chemin)
		}
		workspaces = append(workspaces, chemin)
	}

	// 4. Profil agent : monté RW, il doit exister — sinon sbx crée un dossier
	// vide au mount et l'agent repart de zéro à chaque spawn.
	if err := os.MkdirAll(r.AgentConfigDir, 0o755); err != nil {
		return fmt.Errorf("création du profil de l'agent %s (%s) : %w", r.AgentName, r.AgentConfigDir, err)
	}
	workspaces = append(workspaces, r.AgentConfigDir)
	if r.SSHMode == "mount" {
		if r.SSHDir == "" {
			return fmt.Errorf(
				"ssh.mode vaut « mount » mais ssh.dir n'est pas déclaré dans %s",
				filepath.Join(denHome, "config.yaml"))
		}
		workspaces = append(workspaces, r.SSHDir)
	}

	// 5. Mixin généré. r.DenHome et non denHome : Resolve garantit qu'il est
	// absolu, et ce chemin repart tel quel vers `sbx create --kit`, où le cwd
	// n'est plus garanti.
	m, err := agent.MixinDepuis(r, nomSandbox)
	if err != nil {
		return err
	}
	dirMixin, err := agent.EcrisMixin(r.DenHome, nomSandbox, m)
	if err != nil {
		return err
	}

	// 6. Spawn-or-attach : un nom déjà vivant n'est pas une erreur (spec §11).
	vivante, err := sbx.Existe(ctx, d.Sbx, nomSandbox)
	if err != nil {
		return err
	}
	if vivante {
		fmt.Fprintf(d.Sortie, "sandbox %s déjà vivante : attache\n", nomSandbox)
	} else {
		kits := append([]string{}, r.Stack.Kits...)
		if r.Stack.Kit != "" {
			kits = append(kits, r.Stack.Kit)
		}
		argv, err := sbx.ArgvCreate(sbx.Create{
			Nom:        nomSandbox,
			Image:      r.Stack.Image,
			KitsStack:  kits,
			KitMixin:   dirMixin,
			Workspaces: workspaces,
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(d.Sortie, "création de la sandbox %s (image %s)…\n", nomSandbox, r.Stack.Image)
		// Recontextualisé : Exec.Run préfixe déjà son message de l'argv COMPLET
		// — tous les --kit et tous les workspaces sur une seule ligne — où
		// l'étape qui a échoué se perd.
		if _, err := d.Sbx.Run(ctx, argv...); err != nil {
			return fmt.Errorf("création de la sandbox %s : %w", nomSandbox, err)
		}
	}

	// 7. Settle-loop fail-closed AVANT toute attache — y compris sous --detach :
	// une sandbox rendue « prête » sans policy posée est le même demi-démarrage,
	// simplement constaté plus tard.
	if len(r.Egress) > 0 {
		fmt.Fprintf(d.Sortie, "attente de la policy réseau (%d hôte(s))…\n", len(r.Egress))
	}
	if err := policy.Settle(ctx, d.Sbx, nomSandbox, r.Egress, d.Policy); err != nil {
		return err
	}

	// 8. Attache.
	if o.Detach {
		fmt.Fprintf(d.Sortie, "sandbox %s prête (détachée) — `den sh %s` pour y entrer\n",
			nomSandbox, nomSandbox)
		return nil
	}
	return Attache(ctx, d.Sbx, nomSandbox, premier(workspaces))
}

// Attache ouvre un shell interactif dans la sandbox.
//
// `sbx exec` et non `sbx run` : run attache la commande du FLAVOR de l'image
// (souvent `claude`), n'a aucun flag pour la remplacer, et son `-- ARGS` ne fait
// qu'ajouter des arguments.
//
// Le -w reste AVANT le nom de sandbox. `sbx exec [flags] SANDBOX COMMAND
// [ARG...]` : postposé, il serait lu comme un argument de la COMMAND et
// arriverait tel quel à `bash -l`.
func Attache(ctx context.Context, r sbx.Runner, nomSandbox, workdir string) error {
	argv := []string{"exec", "-it"}
	if workdir != "" {
		argv = append(argv, "-w", workdir)
	}
	argv = append(argv, nomSandbox, "bash", "-l")
	return r.Attach(ctx, argv...)
}

func premier(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return filepath.Clean(s[0])
}
