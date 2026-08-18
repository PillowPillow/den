package cli

import (
	"fmt"
	"strings"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/spawn"
	"github.com/spf13/cobra"
)

// spawnNest is the body `den up` and `den run` share: resolve --den-home, point
// spawn's streams at THIS invocation, call spawn.Spawn.
//
// It is enterSandbox's twin (exec.go), and its virtue is the same: nothing is
// guessed from the command. Two spellings of one door is the failure mode #60
// named, and a shared body is what keeps that false of the BEHAVIOUR as well as
// of the name. What stays each command's own fits in two lines — `up` sets
// o.Nest and leaves o.Command empty (an empty Argv means `bash -l`, one layer
// down in spawn.Command); `run` sets o.Nest and o.Command = args[1:].
//
// Out and Err are decided HERE, at run time, because they alone depend on the
// command and hence on a test's SetOut/SetErr. The terminal probe stays in
// deps — it describes the machine, not the command — and so does the Prompter
// that succeeded the checklist's io.Reader: it takes over the terminal rather
// than reading whatever cobra hands down, so there is nothing per-invocation
// left to decide about it. Out set here is not the last word on it: on a
// non-tty command spawn.Spawn aliases it to Err itself, so den's own log never
// joins a pipe the command owns.
func spawnNest(cmd *cobra.Command, denHome *string, o spawn.Options, deps spawn.Deps) error {
	home, err := config.Home(*denHome)
	if err != nil {
		return err
	}
	d := deps
	d.Out = cmd.OutOrStdout()
	d.Err = cmd.ErrOrStderr()
	return spawn.Spawn(cmd.Context(), home, o, d)
}

// registerSpawnFlags registers the flags `den up` and `den run` share.
//
// Shared rather than written twice: a flag added to one and forgotten on the
// other is silent — cobra reports nothing, and the flag simply reaches the VM
// as a command token on the command that lacks it. --detach and -T are NOT here
// because the two commands spell them differently: each registers the one that
// works and the one that is refused, with its own help text.
//
// --repo is a StringArrayVar, never a StringSliceVar, and that is measured:
// StringSlice splits on commas and a path may contain one. --only and --without
// keep StringSliceVar, and this slice does not change them — their comma
// limitation on a repo named `a,b` is pre-existing and has its own subject.
func registerSpawnFlags(cmd *cobra.Command, o *spawn.Options) {
	cmd.Flags().StringVarP(&o.Worktree, "worktree", "w", "", "worktree to propagate across all repos")
	cmd.Flags().StringVar(&o.Instance, "as", "",
		"name this instance, to run several sandboxes of one nest side by side")
	cmd.Flags().StringVar(&o.Agent, "agent", "", "agent to use (default: defaults.agent)")
	cmd.Flags().StringSliceVar(&o.Without, "without", nil, "exclude these optional repos")
	cmd.Flags().StringSliceVar(&o.Only, "only", nil, "keep only these optional repos")
	cmd.Flags().BoolVarP(&o.Interactive, "interactive", "i", false,
		"pick the nest's optional repos from a checklist (contradicts --only/--without)")
	cmd.Flags().StringVar(&o.Workdir, "workdir", "",
		"working directory for the command (default: the directory you ran den from, when the sandbox mounts it; otherwise the first workspace it reports)")
	cmd.Flags().StringArrayVar(&o.Repos, "repo", nil,
		"mount this repository too, ad hoc (repeatable; the order you type is the order den mounts)")
}

// addFlag appends a flag den's proposed line needs but the user never typed.
//
// Two remedies cannot be built without it, and both are inter-command: `den up`
// turning a stray positional into `--repo <path>`, and `den run`'s warning
// turning the first command token into one. Written as a method on the shape so
// those lines come out of the SHARED builder — a Sprintf at the call site is
// outside TestRunRemediesAreThemselvesLegal, hence free to rot the way slice
// 1's did.
func (s execShape) addFlag(name, value string) execShape {
	s.flags = append(s.flags, "--"+name, value)
	return s
}

