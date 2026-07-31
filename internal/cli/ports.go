package cli

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
	"github.com/PillowPillow/den/internal/ports"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/spf13/cobra"
)

// newPortsCmd resolves where a sandbox's declared ports land on the host and
// prints the §8 table.
//
// The argument is a SANDBOX name, like `den sh` and `den rm` — the ports are
// published into a live VM, and only a sandbox name says which one. But the
// WINDOW is seeded by the NEST the name belongs to (sbx.SplitName), never by
// the sandbox: §8 promises a bookmarkable URL per nest, and a window hashed
// from "api.feat12" would hand every worktree its own base, so the URL a user
// wrote down would depend on which worktree happened to be running.
//
// denHome is a POINTER for the same reason newRmCmd's is: --den-home is a
// persistent flag, and its value only exists once cobra has parsed it, well
// after this constructor returned.
//
// scanner and open are injected rather than constructed here: they are the two
// system accesses of internal/ports, and the real ones bind host sockets and
// spawn a browser (see Deps.Scanner and Deps.Open). open may be nil, which
// makes the opening step a no-op — that is what keeps every command tree built
// by hand in the tests from launching anything.
func newPortsCmd(denHome *string, runner sbx.Runner, scanner ports.Scanner,
	open func(url string) error) *cobra.Command {
	var add []string
	cmd := &cobra.Command{
		Use:   "ports <name>",
		Short: "Show where a sandbox's declared ports land on the host",
		Args:  exactlyOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			// `--add` FIRST, before a single call is made to sbx: what it can
			// refuse — a bind address that is not the loopback, a value that is
			// not a pair — is knowable from the flag alone, and a refusal found
			// later would leave the declared ports published while rejecting the
			// very port the user ran the command for. Every refusal DEN can make
			// is made before anything is published — but only den's: the added
			// host port is never scanned (Options.Extra's contract — re-running
			// the command must re-read a table whose added pair THIS sandbox
			// already publishes), so sbx can still refuse it host-side after the
			// declared ports are bound. That failure aborts like any other
			// (declared ports stay published, no table), and the runner's own
			// error says which port it was.
			extra, err := parseAdds(add)
			if err != nil {
				return err
			}

			// The sandbox is checked BEFORE the nest is even read: a window
			// resolved for a VM that does not exist, or that den cannot attach
			// to, is a set of URLs nobody can reach — and computing it would
			// mean scanning the host for nothing.
			boxes, err := sbx.Ls(cmd.Context(), runner)
			if err != nil {
				return err
			}
			b := sbx.Find(boxes, name)
			if b == nil {
				// The exact sentence of `den rm` and `den sh`: one situation,
				// one wording. A third dialect here would be a message users
				// have to learn twice.
				return fmt.Errorf("sandbox %q not found (live: %v)", name, liveNames(boxes))
			}
			// The SHARED guard, not a local rule: publishing into a VM den
			// knows nothing about is no more defensible than attaching to it,
			// and sbx.Sandbox.CheckAttachable is where that verdict lives.
			if err := b.CheckAttachable(); err != nil {
				return err
			}

			// The den home is read HERE rather than at the top like `den rm`:
			// everything above answers from `sbx ls --json` alone, so a sandbox
			// that does not exist is reported as such even on a machine whose
			// den home cannot be located.
			nestName, _ := sbx.SplitName(name)
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}
			n, err := nest.LoadNest(home, nestName)
			if err != nil {
				return err
			}

			res, err := ports.Resolve(n, ports.Options{
				Extra: extra,
				// WHAT THIS SANDBOX ALREADY PUBLISHES, read before anything is
				// resolved — the whole of the fix for #15/#22. Without it the
				// scan finds den's own previous window, calls it busy, and
				// binds a second set of host ports for the same VM.
				//
				// Read from the `ls --json` already fetched above rather than
				// from a second `sbx ports <name>`: same answer (both were
				// measured against the same VM on 2026-07-31), one process
				// fewer, and no third place where a sandbox name is spelled.
				Published: publicationsOf(b),
			}, scanner)
			if err != nil {
				return err
			}

			// Publication BEFORE the render: the table states where the ports
			// landed, and printing it for a window sbx refused would hand the
			// user URLs nothing is listening on.
			if err := publishPorts(cmd.Context(), runner, name, res.Ports); err != nil {
				return err
			}
			if err := renderPorts(cmd, name, res); err != nil {
				return err
			}
			// LAST, after the table: the browser is a courtesy on top of the
			// output, and a URL opened before the row that names it would leave
			// a user staring at a window with nothing on screen to say which
			// port it is.
			openMarkedPorts(cmd, open, res.Ports)
			return nil
		},
	}
	// StringArray, not StringSlice like `--without` and `--only`: those take
	// comma-separated LISTS, while `H:C` is a single token that must reach
	// ports.ParseAdd exactly as the user typed it. Repeating the flag is how a
	// second pair is asked for.
	cmd.Flags().StringArrayVar(&add, "add", nil,
		"publish an extra HOST_PORT:CONTAINER_PORT pair the nest does not declare (repeatable)")
	return cmd
}

