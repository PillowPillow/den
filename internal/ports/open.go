package ports

import (
	"fmt"
	"os/exec"
	"runtime"
)

// OpenURL hands a URL to the host's browser, and is the ONLY file of this
// package that spawns a process — the second system access of internal/ports,
// isolated exactly as ListenScanner isolates the first. It lives here, and not
// in internal/cli, so that the command stays free of os/exec: the wiring layer
// names the dependency, this package owns it.
//
// Untested by construction: any test would launch a real browser on the machine
// running the suite. The whole of `den ports`' behaviour around it — which
// ports are opened, with which URL, how many times, what a failure costs — is
// tested against an injected double on cli.Deps.Open, which is why this
// function must stay a dispatch and nothing more. Growing a decision into it is
// how that boundary gets lost (see ListenScanner's own note).
//
// START, NOT RUN: the process is launched and never waited on. `open` returns
// immediately, but `xdg-open` can stay alive for as long as the browser it
// started, and waiting for it would hang `den ports` AFTER the table was
// printed and the ports published — a command that looks finished but never
// returns the prompt. Starting still surfaces the failure worth reporting: a
// launcher missing from the host, which is what an unopened URL means here.
//
// The default branch NAMES the OS rather than returning nil: den has no opener
// for it, and reporting success for a browser nobody launched would leave the
// user waiting for a window that is never coming.
func OpenURL(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		// The empty argument is `start`'s TITLE, not padding: given a single
		// quoted argument, cmd's start reads it as the window title and opens
		// nothing at all.
		cmd = exec.Command("cmd", "/c", "start", "", url)
	case "linux", "freebsd", "netbsd", "openbsd":
		cmd = exec.Command("xdg-open", url)
	default:
		return fmt.Errorf("den does not know how to open a browser on %s: open %s yourself",
			runtime.GOOS, url)
	}
	// Streams left nil, i.e. /dev/null: a launcher writing its own diagnostics
	// into den's stdout would land in the middle of the §8 table, which a
	// caller may be piping.
	return cmd.Start()
}
