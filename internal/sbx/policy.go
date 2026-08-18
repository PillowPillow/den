package sbx

import "context"

// LocalNetworkPolicy reads the machine's LOCAL network allow rules, as JSON.
//
// It exists so the argv has ONE spelling. Two callers need this exact read for
// opposite purposes — internal/converge parses it to observe what a source's
// build_network still has to allow, and `den doctor` only asks whether it
// answers at all — and a second hand-written argv is how the diagnostic and
// the observation start disagreeing about which command failed.
//
// The output is returned RAW, unparsed: the parser belongs to the caller that
// needs the hosts (converge.parseAllowedHosts), and doctor needs no parser at
// all. What matters to both is the error, and it is returned as is: ExecError
// already renders the binary, the full argv and sbx's own stderr, which is
// where sbx's remedy lives.
//
// A fresh machine is exactly where this fails: sbx requires a one-time
// `sbx policy init <allow-all|balanced|deny-all>` before ANY policy command
// answers, and until it is run this read returns "global network policy has not
// been initialized" (observed on a colleague's laptop, 2026-08-18).
func LocalNetworkPolicy(ctx context.Context, r Runner) ([]byte, error) {
	return r.Run(ctx, "policy", "ls", "--type", "network", "--source", "local", "--decision", "allow", "--json")
}
