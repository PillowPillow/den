package sbx

import (
	"context"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"
)

// CustomSecret is the identity of one `sbx secret set-custom` entry: den tells
// two apart by their targets and their environment variable, and never by the
// value or the placeholder.
type CustomSecret struct{ Host, Env string }

// Machine is a MUTABLE double of a real sbx installation: it answers the
// inspection commands from an internal state that its own mutating commands
// change.
//
// It lives beside Fake, in the production package, for the reason Fake's own
// comment gives — internal/converge and internal/cli both need it, and a double
// per package would drift from the real contract right away. The two are not
// redundant: Fake replays SCRIPTED answers, which is what a test asserting on
// an argv wants; Machine SIMULATES state, which is the only way to exercise
// den's apply → verify loop at all. den configures a credential and then
// re-reads the machine to PROVE it landed, so a static double makes every
// verification fail.
//
// What it simulates is deliberately narrow: the credential inventory, the local
// network policy, and the image list. Everything else is recorded and answers
// nothing — a double that pretended to be sbx would start being wrong in ways
// no test could see.
type Machine struct {
	// mu guards every field: `policy.Settle` probes the allowlist concurrently,
	// and Fake carries a mutex for that exact reason (its own comment). A double
	// that only ever ran sequentially in today's tests would still be a race
	// waiting for the first caller that does not.
	mu sync.Mutex

	// Calls records every invocation, in order, with sensitive arguments
	// ALREADY redacted — a test that dumps them on failure must not print a
	// value the production redaction exists to hide.
	Calls [][]string

	// Attaches records ONLY the calls to Attach, in order — same reason as
	// Fake.Attaches (fake.go, and Runner's own doc): Calls conflates every
	// method, and a test proving the github credential went through a real
	// terminal, not a Run whose stdin is nil, needs a log only Attach feeds.
	// Attach feeds both Calls and Attaches; Run feeds only Calls.
	Attaches [][]string

	Services   map[string]bool
	Registries map[string]bool
	Customs    map[CustomSecret]bool
	Allowed    map[string]bool
	Images     map[string]bool

	// Fail maps a joined argv to the error that command must return. The argv
	// is the RECORDED one, so a sensitive command is keyed on its redacted
	// form — the only spelling a test can write without holding the secret.
	Fail map[string]error
}

// NewMachine returns an empty machine: nothing configured, nothing allowed, no
// image. That is the state a fresh laptop is in, which is what onboarding
// exists to change.
func NewMachine() *Machine {
	return &Machine{
		Services: map[string]bool{}, Registries: map[string]bool{},
		Customs: map[CustomSecret]bool{}, Allowed: map[string]bool{},
		Images: map[string]bool{}, Fail: map[string]error{},
	}
}

// HasCalled reports whether a call started with this argument prefix — same
// contract as Fake.HasCalled, so a test moving between the two doubles keeps
// its assertions.
func (m *Machine) HasCalled(prefix ...string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.Calls {
		if len(a) >= len(prefix) && slices.Equal(a[:len(prefix)], prefix) {
			return true
		}
	}
	return false
}

// HasAttached reports whether an ATTACH (never a Run) started with this
// prefix — same contract as Fake.HasAttached: it is what lets a test decide
// that the github credential went through the terminal Attach hands over,
// not a buffered Run whose prompt sbx would read as EOF.
func (m *Machine) HasAttached(prefix ...string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.Attaches {
		if len(a) >= len(prefix) && slices.Equal(a[:len(prefix)], prefix) {
			return true
		}
	}
	return false
}

func (m *Machine) record(args []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, slices.Clone(args))
}

func (m *Machine) failure(args []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Fail[strings.Join(args, " ")]
}

