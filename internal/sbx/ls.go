package sbx

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// Publication is one host↔sandbox port mapping the VM currently publishes, as
// the `ports` array of `sbx ls --json` carries it (schema recorded 2026-07-31,
// sbx v0.35.0; re-measured 2026-09-16 on v0.43.0):
//
//	{"host_ip":"127.0.0.1","host_port":9500,"sandbox_port":8080,"protocol":"tcp4"}
//
// `protocol` is the string sbx STORED, never normalized: a publish without a
// protocol stores "tcp4" since v0.42.0 (and "tcp" before), an explicit `/tcp`
// stores "tcp", and the two coexist in one listing. internal/cli/ports.go
// accepts both as den's own.
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
//	[{"name","id","agent","status","last_used_at","ports":[…],"workspaces":["/p","/p:ro"]}]
//
// `last_used_at` arrived with v0.43.0 (measured 2026-09-16); before it there
// was NO date field, which is what got the "age" column of spec §5 dropped.
// The column is back as LAST USED — it is the last use, not the creation.
//
// Decoding is deliberately tolerant of unknown fields (unlike the strict
// configuration YAML): this output comes from a third-party tool, not from
// the user. A field added by a later sbx version must not break `den ls`.
type Sandbox struct {
	Name   string `json:"name"`
	Agent  string `json:"agent"`
	Status string `json:"status"`
	// LastUsedAt is the RFC 3339 instant of the last use, as sbx v0.43.0
	// reports it (`last_used_at`, absent before that release). Kept RAW: the
	// rendering (Relative) needs a clock, and this struct is a reading of
	// sbx's output, not a view of it.
	LastUsedAt string `json:"last_used_at"`
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

// Instance is the second name component: the label of `--as`, or the
// flattened branch of `-w`, or empty. It is NOT "the worktree" — that
// reading was true only while -w was the sole thing that could fill this
// component. The branch, when there is one, lives in the manifest.
func (s Sandbox) Instance() string {
	_, instance := SplitName(s.Name)
	return instance
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
// guard shared by `den spawn` and `den exec`: both end in an `sbx exec`.
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

	// decodeList, not json.Unmarshal: it keeps DecodeJSON's tolerance of the
	// update banner sbx prints on stdout behind the payload (the 2026-08-21
	// outage, told in DecodeJSON), and it turns a shape den does not read into
	// an error rather than an empty list — den exec/den rm would otherwise
	// tell the user a live sandbox does not exist instead of saying the read
	// failed.
	var boxes []Sandbox
	if err := decodeList("ls", output, &boxes); err != nil {
		return nil, err
	}

	slices.SortFunc(boxes, func(a, b Sandbox) int { return strings.Compare(a.Name, b.Name) })
	return boxes, nil
}
