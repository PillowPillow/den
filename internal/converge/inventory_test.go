package converge

import (
	"context"
	"slices"
	"testing"

	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/source"
)

// The property the whole report rests on: an identity den OBSERVES and the
// identity a manifest DECLARES for the same credential are the same string.
//
// Two spellings would report a declared credential as undeclared on every run
// of `den doctor`, forever — the shape of defect githubService's comment
// records one level down, and the reason the three constructors exist.
func TestDeclaredAndObservedSecretsAgreeOnIdentity(t *testing.T) {
	m := sbx.NewMachine()
	m.Services["github"] = true
	m.Registries["registry.example.test:443"] = true
	m.Customs[sbx.CustomSecret{Host: "proxy.example.test", Env: "HTTPS_PROXY"}] = true

	state, err := ReadSbxState(context.Background(), m)
	if err != nil {
		t.Fatalf("ReadSbxState: %v", err)
	}
	observed := state.SecretEntries()

	declared := DeclaredSecrets(&source.Manifest{Resources: source.Resources{
		Credentials: []source.CredentialResource{
			// The id: is deliberately NOT the service name — an identity read
			// off it would miss the observed "service github".
			{ID: "github-service", Type: source.CredentialGitHub},
			{ID: "registry", Type: source.CredentialRegistry, Host: "registry.example.test:443"},
			{ID: "proxy", Type: source.CredentialHTTPSubstitution,
				Host: "proxy.example.test", Environment: "HTTPS_PROXY"},
		},
	}})
	slices.Sort(declared)

	if !slices.Equal(observed, declared) {
		t.Fatalf("observed and declared identities differ:\n observed %q\n declared %q",
			observed, declared)
	}
}

// A machine holding something no manifest declares keeps it in the observed
// list: that entry is the report's whole subject.
func TestSecretEntriesNamesEveryKindItObserved(t *testing.T) {
	m := sbx.NewMachine()
	m.Services["github"] = true
	m.Registries["ghcr.io:443"] = true
	m.Customs[sbx.CustomSecret{Host: "npm.example.test", Env: "NPM_TOKEN"}] = true

	state, err := ReadSbxState(context.Background(), m)
	if err != nil {
		t.Fatalf("ReadSbxState: %v", err)
	}
	want := []string{
		"custom npm.example.test (NPM_TOKEN)",
		"registry ghcr.io:443",
		"service github",
	}
	if got := state.SecretEntries(); !slices.Equal(got, want) {
		t.Errorf("SecretEntries() = %q, want %q (sorted)", got, want)
	}
}

// A nil state yields nil, not an empty list. The caller holds ReadSbxState's
// error and must render "den could not look", never "the machine holds
// nothing" — the confusion Observation exists to prevent.
func TestSecretEntriesOnANilStateIsNotAnEmptyMachine(t *testing.T) {
	var state *SbxState
	if got := state.SecretEntries(); got != nil {
		t.Errorf("a nil state reported %q instead of nil", got)
	}
}

// A manifest declaring nothing declares nothing — and a nil one does not
// panic: doctor reaches this with whatever LoadManifest returned.
func TestDeclaredSecretsOnAnEmptyManifest(t *testing.T) {
	if got := DeclaredSecrets(nil); got != nil {
		t.Errorf("DeclaredSecrets(nil) = %q, want nil", got)
	}
	if got := DeclaredSecrets(&source.Manifest{}); got != nil {
		t.Errorf("a manifest with no credentials declared %q", got)
	}
}
