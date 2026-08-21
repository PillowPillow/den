package converge

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/source"
)

// ResourceDriver is the internal lifecycle every managed resource implements
// (spec §4.3). It is a Go interface, NOT an extension point: a manifest can
// only name types den compiles in, so this is where the closed vocabulary is
// implemented, never where it is opened.
//
// Inspect and Verify are the same question asked at two moments — before and
// after applying — and they are separate methods because the second is a
// PROOF: den never reports a resource as converged because it ran a command
// that exited 0.
type ResourceDriver interface {
	Inspect(ctx context.Context) (Observation, error)
	Plan(Observation) ResourcePlan
	Apply(ctx context.Context, answers Answers, out io.Writer) error
	Verify(ctx context.Context) (Observation, error)
}

// Observation is what den SAW. An error from Inspect is not an Observation:
// "den could not look" and "den looked and found nothing" are different facts
// with different remedies, and the prototype (2026-08-14) showed how easily
// they are confused — a restricted execution environment denied Keychain
// access and both inspection commands failed with exit 1, on a machine whose
// credentials were perfectly configured.
type Observation struct {
	Present bool
	// Detail is a rendered summary — "configured", "2 of 3 hosts allowed".
	// Never a value.
	Detail string
}

// SbxState is one read of the machine's sbx configuration, shared by every
// driver of a plan.
//
// Read once, not per resource: `sbx secret ls -g` forks a process and talks to
// the OS keychain, and a source declaring three credentials would otherwise
// pay for it three times per plan — and could observe an inconsistent machine
// if something changed between two reads.
type SbxState struct {
	// Services are the service credentials by name ("github"). Both `(stored)`
	// and `(oauth configured)` count as present (prototype §Secret inspection).
	Services map[string]bool
	// Registries are the registry credentials by host, exactly as sbx prints
	// them ("registry.example.test:443").
	Registries map[string]bool
	// Customs are the custom secrets, keyed by targets+environment: den
	// identifies one by scope, targets and variable, and never reads its
	// placeholder or masked value.
	Customs map[string]bool
	// AllowedHosts are the exact entries of `rules[].resources` for the local
	// allow rules on network resources.
	AllowedHosts map[string]bool
}

// customKey is the identity of a custom secret. Sole definition, so the
// parser and the driver cannot key the same thing differently.
func customKey(targets, environment string) string { return targets + "\x00" + environment }

// ReadSbxState observes the machine. Any failure is returned as an error and
// NEVER as an empty state: an empty state would read as "nothing is
// configured", which makes den offer to create credentials that already exist
// and, worse, report a working machine as broken.
func ReadSbxState(ctx context.Context, runner sbx.Runner) (*SbxState, error) {
	secrets, err := runner.Run(ctx, "secret", "ls", "-g")
	if err != nil {
		return nil, fmt.Errorf("reading the global sbx secrets: %w", err)
	}
	// StripUpdateBanner first: this is the one sbx read with no `--json`, so
	// the update box lands INSIDE the table den parses by column, where a
	// corner line carries one field and the row guard refuses the whole
	// inventory. See sbx.StripUpdateBanner.
	state, err := parseSecretList(sbx.StripUpdateBanner(string(secrets)))
	if err != nil {
		return nil, err
	}
	policies, err := sbx.LocalNetworkPolicy(ctx, runner)
	if err != nil {
		return nil, fmt.Errorf("reading the local sbx network policy: %w", err)
	}
	hosts, err := parseAllowedHosts(policies)
	if err != nil {
		return nil, err
	}
	state.AllowedHosts = hosts
	return state, nil
}

