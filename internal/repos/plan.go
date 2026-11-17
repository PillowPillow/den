package repos

import "sort"

// Change is one table line a scan would write. Old is empty for an addition.
type Change struct{ Identity, Path, Old string }

// Duplicate lists the clones that carry one identity when more than one does.
// Others is sorted and never holds Chosen.
type Duplicate struct {
	Identity, Chosen string
	Others           []string
}

// Plan holds what a scan would do to the table, every slice sorted by identity.
// Stale holds identities: the caller looks the path up in the table.
type Plan struct {
	Adds, Replaces []Change
	Duplicates     []Duplicate
	Stale          []string
}

// BuildPlan compares the clones a scan found with the table. valid is injected so
// tests need no real folders; production passes Valid.
func BuildPlan(clones []Clone, t Table, valid func(path, identity string) bool) Plan {
	type candidate struct {
		path     string
		isOrigin bool
	}
	found := map[string][]candidate{}
	for _, c := range clones {
		// A clone reached through two remotes of one identity counts once.
		seen := map[string]int{}
		for _, r := range c.Remotes {
			i, ok := seen[r.Identity]
			if !ok {
				seen[r.Identity] = len(found[r.Identity])
				found[r.Identity] = append(found[r.Identity], candidate{path: c.Path})
				i = seen[r.Identity]
			}
			if r.Name == "origin" {
				found[r.Identity][i].isOrigin = true
			}
		}
	}

	var p Plan
	for identity, cands := range found {
		sort.Slice(cands, func(i, j int) bool { return cands[i].path < cands[j].path })
		winner := cands[0].path
		for _, c := range cands {
			if c.isOrigin {
				winner = c.path
				break
			}
		}

		entry, inTable := t.Repos[identity]
		chosen := winner
		entryHolds := false
		switch {
		case !inTable:
			p.Adds = append(p.Adds, Change{Identity: identity, Path: winner})
		case valid(entry.Path, identity):
			// A valid entry is the user's choice, even when another clone's origin would win.
			chosen = entry.Path
			entryHolds = true
		default:
			p.Replaces = append(p.Replaces, Change{Identity: identity, Path: winner, Old: entry.Path})
		}

		othersExist := len(cands) > 1
		if entryHolds {
			othersExist = false
			for _, c := range cands {
				if c.path != chosen {
					othersExist = true
				}
			}
		}
		if othersExist {
			d := Duplicate{Identity: identity, Chosen: chosen}
			for _, c := range cands {
				if c.path != chosen {
					d.Others = append(d.Others, c.path)
				}
			}
			p.Duplicates = append(p.Duplicates, d)
		}
	}

	// An entry no clone of this scan carries is stale only when it no longer holds; a valid one
	// outside the scan root is none of the scan's business.
	for identity, entry := range t.Repos {
		if _, ok := found[identity]; !ok && !valid(entry.Path, identity) {
			p.Stale = append(p.Stale, identity)
		}
	}

	sort.Slice(p.Adds, func(i, j int) bool { return p.Adds[i].Identity < p.Adds[j].Identity })
	sort.Slice(p.Replaces, func(i, j int) bool { return p.Replaces[i].Identity < p.Replaces[j].Identity })
	sort.Slice(p.Duplicates, func(i, j int) bool { return p.Duplicates[i].Identity < p.Duplicates[j].Identity })
	sort.Strings(p.Stale)
	return p
}

// Apply writes each change under its identity, replacing any previous path.
func (t *Table) Apply(changes ...Change) {
	if t.Repos == nil {
		t.Repos = map[string]Entry{}
	}
	for _, c := range changes {
		t.Repos[c.Identity] = Entry{Path: c.Path}
	}
}
