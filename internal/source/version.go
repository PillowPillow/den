package source

import (
	"errors"
	"fmt"
	"os"
)

// Active is an installed source a command may CONSUME: its checkout, its
// contract when it has one, and the mapping its nests resolve their repo keys
// through.
//
// Repos is nil for a legacy source, and that nil is the signal, not an
// absence of data: nest.Resolve reads a nil mapping as "no source scope, use
// config.yaml.repos". A manifested source always carries a non-nil map, empty
// when it maps nothing yet — see Personal.ExpandedRepos.
type Active struct {
	Name     string
	Root     string
	Manifest *Manifest // nil for a legacy source
	Personal *Personal // nil for a legacy source
	Receipt  *Receipt  // nil for a legacy source
	Repos    map[string]string
}

// Manifested reports whether this source carries den-source.yaml. Every
// behavior that differs between the two worlds keys off this one method, so
// the question is asked in one dialect.
func (a *Active) Manifested() bool { return a != nil && a.Manifest != nil }

// RepoMapping is the mapping this source's nests resolve their `key:` entries
// through, nil-safe so a caller holding no source at all (a local nest) can
// ask without a branch. nil means "no source scope": nest.Resolve then keeps
// config.yaml.repos.
func (a *Active) RepoMapping() map[string]string {
	if a == nil {
		return nil
	}
	return a.Repos
}

// MappingPath names the file a refusal about this source's mapping must
// point at. Empty for a legacy source, whose keys live in config.yaml.
func (a *Active) MappingPath(denHome string) string {
	if a == nil || !a.Manifested() {
		return ""
	}
	return PersonalPath(denHome, a.Name)
}

// RequireUsable is the gate every consumer of an installed source passes
// through: `den spawn`, `den nest show`, `den build <source>:<stack>`.
//
// For a MANIFESTED source it enforces the invariant a partial application can
// break (spec §11.3): the checked-out contract, the configured exact version
// and the final receipt must name the SAME version. While they do not, the
// machine holds a half-converged source — some credentials of the new version
// applied, an image possibly not rebuilt — and a spawn there would mix a new
// catalogue with old infrastructure, silently. Refusing names the pending
// version and `den source configure <name>`, which resumes exactly that
// convergence.
//
// For a LEGACY source it enforces nothing: those sources predate the contract
// and nothing about them may start refusing (spec §9.3).
//
// It reads the filesystem only. No fetch, no sbx call: an ordinary command
// never contacts a remote (spec §11.1), and the resource observations belong
// to `den source status` and `den doctor`.
func RequireUsable(denHome, name string) (*Active, error) {
	root, err := requireInstalled(denHome, name)
	if err != nil {
		return nil, err
	}
	if !HasManifest(root) {
		return &Active{Name: name, Root: root}, nil
	}
	m, err := LoadManifest(root)
	if err != nil {
		return nil, err
	}
	resume := fmt.Sprintf("run `den source configure %s`", name)

	personal, err := LoadPersonal(denHome, name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf(
				"source %q: installed at version %s but not configured on this machine — "+
					"its repo mapping and exact version live in %s; %s",
				name, m.Metadata.Version, PersonalPath(denHome, name), resume)
		}
		return nil, err
	}
	receipt, err := LoadReceipt(denHome, name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf(
				"source %q: no convergence receipt in %s — den cannot tell which of its "+
					"credentials, policies and images were applied, so it will not spawn from it; %s",
				name, ReceiptPath(denHome, name), resume)
		}
		return nil, err
	}
	// The applying marker is checked FIRST among the divergences: it is the
	// only one that says WHY the versions disagree, and its message names the
	// target the resume converges on rather than the mismatch it caused.
	if receipt.Status == StatusApplying {
		return nil, fmt.Errorf(
			"source %q: a convergence to %s was interrupted (it was at %s) — some resources of "+
				"the new version may be applied and others not; %s to finish it",
			name, receipt.TargetVersion, receipt.PreviousVersion, resume)
	}
	if personal.Version != m.Metadata.Version {
		return nil, fmt.Errorf(
			"source %q: the checkout is at version %s while this machine is configured for %s — "+
				"the resources of %s were never applied here; %s",
			name, m.Metadata.Version, personal.Version, m.Metadata.Version, resume)
	}
	if receipt.Version != personal.Version {
		return nil, fmt.Errorf(
			"source %q: the receipt attests version %s while this machine is configured for %s — "+
				"den cannot tell which one the applied resources belong to; %s",
			name, receipt.Version, personal.Version, resume)
	}
	repos, err := personal.ExpandedRepos()
	if err != nil {
		return nil, fmt.Errorf("source %q: %s: %w", name, PersonalPath(denHome, name), err)
	}
	return &Active{
		Name:     name,
		Root:     root,
		Manifest: m,
		Personal: personal,
		Receipt:  receipt,
		Repos:    repos,
	}, nil
}
