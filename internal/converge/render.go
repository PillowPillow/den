package converge

import (
	"fmt"
	"io"
	"strings"

	"github.com/PillowPillow/den/internal/source"
)

// RenderPlan writes the plan a user confirms.
//
// Deterministic by construction: it iterates the plan's own ordered slices and
// never a map, so two runs on one machine print byte-identical text. That is
// not cosmetic — a user comparing today's plan with yesterday's must see only
// real changes, and the acceptance tests compare output.
//
// It names the TRUST BOUNDARY before anything else den would do: confirming a
// plan that builds a stack authorizes running that source's provision scripts
// (spec §4.3). A confirmation prompt that hid it would be uninformed consent.
func RenderPlan(w io.Writer, p *Plan) {
	fmt.Fprintf(w, "source: %s  version: %s\n", p.Source, p.Version)
	if p.TrustBoundary != "" {
		fmt.Fprintf(w, "\n%s\n", p.TrustBoundary)
	}

	fmt.Fprintf(w, "\nRESOURCES\n")
	if len(p.Resources) == 0 {
		fmt.Fprintln(w, "(none declared)")
	}
	for _, r := range p.Resources {
		fmt.Fprintf(w, "%-9s %-14s %s\n", r.Action, r.Kind, r.ID)
		// The observed/expected pair is printed only when it says something the
		// action does not: an unchanged resource whose two states agree would
		// add a line per resource for no information.
		if r.Action != ActionUnchanged || !r.Known {
			fmt.Fprintf(w, "          observed: %s\n", observedText(r))
			if r.Expected != "" {
				fmt.Fprintf(w, "          expected: %s\n", r.Expected)
			}
		}
	}

	if len(p.RepoMatches) > 0 {
		fmt.Fprintf(w, "\nREPOSITORIES\n")
		for _, m := range p.RepoMatches {
			fmt.Fprintf(w, "%-12s %s\n", m.Requirement.Key, repoText(m))
			for _, c := range m.Candidates {
				fmt.Fprintf(w, "             candidate: %s\n", c)
			}
		}
	}

	if len(p.Nests) > 0 {
		fmt.Fprintf(w, "\nNESTS\n")
		for _, n := range p.Nests {
			fmt.Fprintf(w, "%-18s %s%s\n", n.Name, n.Status, missingText(n))
		}
	}
	fmt.Fprintf(w, "\nstatus: %s\n", p.Status)
}

// RenderStatus writes the read-only view of an installed source (spec §12.1).
// The same model as the plan, minus the actions: a status describes, a plan
// proposes.
func RenderStatus(w io.Writer, p *Plan) {
	fmt.Fprintf(w, "source: %s  version: %s  status: %s\n", p.Source, p.Version, p.Status)

	fmt.Fprintf(w, "\nRESOURCES\n")
	for _, r := range p.Resources {
		fmt.Fprintf(w, "%-18s %s\n", label(r), resourceStatusText(r))
	}

	if len(p.Nests) > 0 {
		fmt.Fprintf(w, "\nNESTS\n")
		for _, n := range p.Nests {
			fmt.Fprintf(w, "%-18s %-11s%s\n", n.Name, n.Status, missingDetail(n, p))
		}
	}
}

// label is a resource's display name: "stack:base" reads as one object, where
// the id alone ("base") would not say what it is.
func label(r ResourcePlan) string {
	if r.Kind == KindStackBuild {
		return "stack:" + r.ID
	}
	if r.Kind == KindBuildNetwork {
		return KindBuildNetwork
	}
	return r.ID
}

// observedText is the ONE place an unobservable resource is worded. den never
// prints "absent" for something it could not read: the two have different
// remedies (configure it / find out why sbx did not answer), and a user acts
// on the word.
func observedText(r ResourcePlan) string {
	if !r.Known {
		if r.Observed != "" {
			return "unknown (" + r.Observed + ")"
		}
		return "unknown — den could not observe it"
	}
	if r.Observed == "" {
		return "absent"
	}
	return r.Observed
}

func resourceStatusText(r ResourcePlan) string {
	switch {
	case !r.Known:
		return string(source.ResourceUnknown)
	case r.Action == ActionBlocked:
		return string(source.ResourceBlocked)
	case r.Action == ActionUnchanged:
		return string(source.ResourceReady)
	default:
		return string(source.ResourceBlocked) + " (not applied yet)"
	}
}

// repoText renders one discovery verdict. Each wording says what den KNOWS and
// what it would do — a "found" that turns out to be a guess is exactly what
// the classification exists to keep out of a confirmation.
func repoText(m RepoMatch) string {
	switch m.Kind {
	case MatchExplicit:
		return "map to " + m.Path + " (you named it)"
	case MatchRemote:
		return "map to " + m.Path + " (its remote is this repository)"
	case MatchName:
		return "found " + m.Path + " by name only — confirm it, or it stays unmapped"
	case MatchAmbiguous:
		return "several candidates — choose one, or it stays unmapped"
	default:
		return "not on this machine — den does not clone it; the nests needing it stay not_ready"
	}
}

func missingText(n NestReadiness) string {
	if len(n.MissingRepos) == 0 {
		return ""
	}
	return "   missing repo: " + strings.Join(n.MissingRepos, ", ")
}

// missingDetail is the status view's richer version: it adds the declared URL
// and the command that fixes it, which is what a user reading `den source
// status` is looking for (spec §12.1).
func missingDetail(n NestReadiness, p *Plan) string {
	if len(n.MissingRepos) == 0 {
		return ""
	}
	urls := map[string]string{}
	for _, m := range p.RepoMatches {
		urls[m.Requirement.Key] = m.Requirement.URL
	}
	var parts []string
	for _, key := range n.MissingRepos {
		if url := urls[key]; url != "" {
			parts = append(parts, fmt.Sprintf("%s (%s)", key, url))
			continue
		}
		parts = append(parts, key)
	}
	return fmt.Sprintf("missing repo: %s — clone it, then run `den source configure %s`",
		strings.Join(parts, ", "), p.Source)
}
