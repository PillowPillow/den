package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/converge"
	"github.com/PillowPillow/den/internal/prompt"
	"github.com/PillowPillow/den/internal/source"
)

// loadAnswerFile reads and validates `--answers <file>`, and answers the empty
// Answers when no file was given.
//
// It is a HALF of what collectInitialAnswers used to be, split out so
// runConvergence can run it before it touches the machine. den refuses on what
// files alone decide before it observes anything (the spawn sequence's own
// doctrine, spec §6): a malformed answer file is a fault den holds without
// asking sbx a single question, and reporting the machine first would send the
// user to fix sbx, re-run, and only then learn the file was wrong.
//
// Both halves stay one contract — the answers a file produces and the answers a
// terminal produces are the same converge.Answers, which is what makes a CI
// run a real rehearsal of a human one (spec §7.1).
func loadAnswerFile(d Deps, m *source.Manifest, answersPath string) (converge.Answers, error) {
	if answersPath == "" {
		return converge.Answers{}, nil
	}
	a, err := converge.LoadAnswers(answersPath, d.getenv())
	if err != nil {
		return converge.Answers{}, err
	}
	if err := converge.ValidateAnswers(m, a); err != nil {
		return converge.Answers{}, fmt.Errorf("%s: %w", answersPath, err)
	}
	return a, nil
}

