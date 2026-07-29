// Package config charge et valide le contenu de ~/.den (config.yaml, stacks/).
package config

import (
	"fmt"
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
			// Le message d'os.UserHomeDir (« $HOME is not defined ») est exact
			// mais muet : il ne dit ni ce que den cherchait, ni que deux
			// sorties de secours existent. Le cas se produit pour de bon sous
			// systemd, dans un conteneur ou dans un cron — là, précisément, où
			// personne n'est devant l'écran pour deviner.
			return "", fmt.Errorf(
				"impossible de situer le dossier de configuration de den (~/.den) : %w — "+
					"passe --den-home <dossier>, ou définis DEN_HOME", err)
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
		// Le chemin fautif est nommé : cette fonction expanse les « ~ » des
		// config_dir, ssh.dir et repos, et l'erreur nue ne disait pas lequel de
		// ces champs portait le tilde. Les appelants ajoutent par-dessus l'objet
		// concerné (« agent claude : … »).
		return "", fmt.Errorf("expansion de %q : %w — définis HOME, ou écris un chemin absolu", p, err)
	}
	if p == "~" {
		return h, nil
	}
	return filepath.Join(h, p[2:]), nil
}