// parseSecretList reads the two tables `sbx secret ls -g` prints. sbx has no
// JSON output for this command (probed on v0.38.0, 2026-08-14), so den parses
// text — anchored on the HEADER's own column positions, and tolerant about
// widths: column widths and the number of rows may change without breaking
// den, but a header den does not recognize is an error rather than an empty
// result.
//
// den reads TYPE and NAME from the first table, and TARGETS and ENV from the
// optional `CUSTOM SECRETS` one — each of them from the offset the header's
// SECOND column starts at, never from a field index. SCOPE is the column den
// never reads and the only one that can hold whitespace: sbx renders a
// host-only secret's scope as `(host only)`, two tokens, and a field-index
// parser then reads that row's TYPE as `only)`, matches neither kind, and
// drops the row through the unknown-TYPE branch below — reporting a
// configured credential as missing for good, which is exactly the permanent
// block Apply's `--all-sandboxes` exists to prevent. Reading from the offset
// removes the dependency on how many tokens SCOPE spans: sbx aligns a table
// and its header in one pass (observed on v0.38.0, 2026-08-18), so a wider
// SCOPE moves both together.
//
// den never reads the masked value or the placeholder: neither is needed to
// decide whether a credential is present, and reading them would put
// fragments of secrets into den's memory and its errors.
func parseSecretList(text string) (*SbxState, error) {
	state := &SbxState{
		Services:   map[string]bool{},
		Registries: map[string]bool{},
		Customs:    map[string]bool{},
	}
	lines := strings.Split(text, "\n")
	section := ""
	// Where the second column starts, in bytes. Reset at every header — the
	// two tables are aligned independently — and -1 means "no header seen for
	// this section yet".
	second := -1
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if trimmed == "CUSTOM SECRETS" {
			section = "custom"
			second = -1
			continue
		}
		fields := strings.Fields(trimmed)
		if fields[0] == "SCOPE" {
			switch {
			case section == "custom" && len(fields) >= 3 && fields[1] == "TARGETS" && fields[2] == "ENV":
			case section == "" && len(fields) >= 3 && fields[1] == "TYPE" && fields[2] == "NAME":
			default:
				return nil, fmt.Errorf(
					"sbx secret ls: unrecognized table header %q — den parses this output by column, "+
						"and guessing on a layout it does not know could report a configured "+
						"credential as missing", trimmed)
			}
			// Neither "TYPE" nor "TARGETS" occurs inside "SCOPE", so the
			// first match is the second column's own start.
			second = strings.Index(line, fields[1])
			continue
		}
		if second < 0 {
			return nil, fmt.Errorf(
				"sbx secret ls: the row %q arrives before any table header — den reads this "+
					"output from the header's column positions, and cannot place a row whose "+
					"table it never saw", trimmed)
		}
		var cells []string
		if second < len(line) {
			cells = strings.Fields(line[second:])
		}
		if section == "custom" {
			// TARGETS ENV PLACEHOLDER SECRET — the two trailing columns are
			// deliberately not read.
			if len(cells) < 2 {
				return nil, fmt.Errorf("sbx secret ls: unreadable custom secret row %q", trimmed)
			}
			state.Customs[customKey(cells[0], cells[1])] = true
			continue
		}
		// TYPE NAME SECRET
		if len(cells) < 2 {
			return nil, fmt.Errorf("sbx secret ls: unreadable row %q", trimmed)
		}
		switch cells[0] {
		case "service":
			state.Services[cells[1]] = true
		case "registry":
			state.Registries[cells[1]] = true
		}
		// An unknown TYPE is ignored rather than refused: sbx may grow a kind
		// den does not manage, and a source that does not declare it is
		// unaffected.
	}
	return state, nil
}

// policyList is the shape `sbx policy ls --json` returns (prototype §Policy
// inspection). Only the fields den compares are decoded: the rest of a rule
// is sbx's business, and decoding it would make den fail on a field sbx adds.
type policyList struct {
	Rules []struct {
		Resources []string `json:"resources"`
	} `json:"rules"`
}

