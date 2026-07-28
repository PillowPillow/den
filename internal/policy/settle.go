// Package policy attend que la policy réseau d'une sandbox soit effectivement
// posée, avant que den n'y attache un shell.
package policy

import (
	"bytes"
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

// valide refuse des Options incomplètes ou incohérentes au lieu d'y suppléer.
//
// Suppléer serait pire que le refus. Un Sommeil ou un Maintenant nil panique,
// donc se voit ; mais un Timeout à zéro rendrait la boucle sans aucune
// patience, et un Intervalle à zéro la ferait marteler sbx sans répit. Dans les
// deux cas la seule garde réseau de den continuerait de dire oui sans plus rien
// garder — un « ça marche à moitié » silencieux, exactement ce que Settle
// existe pour empêcher. Les compléter en secret par les valeurs de
// OptionsDefaut() masquerait tout autant le bug de l'appelant.
//
// Elle contrôle aussi une RELATION, pas seulement des valeurs : un Intervalle
// plus grand que le Timeout promet une patience d'une seconde et dort trente.
// Ce qu'elle ne peut structurellement PAS voir, c'est une horloge qui ment ;
// c'est la borne en nombre de tours de Settle qui s'en charge.
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
	if len(fautifs) > 0 {
		return fmt.Errorf(
			"options de settle inutilisables : %s — construis-les à partir de "+
				"policy.OptionsDefaut() et ne surcharge que ce qui doit l'être",
			strings.Join(fautifs, ", "))
	}
	if o.Intervalle > o.Timeout {
		return fmt.Errorf(
			"options de settle inutilisables : Intervalle (%s) dépasse Timeout (%s) — "+
				"la boucle dormirait plus longtemps que la patience annoncée ; "+
				"construis-les à partir de policy.OptionsDefaut()",
			o.Intervalle, o.Timeout)
	}
	return nil
}

