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

// LooksInteractive reports whether den appears to have both a terminal to
// read from and a terminal to write to.
//
// ONE LINE-ish, on purpose, and exported so the wiring site names it: this is
// the only part of `-i` (and, since #den-exec, of the `-it` decision in `den
// exec`/`den spawn -- <cmd>`) that no test can exercise — a test has no tty,
// and a suite that acquired one would stop being hermetic. Everything around
// it — the checklist, the toggles, the refusals — takes an io.Reader and is
// tested. Growing this function is how that boundary gets lost.
//
// Requires BOTH descriptors, not just stdin. `os.ModeCharDevice` alone
// answers true for `/dev/null`, `/dev/zero` and friends — not only for a real
// terminal — and a lone stdin check paid for that on `den exec`: measured
// 2026-08-10, `./den exec <sb> -- echo hello < /dev/null` produced NO output
// with rc=0, because `sbx exec -it` with no real terminal behind it silently
// DISCARDS the command's output while still reporting success (spec §14.0).
// `< /dev/null` is the canonical CI and cron stdin, so the single-descriptor
// probe was a data-loss path with a clean exit code.
//
// Requiring stdout to be a char device too is a NARROWING chosen over a
// rigorous `ioctl`-based terminal test — that test needs a syscall this
// module deliberately does not depend on (stdlib + cobra + yaml.v3 only) and
// is deferred to a follow-up issue. It is not exact: `den exec <sb> -- cmd </
// dev/null` with stdout still attached to a real terminal passes this check
// (both descriptors are char devices) and still allocates a tty, because
// `/dev/null` alone cannot be told apart from a terminal by this test. The
// residual false positive is accepted; the false positive this narrowing
// removes (`/dev/null` stdin, redirected stdout) was the one silently losing
// output.
//
// Consequence for `-i`: with stdout redirected, LooksInteractive now answers
// false even when stdin is a real terminal, so the checklist takes its clean
// refusal (interactiveWithout, below) instead of drawing a prompt nobody can
// see. That is a behaviour change from the stdin-only probe, and it is
// coherent on purpose — a checklist the user cannot see is worse than a
// refusal that names the non-interactive equivalent.
func LooksInteractive() bool {
	in, err := os.Stdin.Stat()
	if err != nil || in.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	out, err := os.Stdout.Stat()
	return err == nil && out.Mode()&os.ModeCharDevice != 0
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
func interactiveWithout(d Deps, n *nest.Nest, mapping map[string]string) ([]string, error) {
	// Nothing to ask comes FIRST, before the terminal check: a nest with no
	// optional repo needs no answer, so it needs no terminal either — `den spawn
	// api -i --detach` from a script keeps working, and says why it asked nothing
	// rather than drawing an empty list.
	if !hasOptionalRepo(n.Repos) {
		fmt.Fprintf(d.Out, "nest %s declares no optional repo: nothing to choose, every repo is mounted\n", n.Name)
		return nil, nil
	}
	// A nil IsTTY is "no terminal", never "assume one": an unwired probe must
	// take the clean refusal below, not hang the spawn on a read nobody
	// answers.
	if d.IsTTY == nil || !d.IsTTY() {
		// The prefix follows the entry point: naming `-i` to someone who never
		// typed it sends them looking for a flag they did not use. Both
		// sentences name the same remedy, because there IS only one.
		if n.PromptsForRepos() {
			return nil, fmt.Errorf(
				"nest %s selects its repos at spawn time and there is no terminal on den's input — "+
					"the checklist has nobody to ask, and reading anyway would block a pipe or a CI "+
					"job forever; %s", n.Name, nonInteractiveEquivalents)
		}
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
	return promptOptionalRepos(d.Out, in, n.Name, n.Repos, !n.PromptsForRepos(), mapping)
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
// startChecked is the initial state of every box, and it is NOT cosmetic. `-i`
// starts full, because confirming an -i checklist without touching it must
// produce exactly what `den spawn` alone produces
// (TestInteractiveProducesTheSameArgvAsTheEquivalentWithout). A `select:
// prompt` nest starts EMPTY, because it has no default selection to propose by
// definition — and thirty ticked boxes would turn an empty line into a
// thirty-repo mount.
//
// mapping is the personal `repos:` of config.yaml, used to ANNOTATE the keys
// it does not carry. Annotation only: ticking an unmapped key stays possible,
// and the refusal that follows is resolveRepoKeys', which names the key, the
// file and the clone URL. Refusing the tick here would make this a second
// judge of the mapping, whose single judge is that function.
//
// A nil mapping is NOT a special case, and unmappedNote treats it as none: a
// path-typed repo renders unannotated either way, and every key-typed one
// renders annotated. That is the correct reading — an unmapped key is unmapped
// whether the personal `repos:` is empty or absent — and it is what a nest whose
// keys nobody has mapped yet must show.
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
func promptOptionalRepos(out io.Writer, in io.Reader, nestName string, repos []nest.Repo,
	startChecked bool, mapping map[string]string) ([]string, error) {
	optional := make([]nest.Repo, 0, len(repos))
	for _, r := range repos {
		if r.Optional {
			optional = append(optional, r)
		}
	}

	keep := make([]bool, len(optional))
	for i := range keep {
		keep[i] = startChecked
	}

	selected := "none selected"
	if startChecked {
		selected = "all selected"
	}
	fmt.Fprintf(out, "nest %s: %d optional repo(s), %s — required repos are always mounted\n",
		nestName, len(optional), selected)

	s := bufio.NewScanner(in)
	for {
		for i, r := range optional {
			box := " "
			if keep[i] {
				box = "x"
			}
			fmt.Fprintf(out, "  %d [%s] %s%s\n", i+1, box, r.Name(), unmappedNote(r, mapping))
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

// unmappedNote annotates a key-typed repo the personal mapping does not carry.
// Empty for a path-typed repo, and empty for a mapped key: an annotation on
// every line would annotate nothing.
func unmappedNote(r nest.Repo, mapping map[string]string) string {
	if r.Key == "" {
		return ""
	}
	if _, ok := mapping[r.Key]; ok {
		return ""
	}
	return "      (not mapped in config.yaml)"
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
