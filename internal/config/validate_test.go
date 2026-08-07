package config

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

func validGlobal() *Global {
	return &Global{
		Agents: map[string]Agent{
			"claude": {ConfigDir: "/tmp/claude", Update: "claude update", BinDirs: []string{"$HOME/.local/bin"}},
		},
		Defaults:       Defaults{Agent: "claude", Stack: "devx"},
		SSH:            SSH{Mode: "agent-forward"},
		WorktreeLayout: "central",
		WorktreeRoot:   "/tmp/wt",
	}
}

func TestValidateCorrectConfig(t *testing.T) {
	if errs := validGlobal().Validate(); len(errs) != 0 {
		t.Errorf("expected 0 errors, got %v", errs)
	}
}

// The three SSH modes from spec §10 are accepted. `none` wasn't covered by
// any test: nothing proved that a mode declared in the spec passed validation.
func TestValidateAcceptsTheSSHModesFromTheSpec(t *testing.T) {
	for _, mode := range []string{"agent-forward", "mount", "none"} {
		t.Run(mode, func(t *testing.T) {
			g := validGlobal()
			g.SSH.Mode = mode
			if mode == "mount" {
				g.SSH.Dir = "/tmp/ssh_sbx" // required only in this mode
			}
			if errs := g.Validate(); len(errs) != 0 {
				t.Errorf("mode %q refused although it's declared in spec §10: %v", mode, errs)
			}
		})
	}
}

func TestValidateDetectsFaults(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Global)
		want   string
	}{
		{"default agent missing from registry", func(g *Global) { g.Defaults.Agent = "codex" }, "codex"},
		{"default agent empty", func(g *Global) { g.Defaults.Agent = "" }, "defaults.agent"},
		{"default stack empty", func(g *Global) { g.Defaults.Stack = "" }, "defaults.stack"},
		{"empty agent registry", func(g *Global) { g.Agents = nil }, "agents"},
		{"unknown ssh mode", func(g *Global) { g.SSH.Mode = "vpn" }, "ssh.mode"},
		{"unknown worktree layout", func(g *Global) { g.WorktreeLayout = "scattered" }, "worktree_layout"},
		{"mount mode without dir", func(g *Global) { g.SSH.Mode = "mount"; g.SSH.Dir = "" }, "ssh.dir"},
		{"agent without update command", func(g *Global) {
			a := g.Agents["claude"]
			a.Update = ""
			g.Agents["claude"] = a
		}, "update"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := validGlobal()
			c.mutate(g)
			errs := g.Validate()
			if len(errs) == 0 {
				t.Fatalf("expected at least one error mentioning %q", c.want)
			}
			var all []string
			for _, e := range errs {
				all = append(all, e.Error())
			}
			joined := strings.Join(all, " | ")
			if !strings.Contains(joined, c.want) {
				t.Errorf("errors = %q, expected a mention of %q", joined, c.want)
			}
		})
	}
}

// The two judges of the same field must agree: validate.go used to test
// `Update == ""` where agent.FreshnessCommand tests
// strings.TrimSpace(...). A `update: "   "` would pass `den doctor` clean and
// only fail at spawn — the latest and least readable of the two moments. The
// stricter judge must win.
func TestValidateRefusesABlankUpdate(t *testing.T) {
	for _, blank := range []string{"   ", "\t", "\n"} {
		t.Run(strconv.Quote(blank), func(t *testing.T) {
			g := validGlobal()
			a := g.Agents["claude"]
			a.Update = blank
			g.Agents["claude"] = a

			errs := g.Validate()
			if len(errs) == 0 {
				t.Fatalf("update = %q: expected a refusal, the freshness command already rejects it at spawn", blank)
			}
			// The specific error, not just any: validGlobal() is otherwise
			// healthy, so any error unrelated to update would signal a fixture defect.
			var all []string
			for _, e := range errs {
				all = append(all, e.Error())
			}
			joined := strings.Join(all, " | ")
			if !strings.Contains(joined, "agents.claude.update") {
				t.Errorf("errors = %q, expected the key agents.claude.update", joined)
			}
		})
	}
}