// publicationsOf translates what `sbx ls --json` says a sandbox publishes into
// the pairs internal/ports reasons about.
//
// THE FILTER IS THE POINT. Only publications on ports.Loopback over tcp are
// den's: den binds nothing else anywhere (spec §8), and sbx keys its "already
// published" refusal on the triple address/port/protocol — so a publication on
// another address is neither reusable as den's own window nor in the way of
// one, and counting it would make den skip a publication it never made.
//
// An empty result on a STOPPED sandbox means nothing at all: sbx omits the
// `ports` array entirely until the VM is running, while the publications come
// back on resume (sbx.Publication). This is why the caller must not reach here
// with a stopped sandbox — the reading would be a fiction, and acting on it
// republishes a window that is already bound.
func publicationsOf(b *sbx.Sandbox) []ports.Published {
	if b.Status != sbx.StatusRunning {
		return nil
	}
	out := make([]ports.Published, 0, len(b.Ports))
	for _, p := range b.Ports {
		if p.HostIP != ports.Loopback || p.Protocol != "tcp" {
			continue
		}
		out = append(out, ports.Published{Host: p.HostPort, Container: p.SandboxPort})
	}
	return out
}

// parseAdds turns the `--add` values into the pairs ports.Resolve appends to the
// declared window, and stops on the FIRST one it cannot accept.
//
// It decides nothing: every verdict — the shape of a pair, and the refusal of
// any bind address but the loopback — is ports.ParseAdd's, where the port rules
// live. This is the loop, and only the loop.
func parseAdds(values []string) ([]ports.Port, error) {
	out := make([]ports.Port, 0, len(values))
	for _, v := range values {
		p, err := ports.ParseAdd(v)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// openMarkedPorts hands each `open: true` port's URL to the injected opener —
// once, and only for the ports that declared it.
//
// It returns NOTHING, and that is the contract: the table is already on stdout
// and the window is already published, so a browser that would not start
// changes nothing about what den accomplished. Failing the command over it
// would tell a script the publication failed when it did not. The user hears
// about it on stderr, where every other diagnostic of this command lands.
//
// A nil opener loops without doing anything: `den ports` still publishes and
// still renders, which is exactly what a command tree built without an opener
// must do rather than panic.
func openMarkedPorts(cmd *cobra.Command, open func(url string) error, list []ports.Port) {
	if open == nil {
		return
	}
	for _, p := range list {
		if !p.Open {
			continue
		}
		if err := open(portURL(p)); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"warning: port %q is published, but den could not open it in a browser: %v\n",
				p.Name, err)
		}
	}
}

