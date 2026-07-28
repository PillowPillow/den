// Package worktree propage un worktree git sur les repos d'un nest. C'est le
// seul module de den qui pilote git ; comme sbx, il le fait derrière une
// interface pour rester substituable.
package worktree

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Git est l'accès à la CLI git, injecté pour rester substituable.
type Git interface {
	Run(ctx context.Context, dir string, args ...string) ([]byte, error)
}

type gitExec struct{}

// NewGit renvoie l'accès réel au git du PATH.
func NewGit() Git { return gitExec{} }

func (gitExec) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return stdout.Bytes(), fmt.Errorf("git %s (dans %s) : %s", strings.Join(args, " "), dir, detail)
	}
	return stdout.Bytes(), nil
}

// Chemin calcule où vit le worktree wt du repo, selon le layout (spec §13.5).
//
//	central  : <root>/<wt>/<repo>     — défaut, tous les worktrees d'un même
//	                                    wt voisins, ce qui rend le co-montage
//	                                    multi-repo lisible
//	per-repo : <repo>/.den/<wt>       — pour qui préfère garder ses worktrees
//	                                    près de leur dépôt
func Chemin(layout, root, wt, cheminRepo string) string {
	if layout == "per-repo" {
		return filepath.Join(cheminRepo, ".den", wt)
	}
	return filepath.Join(root, wt, filepath.Base(cheminRepo))
}

// Assure garantit l'existence du worktree wt pour ce repo et renvoie son
// chemin. Idempotent : si le worktree existe déjà SUR LA BONNE BRANCHE, il est
// laissé tel quel.
//
// Un worktree existant sur une AUTRE branche est une erreur, jamais un checkout
// silencieux : basculer la branche d'un worktree où l'utilisateur travaille
// déplacerait son travail sans qu'il l'ait demandé.
func Assure(ctx context.Context, g Git, layout, root, wt, cheminRepo string) (string, error) {
	if _, err := os.Stat(cheminRepo); err != nil {
		return "", fmt.Errorf("repo introuvable : %s", cheminRepo)
	}

	chemin := Chemin(layout, root, wt, cheminRepo)

	if _, err := os.Stat(chemin); err == nil {
		actuelle, err := brancheCourante(ctx, g, chemin)
		if err != nil {
			return "", fmt.Errorf(
				"%s existe déjà mais n'est pas un worktree git exploitable : %w", chemin, err)
		}
		if actuelle != wt {
			return "", fmt.Errorf(
				"le worktree %s est sur la branche %q, pas %q — choisis un autre nom de worktree "+
					"ou bascule ce dossier sur %q à la main", chemin, actuelle, wt, wt)
		}
		return chemin, nil // déjà en place : idempotent
	}

	if err := os.MkdirAll(filepath.Dir(chemin), 0o755); err != nil {
		return "", fmt.Errorf("création de %s : %w", filepath.Dir(chemin), err)
	}

	// `git worktree add <chemin> <branche>` si la branche existe déjà,
	// `-b <branche>` sinon : git refuse de recréer une branche existante.
	args := []string{"worktree", "add", chemin, wt}
	if !brancheExiste(ctx, g, cheminRepo, wt) {
		args = []string{"worktree", "add", "-b", wt, chemin}
	}
	if _, err := g.Run(ctx, cheminRepo, args...); err != nil {
		return "", fmt.Errorf("création du worktree %q de %s : %w", wt, cheminRepo, err)
	}
	return chemin, nil
}

// Retire supprime un worktree. Refuse si l'arbre est sale et que force est
// faux : perdre du travail non commité serait le pire effet de bord d'un
// `den rm` (spec §14).
func Retire(ctx context.Context, g Git, cheminRepo, cheminWorktree string, force bool) error {
	if _, err := os.Stat(cheminWorktree); os.IsNotExist(err) {
		return nil // déjà retiré : idempotent
	}

	if !force {
		sale, err := estSale(ctx, g, cheminWorktree)
		if err != nil {
			return err
		}
		if sale {
			return fmt.Errorf(
				"le worktree %s contient des modifications non commitées — commite-les, ou relance "+
					"avec --force pour les perdre, ou avec --keep-worktrees pour garder le dossier",
				cheminWorktree)
		}
	}

	args := []string{"worktree", "remove", cheminWorktree}
	if force {
		args = append(args, "--force")
	}
	if _, err := g.Run(ctx, cheminRepo, args...); err != nil {
		return fmt.Errorf("suppression du worktree %s : %w", cheminWorktree, err)
	}
	return nil
}

func brancheCourante(ctx context.Context, g Git, dir string) (string, error) {
	out, err := g.Run(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func brancheExiste(ctx context.Context, g Git, cheminRepo, branche string) bool {
	_, err := g.Run(ctx, cheminRepo, "show-ref", "--verify", "--quiet", "refs/heads/"+branche)
	return err == nil
}

func estSale(ctx context.Context, g Git, dir string) (bool, error) {
	// --porcelain inclut les fichiers non suivis : un brouillon non ajouté est
	// exactement le travail qu'on ne veut pas détruire.
	out, err := g.Run(ctx, dir, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("état de %s : %w", dir, err)
	}
	return strings.TrimSpace(string(out)) != "", nil
}
