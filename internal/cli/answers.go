package cli

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/converge"
	"github.com/PillowPillow/den/internal/source"
)

// collectInitialAnswers produces the transient answers of ONE onboarding run,
// from `--answers <file>` or from the terminal.
//
// Both paths end in the same converge.Answers, and that is the contract this
// function exists for: a CI running `--answers` must exercise the same
// planner, the same applier and the same validation as a human answering
// prompts. The only difference between them is where the strings come from.
//
// A credential is read ONLY when the source declares it and the answers do not
// already carry it. Nothing is prompted "just in case": den reads a secret
// because a resource needs it (spec §5.3).
func collectInitialAnswers(cmd *cobra.Command, d Deps, m *source.Manifest,
	answersPath string) (converge.Answers, error) {

	var a converge.Answers
	if answersPath != "" {
		var err error
		if a, err = converge.LoadAnswers(answersPath, d.getenv()); err != nil {
			return converge.Answers{}, err
		}
		if err := converge.ValidateAnswers(m, a); err != nil {
			return converge.Answers{}, fmt.Errorf("%s: %w", answersPath, err)
		}
	}

	missing := converge.MissingCredentials(m, a)
	needsRoots := len(a.RepositoryRoots) == 0

	// Nothing left to ask: a fully-answered non-interactive run never touches
	// the terminal, so `den init --source ... --answers f --yes` works with no
	// tty at all — a cron job, a CI step, a provisioning script.
	if len(missing) == 0 && !needsRoots {
		return a, nil
	}
	if d.IsTTY == nil || !d.IsTTY() {
		return converge.Answers{}, fmt.Errorf(
			"%s — den has no terminal to ask on: pass `--answers <file>` naming the repository "+
				"roots and the environment variables holding the credentials, and `--yes` to apply "+
				"the printed plan", whatIsMissing(missing, needsRoots))
	}

	in := bufio.NewReader(cmd.InOrStdin())
	out := cmd.OutOrStdout()
	if needsRoots {
		roots, err := askRepositoryRoots(out, in)
		if err != nil {
			return converge.Answers{}, err
		}
		a.RepositoryRoots = roots
	}
	for _, name := range missing {
		prompt := m.Inputs.Credentials[name].Prompt
		if strings.TrimSpace(prompt) == "" {
			prompt = name
		}
		if d.ReadSecret == nil {
			return converge.Answers{}, fmt.Errorf(
				"credential %q must be typed, and no secret reader is wired — this is a den defect; "+
					"pass `--answers <file>` with `from_env:` as a workaround", name)
		}
		// Never echoed, and never carried in a flag: an argv is visible to
		// every process on the machine (spec §5.3).
		value, err := d.ReadSecret(prompt + ": ")
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

// whatIsMissing names what the run would have had to ask for. Assembled here
// so the no-terminal refusal says which answer is absent rather than "some
// input" — the user has to know whether to add roots, credentials, or both.
func whatIsMissing(missing []string, needsRoots bool) string {
	switch {
	case needsRoots && len(missing) > 0:
		return fmt.Sprintf("this source needs the credentials %v and the directories to look for its "+
			"working repositories in", missing)
	case needsRoots:
		return "den needs the directories to look for this source's working repositories in"
	default:
		return fmt.Sprintf("this source needs the credentials %v", missing)
	}
}

// askRepositoryRoots reads the directories to scan. They are answers to ONE
// execution: den never stores them (spec §7.2), which is also why the prompt
// says what they are for rather than presenting them as a setting.
func askRepositoryRoots(out io.Writer, in *bufio.Reader) ([]string, error) {
	fmt.Fprintln(out, "Where do your working repositories live? (space-separated directories, "+
		"empty line to skip — den only looks, it never clones)")
	fmt.Fprint(out, "> ")
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
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
func confirm(cmd *cobra.Command, d Deps, yes bool) (bool, error) {
	if yes {
		return true, nil
	}
	if d.IsTTY == nil || !d.IsTTY() {
		fmt.Fprintln(cmd.OutOrStdout(),
			"\nnothing was applied: den has no terminal to confirm on — rerun with `--yes` to apply "+
				"the plan above")
		return false, nil
	}
	fmt.Fprint(cmd.OutOrStdout(), "\napply this plan? [y/N] ")
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && line == "" {
		return false, fmt.Errorf("reading the confirmation: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	}
	fmt.Fprintln(cmd.OutOrStdout(), "nothing was applied")
	return false, nil
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

	in := bufio.NewReader(cmd.InOrStdin())
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
		fmt.Fprint(out, "  choose a number, type a path, or press enter to leave it unmapped > ")

		line, err := in.ReadString('\n')
		if err != nil && line == "" {
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
