package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/spawn"
	"github.com/spf13/cobra"
)

// newRunCmd builds `den run <nest> <cmd> [args...]`: create-or-attach, then the
// command. It is `den spawn <nest> -- <cmd>` of 2026-08-15, without the
// separator.
//
// It is NOT compose's ephemeral `run`. `docker compose run` builds a throwaway
// container beside the project that --rm deletes on exit; den has no such
// object. `den run` enters THE nest's sandbox, creates it if absent, and leaves
// it alive. Named here so a compose reader does not discover it by use.
//
// SetInterspersed(false), unlike `den up`: everything after the nest name is
// the command, verbatim, its own flags included. Without it, `den run api go
// test -v` dies on "unknown shorthand flag: 'v'". The consequence is the
// contract's break — den's own flags sit LEFT of the nest name — and enterArgs
// refuses the wrong order by name rather than letting `-T` reach the VM as
// `bash: -T: command not found`.
func newRunCmd(denHome *string, deps spawn.Deps) *cobra.Command {
	var o spawn.Options

	cmd := &cobra.Command{
		Use:   "run <nest> <cmd> [args...]",
		Short: "Spawn or attach a nest's sandbox, then run a command",
		Args: func(cmd *cobra.Command, args []string) error {
			return enterArgs(cmd, args, "nest", "den up")
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// enterArgs has refused every other shape, so args[1:] is a real
			// command. No ArgsLenAtDash: under SetInterspersed(false) it is
			// always -1 past the nest name, and the one shape where it is not —
			// a leading `--` — enterArgs already refused.
			o.Nest = args[0]
			o.Command = args[1:]
			// Advisory only: the command runs exactly as typed, whatever this
			// prints. cmd.ErrOrStderr(), never stdout — `den run api go build |
			// tee log` must carry the command's output and nothing else, and
			// `den run` always has a command, so it has no interactive branch
			// handing stdout back to den.
			warnFirstCommandTokenIsADirectory(cmd, args)
			return spawnNest(cmd, denHome, o, deps)
		},
	}

	registerSpawnFlags(cmd, &o)
	// REGISTERED and always refused; see newUpCmd for why the refusal is not
	// spelled here but in spawn.go's step 0.
	cmd.Flags().BoolVar(&o.Detach, "detach", false,
		"refused here — `den run` runs a command inside the sandbox; use `den up --detach <nest>`")
	cmd.Flags().BoolVarP(&o.NoTTY, "no-tty", "T", false,
		"do not allocate a terminal (for pipes and CI)")
	cmd.Flags().SetInterspersed(false)
	return cmd
}

// warnFirstCommandTokenIsADirectory advises, on stderr, when the first word of
// the command is a directory on this host — the shape a repo typed as a
// positional now takes.
//
// It WARNS and never refuses, and never changes what runs: the doctrine of
// 2026-08-04 at the closest precedent that exists — same feature, same command
// family — and the form reportUnmountedRepos ships (spawn.go: "Warn, never
// refuse, and never recreate"). The `!` prefix, one line, no consequence.
//
// It lives in RunE, never in the validator, for two reasons. After the
// validator, args[1] IS the first command token by construction — the line
// `den exec` already leans on. And no validator in this repo writes to a
// stream: the switch is first-defect-wins, so a printing validator would staple
// advice under a line already refused for something else. A verdict and an
// advisory leaving from the same place end up read as one.
//
// The NORMALIZATION is parseRepoArg's (internal/nest/repos.go), exactly, and
// nothing more: expand `~`, join a relative path to the cwd, Clean. NO
// filepath.Abs, NO EvalSymlinks — two reasonable implementations diverged on
// a quoted `~/repo`, on redundant components, and on a failing Getwd, so it is
// written out rather than described. Every failure is SILENT: ExpandPath fails,
// Getwd fails, stat fails whatever the reason, or it exists but is not a
// directory (`den run api ./build.sh` is a legitimate command).
//
// parseRepoArg's own entry refusals — an empty token, a `:ro` suffix — are NOT
// replayed: those refuse a REPO, and this token is not a repo yet. A `:ro` as
// the first word of a command goes to the VM like everything else, unadvised.
//
// NO git-ness test: a non-git directory was a perfectly legal ad-hoc repo
// (2026-08-04 decision 2 requires git only under -w), and that check — not the
// stat — is what would make this a second resolver.
//
// NO "looks like a path" prefilter (a leading /, ~ or .), although it would kill
// the false positive: parseRepoArg accepts a bare relative name, so
// `den run api hotfix` was a legal typing too, and the prefilter would miss it.
// A warning that fires on some spellings of an ad-hoc repo and not others is
// worse than one that occasionally fires for nothing.
//
// The proposed line carries the RAW token, as typed: proposing
// `--repo /Users/x/dev/hotfix` to someone who wrote `~/dev/hotfix` hands back a
// line they do not recognize, and parseRepoArg will redo the expansion anyway.
// The normalized path serves the stat and nothing else.
func warnFirstCommandTokenIsADirectory(cmd *cobra.Command, args []string) {
	raw := args[1]
	expanded, err := config.ExpandPath(raw)
	if err != nil {
		return
	}
	if !filepath.IsAbs(expanded) {
		cwd, err := os.Getwd()
		if err != nil {
			return
		}
		expanded = filepath.Join(cwd, expanded)
	}
	info, err := os.Stat(filepath.Clean(expanded))
	if err != nil || !info.IsDir() {
		return
	}
	// The remedy comes out of the SHARED builder, not a Sprintf: an advisory
	// proposing an illegal line costs the same round trip as a refusal that
	// does, and slice 1 paid it once. It enters TestRunRemediesAreThemselvesLegal
	// like every refusal.
	//
	// The target is `den up` when the line carries no OTHER command: with the
	// first token lifted into --repo there is nothing left to run, and
	// `den run --repo … api` would be refused in turn for "no command given".
	s := execRewrite(cmd, args).addFlag("repo", raw)
	target, tail := "den run", args[2:]
	if len(tail) == 0 {
		target = "den up"
	}
	// s.command was set by execRewrite from args[1:]; the shape is respelled
	// with the tail alone, which is what replacing the command means here.
	fmt.Fprintf(cmd.ErrOrStderr(),
		"! %s is a directory on this host, and den is passing it to the sandbox as the "+
			"first word of the command — ad-hoc repos go behind `--repo` now — write `%s`\n",
		raw, remedyLine(cmd, target, s, tail))
}