// collectInitialAnswers completes the transient answers of ONE onboarding run:
// it takes what loadAnswerFile already read and asks the terminal for the rest.
//
// a is that loaded file, empty when there was none, and fromFile says which —
// the two are not the same thing, and only the caller still knows. A file that
// carries no `repository_roots:` has ANSWERED that question with "none"; no
// file at all leaves it open.
//
// Both paths end in the same converge.Answers, and that is the contract this
// function exists for: a CI running `--answers` must exercise the same
// planner, the same applier and the same validation as a human answering
// prompts. The only difference between them is where the strings come from.
//
// A credential is read when the source DECLARES it and the answers do not
// already carry it — not when a resource turns out to need applying.
//
// That is spec §9.2's ordering (answers at step 7, inspection at step 8), and
// it costs something worth naming: an interactive `den source configure` on a
// fully converged machine still asks for the token, because den has not looked
// at the machine yet when it asks. §5.3's "read a value only when a resource
// needs it" would require planning first and prompting after, which would put
// a terminal prompt in the middle of the plan the user is about to read. The
// answer file is what makes that cost disappear for anyone it bothers.
//
// The ONE exception is the no-terminal REFUSAL below: there, den has nobody
// left to ask, so before giving up it checks the machine — the answers alone
// are not enough to tell "missing" from "already configured", and a resumed
// `den source configure --yes` (spec §11.3) must not refuse over a credential
// sbx already holds. That check costs an `sbx secret ls -g`/`policy ls`, paid
// only on the branch that would otherwise refuse.
//
// runConvergence's own probe does NOT make that read redundant, and does not
// make this branch unreachable either: the probe proves the machine answered
// at one moment, and the daemon can stop answering before this read — the same
// window argument that keeps a check on each planning pass. What the probe does
// change is who reports a machine that was ALREADY blind when the command
// started: that is the probe's refusal now, which names the read rather than
// sending the user to find a terminal they do not need.
//
// yes is `--yes`, threaded in ONLY for that same refusal, and only for the
// half of it a plan-only run does not need: a run without `--yes` never
// applies anything even when confirmed (confirm() itself refuses without a
// terminal and without `--yes`), so a credential no answer can ever supply
// (sbx_github) is not this run's problem yet — Service.Plan's own doctrine is
// that the plan still lists everything the source declares, and a refusal
// here would leave a plan-only run with nothing to print. A credential an
// ANSWER could have supplied stays refused regardless of yes, exactly as
// before this parameter existed (TestCollectInitialAnswersRefusesWithoutATerminal
// passes no `--yes` at all and still expects the refusal).
func collectInitialAnswers(cmd *cobra.Command, d Deps, m *source.Manifest,
	a converge.Answers, fromFile, yes bool) (converge.Answers, error) {

	missing := converge.MissingCredentials(m, a)
	// An answer FILE is the answer. A `repository_roots:` it does not carry
	// means "none" — not "ask me": prompting past a file the user wrote would
	// make `--answers` mean "some of the answers", and would leave a source
	// whose nests declare no repository at all impossible to install without a
	// terminal, over roots den would never look in.
	needsRoots := len(a.RepositoryRoots) == 0 && !fromFile

	// Nothing left to ask FROM AN ANSWER, and there is a terminal standing by
	// for the one credential no answer can ever supply (sbx_github: manifest.go
	// refuses `value_from` for it, so sbx always collects it interactively,
	// through the Attach Apply hands to). An interactive run pays no machine
	// check for this — it never touches the terminal PAST this point unless a
	// prompt below actually needs it — so this fast path costs nothing new.
	if len(missing) == 0 && !needsRoots && d.IsTTY != nil && d.IsTTY() {
		return a, nil
	}

	if d.IsTTY == nil || !d.IsTTY() {
		// Before refusing, ask the MACHINE what it already holds — the same
		// per-type question Service.Plan's credential driver Inspect asks
		// (converge.CredentialPresent), so this refusal and the plan the user
		// would see next can never disagree about what is present. A credential
		// still absent stays refused; one already configured drops out.
		stillMissing, needsTerminal, obsErr := stillMissingCredentials(cmd.Context(), d, m, missing)
		if !yes {
			// Nothing would apply on this run even if den asked for confirmation
			// (confirm() only ever returns true here on `yes`, since there is no
			// terminal to type "y" on) — so an absent github credential is not
			// yet a reason to refuse; the printed plan is what tells the user it
			// needs one.
			needsTerminal = nil
		}
		if len(stillMissing) == 0 && len(needsTerminal) == 0 && !needsRoots {
			return a, nil
		}
		return converge.Answers{}, noTerminalRefusal(stillMissing, needsTerminal, needsRoots, obsErr)
	}

	// A terminal is present (we are past the no-terminal branch above), but
	// nothing is wired to ask on it — that is a WIRING defect, not a user
	// error, and it must refuse HERE, before askRepositoryRoots or the
	// credential loop below ever call a nil Prompter. One guard above both
	// call sites, naming BOTH remedies at once, gated on the question(s)
	// this run would actually need to ask: nobody is sent to the wrong key
	// in the right file, and the guard stays silent when neither question
	// would have been asked (M1/M2 review, Task 4).
	if d.Prompt == nil && (needsRoots || len(missing) > 0) {
		return converge.Answers{}, fmt.Errorf(
			"this run has a terminal but no prompter is wired — this is a den defect; " +
				"pass `--answers <file>` supplying `repository_roots:` and " +
				"`credentials.<name>.from_env:` as a workaround")
	}

	if needsRoots {
		roots, err := askRepositoryRoots(cmd.Context(), d.Prompt)
		if err != nil {
			return converge.Answers{}, err
		}
		a.RepositoryRoots = roots
	}
	for _, name := range missing {
		label := m.Inputs.Credentials[name].Prompt
		if strings.TrimSpace(label) == "" {
			label = name
		}
		// Never echoed, and never carried in a flag: an argv is visible to
		// every process on the machine (spec §5.3).
		value, err := d.Prompt.Secret(cmd.Context(), prompt.SecretRequest{Prompt: label})
		if err != nil {
			return converge.Answers{}, fmt.Errorf("reading %s: %w", name, err)
		}
		if strings.TrimSpace(value) == "" {
			return converge.Answers{}, fmt.Errorf(
				"credential %q: nothing was typed — den configures no empty credential, since sbx "+
					"would then hold one nobody can use", name)
		}
		if a.Credentials == nil {
			a.Credentials = map[string]converge.CredentialAnswer{}
		}
		a.Credentials[name] = converge.CredentialAnswer{Value: value}
	}
	return a, nil
}

