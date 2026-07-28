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

// StatutEnMarche est la valeur de `status` pour laquelle une sandbox accepte un
// `sbx exec`. Relevée le 2026-07-28 sur le schéma de sbx v0.35.0 ; les AUTRES
// valeurs que sbx peut émettre ne sont pas connues, sbx n'étant pas installable
// sur cette machine.
//
// C'est pour ça que les appelants doivent la traiter en LISTE BLANCHE — statut
// différent, on n'attache pas — et non en liste noire {"exited","stopped"} :
// celle-ci attacherait dans tout statut qu'une version ultérieure de sbx
// introduirait, y compris un statut d'erreur.
const StatutEnMarche = "running"

// Ls liste les sandboxes vivantes, triées par nom.
func Ls(ctx context.Context, r Runner) ([]Sandbox, error) {
	sortie, err := r.Run(ctx, "ls", "--json")
	if err != nil {
		return nil, fmt.Errorf("sbx ls : %w", err)
	}

	// Décodage en deux temps, exprès : un objet JSON valide mais SANS la clé
	// "sandboxes" (sbx qui la renommerait) doit être une ERREUR, pas un zéro
	// silencieux. Ls n'a pas de canal de sortie autre que l'erreur pour le
	// signaler, et le silence coûte plus cher ailleurs que sur den ls lui-même :
	// den sh/den rm liraient une liste vide et affirmeraient à l'utilisateur
	// qu'une sandbox bien vivante n'existe pas, au lieu de dire que la lecture a
	// échoué. Même politique que le champ `allowed` de `sbx policy check`
	// (task 11), pour la même raison.
	//
	// HYPOTHÈSE NON VÉRIFIÉE (sbx n'est pas installé sur cette machine) : le cas
	// « zéro sandbox vivante » émet la clé "sandboxes" avec un tableau vide ou
	// nul, jamais son absence totale (`{}` nu). Si un premier smoke test sur une
	// vraie machine montre un `{}` nu quand rien ne tourne, cette garde doit
	// être relâchée.
	var champs map[string]json.RawMessage
	if err := json.Unmarshal(sortie, &champs); err != nil {
		// La sortie brute est dans le message : sans elle, un changement de
		// schéma côté sbx serait indiagnosticable.
		return nil, fmt.Errorf("sbx ls : sortie JSON illisible (%w) : %s", err, string(sortie))
	}
	brut, presente := champs["sandboxes"]
	if !presente {
		return nil, fmt.Errorf("sbx ls : clé %q absente de la sortie JSON : %s", "sandboxes", string(sortie))
	}

	var boxes []Sandbox
	if err := json.Unmarshal(brut, &boxes); err != nil {
		return nil, fmt.Errorf("sbx ls : sortie JSON illisible (%w) : %s", err, string(sortie))
	}

	sort.Slice(boxes, func(i, j int) bool {
		return boxes[i].Nom < boxes[j].Nom
	})
	return boxes, nil
}
