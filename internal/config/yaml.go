package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"

	"gopkg.in/yaml.v3"
)

// DecodeYAMLStrict décode brut dans dest en REFUSANT toute clé inconnue.
//
// Le décodage laxiste est le pire mode de défaillance possible pour den : une
// faute de frappe (`egres:` pour `egress:`) laisse l'allowlist vide sans un mot,
// `doctor` certifie une config « cohérente », et la sandbox n'atteint plus
// api.anthropic.com sans cause visible. Une clé inconnue est donc une erreur.
//
// chemin ne sert qu'au message : il doit nommer le fichier fautif pour rester
// actionnable.
func DecodeYAMLStrict(chemin string, brut []byte, dest any) error {
	dec := yaml.NewDecoder(bytes.NewReader(brut))
	dec.KnownFields(true)
	if err := dec.Decode(dest); err != nil {
		// yaml.v3 signale un document vide par io.EOF. Un fichier de config vide
		// (ou réduit à des commentaires) n'est pas corrompu : c'est une config qui
		// ne déclare rien. On laisse dest à sa valeur zéro — les défauts et la
		// validation diront ensuite ce qui manque, en français et par champ.
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("%s : YAML invalide : %w", chemin, franciseClesInconnues(err))
	}
	// Decode ne lit QU'UN document. Tout ce qui suit un « --- » était donc
	// silencieusement ignoré : même mode de défaillance que la clé inconnue
	// ci-dessus — une configuration qui ne s'applique pas sans un mot — mais à la
	// granularité du document. Mesuré sur un nest de deux documents :
	// `den nest show` rendait rc=0 en n'ayant lu que le premier, le `stack:` et
	// tout un bloc `egress:` du second n'ayant jamais existé pour den.
	//
	// Refuser plutôt que fusionner : den n'a aucun moyen de savoir lequel des
	// documents fait foi, et en choisir un serait exactement le silence qu'on
	// cherche à supprimer.
	//
	// Le second Decode vise un yaml.Node et non dest : il ne s'agit que de
	// SAVOIR s'il reste un document, pas de le décoder — un second document au
	// schéma incompatible doit être signalé comme document en trop, pas comme
	// erreur de type.
	var suivant yaml.Node
	if err := dec.Decode(&suivant); err == nil {
		return fmt.Errorf(
			"%s : le fichier contient plusieurs documents YAML (séparés par « --- ») — "+
				"den n'en lit qu'un, et les suivants seraient ignorés en silence ; "+
				"n'en garde qu'un seul", chemin)
	}
	return nil
}

// motifCleInconnue capture le diagnostic anglais de yaml.v3 pour une clé
// inconnue. Le « line N: » que yaml.v3 place devant n'en fait pas partie :
// c'est lui qui rend le message utile, il doit survivre à la réécriture.
var motifCleInconnue = regexp.MustCompile(`field (\S+) not found in type \S+`)

// franciseClesInconnues réécrit « field egres not found in type config.Global »
// en « clé inconnue "egres" » : le type Go est un détail d'implémentation, en
// anglais, dans une CLI francophone.
//
// Si le motif n'est pas reconnu (YAML malformé, clé contenant un espace…),
// l'erreur passe INTACTE : un message imparfait vaut mieux qu'un message
// mutilé. L'erreur d'origine reste accessible via errors.Unwrap — on réécrit ce
// que l'utilisateur lit, pas ce que le code peut inspecter.
func franciseClesInconnues(err error) error {
	msg := err.Error()
	francise := motifCleInconnue.ReplaceAllString(msg, `clé inconnue "$1"`)
	if francise == msg {
		return err
	}
	return &erreurYAML{msg: francise, origine: err}
}

type erreurYAML struct {
	msg     string
	origine error
}

func (e *erreurYAML) Error() string { return e.msg }
func (e *erreurYAML) Unwrap() error { return e.origine }
