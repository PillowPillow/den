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
	"gopkg.in/yaml.v3"
)

// versionMixin est figée : le mixin est régénéré à chaque spawn et n'a pas de
// cycle de vie propre. Une version variable ferait diverger les golden files
// sans rien apporter.
const versionMixin = "0.0.0"

// Mixin est le kit jetable généré par den à chaque spawn (spec §6.5).
type Mixin struct {
	NomSandbox string
	Env        map[string]string // déjà fusionné et substitué par nest.Resolve
	Egress     []string          // déjà unionné et trié par nest.Resolve
	Fraicheur  []string          // argv, cf. CommandeFraicheur
}

// MixinDepuis assemble le mixin d'un nest résolu.
func MixinDepuis(r *nest.Resolved, nomSandbox string) (Mixin, error) {
	fraicheur, err := CommandeFraicheur(r.AgentName, r.Agent)
	if err != nil {
		return Mixin{}, err
	}
	return Mixin{
		NomSandbox: nomSandbox,
		Env:        r.Env,
		Egress:     r.Egress,
		Fraicheur:  fraicheur,
	}, nil
}

// RendMixin sérialise le mixin au schéma sbx réel (schemaVersion 2).
//
// Le YAML est construit nœud par nœud plutôt que par yaml.Marshal d'une map :
// l'ordre d'itération des maps Go est aléatoire, et un golden file ne tolère
// pas l'aléatoire. Les clés d'environment.variables sont émises TRIÉES.
func RendMixin(m Mixin) ([]byte, error) {
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
			vars.Content = append(vars.Content, scalaire(k), scalaire(m.Env[k]))
		}
		env := &yaml.Node{Kind: yaml.MappingNode}
		env.Content = append(env.Content, scalaire("variables"), vars)
		ajoute("environment", env)
	}

	if len(m.Fraicheur) > 0 {
		entree := &yaml.Node{Kind: yaml.MappingNode}
		entree.Content = append(entree.Content, scalaire("command"), sequence(m.Fraicheur))
		startup := &yaml.Node{Kind: yaml.SequenceNode, Content: []*yaml.Node{entree}}
		commands := &yaml.Node{Kind: yaml.MappingNode}
		commands.Content = append(commands.Content, scalaire("startup"), startup)
		ajoute("commands", commands)
	}

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

// scalaire construit un nœud scalaire. Un contenu multiligne passe en style
// littéral (« | ») : c'est le seul style qui préserve un script bash sans
// échappement, et le script de fraîcheur en est un.
func scalaire(v string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.ScalarNode, Value: v}
	if strings.Contains(v, "\n") {
		n.Style = yaml.LiteralStyle
	}
	return n
}

func sequence(vals []string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.SequenceNode}
	for _, v := range vals {
		n.Content = append(n.Content, scalaire(v))
	}
	return n
}

// EcrisMixin matérialise le mixin sous <denHome>/cache/mixins/<sandbox>/ et
// renvoie le DOSSIER — c'est ce que `sbx create --kit` attend.
//
// Sous cache/ et non dans un mktemp : cache/ est déclaré reconstructible par le
// spec §3, et un mixin qui s'évapore rend indébogable un boot raté. Il est
// réécrit à chaque spawn et reflète toujours la configuration courante.
func EcrisMixin(denHome, nomSandbox string, m Mixin) (string, error) {
	dir := filepath.Join(denHome, "cache", "mixins", nomSandbox)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("création de %s : %w", dir, err)
	}
	contenu, err := RendMixin(m)
	if err != nil {
		return "", err
	}
	chemin := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(chemin, contenu, 0o644); err != nil {
		return "", fmt.Errorf("écriture de %s : %w", chemin, err)
	}
	return dir, nil
}
