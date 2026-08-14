package spawn

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
)

// LooksInteractive reports whether den has both a terminal to read from and a
// terminal to write to.
//
// ONE LINE-ish, on purpose, and exported so the wiring site names it: this is
// the part of `-i` (and, since #60, of the `-it` decision in `den exec`/`den
// spawn -- <cmd>`) that binds den to the process's actual descriptors.
// Everything around it — the checklist, the toggles, the refusals — takes an
// io.Reader and is tested. Growing this function is how that boundary gets
// lost.
//
// It says "reports whether", not "appears to": #66 replaced a heuristic with a
// test. Until then this read `info.Mode()&os.ModeCharDevice != 0` on each
// descriptor, and `os.ModeCharDevice` is true for EVERY character device —
// `/dev/null`, `/dev/zero`, `/dev/random`. That mattered because `sbx exec -it`
// with no real terminal behind it silently DISCARDS the command's output while
// reporting rc=0 (spec §14.0, measured 2026-08-10): `< /dev/null` is the
// canonical CI and cron stdin, so a false positive here was a data-loss path
// with a clean exit code. #60 narrowed the probe to require BOTH descriptors,
// which closed the redirected-stdout shape; the residual case — `/dev/null` on
// stdin with a real terminal on stdout — needed the real test, and isTerminal
// (isterminal_darwin.go, isterminal_linux.go) is it.
//
// The half of this that no test can exercise is now exactly one claim — that a
// REAL terminal answers true — because a suite that acquired a tty would stop
// being hermetic (CLAUDE.md). It was measured by hand instead, on darwin,
// 2026-08-14. The other half, that a character device which is not a terminal
// answers false, is the bug #66 closed and it IS tested: isterminal_test.go
// pins `/dev/null`, a regular file and a closed file. The split into
// `isTerminal(f)` plus this wrapper exists for that test — LooksInteractive
// reads globals a test cannot replace.
//
// It hands isTerminal the *os.File, not `os.Stdin.Fd()`. That is not a style
// preference: the `!darwin && !linux` fallback needs a Stat, and a Stat from a
// bare descriptor means `os.NewFile`, which takes ownership and whose finalizer
// then closes den's own stdin and stdout (isterminal_other.go carries the
// reproduction). Passing the file den already holds leaves exactly one owner.
//
// BOTH descriptors are still required, and that is #60's rule, not a
// consequence of #66. With stdout redirected, LooksInteractive answers false
// even when stdin is a real terminal, so the checklist takes its clean refusal
// (interactiveWithout, below) instead of drawing a prompt nobody can see — a
// checklist the user cannot see is worse than a refusal that names the
// non-interactive equivalent.
func LooksInteractive() bool {
	return isTerminal(os.Stdin) && isTerminal(os.Stdout)
}

// nonInteractiveEquivalents is repeated in every refusal of `-i` on purpose: a
// user who cannot use the checklist needs the way that works, in the same
// breath, not a pointer to `--help`.
//
// It became a function of the MODE the day `--without` stopped working on a
// `select: prompt` nest (Spawn, step 0bis: such a nest declares no default
// selection, so there is nothing for `--without` to subtract from). A constant
// naming both flags would then name a refused command in the very message whose
// job is to name one that works — the worst place for a stale sentence, since a
// remedy is followed.
//
// `--only` is what both modes keep, and on a prompting nest it is the exact
// spelling of what the checklist asks: the set, stated outright.
func nonInteractiveEquivalents(prompts bool) string {
	if prompts {
		return "`--only repo,...` makes the same selection without a prompt"
	}
	return "`--only repo,...` and `--without repo,...` make the same selection without a prompt"
}

