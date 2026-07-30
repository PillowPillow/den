package sbx

import (
	"fmt"
	"path/filepath"
	"strings"
)

// PositionalAgent is the agent passed to `sbx create`.
//
// "shell" and not "claude": `sbx create [flags] AGENT PATH [PATH...]` requires
// a positional agent (omitting it gives "unknown agent"), but the command
// actually attached is decided by the image's FLAVOR, not by this argument —
// an image snapshotted from the claude base launches `claude` regardless of
// what's written here. den attaches via `sbx exec ... bash -l` anyway, so
// "shell" is the honest choice: it promises nothing it doesn't keep.
const PositionalAgent = "shell"

// Create describes an `sbx create` to assemble.
//
// MixinKit is a field SEPARATE from StackKits, not the last element of a
// single list: the mixin is fail-closed and the sbx dispatcher does `exit $rc`
// on the first failure, which deprives later kits of their startup commands.
// It MUST be layered last. Two fields make the inversion impossible; a single
// list would only make it unlikely.
type Create struct {
	Name      string   // sandbox name, see SandboxName
	Image     string   // passed verbatim to --template
	StackKits []string // cross-cutting kits then the stack kit, layering order
	MixinKit  string   // generated mixin directory — ALWAYS the last --kit
	// Workspaces: host paths mounted, in order. The FIRST one must be the repo
	// (or its worktree): Sandbox.Workdir depends on it for the attach.
	// The ":ro" suffix is accepted by sbx.
	Workspaces []string
}

// CreateArgv assembles the full argv of `sbx create`, without the binary name.
func CreateArgv(c Create) ([]string, error) {
	// Single source of truth, shared with internal/agent: validating
	// component-by-component here let "api." through, which sbx would really
	// create and `sbx ls` would split back into "api".
	if err := ValidateSandboxName(c.Name); err != nil {
		return nil, err
	}
	if strings.TrimSpace(c.Image) == "" {
		return nil, fmt.Errorf(
			"sandbox %q: no image — the stack must declare `image:` in stack.yaml", c.Name)
	}
	if strings.TrimSpace(c.MixinKit) == "" {
		return nil, fmt.Errorf(
			"sandbox %q: missing generated mixin — it carries the egress, env and freshness "+
				"command, a create without it would produce a mute VM", c.Name)
	}
	if len(c.Workspaces) == 0 {
		return nil, fmt.Errorf(
			"sandbox %q: no workspace to mount — `sbx create` requires at least one path", c.Name)
	}
	for i, w := range c.Workspaces {
		if err := checkWorkspace(c.Name, i, w); err != nil {
			return nil, err
		}
	}

	argv := []string{"create", "--name", c.Name, "--template", c.Image}
	// BOUNDARY guard, not a duplicate of config.Stack.DeclaredKits — which
	// already filters empty entries at production's one caller.
	//
	// CreateArgv is exported and takes a struct anyone can fill: it guards its
	// own input, like checkWorkspace right below and for the same reason (the
	// doctrine kept for WriteMixin and for ValidateSandboxName). A `--kit ""`
	// would reach sbx, which has no reason to reject it cleanly.
	//
	// Removing it would make CreateArgv correct ONLY as long as every caller
	// filters — a property no test in this package can verify, since it's
	// about code in another package.
	for _, k := range c.StackKits {
		if k == "" {
			continue
		}
		argv = append(argv, "--kit", k)
	}
	argv = append(argv, "--kit", c.MixinKit) // always last, see the type's doc
	argv = append(argv, PositionalAgent)
	argv = append(argv, c.Workspaces...)
	return argv, nil
}

// checkWorkspace guards one entry of Workspaces, identified by its position
// (index 0) in the list.
//
// The guard lives here and not with the caller because it's CreateArgv that
// turns these values into a command line: a relative path would resolve
// against a working directory nothing guarantees at the moment sbx uses it,
// and a path starting with "-" would be read as a flag — the same class of
// failure the sandbox name charset closes off one step earlier in the argv.
func checkWorkspace(sandboxName string, i int, w string) error {
	if strings.TrimSpace(w) == "" {
		return fmt.Errorf(
			"sandbox %q: workspace #%d empty — `sbx create` would receive an empty positional "+
				"argument, which mounts nothing", sandboxName, i+1)
	}
	// ":ro" is an sbx mount option, not part of the path: strip it to judge,
	// and it travels back verbatim in the argv.
	path := strings.TrimSuffix(w, ":ro")
	if !filepath.IsAbs(path) {
		return fmt.Errorf(
			"sandbox %q: workspace #%d (%q) is not an absolute path — a relative path would "+
				"resolve against a working directory no longer guaranteed at the moment sbx uses "+
				"it, and a path starting with \"-\" would be read as a flag",
			sandboxName, i+1, w)
	}
	return nil
}
