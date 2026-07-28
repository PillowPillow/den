package sbx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// Runner est le SEUL point de contact de den avec la CLI sbx. Tout passe par
// là, ce qui rend l'intégralité du reste testable sans microVM — sbx n'est même
// pas installé sur la machine de développement.
//
// Deux méthodes, parce que les deux usages sont irréconciliables :
//   - Run capture stdout pour le parser (`ls --json`, `policy check --json`).
//   - Attach branche les tty du processus courant pour rendre un shell
//     interactif à l'utilisateur (`exec -it … bash -l`) ; il n'y a rien à
//     capturer, et capturer casserait l'interactivité.
type Runner interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
	Attach(ctx context.Context, args ...string) error
}

// Exec est l'implémentation réelle, adossée au binaire sbx du PATH.
type Exec struct {
	Bin string
	// DelaiApresAnnulation borne l'attente de Run après annulation du
	// contexte. Zéro ⇒ delaiApresAnnulationDefaut. Réglable pour que le test
	// de la borne dure des millisecondes plutôt que des secondes.
	DelaiApresAnnulation time.Duration
}

// NewExec construit un Runner réel. bin vide ⇒ « sbx » (résolu via le PATH).
func NewExec(bin string) Runner {
	if bin == "" {
		bin = "sbx"
	}
	return &Exec{Bin: bin}
}

// ErreurExec est l'erreur renvoyée par Run en cas d'échec. Exportée (et pas un
// simple message formaté) parce que policy et spawn ont besoin d'inspecter le
// résultat, pas seulement de l'afficher. Les trois propriétés ci-dessous sont
// tenues par un test chacune dans runner_test.go — aucune n'est déduite de la
// documentation d'os/exec :
//   - errors.As pour retrouver le *exec.ExitError sous-jacent et son code de
//     sortie (create échoué, il faut dire pourquoi) ;
//   - errors.Is(err, exec.ErrNotFound) pour distinguer « sbx absent du PATH »
//     d'un échec applicatif quelconque (doctor.go fait déjà cette distinction
//     via LookPath ; le runner doit pouvoir la faire aussi) ;
//   - errors.Is(err, context.Canceled) — et de même DeadlineExceeded — pour
//     reconnaître un Ctrl-C, que le contexte soit annulé AVANT le démarrage du
//     process ou PENDANT son exécution.
//
// Cette troisième propriété ne s'obtient PAS toute seule, et le champ
// Annulation est ce qui la porte. Mesuré (trois fois, dont ici en Go 1.26) :
// quand le contexte est annulé pendant l'exécution, os/exec tue le process et
// `Cmd.Wait` PRÉFÈRE l'erreur du process à celle du contexte — cmd.Run rend un
// *exec.ExitError « signal: killed » qui n'enveloppe ni Canceled ni
// DeadlineExceeded. Seule l'annulation survenue avant le démarrage remonte
// telle quelle (Cmd.Start rend ctx.Err() sans lancer le process). Run relève
// donc ctx.Err() lui-même et le joint à la chaîne.
//
// D'où Unwrap : la chaîne d'erreurs de cmd.Run() doit survivre intacte — les
// deux premières propriétés en dépendent — en plus du motif d'annulation et du
// message qui, lui, intègre stderr pour rester lisible par un humain.
type ErreurExec struct {
	Bin    string
	Args   []string
	Stderr string
	Err    error
	// Annulation porte le ctx.Err() observé au retour de cmd.Run, nil si le
	// contexte n'y est pour rien. C'est la SEULE source de Canceled /
	// DeadlineExceeded dans la chaîne quand le process a été tué.
	Annulation error
}

func (e *ErreurExec) Error() string {
	detail := e.Stderr
	if detail == "" && e.Err != nil {
		detail = e.Err.Error()
	}
	if e.Annulation != nil {
		// Le motif d'annulation passe AVANT le détail, et ne le remplace pas :
		// « signal: killed » seul se lit comme un crash de sbx, mais le jeter
		// perdrait le stderr que sbx a eu le temps d'écrire.
		return fmt.Sprintf("%s %s : %s : %s",
			e.Bin, strings.Join(e.Args, " "), motifAnnulation(e.Annulation), detail)
	}
	return fmt.Sprintf("%s %s : %s", e.Bin, strings.Join(e.Args, " "), detail)
}

// motifAnnulation traduit les deux motifs d'annulation de la stdlib. Le défaut
// rend l'erreur telle quelle : un contexte peut porter une cause applicative
// (context.WithCancelCause), et l'inventer en français la perdrait.
func motifAnnulation(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "interrompu (délai dépassé)"
	case errors.Is(err, context.Canceled):
		return "interrompu (annulé)"
	default:
		return fmt.Sprintf("interrompu (%v)", err)
	}
}