// stillMissingCredentials narrows `missing` — the declared inputs the ANSWERS
// do not cover — down to the inputs the MACHINE does not cover either, and
// separately names the declared sbx_github credentials that are absent.
//
// github is named apart because it never appears in `missing` at all: it
// takes no `value_from` (manifest.go's own refusal), so it has no input name
// to be missing FROM. Its own absence is a resource-level fact, checked here
// directly against the same observation.
//
// An input `missing` names but no resource references is left in the
// refusal untouched: den has no resource driver to ask about it, so it
// cannot claim the machine already holds it — the same conservative default
// Service.Plan's unobservedResources falls back on when it cannot observe at
// all, applied here to a single input that happens to have no observer.
//
// A machine den cannot observe — Sbx unset (a test wiring gap; every real
// caller injects one) or ReadSbxState itself failing — is treated the same
// way: state stays nil, converge.CredentialPresent then answers false for
// everything, and every declared credential resource stays in the refusal.
// That is the safe direction: the reverse could let a resume skip a
// credential nobody actually configured, on the strength of a read that
// never happened.
//
// obsErr carries WHY state stayed nil, when the reason is a real observation
// failure — ReadSbxState returning an error — rather than the test-only
// wiring gap. M1 (final whole-branch review, 2026-08-16): before this
// return value existed, a ReadSbxState failure with a manifest declaring
// sbx_github produced "the credential "github" cannot be configured without
// a terminal", sending the user to find a terminal they do not need — the
// VERDICT was right (fail-closed) but the REASON named was wrong. obsErr
// lets noTerminalRefusal name the actual cause instead. Nil when d.Sbx is
// unset (nothing was attempted, so there is no error to report) or when the
// read succeeded.
func stillMissingCredentials(ctx context.Context, d Deps, m *source.Manifest, missing []string) (
	stillMissing, needsTerminal []string, obsErr error) {

	var state *converge.SbxState
	if d.Sbx != nil {
		s, err := converge.ReadSbxState(ctx, d.Sbx)
		if err != nil {
			obsErr = err
		} else {
			state = s
		}
	}

	referenced := map[string]bool{}
	needed := map[string]bool{}
	for _, res := range m.Resources.Credentials {
		present := state != nil && converge.CredentialPresent(res, state)
		if res.Type == source.CredentialGitHub {
			if !present {
				needsTerminal = append(needsTerminal, res.ID)
			}
			continue
		}
		name := res.ValueFrom.Credential
		if name == "" {
			continue // refused at manifest load (manifest.go); nothing to key on
		}
		referenced[name] = true
		if !present {
			needed[name] = true
		}
	}

	for _, name := range missing {
		if !referenced[name] || needed[name] {
			stillMissing = append(stillMissing, name)
		}
	}
	return stillMissing, needsTerminal, obsErr
}

// noTerminalRefusal explains, term by term, what den still needs and how to
// supply it — an answer-file key for what an answer file CAN carry, and a
// distinct sentence for a credential it cannot: sbx_github takes no
// `value_from` (manifest.go's refusal, kept by Task 4's ruling), so sbx
// always collects it through the terminal Apply hands to Attach, and no
// `credentials.<name>.from_env` line exists to point at instead — pointing at
// one anyway would send the user to fix an answer file that could never have
// worked.
//
// obsErr, when non-nil, is stillMissingCredentials' own ReadSbxState
// failure: the reason needsTerminal was populated by the safe fallback
// rather than a genuine "absent on the machine" read. M1 review fix — den
// still refuses (the verdict does not change: a credential it could not
// observe is treated as absent), but the sentence now names the read
// failure instead of sending the user to find a terminal that would not
// have helped.
func noTerminalRefusal(missing, needsTerminal []string, needsRoots bool, obsErr error) error {
	var need []string
	if needsRoots {
		need = append(need,
			"the directories to look for its working repositories in (`repository_roots:`)")
	}
	for _, name := range missing {
		need = append(need, fmt.Sprintf(
			"the credential %q (`credentials.%s.from_env` in the answer file)", name, name))
	}

	msg := "den has no terminal to ask on"
	if len(need) > 0 {
		msg += fmt.Sprintf(" and this source still needs %s — pass `--answers <file>` supplying it, "+
			"and `--yes` to apply the printed plan", strings.Join(need, "; "))
	}
	for _, id := range needsTerminal {
		if obsErr != nil {
			msg += fmt.Sprintf(
				"; den could not read sbx (%v), so it must assume the credential %q is absent — "+
					"fix the read (e.g. `den doctor`), then run this command again from a terminal so "+
					"sbx can collect it interactively (e.g. `sbx secret set github`)",
				obsErr, id)
			continue
		}
		msg += fmt.Sprintf(
			"; the credential %q cannot be configured without a terminal — sbx collects it "+
				"interactively (e.g. `sbx secret set github`); run this command again from one",
			id)
	}
	return errors.New(msg)
}

