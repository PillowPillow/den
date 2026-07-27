// Package config charge et valide le contenu de ~/.den (config.yaml, stacks/).
package config

import (
	"os"
	"path/filepath"
	"strings"
)

// Home résout le dossier de config den. Priorité : flag > $DEN_HOME > ~/.den.
// C'est ce point d'indirection qui rend tout le socle testable sur des dossiers temporaires.
//
// Le résultat est TOUJOURS absolu : worktree_root en dérive, et ce chemin part
// ensuite vers `git worktree` et `sbx create`, où le cwd n'est plus garanti.
func Home(flagValue string) (string, error) {
	brut := flagValue
	if brut == "" {
		brut = os.Getenv("DEN_HOME")
	}
	if brut == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		brut = filepath.Join(h, ".den")
	}
	return filepath.Abs(brut)
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
