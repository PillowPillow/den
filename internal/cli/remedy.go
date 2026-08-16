package cli

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// denFlag is one flag den owns, as the DERIVED table carries it: the long
// name, and whether it takes a value.
//
// takes comes from NoOptDefVal being empty, which is pflag's own encoding of
// "this flag needs a value" — the exact fact the hand-written `placeholder`
// field encoded until 2026-08-16, and the one a hand list gets wrong first.
type denFlag struct {
	name  string
	takes bool
}

// denFlags walks the command's merged FlagSet and returns the two lookups the
// classifier reads: long names, and shorthand letters.
//
// The walk is the point: it sees den's own flags, the ROOT's persistent
// --den-home (cobra merges it into the same set), and every shorthand — so a
// flag added tomorrow is refused in first-command position without anyone
// editing a list. The list it replaces held four entries; `den run` needs
// fourteen, and a fourteen-entry list desynchronizes in silence.
//
// --help and -h are excluded BY NAME, and the reason is stronger than "the old
// list omitted them". The walk's contents are PATH-DEPENDENT (measured
// 2026-08-16): --help is present under Execute(), ABSENT under Find +
// ParseFlags, because cobra adds it in InitDefaultHelpFlag during execute().
// The named exclusion is what makes den behave identically on the production
// path and on the test path. Without it, `den exec api --help` — which must ask
// the program inside the sandbox for its help, as `docker compose exec` does —
// would be refused in production and accepted under a validator test.
//
// Returning early on the name also drops the shorthand: there is no `-h` row.
func denFlags(cmd *cobra.Command) (map[string]denFlag, map[byte]denFlag) {
	long := make(map[string]denFlag)
	short := make(map[byte]denFlag)
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Name == "help" {
			return
		}
		d := denFlag{name: f.Name, takes: f.NoOptDefVal == ""}
		long[f.Name] = d
		if f.Shorthand != "" {
			short[f.Shorthand[0]] = d
		}
	})
	return long, short
}

// classifyToken answers what ONE token of the positional tail is.
//
// It is written by hand, and that is the 2026-08-16 measurement's verdict: a
// derived NAME SET cannot classify a token. pflag's short-cluster rules do not
// follow from the names, and the naive reading inverts two of them (measured on
// pflag v1.0.9, replayed on a real den binary):
//
//   - `-iT=true` sets BOTH -i and -T. In parseSingleShortArg the test
//     `shorthands[1] == '='` runs BEFORE the NoOptDefVal test, so the `=` binds
//     to the letter PRECEDING it and TERMINATES the token, whatever that
//     letter's arity.
//   - `-wi feat` sets worktree="i". A letter that takes a value SWALLOWS the
//     rest of the cluster; `feat` stays positional.
//
// Returns:
//   - ok == false: the token is not den's. The scan ends here — everything from
//     this token on is the nest name or the command, verbatim.
//   - ok == true, consumes == false: den's, and its value (if any) is inside
//     the token.
//   - ok == true, consumes == true: den's, and its value is the NEXT token.
//     placeholder is what to write when there is no next token.
//
// names lists every den flag the token sets, which the caller needs for the
// duplicate rule of Task 2 — a cluster sets several.
//
// `--` is NOT handled here: the caller strips it first (it is the separator,
// not a flag), and `--` would otherwise fall into the long branch with an empty
// name and be misreported as "not den's".
//
// An UNKNOWN letter disqualifies the WHOLE cluster, deliberately. That is the
// measured status quo: `den exec api -Tv go build` is accepted today and stays
// accepted, `-Tv` reaching the VM. It is also the only right answer — `-Tv` is
// not a legal den spelling, so den cannot lift it into a remedy.
func classifyToken(tok string, long map[string]denFlag, short map[byte]denFlag) ([]string, bool, string, bool) {
	if len(tok) < 2 || tok[0] != '-' {
		return nil, false, "", false // "", "-", "api", "/tmp/x", "go"
	}
	if strings.HasPrefix(tok, "--") {
		name, _, hasEq := strings.Cut(tok[2:], "=")
		f, known := long[name]
		if !known {
			return nil, false, "", false // --nope, --help
		}
		if f.takes && !hasEq {
			return []string{f.name}, true, "<" + f.name + ">", true
		}
		return []string{f.name}, false, "", true
	}
	body := tok[1:]
	var names []string
	for i := 0; i < len(body); i++ {
		f, known := short[body[i]]
		if !known {
			return nil, false, "", false
		}
		names = append(names, f.name)
		// This test FIRST, whatever f.takes says: pflag binds the `=` to the
		// preceding letter and ends the token there.
		if i+1 < len(body) && body[i+1] == '=' {
			return names, false, "", true
		}
		if !f.takes {
			continue // a boolean; walk on to the next letter (-iT, -Ti)
		}
		if i+1 < len(body) {
			return names, false, "", true // attached value: -wfeat, -wi, -wT
		}
		return names, true, "<" + f.name + ">", true // -w feat
	}
	// Every letter was a known boolean: -iT, -Ti.
	return names, false, "", true
}

