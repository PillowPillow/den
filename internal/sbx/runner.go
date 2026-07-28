package sbx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
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
// résultat, pas seulement de l'afficher :
//   - errors.As pour retrouver le *exec.ExitError sous-jacent et son code de
//     sortie (create échoué, il faut dire pourquoi) ;
//   - errors.Is(err, exec.ErrNotFound) pour distinguer « sbx absent du PATH »
//     d'un échec applicatif quelconque (doctor.go fait déjà cette distinction
//     via LookPath ; le runner doit pouvoir la faire aussi) ;
//   - errors.Is(err, context.Canceled) pour reconnaître un Ctrl-C pendant
//     l'exécution plutôt que de le confondre avec un échec sbx.
//
// D'où Unwrap : la chaîne d'erreurs de cmd.Run() doit survivre intacte, en
// plus du message qui, lui, intègre stderr pour rester lisible par un humain.
type ErreurExec struct {
	Bin    string
	Args   []string
	Stderr string
	Err    error
}

func (e *ErreurExec) Error() string {
	detail := e.Stderr
	if detail == "" {
		detail = e.Err.Error()
	}
	return fmt.Sprintf("%s %s : %s", e.Bin, strings.Join(e.Args, " "), detail)
}

func (e *ErreurExec) Unwrap() error { return e.Err }

// Run exécute sbx et renvoie stdout. En cas d'échec, stderr est INTÉGRÉ au
// message : sbx y met le diagnostic utile, et une erreur « exit status 1 » nue
// est inexploitable pour l'utilisateur comme pour le mainteneur. L'erreur
// d'origine (code de sortie, exec.ErrNotFound, context.Canceled…) reste
// accessible via errors.As/errors.Is grâce à ErreurExec.Unwrap — voir son
// commentaire.
func (e *Exec) Run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, e.Bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), &ErreurExec{
			Bin:    e.Bin,
			Args:   args,
			Stderr: strings.TrimSpace(stderr.String()),
			Err:    err,
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
	// cmd.Cancel = func() error { return nil } NE SUFFIT PAS : d'après la doc
	// de os/exec, Wait renvoie quand même une erreur (dérivée du contexte) dès
	// que Cancel a été appelé, même s'il ne renvoie aucune erreur lui-même et
	// même si le process se termine ensuite avec succès. Seul Cancel = nil
	// laisse le process — et son code de sortie réel — totalement inaffecté
	// par le contexte (vérifié empiriquement, pas seulement lu dans la doc).
	cmd.Cancel = nil
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s : %w", e.Bin, strings.Join(args, " "), err)
	}
	return nil
}
