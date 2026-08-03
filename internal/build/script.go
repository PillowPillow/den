package build

import (
	"context"
	"io"
	"os/exec"
	"path/filepath"

	"github.com/PillowPillow/den/internal/config"
)

// ScriptName is the file a stack builds itself with. Spec §6 is explicit that
// den ORDERS the builds and does not rewrite them: the script is run
// unchanged, and `versions.lock` stays whatever the script maintains.
const ScriptName = "build.sh"

// Script runs one stack's build.sh. An interface for the same reason
// ports.Scanner is one: the real implementation spawns a process, so a suite
// that inherited it would run arbitrary user scripts on the machine running
// `go test`. Every test injects a recorder; issue #8 asks for exactly this
// ("aucun test ne lance de script réel").
type Script interface {
	// Run executes the stack's build.sh with the stack directory as cwd, wiring
	// both its stdout and stderr to out.
	Run(ctx context.Context, s *config.Stack, out io.Writer) error
}

// ExecScript is the real Script, and the ONLY file of this package that spawns
// a process. Untested by construction, and kept to the smallest surface that
// can be — everything around it (the graph, the order, the skip arbitration,
// the missing-script refusal) stays in the hermetic suite.
type ExecScript struct{}

// Run executes <stack.Dir>/build.sh.
//
// cwd is the stack directory, not den's: a build.sh written next to its
// Dockerfile refers to it relatively, and running it from wherever the user
// happened to type `den build` would break every such script. It is also what
// makes the command reproducible from any directory.
//
// stdout AND stderr both go to out, interleaved as the script emits them. A
// build is a stream the user watches, not a result den parses: splitting the
// two would reorder a script's own progress against its warnings, and den has
// nothing to do with either.
//
// The environment is INHERITED (cmd.Env left nil), on the same doctrine as
// sbx.Exec: a build.sh that needs a registry token, a proxy or a
// DOCKER_HOST reads it from the shell the user launched den in, and there is
// no den-side list of what a third-party script legitimately needs.
func (ExecScript) Run(ctx context.Context, s *config.Stack, out io.Writer) error {
	cmd := exec.CommandContext(ctx, ScriptPath(s))
	cmd.Dir = s.Dir
	cmd.Stdout = out
	cmd.Stderr = out
	return cmd.Run()
}

// ScriptPath is where a stack's build script lives. SOLE definition: the
// missing-script refusal (Execute) and the execution itself must name the same
// file, or den would refuse a path it never tried to run.
func ScriptPath(s *config.Stack) string { return filepath.Join(s.Dir, ScriptName) }
