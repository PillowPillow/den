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
