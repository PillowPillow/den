package agent

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/PillowPillow/den/internal/nest"
	"github.com/PillowPillow/den/internal/sbx"
	"gopkg.in/yaml.v3"
)

// versionMixin est figée : le mixin est régénéré à chaque `sbx create` et n'a
// pas de cycle de vie propre. Une version variable ferait diverger les golden
// files sans rien apporter.
const versionMixin = "0.0.0"

// Mixin est le kit jetable généré par den à chaque spawn (spec §6.5).
type Mixin struct {
	NomSandbox string
	Env        map[string]string // déjà fusionné et substitué par nest.Resolve
	Egress     []string          // déjà unionné et trié par nest.Resolve
	Fraicheur  []string          // argv, cf. CommandeFraicheur
}

// MixinDepuis assemble le mixin d'un nest résolu.
//
// Env et Egress sont COPIÉS : ce sont des champs exportés d'une struct
// exportée, et les aliaser ferait d'une écriture sur le mixin une mutation
// silencieuse du nest résolu, que l'appelant continue d'utiliser après le spawn.
func MixinDepuis(r *nest.Resolved, nomSandbox string) (Mixin, error) {
	fraicheur, err := CommandeFraicheur(r.AgentName, r.Agent)
	if err != nil {
		return Mixin{}, err
	}
	return Mixin{
		NomSandbox: nomSandbox,
		Env:        maps.Clone(r.Env),
		Egress:     slices.Clone(r.Egress),
		Fraicheur:  fraicheur,
	}, nil
}

// RendMixin sérialise le mixin au schéma sbx réel (schemaVersion 2).
//
// Le YAML est construit nœud par nœud plutôt que par yaml.Marshal d'une map :
// l'ordre d'itération des maps Go est aléatoire, et un golden file ne tolère
// pas l'aléatoire. Les clés d'environment.variables sont émises TRIÉES.
func RendMixin(m Mixin) ([]byte, error) {
	// La fraîcheur ne suit PAS la règle des sections vides : l'omettre rendrait
	// un mixin parfaitement valide qui démarre une sandbox sans aucun contrôle
	// de fraîcheur — un fail-OPEN silencieux, là où CommandeFraicheur refuse
	// justement un agent sans update (spec §9.1). MixinDepuis ne peut pas
	// atteindre ce cas, mais RendMixin est exportée et Mixin est une struct nue.
	if len(m.Fraicheur) == 0 {
		return nil, fmt.Errorf(
			"rendu du mixin de %s : aucune commande de fraîcheur — une sandbox ne doit jamais "+
				"démarrer avec un agent périmé (spec §9.1)", m.NomSandbox)
	}

	racine := &yaml.Node{Kind: yaml.MappingNode}

	ajoute := func(cle string, valeur *yaml.Node) {
		racine.Content = append(racine.Content, scalaire(cle), valeur)
	}

	ajoute("schemaVersion", scalaire("2"))
	ajoute("kind", scalaire("mixin"))
	// Le nom d'un kit ne peut pas porter le séparateur de nom de sandbox.
	ajoute("name", scalaire("den-"+strings.ReplaceAll(m.NomSandbox, ".", "-")))
	ajoute("version", scalaire(versionMixin))
	ajoute("description", scalaire(fmt.Sprintf(
		"Mixin genere par den pour la sandbox %s. Regenere a chaque spawn, "+
			"ne pas editer a la main.", m.NomSandbox)))

	// Sections omises si vides : une `allow: []` vide signifierait « rien
	// d'autorise », pas « pas de contrainte ».
	if len(m.Egress) > 0 {
		reseau := &yaml.Node{Kind: yaml.MappingNode}
		reseau.Content = append(reseau.Content, scalaire("allow"), sequence(m.Egress))
		caps := &yaml.Node{Kind: yaml.MappingNode}
		caps.Content = append(caps.Content, scalaire("network"), reseau)
		ajoute("caps", caps)
	}

	if len(m.Env) > 0 {
		vars := &yaml.Node{Kind: yaml.MappingNode}
		for _, k := range slices.Sorted(maps.Keys(m.Env)) {
			// CLÉ ET VALEUR : les deux sont de la donnée utilisateur. La clé
			// vient d'un `env:` de nest ou d'agent, et AUCUNE validation de
			// charset ne s'y applique dans toute la cascade. Une clé nulle est
			// même pire qu'une valeur nulle — elle fait disparaître l'ENTRÉE
			// ENTIÈRE à la relecture, pas seulement son côté droit.
			vars.Content = append(vars.Content, scalaireTexte(k), scalaireTexte(m.Env[k]))
		}
		env := &yaml.Node{Kind: yaml.MappingNode}
		env.Content = append(env.Content, scalaire("variables"), vars)
		ajoute("environment", env)
	}

	// Inconditionnel : garanti non vide par la garde d'entrée.
	entree := &yaml.Node{Kind: yaml.MappingNode}
	entree.Content = append(entree.Content, scalaire("command"), sequence(m.Fraicheur))
	startup := &yaml.Node{Kind: yaml.SequenceNode, Content: []*yaml.Node{entree}}
	commands := &yaml.Node{Kind: yaml.MappingNode}
	commands.Content = append(commands.Content, scalaire("startup"), startup)
	ajoute("commands", commands)

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(racine); err != nil {
		return nil, fmt.Errorf("rendu du mixin de %s : %w", m.NomSandbox, err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("rendu du mixin de %s : %w", m.NomSandbox, err)
	}
	return buf.Bytes(), nil
}

// scalaire construit un nœud scalaire STRUCTUREL : les clés, et les valeurs que
// den fixe lui-même (schemaVersion, kind, name, version, description). Elles
// sont connues, et `schemaVersion: 2` doit rester le nombre 2.
//
// Un contenu multiligne passe en style littéral (« | ») : c'est le seul style
// qui préserve un script bash sans échappement, et le script de fraîcheur en est
// un.
func scalaire(v string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.ScalarNode, Value: v}
	if strings.Contains(v, "\n") {
		n.Style = yaml.LiteralStyle
	}
	return n
}

