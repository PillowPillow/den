package spawn

import (
	"context"

	"github.com/PillowPillow/den/internal/sbx"
)

// Command is what den runs in a live sandbox, and how.
//
// Argv EMPTY means `bash -l`: the interactive shell is not a special case in
// this package, it is the default argument. That is the whole shape of #60 —
// one door, two modes — and putting the default here rather than at each call
// site is what keeps `den exec` and `den spawn` from drifting apart.
type Command struct {
	// Argv is the command and its arguments. Empty ⇒ `bash -l`.
	Argv []string
	// Workdir is passed to sbx as -w. Empty ⇒ not passed at all, and the VM
	// decides. Callers take it from the first workspace the VM REPORTS, never
	// from a path recomputed out of the configuration.
	//
	// It applies to a COMMAND exactly as it applies to the shell, and that was
	// arbitrated rather than inherited. The alternative — no workdir unless
	// --workdir is given — would drop `den exec api go test ./...` into the
	// VM's home, where it fails for a reason nothing on screen explains. A
	// caller holding only a sandbox name has no better cwd to offer, and one
	// that does has --workdir.
	Workdir string
	// TTY allocates a pseudo-terminal (`sbx exec -it`). It also decides WHICH
	// runner method carries the call — see Enter.
	TTY bool
}

// Enter runs c in the live sandbox named sandboxName.
//
// This is the ONLY place in den that builds an `sbx exec` argv. It replaced
// spawn.Attach, which built the interactive one; a second builder is exactly
// how the two doors would come to disagree about the flag order that spec
// §14.0 pins down (`sbx exec [flags] SANDBOX COMMAND [ARG...]` — a postponed
// -w lands on `bash -l` as an argument instead of setting the directory).
//
// The TTY decides the METHOD, and that is not a detail:
//
//   - with a tty, Attach — it hands the terminal over and deliberately lets
//     the context cancellation do nothing, so a Ctrl-C typed inside the shell
//     is the tty driver's business and never leaves the terminal in raw mode;
//   - without one, Pipe — the three descriptors pass through unmerged and the
//     context can kill the child, which is what a Ctrl-C on
//     `den exec -T api go test ./...` must do.
//
// No flag at all on the non-tty path: a piped stdin reaches the command with
// none on sbx v0.38.0 (measured 2026-08-10, spec §14.0), unlike docker exec,
// which needs -i. den passes what the attested surface needs and nothing else.
//
// A child that exits nonzero comes back as *sbx.ChildExit, so `den exec api false`
// exits 1 as the command did rather than as den failing. Anything else
// — sbx missing (exec.ErrNotFound, no ExitCode()), a cancellation (a signalled
// child answers -1, which ExitCodeOf refuses: internal/sbx/exit.go) — stays
// den's error, with den's message. cmd/den/main.go tells the two apart
// (cli.ExitStatus).
//
// The real rule is wider than the command's own failure: ANY nonzero status
// sbx itself returns becomes the child's too, because den cannot tell sbx's
// own refusal (a sandbox that vanished between sbx.Ls and this call, say)
// from the command's failure by the status alone — both doors already run
// sbx.Ls + sbx.Find + CheckAttachable first, so this is a race window, not
// the common case, and sbx's own message still reaches the user unmerged
// (Pipe and Attach both pass stderr through).
//
// That covers the interactive branch too, deliberately: a shell the user
// leaves with `exit 3` exits den with 3 and prints nothing, where the
// previous spawn.Attach reported `den: sbx exec -it … : exit status 3` and
// exited 1 — den claiming a failure of its own over an ordinary shell exit.
// Gating this on the tty would make den's status depend on whether a
// terminal happened to be attached.
func Enter(ctx context.Context, r sbx.Runner, sandboxName string, c Command) error {
	argv := []string{"exec"}
	if c.TTY {
		argv = append(argv, "-it")
	}
	if c.Workdir != "" {
		argv = append(argv, "-w", c.Workdir)
	}
	argv = append(argv, sandboxName)
	if len(c.Argv) == 0 {
		argv = append(argv, "bash", "-l")
	} else {
		argv = append(argv, c.Argv...)
	}

	var err error
	if c.TTY {
		err = r.Attach(ctx, argv...)
	} else {
		err = r.Pipe(ctx, argv...)
	}
	if err == nil {
		return nil
	}
	if code, ok := sbx.ExitCodeOf(err); ok {
		return &sbx.ChildExit{Code: code}
	}
	return err
}
