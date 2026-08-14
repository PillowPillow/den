package sbx

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// REAL output of `sbx ls --json` (sbx v0.35.0, recorded 2026-07-28),
// ANONYMIZED: the record is genuine, but the paths and identifier are
// replaced.
//
// What's preserved has evidentiary value — the SCHEMA, and only that: root
// key `sandboxes`, the five fields of an entry, `workspaces` as an array of
// ABSOLUTE paths, one of which carries the `:ro` suffix, an `id` shaped like a
// UUID (8-4-4-4-12), and the ABSENCE of any date field — it's what got the
// "age" column dropped from spec §5.
//
// What's replaced had none: the original paths were those of the development
// machine and named a third party. The record's evidentiary value is real;
// so would the leak be if the repo went public.
const realLsOutput = `{
  "sandboxes": [
    {
      "name": "den",
      "id": "11111111-2222-4333-8444-555555555555",
      "agent": "shell",
      "status": "running",
      "workspaces": [
        "/Users/dev/Development/Example/project",
        "/Users/dev/.agent_sbx",
        "/Users/dev/Development/Other/dependency:ro"
      ]
    }
  ]
}`

func TestLsDecodesRealOutput(t *testing.T) {
	f := &Fake{Responses: map[string]Response{
		"ls --json": {Output: []byte(realLsOutput)},
	}}

	boxes, err := Ls(context.Background(), f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(boxes) != 1 {
		t.Fatalf("boxes = %v, want 1", boxes)
	}
	b := boxes[0]
	if b.Name != "den" || b.Agent != "shell" || b.Status != "running" {
		t.Errorf("sandbox = %+v", b)
	}
	if len(b.Workspaces) != 3 {
		t.Errorf("Workspaces = %v, want 3", b.Workspaces)
	}
	// Workdir serves as -w for the attach: the :ro suffix must be stripped, and
	// it's the FIRST workspace (the repo, not the agent profile).
	if got := b.Workdir(); got != "/Users/dev/Development/Example/project" {
		t.Errorf("Workdir = %q", got)
	}
}

func TestLsAssignsNestAndWorktree(t *testing.T) {
	f := &Fake{Responses: map[string]Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api.feat12","status":"running","workspaces":["/w"]}]}`)},
	}}

	boxes, err := Ls(context.Background(), f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if boxes[0].Nest() != "api" || boxes[0].Instance() != "feat12" {
		t.Errorf("nest/instance = %q/%q", boxes[0].Nest(), boxes[0].Instance())
	}
}

// Everything displayed is sorted (repo convention) — and sbx guarantees no
// order.
func TestLsSortsByName(t *testing.T) {
	f := &Fake{Responses: map[string]Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"zeta"},{"name":"alpha"},{"name":"mu"}]}`)},
	}}

	boxes, err := Ls(context.Background(), f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i, want := range []string{"alpha", "mu", "zeta"} {
		if boxes[i].Name != want {
			t.Errorf("boxes[%d].Name = %q, want %q", i, boxes[i].Name, want)
		}
	}
}

func TestLsNoSandbox(t *testing.T) {
	f := &Fake{Responses: map[string]Response{
		"ls --json": {Output: []byte(`{"sandboxes":[]}`)},
	}}

	boxes, err := Ls(context.Background(), f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(boxes) != 0 {
		t.Errorf("boxes = %v, want empty", boxes)
	}
}

// Unreadable JSON must produce a message that contains the raw output:
// without it, a schema change on sbx's side is undiagnosable.
func TestLsUnreadableOutput(t *testing.T) {
	f := &Fake{Responses: map[string]Response{
		"ls --json": {Output: []byte("not json")},
	}}

	if _, err := Ls(context.Background(), f); err == nil {
		t.Fatal("non-JSON output must produce an error")
	} else if !containsAll(err.Error(), "sbx ls", "not json") {
		t.Errorf("unhelpful message: %v", err)
	}
}

// TRUNCATED output — valid JSON up to the cut — must fail loudly, never read
// as a short list.
//
// This case isn't theoretical: Run bounds pipe drain (see
// defaultDrainDelay) and now returns SUCCESS when the process exited cleanly,
// even if a descendant still held the pipe open. The direct child's output is
// complete (measured, see runner_test.go), but that's exactly the kind of
// property an os/exec or sbx change could silently drop — and a silently
// amputated sandbox list would make `den spawn` recreate a sandbox that's
// already running.
func TestLsTruncatedOutput(t *testing.T) {
	complete := `{"sandboxes":[{"name":"api","status":"running"},{"name":"web","status":"running"}]}`
	for _, cut := range []int{len(complete) - 1, len(complete) / 2, 20} {
		truncated := complete[:cut]
		t.Run(truncated, func(t *testing.T) {
			f := &Fake{Responses: map[string]Response{"ls --json": {Output: []byte(truncated)}}}

			if _, err := Ls(context.Background(), f); err == nil {
				t.Fatalf("truncated output must produce an error, not an amputated list; output = %q", truncated)
			} else if !containsAll(err.Error(), "sbx ls", truncated) {
				t.Errorf("unhelpful message: %v", err)
			}
		})
	}
}

// Valid JSON with the wrong schema (sbx renaming "sandboxes") must not read
// as "no sandbox": that's indistinguishable from a genuine zero to the
// caller, and den ls/sh/rm would then wrongly claim no sandbox is running.
// The raw output must stay in the message, for the same reason as unreadable
// JSON.
func TestLsMissingSandboxesKey(t *testing.T) {
	f := &Fake{Responses: map[string]Response{
		"ls --json": {Output: []byte(`{"somethingelse":[]}`)},
	}}

	if _, err := Ls(context.Background(), f); err == nil {
		t.Fatal("a missing sandboxes key must produce an error")
	} else if !containsAll(err.Error(), "sbx ls", "sandboxes", "somethingelse") {
		t.Errorf("unhelpful message: %v", err)
	}
}

// The "sandboxes" key present but empty or null stays a zero-sandbox
// success: sbx ls --json has never been observed without a live sandbox, and
// nothing guarantees which of the two JSON forms it produces. Both must work.
func TestLsZeroSandboxesEmptyOrNil(t *testing.T) {
	for _, output := range []string{`{"sandboxes":[]}`, `{"sandboxes":null}`} {
		f := &Fake{Responses: map[string]Response{
			"ls --json": {Output: []byte(output)},
		}}

		boxes, err := Ls(context.Background(), f)
		if err != nil {
			t.Fatalf("output %s: unexpected error: %v", output, err)
		}
		if len(boxes) != 0 {
			t.Errorf("output %s: boxes = %v, want empty", output, boxes)
		}
	}
}

func TestLsPropagatesRunnerError(t *testing.T) {
	sentinel := errors.New("sbx not found")
	f := &Fake{Default: Response{Err: sentinel}}

	if _, err := Ls(context.Background(), f); !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want the wrapped sentinel", err)
	}
}

// Ls must not REPEAT the subcommand the runner's error already names.
//
// Measured against the real binary, sbx missing from the PATH:
//
//	den: sbx ls: sbx ls --json: exec: "sbx": executable file not found in $PATH
//
// "sbx ls" twice on the first line a new user sees: once from Ls's wrapper,
// once from ExecError.Error, which ALWAYS renders the binary and its full
// argv. It's the wrapper that's redundant — it adds nothing the argv doesn't
// say better ("ls --json" rather than "ls").
//
// The *ExecError is built here rather than produced by a real Exec: it's the
// type production returns, and building it lets us exercise the Ls+ExecError
// composition without depending on any binary.
func TestLsDoesNotRepeatTheSubcommand(t *testing.T) {
	f := &Fake{Default: Response{Err: &ExecError{
		Bin:  "sbx",
		Args: []string{"ls", "--json"},
		Err:  errors.New("exit status 1"),
	}}}

	_, err := Ls(context.Background(), f)
	if err == nil {
		t.Fatal("a runner error must propagate")
	}
	message := err.Error()
	if n := strings.Count(message, "sbx ls"); n != 1 {
		t.Errorf("\"sbx ls\" appears %d times, want 1; message: %s", n, message)
	}
}

// And the first-contact case, seen from Ls — the path taken by the FOUR
// commands that touch sbx (`den ls`, `den spawn`, `den exec`, `den rm`): all of
// them call Ls before anything else.
func TestLsMissingBinaryProducesAnActionableMessage(t *testing.T) {
	f := &Fake{Default: Response{Err: &ExecError{
		Bin:  "sbx",
		Args: []string{"ls", "--json"},
		Err:  exec.ErrNotFound,
	}}}

	_, err := Ls(context.Background(), f)
	if err == nil {
		t.Fatal("a missing binary must produce an error")
	}
	message := err.Error()
	if !containsAll(message, "sbx", "not found in the PATH", "den doctor") {
		t.Errorf("unhelpful message: %s", message)
	}
	if strings.Contains(message, "executable file not found") {
		t.Errorf("leftover os/exec wording in the message: %s", message)
	}
}

func TestFind(t *testing.T) {
	boxes := []Sandbox{{Name: "api"}, {Name: "api.feat12"}, {Name: "web"}}

	b := Find(boxes, "api.feat12")
	if b == nil {
		t.Fatalf("Find(api.feat12) = nil, want the sandbox")
	}
	// The ADDRESS matters: callers read Status and Workspaces on the found
	// sandbox. A copy of another element would still pass a test on Name
	// alone.
	if b != &boxes[1] {
		t.Errorf("Find must return the slice's element; got %v", b)
	}
	if Find(boxes, "absent") != nil {
		t.Errorf("Find(absent) must return nil")
	}
	// The name is matched WHOLE: "api" must not capture "api.feat12", or a
	// `den spawn api` would attach into the worktree's sandbox.
	if b := Find([]Sandbox{{Name: "api.feat12"}}, "api"); b != nil {
		t.Errorf("Find must not match by prefix; got %v", b)
	}
}

// CheckAttachable is the guard shared by `den spawn` and `den exec`: both paths
// end in an `sbx exec`.
//
// "stopped" PASSES, and it's measured, not assumed (2026-07-29 smoke test,
// sbx v0.35.0): `sbx exec` restarts a stopped sandbox transparently ("Sandbox
// duo.essai started successfully"), the container-layer state survives the
// stop, and the dispatcher REPLAYS every kit startup command on resume —
// den's mixin included. A resumed VM is therefore functionally complete.
//
// That was the reservation justifying the refusal: it's lifted. Refusing cost
// more than the failure — sbx stops inactive sandboxes on its own, so the
// case is the NORM on returning to a `--detach` VM, and the remedy shown
// (`den rm`) destroyed state that would have survived.
//
// A WHITELIST regardless, and that's the rest of the test: other values sbx
// may emit are not accepted. A blacklist would let through any status a later
// version introduced — hence the "exited", "paused", "Running" and "" cases.
func TestCheckAttachable(t *testing.T) {
	for _, status := range []string{StatusRunning, StatusStopped} {
		if err := (Sandbox{Name: "api", Status: status}).CheckAttachable(); err != nil {
			t.Errorf("a %q sandbox must pass; got: %v", status, err)
		}
	}

	for _, status := range []string{"exited", "paused", "Running", ""} {
		t.Run("status="+status, func(t *testing.T) {
			err := Sandbox{Name: "api", Status: status}.CheckAttachable()
			if err == nil {
				t.Fatalf("a %q status must not be treated as running", status)
			}
			// Repo error format: the context, the detail, and the available
			// values. Without the status READ, the user doesn't know what den is
			// complaining about; without the EXPECTED statuses, they don't know
			// what would have worked.
			//
			// strconv.Quote rather than the bare status: on the status="" case,
			// `strings.Contains(err, "")` is true BY CONSTRUCTION and asserts
			// nothing (measured: removing s.Status from the message left this
			// subcase green while the other four went red). The quoted form is
			// what the message renders — %q — and it discriminates the empty
			// status too.
			if !containsAll(err.Error(), "api", strconv.Quote(status),
				strconv.Quote(StatusRunning), strconv.Quote(StatusStopped)) {
				t.Errorf("the message must render the sandbox, the read status and BOTH "+
					"attachable statuses; got: %v", err)
			}
		})
	}
}

// IsStopped distinguishes the two attachable statuses, because one of the two
// deserves to be said: resuming takes several seconds, and a silent
// `den spawn` during that time looks like a hang.
//
// It's also what keeps the code from sliding back into refusing: "should we
// warn?" is separate from "should we refuse?", and a caller must not answer
// the second while believing it answers the first.
func TestIsStopped(t *testing.T) {
	if !(Sandbox{Status: StatusStopped}).IsStopped() {
		t.Error("a \"stopped\" sandbox is stopped")
	}
	for _, status := range []string{StatusRunning, "exited", ""} {
		if (Sandbox{Status: status}).IsStopped() {
			t.Errorf("a %q status is not \"stopped\": only %q resumes",
				status, StatusStopped)
		}
	}
}

// The message must name ONLY ATTESTED sbx subcommands.
//
// Suggesting a possibly nonexistent command to the user is worse than a false
// comment: the comment only misleads a developer.
//
// WHITELIST — and that's the whole point of this test. An earlier version
// forbade only the single string "sbx start": a one-element BLACKLIST, which
// said nothing about `sbx resume` or a `den up --force` invented tomorrow
// (measured: a message enriched with both left the whole suite green). That
// was exactly the anti-pattern CheckAttachable argues against, ten lines up,
// for statuses. So we extract EVERYTHING the message presents as a command —
// the backtick segments — and require each to be attested.
func TestCheckAttachableOnlySuggestsAttestedCommands(t *testing.T) {
	// TWO sources of attestation, and they don't carry equal weight:
	//
	//   - `sbx` subcommands come from the 2026-07-28 survey, recorded in spec
	//     §14.0: create, ls, exec, ports, policy check, rm --force (sbx-devbox
	//     adds stop, template save, secret, inspect, login). `sbx start`
	//     doesn't appear there, and sbx isn't installable here: nobody can
	//     verify it. Extending this list needs a SURVEY, not a guess;
	//   - `den rm` is a DEN command, attested by the repo's own source
	//     (internal/cli/rm.go, newRmCmd). The "we don't know if it exists"
	//     argument doesn't apply: we can read it.
	attested := []string{"sbx ls", "sbx rm --force api", "den rm api"}

	err := Sandbox{Name: "api", Status: "exited"}.CheckAttachable()
	if err == nil {
		t.Fatal("an \"exited\" status must produce an error")
	}
	message := err.Error()

	commands := betweenBackticks(message)
	if len(commands) == 0 {
		t.Fatalf("the message must present its remediation between backticks; got: %v", err)
	}
	for _, c := range commands {
		if !slices.Contains(attested, c) {
			t.Errorf("UNATTESTED command suggested to the user: %q; message: %v", c, err)
		}
	}

	// Second net, independent of backticks: ANY occurrence of "sbx <word>" in
	// the message, backticked or not, must name an attested subcommand.
	// Without it, the test would only hold by the repo's typographic
	// convention — measured, two invented commands WITHOUT backticks left the
	// suite green.
	for _, sc := range sbxSubcommands(message) {
		if !slices.Contains([]string{"ls", "rm"}, sc) {
			t.Errorf("UNATTESTED sbx subcommand suggested to the user: %q; message: %v", sc, err)
		}
	}

	// And the way out must be there: a refusal without remediation forces the
	// user to guess.
	if !slices.Contains(commands, "sbx rm --force api") {
		t.Errorf("the message must give the exact remediation; got: %v", err)
	}

	// den's remediation comes first: `den rm api` does what `sbx rm --force
	// api` does AND cleans up the worktrees den created for this sandbox
	// (internal/cli/rm.go, cleanWorktrees). Sending the user straight to sbx
	// leaves orphaned worktrees under worktree_root, without telling them.
	if !slices.Contains(commands, "den rm api") {
		t.Errorf("the message must suggest `den rm api`, which also cleans up worktrees; got: %v", err)
	}
	if strings.Index(message, "den rm api") > strings.Index(message, "sbx rm --force api") {
		t.Errorf("`den rm api` must precede `sbx rm --force api`: it's the full remediation, "+
			"the other is the fallback; got: %v", err)
	}
}

