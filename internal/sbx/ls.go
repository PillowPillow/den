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

// StatutEnMarche est la valeur de `status` d'une sandbox qui tourne.
// Relevée le 2026-07-28 sur le schéma de sbx v0.35.0.
const StatutEnMarche = "running"

// StatutArretee est la valeur de `status` d'une sandbox arrêtée — l'état où sbx
// range TOUT SEUL les sandboxes inactives, au bout de quelques minutes (constaté
// au smoke du 2026-07-29).
//
// Elle accepte un `sbx exec`, qui la redémarre au passage. C'est donc un état de
// repos, pas une panne.
const StatutArretee = "stopped"

// Trouve rend la sandbox de ce nom parmi boxes, ou nil.
//
// Le POINTEUR, et pas un booléen : c'est la sandbox trouvée qui porte le statut
// réel et les workspaces que la VM monte vraiment. Une version antérieure
// réduisait cette recherche à un `Existe` booléen, et jetait les deux — d'où
// l'attache dans une VM arrêtée et le `-w` recalculé depuis une configuration
// que la VM ne monte pas.
//
// Le nom est comparé ENTIER : « api » et « api.feat12 » sont deux sandboxes,
// jamais l'une le préfixe de l'autre.
func Trouve(boxes []Sandbox, nom string) *Sandbox {
	for i := range boxes {
		if boxes[i].Nom == nom {
			return &boxes[i]
		}
	}
	return nil
}

// EstArretee dit si la sandbox est au repos — attachable, mais au prix d'un
// redémarrage que l'appelant a intérêt à annoncer : il prend plusieurs secondes,
// et un `den <nest>` muet pendant ce temps-là ressemble à un gel.
//
// Séparée de VerifieAttachable À DESSEIN : « faut-il prévenir ? » et « faut-il
// refuser ? » sont deux questions, et les confondre est exactement ce qui a
// produit le refus que le smoke du 2026-07-29 a invalidé.
func (s Sandbox) EstArretee() bool { return s.Statut == StatutArretee }

// VerifieAttachable refuse une sandbox dans laquelle den ne doit pas ouvrir de
// shell. C'est la garde partagée par `den <nest>` (spawn-or-attach) et
// `den sh` : les deux finissent par un `sbx exec`.
//
// DEUX statuts passent, et le second est le correctif du smoke du 2026-07-29 :
//
//   - « running » : rien à dire ;
//   - « stopped » : `sbx exec` redémarre la sandbox de façon transparente
//     (« Sandbox <nom> started successfully »). Refuser était plus strict que
//     sbx lui-même, et le remède qu'on affichait — `den rm` — DÉTRUISAIT un état
//     qui aurait survécu : mesuré, un fichier écrit dans la couche conteneur est
//     toujours là après stop/exec. Avec l'arrêt automatique des sandboxes
//     inactives, ce cas est la NORME au retour sur une VM `--detach`, pas
//     l'exception.
//
// La réserve qui justifiait le refus est levée, et elle l'est par mesure : les
// startup commands des kits SE REJOUENT à la reprise. Compteur de
// « dispatcher run » dans /var/log/sbx-kit-startup.log passé de 2 à 3 après un
// stop puis un exec, avec la chaîne complète des kits dans l'ordre de l'argv et
// le mixin de den en dernier. Une VM reprise n'est donc pas fonctionnellement
// incomplète — la config git globale et la commande de fraîcheur de l'agent y
// repassent.
//
// LISTE BLANCHE quand même : tout ce qui n'est ni l'un ni l'autre est refusé.
// Une liste noire laisserait passer n'importe quel statut qu'une version
// ultérieure de sbx introduirait — y compris un statut d'erreur — et den n'a
// aucun moyen de connaître cette liste ici. Le prix ASSUMÉ : un éventuel statut
// transitoire de démarrage ferait échouer un `den <nest>` lancé trop tôt. Le
// message rend le statut lu, ce qui rend le cas diagnosticable et rapportable.
//
// La remédiation ne nomme que des commandes ATTESTÉES, et l'attestation a deux
// sources qui ne se valent pas :
//
//   - les sous-commandes `sbx` viennent du relevé du 2026-07-28, versé au spec
//     §14.0 (`sbx ls`, `sbx rm --force`). `sbx start` n'y apparaît pas et
//     personne ne peut le falsifier ici, sbx n'étant pas installable sur cette
//     machine : suggérer une commande peut-être inexistante serait pire qu'un
//     commentaire faux ;
//   - `den rm` est une commande de DEN, attestée par la source du dépôt
//     (internal/cli/rm.go). L'argument ci-dessus ne s'y applique pas.
//
// Et c'est `den rm` qui passe EN PREMIER, parce qu'il fait strictement plus :
// il détruit la VM comme `sbx rm --force`, mais nettoie en plus les worktrees
// que den a créés pour cette sandbox. Envoyer l'utilisateur directement sur sbx
// lui laisse des worktrees orphelins sous worktree_root, sans rien lui dire.
// `sbx rm --force` reste nommé comme repli : il marche même quand ~/.den est
// cassé, ce dont `den rm` a besoin pour situer les worktrees.
func (s Sandbox) VerifieAttachable() error {
	if s.Statut == StatutEnMarche || s.Statut == StatutArretee {
		return nil
	}
	return fmt.Errorf(
		"sandbox %q : statut lu %q, attendu %q ou %q — den n'attache pas dans une VM dont il ne "+
			"sait rien ; inspecte-la avec `sbx ls`, ou détruis-la puis relance : `den rm %s` "+
			"(qui nettoie aussi les worktrees ; repli `sbx rm --force %s`, qui ne détruit que la VM)",
		s.Nom, s.Statut, StatutEnMarche, StatutArretee, s.Nom, s.Nom)
}

// Ls liste les sandboxes vivantes, triées par nom.
func Ls(ctx context.Context, r Runner) ([]Sandbox, error) {
	sortie, err := r.Run(ctx, "ls", "--json")
	if err != nil {
		// TEL QUEL, sans enveloppe : ErreurExec rend déjà le binaire et son argv
		// COMPLET (« sbx ls --json : … »), et un « sbx ls : » ajouté devant
		// écrivait la sous-commande deux fois sur la première ligne que voit un
		// utilisateur dont sbx n'est pas installé. Ce que l'enveloppe ajoutait
		// est strictement moins précis que ce qu'elle répétait. Tenu par
		// TestLsNeRepetePasLaSousCommande.
		//
		// Les échecs de DÉCODAGE ci-dessous gardent leur préfixe : eux ne sont
		// situés par personne d'autre.
		return nil, err
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
