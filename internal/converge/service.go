package converge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"time"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/source"
	"github.com/PillowPillow/den/internal/worktree"
)

// Mode says which command is converging. It changes WHERE the source content
// comes from and nothing else — the planner, the applier and the ordering are
// one implementation for all four (spec §9.3).
type Mode string

const (
	// ModeInit and ModeAdd install a source that is not there yet, from a
	// temporary clone.
	ModeInit Mode = "init"
	ModeAdd  Mode = "add"
	// ModeConfigure reconverges the INSTALLED checkout, without contacting the
	// remote (spec §11.1). It is what discovers a repository cloned after the
	// initial setup, and what resumes a partial application.
	ModeConfigure Mode = "configure"
	// ModeUpdate converges a fetched candidate at a greater version.
	ModeUpdate Mode = "update"
)

// Request is one convergence. It carries no writer and no terminal: the CLI
// owns interaction, this package owns the decision.
type Request struct {
	Mode    Mode
	DenHome string
	// Name is the INSTALL name. Resolved by the caller (source.ResolveNamespace)
	// so a plan can name it before anything is written.
	Name string
	// Candidate is the source content being converged: a temporary clone, the
	// installed checkout, or a fetched update.
	Candidate *source.Candidate
	Answers   Answers
	// DenVersion is this binary's version, for the manifest's `requires.den`
	// floor. Injected rather than read, so tests pin it.
	DenVersion string
	// FreshGlobalConfig is the source-aware config.yaml to write when the den
	// home has none. Nil on an initialized home, whose global configuration is
	// preserved byte for byte (spec §8).
	FreshGlobalConfig []byte
}

// Service is the shared convergence engine. Every system access is injected,
// as everywhere else in den: the suite exercises the real planner and the real
// applier against fakes, on a temporary den home.
type Service struct {
	Git     worktree.Git
	Sbx     sbx.Runner
	Secrets sbx.SecretRunner
	// Now stamps the receipt. Injected so a receipt is comparable between runs
	// in a test.
	Now func() time.Time
}

// Result is what Apply committed.
type Result struct {
	Status   Status
	Personal source.Personal
	Receipt  source.Receipt
}

// Plan observes the machine and computes what converging would do. It mutates
// NOTHING — no file, no directory, no sbx state — which is what makes the
// confirmation it feeds meaningful.
//
// Two error classes, kept apart on purpose:
//
//   - a STRUCTURAL fault returns an error: the manifest does not load, an
//     answer names a directory that is not a repository, a required binary is
//     too old. There is no plan to show.
//   - an OBSERVATION failure does not. den could not read the machine's sbx
//     state, so every resource is `unknown`, the aggregate status is `unknown`,
//     and the plan still lists everything the source declares. That is the
//     state doctor must be able to render and exit non-zero on (spec §12.2) —
//     returning an error here would leave it with nothing to print.
func (s Service) Plan(ctx context.Context, req Request) (*Plan, error) {
	m := req.Candidate.Manifest
	if m == nil {
		return nil, fmt.Errorf(
			"source %q: no %s — this command converges a manifested source; a source without a "+
				"contract keeps the legacy `den source add`/`den source update` behavior",
			req.Name, source.ManifestFile)
	}

	plan := &Plan{Source: req.Name, Version: m.Metadata.Version}
	if err := s.checkCompatibility(ctx, m, req, plan); err != nil {
		return nil, err
	}
	if len(m.Resources.Builds) > 0 {
		plan.TrustBoundary = fmt.Sprintf(
			"applying this plan builds %s, which RUNS the provision scripts of source %s "+
				"(%s) — confirm only a source you trust",
			buildList(m), req.Name, req.Candidate.Root)
	}

	// One observation for the whole plan, and its failure is a state rather
	// than an exception (see the godoc above).
	state, observeErr := ReadSbxState(ctx, s.Sbx)
	if observeErr != nil {
		plan.Resources = unobservedResources(m, observeErr)
	} else {
		for _, d := range s.drivers(req, m, state) {
			o, err := d.Inspect(ctx)
			if err != nil {
				// A single driver that cannot observe (the image inventory did
				// not answer) is unknown on its own line; the rest of the plan
				// stays truthful.
				p := d.Plan(Observation{})
				p.Known, p.Observed = false, err.Error()
				plan.Resources = append(plan.Resources, p)
				continue
			}
			plan.Resources = append(plan.Resources, d.Plan(o))
		}
	}

	reqs, err := CollectRepoRequirements(req.Candidate.Root, m)
	if err != nil {
		return nil, err
	}
	if plan.RepoMatches, err = DiscoverRepos(ctx, s.Git, reqs, req.Answers); err != nil {
		return nil, err
	}
	plan.Nests = EvaluateReadiness(m.ExportedNestNames(), plan.RepoMatches)
	plan.Status = AggregateStatus(plan.Resources, plan.Nests)
	return plan, nil
}