// interactiveWithout runs the `-i` checklist and returns its answer AS A
// `--without` LIST.
//
// Translating to the existing flag rather than building a second selection path
// is the whole design: nest.Resolve keeps applying the one rule it already
// owns (required repos always mounted, short names unique), and `-i` is just
// another way to fill its input. That is what makes "-i produces the same
// sandbox as the equivalent --without" true by construction rather than by
// coincidence — see TestInteractiveProducesTheSameArgvAsTheEquivalentWithout.
//
// denHome is here for one line of output: the checklist's unmapped-key
// annotation names the file to edit, and that file is <denHome>/config.yaml —
// NOT the literal "config.yaml" it used to print. Under DEN_HOME (which is what
// makes den's own suite hermetic, and what a user with two homes types every
// day) the literal named a file that does not exist at the place the reader
// would look for it. Threaded rather than derived here, because
// config.GlobalPath is the sole definition of that path and unmappedNote is the
// message site that must agree with every other one.
func interactiveWithout(d Deps, denHome string, n *nest.Nest, mapping map[string]string) ([]string, error) {
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
					"job forever; %s", n.Name, nonInteractiveEquivalents(true))
		}
		return nil, fmt.Errorf(
			"-i: no terminal on den's input — the checklist has nobody to ask, and reading anyway would "+
				"block a pipe or a CI job forever; %s", nonInteractiveEquivalents(false))
	}
	in := d.In
	if in == nil {
		// Belt and braces: IsTTY answered yes, so something IS attached, but a
		// caller that wired the probe and forgot the stream must not panic
		// mid-sequence.
		in = os.Stdin
	}
	return promptOptionalRepos(d.Out, in, denHome, n.Name, n.Repos, n.PromptsForRepos(), mapping)
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
// prompts is the nest's MODE — `select: prompt` — and it is the one thing this
// function needs to know about it, because both of the things it decides follow
// from it and neither may disagree with the other.
//
// It decides the initial state of every box, which is NOT cosmetic. `-i` starts
// full, because confirming an -i checklist without touching it must produce
// exactly what `den spawn` alone produces
// (TestInteractiveProducesTheSameArgvAsTheEquivalentWithout). A `select: prompt`
// nest starts EMPTY, because it has no default selection to propose by
// definition — and thirty ticked boxes would turn an empty line into a
// thirty-repo mount.
//
// It also decides which flags the footer names, and that is the same fact read
// from the other end: a nest with no default selection is exactly the nest
// `--without` is refused on. Passed as ONE parameter rather than as a
// `startChecked` plus an equivalents string, because two parameters carrying one
// fact are two things to keep in agreement — the drift the selectionOpen comment
// in spawn.go records having already paid for once.
//
// mapping is the personal `repos:` of <denHome>/config.yaml, used to ANNOTATE
// the keys it does not carry — denHome is beside it because the annotation names
// that file, and the two must describe the same one (unmappedNote).
// Annotation only: ticking an unmapped key stays possible,
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
func promptOptionalRepos(out io.Writer, in io.Reader, denHome, nestName string, repos []nest.Repo,
	prompts bool, mapping map[string]string) ([]string, error) {
	startChecked := !prompts
	equivalents := nonInteractiveEquivalents(prompts)
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
			fmt.Fprintf(out, "  %d [%s] %s%s\n", i+1, box, r.Name(), unmappedNote(r, mapping, denHome))
		}
		fmt.Fprintf(out, "toggle by number (space-separated), empty line to confirm — %s\n> ",
			equivalents)

		if !s.Scan() {
			if err := s.Err(); err != nil {
				return nil, fmt.Errorf("-i: reading the selection: %w", err)
			}
			// EOF before a confirmation. Confirming the current state here
			// would be den deciding for the user — and this decision creates a
			// microVM with a set of repos nobody chose.
			return nil, fmt.Errorf(
				"-i: input ended before the selection was confirmed (a pipe, a closed terminal) — "+
					"nothing was spawned; %s", equivalents)
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
//
// It names <denHome>/config.yaml, through config.GlobalPath — never the bare
// "config.yaml" this line printed before. den has as many config.yaml as the
// user has den homes (`--den-home`, `DEN_HOME`), and the one this checklist is
// reading is the only one that can fix the annotation: the bare filename sent a
// reader with two homes to edit the wrong file, or to look for a path den never
// stated. Same rule as resolveRepoKeys' own refusal and `den doctor`'s, which is
// what the reader will see next if they tick this box anyway.
func unmappedNote(r nest.Repo, mapping map[string]string, denHome string) string {
	if r.Key == "" {
		return ""
	}
	if _, ok := mapping[r.Key]; ok {
		return ""
	}
	return "      (not mapped in " + config.GlobalPath(denHome) + ")"
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
