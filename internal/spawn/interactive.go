package spawn

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/PillowPillow/den/internal/nest"
)

// StdinIsTerminal reports whether den's standard input is a terminal.
//
// ONE LINE, on purpose, and exported so the wiring site names it: this is the
// only part of `-i` that no test can exercise (a test has no tty, and a suite
// that acquired one would stop being hermetic). Everything around it — the
// checklist, the toggles, the refusals — takes an io.Reader and is tested.
// Growing this function is how that boundary gets lost.
func StdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// nonInteractiveEquivalents is repeated in every refusal of `-i` on purpose: a
// user who cannot use the checklist needs the way that works, in the same
// breath, not a pointer to `--help`.
const nonInteractiveEquivalents = "`--only repo,...` and `--without repo,...` make the same selection without a prompt"

// interactiveWithout runs the `-i` checklist and returns its answer AS A
// `--without` LIST.
//
// Translating to the existing flag rather than building a second selection path
// is the whole design: nest.Resolve keeps applying the one rule it already
// owns (required repos always mounted, short names unique), and `-i` is just
// another way to fill its input. That is what makes "-i produces the same
// sandbox as the equivalent --without" true by construction rather than by
// coincidence — see TestInteractiveProducesTheSameArgvAsTheEquivalentWithout.
func interactiveWithout(d Deps, n *nest.Nest) ([]string, error) {
	// Nothing to ask comes FIRST, before the terminal check: a nest with no
	// optional repo needs no answer, so it needs no terminal either — `den api
	// -i --detach` from a script keeps working, and says why it asked nothing
	// rather than drawing an empty list.
	if !hasOptionalRepo(n.Repos) {
		fmt.Fprintf(d.Out, "nest %s declares no optional repo: nothing to choose, every repo is mounted\n", n.Name)
		return nil, nil
	}
	// A nil IsTTY is "no terminal", never "assume one": an unwired probe must
	// take the clean refusal below, not hang the spawn on a read nobody
	// answers.
	if d.IsTTY == nil || !d.IsTTY() {
		return nil, fmt.Errorf(
			"-i: no terminal on den's input — the checklist has nobody to ask, and reading anyway would "+
				"block a pipe or a CI job forever; %s", nonInteractiveEquivalents)
	}
	in := d.In
	if in == nil {
		// Belt and braces: IsTTY answered yes, so something IS attached, but a
		// caller that wired the probe and forgot the stream must not panic
		// mid-sequence.
		in = os.Stdin
	}
	return promptOptionalRepos(d.Out, in, n.Name, n.Repos)
}

// selectionFlagsInPlay names the repo-selection flag `-i` collides with, or ""
// when there is none. `--without` is named first when both are present: the
// pair is already mutually exclusive downstream (nest.Resolve), so naming one
// is enough to point at the contradiction with `-i`.
func selectionFlagsInPlay(o Options) string {
	switch {
	case len(o.Without) > 0:
		return "--without"
	case len(o.Only) > 0:
		return "--only"
	}
	return ""
}

func hasOptionalRepo(repos []nest.Repo) bool {
	for _, r := range repos {
		if r.Optional {
			return true
		}
	}
	return false
}

// promptOptionalRepos draws the checklist of a nest's OPTIONAL repos and reads
// the toggles until the user confirms. It returns the short names of the repos
// left unchecked — a `--without` list.
//
// Required repos are neither listed nor numbered (spec §6.2): they are always
// mounted, and numbering them would make "1" designate different repos
// depending on how many required ones happen to precede it.
//
// bufio.Scanner, no TUI library. `cobra` and `yaml.v3` are den's only
// dependencies and that is a claimed property (a static binary, HANDOFF §8);
// what this checklist needs — print a list, read a line, toggle — is a dozen
// lines of stdlib. A TUI library would buy cursor movement and colours for the
// price of the one property the project advertises.
func promptOptionalRepos(out io.Writer, in io.Reader, nestName string, repos []nest.Repo) ([]string, error) {
	optional := make([]nest.Repo, 0, len(repos))
	for _, r := range repos {
		if r.Optional {
			optional = append(optional, r)
		}
	}

	// Everything checked is the starting point: `-i` confirmed as-is must
	// produce exactly what `den <nest>` alone produces.
	keep := make([]bool, len(optional))
	for i := range keep {
		keep[i] = true
	}

	fmt.Fprintf(out, "nest %s: %d optional repo(s) — required repos are always mounted\n",
		nestName, len(optional))

	s := bufio.NewScanner(in)
	for {
		for i, r := range optional {
			box := " "
			if keep[i] {
				box = "x"
			}
			fmt.Fprintf(out, "  %d [%s] %s\n", i+1, box, r.Name())
		}
		fmt.Fprintf(out, "toggle by number (space-separated), empty line to confirm — %s\n> ",
			nonInteractiveEquivalents)

		if !s.Scan() {
			if err := s.Err(); err != nil {
				return nil, fmt.Errorf("-i: reading the selection: %w", err)
			}
			// EOF before a confirmation. Confirming the current state here
			// would be den deciding for the user — and this decision creates a
			// microVM with a set of repos nobody chose.
			return nil, fmt.Errorf(
				"-i: input ended before the selection was confirmed (a pipe, a closed terminal) — "+
					"nothing was spawned; %s", nonInteractiveEquivalents)
		}
		line := strings.TrimSpace(s.Text())
		if line == "" {
			break
		}
		toggles, err := parseToggles(line, len(optional))
		if err != nil {
			// The WHOLE line is rejected, nothing applied: acting on the valid
			// half of "2 zzz" would toggle something the user cannot see they
			// asked for.
			fmt.Fprintf(out, "  %v — nothing changed\n", err)
			continue
		}
		for _, i := range toggles {
			keep[i] = !keep[i]
		}
	}

	var without []string
	for i, r := range optional {
		if !keep[i] {
			without = append(without, r.Name())
		}
	}
	return without, nil
}

// parseToggles turns a line of numbers into zero-based indexes, or returns the
// first entry it could not read. It is all-or-nothing by contract: the caller
// applies the result only when the whole line is valid.
func parseToggles(line string, count int) ([]int, error) {
	fields := strings.Fields(line)
	out := make([]int, 0, len(fields))
	for _, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			return nil, fmt.Errorf("%q is not a number (expected 1 to %d, or an empty line to confirm)", f, count)
		}
		if n < 1 || n > count {
			return nil, fmt.Errorf("%q is outside the list (expected 1 to %d)", f, count)
		}
		out = append(out, n-1)
	}
	return out, nil
}