// publishPorts binds the resolved window into the live VM: ONE `sbx ports
// <sandbox> --publish 127.0.0.1:H:C` per declared port, in the order
// ports.Resolve returned them — which is the nest's declaration order, the very
// order that assigned each port its host number.
//
// One call per port, never a single argv carrying N --publish flags: sbx
// answers a call as a whole, so a batch failing on one port already taken in
// the VM would take the entire window down with it, and the error would quote a
// spec without saying which of the nest's ports it belongs to. Per port, the
// failure names the port that could not be bound.
//
// The runner's error travels BARE, like `den rm`'s: sbx already says what went
// wrong with a publication, and a den-flavoured prefix would only push its own
// sentence further from the reader.
//
// A nest declaring no port loops zero times — no `sbx ports` call at all, which
// is the second half of the promise ports.Resolve keeps host-side by never
// scanning for it.
//
// A port the sandbox ALREADY publishes is skipped, and that skip is the command
// becoming re-runnable. `sbx ports --publish` is not idempotent: republishing a
// pair the same sandbox carries answers `409 Conflict: port 127.0.0.1:<H>/tcp is
// already published` (#22), and it answered it AFTER the declared window had
// been bound a second time — a command that both failed and left a mess. den
// publishes only what is missing, so the second run of an identical command
// makes no `sbx ports` call at all and prints the same table as the first.
func publishPorts(ctx context.Context, runner sbx.Runner, sandbox string, list []ports.Port) error {
	for _, p := range list {
		if p.AlreadyPublished {
			continue
		}
		if _, err := runner.Run(ctx, "ports", sandbox, "--publish", p.PublishSpec()); err != nil {
			return err
		}
	}
	return nil
}

// renderPorts writes the §8 table on STDOUT and, when the window had to move,
// the warning on STDERR — the split internal/spawn applies to its own
// warnings: what a script pipes is the table, and only the table.
//
// The scheme is `http://` on every row. §8's own sample writes `ws://` for the
// CDP port, but `ports.publish` declares no protocol: den would be guessing
// from a port's name, and guessing wrong on the row a user is most likely to
// paste. One scheme, uniformly, is the honest render — the endpoint's protocol
// is the endpoint's business.
func renderPorts(cmd *cobra.Command, sandbox string, res *ports.Resolution) error {
	out := cmd.OutOrStdout()

	// A PORTLESS nest has no window on the host: ports.Resolve placed nothing
	// in the range (Resolution.Declared is zero), so a header claiming
	// `window: 9100-9109 (canonical)` — and, below, a tunnel line on its base
	// — would describe ports nothing listens on. The header then names the
	// nest and the sandbox and stops; the table still renders, because the
	// `--add` pairs (when any) really were published and their rows are the
	// whole point of running the command on such a nest.
	if res.Declared == 0 {
		fmt.Fprintf(out, "nest: %s   sandbox: %s\n", res.Nest, sandbox)
		return renderPortRows(cmd, res)
	}

	window := fmt.Sprintf("%d-%d", res.Window.Base, res.Window.Last())
	if res.Window.Canonical {
		window += " (canonical)"
	} else {
		// STDERR ONLY. A shifted window's header carries the range and stops
		// there: what a script pipes is the table, and a diagnosis smuggled
		// into the header would be a line the pipe has to parse around. The
		// absence of ` (canonical)` is what the table itself says; the reason,
		// the range that was taken and the fact that these addresses hold for
		// this instance alone are said here, to the human.
		//
		// WHO holds the canonical window is still not fully known — the scan
		// reads the host, and a bound port names no owner — but ONE candidate
		// is now excluded, and it was the likeliest by far: this sandbox's own
		// earlier publication. den reads what the sandbox publishes before
		// resolving and reuses that window instead of shifting past it, so a
		// shift here can no longer be den moving away from itself.
		//
		// The warning this replaced had to hedge, and the hedge was the defect:
		// re-reading the table was the ordinary gesture, it shifted every time,
		// and the message told the user a rival instance owned a window nobody
		// but them was on — while the run bound ten more host ports (#15).
		//
		// What remains genuinely ambiguous is named as such, and the one case
		// den CAN name is named: publications of this sandbox that no longer
		// match a declaration (res.Stale), with the command that clears them.
		canonical := res.Window.Base - res.Window.Shifts*ports.WindowSize
		fmt.Fprintf(cmd.ErrOrStderr(),
			"warning: nest %q: the canonical window %d-%d is busy — den moved the whole block to %d-%d, "+
				"so the addresses below are valid for THIS INSTANCE ONLY and are not the ones to bookmark. "+
				"It is not this sandbox publishing there: den read what %s already publishes and would have "+
				"reused it. So the holder is another sandbox of yours, or a foreign listener (`sbx ports "+
				"SANDBOX` lists what a sandbox publishes; `lsof -nP -iTCP:%d -sTCP:LISTEN` names the "+
				"process).%s\n",
			res.Nest, canonical, canonical+ports.WindowSize-1,
			res.Window.Base, res.Window.Last(), sandbox, canonical, staleClause(sandbox, res))
	}
	fmt.Fprintf(out, "nest: %s   sandbox: %s   window: %s\n", res.Nest, sandbox, window)

	if err := renderPortRows(cmd, res); err != nil {
		return err
	}

	// `you@$(hostname)` is a LITERAL: den never resolves it. The line is meant
	// to be pasted into the user's OWN shell, where $(hostname) expands on the
	// machine that will run the tunnel — and `you` is the one thing den cannot
	// know, since the remote account is not the local one.
	fmt.Fprintf(out, "  remote?  ssh -L %d:%s:%d you@$(hostname)\n",
		res.Window.Base, ports.Loopback, res.Window.Base)
	return nil
}

