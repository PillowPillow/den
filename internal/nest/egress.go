package nest

import "slices"

// unionEgress merges the cascade's allowlists (baseline ∪ stack ∪ nest),
// dedupes and SORTS. Sorting is a determinism requirement: this list becomes
// the generated mixin's network.allow, asserted in a golden file.
func unionEgress(lists ...[]string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0)
	for _, list := range lists {
		for _, h := range list {
			if h == "" || seen[h] {
				continue
			}
			seen[h] = true
			out = append(out, h)
		}
	}
	slices.Sort(out)
	return out
}