func (m *Machine) Run(_ context.Context, args ...string) ([]byte, error) {
	m.record(args)
	if err := m.failure(args); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	joined := strings.Join(args, " ")
	switch {
	case joined == "version":
		return []byte("sbx version: v0.38.0 abc\n"), nil
	case joined == "secret ls -g":
		return []byte(m.renderSecrets()), nil
	case strings.HasPrefix(joined, "policy ls"):
		return []byte(m.renderPolicies()), nil
	case joined == "template ls --json":
		return []byte(m.renderImages()), nil
	case joined == "ls --json":
		return []byte(`{"sandboxes":[]}`), nil
	case joined == "secret set -g github":
		// github is interactive on sbx's side (measured 2026-08-16, `sbx secret
		// set --help` on v0.38.0): sbx reads the value from a prompt. Exec.Run
		// leaves cmd.Stdin nil, so the real command's prompt would read EOF and
		// fail — a Run reaching this case must fail the same way, or this
		// double keeps lying about the one command that made den permanently
		// unable to configure the credential (internal/converge/sbx.go). Only
		// Attach, below, hands over a real terminal and may complete it.
		return nil, fmt.Errorf("sbx: reading github credential: EOF (stdin is not a terminal)")
	case len(args) == 4 && args[0] == "policy" && args[1] == "allow" && args[2] == "network":
		// sbx NORMALIZES a portless host to :443 when it stores the rule.
		// Measured on this machine, 2026-08-16: `sbx policy allow network
		// cdn.playwright.dev` (the spelling digitaleo-den-env's README tells a
		// human to type) comes back out of `sbx policy ls --json` as
		// "cdn.playwright.dev:443". A double that stored the argv verbatim
		// hid a real defect — den compared the declared bare host against the
		// stored one, never matched, and a resource it had just applied failed
		// to verify.
		m.Allowed[NormalizeNetworkResource(args[3])] = true
		return nil, nil
	case len(args) == 4 && args[0] == "template" && args[1] == "save":
		m.Images[args[3]] = true
		return nil, nil
	}
	return nil, nil
}

// RunInput is the registry credential's path: the password arrives on stdin
// and is DROPPED here, exactly as Fake.RunInput drops it — a double holding a
// live credential would put it in a test's memory and, on failure, in its
// output.
func (m *Machine) RunInput(_ context.Context, _ []byte, args ...string) ([]byte, error) {
	m.record(args)
	if err := m.failure(args); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// secret set -g --registry <host> --password-stdin
	if len(args) >= 5 && args[3] == "--registry" {
		m.Registries[args[4]] = true
	}
	return nil, nil
}

// RunSensitive records the REDACTED argv and reads only the identity of the
// custom secret — its host and its variable. The value travels in argv on
// sbx v0.38.0 and is the one thing this double never keeps.
func (m *Machine) RunSensitive(_ context.Context, redacted []int, args ...string) ([]byte, error) {
	safe := RedactArgs(args, redacted)
	m.record(safe)
	if err := m.failure(safe); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// secret set-custom -g --host <host> --env <env> --value <secret>
	var entry CustomSecret
	for i, a := range args {
		switch a {
		case "--host", "--env":
			// The real call site (internal/converge/sbx.go) always pairs the
			// flag with a value, so this never fires from there — but Machine
			// is a production file other packages build on, and a truncated
			// argv used to read args[i+1] straight past the slice and panic
			// the double instead of failing whatever assertion wired it wrong.
			if i+1 >= len(args) {
				return nil, fmt.Errorf(
					"RunSensitive: %q has no value after it in %q — a truncated argv, not something "+
						"sbx would ever be asked to run", a, safe)
			}
			if a == "--host" {
				entry.Host = args[i+1]
			} else {
				entry.Env = args[i+1]
			}
		}
	}
	m.Customs[entry] = true
	return nil, nil
}

func (m *Machine) Stream(_ context.Context, _ io.Writer, args ...string) error {
	m.record(args)
	return m.failure(args)
}

func (m *Machine) Attach(_ context.Context, args ...string) error {
	m.record(args)
	m.mu.Lock()
	m.Attaches = append(m.Attaches, slices.Clone(args))
	m.mu.Unlock()
	if err := m.failure(args); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// secret set -g github is the interactive credential
	// (internal/converge/sbx.go's credentialDriver.Apply) hands to Attach and
	// nowhere else: Attach is the one method that wires a real terminal
	// (runner.go's own doc), so it is the only method allowed to complete this
	// the way a human at the keyboard would. Run's matching case, above,
	// fails on purpose — that asymmetry is what a test can tell apart.
	if joined := strings.Join(args, " "); joined == "secret set -g github" {
		m.Services["github"] = true
	}
	return nil
}

func (m *Machine) Pipe(_ context.Context, args ...string) error {
	m.record(args)
	return m.failure(args)
}

