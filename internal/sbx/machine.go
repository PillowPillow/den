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
		m.Services["github"] = true
		return nil, nil
	case len(args) == 4 && args[0] == "policy" && args[1] == "allow" && args[2] == "network":
		m.Allowed[args[3]] = true
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
		case "--host":
			entry.Host = args[i+1]
		case "--env":
			entry.Env = args[i+1]
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
	return m.failure(args)
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
