package cli

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/converge"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/source"
)

// convergenceFlags are the flags the three onboarding entry points share.
// One struct, so `den init --source`, `den source add` and `den source
// configure` cannot drift into three spellings of the same question.
type convergenceFlags struct {
	Source  string
	Name    string
	Answers string
	Yes     bool
}

// bind declares the flags on a command. withSource and withName are explicit
// because the three commands differ exactly there: `configure` names an
// INSTALLED source (its argument), so neither a URL nor an install name is a
// choice it still has.
func (f *convergenceFlags) bind(cmd *cobra.Command, withSource, withName bool) {
	if withSource {
		cmd.Flags().StringVar(&f.Source, "source", "",
			"install this team source (URL) instead of the shipped example")
	}
	if withName {
		cmd.Flags().StringVar(&f.Name, "name", "",
			"install name (default: the manifest's metadata.name, or the URL's basename)")
	}
	cmd.Flags().StringVar(&f.Answers, "answers", "",
		"answer file: repository roots, and the environment variables holding the credentials")
	cmd.Flags().BoolVar(&f.Yes, "yes", false, "apply the printed plan without asking")
}

// denVersion returns this binary's version for the manifest's `requires.den`
// floor. Same shape as getenv(): nil means "unknown", never "read the build
// info" — a test that forgot to wire it must get the UnknownVersionError
// warning path, not whatever version the test binary happens to carry.
func (d Deps) denVersion() string {
	if d.DenVersion == nil {
		return ""
	}
	return d.DenVersion()
}

// runConvergence is the ONE flow behind `den init --source`, `den source add`
// and `den source configure`. The mode says where the content comes from;
// everything below is identical for all three, which is the whole point of
// spec §9.3 — a user who has read one plan has read them all.
//
// The order is fixed: collect the answers, plan, settle what discovery could
// not decide alone, plan AGAIN with those choices, print, confirm, apply.
// Nothing before the confirmation writes to the den home or the machine.
func runConvergence(cmd *cobra.Command, d Deps, mode converge.Mode, home, name string,
	c *source.Candidate, f convergenceFlags, fresh []byte) error {

	// The SAME runner as everywhere else in den, asked for its secret-carrying
	// half. A second injected field would be a second runner to keep in sync —
	// the one thing internal/cli's wiring does not have (see Deps.Sbx).
	secrets, ok := d.Sbx.(sbx.SecretRunner)
	if !ok {
		return fmt.Errorf(
			"converging source %q needs an sbx runner that can carry a secret off argv, and the one "+
				"wired here cannot — this is a den defect", name)
	}
	svc := converge.Service{Git: d.Git, Sbx: d.Sbx, Secrets: secrets, Now: time.Now}
	req := converge.Request{
		Mode:              mode,
		DenHome:           home,
		Name:              name,
		Candidate:         c,
		DenVersion:        d.denVersion(),
		FreshGlobalConfig: fresh,
	}

	// Ask the MACHINE before asking the human. collectInitialAnswers below
	// prompts for the repository roots and for every declared credential, and
	// on a machine den cannot observe all of that is collected only to be
	// thrown away by the refusal further down — a secret typed into a run that
	// was already lost, which the human then retypes on the retry. That is the
	// shape the 2026-08-18 report named, and it survived the refusal that was
	// added for it because that refusal sits after the questions.
	//
	// The guards on the plans below STAY. This probe and they cover different
	// windows: the sbx daemon can die between this read and the first Plan, or
	// between the two Plans, and an observation is a fact about a moment rather
	// than a property of the run.
	if err := svc.Observable(cmd.Context(), req); err != nil {
		return err
	}

	answers, err := collectInitialAnswers(cmd, d, c.Manifest, f.Answers, f.Yes)
	if err != nil {
		return err
	}
	req.Answers = answers

	plan, err := svc.Plan(cmd.Context(), req)
	if err != nil {
		return err
	}
	// An unobservable machine stops the command HERE — before the repository
	// questions below, before the plan is printed, before the confirmation.
	//
	// It covers the window the probe above cannot: the machine answered when den
	// asked, and stopped answering by the time Plan read it. Plan itself does not
	// refuse (its godoc says why: `den source status` and `den doctor` must still
	// render such a machine), so this is where that state becomes a refusal.
	//
	// The shape reported on 2026-08-18 is what its absence cost: sbx demands a
	// one-time `sbx policy init <profile>`, a laptop that never ran it answered
	// nothing to `policy ls`, and den printed a plan whose every line read
	// `unknown`, prompted `apply this plan? [y/N]`, accepted the `y` — and only
	// then refused, from inside Apply, with the applying receipt already written.
	if err := svc.Unobservable(req, plan); err != nil {
		return err
	}
	// A second planning pass, and only when discovery left something open: the
	// choices are written into the SAME Answers the first pass read, so the plan
	// the user confirms is the plan den would compute from an answer file
	// carrying those choices. Skipped when there is nothing to settle, since a
	// pass rescans every repository root.
	if len(converge.UnconfirmedMatches(plan.RepoMatches)) > 0 {
		if err := resolveRepoChoices(cmd, d, plan.RepoMatches, &req.Answers); err != nil {
			return err
		}
		if plan, err = svc.Plan(cmd.Context(), req); err != nil {
			return err
		}
		// And check THAT plan too: it is the one that gets printed, confirmed
		// and applied. The human has just spent time answering repository
		// choices, which is exactly long enough for the sbx daemon to stop
		// answering — and an all-`unknown` plan confirmed here, then refused
		// from inside Apply with the applying receipt already written, is the
		// very shape the guard above exists to prevent. Checking only the first
		// pass would leave that door open.
		if err := svc.Unobservable(req, plan); err != nil {
			return err
		}
	}

	out := cmd.OutOrStdout()
	converge.RenderPlan(out, plan)
	printRepoMigration(out, home, name, plan)

	confirmed, err := confirm(cmd, d, f.Yes, plan.Changes())
	if err != nil || !confirmed {
		return err
	}

	result, err := svc.Apply(cmd.Context(), req, plan, out, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "\nsource %q is %s at version %s — `den up %s:<nest>`\n",
		name, result.Status, plan.Version, name)
	return nil
}

