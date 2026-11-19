// Package instance runs the sequence behind `den up` and `den exec`: prepare
// an instance from its nest, refusing before any side effect, then create it.
package instance

import (
	"io"

	"github.com/PillowPillow/den/v2/internal/nest"
	"github.com/PillowPillow/den/v2/internal/prompt"
	"github.com/PillowPillow/den/v2/internal/sbx"
	"github.com/PillowPillow/den/v2/internal/sshagent"
	"github.com/PillowPillow/den/v2/internal/worktree"
)

// Deps is every system access an instance command needs, injected so tests stay hermetic.
type Deps struct {
	Sbx      sbx.Runner
	Git      worktree.Git
	Prompt   prompt.Prompter
	DenHome  string
	Cwd      string
	Out, Err io.Writer
	Agent    sshagent.Probe // nil: den does not check the SSH agent
	IsTTY    func() bool    // nil: no terminal, so den never asks
}

// Options is what the command line asks of one instance.
type Options struct {
	Nest      string
	Branch    string // -w, as typed
	Selection nest.Selection
	NoInput   bool   // den exec
	Workdir   string // --workdir override, VM path, passed verbatim
}
