package sbx

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Publication is one host↔sandbox port mapping the VM currently publishes, as
// the `ports` array of `sbx ls --json` carries it (schema recorded 2026-07-31,
// sbx v0.35.0):
//
//	{"host_ip":"127.0.0.1","host_port":9500,"sandbox_port":8080,"protocol":"tcp"}
//
// It is the ONLY surface that tells den what a sandbox already publishes
// without a second call to sbx — `den ports` reads `sbx ls --json` anyway, to
// find the sandbox. `sbx ports SANDBOX [--json]` says the same thing, at the
// cost of one more process.
//
// MEASURED, AND THE MEASUREMENT MATTERS: the field is present only while the
// sandbox is RUNNING. A stopped sandbox carries no `ports` key at all (and the
// bare `sbx ports SANDBOX` answers "No published ports"), yet every publication
// comes back on resume. Reading it on a stopped sandbox therefore reports
// "publishes nothing" about a VM that publishes plenty, which is why callers
// must gate the reading on the status (internal/cli/ports.go) rather than take
// an absent array for an answer.
type Publication struct {
	HostIP      string `json:"host_ip"`
	HostPort    int    `json:"host_port"`
	SandboxPort int    `json:"sandbox_port"`
	Protocol    string `json:"protocol"`
}

// Sandbox is a sandbox as `sbx ls --json` describes it.
//
// The schema is that of sbx v0.35.0, recorded 2026-07-28 and extended
// 2026-07-31 with `ports`:
//
//	{"sandboxes":[{"name","id","agent","status","ports":[…],"workspaces":["/p","/p:ro"]}]}
//
// There is NO date field: a sandbox's age isn't computable, and the "age"
// column of spec §5 was dropped as a result.
//
// Decoding is deliberately tolerant of unknown fields (unlike the strict
// configuration YAML): this output comes from a third-party tool, not from
// the user. A field added by a later sbx version must not break `den ls`.
type Sandbox struct {
	Name   string `json:"name"`
	Agent  string `json:"agent"`
	Status string `json:"status"`
	// Ports is empty when the sandbox publishes nothing — AND when it is
	// stopped, which is a different thing entirely. See Publication.
	Ports      []Publication `json:"ports"`
	Workspaces []string      `json:"workspaces"`
}

// Nest is the sandbox's originating nest, derived from the name — sbx having
// no labels, the name is the only state carrier.
func (s Sandbox) Nest() string {
	nest, _ := SplitName(s.Name)
	return nest
}

// Worktree is the originating worktree, empty if the sandbox carries none.
func (s Sandbox) Worktree() string {
	_, wt := SplitName(s.Name)
	return wt
}

// Workdir is the natural working directory: the FIRST workspace, which is by
// construction the first repo (or its worktree) — den mounts those before the
// agent profile. The ":ro" suffix is stripped: it's a mount option, not part
// of the path.
func (s Sandbox) Workdir() string {
	if len(s.Workspaces) == 0 {
		return ""
	}
	return strings.TrimSuffix(s.Workspaces[0], ":ro")
}

// StatusRunning is the `status` value of a sandbox that is running.
// Recorded 2026-07-28 against the sbx v0.35.0 schema.
const StatusRunning = "running"

// StatusStopped is the `status` value of a stopped sandbox — the state sbx
// parks inactive sandboxes into BY ITSELF, after a few minutes (observed in
// the 2026-07-29 smoke test).
//
// It accepts an `sbx exec`, which restarts it in the process. It is therefore
// a resting state, not a failure.
const StatusStopped = "stopped"

// Find returns the sandbox with this name among boxes, or nil.
//
// A POINTER, not a boolean: it's the found sandbox that carries the real
// status and the workspaces the VM actually mounts. An earlier version
// reduced this lookup to a boolean `Exists` and threw both away — hence
// attaching into a stopped VM and `-w` recomputed from a configuration the VM
// doesn't mount.
//
// The name is compared WHOLE: "api" and "api.feat12" are two sandboxes,
// never one the prefix of the other.
func Find(boxes []Sandbox, name string) *Sandbox {
	for i := range boxes {
		if boxes[i].Name == name {
			return &boxes[i]
		}
	}
	return nil
}

// IsStopped reports whether the sandbox is at rest — attachable, but at the
// cost of a restart the caller should announce: it takes several seconds, and
// a silent `den spawn` during that time looks like a hang.
//
// Kept SEPARATE from CheckAttachable ON PURPOSE: "should we warn?" and
// "should we refuse?" are two different questions, and conflating them is
// exactly what produced the refusal the 2026-07-29 smoke test invalidated.
func (s Sandbox) IsStopped() bool { return s.Status == StatusStopped }