func parseAllowedHosts(raw []byte) (map[string]bool, error) {
	// sbx.DecodeJSON, not json.Unmarshal: sbx writes its update banner on
	// stdout behind the payload, and Unmarshal refuses anything after the
	// value — a converge run failed here for that alone. See sbx.DecodeJSON.
	var list policyList
	if err := sbx.DecodeJSON("policy ls --json", raw, &list); err != nil {
		return nil, fmt.Errorf(
			"%w — den compares the exact entries of rules[].resources, and cannot guess on a "+
				"shape it does not know", err)
	}
	out := map[string]bool{}
	for _, r := range list.Rules {
		for _, host := range r.Resources {
			out[host] = true
		}
	}
	return out, nil
}

// ParseSbxVersion reads the version out of `sbx version`, whose output is
// "sbx version: v0.38.0 <commit>" (observed on v0.38.0, 2026-08-14).
//
// It returns "" rather than an error when it cannot find one: an unreadable
// version becomes source.UnknownVersionError at the compatibility check, which
// is the layer that knows what a floor is — and a version den cannot read must
// never be turned into a number it then compares.
func ParseSbxVersion(output string) string {
	for _, line := range strings.Split(output, "\n") {
		_, rest, ok := strings.Cut(line, "sbx version:")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return ""
		}
		return fields[0]
	}
	return ""
}

// githubService is the sbx service name behind source.CredentialGitHub.
//
// Derived from the TYPE, never from the manifest's `id:`. The two happen to
// coincide in every manifest written so far, and reading the id was a latent
// trap: a source declaring `- { id: github-service, type: sbx_github }` would
// have den inspect service "github-service", configure service "github", then
// fail to verify what it had just applied — a resource permanently blocked by
// a name the author was free to choose. The type fully determines the service:
// `refuseUnusedFields` rejects `host:` and `environment:` on this type, so the
// manifest has no field that could name it differently.
const githubService = "github"

// credentialDriver converges one declared sbx credential.
type credentialDriver struct {
	res     source.CredentialResource
	state   *SbxState
	runner  sbx.Runner
	secrets sbx.SecretRunner
	source  string
}

func (d *credentialDriver) resume() string {
	return fmt.Sprintf("run `den source configure %s`", d.source)
}

func (d *credentialDriver) Inspect(context.Context) (Observation, error) {
	switch d.res.Type {
	case source.CredentialGitHub, source.CredentialRegistry, source.CredentialHTTPSubstitution:
		return Observation{Present: CredentialPresent(d.res, d.state), Detail: "configured in sbx"}, nil
	}
	// Unreachable through LoadManifest, which refuses an unsupported type.
	return Observation{}, fmt.Errorf("credential %q: unsupported type %q", d.res.ID, d.res.Type)
}

// CredentialPresent asks a single question — per the resource's own TYPE —
// against one observation of the machine. Exported so a caller that must
// judge "is this genuinely absent" BEFORE Service.Plan runs (den's
// non-interactive resume, internal/cli/answers.go) asks it the identical way
// Inspect does above, rather than re-encoding the per-type dispatch a second
// time. Two implementations of "is it there" is exactly the defect class
// this plan repairs — see 1c9aca8/d4ece41 on this branch for the network-rule
// version of the same lesson.
//
// state is a completed ReadSbxState observation, never nil: a caller that
// could not observe the machine has no question to ask here at all, and must
// treat every credential as absent itself — the nil guard other call sites
// already carry (see stillMissingCredentials) — rather than pass a nil state
// in and rely on this function to decide it for them.
func CredentialPresent(res source.CredentialResource, state *SbxState) bool {
	switch res.Type {
	case source.CredentialGitHub:
		return state.Services[githubService]
	case source.CredentialRegistry:
		return state.Registries[res.Host]
	case source.CredentialHTTPSubstitution:
		return state.Customs[customKey(res.Host, res.Environment)]
	}
	return false
}

func (d *credentialDriver) Plan(o Observation) ResourcePlan {
	p := ResourcePlan{
		ID:       d.res.ID,
		Kind:     KindCredential,
		Known:    true,
		Action:   ActionCreate,
		Expected: d.expected(),
		Resume:   d.resume(),
	}
	if o.Present {
		p.Action, p.Observed = ActionUnchanged, o.Detail
	}
	return p
}

