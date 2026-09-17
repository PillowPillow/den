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
// Read once, not per resource: `sbx secret ls -g --json` forks a process and talks to
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
	secrets, err := runner.Run(ctx, "secret", "ls", "-g", "--json")
	if err != nil {
		return nil, fmt.Errorf("reading the global sbx secrets: %w", err)
	}
	state, err := decodeSecretList(secrets)
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

// secretList is the shape of `sbx secret ls -g --json` (measured 2026-09-16 on
// v0.43.0, spec 2026-09-16-sbx-0.43-compat §1.4; `--json` on this command
// exists since v0.42.0, which is why den's floor is where it is).
//
// ONLY the identity fields. The document also carries `secret` and
// `placeholder`, both masked forms of real token material (a prefix and the
// last four characters), and a struct WITHOUT those fields is how den keeps
// them out of what it DECODES and holds in memory — the doctrine the text
// parser this replaced applied by never reading past the columns it needed.
//
// That protection does not reach a decode FAILURE: sbx.DecodeJSON embeds the
// whole raw payload in its error, so a malformed or shape-changed listing
// still surfaces every row's masked `secret`/`placeholder` in den's own error
// output — widened from the one row the old text parser's error quoted to
// every row here (verified 2026-09-16). Masked forms only, and only off a
// healthy machine's path; still wider than before, and out of scope for this
// struct to fix — sbx.DecodeJSON is shared by every `--json` read in den.
//
// Secrets is a POINTER so that a document simply lacking the key can be told
// from an empty list: the first is a shape den does not know, the second a
// machine with nothing configured, and only the second may answer "absent".
type secretList struct {
	Secrets *[]struct {
		Type string `json:"type"`
		Name string `json:"name"`
	} `json:"secrets"`
	CustomSecrets []struct {
		Targets []string `json:"targets"`
		Env     string   `json:"env"`
	} `json:"custom_secrets"`
}

// decodeSecretList reads the secret inventory. A document without `secrets`
// is an error and never an empty state: an empty state reads as "nothing is
// configured", which makes den offer to create credentials that already exist.
//
// sbx.DecodeJSON, not json.Unmarshal: sbx writes its update banner on stdout
// behind the payload, and Unmarshal refuses anything after the value.
func decodeSecretList(raw []byte) (*SbxState, error) {
	var list secretList
	if err := sbx.DecodeJSON("secret ls -g --json", raw, &list); err != nil {
		return nil, fmt.Errorf(
			"%w — den reads secrets[].type/name and custom_secrets[].targets/env, and cannot "+
				"guess on a shape it does not know", err)
	}
	if list.Secrets == nil {
		return nil, fmt.Errorf(
			"sbx secret ls -g --json: key %q absent from the JSON output — den cannot tell an "+
				"unconfigured machine from a listing it does not understand", "secrets")
	}
	state := &SbxState{
		Services:   map[string]bool{},
		Registries: map[string]bool{},
		Customs:    map[string]bool{},
	}
	for _, s := range *list.Secrets {
		switch s.Type {
		case "service":
			state.Services[s.Name] = true
		case "registry":
			state.Registries[s.Name] = true
		}
		// An unknown TYPE is ignored rather than refused: sbx may grow a kind
		// den does not manage, and a source that does not declare it is
		// unaffected.
	}
	for _, c := range list.CustomSecrets {
		// One key PER TARGET: the source manifest declares one host per
		// resource (CredentialPresent looks up res.Host), and an entry sbx
		// stores for several targets must answer for each of them.
		for _, target := range c.Targets {
			state.Customs[customKey(target, c.Env)] = true
		}
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
// human reads it as den failing. `secret ls -g --json` in ReadSbxState is a
// DIFFERENT flag on a different command — still live, still documented — and
// it stays.
//
// The registry call takes `--all-sandboxes`, not nothing. Dropping the flag
// there is the one change that looks equivalent and is not: a registry
// credential now defaults to HOST ONLY — used for the host's own template and
// kit pulls, never injected into a sandbox. That is the INJECTION axis, and
// it is orthogonal to SCOPE, which is what `secret ls -g --json` filters on
// ("Only list global secrets") — a registry credential set without
// `--all-sandboxes` is still global on scope, so `secret ls -g --json` DOES
// list it (corrected 2026-09-16 against real sbx v0.43.0 output; the earlier
// claim here that it did not was wrong). Not a regression: the deleted text
// parser this replaced never read the SCOPE column either — its own deleted
// test asserted a `(host only)` row "must be read, not dropped". And not
// fixable by decoding more fields: the payload carries no injection field at
// all (secretList above decodes only `type`/`name`). den therefore cannot
// tell a host-only registry credential from an injected one, and may see one
// as present while the other is missing — a pre-existing doctrine question
// this branch did not introduce, left open on purpose.
//
// That argv needs sbx >= 0.38.0: an older binary knows `-g` and not
// `--all-sandboxes`, and answers cobra's bare `unknown flag:
// --all-sandboxes`. den now declares a floor of its own (sbx.MinVersion,
// currently v0.43.0, well above this argv's 0.38.0 need) — but `den doctor`
// is its ONLY judge, and doctor is advisory: no other command, Apply
// included, consults it. The guard THIS path actually runs under is still
// the source manifest's `requires.sbx` — which is optional, and
// source.CheckCompatibility skips an undeclared one. A source that omits it
// therefore still fails HERE rather than at the compatibility check, exactly
// as before sbx.MinVersion existed (2026-09-16).
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