// renderSecrets prints the two tables `sbx secret ls -g` prints (probed on
// v0.38.0). Sorted, so a golden or an order assertion holds between runs.
//
// Callers hold m.mu.
func (m *Machine) renderSecrets() string {
	var b strings.Builder
	b.WriteString("SCOPE      TYPE       NAME       SECRET\n")
	for _, name := range slices.Sorted(maps.Keys(m.Services)) {
		fmt.Fprintf(&b, "(global)   service    %s   (stored)\n", name)
	}
	for _, host := range slices.Sorted(maps.Keys(m.Registries)) {
		fmt.Fprintf(&b, "(global)   registry   %s   token-***\n", host)
	}
	if len(m.Customs) > 0 {
		b.WriteString("\nCUSTOM SECRETS\nSCOPE      TARGETS   ENV   PLACEHOLDER   SECRET\n")
		entries := slices.SortedFunc(maps.Keys(m.Customs), func(a, b CustomSecret) int {
			if c := strings.Compare(a.Host, b.Host); c != 0 {
				return c
			}
			return strings.Compare(a.Env, b.Env)
		})
		for _, e := range entries {
			fmt.Fprintf(&b, "(global)   %s   %s   sbx-cs-x   token-***\n", e.Host, e.Env)
		}
	}
	return b.String()
}

// Callers hold m.mu.
func (m *Machine) renderPolicies() string {
	hosts := slices.Sorted(maps.Keys(m.Allowed))
	quoted := make([]string, 0, len(hosts))
	for _, h := range hosts {
		quoted = append(quoted, `"`+h+`"`)
	}
	return `{"rules":[{"resources":[` + strings.Join(quoted, ",") + `]}]}`
}

// Callers hold m.mu.
func (m *Machine) renderImages() string {
	var entries []string
	for _, image := range slices.Sorted(maps.Keys(m.Images)) {
		repo, tag, _ := strings.Cut(image, ":")
		entries = append(entries, `{"repository":"docker.io/library/`+repo+`","tag":"`+tag+`"}`)
	}
	return `{"images":[` + strings.Join(entries, ",") + `]}`
}

// Compile-time guards, here rather than in a `_test.go` for Fake's reason: a
// change to either interface must fail `go build ./...`, not the first test of
// some downstream package.
var (
	_ Runner       = (*Machine)(nil)
	_ SecretRunner = (*Machine)(nil)
)

// NormalizeNetworkResource applies sbx's own normalization of a network
// resource: a host with no port is stored with :443.
//
// The rule comes from ONE sample, and the next reader should know which:
// measured 2026-08-16 against sbx v0.38.0, on a machine where a human had run
// `sbx policy allow network cdn.playwright.dev` (no port, the spelling
// digitaleo-den-env's README tells them to type). That rule comes back out of
// `sbx policy ls --type network --source local --decision allow --json` as
// "cdn.playwright.dev:443".
//
// What has NOT been observed yet is den performing the round trip itself —
// den's own `policy allow` is the same command shape, so the inference holds,
// but the first real `den init --source --yes` is what confirms it. If sbx
// ever stores a portless allow under another default, the failure mode is the
// one this function exists to prevent: den reporting a host it just allowed as
// still missing, and a resource it just applied failing to verify.
//
// It lives HERE, in the package that owns what sbx says, and is the SOLE
// definition: internal/converge compares a manifest's declared host against
// those stored strings, and Machine simulates the storing. Two copies of this
// rule would drift, and the drift would be invisible — den would report a host
// it had just allowed as still missing, and a resource it had just applied
// would fail to verify.
//
// The IPv6 guard is not decoration — "::1" and "[::1]:443" both contain
// colons, and treating the first as already-ported would make den disagree
// with sbx on the one shape nobody would think to test.
func NormalizeNetworkResource(host string) string {
	if strings.HasPrefix(host, "[") {
		if i := strings.LastIndex(host, "]"); i >= 0 {
			if strings.Contains(host[i:], ":") {
				return host
			}
			return host + ":443"
		}
	}
	if strings.Count(host, ":") == 1 {
		return host
	}
	if strings.Contains(host, ":") {
		return host // bare IPv6, or a shape this double will not second-guess
	}
	return host + ":443"
}