// toursMax borne le nombre de tours de la boucle.
//
// C'est une garde ARITHMÉTIQUE, pas temporelle, et c'est tout l'intérêt :
// l'horloge étant injectée, la seule borne temporelle de la boucle est la bonne
// foi de l'appelant. Un double d'horloge qui rend toujours la même date —
// parfaitement accepté par valide(), qui inspecte des valeurs et non un
// comportement — ferait boucler Settle sans fin, et un `go test ./...` se
// bloquerait sans que rien ne désigne ce paquet.
//
// La borne est calée EXACTEMENT sur ce qu'une horloge honnête produit :
// ceil(Timeout/Intervalle) sommeils, donc un tour de plus. Elle ne se déclenche
// donc jamais avant le timeout normal ; si elle se déclenche, l'horloge ment.
func (o Options) toursMax() int {
	tours := o.Timeout / o.Intervalle
	if o.Timeout%o.Intervalle != 0 {
		tours++
	}
	return int(tours) + 1
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
// autre chose que ce qu'on vient de poser — d'où la validation du nom, sans
// laquelle un nom vide partirait tel quel dans l'argv.
func Settle(ctx context.Context, r sbx.Runner, sandbox string, hotes []string, o Options) error {
	// Avant le raccourci sur une allowlist vide : des Options cassées le
	// restent au prochain appel, avec des hôtes cette fois. Mieux vaut que
	// l'appelant l'apprenne au premier passage.
	if err := o.valide(); err != nil {
		return fmt.Errorf("sandbox %s : %w", sandbox, err)
	}
	// ValiderNomSandbox est la source unique du verdict sur un nom (tâche 3) ;
	// redéfinir ici un contrôle « non vide » en ferait une deuxième copie, et
	// dans ce dépôt deux copies d'une même validation ont déjà divergé.
	if err := sbx.ValiderNomSandbox(sandbox); err != nil {
		return fmt.Errorf("attente de la policy réseau : %w", err)
	}
	restants, err := allowlistNettoyee(hotes)
	if err != nil {
		return fmt.Errorf("sandbox %s : allowlist : %w", sandbox, err)
	}
	if len(restants) == 0 {
		return nil
	}

	limite := o.Maintenant().Add(o.Timeout)
	toursMax := o.toursMax()

	for tour := 1; ; tour++ {
		if err := ctx.Err(); err != nil {
			return interrompue(sandbox, err)
		}
		if tour > toursMax {
			return fmt.Errorf(
				"sandbox %s : la boucle d'attente a dépassé %d tours (Timeout %s, Intervalle %s) "+
					"alors que Maintenant() rend toujours %s — l'horloge fournie dans "+
					"policy.Options n'avance pas. C'est un défaut de l'appelant, pas un "+
					"blocage réseau",
				sandbox, toursMax, o.Timeout, o.Intervalle,
				o.Maintenant().Format(time.RFC3339Nano))
		}

		// Seuls les hôtes ENCORE bloqués sont resondés : une allowlist longue
		// dont un seul hôte traîne ne doit pas rejouer toute la liste à chaque
		// tour, et un hôte déjà autorisé n'a pas à revenir dans le message.
		var encoreBloques []string
		for _, h := range restants {
			ok, err := hoteAutorise(ctx, r, sandbox, h)
			if err != nil {
				// Une annulation arrive presque toujours PENDANT une passe, pas
				// entre deux tours : sbx est tué et le runner rend une erreur de
				// transport (« signal: killed ») qui n'enveloppe aucun motif de
				// contexte — mesuré, malgré ce qu'affirme le commentaire de
				// sbx.ErreurExec. On lui substitue donc le motif du contexte, et
				// sans l'hôte : un Ctrl-C n'est pas la faute de l'hôte sondé, et
				// l'erreur du runner en incrusterait l'argv complet.
				if errCtx := ctx.Err(); errCtx != nil {
					return interrompue(sandbox, errCtx)
				}
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
		// restants : un timeout doit parler de TOUT ce qui reste bloqué, pas du
		// dernier échec rencontré, et jamais des hôtes déjà passés.
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

// interrompue : message unique de l'annulation, d'où qu'elle soit constatée. Il
// enveloppe le motif du CONTEXTE et non l'erreur du runner, pour qu'un appelant
// puisse faire errors.Is(err, context.Canceled) — ce que l'erreur du runner ne
// permet pas (mesuré : un processus tué rend « signal: killed », qui n'enveloppe
// ni Canceled ni DeadlineExceeded).
func interrompue(sandbox string, err error) error {
	return fmt.Errorf("sandbox %s : attente de la policy interrompue : %w", sandbox, err)
}

// allowlistNettoyee valide et dédoublonne l'allowlist AVANT la boucle.
//
// Un hôte vide (un « - » sans valeur dans un YAML d'allowlist suffit à le
// produire) partirait en `--json ""` et den pourrait en conclure que tout va
// bien. Un doublon (le même hôte dans le nest et dans la stack) serait sondé
// deux fois par tour et listé deux fois dans le message d'échec.
func allowlistNettoyee(hotes []string) ([]string, error) {
	vus := make(map[string]bool, len(hotes))
	propre := make([]string, 0, len(hotes))
	for i, h := range hotes {
		if strings.TrimSpace(h) == "" {
			return nil, fmt.Errorf(
				"hôte vide en position %d sur %d — un « - » sans valeur dans un YAML "+
					"d'allowlist suffit à le produire", i+1, len(hotes))
		}
		if vus[h] {
			continue
		}
		vus[h] = true
		propre = append(propre, h)
	}
	return propre, nil
}

// hoteAutorise interroge la policy pour UN hôte, dans le contexte de la sandbox.
//
// Le code de sortie de sbx n'est PAS le verdict. Personne ici n'a pu confirmer
// que `sbx policy check` sort en 0 quand un hôte est simplement refusé ; s'il
// sortait en 1, une lecture naïve ferait échouer den dès le premier tour, en
// accusant le premier hôte sondé, et le settle-loop ne servirait plus à rien.
// La sortie est donc lue AVANT de conclure quoi que ce soit de l'erreur.
//
// L'asymétrie qui suit est délibérée : un « non » rendu par une commande qui a
// échoué est cru (on reboucle — c'est le comportement sûr), un « oui » ne l'est
// pas (den n'attache pas). Croire un « oui » sorti d'une invocation ratée —
// stdout tronqué, flag inconnu, sandbox absente — serait le seul chemin par
// lequel ce paquet pourrait ouvrir un shell sur une policy jamais vérifiée.
func hoteAutorise(ctx context.Context, r sbx.Runner, sandbox, hote string) (bool, error) {
	sortie, errRun := r.Run(ctx, "policy", "check", "network", "--sandbox", sandbox, "--json", hote)
	verdict, errLecture := litVerdict(sortie)

	if errRun != nil {
		if verdict != nil && !*verdict {
			return false, nil // refus explicite : c'est un verdict, on reboucle
		}
		return false, fmt.Errorf("sandbox %s : vérification de %s : %w", sandbox, hote, errRun)
	}
	if errLecture != nil {
		return false, fmt.Errorf("sandbox %s : vérification de %s : %w", sandbox, hote, errLecture)
	}
	return *verdict, nil
}

// litVerdict extrait le champ `allowed` de la sortie de sbx.
//
// Allowed est un POINTEUR : un champ absent doit se distinguer d'un `false`.
// Confondre les deux ferait tourner la boucle jusqu'au timeout en accusant le
// réseau, alors que la cause serait un changement de schéma côté sbx.
//
// La lecture se fait au json.Decoder et non au json.Unmarshal : Unmarshal
// refuse tout contenu APRÈS la valeur, si bien qu'une bannière, une ligne de log
// ou du NDJSON derrière le verdict empêcheraient définitivement den d'attacher.
// Le verdict est dans la première valeur ; ce qui suit est ignoré, exprès. Ce
// qui PRÉCÈDE reste en revanche une erreur : on ne va pas chercher un verdict au
// milieu d'un flux qu'on ne comprend pas.
func litVerdict(sortie []byte) (*bool, error) {
	if len(bytes.TrimSpace(sortie)) == 0 {
		return nil, fmt.Errorf(
			"`sbx policy check network` n'a rien écrit sur sa sortie standard (sortie vide) — " +
				"vérifie que le flag --json existe et que le verdict ne part pas sur stderr")
	}

	var doc struct {
		Allowed *bool `json:"allowed"`
	}
	if err := json.NewDecoder(bytes.NewReader(sortie)).Decode(&doc); err != nil {
		// %q, et non %s : une sortie qui n'est pas du JSON peut n'avoir aucun
		// caractère visible, ou être pleine de caractères de contrôle. Un
		// message qui se termine par « : » et rien ne laisse aucune piste.
		return nil, fmt.Errorf(
			"sortie de `sbx policy check network` illisible (%w) : %q", err, string(sortie))
	}
	if doc.Allowed == nil {
		// La sortie brute est montrée telle quelle — le brief l'exige, et c'est
		// la seule chose qui permette de voir ce que sbx rend désormais. Elle
		// est ANNONCÉE, parce qu'un sbx verbeux y cite volontiers d'autres hôtes
		// que celui sondé : l'appelant a préfixé « vérification de <hôte> », et
		// ce qui suit « Sortie brute : » appartient à sbx, pas au verdict de den.
		return nil, fmt.Errorf(
			"la sortie de `sbx policy check network` ne porte pas de champ \"allowed\" — "+
				"le schéma de sbx a probablement changé. Sortie brute : %s", string(sortie))
	}
	return doc.Allowed, nil
}
