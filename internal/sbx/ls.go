package sbx

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Sandbox est une sandbox telle que `sbx ls --json` la décrit.
//
// Le schéma est celui de sbx v0.35.0, relevé le 2026-07-28 :
//
//	{"sandboxes":[{"name","id","agent","status","workspaces":["/p","/p:ro"]}]}
//
// Il n'y a AUCUN champ de date : l'âge d'une sandbox n'est pas calculable, et
// la colonne « âge » du spec §5 a été retirée en conséquence.
//
// Le décodage est volontairement tolérant aux champs inconnus (contrairement au
// YAML de configuration, strict) : cette sortie vient d'un outil tiers, pas de
// l'utilisateur. Un champ ajouté par une version ultérieure de sbx ne doit pas
// casser `den ls`.
type Sandbox struct {
	Nom        string   `json:"name"`
	ID         string   `json:"id"`
	Agent      string   `json:"agent"`
	Statut     string   `json:"status"`
	Workspaces []string `json:"workspaces"`
}

// Nest est le nest d'origine, déduit du nom — sbx n'ayant pas de labels, le nom
// est le seul porteur d'état.
func (s Sandbox) Nest() string {
	nest, _ := DecomposeNom(s.Nom)
	return nest
}

// Worktree est le worktree d'origine, vide si la sandbox n'en porte pas.
func (s Sandbox) Worktree() string {
	_, wt := DecomposeNom(s.Nom)
	return wt
}

// Workdir est le répertoire de travail naturel : le PREMIER workspace, qui est
// par construction le premier repo (ou son worktree) — den les monte avant le
// profil agent. Le suffixe « :ro » est retiré : c'est une option de mount, pas
// une partie du chemin.
func (s Sandbox) Workdir() string {
	if len(s.Workspaces) == 0 {
		return ""
	}
	return strings.TrimSuffix(s.Workspaces[0], ":ro")
}

// Ls liste les sandboxes vivantes, triées par nom.
func Ls(ctx context.Context, r Runner) ([]Sandbox, error) {
	sortie, err := r.Run(ctx, "ls", "--json")
	if err != nil {
		return nil, fmt.Errorf("sbx ls : %w", err)
	}

	var doc struct {
		Sandboxes []Sandbox `json:"sandboxes"`
	}
	if err := json.Unmarshal(sortie, &doc); err != nil {
		// La sortie brute est dans le message : sans elle, un changement de
		// schéma côté sbx serait indiagnosticable.
		return nil, fmt.Errorf("sbx ls : sortie JSON illisible (%w) : %s", err, string(sortie))
	}

	sort.Slice(doc.Sandboxes, func(i, j int) bool {
		return doc.Sandboxes[i].Nom < doc.Sandboxes[j].Nom
	})
	return doc.Sandboxes, nil
}

// Existe indique si une sandbox de ce nom tourne déjà. C'est ce qui fait du
// spawn un « spawn-or-attach » (spec §6.6) : un nom déjà vivant n'est pas une
// erreur.
func Existe(ctx context.Context, r Runner, nom string) (bool, error) {
	boxes, err := Ls(ctx, r)
	if err != nil {
		return false, err
	}
	for _, b := range boxes {
		if b.Nom == nom {
			return true, nil
		}
	}
	return false, nil
}