// --- D4: bin_dirs is a list of PATHS, not code ------------------------------
//
// agent.FreshnessCommand injects bin_dirs literally into an
// `export PATH=%q`, unescaped, because the `$HOME` they contain MUST be
// expanded by the VM's bash (invariant 1 of FreshnessCommand). That same lack
// of escaping executes `$(...)` and backticks, renders a tab as a literal
// `\t`, and lets an empty element become "the current directory" in PATH.
//
// Escaping would therefore be self-defeating — it would kill `$HOME`. The
// defect is one of TYPING: these values aren't paths, and that's where they're refused.
func TestValidateRefusesABinDirThatIsNotAPath(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string // fragment the message must carry
	}{
		{"command substitution", "/opt/$(id -un)", "$("},
		{"backtick", "/opt/`id -un`", "`"},
		{"substitution nested in an expansion", "${x:-$(id)}", "$("},
		{"tab", "/opt/a\tb", "control character"},
		{"newline", "/opt/a\nb", "control character"},
		{"empty entry", "", "empty"},
		// F5 — measured on the line FreshnessCommand actually generates:
		// INSIDE DOUBLE QUOTES, bash does NOT expand the tilde.
		//   export PATH="~/.local/bin:$PATH"  →  ~/.local/bin:...  (literal)
		// The VM's PATH therefore receives a directory named "~", which
		// doesn't exist: exactly the `claude: exit 127` invariant 1 of
		// FreshnessCommand exists to prevent. Unquoted, bash would expand it
		// — but den doesn't generate that form, and can't without losing
		// protection for spaces.
		{"tilde", "~/.local/bin", "~"},
		{"user tilde", "~agent/.local/bin", "~"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := validGlobal()
			a := g.Agents["claude"]
			a.BinDirs = []string{c.value}
			g.Agents["claude"] = a

			errs := g.Validate()
			if len(errs) == 0 {
				t.Fatalf("bin_dirs[0] = %q: expected a refusal, got no error", c.value)
			}
			var all []string
			for _, e := range errs {
				all = append(all, e.Error())
			}
			joined := strings.Join(all, " | ")
			// The INDEXED key: with four bin_dirs, "bin_dirs" alone wouldn't
			// say which one to fix.
			if !strings.Contains(joined, "agents.claude.bin_dirs[0]") {
				t.Errorf("errors = %q, expected the indexed key agents.claude.bin_dirs[0]", joined)
			}
			if !strings.Contains(joined, c.want) {
				t.Errorf("errors = %q, expected the named cause (%q)", joined, c.want)
			}
		})
	}
}

// The indispensable counterpart: `$HOME` and `${HOME}` target the VM's home
// and must cross INTACT (invariant 1 of agent.FreshnessCommand). A refusal
// that's too broad would break them, and the test above wouldn't catch it.
func TestValidateAcceptsLegitimateBinDirs(t *testing.T) {
	for _, value := range []string{
		"$HOME/.local/bin",
		"${HOME}/.local/bin",
		"/usr/local/bin",
		"$HOME/.claude/local",
		// The refusal targets the PREFIX only. `backup~` is a perfectly legal
		// directory name, and `export PATH="/opt/backup~/bin:$PATH"` renders
		// `/opt/backup~/bin` — an exact path, nothing to fix. Refusing any
		// "~" would forbid this path for no reason.
		"/opt/backup~/bin",
	} {
		t.Run(value, func(t *testing.T) {
			g := validGlobal()
			a := g.Agents["claude"]
			a.BinDirs = []string{value}
			g.Agents["claude"] = a
			if errs := g.Validate(); len(errs) != 0 {
				t.Errorf("bin_dirs[0] = %q refused although it must cross intact: %v", value, errs)
			}
		})
	}
}