// scalaireTexte construit un scalaire qui doit se relire comme une CHAÎNE, quoi
// qu'il contienne. Réservé aux DONNÉES UTILISATEUR : clés ET valeurs
// d'environnement, hôtes d'egress, arguments de la commande de fraîcheur.
//
// Le tag `!!str` fait quoter par l'encodeur les seules valeurs qui, nues, se
// reliraient autrement — `~`, `null`, `true`, `8080`… Mesuré : sur toutes les
// valeurs qui SONT déjà des chaînes pour YAML (`value`, un chemin, `github.com`,
// `bash`, `-c`, un bloc littéral), la sortie est identique au bit près, tag ou
// pas. C'est pourquoi le golden n'a pas bougé.
//
// Sans ça : un `TILDE: ~` se relisait `""` et Differences annonçait « env
// changé » à CHAQUE attache alors que rien n'avait bougé ; pire, une entrée
// nulle d'une SÉQUENCE (egress, argv de fraîcheur) disparaissait purement et
// simplement à la relecture. Un faux avertissement permanent apprend à
// l'utilisateur à ne plus lire l'avertissement — y compris le jour où un egress
// a vraiment été rétréci.
func scalaireTexte(v string) *yaml.Node {
	n := scalaire(v)
	n.Tag = "!!str"
	return n
}

func sequence(vals []string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.SequenceNode}
	for _, v := range vals {
		n.Content = append(n.Content, scalaireTexte(v))
	}
	return n
}

