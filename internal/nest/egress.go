package nest

import "sort"

// UnionEgress fusionne les allowlists de la cascade (baseline ∪ stack ∪ nest),
// déduplique et TRIE. Le tri est une exigence de déterminisme : cette liste
// devient le network.allow du mixin généré, asserté en golden file.
func UnionEgress(listes ...[]string) []string {
	vu := make(map[string]bool)
	out := make([]string, 0)
	for _, liste := range listes {
		for _, h := range liste {
			if h == "" || vu[h] {
				continue
			}
			vu[h] = true
			out = append(out, h)
		}
	}
	sort.Strings(out)
	return out
}