// betweenBackticks returns the `...` segments of a message, i.e. everything den
// presents to the user as a command to type.
func betweenBackticks(s string) []string {
	var out []string
	for {
		i := strings.Index(s, "`")
		if i < 0 {
			return out
		}
		rest := s[i+1:]
		j := strings.Index(rest, "`")
		if j < 0 {
			return out
		}
		out = append(out, rest[:j])
		s = rest[j+1:]
	}
}

// sbxSubcommands returns the word following each "sbx " in the message,
// backticked or not.
//
// WHAT THIS NET DOESN'T CATCH, and it's worth saying rather than implying
// otherwise: an invented `den` command written outside backticks ("den up
// --force") slips through, because "den" legitimately appears in the
// message's prose ("den does not attach...") and no heuristic separates prose
// from a program name. It's the backtick whitelist that covers that case, and
// only it.
func sbxSubcommands(s string) []string {
	var out []string
	for {
		i := strings.Index(s, "sbx ")
		if i < 0 {
			return out
		}
		s = s[i+len("sbx "):]
		end := strings.IndexFunc(s, func(r rune) bool {
			return !unicode.IsLetter(r) && r != '-'
		})
		if end < 0 {
			end = len(s)
		}
		if end > 0 {
			out = append(out, s[:end])
		}
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}
