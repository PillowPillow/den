package sbx

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// mcpStore and skillsStore name the two rows under test, so a test reads the
// surface it means rather than an index into Stores.
func storeNamed(t *testing.T, name string) Store {
	t.Helper()
	for _, s := range Stores {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no store named %q in Stores", name)
	return Store{}
}

// The measured empty output — gateway header included — must read as EMPTY.
//
// This is THE case an emptiness test gets wrong: `sbx mcp ls` prints its
// gateway header with nothing registered (spec §14.3), so a reader judging on
// output length reports a fresh machine as holding servers.
func TestReadStoreReadsTheMeasuredEmptyOutputAsEmpty(t *testing.T) {
	for _, tc := range []struct {
		name   string
		store  string
		output string
	}{
		{
			name:   "mcp",
			store:  "sbx mcp servers",
			output: "LOCAL · managed by you · ✓ on\n\nNo MCP servers registered\n  add one   sbx mcp add <name> --url <url>\n",
		},
		{
			name:   "skills",
			store:  "sbx skills",
			output: "Skills store: /tmp/sbx/agent-skills\nNo skills found. Use 'sbx skills import' to add skills.\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := storeNamed(t, tc.store)
			f := &Fake{Responses: map[string]Response{
				strings.Join(s.Args, " "): {Output: []byte(tc.output)},
			}}
			occupied, err := ReadStore(context.Background(), f, s)
			if err != nil {
				t.Fatalf("ReadStore: %v", err)
			}
			if occupied {
				t.Errorf("the measured empty output of `%s` reads as occupied", s.Look)
			}
		})
	}
}

// Anything the sentinel does not claim is OCCUPIED. The fail direction is the
// whole design: an output den does not recognize must become "something is
// there, go look" and never "this surface is empty".
func TestReadStoreReadsAnUnrecognizedOutputAsOccupied(t *testing.T) {
	s := storeNamed(t, "sbx mcp servers")
	for _, tc := range []struct {
		name   string
		output string
	}{
		{"a body den cannot parse", "LOCAL · managed by you · ✓ on\n\nnotion   http   ready\n"},
		{"a layout sbx changed", "GATEWAY local\nnothing here yet\n"},
		{"an empty output", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &Fake{Default: Response{Output: []byte(tc.output)}}
			occupied, err := ReadStore(context.Background(), f, s)
			if err != nil {
				t.Fatalf("ReadStore: %v", err)
			}
			if !occupied {
				t.Errorf("an output carrying no sentinel read as empty: %q", tc.output)
			}
		})
	}
}

// The update banner must not sit between den and the sentinel — the regression
// StripUpdateBanner exists for, on the surface next door.
func TestReadStoreSeesThroughTheUpdateBanner(t *testing.T) {
	s := storeNamed(t, "sbx skills")
	banner := "╭──────────────────────────────╮\n" +
		"│ Docker Sandboxes Update      │\n" +
		"╰──────────────────────────────╯\n"
	f := &Fake{Default: Response{Output: []byte(banner +
		"Skills store: /tmp/sbx/agent-skills\nNo skills found. Use 'sbx skills import' to add skills.\n")}}
	occupied, err := ReadStore(context.Background(), f, s)
	if err != nil {
		t.Fatalf("ReadStore: %v", err)
	}
	if occupied {
		t.Error("the update banner made an empty skills store read as occupied")
	}
}

// A command that FAILS is an error, never an empty store: "den could not look"
// and "den looked and found nothing" are different facts, and the second one
// invented here would hide the very state this reader exists to surface.
func TestReadStoreRefusesRatherThanReportingAnEmptyStore(t *testing.T) {
	s := storeNamed(t, "sbx mcp servers")
	boom := errors.New("sbx: gateway unreachable")
	f := &Fake{Default: Response{Err: boom}}
	occupied, err := ReadStore(context.Background(), f, s)
	if err == nil {
		t.Fatalf("a failed read returned occupied=%v and no error", occupied)
	}
	if !errors.Is(err, boom) {
		t.Errorf("the underlying failure is not in the chain: %v", err)
	}
	// The command is named: the user reproduces it to see sbx's own message.
	if !strings.Contains(err.Error(), s.Look) {
		t.Errorf("the error does not name the command to run: %v", err)
	}
}

// The argv den sends is the read command and nothing else — no `--json`, which
// v0.39.0 refuses on both surfaces (spec §14.3).
func TestReadStoreSendsThePlainReadCommand(t *testing.T) {
	for _, s := range Stores {
		f := &Fake{}
		if _, err := ReadStore(context.Background(), f, s); err != nil {
			t.Fatalf("ReadStore(%s): %v", s.Name, err)
		}
		if !f.HasCalled(s.Args...) {
			t.Errorf("%s: den did not run %v, calls=%v", s.Name, s.Args, f.Calls)
		}
		for _, call := range f.Calls {
			for _, a := range call {
				if a == "--json" {
					t.Errorf("%s: den sent --json, which sbx v0.39.0 refuses", s.Name)
				}
			}
		}
	}
}