// upArgs is `den up`'s validator, and `den nest show`'s.
//
// exactlyOneArg does NOT fit, and this is the one place this slice ADDS a
// message rather than moving one. The gesture the break makes most likely is
// finger memory — `den up api ~/dev/hotfix` — and under exactlyOneArg the user
// reads "exactly one argument expected, 2 received, starting with
// "~/dev/hotfix" — usage: …", which names neither --repo nor what changed.
//
// FOUR branches, and their ORDER is the subject. The command tail is
// execRewrite's s.command, NEVER a slice of args cut by hand: neither
// args[dash:] (on `up -- api`, dash is 0 while `api` is the nest, so indexing by
// dash swaps the two) nor args[1:] (which assumes args[0] is the nest, false as
// soon as pflag ate a leading `--` — see branch 2). ArgsLenAtDash() says WHETHER
// a `--` was typed, it does not cut, and it is the only thing it is asked here.
//
// pflag terminates its parse on `--` whatever SetInterspersed says, and `--`
// NEVER appears in args — only ArgsLenAtDash reveals it (measured 2026-08-16:
// `up -- api` → args ["api"], dash 0; `up api --` → args ["api"], dash 1;
// `up api -- go test` → args ["api","go","test"], dash 1). A validator counting
// positionals is therefore blind, and `den up api -- go test` reaches it as
// three positionals — whence the remedy `den up --repo go --repo test api`,
// legal, replayable, and proposing to mount two directories named `go` and
// `test` when the user meant `den run api go test`.
//
// A validator NEVER writes to a stream. `den run`'s directory warning lives in
// its RunE for that reason (run.go): first-defect-wins means a printing
// validator would staple advice under a line already refused for something else.
//
// refused carries the two flags spawn's step 0 can refuse, and it is what stops
// this function from proposing a line den itself rejects (#76). `den nest show`
// passes the zero value: it registers neither flag.
func upArgs(cmd *cobra.Command, args []string, refused refusableFlags) error {
	path := cmd.CommandPath()
	s := execRewrite(cmd, args)
	// The shape is taken ONCE, at the top, and the name branch reads IT rather
	// than len(args) — which is what makes this function agree with enterArgs
	// (exec.go), where the identical test is `s.name == ""`.
	//
	// Counting arguments answered the wrong question. It missed the shape
	// execShape.haveName exists for: `den up "$NEST" ~/dev/hotfix` with the
	// variable unset arrives as an empty first token, so len(args) is 2 and the
	// repo branch fired — proposing `den up --repo /dev/hotfix` followed by the
	// nest spelled as a quoted empty word. A legal line naming a nest the user
	// never typed, which remedy.go calls worse than no proposal. `den nest show`
	// inherited it through this same validator.
	//
	// It sits ABOVE the separator branch, not below, and that ordering is the
	// fix rather than an accident of writing: `den up -- --repo /a` reaches the
	// separator branch with no name at all, and remedyLine would spell the
	// missing name as a quoted empty word there too.
	if s.name == "" {
		return fmt.Errorf("%s: a nest expected — usage: %s", path, cmd.UseLine())
	}
	// #76, and it sits here for two reasons. BELOW the name check, because what is
	// MISSING outranks what contradicts — `den up -T` answers "a nest expected",
	// enterArgs's own ordering — and ABOVE every branch that builds a line,
	// because a flag this command always refuses must be NAMED, never carried into
	// a proposal. `den up -T api /dev/hotfix` proposed
	// `den up --no-tty=true --repo /dev/hotfix api`, refused one round trip later
	// by the judge asked here.
	//
	// false is `den up`'s contract, not this line's shape: `den up api -- go test`
	// carries a command in s.command, and the sandbox `den up` would spawn still
	// has none. That branch asks the judge again, about `den run`, below.
	//
	// No remedy line accompanies this refusal, deliberately, and the precedent is
	// the --repo-ambiguity branch further down: there is no legal line carrying
	// everything the user typed, so den names the flag and proposes nothing rather
	// than dropping a flag (the silent normalization spec §2 forbids) or inventing
	// a command they never gave. The judge's own message names both ways out.
	if err := refused.refusesLine(s, false); err != nil {
		return err
	}
	// Branch 2 is the DISCRIMINANT and runs before the repo branch: the user
	// wrote a separator, which in the old grammar meant "a command follows".
	// That reading beats the repo one.
	if cmd.ArgsLenAtDash() >= 0 {
		// The discriminant is the SHAPE's command, not the positional count, and
		// that distinction is the whole branch (fixed 2026-08-16, found on the
		// built binary). `len(args) > 1` reads args[0] as the nest and args[1:]
		// as the command, which holds for `up api -- go test` and is FALSE the
		// moment pflag ate a LEADING `--`: everything after it arrives
		// positional, den flags included, so args[0] is a flag. `up -- --repo /a
		// api` then proposed `den run --repo /a api /a api` — the nest emitted
		// twice, once as the name and once as the first word of a command the
		// user never typed, which would run a program named `/a` inside the VM.
		// Syntactically legal, semantically false: validateArgs accepts it, so
		// the replay property cannot see it.
		//
		// execRewrite already answers the question correctly — it walks both
		// sides of the name and puts `--repo /a` in s.flags, `api` in s.name and
		// NOTHING in s.command. Reading its verdict rather than re-reading the
		// raw slice is also what makes this function agree with enterArgs
		// (exec.go), which quotes s.command for the identical shape and is
		// pinned on `run` by TestRunRemediesAreThemselvesLegal's leading-
		// separator row. Two answers to one question is what a shared builder
		// exists to prevent.
		if len(s.command) > 0 {
			// A `run` typed `up`. The remedy names `den run`, NOT --repo:
			// `go test` is a command, and proposing to mount it as two
			// directories is the absurdity this branch exists to prevent.
			//
			// Reached from `den nest show` too, and accepted there rather than
			// special-cased: `den nest show api -- foo` proposes `den run api
			// foo`, which reads oddly from a dry-run but is the honest answer —
			// the user typed a command, and commands go to `den run`. Naming
			// `den nest show api` instead would drop `foo` in silence, which is
			// the normalization §2 refuses.
			//
			// The judge is asked a SECOND time here, with true, and this is the
			// cross-command shape #76 exists for: --detach is perfectly legal on
			// `den up`, so the check above passed, and only the `den run` line this
			// branch is about to write refuses it. `den up --detach api -- go test`
			// proposed `den run --detach=true api go test`. Keying on the source
			// command's own flags cannot see it; keying on the TARGET's contract
			// can.
			if err := refused.refusesLine(s, true); err != nil {
				return err
			}
			return fmt.Errorf("%s: %s takes no command — write `%s`",
				path, path, remedyLine(cmd, "den run", s, s.command))
		}
		// The separator is merely useless: `up -- api`, `up api --`, and — since
		// the discriminant above became the shape — `up -- --repo /a api`, where
		// the separator only cost pflag the flags behind it. The remedy still
		// carries every one of them, whether pflag consumed it (`up --repo /a --
		// api`) or execRewrite lifted it back out of the tail: either way the
		// line must come back as `den up --repo /a api`, not `den up api`, or the
		// mount vanishes in silence.
		return fmt.Errorf("%s: `--` is not needed — write `%s`",
			path, remedyLine(cmd, path, s, nil))
	}
	if cmd.Flags().Changed("repo") && len(args) > 1 {
		// den cannot say WHICH positional is the nest, and it says so instead of
		// guessing. No remedy line is built here, deliberately: building one from
		// the positionals proposes the wrong nest.
		//
		// The likeliest cause is a shell pattern — --repo cannot take a glob, the
		// shell expands before den sees anything, --repo binds the first match and
		// the rest arrive as positionals. But Changed("repo") does NOT prove an
		// expansion: `den up --repo /a api /b` satisfies it with no pattern at
		// all. So the message states the FACT and names both exits, and claims no
		// cause. No os.Stat either: the trigger is a fact about the command line,
		// not a hypothesis about the disk.
		return fmt.Errorf(
			"%s: --repo was given and %d arguments remain, so den cannot tell which one is the nest\n"+
				"  — if a shell pattern expanded, quote it or repeat --repo once per path\n"+
				"  — if these are ad-hoc repos, repeat --repo once per path\n"+
				"  (the arguments were %s)",
			path, len(args), strings.Join(args, ", "))
	}
	if len(args) > 1 {
		// Finger memory, the case this validator exists for. The extra
		// positionals ARE repos, so they come back as --repo pairs through the
		// shared builder — never re-joined by hand, so this line enters
		// TestRunRemediesAreThemselvesLegal like every refusal.
		//
		// The shape is the one taken at the top; it carries args[1:] in
		// s.command, which is why remedyLine is handed nil rather than s.command
		// — those tokens are becoming --repo pairs, not a command.
		for _, p := range args[1:] {
			s = s.addFlag("repo", p)
		}
		return fmt.Errorf("%s: extra arguments — ad-hoc repos go behind --repo now — write `%s`",
			path, remedyLine(cmd, path, s, nil))
	}
	return nil
}