// askRepositoryRoots reads the directories to scan. They are answers to ONE
// execution: den never stores them (spec §7.2), which is also why the question
// says what they are for rather than presenting them as a setting.
//
// The Prompter returns the line RAW. Splitting on whitespace, expanding `~` and
// validating each entry stay here, exactly where they were: a Prompter that
// knew what a path is would be a second judge of den's config, and there is one
// judge (config.ExpandPath).
//
// ctx is the command's own (cmd.Context()), threaded rather than created here:
// this question is the longest den ever blocks on, and root.go's
// signal.NotifyContext is what ends that wait on ^C or SIGTERM. A
// context.Background() at this call site would silently opt the one prompt
// that can hang forever out of the shutdown path.
func askRepositoryRoots(ctx context.Context, p prompt.Prompter) ([]string, error) {
	// Deliberate redundancy with collectInitialAnswers' own guard above its
	// call site: THAT guard names both remedies for the run and is what a
	// human actually reads; THIS one defends the function itself, so a
	// future caller reaching askRepositoryRoots directly (it is exported to
	// this package) cannot bypass a guard that lives only at today's one
	// call site — the exact failure mode that produced this defect. Same
	// belt-and-braces the repo already accepts between huhui's own gate and
	// its callers' IsTTY checks, and the same reason spawn.promptOptionalRepos
	// guards inside the callee.
	if p == nil {
		return nil, fmt.Errorf(
			"the repository-roots question has no prompter to ask on — this is a den defect; " +
				"pass `--answers <file>` supplying `repository_roots:` as a workaround")
	}
	line, err := p.Line(ctx, prompt.LineRequest{
		Question: "Where do your working repositories live? (space-separated directories, " +
			"empty line to skip — den only looks, it never clones)",
	})
	if err != nil {
		return nil, fmt.Errorf("reading the repository roots: %w", err)
	}
	var roots []string
	for _, field := range strings.Fields(line) {
		expanded, err := config.ExpandPath(field)
		if err != nil {
			return nil, err
		}
		roots = append(roots, expanded)
	}
	return roots, nil
}

// confirm asks for the go-ahead on a printed plan. `--yes` is the answer of
// someone who has read a plan before (a CI, a scripted install); without a
// terminal and without `--yes`, den prints the plan and applies nothing —
// never a default "yes" (spec §7.1).
//
// changes is Plan.Changes(): a plan with nothing to create or update needs no
// human decision, so the interactive question is skipped and confirm answers
// true on its own. It is checked only INSIDE the terminal branch, never
// before it: the no-terminal refusal above stays exactly as strict as before
// — a run without `--yes` and without a terminal still refuses regardless of
// changes, which is what keeps the two sentences in collectInitialAnswers's
// godoc ("confirm() itself refuses without a terminal and without `--yes`";
// "confirm() only ever returns true here on `yes`") true for every caller
// that reasons about them. Apply itself is UNCHANGED by this: a resource plan
// with nothing to create or update is not "nothing left to do" — ModeInit and
// ModeAdd still install the candidate, and every resource still gets
// verified — so skipping Apply here, not only the prompt, would be wrong.
func confirm(cmd *cobra.Command, d Deps, yes, changes bool) (bool, error) {
	if yes {
		return true, nil
	}
	if d.IsTTY == nil || !d.IsTTY() {
		fmt.Fprintln(cmd.OutOrStdout(),
			"\nnothing was applied: den has no terminal to confirm on — rerun with `--yes` to apply "+
				"the plan above")
		return false, nil
	}
	if !changes {
		return true, nil
	}
	// A nil Prompter is "no way to ask", never "assume the defaults": an
	// unwired double must refuse here rather than let the caller apply a
	// plan nobody confirmed. Same rule as promptOptionalRepos (internal/
	// spawn/interactive.go), one caller down.
	if d.Prompt == nil {
		return false, fmt.Errorf(
			"no prompter is wired to confirm this plan — this is a den defect; " +
				"pass `--yes` as a workaround")
	}
	// The plan is ALREADY on screen, printed by the caller. The question names
	// what it applies to and nothing else: internal/converge/render.go calls
	// that plan the trust boundary, and a prompt that redrew it — or that took
	// the screen and scrolled it away — would turn consent into a guess.
	ok, err := d.Prompt.Confirm(cmd.Context(), prompt.ConfirmRequest{Question: "apply this plan?"})
	if err != nil {
		return false, fmt.Errorf("reading the confirmation: %w", err)
	}
	if !ok {
		fmt.Fprintln(cmd.OutOrStdout(), "nothing was applied")
	}
	return ok, nil
}

// getenv returns the environment reader, defaulting to a reader that finds
// nothing. nil is "no environment", never "read the process's": a test that
// forgot to wire it must fail on a missing variable, not silently pick up the
// developer's own shell.
func (d Deps) getenv() func(string) string {
	if d.Getenv == nil {
		return func(string) string { return "" }
	}
	return d.Getenv
}

