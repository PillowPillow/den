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
			// very port the user ran the command for. Nothing is published until
			// everything asked for is admissible.
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

			res, err := ports.Resolve(n, ports.Options{Extra: extra}, scanner)
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
func publishPorts(ctx context.Context, runner sbx.Runner, sandbox string, list []ports.Port) error {
	for _, p := range list {
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
		// WHO holds the canonical window is NOT said, because den does not know
		// it. ports.Resolve scans the host through ports.Scanner, and a bound
		// port names no owner: the holder may be another instance of the nest —
		// or this very sandbox, whose previous `den ports` run published that
		// window and never took it down. That second case is the ordinary one
		// (re-running the command to re-read the table), and the sentence this
		// warning used to carry — "the first instance of the nest keeps the
		// canonical window" — told the user, precisely then, that a rival
		// instance owned a window nobody but them was on.
		//
		// So the warning names both candidates and hands over the one command
		// that settles it: `sbx ports SANDBOX`, the bare attested form of spec
		// §14.0 (den reads no JSON here — the schema of `sbx ports --json` is
		// not in the 2026-07-28 survey, and this package assumes no surface the
		// survey does not attest). Same discipline as the SSH-agent warning's
		// key-name caveat: den names the limit, it does not guess.
		canonical := res.Window.Base - res.Window.Shifts*ports.WindowSize
		fmt.Fprintf(cmd.ErrOrStderr(),
			"warning: nest %q: the canonical window %d-%d is busy — den moved the whole block to %d-%d, "+
				"so the addresses below are valid for THIS INSTANCE ONLY and are not the ones to bookmark; "+
				"den cannot tell who holds it (the scan reads the host, which says a port is bound, never "+
				"by whom): either another instance of the nest, or an earlier `den ports %s` run of THIS "+
				"sandbox, still published on the canonical window — in which case those addresses are the "+
				"ones to keep and this run just bound a SECOND set of host ports for the same VM "+
				"(`sbx ports %s` lists what this sandbox publishes). Either way the block moves again on "+
				"the next run while the host stays busy.\n",
			res.Nest, canonical, canonical+ports.WindowSize-1,
			res.Window.Base, res.Window.Last(), sandbox, sandbox)
	}
	fmt.Fprintf(out, "nest: %s   sandbox: %s   window: %s\n", res.Nest, sandbox, window)

	// Same tabwriter settings as `den ls`: the two tables are read in the same
	// terminal, and a second alignment convention would look like a bug.
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
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
	if err := w.Flush(); err != nil {
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
