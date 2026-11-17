package instance

import (
	"path/filepath"

	"github.com/PillowPillow/den/v2/internal/registry"
	"github.com/PillowPillow/den/v2/internal/sbxenv"
)

// repoMapOf derives the instance's repo map from the plan and its record, never from a scan: it is
// exact for the VM's life because sbx fixes the mounts at create. The branch is the one typed with
// -w: worktree.Ensure refuses a worktree found in place on any other branch or on a detached HEAD.
func repoMapOf(p *Plan, rec registry.Record) sbxenv.RepoMap {
	m := sbxenv.RepoMap{
		Schema: 1, Instance: p.Instance, Nest: rec.Nest, ConfigFile: rec.ConfigFile,
		Template: rec.Template, Start: rec.Start,
	}
	if p.Worktree != nil {
		m.WorktreeBranch = p.Worktree.Branch
	}
	// With `start: .` the project folder is mounted as the workspace: a `repos:` key naming it
	// must not be offered as a repo this VM lacks.
	listed := map[string]bool{}
	if p.Resolved.Start == "." {
		listed[p.Workspace] = true
	}
	for _, r := range p.Repos {
		e := sbxenv.MappedRepo{Name: r.Name, Key: r.Key, Path: r.Path, Optional: r.Optional}
		if p.Worktree != nil {
			e.Branch = p.Worktree.Branch
		}
		m.Mounted = append(m.Mounted, e)
		listed[r.Path] = true
	}
	for _, r := range p.Resolved.Repos {
		if !listed[r.Path] {
			m.NestNotMounted = append(m.NestNotMounted, sbxenv.MappedRepo{Name: r.Name, Key: r.Key, Path: r.Path, Optional: r.Optional})
			listed[r.Path] = true
		}
	}
	for _, r := range p.Resolved.MachineRepos {
		if !listed[r.Path] {
			m.Machine = append(m.Machine, sbxenv.MappedRepo{Name: r.Name, Path: r.Path})
		}
	}
	return m
}

// writeRepoMap writes both forms of the map under kitDir's files/home/, which sbx copies into the
// agent's home at create; nothing lands under a mount, so no other VM can rewrite it.
func writeRepoMap(kitDir string, m sbxenv.RepoMap) error {
	js, err := sbxenv.RenderRepoMapJSON(m)
	if err != nil {
		return err
	}
	if err := WriteGenFile(filepath.Join(kitDir, sbxenv.RepoMapJSONFile), js); err != nil {
		return err
	}
	return WriteGenFile(filepath.Join(kitDir, sbxenv.RepoMapRulesFile), sbxenv.RenderRepoMapMarkdown(m))
}
