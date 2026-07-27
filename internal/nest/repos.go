package nest

import (
	"fmt"
	"sort"
	"strings"
)

// verifieNomsUniques rejette deux repos de même basename dans un même nest.
// Le basename EST l'identité d'un repo : --without/--only le désignent par ce
// nom, et au plan 2 il devient un chemin (worktree_root/<wt>/<repo>) et la
// position d'un argument de `sbx create`. Deux homonymes rendraient les trois
// ambigus — et `--without api` en ferait disparaître deux en silence. C'est une
// configuration qu'on ne peut pas honorer : erreur dure, pas surprise.
func verifieNomsUniques(repos []Repo) error {
	vus := make(map[string]string, len(repos))
	for _, r := range repos {
		if precedent, ok := vus[r.Name()]; ok {
			return fmt.Errorf(
				"deux repos portent le même nom court %q (%s et %s) — ce nom sert à --without/--only "+
					"et au chemin du worktree, il doit être unique dans le nest",
				r.Name(), precedent, r.Path)
		}
		vus[r.Name()] = r.Path
	}
	return nil
}

// SelectRepos applique --without / --only à la liste déclarée par le nest.
// Les repos requis sont toujours retenus : seuls les optionnels se filtrent.
// L'ordre de déclaration est préservé — il fixe l'ordre des positionnels `sbx create`.
func SelectRepos(repos []Repo, without, only []string) ([]Repo, error) {
	if len(without) > 0 && len(only) > 0 {
		return nil, fmt.Errorf("--without et --only sont mutuellement exclusifs")
	}

	connus := make(map[string]Repo, len(repos))
	for _, r := range repos {
		connus[r.Name()] = r
	}

	verifie := func(flag string, valeurs []string) error {
		for _, v := range valeurs {
			if _, ok := connus[v]; !ok {
				return fmt.Errorf("%s : repo %q inconnu dans ce nest (disponibles : %s)",
					flag, v, strings.Join(nomsTries(repos), ", "))
			}
		}
		return nil
	}
	if err := verifie("--without", without); err != nil {
		return nil, err
	}
	if err := verifie("--only", only); err != nil {
		return nil, err
	}

	exclus := make(map[string]bool, len(without))
	for _, v := range without {
		if !connus[v].Optional {
			return nil, fmt.Errorf("--without : %q est un repo requis de ce nest, il ne peut pas être retiré", v)
		}
		exclus[v] = true
	}

	garde := make(map[string]bool, len(only))
	for _, v := range only {
		garde[v] = true
	}

	out := make([]Repo, 0, len(repos))
	for _, r := range repos {
		switch {
		case !r.Optional: // requis : toujours
		case exclus[r.Name()]:
			continue
		case len(only) > 0 && !garde[r.Name()]:
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func nomsTries(repos []Repo) []string {
	out := make([]string, 0, len(repos))
	for _, r := range repos {
		out = append(out, r.Name())
	}
	sort.Strings(out)
	return out
}
