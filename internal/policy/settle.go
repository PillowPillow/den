// Package policy attend que la policy réseau d'une sandbox soit effectivement
// posée, avant que den n'y attache un shell.
package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/PillowPillow/den/internal/sbx"
)

// Options paramètre la boucle. Sommeil et Maintenant sont injectés pour que les
// tests n'attendent pas réellement.
//
// Aucun champ n'a de valeur par défaut implicite : des Options incomplètes sont
// refusées par Settle, pas complétées en douce. Voir valide().
type Options struct {
	Timeout    time.Duration
	Intervalle time.Duration
	Sommeil    func(time.Duration)
	Maintenant func() time.Time
}

// OptionsDefaut : 60 s de patience, un sondage toutes les 2 s. La propagation
// observée aux spikes se compte en secondes, jamais en minutes ; 60 s laisse une
// marge large sans transformer un vrai blocage en attente interminable.
func OptionsDefaut() Options {
	return Options{
		Timeout:    60 * time.Second,
		Intervalle: 2 * time.Second,
		Sommeil:    time.Sleep,
		Maintenant: time.Now,
	}
}

// valide refuse des Options incomplètes au lieu d'y suppléer.
//
// Suppléer serait pire que le refus. Un Sommeil ou un Maintenant nil panique,
// donc se voit ; mais un Timeout à zéro rendrait la boucle sans aucune
// patience, et un Intervalle à zéro la ferait marteler sbx sans répit. Dans les
// deux cas la seule garde réseau de den continuerait de dire oui sans plus rien
// garder — un « ça marche à moitié » silencieux, exactement ce que Settle
// existe pour empêcher. Les compléter en secret par les valeurs de
// OptionsDefaut() masquerait tout autant le bug de l'appelant.
func (o Options) valide() error {
	var fautifs []string
	if o.Timeout <= 0 {
		fautifs = append(fautifs, fmt.Sprintf("Timeout (%s)", o.Timeout))
	}
	if o.Intervalle <= 0 {
		fautifs = append(fautifs, fmt.Sprintf("Intervalle (%s)", o.Intervalle))
	}
	if o.Sommeil == nil {
		fautifs = append(fautifs, "Sommeil (nil)")
	}
	if o.Maintenant == nil {
		fautifs = append(fautifs, "Maintenant (nil)")
	}
	if len(fautifs) == 0 {
		return nil
	}
	return fmt.Errorf(
		"options de settle inutilisables : %s — construis-les à partir de "+
			"policy.OptionsDefaut() et ne surcharge que ce qui doit l'être",
		strings.Join(fautifs, ", "))
}

// Settle boucle jusqu'à ce que TOUS les hôtes soient autorisés dans le contexte
// de cette sandbox, ou jusqu'au timeout.
//
// Fail-closed (spec §7) : si un hôte ne passe pas, den n'attache pas. Une
// sandbox qui démarre à moitié — agent sans accès à api.anthropic.com, install
// qui échoue à mi-parcours — coûte plus cher à diagnostiquer qu'un refus net.
//
// Le scope --sandbox est essentiel : l'allowlist est posée en caps.network.allow
// d'un mixin auto-scopé à la sandbox. Interroger la policy GLOBALE validerait
// autre chose que ce qu'on vient de poser.
func Settle(ctx context.Context, r sbx.Runner, sandbox string, hotes []string, o Options) error {
	// Avant le raccourci sur une allowlist vide : des Options cassées le
	// restent au prochain appel, avec des hôtes cette fois. Mieux vaut que
	// l'appelant l'apprenne au premier passage.
	if err := o.valide(); err != nil {
		return fmt.Errorf("sandbox %s : %w", sandbox, err)
	}
	if len(hotes) == 0 {
		return nil
	}

	restants := slices.Clone(hotes)
	limite := o.Maintenant().Add(o.Timeout)

	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("sandbox %s : attente de la policy interrompue : %w", sandbox, err)
		}

		// Seuls les hôtes ENCORE bloqués sont resondés : une allowlist longue
		// dont un seul hôte traîne ne doit pas rejouer toute la liste à chaque
		// tour, et un hôte déjà autorisé n'a pas à revenir dans le message.
		var encoreBloques []string
		for _, h := range restants {
			ok, err := hoteAutorise(ctx, r, sandbox, h)
			if err != nil {
				return err
			}
			if !ok {
				encoreBloques = append(encoreBloques, h)
			}
		}
		restants = encoreBloques

		if len(restants) == 0 {
			return nil
		}
		// Le message du timeout est fabriqué ici, à partir des seuls hôtes
		// restants : propager l'erreur d'un runner y réinjecterait l'argv
		// complet (sbx.ErreurExec), donc des hôtes déjà passés.
		if !o.Maintenant().Before(limite) {
			slices.Sort(restants) // déterminisme du message
			return fmt.Errorf(
				"sandbox %s : la policy réseau n'autorise toujours pas %d hôte(s) après %s — "+
					"den n'attache pas (fail-closed). Hôtes bloqués : %s. "+
					"Vérifie l'allowlist du nest et de la stack, puis "+
					"`sbx policy check network --sandbox %s --verbose <hôte>`",
				sandbox, len(restants), o.Timeout, strings.Join(restants, ", "), sandbox)
		}
		o.Sommeil(o.Intervalle)
	}
}

// hoteAutorise interroge la policy pour UN hôte, dans le contexte de la sandbox.
func hoteAutorise(ctx context.Context, r sbx.Runner, sandbox, hote string) (bool, error) {
	sortie, err := r.Run(ctx, "policy", "check", "network", "--sandbox", sandbox, "--json", hote)
	if err != nil {
		return false, fmt.Errorf("sandbox %s : vérification de %s : %w", sandbox, hote, err)
	}

	// Allowed est un POINTEUR : un champ absent doit se distinguer d'un `false`.
	// Confondre les deux ferait tourner la boucle jusqu'au timeout en accusant
	// le réseau, alors que la cause serait un changement de schéma côté sbx.
	var doc struct {
		Allowed *bool `json:"allowed"`
	}
	if err := json.Unmarshal(sortie, &doc); err != nil {
		return false, fmt.Errorf(
			"sandbox %s : sortie de `sbx policy check network` illisible (%w) : %s",
			sandbox, err, string(sortie))
	}
	if doc.Allowed == nil {
		return false, fmt.Errorf(
			"sandbox %s : la sortie de `sbx policy check network` ne porte pas de champ "+
				"\"allowed\" — le schéma de sbx a probablement changé : %s",
			sandbox, string(sortie))
	}
	return *doc.Allowed, nil
}
