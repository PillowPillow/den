package agent

import (
	"fmt"
	"maps"
	"os"
	"slices"

	"gopkg.in/yaml.v3"
)

// specLu est le sous-ensemble du schéma de kit que den GÉNÈRE, et donc le seul
// qu'il sache relire. Décodage TOLÉRANT aux clés inconnues (contrairement au
// YAML de configuration, strict) : ce fichier peut avoir été écrit par une autre
// version de den, et une section ajoutée depuis ne doit pas rendre la dérive
// indétectable.
//
// Le prix de cette tolérance : une clé RENOMMÉE côté RendMixin ne produirait pas
// d'erreur ici mais une section vide, donc une dérive fantôme signalée à chaque
// attache. C'est TestLisMixinDecodeLeGolden qui empêche ce cas, en décodant le
// golden — écrit à la main, jamais régénéré — plutôt qu'une sortie fraîche de
// RendMixin qui bougerait avec lui.
type specLu struct {
	Caps struct {
		Network struct {
			Allow []string `yaml:"allow"`
		} `yaml:"network"`
	} `yaml:"caps"`
	Environment struct {
		Variables map[string]string `yaml:"variables"`
	} `yaml:"environment"`
	Commands struct {
		Startup []struct {
			Command []string `yaml:"command"`
		} `yaml:"startup"`
	} `yaml:"commands"`
}

// LisMixin relit le mixin posé par un spawn précédent — c'est-à-dire celui que
// la sandbox a réellement reçu à son `sbx create`.
//
// L'absence du fichier est enveloppée avec %w : l'appelant doit pouvoir
// distinguer « aucune référence » (cache/ purgé — le spec §3 le déclare
// reconstructible — ou sandbox créée hors de ce den) d'une lecture cassée. Les
// DEUX sont des « den ne sait pas » et les deux s'annoncent ; la distinction ne
// sert qu'à dire à l'utilisateur laquelle des deux il regarde, parce qu'il n'y
// répond pas de la même façon.
func LisMixin(denHome, nomSandbox string) (Mixin, error) {
	// Même garde qu'EcrisMixin, pour la même raison : cette fonction est
	// exportée et compose un chemin hôte à partir du nom. Défense en
	// profondeur — Spawn refuse déjà ces noms via sbx.NomSandbox.
	if err := valideNomSandbox(nomSandbox); err != nil {
		return Mixin{}, fmt.Errorf("lecture du mixin : %w", err)
	}
	chemin := cheminMixin(denHome, nomSandbox)
	contenu, err := os.ReadFile(chemin)
	if err != nil {
		return Mixin{}, fmt.Errorf("lecture du mixin %s : %w", chemin, err)
	}
	var spec specLu
	if err := yaml.Unmarshal(contenu, &spec); err != nil {
		return Mixin{}, fmt.Errorf("lecture du mixin %s : %w", chemin, err)
	}

	m := Mixin{
		NomSandbox: nomSandbox,
		Env:        spec.Environment.Variables,
		Egress:     spec.Caps.Network.Allow,
	}
	// RendMixin n'émet qu'une seule startup command, et la fraîcheur est la
	// dernière (spec §9.1) : c'est donc la dernière qu'on relit, pas la première
	// — un den ultérieur qui en ajouterait une avant ne doit pas faire prendre
	// la mauvaise pour la fraîcheur.
	if n := len(spec.Commands.Startup); n > 0 {
		m.Fraicheur = spec.Commands.Startup[n-1].Command
	}
	return m, nil
}

// Differences énumère, en clair et dans un ordre déterministe, ce qui a changé
// entre le mixin d'un `sbx create` et celui que la configuration produirait
// maintenant.
//
// Elle existe parce que RIEN ne réapplique un mixin à une VM en marche : un
// `egress:` rétréci passe le settle-loop en silence (la policy large de la VM
// autorise évidemment la liste étroite) et l'utilisateur croit sa sandbox
// resserrée alors qu'elle est restée ouverte. Le sens inverse — un egress
// élargi — échoue proprement, sur le settle-loop.
//
// Ce qu'elle NE voit PAS : l'image de la stack, les kits et la liste des
// workspaces. Aucun des trois n'est porté par le mixin, et une dérive sur ces
// axes-là reste donc invisible ici.
//
// Les VALEURS d'environnement ne sont jamais rendues, seulement les clés :
// environment.variables porte l'env utilisateur libre — c'est la raison même du
// 0600 d'EcrisMixin — et ces lignes partent sur le terminal.
func Differences(ancien, nouveau Mixin) []string {
	var lignes []string

	for _, h := range absentsDe(ancien.Egress, nouveau.Egress) {
		lignes = append(lignes, fmt.Sprintf(
			"egress retiré de la config : %s — la sandbox le laisse toujours passer", h))
	}
	for _, h := range absentsDe(nouveau.Egress, ancien.Egress) {
		lignes = append(lignes, fmt.Sprintf(
			"egress ajouté à la config : %s — la sandbox ne le connaît pas", h))
	}

	for _, k := range slices.Sorted(maps.Keys(ancien.Env)) {
		valeur, present := nouveau.Env[k]
		switch {
		case !present:
			lignes = append(lignes, fmt.Sprintf(
				"env retiré de la config : %s — la sandbox le porte encore", k))
		case valeur != ancien.Env[k]:
			lignes = append(lignes, fmt.Sprintf(
				"env changé dans la config : %s — la sandbox garde la valeur de son create", k))
		}
	}
	for _, k := range slices.Sorted(maps.Keys(nouveau.Env)) {
		if _, present := ancien.Env[k]; !present {
			lignes = append(lignes, fmt.Sprintf(
				"env ajouté à la config : %s — la sandbox ne l'a pas", k))
		}
	}

	// Le script n'est pas rendu : il fait des dizaines de lignes et noierait
	// tout le reste.
	if !slices.Equal(ancien.Fraicheur, nouveau.Fraicheur) {
		lignes = append(lignes,
			"commande de fraîcheur changée — la sandbox rejoue l'ancienne à chaque boot")
	}
	return lignes
}

// absentsDe rend, triés, les éléments de a qui ne sont pas dans b. Trié parce
// que ces lignes vont sur un terminal : un ordre qui bouge d'un spawn à l'autre
// se lit comme un changement.
func absentsDe(a, b []string) []string {
	var out []string
	for _, v := range a {
		if !slices.Contains(b, v) {
			out = append(out, v)
		}
	}
	slices.Sort(out)
	return out
}