// expected states what the manifest wants, in the user's terms and without a
// value. The host is part of it: "a registry credential" says nothing, "a
// credential for gitlab.example.test:4567" says what to check by hand.
func (d *credentialDriver) expected() string {
	switch d.res.Type {
	case source.CredentialGitHub:
		return "the github service credential, configured in sbx"
	case source.CredentialRegistry:
		return "a registry credential for " + d.res.Host
	default:
		return fmt.Sprintf("an http substitution for %s (%s)", d.res.Host, d.res.Environment)
	}
}

// Apply configures the credential. Which command, and how the value travels,
// is decided per type — and the value never travels in an argv den can avoid:
//
//   - github is interactive on sbx's side (measured 2026-08-16, `sbx secret
//     set --help` on v0.38.0): sbx reads it from its own prompt, and Run's
//     stdin is nil, so the prompt would read EOF and fail. Apply hands the
//     call to Attach instead — Runner's own doc reserves it for exactly this,
//     "an interactive shell … there's nothing to capture, and capturing
//     would break interactivity" — which wires the real terminal the prompt
//     needs. The value still never reaches den: it goes straight from the
//     user's keyboard to sbx.
//   - a registry password is piped on stdin (`--password-stdin`), because an
//     argv is readable by every process on the machine.
//   - a custom secret has no stdin form on v0.38.0 (probed 2026-08-14): the
//     value goes in argv, through RunSensitive, which redacts it from every
//     error den can produce.
//
// None of the three passes `-g` any more. sbx deprecated that flag on both
// `set` commands (measured 2026-08-18 — "Flag --global has been deprecated,
// global is now the default for service secrets; omit --global, use --sandbox
// to target one sandbox, or use --all-sandboxes with --registry"), and it
// prints the warning on stderr in the middle of the github prompt, where a
// human reads it as den failing. `secret ls -g` in ReadSbxState is a
// DIFFERENT flag on a different command — still live, still documented — and
// it stays.
//
// The registry call takes `--all-sandboxes`, not nothing. Dropping the flag
// there is the one change that looks equivalent and is not: a registry
// credential now defaults to HOST ONLY — used for the host's own template and
// kit pulls, never injected into a sandbox — and `secret ls -g` does not list
// it (both measured 2026-08-18). den would apply a credential its own Verify
// could never observe, and block the resource for good.
//
// That argv needs sbx >= 0.38.0: an older binary knows `-g` and not
// `--all-sandboxes`, and answers cobra's bare `unknown flag:
// --all-sandboxes`. den declares no sbx floor of its own, so the ONLY guard
// is the source manifest's `requires.sbx` — which is optional, and
// source.CheckCompatibility skips an undeclared one. A source that omits it
// therefore fails HERE rather than at the compatibility check. Left that way
// on purpose: a den-level floor would refuse machines that work today for
// every source declaring no registry credential.
func (d *credentialDriver) Apply(ctx context.Context, answers Answers, out io.Writer) error {
	switch d.res.Type {
	case source.CredentialGitHub:
		fmt.Fprintf(out, "configuring the sbx github credential (sbx will ask for it)\n")
		return d.runner.Attach(ctx, "secret", "set", githubService)

	case source.CredentialRegistry:
		value, err := d.value(answers)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "configuring the sbx registry credential for %s\n", d.res.Host)
		_, err = d.secrets.RunInput(ctx, []byte(value),
			"secret", "set", "--all-sandboxes", "--registry", d.res.Host, "--password-stdin")
		return err

	case source.CredentialHTTPSubstitution:
		value, err := d.value(answers)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "configuring the sbx http substitution for %s (%s)\n", d.res.Host, d.res.Environment)
		args := []string{"secret", "set-custom", "--host", d.res.Host,
			"--env", d.res.Environment, "--value", value}
		// The value is the LAST argument; its index is computed rather than
		// written, so reordering the argv cannot silently unredact it.
		_, err = d.secrets.RunSensitive(ctx, []int{len(args) - 1}, args...)
		return err
	}
	return fmt.Errorf("credential %q: unsupported type %q", d.res.ID, d.res.Type)
}

