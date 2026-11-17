package sbxenv

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// The map reaches the VM through the instance's den-claude kit (`files/home/` is copied into the
// agent's home at create, measured on claude-code-docker, home /home/agent). A rules file, not
// ~/.claude/CLAUDE.md: that file and settings.json belong to the profile kit, which would clobber
// the map or be clobbered by it.
const (
	RepoMapVMPath    = "/home/agent/.den/repos.json"
	RepoMapEnv       = "DEN_REPOS"
	RepoMapRulesFile = "files/home/.claude/rules/den-instance.md"
	RepoMapJSONFile  = "files/home/.den/repos.json"
)

type MappedRepo struct {
	Name     string `json:"name"`
	Key      string `json:"key,omitempty"`
	Path     string `json:"path"`
	Optional bool   `json:"optional,omitempty"`
	Branch   string `json:"branch,omitempty"` // worktree only; "detached HEAD" when detached
}

// RepoMap is what one instance can reach: its mounts, the nest's optional repos left out at
// create, and the machine's other `repos:` keys, so the agent can name the exact remedy.
type RepoMap struct {
	Schema         int          `json:"schema"`
	Instance       string       `json:"instance"`
	Nest           string       `json:"nest"`
	ConfigFile     string       `json:"config_file"`
	Template       string       `json:"template"`
	Start          string       `json:"start"`
	Mounted        []MappedRepo `json:"mounted"`
	NestNotMounted []MappedRepo `json:"nest_not_mounted"`
	Machine        []MappedRepo `json:"machine"`
}

// RenderRepoMapJSON keeps empty lists as [] so a script never has to tell null from empty.
func RenderRepoMapJSON(m RepoMap) ([]byte, error) {
	for _, l := range []*[]MappedRepo{&m.Mounted, &m.NestNotMounted, &m.Machine} {
		if *l == nil {
			*l = []MappedRepo{}
		}
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// RenderRepoMapMarkdown names no agent and no project convention: Codex will read the same text.
func RenderRepoMapMarkdown(m RepoMap) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# den instance %s\n\n", m.Instance)
	b.WriteString("den wrote this file when it created this VM. It lists the repos this VM can reach.\n\n")
	fmt.Fprintf(&b, "- Nest: `%s`, declared in %s\n", m.Nest, m.ConfigFile)
	fmt.Fprintf(&b, "- Template: `%s`\n", m.Template)
	fmt.Fprintf(&b, "- Start folder: %s\n", m.Start)
	section(&b, "Mounted repos", "", m.Mounted)
	section(&b, "Repos of the nest not mounted", "host path ", m.NestNotMounted)
	section(&b, "Other repos of this machine", "host path ", m.Machine)
	fmt.Fprintf(&b, "\nA repo missing from \"Mounted repos\" is not available in this VM. Say so, and ask the human to add it: `den rm %s`, then `den up %s --repo <host path>`, or add the repo to the nest. Do not look for it elsewhere and do not clone it.\n", m.Instance, m.Nest)
	fmt.Fprintf(&b, "\nThe same data as JSON is in the file named by the environment variable `%s`.\n", RepoMapEnv)
	return b.Bytes()
}

func section(b *bytes.Buffer, heading, pathPrefix string, repos []MappedRepo) {
	fmt.Fprintf(b, "\n## %s\n\n", heading)
	if len(repos) == 0 {
		b.WriteString("- (none)\n")
		return
	}
	for _, r := range repos {
		fmt.Fprintf(b, "- %s — %s%s", r.Name, pathPrefix, r.Path)
		if r.Key != "" && r.Key != r.Name {
			fmt.Fprintf(b, " (key %s)", r.Key)
		}
		if r.Optional {
			b.WriteString(" (optional)")
		}
		if r.Branch != "" {
			fmt.Fprintf(b, " (worktree, branch %s)", r.Branch)
		}
		b.WriteString("\n")
	}
}