// checkCompatibility verifies the binaries against the manifest's floors.
//
// A version that is TOO OLD is a refusal: den installs neither binary, so
// nothing it could do would fix it. A version den cannot READ is a warning
// instead — `go build` answers "dev", and refusing there would make den
// unusable for the people who develop it, on a machine whose sbx may be
// perfectly recent.
func (s Service) checkCompatibility(ctx context.Context, m *source.Manifest, req Request, plan *Plan) error {
	sbxVersion := ""
	if out, err := s.Sbx.Run(ctx, "version"); err == nil {
		sbxVersion = ParseSbxVersion(string(out))
	}
	err := source.CheckCompatibility(m, req.DenVersion, sbxVersion)
	var unknown *source.UnknownVersionError
	if errors.As(err, &unknown) {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf(
			"%v — den cannot check this requirement and continues; verify it by hand", err))
		return nil
	}
	return err
}

// unobservedResources is the plan den can still show when it could not read
// the machine: every declared resource, marked unknown.
//
// Built from the MANIFEST rather than from drivers, because a driver's Inspect
// needs the state that is precisely what is missing. Everything the source
// declares is listed — a plan that shrank when observation failed would let a
// user confirm an installation whose scope they cannot see.
func unobservedResources(m *source.Manifest, cause error) []ResourcePlan {
	detail := cause.Error()
	var out []ResourcePlan
	for _, c := range m.Resources.Credentials {
		out = append(out, ResourcePlan{ID: c.ID, Kind: KindCredential, Action: ActionCreate, Observed: detail})
	}
	if len(m.Resources.BuildNetwork.Allow) > 0 {
		out = append(out, ResourcePlan{
			ID: KindBuildNetwork, Kind: KindBuildNetwork, Action: ActionCreate, Observed: detail})
	}
	for _, b := range m.Resources.Builds {
		out = append(out, ResourcePlan{ID: b.Stack, Kind: KindStackBuild, Action: ActionCreate, Observed: detail})
	}
	return out
}

func buildList(m *source.Manifest) string {
	names := make([]string, 0, len(m.Resources.Builds))
	for _, b := range m.Resources.Builds {
		names = append(names, "stack "+b.Stack)
	}
	return joinWithAnd(names)
}

func joinWithAnd(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	default:
		return fmt.Sprintf("%s and %s", joinComma(items[:len(items)-1]), items[len(items)-1])
	}
}

