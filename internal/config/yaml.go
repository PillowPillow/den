package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"

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
		return fmt.Errorf("%s : YAML invalide : %w", chemin, err)
	}
	return nil
}
