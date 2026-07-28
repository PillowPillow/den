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

// Run exécute sbx et renvoie stdout. En cas d'échec, stderr est INTÉGRÉ au
// message : sbx y met le diagnostic utile, et une erreur « exit status 1 » nue
// est inexploitable pour l'utilisateur comme pour le mainteneur.
func (e *Exec) Run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, e.Bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return stdout.Bytes(), fmt.Errorf("%s %s : %s", e.Bin, strings.Join(args, " "), detail)
	}
	return stdout.Bytes(), nil
}

// Attach donne la main à sbx sur les tty du processus courant.
func (e *Exec) Attach(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, e.Bin, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s : %w", e.Bin, strings.Join(args, " "), err)
	}
	return nil
}