// Unwrap rend une SLICE (et non l'unique Err d'avant) : errors.Is doit trouver
// le motif d'annulation ET errors.As le *exec.ExitError, qui sont deux erreurs
// distinctes. La slice ne contient jamais de nil, ce que le contrat d'errors
// interdit.
func (e *ErreurExec) Unwrap() []error {
	var chaine []error
	if e.Err != nil {
		chaine = append(chaine, e.Err)
	}
	if e.Annulation != nil {
		chaine = append(chaine, e.Annulation)
	}
	return chaine
}

// delaiApresAnnulationDefaut borne l'attente de Run APRÈS l'annulation du
// contexte, et seulement là : sur le chemin normal, WaitDelay ne se déclenche
// jamais et Run attend sbx aussi longtemps qu'il faut.
//
// Mesuré : tuer le process ne suffit pas à débloquer Run. `sh -c "sleep 5"`
// annulé au bout de 20 ms rend « signal: killed »… au bout de 5 s pleines,
// parce que dash a FORKÉ et que le petit-fils garde le tube stdout ouvert :
// Wait bloque sur la copie tant qu'il vit. `sh -c "exec sleep 5"` et un binaire
// direct, eux, rendent la main en 21 ms. sbx supervisant une microVM, le
// petit-fils qui hérite du tube peut être un démon qui ne meurt JAMAIS — sans
// cette borne, un Ctrl-C laisse den suspendu sans fin.
const delaiApresAnnulationDefaut = 2 * time.Second

// delaiEffectif applique le défaut. Fonction séparée pour que « la valeur nulle
// d'Exec est la valeur SÛRE » soit vérifiable sans faire dormir la suite : la
// borne elle-même se prouve en exécutant un process, le choix du défaut non.
func delaiEffectif(d time.Duration) time.Duration {
	if d <= 0 {
		return delaiApresAnnulationDefaut
	}
	return d
}

// Run exécute sbx et renvoie stdout. En cas d'échec, stderr est INTÉGRÉ au
// message : sbx y met le diagnostic utile, et une erreur « exit status 1 » nue
// est inexploitable pour l'utilisateur comme pour le mainteneur. L'erreur
// d'origine (code de sortie, exec.ErrNotFound) et le motif d'annulation
// éventuel restent accessibles via errors.As/errors.Is grâce à
// ErreurExec.Unwrap — voir son commentaire.
func (e *Exec) Run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, e.Bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Zéro ⇒ défaut, jamais « pas de borne » : la valeur nulle d'Exec (celle
	// que construisent les tests, `&Exec{Bin: "sh"}`) doit être la valeur SÛRE.
	// C'est toute la raison de delaiEffectif : passé tel quel à WaitDelay, 0
	// signifie précisément « attendre indéfiniment ».
	cmd.WaitDelay = delaiEffectif(e.DelaiApresAnnulation)
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), &ErreurExec{
			Bin:    e.Bin,
			Args:   slices.Clone(args),
			Stderr: strings.TrimSpace(stderr.String()),
			Err:    err,
			// ctx.Err() est relevé ICI, au retour de cmd.Run : c'est le seul
			// endroit où l'on sait que l'échec et l'annulation sont concomitants.
			Annulation: ctx.Err(),
		}
	}
	return stdout.Bytes(), nil
}

// Attach donne la main à sbx sur les tty du processus courant.
func (e *Exec) Attach(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, e.Bin, args...)
	// Attach est la SEULE méthode de ce fichier où l'annulation du contexte ne
	// doit RIEN faire. Le comportement par défaut de CommandContext est un
	// SIGKILL du process (cmd.Cancel = Process.Kill) : sur un shell interactif,
	// ça laisse le terminal en mode raw, sans flush, sans `exit` propre. Un
	// Ctrl-C tapé DANS le shell de la sandbox est de toute façon délivré par le
	// driver tty au groupe de processus au premier plan, pas relayé via ce
	// contexte — den n'a donc rien à faire quand ce contexte se termine ici.
	//
	// cmd.Cancel = func() error { return nil } NE SUFFIT PAS : dans watchCtx
	// (os/exec), un Cancel qui renvoie nil sans être os.ErrProcessDone déclenche
	// quand même `err = ctx.Err()` après coup, même si le process se termine
	// ensuite avec succès (vérifié empiriquement, pas seulement lu dans la
	// doc). Renvoyer os.ErrProcessDone depuis Cancel obtiendrait le même effet
	// que ci-dessous ; Cancel = nil est retenu parce qu'il l'exprime plus
	// directement, pas parce que ce serait la seule forme qui marche.
	cmd.Cancel = nil
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s : %w", e.Bin, strings.Join(args, " "), err)
	}
	return nil
}