// printRepoMigration tells the user how to move a legacy `repos:` entry into
// the source's own personal configuration.
//
// It prints, and copies NOTHING. A manifested source resolves its repository
// keys through <den home>/source-config/<name>.yaml only (spec §6), so a key
// still sitting in the global config.yaml would silently stop being used; but
// den cannot know that the global entry and the source's key mean the same
// repository — two teams may use "api" for two different things. Copying would
// mount one of them into the other's sandboxes without anyone deciding it.
//
// Only the keys discovery did NOT confirm are listed: a repository den found
// on the machine is already in the plan, and telling the user to migrate it
// would name work that is done.
func printRepoMigration(out io.Writer, home, name string, p *converge.Plan) {
	// Best effort, deliberately: this is a diagnostic. A global configuration
	// that does not load is a fault the commands that need it report with their
	// own message — it must not turn an onboarding into a config error.
	g, err := config.LoadGlobal(home)
	if err != nil || len(g.Repos) == 0 {
		return
	}
	movable := map[string]string{}
	for _, m := range p.RepoMatches {
		if m.Confirmed {
			continue
		}
		if path, ok := g.Repos[m.Requirement.Key]; ok {
			movable[m.Requirement.Key] = path
		}
	}
	if len(movable) == 0 {
		return
	}
	fmt.Fprintf(out, "\nREPOSITORY MAPPINGS TO MIGRATE\n")
	fmt.Fprintf(out, "%s no longer supplies the repositories of a manifested source. den copies "+
		"nothing — check these are the same repositories, then add them to %s yourself:\n\n",
		config.GlobalPath(home), source.PersonalPath(home, name))
	fmt.Fprintln(out, "repos:")
	for _, key := range slices.Sorted(maps.Keys(movable)) {
		fmt.Fprintf(out, "  %s: %s\n", key, movable[key])
	}
}

// requireManifest is the shared refusal for a source that carries no contract.
// Named once: `den init --source`, `den source add` and `den source configure`
// all reach it, and a user who met it under one command must recognize it under
// the others.
func requireManifest(c *source.Candidate, name, remedy string) error {
	if c.Manifest != nil {
		return nil
	}
	return fmt.Errorf("source %q has no %s — %s", name, source.ManifestFile, remedy)
}