// F2 — the unfinished half of T2-min-5. `TrimSpace(a.Update)` had been
// aligned, but `config_dir` was tested two lines above with `== ""` — and
// `defaults.agent`, `defaults.stack`, `ssh.dir`, `worktree_root` were in the
// same boat. `den doctor` would exit 0 on a config that can't spawn, and the
// downstream MkdirAll would create a directory literally named "␣␣␣" in the
// user's current directory. (config_dir and worktree_root have since moved to
// their own TestValidateRefusesABlankConfigDir / TestValidateRefusesABlankWorktreeRoot
// below — neither message says "required" anymore, now that both default —
// but the TrimSpace judgment itself is unchanged and still belongs to this
// same fix.)
//
// A "required" field must judge on content, not on length.
func TestValidateRefusesABlankRequiredField(t *testing.T) {
	cases := []struct {
		field  string
		mutate func(*Global, string)
	}{
		// config_dir and worktree_root are deliberately NOT in this table:
		// since both now default (config.go), neither can say "required" —
		// see TestValidateRefusesABlankConfigDir and
		// TestValidateRefusesABlankWorktreeRoot below, which check their own
		// wording.
		{"defaults.agent", func(g *Global, v string) { g.Defaults.Agent = v }},
		{"defaults.stack", func(g *Global, v string) { g.Defaults.Stack = v }},
		{"ssh.dir", func(g *Global, v string) { g.SSH.Mode = "mount"; g.SSH.Dir = v }},
	}
	for _, c := range cases {
		for _, blank := range []string{"   ", "\t", "\n"} {
			t.Run(c.field+"/"+strconv.Quote(blank), func(t *testing.T) {
				g := validGlobal()
				c.mutate(g, blank)

				errs := g.Validate()
				if len(errs) == 0 {
					t.Fatalf("%s = %q: expected a refusal, got no error", c.field, blank)
				}
				var all []string
				for _, e := range errs {
					all = append(all, e.Error())
				}
				joined := strings.Join(all, " | ")
				// "<field>: required", not just the field's name.
				//
				// The weak version of this assertion let the mutation on
				// defaults.agent survive: with `== ""`, a
				// `defaults.agent: "   "` is judged as filled in, then looked
				// up as-is in the registry, and the resulting error is
				// `"   " is missing from the registry` — which does contain
				// "defaults.agent" and would satisfy a weaker assertion,
				// while inviting the user to declare an agent named "␣␣␣"
				// rather than fill in the field. The message IS the
				// deliverable, not just the refusal.
				required := c.field + ": required"
				if !strings.Contains(joined, required) {
					t.Errorf("errors = %q, expected %q (a blank field is a field to fill in, "+
						"not a value to look up in the registry)", joined, required)
				}
			})
		}
	}
}

// config_dir's own wording, split out of TestValidateRefusesABlankRequiredField
// above because its message changed shape: the field now defaults when
// ABSENT (config.go), so an explicitly blank value is no longer "missing", it's
// "written wrong" — the message must say so and must not say "required".
func TestValidateRefusesABlankConfigDir(t *testing.T) {
	for _, blank := range []string{"   ", "\t", "\n"} {
		t.Run(strconv.Quote(blank), func(t *testing.T) {
			g := validGlobal()
			a := g.Agents["claude"]
			a.ConfigDir = blank
			g.Agents["claude"] = a

			errs := g.Validate()
			if len(errs) == 0 {
				t.Fatalf("config_dir = %q: expected a refusal", blank)
			}
			var all []string
			for _, e := range errs {
				all = append(all, e.Error())
			}
			joined := strings.Join(all, " | ")
			if !strings.Contains(joined, "agents.claude.config_dir: blank") {
				t.Errorf("errors = %q, expected \"agents.claude.config_dir: blank\"", joined)
			}
			if strings.Contains(joined, "config_dir: required") {
				t.Errorf("errors = %q, must not say \"required\": the field has a default now", joined)
			}
		})
	}
}