// CheckAttachable refuses a sandbox den must not open a shell into. It's the
// guard shared by `den spawn` and `den sh`: both end in an `sbx exec`.
//
// TWO statuses pass, and the second is the 2026-07-29 smoke-test fix:
//
//   - "running": nothing to say;
//   - "stopped": `sbx exec` restarts the sandbox transparently ("Sandbox
//     <name> started successfully"). Refusing was stricter than sbx itself,
//     and the remedy shown — `den rm` — DESTROYED state that would have
//     survived: a file written into the container layer is still there after
//     stop/exec. With automatic shutdown of inactive sandboxes, this case is
//     the NORM on returning to a `--detach` VM, not the exception.
//
// A WHITELIST regardless: anything that is neither is refused. A blacklist
// would let through any status a later sbx version introduced — including an
// error status — and den has no way to know that list here. The accepted
// price: a transitory startup status could make a `den spawn` launched too
// early fail. The message renders the status read, which makes the case
// diagnosable and reportable.
//
// The remediation names only ATTESTED commands: `sbx` subcommands come from
// the 2026-07-28 survey (spec §14.0); `den rm` is a den command, attested by
// the repo's own source (internal/cli/rm.go). `den rm` comes FIRST because it
// does strictly more: it destroys the VM like `sbx rm --force`, but also
// cleans up the worktrees den created for this sandbox. Sending the user
// straight to sbx leaves orphaned worktrees under worktree_root, without
// telling them.
func (s Sandbox) CheckAttachable() error {
	if s.Status == StatusRunning || s.Status == StatusStopped {
		return nil
	}
	return fmt.Errorf(
		"sandbox %q: status read %q, expected %q or %q — den does not attach into a VM it "+
			"knows nothing about; inspect it with `sbx ls`, or destroy it and relaunch: `den rm %s` "+
			"(which also cleans up worktrees; fallback `sbx rm --force %s`, which only destroys the VM)",
		s.Name, s.Status, StatusRunning, StatusStopped, s.Name, s.Name)
}

// Ls lists live sandboxes, sorted by name.
func Ls(ctx context.Context, r Runner) ([]Sandbox, error) {
	output, err := r.Run(ctx, "ls", "--json")
	if err != nil {
		// AS IS, no wrapper: ExecError already renders the full binary and argv
		// ("sbx ls --json: ..."), and an "sbx ls: " prepended in front wrote the
		// subcommand twice on the first line seen by a user without sbx
		// installed. What the wrapper added was strictly less precise than what
		// it repeated. Held by TestLsDoesNotRepeatTheSubcommand.
		//
		// The DECODING failures below keep their prefix: nothing else locates
		// them.
		return nil, err
	}

	// Two-step decoding, on purpose: a valid JSON object WITHOUT the
	// "sandboxes" key (sbx renaming it) must be an ERROR, not a silent zero. Ls
	// has no output channel other than the error to signal it, and silence
	// costs more elsewhere than on den ls itself: den sh/den rm would read an
	// empty list and tell the user a live sandbox doesn't exist, instead of
	// saying the read failed. Same policy as the `allowed` field of `sbx
	// policy check` (task 11), for the same reason.
	//
	// UNVERIFIED ASSUMPTION (sbx isn't installed on this machine): the "zero
	// live sandboxes" case emits the "sandboxes" key with an empty or null
	// array, never its outright absence (a bare `{}`). If a first smoke test
	// on a real machine shows a bare `{}` when nothing is running, this guard
	// must be relaxed.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(output, &fields); err != nil {
		// The raw output is in the message: without it, a schema change on
		// sbx's side would be undiagnosable.
		return nil, fmt.Errorf("sbx ls: unreadable JSON output (%w): %s", err, string(output))
	}
	raw, present := fields["sandboxes"]
	if !present {
		return nil, fmt.Errorf("sbx ls: key %q absent from the JSON output: %s", "sandboxes", string(output))
	}

	var boxes []Sandbox
	if err := json.Unmarshal(raw, &boxes); err != nil {
		return nil, fmt.Errorf("sbx ls: unreadable JSON output (%w): %s", err, string(output))
	}

	slices.SortFunc(boxes, func(a, b Sandbox) int { return strings.Compare(a.Name, b.Name) })
	return boxes, nil
}
