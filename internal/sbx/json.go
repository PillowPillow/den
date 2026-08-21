package sbx

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// DecodeJSON reads the FIRST top-level JSON value of an sbx `--json` output
// into v, and IGNORES whatever follows it.
//
// The trailing content is not hypothetical. sbx checks for a new release in
// the background (its own `startUpdateCheck`, visible in the binary) and
// prints the result on STDOUT, BEHIND the payload — measured 2026-08-21 on
// sbx v0.38.0:
//
//	{"sandboxes":[ ... ]}
//	+-------------------------------------+
//	| Docker Sandboxes Update Available   |
//	| v0.38.0  ->  v0.39.0                |
//	+-------------------------------------+
//
// json.Unmarshal refuses ANY content after the value, so on the day sbx
// decided to advertise v0.39.0, `den exec swimspot bash` died with
// `sbx ls: unreadable JSON output (invalid character 'â' after top-level
// value)` — den refusing to attach to a VM that was running, over a cosmetic
// line it did not write, with no remedy the user could apply. The banner is
// throttled, so the failure comes back once a day: intermittent, and
// undiagnosable for anyone who has not read this comment.
//
// Silencing the banner is NOT available: no flag on `sbx ls`, and no `SBX_*`
// variable in the binary does it (searched 2026-08-21, sbx v0.38.0). Tolerance
// on den's side is the only place the problem can be solved.
//
// The doctrine is not new here: internal/policy's readVerdict already decoded
// this way, for this exact reason, which is why `sbx policy check` was the one
// path the outage spared. This function is that doctrine, made shared instead
// of local.
//
// What PRECEDES the value stays an ERROR: den does not go looking for a
// payload in the middle of a stream it does not understand, and the raw output
// in the message keeps that case diagnosable.
//
// The empty-output guard comes FIRST, and it is not decoration: a json.Decoder
// answers a bare `EOF` on empty input, where json.Unmarshal at least said
// "unexpected end of JSON input". Without it, tolerating the banner would have
// made the "sbx wrote nothing at all" failure LESS readable than before the
// fix.
//
// command is the subcommand as a user would type it ("ls", "template ls"): on
// the success path there is no ExecError to render the argv, so this string is
// the only thing that locates the failure.
func DecodeJSON(command string, output []byte, v any) error {
	if len(bytes.TrimSpace(output)) == 0 {
		return fmt.Errorf(
			"sbx %s: wrote nothing to stdout (empty output) — check that the `--json` flag "+
				"still exists on this sbx, and that the payload is not going to stderr", command)
	}
	if err := json.NewDecoder(bytes.NewReader(output)).Decode(v); err != nil {
		// The raw output is in the message: without it, a schema change on
		// sbx's side would be undiagnosable.
		return fmt.Errorf("sbx %s: unreadable JSON output (%w): %s", command, err, string(output))
	}
	return nil
}
