package converge

import (
	"fmt"
	"slices"
	"strings"

	"github.com/PillowPillow/den/internal/source"
)

// Secret identities. THREE constructors, used by both sides of the comparison
// — what den observed on the machine and what a source declares — because the
// comparison is a string equality and two spellings of the same credential
// would report a declared secret as undeclared forever. Same lesson as
// customKey right above, one level up: the identity of a thing has exactly one
// definition.
//
// The form is the user's, not sbx's: it is printed in `den doctor` and the
// reader has to recognize the credential they configured.
func serviceIdentity(name string) string  { return "service " + name }
func registryIdentity(host string) string { return "registry " + host }
func customIdentity(targets, environment string) string {
	return fmt.Sprintf("custom %s (%s)", targets, environment)
}

// SecretEntries lists every global sbx secret den OBSERVED, sorted.
//
// This is the one machine surface den can name entry by entry — parseSecretList
// reads TYPE and NAME off the table — which is why it is reported by identity
// while the MCP registry and the skills store are reported by presence alone
// (sbx.ReadStore says why).
//
// A nil state yields nil rather than an empty list, and the difference matters
// to the caller: "den could not observe the machine" must not be rendered as
// "the machine holds no secret", the confusion Observation exists to prevent.
// Every caller here already holds ReadSbxState's error to tell them apart.
func (s *SbxState) SecretEntries() []string {
	if s == nil {
		return nil
	}
	var out []string
	for name := range s.Services {
		out = append(out, serviceIdentity(name))
	}
	for host := range s.Registries {
		out = append(out, registryIdentity(host))
	}
	for key := range s.Customs {
		// customKey joins targets and environment with a NUL; splitting it
		// back is why that constructor and this reader must stay neighbours.
		targets, environment, _ := strings.Cut(key, "\x00")
		out = append(out, customIdentity(targets, environment))
	}
	// Sorted: a Go map has no order, and a report whose lines move between two
	// runs of `den doctor` is unreadable as a diff.
	slices.Sort(out)
	return out
}

// DeclaredSecrets lists the secret identities a source's manifest claims.
//
// Derived from the resource TYPE, exactly as CredentialPresent derives its
// lookup, and never from the manifest's `id:` — the trap githubService's
// comment records: a source is free to name a resource anything, and an
// identity read off that name would put a declared credential in the
// undeclared column of every report.
//
// A type den does not know contributes NOTHING rather than an error. It cannot
// reach here through LoadManifest, which refuses the unknown vocabulary; but
// this list only ever SUPPRESSES a report line, so the fail-safe direction is
// to keep reporting — den naming a credential a future den declares is a line
// too many, where dropping it silently is the blind spot again.
func DeclaredSecrets(m *source.Manifest) []string {
	if m == nil {
		return nil
	}
	var out []string
	for _, res := range m.Resources.Credentials {
		switch res.Type {
		case source.CredentialGitHub:
			out = append(out, serviceIdentity(githubService))
		case source.CredentialRegistry:
			out = append(out, registryIdentity(res.Host))
		case source.CredentialHTTPSubstitution:
			out = append(out, customIdentity(res.Host, res.Environment))
		}
	}
	return out
}