// staleClause names the publications of THIS sandbox that sit in the blocks the
// resolution had to skip, and the command that releases them. Empty — not a
// sentence saying "none" — when there are none: the ordinary shift has nothing
// to do with den's own leftovers, and a clause about an empty list would read
// as an accusation.
//
// These are the residue of #15 on a sandbox that lived through it: windows an
// earlier den bound and that no longer map onto any declaration (the nest's
// `ports:` changed since, or a `--add` pair took a port the window now wants).
// den does NOT unpublish them by itself — releasing a port a user may be
// tunnelling through is a destruction they did not ask for, and `den rm`
// already frees every publication when the VM goes.
func staleClause(sandbox string, res *ports.Resolution) string {
	if len(res.Stale) == 0 {
		return ""
	}
	specs := make([]string, 0, len(res.Stale))
	for _, p := range res.Stale {
		specs = append(specs, fmt.Sprintf("%s:%d:%d", ports.Loopback, p.Host, p.Container))
	}
	return fmt.Sprintf(
		" What den can name: %s itself still publishes %s in that range, on mappings the nest no "+
			"longer declares — den left them alone rather than unpublish behind your back; clear them "+
			"with `sbx ports %s --unpublish %s` to get the canonical window back.",
		sandbox, strings.Join(specs, " "), sandbox, strings.Join(specs, " --unpublish "))
}

// renderPortRows writes the column header and one row per resolved port — the
// part of the §8 table both headers share, whether or not a window was placed.
func renderPortRows(cmd *cobra.Command, res *ports.Resolution) error {
	// Same tabwriter settings as `den ls`: the two tables are read in the same
	// terminal, and a second alignment convention would look like a bug.
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  NAME\tCONTAINER\tURL")
	for _, p := range res.Ports {
		// DECLARATION ORDER, never sorted: it IS the offset (ports.Resolve),
		// so a sorted table would number the ports differently from the host.
		row := fmt.Sprintf("  %s\t%d\t%s", p.Name, p.Container, portURL(p))
		// The markers are a trailing cell only when there ARE markers: a cell
		// always emitted would pad every URL to the column width and leave
		// trailing spaces on every unmarked row.
		if m := portMarkers(p); m != "" {
			row += "\t" + m
		}
		fmt.Fprintln(w, row)
	}
	return w.Flush()
}

// portURL is the address a resolved port answers on — written ONCE, because the
// table and the browser must state the same thing. Two formatters would be two
// places to change the day the scheme or the host does, and the day they
// disagree den would open a URL no row ever showed.
//
// The scheme is `http://` for every port, for the reason renderPorts explains:
// a declaration carries no protocol, and guessing one from a port's name would
// guess wrong on the row a user is most likely to paste.
func portURL(p ports.Port) string {
	return fmt.Sprintf("http://%s:%d", ports.Loopback, p.Host)
}

// portMarkers renders the flags a declaration carries, in the order §8 lists
// them. Both can be true at once, so this returns a joined string rather than
// picking one: a port that is both opened and loopback-locked must not lose
// half of what the nest said about it.
func portMarkers(p ports.Port) string {
	var m []string
	if p.Open {
		m = append(m, "[opened]")
	}
	if p.LoopbackLock {
		m = append(m, "[loopback-locked]")
	}
	return strings.Join(m, " ")
}
