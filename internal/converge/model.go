package converge

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/PillowPillow/den/internal/source"
)

// Status is the aggregate verdict on an installed source. Aliased rather than
// redefined: internal/source persists these exact strings in the receipt, and
// two enumerations of one vocabulary is how a status den writes stops being a
// status den can read.
type Status = source.SourceStatus

// The resource KINDS den converges. They are the manifest's own groups (spec
// §5.3), and they key both the plan's rendering and the receipt's shape — one
// vocabulary, so a group added to the manifest surfaces in both without a
// second mapping to keep in sync.
const (
	KindCredential   = "credential"
	KindBuildNetwork = "build_network"
	KindStackBuild   = "stack"
)

// Action is what den will DO to a resource, and nothing else — the four
// values answer "what changes", never "is it healthy" (that is Known and the
// aggregate Status below).
type Action string

const (
	// ActionUnchanged: observed state already matches the manifest. Applying
	// is skipped, which is also what makes a resumed convergence cheap.
	ActionUnchanged Action = "unchanged"
	ActionCreate    Action = "create"
	ActionUpdate    Action = "update"
	// ActionBlocked: den knows this resource cannot converge — a missing
	// input, an incompatibility. The plan still prints it, because a plan
	// that hid what it cannot do would be confirmed by someone expecting it.
	ActionBlocked Action = "blocked"
)

// ResourcePlan is one managed resource's line in the plan.
//
// Observed and Expected are RENDERED states, never values: a credential's
// observed state is "configured" or "absent", never the secret (spec §12.3).
type ResourcePlan struct {
	ID     string
	Kind   string
	Action Action
	// Known is false when den could not OBSERVE the resource — the sbx daemon
	// did not answer, or the Keychain was unreachable in a restricted
	// environment (prototype 2026-08-14). It is never inferred as "absent":
	// applying over an unobservable credential could silently replace a
	// working one, and reporting it as missing would be a lie the user acts on.
	Known    bool
	Observed string
	Expected string
	// Resume is the exact command that continues this resource's convergence
	// after a failure (spec §12.3).
	Resume string
}

// ResourceError is the convergence failure contract of spec §12.3: it names
// the resource, what den saw, what the manifest wants, what remains to be
// done, and the command that resumes.
//
// A type rather than a formatted string, so the CLI can render it and the
// service can classify it — and so every field is present or visibly empty,
// rather than remembered at each raise site.
type ResourceError struct {
	Resource  string
	Observed  string
	Expected  string
	Remaining string
	Resume    string
	Cause     error
}

func (e *ResourceError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "resource %s: ", e.Resource)
	if e.Cause != nil {
		fmt.Fprintf(&b, "%v: ", e.Cause)
	}
	fmt.Fprintf(&b, "observed %s, expected %s", e.Observed, e.Expected)
	if e.Remaining != "" {
		fmt.Fprintf(&b, " — %s", e.Remaining)
	}
	if e.Resume != "" {
		fmt.Fprintf(&b, "; %s", e.Resume)
	}
	return b.String()
}

func (e *ResourceError) Unwrap() error { return e.Cause }

// NestReadiness is one exported nest's verdict. Status is source.NestReady or
// source.NestNotReady — a nest has no third state: anything wrong with the
// source itself blocks the SOURCE, not one of its nests.
type NestReadiness struct {
	Name         string
	Status       string
	MissingRepos []string
}

// EvaluateReadiness decides, for every exported nest, whether this machine can
// spawn it.
//
// The rule is narrow on purpose (spec §7.2): a nest is not_ready when a
// REQUIRED repository of its own has no confirmed mapping. An optional one
// changes nothing — that is the whole meaning of `optional:` — and a repository
// required by ANOTHER nest is not this nest's problem.
func EvaluateReadiness(nests []string, matches []RepoMatch) []NestReadiness {
	missing := map[string][]string{}
	for _, m := range matches {
		if m.Confirmed {
			continue
		}
		// Unconfirmed covers both "absent" and "found but not attributable":
		// den maps neither, so a nest needing it cannot spawn.
		for _, nestName := range m.Requirement.RequiredBy {
			missing[nestName] = append(missing[nestName], m.Requirement.Key)
		}
	}
	out := make([]NestReadiness, 0, len(nests))
	for _, name := range slices.Sorted(slices.Values(nests)) {
		keys := missing[name]
		slices.Sort(keys)
		r := NestReadiness{Name: name, Status: source.NestReady}
		if len(keys) > 0 {
			r.Status, r.MissingRepos = source.NestNotReady, keys
		}
		out = append(out, r)
	}
	return out
}

// AggregateStatus is the source's single verdict (spec §10.1).
//
// The order of the tests is the doctrine, not an implementation detail:
//
//   - UNKNOWN wins over everything. den could not observe something it needs
//     to judge, so every other conclusion would be a guess — and `den doctor`
//     must never answer "all good" on a machine it could not read.
//   - BLOCKED next: a managed resource den cannot converge stops the source
//     from working at all.
//   - PARTIALLY_READY is the normal, successful outcome of an onboarding on a
//     machine that does not have every repository yet. It is NOT a failure:
//     den never clones, so a missing working repository is a fact about the
//     machine, not a fault of the installation.
func AggregateStatus(resources []ResourcePlan, nests []NestReadiness) Status {
	for _, r := range resources {
		if !r.Known {
			return source.StatusUnknown
		}
	}
	for _, r := range resources {
		if r.Action == ActionBlocked {
			return source.StatusBlocked
		}
	}
	for _, n := range nests {
		if n.Status != source.NestReady {
			return source.StatusPartiallyReady
		}
	}
	return source.StatusReady
}

