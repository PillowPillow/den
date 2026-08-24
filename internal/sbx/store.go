package sbx

import (
	"context"
	"fmt"
	"strings"
)

// Store is one machine-side inventory sbx keeps and den does not own.
//
// A TABLE rather than a reader per surface, and that generality is the whole
// point: sbx v0.39.0 already ships three of these — the global secret store,
// the MCP registry, the agent-skills store — and it grows them faster than den
// grows parsers. A fourth surface must be a row here, not a fourth reader to
// write and a fourth report to word.
type Store struct {
	// Name is what den's report calls the surface.
	Name string
	// Args is the read command, without the binary.
	Args []string
	// Look is that same command spelled out, so a message can send the user to
	// the primary source rather than paraphrase it.
	Look string
	// Empty is the sentinel the command prints when it has nothing to list.
	// Matched as a substring of a line — see ReadStore for why this is the only
	// reading den is entitled to.
	Empty string
}

// Stores are the surfaces den can only observe as "something is there".
//
// The global secret store is deliberately NOT here: `sbx secret ls -g` is the
// one surface whose entries den can NAME (converge.ReadSbxState parses it by
// column), so it is reported by identity. These two are the surfaces where
// naming would mean guessing.
var Stores = []Store{
	{
		Name:  "sbx mcp servers",
		Args:  []string{"mcp", "ls"},
		Look:  "sbx mcp ls",
		Empty: "No MCP servers registered",
	},
	{
		Name:  "sbx skills",
		Args:  []string{"skills", "ls"},
		Look:  "sbx skills ls",
		Empty: "No skills found",
	},
}

// ReadStore reports whether the store holds anything at all.
//
// A BOOLEAN, not a list, and that is a measurement talking rather than a
// shortcut. Neither command accepts `--json` (probed 2026-08-24 on v0.39.0,
// spec §14.3: `ERROR: unknown flag: --json`, printed on stdout with exit 0 —
// so a reader that judged the flag by the exit code would conclude the
// opposite), and neither POPULATED output has ever been observed: seeing one
// means writing into the very registry this report exists to make visible
// (`sbx mcp add`, `sbx skills import`). A header-anchored parser like
// converge.parseSecretList would therefore be a SUPPOSITION about columns
// nobody has seen — which is exactly what makes §14.0 and §14.2 worth
// something, that they suppose nothing. `sbx skills` also announces itself
// EXPERIMENTAL: its layout is not a contract.
//
// So den recognizes the NEGATIVE SENTINEL and treats everything else as
// occupied. The failure direction is the right one: an output den does not
// recognize becomes "something is there, go look", never "this surface is
// empty" — that second reading is precisely the blind spot this report closes.
//
// It is NOT an emptiness test on the output. `sbx mcp ls` prints its gateway
// header ("LOCAL · managed by you · ✓ on") even with nothing registered, so
// "non-empty output ⇒ servers exist" is false on the one machine anyone has
// measured.
//
// StripUpdateBanner first, for the reason ReadSbxState applies it too: these
// are text reads, and sbx puts its update box on stdout, where a framed line
// would sit between den and the sentinel.
func ReadStore(ctx context.Context, runner Runner, s Store) (bool, error) {
	out, err := runner.Run(ctx, s.Args...)
	if err != nil {
		return false, fmt.Errorf("reading %s (`%s`): %w", s.Name, s.Look, err)
	}
	for _, line := range strings.Split(StripUpdateBanner(string(out)), "\n") {
		if strings.Contains(line, s.Empty) {
			return false, nil
		}
	}
	return true, nil
}
