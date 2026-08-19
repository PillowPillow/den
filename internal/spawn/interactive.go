package spawn

import (
	"context"
	"fmt"
	"os"
	"slices"

	"github.com/PillowPillow/den/internal/nest"
	"github.com/PillowPillow/den/internal/prompt"
)

// LooksInteractive reports whether den has both a terminal to read from and a
// terminal to write to.
//
// ONE LINE-ish, on purpose, and exported so the wiring site names it: this is
// the part of `-i` (and, since #60, of the `-it` decision in `den exec`/`den
// run <cmd>`) that binds den to the process's actual descriptors.
// Everything around it — the checklist and its refusals — takes a
// prompt.Prompter and is tested, which is what confines the untested claim to
// this function. Growing this function is how that boundary gets lost.
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
// (interactiveWithout, below) instead of handing the Prompter a question
// nobody can see — a checklist the user cannot see is worse than a refusal
// that names the non-interactive equivalent. den draws none of it any more,
// which makes this gate carry MORE than it did: the library behind the
// Prompter fails open (spec §3.d), so a probe that answered true here on a
// redirected stdout would get a default selection nobody chose.
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
// mappingPath is here for one line of output: the checklist's unmapped-key
// annotation names the file to edit — NOT the literal "config.yaml" it used to
// print. Under DEN_HOME (which is what makes den's own suite hermetic, and what
// a user with two homes types every day) the literal named a file that does not
// exist at the place the reader would look for it.
//
// mapping and mappingPath travel TOGETHER, and neither is derived here: a
// manifested source resolves its keys through its own source-config file
// (spec §6), so a checklist that consulted the global mapping would offer a
// repo nest.Resolve then refuses, or mark as unmapped one it resolves. The
// caller selected both; this layer only displays them.
func interactiveWithout(ctx context.Context, d Deps, mappingPath string, n *nest.Nest,
	mapping map[string]string) ([]string, error) {
	// Nothing to ask comes FIRST, before the terminal check: a nest with no
	// optional repo needs no answer, so it needs no terminal either — `den up
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
	return promptOptionalRepos(ctx, d.Prompt, mappingPath, n.Name, n.Repos, n.PromptsForRepos(), mapping)
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

// promptOptionalRepos asks for a nest's OPTIONAL repos and returns the short
// names of the ones left unchecked — a `--without` list.
//
// Translating to the existing flag rather than building a second selection path
// is the whole design: nest.Resolve keeps applying the one rule it already
// owns (required repos always mounted, short names unique), and `-i` is just
// another way to fill its input. That is what makes "-i produces the same
// sandbox as the equivalent --without" true by construction rather than by
// coincidence — see TestInteractiveProducesTheSameArgvAsTheEquivalentWithout.
//
// prompts is the nest's MODE — `select: prompt` — and it is the one thing this
// function needs to know about it, because both of the things it decides follow
// from it and neither may disagree with the other.
//
// It decides the initial state of every box, which is NOT cosmetic. `-i` starts
// full, because confirming an -i checklist without touching it must produce
// exactly what `den up` alone produces. A `select: prompt` nest starts EMPTY,
// because it has no default selection to propose by definition — and thirty
// ticked boxes would turn an empty confirmation into a thirty-repo mount.
//
// It also decides which flags the title names, and that is the same fact read
// from the other end: a nest with no default selection is exactly the nest
// `--without` is refused on. Passed as ONE parameter rather than as a
// `startChecked` plus an equivalents string, because two parameters carrying one
// fact are two things to keep in agreement.
//
// mapping is the personal `repos:` of <denHome>/config.yaml, used to ANNOTATE
// the keys it does not carry — mappingPath is beside it because the annotation
// names that file, and the two must describe the same one (unmappedNote).
// Annotation only: ticking an unmapped key stays possible, and the refusal that
// follows is resolveRepoKeys', which names the key, the file and the clone URL.
//
// Required repos are neither listed nor offered (spec §6.2): they are always
// mounted, and offering them would let a human decline what den then mounts
// anyway.
//
// This function draws NOTHING. It builds a request and inverts the answer; the
// Prompter owns every byte on the terminal. That split is what lets the suite
// assert on what den ASKED without a tty ever existing (CLAUDE.md).
func promptOptionalRepos(ctx context.Context, p prompt.Prompter, mappingPath, nestName string,
	repos []nest.Repo, prompts bool, mapping map[string]string) ([]string, error) {
	// A nil Prompter is "no way to ask", never "assume the defaults": an
	// unwired double must refuse here rather than let the caller mount a
	// selection nobody made. Same rule as a nil IsTTY, one layer down.
	if p == nil {
		// The prefix follows the entry point, the same rule interactiveWithout
		// applies to its own refusal: a `select: prompt` nest reaches this
		// checklist with no -i on the command line, and naming that flag would
		// send its user hunting for something they never typed. The nest name
		// takes its place, because a refusal that names neither is anonymous.
		if prompts {
			return nil, fmt.Errorf(
				"nest %s selects its repos at spawn time and no prompter is wired — this is a den "+
					"defect; %s", nestName, nonInteractiveEquivalents(true))
		}
		return nil, fmt.Errorf("-i: no prompter is wired — this is a den defect; %s",
			nonInteractiveEquivalents(false))
	}

	optional := make([]nest.Repo, 0, len(repos))
	for _, r := range repos {
		if r.Optional {
			optional = append(optional, r)
		}
	}

	selected := "none selected"
	if !prompts {
		selected = "all selected"
	}
	options := make([]prompt.Option, 0, len(optional))
	for _, r := range optional {
		options = append(options, prompt.Option{
			Value:       r.Name(),
			Label:       r.Name(),
			Description: unmappedNote(r, mapping, mappingPath),
		})
	}

	keep, err := p.MultiSelect(ctx, prompt.MultiSelectRequest{
		Title: fmt.Sprintf("nest %s: %d optional repo(s), %s — required repos are always mounted (%s)",
			nestName, len(optional), selected, nonInteractiveEquivalents(prompts)),
		Options:     options,
		Preselected: !prompts,
	})
	if err != nil {
		// The refusal names the non-interactive equivalents, exactly as the
		// EOF refusal it replaces did. A Prompter error is the last thing a
		// user sees before den gives up on asking, and "reading the selection
		// failed" without the flag that does the same job is a dead end — den
		// names the file to fix and the remedy (spec §2).
		//
		// Which prefix it carries is the OTHER fact, and it follows the entry
		// point exactly as the nil guard above and interactiveWithout do: a
		// `select: prompt` nest gets here with no -i typed anywhere.
		if prompts {
			return nil, fmt.Errorf("nest %s: reading the selection: %w; %s",
				nestName, err, nonInteractiveEquivalents(true))
		}
		return nil, fmt.Errorf("-i: reading the selection: %w; %s",
			err, nonInteractiveEquivalents(false))
	}

	// The answer names what STAYS; den's flag names what goes. Inverting here,
	// against the offered list rather than against the nest's full repo list,
	// is what keeps a required repo out of `--without` even if a Prompter
	// echoed one back.
	var without []string
	for _, r := range optional {
		if !slices.Contains(keep, r.Name()) {
			without = append(without, r.Name())
		}
	}
	return without, nil
}

// unmappedNote annotates a key-typed repo the personal mapping does not carry.
// Empty for a path-typed repo, and empty for a mapped key: an annotation on
// every line would annotate nothing.
//
// The six spaces that used to open it are gone with the renderer that needed
// them: this string is a prompt.Option.Description now, and that field's
// contract is that the caller says WHAT the annotation says while the renderer
// decides how it looks. Padding baked into the text is the renderer's job done
// twice, at the one layer that cannot see the result.
//
// A nil mapping is NOT a special case, and this function treats it as none: a
// path-typed repo is unannotated either way, and every key-typed one is
// annotated. That is the correct reading — an unmapped key is unmapped whether
// the personal `repos:` is empty or absent — and it is what a nest whose keys
// nobody has mapped yet must show.
//
// It names <denHome>/config.yaml, through config.GlobalPath — never the bare
// "config.yaml" this line printed before. den has as many config.yaml as the
// user has den homes (`--den-home`, `DEN_HOME`), and the one this checklist is
// reading is the only one that can fix the annotation: the bare filename sent a
// reader with two homes to edit the wrong file, or to look for a path den never
// stated. Same rule as resolveRepoKeys' own refusal and `den doctor`'s, which is
// what the reader will see next if they tick this box anyway.
func unmappedNote(r nest.Repo, mapping map[string]string, mappingPath string) string {
	if r.Key == "" {
		return ""
	}
	if _, ok := mapping[r.Key]; ok {
		return ""
	}
	return "(not mapped in " + mappingPath + ")"
}