// execShape is the LEGAL command line den reads out of a refused one: the
// sandbox or nest name, den's own flags lifted to where they belong, and the
// command.
//
// It exists because every refusal ends in "write `…`", and a remedy that is
// itself refused costs the user a second round trip. Building the shape ONCE
// and phrasing the refusal from it is what makes the proposal answerable:
// TestExecRemediesAreThemselvesLegal feeds every remedy back through the
// validator and requires nil.
type execShape struct {
	flags   []string // den's own, value included, in the order they were typed
	name    string   // the sandbox or nest; "" when the line names none
	command []string // what runs inside the sandbox; empty when none was given
	sawDash bool     // a `--` was dropped from the line
	// haveName separates "no name yet" from "the name IS the empty string", and
	// the string's own emptiness cannot: `den exec "$SANDBOX" -T go build` with
	// the variable unset hands over an empty first token, and a scan that kept
	// looking would have taken `go` for the sandbox and proposed
	// `den exec -T go build` — a legal line naming a sandbox the user never
	// typed, which is worse than no proposal. It ends up in the branch that
	// proposes nothing.
	haveName bool
	// lifted names the den flags found in the TAIL. Task 2's re-read consults
	// it: a flag typed after the name is the last one typed, so it wins over the
	// same flag read back from the FlagSet.
	lifted map[string]bool
}

// execRewrite reads the positionals cobra handed over and returns that shape.
//
// One left-to-right pass, and the same rule on both sides of the name: a `--`
// is dropped, a den flag is lifted (with its value), and the FIRST token that
// is neither ends the scan — everything from there is the command, verbatim,
// its own flags included. Both sides, because the name is not always first:
// pflag eats a LEADING `--` before its interspersed check (measured 2026-08-14,
// cobra v1.10.2), so `den exec -- -T api go build` arrives as
// ["-T","api","go","build"].
//
// cmd is a parameter rather than a package-level table because the table is
// DERIVED from it: `den exec`, `den run` and `den nest show` do not own the
// same flags, and each must classify against its own.
func execRewrite(cmd *cobra.Command, args []string) execShape {
	long, short := denFlags(cmd)
	s := execShape{lifted: map[string]bool{}}
	for i := 0; i < len(args); i++ {
		tok := args[i]
		if tok == "--" {
			s.sawDash = true
			continue
		}
		names, consumes, placeholder, ok := classifyToken(tok, long, short)
		if !ok {
			if !s.haveName {
				s.name, s.haveName = tok, true
				continue
			}
			s.command = args[i:]
			return s
		}
		for _, n := range names {
			s.lifted[n] = true
		}
		s.flags = append(s.flags, tok)
		if consumes {
			if i+1 < len(args) {
				i++
				s.flags = append(s.flags, args[i])
			} else {
				s.flags = append(s.flags, placeholder)
			}
		}
	}
	return s
}

// execLine spells a shape back as a command line, for the "write `…`" half of
// every refusal. den's flags first, then the name, then the command — the order
// the contract requires, which is the whole point of proposing it.
func execLine(path string, s execShape, command []string) string {
	parts := make([]string, 0, len(s.flags)+len(command)+2)
	parts = append(parts, path)
	parts = append(parts, s.flags...)
	parts = append(parts, s.name)
	return strings.Join(append(parts, command...), " ")
}
