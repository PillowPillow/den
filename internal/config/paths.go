// Package config charge et valide le contenu de ~/.den (config.yaml, stacks/).
package config

import (
	"os"
	"path/filepath"
	"strings"
)

// Home résout le dossier de config den. Priorité : flag > $DEN_HOME > ~/.den.
// C'est ce point d'indirection qui rend tout le socle testable sur des dossiers temporaires.
func Home(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if env := os.Getenv("DEN_HOME"); env != "" {
		return env, nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".den"), nil
}

// ExpandPath expanse un « ~ » en tête de chemin. Volontairement minimaliste :
// ni $VAR ni ~user. Les $HOME présents dans bin_dirs visent le home DE LA VM et
// doivent traverser den intacts (cf. spec §9.1).
func ExpandPath(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if p == "~" {
		return h, nil
	}
	return filepath.Join(h, p[2:]), nil
}