// worktree_root's own wording, split out for the identical reason as
// config_dir above: it now defaults when ABSENT (config.go:73), so the
// message for an explicitly blank value must describe the field and the
// remedy, not claim the field is "required".
func TestValidateRefusesABlankWorktreeRoot(t *testing.T) {
	for _, blank := range []string{"   ", "\t", "\n"} {
		t.Run(strconv.Quote(blank), func(t *testing.T) {
			g := validGlobal()
			g.WorktreeRoot = blank

			errs := g.Validate()
			if len(errs) == 0 {
				t.Fatalf("worktree_root = %q: expected a refusal", blank)
			}
			var all []string
			for _, e := range errs {
				all = append(all, e.Error())
			}
			joined := strings.Join(all, " | ")
			if !strings.Contains(joined, "worktree_root: blank") {
				t.Errorf("errors = %q, expected \"worktree_root: blank\"", joined)
			}
			if strings.Contains(joined, "worktree_root: required") {
				t.Errorf("errors = %q, must not say \"required\": the field has a default now", joined)
			}
		})
	}
}

// The counterpart: a required field that IS filled in must not be refused.
// Without it, a misplaced TrimSpace that refused everything would pass the test above.
func TestValidateAcceptsFilledInRequiredFields(t *testing.T) {
	g := validGlobal()
	g.SSH.Mode = "mount"
	g.SSH.Dir = "/home/x/.ssh_sbx"
	if errs := g.Validate(); len(errs) != 0 {
		t.Errorf("healthy config refused: %v", errs)
	}
}

func TestValidateAccumulatesErrors(t *testing.T) {
	g := validGlobal()
	g.SSH.Mode = "vpn"
	g.WorktreeLayout = "scattered"
	if errs := g.Validate(); len(errs) < 2 {
		t.Errorf("expected at least 2 accumulated errors, got %d: %v", len(errs), errs)
	}
}

func TestValidateSortsAgentsDeterministically(t *testing.T) {
	// Go maps iterate in random order. Validate() must sort agent names
	// before iterating, or the error order would vary between runs. This
	// test proves the sort works by creating two faulty agents (alpha, zeta
	// without Update) and checking that errors appear in alphabetical order.
	g := validGlobal()
	g.Agents = map[string]Agent{
		"zeta":  {ConfigDir: "/tmp/zeta", Update: "", BinDirs: []string{}},
		"alpha": {ConfigDir: "/tmp/alpha", Update: "", BinDirs: []string{}},
	}
	g.Defaults.Agent = "alpha"

	errs := g.Validate()
	if len(errs) < 2 {
		t.Fatalf("expected at least 2 errors, got %d", len(errs))
	}

	// Extract the messages and check the order
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}

	// alpha.update must appear before zeta.update
	alphaIdx := -1
	zetaIdx := -1
	for i, msg := range msgs {
		if strings.Contains(msg, "alpha.update") {
			alphaIdx = i
		}
		if strings.Contains(msg, "zeta.update") {
			zetaIdx = i
		}
	}

	if alphaIdx == -1 || zetaIdx == -1 {
		t.Fatalf("expected alpha.update and zeta.update errors, got %v", msgs)
	}
	if alphaIdx >= zetaIdx {
		t.Errorf("error order not sorted: alpha at %d, zeta at %d (messages: %v)", alphaIdx, zetaIdx, msgs)
	}
}