// newUpCmd builds `den up <nest>`: create-or-attach, then a login shell.
//
// It is `den spawn <nest>` of 2026-08-15, minus the ad-hoc repos' spelling and
// minus `--`. The name is compose's, and the 2026-08-05 objection to it — "up
// lies about the semantics, this is a spawn-OR-attach, not a start" — rests on
// a false premise, measured 2026-08-16 on Docker Compose v5.3.1: `docker
// compose up` on live containers neither recreates nor restarts them. It is a
// create-or-attach too.
//
// NO SetInterspersed(false), and that is a decision. shell.go:93-100 holds the
// argument word for word: `up` takes no command, so no flag has a possible
// second owner, and interspersing buys one thing — `den up api -T` reaches -T's
// NAMED refusal instead of being refused for its ARITY by a message naming
// neither the flag nor the way out.
func newUpCmd(denHome *string, deps spawn.Deps) *cobra.Command {
	var o spawn.Options

	cmd := &cobra.Command{
		Use:   "up <nest>",
		Short: "Spawn or attach a nest's sandbox, then open a shell",
		// The shape is read out of o, and out of o only: the validator asking
		// spawn's judge (#76) is now the only thing o.NoTTY can change on this
		// command, so reading the FlagSet instead would leave `BoolVarP(&o.NoTTY,
		// …)` write-only and TestUpRefusesNoTTY green over an unwired flag.
		Args: func(cmd *cobra.Command, args []string) error {
			return upArgs(cmd, args, refusableFlags{detach: o.Detach, noTTY: o.NoTTY})
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// o.Command stays empty: an empty spawn.Command.Argv IS `bash -l`,
			// one layer down (internal/spawn/enter.go), where `den run` reads
			// the same default.
			o.Nest = args[0]
			return spawnNest(cmd, denHome, o, deps)
		},
	}

	registerSpawnFlags(cmd, &o)
	cmd.Flags().BoolVar(&o.Detach, "detach", false, "do not open a shell after the sandbox is up")
	// REGISTERED and always refused, like -T on `den shell`: a named refusal
	// beats cobra's `unknown flag: -T`. The verdict itself is NOT here and never
	// will be — it is spawn's Detach×Command / NoTTY×no-command contradiction,
	// spawn.CommandLineContradiction, which step 0 of Spawn still calls before a
	// single config file is read.
	//
	// Since #76 (2026-08-17) this command's VALIDATOR calls that same function,
	// and the doctrine enterOptions set in slice 1 is why it is a call and not a
	// copy: asking the owner is one source, re-deriving the verdict beside it
	// would be two, and two is how they drift. The validator has to ask, because
	// every refusal it writes ends in "write `…`" and the line it proposes must be
	// one den accepts — `den up -T api /dev/hotfix` proposed
	// `den up --no-tty=true --repo /dev/hotfix api`, refused one round trip later
	// by step 0. Do NOT move the message here to "save the round trip"; that is
	// the second source.
	//
	// NO BACKTICKS in the usage string, and that is not a matter of taste:
	// cobra's UnquoteUsage takes the FIRST backquoted span as the flag's VALUE
	// PLACEHOLDER and strips that pair from the text. `den up --help` therefore
	// rendered `-T, --no-tty den up`, telling the reader a boolean takes an
	// argument named `den up`. Relocating the pair does not help — any pair
	// anywhere becomes the placeholder — so there is none.
	cmd.Flags().BoolVarP(&o.NoTTY, "no-tty", "T", false,
		"refused here — den up opens a login shell, which needs a terminal; use den run -T <nest> <cmd>")
	return cmd
}