// resolveRepoChoices settles the discovery matches den will not act on
// alone — a directory named like the repository, or several candidates — and
// writes the confirmed ones into a.Repos.
//
// Into the ANSWERS, not into a separate result: the second planning pass reads
// the same Answers the first one did, so a choice made here is indistinguishable
// from one an answer file supplied. That is what keeps the interactive and the
// automated flow on one planner.
//
// Nothing is chosen by default. An answer that is skipped leaves the repository
// unmapped, and the nests needing it become not_ready — a state the user can
// fix later with `den source configure`, unlike a wrong directory silently
// mounted into every sandbox.
//
// The candidates are PRINTED to out and only the choice is asked through the
// Prompter. That is the split ConfirmRequest's godoc already states: the caller
// owns the context on screen, the Prompter owns the one line it reads. The
// question used to be a bufio read off cmd.InOrStdin() with the `> ` prompt
// printed by hand, and two verified defects are why it stopped being one
// (PR 82, finding F6):
//
//   - it had no gate of its own beyond the IsTTY check above, and none of the
//     cancelled path: ctrl+c on it was a raw SIGINT killing den mid-run,
//     whereas a Prompter call runs under cmd.Context() and unwinds through
//     the same signal handling as every other question;
//   - prompt.Fake could not see it, which made this — den's fifth interactive
//     question — the only one a test had to script through a byte stream.
//
// A third reason was argued and is NOT claimed here: that a bufio.Reader
// reading past the newline it returns could strand type-ahead in a buffer the
// huh confirmation one planning pass later never sees. The path was never
// reproduced — a terminal in canonical mode returns at most one line per
// read(2), so the type-ahead stays in the tty queue rather than in the buffer,
// and a piped run never reaches this loop at all (IsTTY sends it to the report
// branch above). A hazard the shape carried, not a defect it was measured to
// carry; the two above carry the change on their own.
//
// With it gone, "den's ONE question-asking surface" (internal/prompt/prompt.go)
// is a fact rather than an intention: a convergence reads no stdin at all.
func resolveRepoChoices(cmd *cobra.Command, d Deps, matches []converge.RepoMatch,
	a *converge.Answers) error {

	pending := converge.UnconfirmedMatches(matches)
	if len(pending) == 0 {
		return nil
	}
	out := cmd.OutOrStdout()
	if d.IsTTY == nil || !d.IsTTY() {
		// Not a refusal: a non-interactive run installs what it can and reports
		// the rest. The repositories den could not attribute stay unmapped, and
		// the plan says so — `repos:` in the answer file is how a scripted run
		// answers them.
		for _, m := range pending {
			fmt.Fprintf(out, "repo %s: not confirmed (%s) — name it under `repos:` in the answer file "+
				"to map it\n", m.Requirement.Key, m.Kind)
		}
		return nil
	}

	// Below the non-TTY branch on purpose: the guard is reachable only when den
	// really is about to ask, so a scripted run with no Prompter still gets its
	// report instead of a refusal. Same family as collectInitialAnswers and
	// confirm — a nil Prompter is "no way to ask", never "leave them unmapped in
	// silence", which would strand the nests at not_ready with nothing in the
	// output naming the missing wiring.
	if d.Prompt == nil {
		return fmt.Errorf(
			"the repo-choice question has no prompter to ask on — this is a den defect; " +
				"pass `--answers <file>` supplying `repos:` as a workaround")
	}
	for _, m := range pending {
		candidates := m.Candidates
		if len(candidates) == 0 && m.Path != "" {
			candidates = []string{m.Path}
		}
		fmt.Fprintf(out, "\nrepo %s (%s)\n", m.Requirement.Key, m.Requirement.URL)
		switch m.Kind {
		case converge.MatchName:
			fmt.Fprintln(out, "  a directory carries this name, but its remotes do not confirm it:")
		case converge.MatchAmbiguous:
			fmt.Fprintln(out, "  several directories could be it:")
		}
		for i, c := range candidates {
			fmt.Fprintf(out, "  %d %s\n", i+1, c)
		}

		line, err := d.Prompt.Line(cmd.Context(), prompt.LineRequest{
			Question: fmt.Sprintf("repo %s: choose a number, type a path, or leave empty to keep it unmapped",
				m.Requirement.Key),
		})
		if err != nil {
			return fmt.Errorf("reading the choice for repo %s: %w", m.Requirement.Key, err)
		}
		answer := strings.TrimSpace(line)
		if answer == "" {
			continue
		}
		chosen := answer
		if n, convErr := strconv.Atoi(answer); convErr == nil {
			if n < 1 || n > len(candidates) {
				return fmt.Errorf("repo %s: %q is outside the list (expected 1 to %d, or a path)",
					m.Requirement.Key, answer, len(candidates))
			}
			chosen = candidates[n-1]
		}
		expanded, err := config.ExpandPath(chosen)
		if err != nil {
			return err
		}
		if a.Repos == nil {
			a.Repos = map[string]string{}
		}
		a.Repos[m.Requirement.Key] = expanded
	}
	return nil
}