func joinComma(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

// drivers instantiates the resource drivers for this request.
//
// force is decided HERE, from the versions, and not from the mode: an existing
// image is evidence of nothing on a FIRST install (nothing recorded which
// source content produced it) or when the source's version has moved, and is
// evidence of everything when a resume converges the version already
// configured. Forcing on every mode would make `den source configure` — the
// command that exists to be run again — rebuild every image each time.
func (s Service) drivers(req Request, m *source.Manifest, state *SbxState) []ResourceDriver {
	return Drivers{
		Name:      req.Name,
		StackRoot: req.Candidate.Root,
		DenHome:   req.DenHome,
		Runner:    s.Sbx,
		Secrets:   s.Secrets,
		Force:     s.forceRebuild(req, m),
	}.ForManifest(m, state)
}

func (s Service) forceRebuild(req Request, m *source.Manifest) bool {
	personal, err := source.LoadPersonal(req.DenHome, req.Name)
	if err != nil {
		return true // never configured here: nothing says the image matches
	}
	return personal.Version != m.Metadata.Version
}

// Apply performs the plan, in the order spec §9.2 fixes, and commits last.
//
// The ordering is the whole safety property of a resumable installation:
//
//  1. the fresh global configuration, when the den home has none — everything
//     after it needs a home that loads;
//  2. the `applying` receipt, BEFORE the first mutation. It is what makes an
//     interrupted run recognizable: consumers refuse while it is there, and
//     `den source configure` resumes from it;
//  3. credentials, then the machine's network policy, then the images — a
//     build pulls through the registry credential and needs the egress;
//  4. the source checkout is installed;
//  5. the personal configuration, with the confirmed repository mappings;
//  6. the final receipt, LAST. It is the commit marker: until it is written,
//     the machine is in a state den knows how to finish.
//
// A managed resource that fails stops the run and leaves the applying receipt
// in place. Missing working repositories never stop it (spec §9.2).
func (s Service) Apply(ctx context.Context, req Request, plan *Plan, out, errOut io.Writer) (*Result, error) {
	if req.FreshGlobalConfig != nil {
		if err := s.writeFreshGlobalConfig(req); err != nil {
			return nil, err
		}
	}

	digest, err := source.ManifestDigest(req.Candidate.Root)
	if err != nil {
		return nil, err
	}
	previous := ""
	if p, err := source.LoadPersonal(req.DenHome, req.Name); err == nil {
		previous = p.Version
	}
	applying := source.Receipt{
		Status:          source.StatusApplying,
		PreviousVersion: previous,
		TargetVersion:   plan.Version,
		TargetCommit:    req.Candidate.Commit,
		ManifestDigest:  digest,
	}
	if err := source.WriteReceipt(req.DenHome, req.Name, applying); err != nil {
		return nil, err
	}

	state, err := ReadSbxState(ctx, s.Sbx)
	if err != nil {
		return nil, err
	}
	drivers := s.drivers(req, req.Candidate.Manifest, state)
	applied := make([]ResourcePlan, 0, len(drivers))
	for i, d := range drivers {
		planned := plan.Resources[i]
		if planned.Action != ActionUnchanged {
			if err := d.Apply(ctx, req.Answers, out); err != nil {
				return nil, s.failed(req, applying, applied, planned, err)
			}
		}
		// Verified even when nothing was applied: a resumed convergence must
		// PROVE that the resource it is skipping is really there, not trust a
		// plan computed before an interruption.
		o, err := d.Verify(ctx)
		if err != nil || !o.Present {
			return nil, s.failed(req, applying, applied, planned, err)
		}
		verified := planned
		verified.Action, verified.Known, verified.Observed = ActionUnchanged, true, o.Detail
		applied = append(applied, verified)
	}

	// The checkout becomes a real source only now: every managed resource of
	// this version verified.
	if req.Mode == ModeInit || req.Mode == ModeAdd {
		if err := req.Candidate.Install(req.DenHome, req.Name); err != nil {
			return nil, err
		}
	}

	personal, err := s.writePersonal(req, plan)
	if err != nil {
		return nil, err
	}

	final := plan.Receipt(req.Candidate.Commit, digest, s.now())
	final.Resources = receiptResources(applied)
	final.Status = AggregateStatus(applied, plan.Nests)
	if err := source.WriteReceipt(req.DenHome, req.Name, final); err != nil {
		return nil, err
	}
	return &Result{Status: final.Status, Personal: *personal, Receipt: final}, nil
}

// failed records what DID converge before returning, so a later
// `den source configure` can skip it, and wraps the cause in the error
// contract of spec §12.3.
func (s Service) failed(req Request, applying source.Receipt, applied []ResourcePlan,
	failing ResourcePlan, cause error) error {

	applying.Resources = receiptResources(applied)
	// Best effort: the convergence already failed, and a receipt that could
	// not be updated must not replace the real cause in the message.
	_ = source.WriteReceipt(req.DenHome, req.Name, applying)

	var resErr *ResourceError
	if errors.As(cause, &resErr) {
		return cause
	}
	return &ResourceError{
		Resource:  failing.ID,
		Observed:  failing.Observed,
		Expected:  failing.Expected,
		Remaining: "the resources already applied are recorded; the rest were not attempted",
		Resume:    fmt.Sprintf("run `den source configure %s`", req.Name),
		Cause:     cause,
	}
}

// writePersonal MERGES the confirmed mappings into the existing personal
// configuration rather than replacing it.
//
// Replacing would rewrite the paths the user authored: den discovers absolute
// directories, while a hand-written `~/Development/api` must survive a
// configure untouched — the reason Personal keeps paths as written and
// ExpandedRepos is a separate view.
func (s Service) writePersonal(req Request, plan *Plan) (*source.Personal, error) {
	personal := source.Personal{Version: plan.Version, Repos: map[string]string{}}
	if existing, err := source.LoadPersonal(req.DenHome, req.Name); err == nil {
		maps.Copy(personal.Repos, existing.Repos)
	}
	maps.Copy(personal.Repos, plan.ConfirmedRepos())
	if len(personal.Repos) == 0 {
		personal.Repos = nil
	}
	if err := source.WritePersonal(req.DenHome, req.Name, personal); err != nil {
		return nil, err
	}
	return &personal, nil
}

// writeFreshGlobalConfig writes the source-aware config.yaml, and only when
// the home really has none: an existing global configuration is preserved byte
// for byte (spec §8), so a `den init --source` on a working den can never
// rewrite the user's settings.
func (s Service) writeFreshGlobalConfig(req Request) error {
	path := config.GlobalPath(req.DenHome)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(req.DenHome, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", req.DenHome, err)
	}
	if err := os.WriteFile(path, req.FreshGlobalConfig, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

func (s Service) now() time.Time {
	if s.Now == nil {
		return time.Time{}
	}
	return s.Now()
}