// valideNomSandbox contrôle qu'un nom peut devenir un segment de chemin.
//
// La garde vit ici et non chez l'appelant : ce paquet transforme un nom en
// chemin hôte — EcrisMixin ET LisMixin, toutes deux exportées, via le même
// cheminMixin — et « à la charge de l'appelant » est un contrat écrit nulle
// part. sbx.DecomposeNom est délibérément TOTALE et ne valide rien (elle sert
// aussi aux sandboxes créées hors den), et filepath.Join NETTOIE un « .. » en
// une vraie traversée au lieu de la rejeter.
//
// Défense en profondeur : Spawn refuse déjà ces noms bien en amont, via
// sbx.NomSandbox. Aucun chemin d'appel connu n'atteint la garde aujourd'hui.
//
// Le contrôle lui-même vit désormais dans sbx : il était dupliqué avec
// l'assemblage de l'argv, et les deux copies avaient divergé sur « api. ».
func valideNomSandbox(nom string) error {
	return sbx.ValiderNomSandbox(nom)
}

// dossierMixin et cheminMixin sont l'UNIQUE définition de l'emplacement du
// mixin. Écriture (EcrisMixin) et relecture (LisMixin) doivent y converger : si
// les deux composaient leur chemin séparément et divergeaient, LisMixin rendrait
// éternellement os.ErrNotExist, et den n'annoncerait plus jamais qu'une « dérive
// non vérifiable » — pour TOUTES les sandboxes, en permanence, sans que rien
// n'échoue. La détection ne serait pas silencieuse, elle serait définitivement
// inutile. Verrouillé par TestLisMixinRelitCeQuEcrisMixinEcrit.
func dossierMixin(denHome, nomSandbox string) string {
	return filepath.Join(denHome, "cache", "mixins", nomSandbox)
}

func cheminMixin(denHome, nomSandbox string) string {
	return filepath.Join(dossierMixin(denHome, nomSandbox), "spec.yaml")
}

// EcrisMixin matérialise le mixin sous <denHome>/cache/mixins/<sandbox>/ et
// renvoie le DOSSIER — c'est ce que `sbx create --kit` attend.
//
// Sous cache/ et non dans un mktemp : cache/ est déclaré reconstructible par le
// spec §3, et un mixin qui s'évapore rend indébogable un boot raté.
//
// Le fichier décrit ce qu'une VM a reçu à son `sbx create`, pas la configuration
// du moment : Spawn n'appelle EcrisMixin que sur la branche create, et s'en sert
// ensuite de RÉFÉRENCE pour détecter une configuration qui a dérivé sous une
// sandbox vivante (cf. LisMixin et Differences). Le réécrire à chaque passage
// détruirait cette référence — verrouillé par
// TestSpawnNeReecritPasLeMixinDUneSandboxVivante, dans internal/spawn.
//
// Corollaire : le fichier survit à la sandbox (den ne purge pas cache/), et un
// `sbx rm` suivi d'un nouveau spawn le remplace.
//
// Droits restrictifs (0700/0600) : spec.yaml porte environment.variables,
// c'est-à-dire l'env fusionné agent ∪ nest — de l'env utilisateur libre, où
// atterrissent naturellement une clé d'API ou une URI à credentials. Rien ne
// justifie de le rendre lisible par tous les comptes de la machine.
//
// Choix DÉLIBÉRÉ, à confirmer au premier boot réel : `sbx` n'étant pas
// installable ici, personne n'a pu vérifier qu'il lit le --kit sous l'uid
// appelant. S'il le lisait sous un autre uid non-root, 0600 casserait le boot —
// mais bruyamment et immédiatement (permission refusée au premier `create`),
// alors qu'un secret lisible par tous ne se signale jamais. root, lui,
// outrepasse les droits de toute façon.
func EcrisMixin(denHome, nomSandbox string, m Mixin) (string, error) {
	if err := valideNomSandbox(nomSandbox); err != nil {
		return "", fmt.Errorf("écriture du mixin : %w", err)
	}
	dir := dossierMixin(denHome, nomSandbox)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("création de %s : %w", dir, err)
	}
	contenu, err := RendMixin(m)
	if err != nil {
		return "", err
	}
	chemin := cheminMixin(denHome, nomSandbox)
	if err := os.WriteFile(chemin, contenu, 0o600); err != nil {
		return "", fmt.Errorf("écriture de %s : %w", chemin, err)
	}
	return dir, nil
}
