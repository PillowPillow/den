// Package agent résout le profil de l'agent actif et génère le mixin jetable
// layeré au `sbx create` (spec §5, §9).
package agent

import (
	"fmt"
	"strings"

	"github.com/PillowPillow/den/internal/config"
)

// tentativesFraicheur borne les essais de mise à jour. Trois essais espacés de
// 10 s absorbent la propagation NON instantanée de la policy egress (spec §7) :
// sans eux, un simple hoquet réseau au boot avorterait tout le démarrage.
const tentativesFraicheur = 3

// CommandeFraicheur rend l'argv de la commande de mise à jour de l'agent, à
// placer en DERNIÈRE commands.startup du DERNIER kit (spec §9.1).
//
// Deux invariants non négociables :
//
//  1. Les bin_dirs sont injectés LITTÉRALEMENT dans un `export PATH`. Ils
//     contiennent des `$HOME` qui visent le home DE LA VM ; les expanser côté
//     hôte produirait un chemin inexistant dans la microVM. Le dispatcher sbx
//     enveloppe chaque commande dans un `su` NON-login, dont le PATH ne
//     contient pas ~/.local/bin — sans cette ligne, `claude` sort en 127.
//  2. Le script est fail-closed. Le dispatcher fait `exit $rc` au premier
//     échec, ce qui prive les kits SUIVANTS de leurs startup commands : c'est
//     précisément pourquoi ce kit se layere en dernier.
func CommandeFraicheur(nomAgent string, a config.Agent) ([]string, error) {
	if strings.TrimSpace(a.Update) == "" {
		return nil, fmt.Errorf(
			"agent %q : aucune commande update déclarée — une sandbox ne doit jamais démarrer "+
				"avec un agent périmé (spec §9.1)", nomAgent)
	}

	// Le binaire à sonder est le premier mot de la commande update. C'est une
	// convention, pas une déduction : elle est documentée dans le message
	// d'erreur du script pour que le diagnostic reste lisible en VM.
	binaire := strings.Fields(a.Update)[0]

	var b strings.Builder
	b.WriteString("set -uo pipefail\n\n")
	if len(a.BinDirs) > 0 {
		// Guillemets doubles, sans échappement : $HOME doit être expansé par le
		// bash de la VM, pas par den.
		fmt.Fprintf(&b, "# su non-login : PATH minimal, on rétablit les bin_dirs de l'agent.\n")
		fmt.Fprintf(&b, "export PATH=%q\n\n", strings.Join(a.BinDirs, ":")+":$PATH")
	}
	fmt.Fprintf(&b, "if ! command -v %s >/dev/null 2>&1; then\n", binaire)
	fmt.Fprintf(&b, "  echo \"agent %s : FATAL binaire %s introuvable (PATH=$PATH)\" >&2\n", nomAgent, binaire)
	b.WriteString("  exit 127\n")
	b.WriteString("fi\n\n")
	b.WriteString("tentative=1\n")
	fmt.Fprintf(&b, "while [ \"$tentative\" -le %d ]; do\n", tentativesFraicheur)
	fmt.Fprintf(&b, "  if sortie=\"$(%s 2>&1)\"; then\n", a.Update)
	fmt.Fprintf(&b, "    echo \"agent %s : à jour\"\n", nomAgent)
	b.WriteString("    exit 0\n")
	b.WriteString("  fi\n")
	fmt.Fprintf(&b, "  echo \"agent %s : tentative ${tentative}/%d échouée :\" >&2\n", nomAgent, tentativesFraicheur)
	b.WriteString("  echo \"$sortie\" >&2\n")
	fmt.Fprintf(&b, "  if [ \"$tentative\" -lt %d ]; then\n", tentativesFraicheur)
	b.WriteString("    sleep 10\n")
	b.WriteString("  fi\n")
	b.WriteString("  tentative=$((tentative + 1))\n")
	b.WriteString("done\n\n")
	fmt.Fprintf(&b, "echo \"agent %s : FATAL mise à jour impossible après %d tentatives (fail-closed)\" >&2\n",
		nomAgent, tentativesFraicheur)
	b.WriteString("exit 1\n")

	return []string{"bash", "-c", b.String()}, nil
}
