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
	// l'ordre. C'est sur lui que portent les assertions.
	Appels [][]string

	// Reponses associe une réponse à un appel exact, clé = args joints par un
	// espace (ex. "ls --json").
	Reponses map[string]Reponse

	// Defaut sert quand aucune entrée de Reponses ne correspond.
	Defaut Reponse

	// ErreurAttach est renvoyée par Attach. Le fait que l'attache ait eu lieu
	// reste enregistré dans Appels même si elle échoue.
	ErreurAttach error
}

func (f *Fake) Run(_ context.Context, args ...string) ([]byte, error) {
	f.Appels = append(f.Appels, slices.Clone(args))
	if r, ok := f.Reponses[strings.Join(args, " ")]; ok {
		return r.Sortie, r.Err
	}
	return f.Defaut.Sortie, f.Defaut.Err
}

func (f *Fake) Attach(_ context.Context, args ...string) error {
	f.Appels = append(f.Appels, slices.Clone(args))
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
