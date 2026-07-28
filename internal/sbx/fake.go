package sbx

import (
	"context"
	"slices"
	"strings"
)

// Reponse est ce que le Fake renvoie pour un appel donné.
type Reponse struct {
	Sortie []byte
	Err    error
}

// Fake est le double de test de Runner.
//
// Il vit dans le paquet de production et non dans un `_test.go` À DESSEIN :
// policy, cli et agent en ont tous besoin, et un double par paquet dériverait
// aussitôt du contrat réel. `internal/` en borne déjà la portée.
type Fake struct {
	// Appels enregistre chaque invocation, Run et Attach confondues, dans
	// l'ordre. C'est sur lui que portent les assertions générales.
	Appels [][]string

	// Attaches enregistre UNIQUEMENT les appels à Attach, dans l'ordre. Run et
	// Attach sont irréconciliables (cf. runner.go) : un Run dont l'argv
	// commence par "exec" ne doit jamais compter comme une attache, et une
	// attache doit rester détectable même si un Run légitime a un argv voisin.
	// Attach alimente Appels ET Attaches ; Run n'alimente que Appels.
	Attaches [][]string

	// Reponses associe une réponse à un appel exact, clé = args joints par un
	// espace (ex. "ls --json"). Un argument qui contient lui-même une espace
	// (un chemin de workspace, par exemple) peut donc produire une clé
	// ambiguë avec un autre appel : à éviter en scriptant Reponses.
	Reponses map[string]Reponse

	// Defaut sert quand aucune entrée de Reponses ne correspond.
	Defaut Reponse

	// ErreurAttach est renvoyée par Attach. Le fait que l'attache ait eu lieu
	// reste enregistré dans Appels même si elle échoue.
	ErreurAttach error
}

// Run renvoie une COPIE de la sortie scriptée (jamais la slice sous-jacente) :
// un appelant qui modifierait le résultat reçu ne doit pas corrompre les
// appels suivants qui retombent sur la même entrée de Reponses ou sur Defaut.
func (f *Fake) Run(_ context.Context, args ...string) ([]byte, error) {
	f.Appels = append(f.Appels, slices.Clone(args))
	if r, ok := f.Reponses[strings.Join(args, " ")]; ok {
		return slices.Clone(r.Sortie), r.Err
	}
	return slices.Clone(f.Defaut.Sortie), f.Defaut.Err
}

func (f *Fake) Attach(_ context.Context, args ...string) error {
	f.Appels = append(f.Appels, slices.Clone(args))
	f.Attaches = append(f.Attaches, slices.Clone(args))
	return f.ErreurAttach
}

// DernierAppel renvoie le dernier appel enregistré, ou nil s'il n'y en a aucun.
func (f *Fake) DernierAppel() []string {
	if len(f.Appels) == 0 {
		return nil
	}
	return f.Appels[len(f.Appels)-1]
}

// AAppele indique si un appel a commencé par ce préfixe d'arguments. Assertion
// par préfixe et non par égalité : un test qui vérifie « on a bien fait un
// create » ne doit pas casser parce qu'un chemin de plus a été monté.
func (f *Fake) AAppele(prefixe ...string) bool {
	for _, a := range f.Appels {
		if len(a) >= len(prefixe) && slices.Equal(a[:len(prefixe)], prefixe) {
			return true
		}
	}
	return false
}

// AAttache indique si une ATTACHE (jamais un Run) a commencé par ce préfixe.
// Même patron que AAppele, mais sur Attaches : c'est elle qu'il faut utiliser
// pour asserter qu'un shell interactif a bien été branché, ou qu'aucune
// attache n'a eu lieu (policy bloquée, `--detach`).
func (f *Fake) AAttache(prefixe ...string) bool {
	for _, a := range f.Attaches {
		if len(a) >= len(prefixe) && slices.Equal(a[:len(prefixe)], prefixe) {
			return true
		}
	}
	return false
}

// Garde-fou de compilation : Fake doit rester substituable à Runner. Vit ici,
// avec le code de production, et pas dans un `_test.go` : si Runner change,
// l'échec doit sortir dès `go build ./...`, pas au premier `go test` d'un
// paquet tiers qui consomme Fake.
var _ Runner = (*Fake)(nil)