// Succeeds reports whether a command may exit 0 on this status. `ready` and
// `partially_ready` do; `blocked` and `unknown` do not (spec §10.1). One
// function, because `den init --source`, `den source status` and `den doctor`
// must not each decide it.
func Succeeds(s Status) bool {
	return s == source.StatusReady || s == source.StatusPartiallyReady
}

// Plan is what den prints before it mutates anything, and what it applies
// afterwards. Every collection in it is ordered — manifest order for
// resources, sorted names elsewhere — so two runs on one machine produce
// byte-identical output and a diff between two plans means a real change.
type Plan struct {
	Source  string
	Version string
	// TrustBoundary names what confirming this plan authorizes beyond den's
	// own writes: building a declared stack executes that stack's provision
	// files, which are the SOURCE's code (spec §4.3). Confirmation without
	// that sentence would be uninformed.
	TrustBoundary string
	Resources     []ResourcePlan
	// Warnings are facts den could not verify but chose not to refuse on — an
	// unreadable binary version, typically. Printed with the plan: a check den
	// skipped is something the user must be told about, not something den
	// silently decides is fine.
	Warnings    []string
	RepoMatches []RepoMatch
	Nests       []NestReadiness
	Status      Status
}

// UnconfirmedRepoMatches are the discovery results a human still has to
// settle. The CLI resolves them, then asks for a second plan — both passes
// are read-only.
func (p *Plan) UnconfirmedRepoMatches() []RepoMatch { return UnconfirmedMatches(p.RepoMatches) }

// ConfirmedRepos is the mapping this plan would write into the personal
// source configuration: confirmed entries only, never a guess.
func (p *Plan) ConfirmedRepos() map[string]string {
	out := map[string]string{}
	for _, m := range p.RepoMatches {
		if m.Confirmed && m.Path != "" {
			out[m.Requirement.Key] = m.Path
		}
	}
	return out
}

// Changes reports whether applying this plan would create or update a
// RESOURCE — a credential, the build_network allowlist, a stack build. It is
// not a full mutation oracle: Apply does other work regardless of this
// verdict (ModeInit/ModeAdd still install the candidate, ModeUpdate still
// fast-forwards the checkout, and every resource is still verified even when
// unchanged), so a false Changes() does not mean Apply is a no-op.
//
// What it IS true for: a resource plan with nothing to create or update asks
// nothing of a human that the plan itself didn't already answer, so
// confirm() (internal/cli/answers.go) skips the interactive question on this
// verdict alone and lets Apply run as it always would.
func (p *Plan) Changes() bool {
	for _, r := range p.Resources {
		if r.Action == ActionCreate || r.Action == ActionUpdate {
			return true
		}
	}
	return false
}

// receiptResources renders the plan's resources into the receipt's grouped
// shape. Kind is the grouping key the manifest itself uses, so a resource
// added to a group needs no change here.
func receiptResources(resources []ResourcePlan) source.ReceiptResources {
	out := source.ReceiptResources{}
	stacks := map[string]source.ResourceStatus{}
	for _, r := range resources {
		status := source.ResourceReady
		switch {
		case !r.Known:
			status = source.ResourceUnknown
		case r.Action == ActionBlocked:
			status = source.ResourceBlocked
		}
		switch r.Kind {
		case KindCredential:
			out.Credentials = worst(out.Credentials, status)
		case KindBuildNetwork:
			out.BuildNetwork = worst(out.BuildNetwork, status)
		case KindStackBuild:
			stacks[r.ID] = status
		}
	}
	if len(stacks) > 0 {
		out.Stacks = map[string]source.ResourceStatus{}
		for _, name := range slices.Sorted(maps.Keys(stacks)) {
			out.Stacks[name] = stacks[name]
		}
	}
	return out
}

// worst keeps the least reassuring of two statuses: a group is only ready
// when every resource in it is.
func worst(a, b source.ResourceStatus) source.ResourceStatus {
	rank := map[source.ResourceStatus]int{
		"": 0, source.ResourceReady: 1, source.ResourceBlocked: 2, source.ResourceUnknown: 3,
	}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// Receipt renders what den applied, ready to be written by internal/source.
// Built HERE rather than in the service so the mapping from plan to receipt
// has one definition and one test.
func (p *Plan) Receipt(commit, digest string, appliedAt time.Time) source.Receipt {
	nests := map[string]source.ReceiptNest{}
	for _, n := range p.Nests {
		nests[n.Name] = source.ReceiptNest{Status: n.Status, MissingRepos: n.MissingRepos}
	}
	return source.Receipt{
		Status:         p.Status,
		Version:        p.Version,
		Commit:         commit,
		ManifestDigest: digest,
		AppliedAt:      appliedAt,
		Resources:      receiptResources(p.Resources),
		Nests:          nests,
	}
}