// value reads the transient answer this credential needs. A missing one is a
// ResourceError rather than a bare failure: it names what den wanted, and the
// answer-file key that supplies it.
func (d *credentialDriver) value(answers Answers) (string, error) {
	name := d.res.ValueFrom.Credential
	value := answers.Credentials[name].Value
	if value == "" {
		return "", &ResourceError{
			Resource:  d.res.ID,
			Observed:  "no value for input " + name,
			Expected:  d.expected(),
			Remaining: "supply it interactively, or through `credentials." + name + ".from_env` in the answer file",
			Resume:    d.resume(),
		}
	}
	return value, nil
}

// Verify re-reads the machine. A fresh read, never the cached state: proving
// convergence from the observation taken BEFORE the mutation would prove
// nothing at all.
func (d *credentialDriver) Verify(ctx context.Context) (Observation, error) {
	state, err := ReadSbxState(ctx, d.runner)
	if err != nil {
		return Observation{}, err
	}
	d.state = state
	return d.Inspect(ctx)
}

// networkDriver converges the machine-level egress a source's builds need.
//
// ONE driver for the whole `build_network.allow` list rather than one per
// host: the plan reads better ("2 of 3 hosts allowed") and the receipt records
// one status for the group, which is the granularity spec §10.2 defines.
type networkDriver struct {
	allow  []string
	state  *SbxState
	runner sbx.Runner
	source string
}

// missing lists the declared hosts this machine does not already allow.
//
// It accepts BOTH spellings of a rule, and that is not laxity: sbx stores a
// portless host with :443 (sbx.NormalizeNetworkResource holds the measurement),
// so an exact comparison against the declared string reported every bare host
// as missing — den re-applied it on every run and then failed to VERIFY the
// resource it had just applied, blocking the source permanently. Comparing
// both forms can only turn a false "absent" into a true "present": a source
// that means another port declares it, and that spelling is compared exactly.
func (d *networkDriver) missing() []string {
	var out []string
	for _, host := range d.allow {
		if !d.state.AllowedHosts[host] && !d.state.AllowedHosts[sbx.NormalizeNetworkResource(host)] {
			out = append(out, host)
		}
	}
	slices.Sort(out)
	return out
}

func (d *networkDriver) Inspect(context.Context) (Observation, error) {
	missing := d.missing()
	return Observation{
		Present: len(missing) == 0,
		Detail:  fmt.Sprintf("%d of %d hosts allowed", len(d.allow)-len(missing), len(d.allow)),
	}, nil
}

func (d *networkDriver) Plan(o Observation) ResourcePlan {
	p := ResourcePlan{
		ID:       KindBuildNetwork,
		Kind:     KindBuildNetwork,
		Known:    true,
		Observed: o.Detail,
		Expected: strings.Join(d.allow, ", "),
		Resume:   fmt.Sprintf("run `den source configure %s`", d.source),
	}
	switch {
	case o.Present:
		p.Action = ActionUnchanged
	case len(d.allow) == len(d.missing()):
		p.Action = ActionCreate
	default:
		// Some hosts are already allowed: den adds the rest and says so, rather
		// than presenting a partially-configured machine as untouched.
		p.Action = ActionUpdate
	}
	return p
}

func (d *networkDriver) Apply(ctx context.Context, _ Answers, out io.Writer) error {
	for _, host := range d.missing() {
		fmt.Fprintf(out, "allowing %s on this machine's local network policy\n", host)
		if _, err := d.runner.Run(ctx, "policy", "allow", "network", host); err != nil {
			return err
		}
	}
	return nil
}

func (d *networkDriver) Verify(ctx context.Context) (Observation, error) {
	state, err := ReadSbxState(ctx, d.runner)
	if err != nil {
		return Observation{}, err
	}
	d.state = state
	return d.Inspect(ctx)
}
