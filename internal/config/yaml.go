package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// DecodeYAMLStrict decodes raw into dest, REJECTING any unknown key.
//
// Lax decoding is the worst possible failure mode for den: a typo (`egres:`
// for `egress:`) silently leaves the allowlist empty, `doctor` certifies a
// "consistent" config, and the sandbox stops reaching api.anthropic.com with
// no visible cause. An unknown key is therefore an error.
//
// path is used only in the message: it must name the offending file to stay actionable.
func DecodeYAMLStrict(path string, raw []byte, dest any) error {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(dest); err != nil {
		// yaml.v3 signals an empty document with io.EOF. An empty config file
		// (or one reduced to comments) isn't corrupt: it's a config that
		// declares nothing. We leave dest at its zero value — defaults and
		// validation will say what's missing, field by field.
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("%s: invalid YAML: %w", path, rewriteUnknownKeys(err))
	}
	// Decode reads only ONE document. Anything following a "---" was
	// therefore silently ignored — same failure mode as the unknown key
	// above (a configuration that doesn't apply without a word), but at
	// document granularity.
	//
	// Refuse rather than merge: den has no way to know which document is
	// authoritative, and picking one would be exactly the silence we're
	// trying to remove.
	//
	// The second Decode targets a yaml.Node, not dest: it only needs to KNOW
	// whether a document remains, not decode it — a second document with an
	// incompatible schema must be reported as an extra document, not a type error.
	//
	// THE MESSAGE DOES NOT CLAIM ANY DATA LOSS, deliberately: three forms
	// trigger this refusal WITHOUT anything being dropped (verified by
	// re-encoding the second document as empty in each case): a trailing
	// "---" with nothing behind it, front-matter form "---\n...\n---", and a
	// "---" followed only by comments. The refusal stays fail-closed on all
	// three — "..." is the real end-of-document marker, and the ambiguity
	// isn't worth tolerating — but claiming a loss the user doesn't actually
	// suffer would send them hunting for a configuration that never existed.
	var next yaml.Node
	if err := dec.Decode(&next); err == nil {
		return fmt.Errorf(
			"%s: the file contains multiple YAML documents (separated by \"---\") — "+
				"den reads only one and refuses rather than guess which is authoritative; "+
				"keep a single document, without a \"---\" separator "+
				"(the end marker \"...\" is still accepted)", path)
	}
	return nil
}

// unknownKeyPattern captures yaml.v3's diagnostic for an unknown key. The
// "line N:" prefix yaml.v3 places in front is not part of it and must survive
// the rewrite — it's what makes the message actionable.
var unknownKeyPattern = regexp.MustCompile(`field (\S+) not found in type \S+`)

// typeErrorHeader is the header yaml.v3 places before the error LIST of a
// *yaml.TypeError. A fixed literal from the library, with no variable part.
//
// It's visible on every invalid YAML in den — the first line of `den nest ls`
// on a broken nest, while the detail line right below is already rewritten.
//
// Each detail's "line N:" prefix, though, stays as-is: it's the line number
// that makes the message actionable, and it's pinned by a test.
const typeErrorHeader = "yaml: unmarshal errors:"

// rewriteUnknownKeys rewrites "field egres not found in type config.Global"
// into `unknown key "egres"`: the Go type is an implementation detail with no
// business in front of the user. Also replaces yaml.v3's header.
//
// If NOTHING is recognized (malformed YAML with neither an unknown key nor
// the list header), the error passes through UNCHANGED: an imperfect message
// beats a mangled one. The original error stays reachable via errors.Unwrap —
// this rewrites what the user reads, not what the code can inspect.
func rewriteUnknownKeys(err error) error {
	msg := err.Error()
	rewritten := unknownKeyPattern.ReplaceAllString(msg, `unknown key "$1"`)
	rewritten = strings.Replace(rewritten, typeErrorHeader, "decoding errors:", 1)
	if rewritten == msg {
		return err
	}
	return &yamlError{msg: rewritten, cause: err}
}

type yamlError struct {
	msg   string
	cause error
}

func (e *yamlError) Error() string { return e.msg }
func (e *yamlError) Unwrap() error { return e.cause }
