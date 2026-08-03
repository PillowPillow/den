package build

import (
	"fmt"
	"os"
	"strings"

	"github.com/PillowPillow/den/internal/config"
)

// StepScript is one entry of `provision.steps`: its path, kept for the failure
// message, and its text.
type StepScript struct {
	Path string
	Body string
}

// Provisioning is the TEXT of one stack's provision files, read on the HOST.
//
// Read as a whole, before anything is created: a build that discovers a
// missing step after four minutes of base image has spent that time to reach a
// refusal den could have made instantly. Nothing is read twice — den needs the
// text anyway to compose the payloads.
type Provisioning struct {
	// Includes is every `provision.includes` file concatenated in order, or
	// empty when none is declared.
	Includes string
	Steps    []StepScript
}

// Payload is what step i is sent as, and it is the whole of `includes`'
// semantics: the includes text, then the step.
//
// CONCATENATION, not a `source`: nothing is written into the VM, the text
// travels inside the exec argv. Which is also why it is re-emitted for EVERY
// step — each `sbx exec` opens a fresh shell, so a function or a variable an
// include defines does not survive the step that saw it (spec §6).
//
// With no includes the step travels VERBATIM, with no separator prepended: a
// stray leading newline would shift every line number a shell error reports,
// which is the one thing a build log must get right.
func (p Provisioning) Payload(i int) string {
	if p.Includes == "" {
		return p.Steps[i].Body
	}
	return p.Includes + "\n" + p.Steps[i].Body
}

// ReadProvisioning reads a stack's includes and steps, in declaration order.
func ReadProvisioning(s *config.Stack) (Provisioning, error) {
	var includes strings.Builder
	for _, path := range s.Provision.Includes {
		body, err := os.ReadFile(path)
		if err != nil {
			return Provisioning{}, fmt.Errorf(
				"stack %q: unreadable `provision.includes` entry %s: %w", s.Name, path, &config.FileError{Err: err})
		}
		includes.Write(body)
		// A file that does not end in a newline would weld its last line onto
		// the next include's first. The separator is added here rather than
		// demanded of the user.
		if len(body) > 0 && body[len(body)-1] != '\n' {
			includes.WriteByte('\n')
		}
	}

	p := Provisioning{Includes: includes.String(), Steps: make([]StepScript, 0, len(s.Provision.Steps))}
	for _, path := range s.Provision.Steps {
		body, err := os.ReadFile(path)
		if err != nil {
			return Provisioning{}, fmt.Errorf(
				"stack %q: unreadable `provision.steps` entry %s: %w", s.Name, path, &config.FileError{Err: err})
		}
		p.Steps = append(p.Steps, StepScript{Path: path, Body: string(body)})
	}
	return p, nil
}