// A RELATIVE worktree_root.
//
// LoadGlobalUnvalidated only makes the DEFAULT absolute
// (filepath.Join(denHome, "worktrees")); a value written by the user passes
// through unchanged, and ExpandPath only touches "~". A `worktree_root: wt`
// would therefore survive all the way to `git worktree add`, where it
// resolves against the REPO's directory.
//
// Measured on the binary, with `worktree_root: wt-relative`:
//
//	den doctor              → "config consistent", rc=0
//	den spawn api -w feat1  → worktree REALLY created in
//	                          <repo>/wt-relative/feat1/myrepo, branch feat1 created,
//	                          THEN fails on "workspace #1 is not absolute"
//
// Two faults in one: den pollutes the user's repo, who must clean up a
// worktree and a branch by hand; and the refusal arrives AFTER the side
// effect, against Spawn's doctrine ("anything that can be refused on the sole
// evidence of the configuration is refused BEFORE the first side effect").
//
// The check lives in ValidateWorktree, and nowhere else, for the same reason
// as the layout check: it's the sole source consulted by Validate() — hence
// by LoadGlobal and `den doctor` — AND by `den rm` (internal/cli/rm.go),
// which combines these two fields into the path it's about to CLEAN. A
// relative root would send `den rm` deleting in the wrong place, silently.
func TestValidateWorktreeRefusesARelativeRoot(t *testing.T) {
	g := validGlobal()
	g.WorktreeRoot = "wt-relative"

	errs := g.ValidateWorktree()
	if len(errs) == 0 {
		t.Fatal("relative worktree_root accepted: den would create the worktree under git's " +
			"current directory, i.e. inside the user's repo")
	}
	msg := errs[0].Error()
	// The field to fix AND the value received: without both, the user knows
	// something is wrong but not which of the two worktree fields carries it.
	if !strings.Contains(msg, "worktree_root") || !strings.Contains(msg, "wt-relative") {
		t.Errorf("message must name worktree_root and the value received; got: %s", msg)
	}
}

// The counterpart of the previous test: an ABSOLUTE root remains accepted.
// Without it, a check that refused everything — including the default
// computed by LoadGlobalUnvalidated — would pass the test above unnoticed,
// and den could no longer create any worktree.
func TestValidateWorktreeAcceptsAnAbsoluteRoot(t *testing.T) {
	g := validGlobal()
	g.WorktreeRoot = "/var/tmp/den worktrees"
	if errs := g.ValidateWorktree(); len(errs) != 0 {
		t.Errorf("absolute root refused: %v", errs)
	}
}

// A blank repos value would expand to a relative path and mount the wrong
// directory — same family of fault as a blank config_dir or worktree_root.
func TestValidateRepoKeyBlankPath(t *testing.T) {
	g := validGlobal()
	g.Repos = map[string]string{"api": "   "}
	errs := g.Validate()
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "repos.api") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a repos.api error, got: %v", errs)
	}
}

func TestValidateMounts(t *testing.T) {
	cases := []struct {
		name   string
		mounts []Mount
		want   string // substring the error must contain; "" means valid
	}{
		{"host required", []Mount{{Link: "$HOME/x"}}, "mounts[0].host: required"},
		{"host blank", []Mount{{Host: "   ", Link: "$HOME/x"}}, "mounts[0].host: required"},
		// A link with no host would be a link to nothing; a host with no link is
		// legitimate (env-var consumers), so only one direction is an error.
		{"link optional", []Mount{{Host: "/tmp/x"}}, ""},
		// Relative link: the VM expands it from an unspecified cwd, so it names
		// no stable location. Caught here rather than at boot, where the message
		// would arrive inside a microVM nobody is watching.
		{"link must be absolute or $HOME/~", []Mount{{Host: "/tmp/x", Link: "some/where"}},
			"mounts[0].link: must be absolute"},
		{"link $HOME ok", []Mount{{Host: "/tmp/x", Link: "$HOME/.ssh"}}, ""},
		{"link tilde ok", []Mount{{Host: "/tmp/x", Link: "~/.ssh"}}, ""},
		{"link absolute ok", []Mount{{Host: "/tmp/x", Link: "/etc/thing"}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := validGlobal()
			g.Mounts = tc.mounts
			err := errors.Join(g.Validate()...)
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("want valid, got %v", err)
			case tc.want != "" && err == nil:
				t.Fatalf("want error containing %q, got nil", tc.want)
			case tc.want != "" && !strings.Contains(err.Error(), tc.want):
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}
