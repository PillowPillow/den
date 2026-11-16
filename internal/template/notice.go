package template

import (
	"context"
	"fmt"

	"github.com/PillowPillow/den/internal/worktree"
)

func short(commit string) string {
	if len(commit) > 8 {
		return commit[:8]
	}
	return commit
}

// Notices lists what `den up` prints to stderr. It never fails: a remote that does not answer, or a
// hash that cannot be computed, is silence, since a notice must not stand between the user and a start.
func Notices(ctx context.Context, g worktree.Git, r *Resolved) []string {
	if r.Record == nil {
		return nil
	}
	name := r.Choice.Name()
	if r.Record.Source.Kind == "repo" {
		head, err := RemoteHead(ctx, g, r.Record.Source.URL, r.Record.Source.Ref)
		if err != nil || head == r.Record.Commit {
			return nil
		}
		return []string{fmt.Sprintf("template %s: a newer definition is available (%s -> %s); run 'den build %s --pull'",
			name, short(r.Record.Commit), short(head), name)}
	}
	hash, err := DefinitionHash(r.Defs, name)
	if err != nil || hash == r.Record.DefinitionHash {
		return nil
	}
	return []string{fmt.Sprintf("template %s: its definition changed since the last build; run 'den build %s'", name, name)}
}
