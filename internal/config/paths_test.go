package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHomePriority(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("the flag wins over everything", func(t *testing.T) {
		t.Setenv("DEN_HOME", "/from/env")
		got, err := Home("/from/flag")
		if err != nil {
			t.Fatal(err)
		}
		if got != "/from/flag" {
			t.Errorf("Home = %q, want %q", got, "/from/flag")
		}
	})

	t.Run("otherwise DEN_HOME", func(t *testing.T) {
		t.Setenv("DEN_HOME", "/from/env")
		got, err := Home("")
		if err != nil {
			t.Fatal(err)
		}
		if got != "/from/env" {
			t.Errorf("Home = %q, want %q", got, "/from/env")
		}
	})

	// Paths coming out of Home() go to `git worktree` and `sbx create` in the
	// next step, with a cwd that's no longer guaranteed: they must be
	// absolute as soon as they're resolved, never later.
	t.Run("a relative flag is made absolute", func(t *testing.T) {
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		got, err := Home("denh")
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(cwd, "denh"); got != want {
			t.Errorf("Home = %q, want %q", got, want)
		}
	})

	t.Run("a relative DEN_HOME is made absolute", func(t *testing.T) {
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("DEN_HOME", "denh")
		got, err := Home("")
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(cwd, "denh"); got != want {
			t.Errorf("Home = %q, want %q", got, want)
		}
	})

	t.Run("otherwise ~/.den", func(t *testing.T) {
		t.Setenv("DEN_HOME", "")
		got, err := Home("")
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(home, ".den")
		if got != want {
			t.Errorf("Home = %q, want %q", got, want)
		}
	})
}

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"tilde alone", "~", home},
		{"tilde at start", "~/dev/projet", filepath.Join(home, "dev/projet")},
		{"absolute path unchanged", "/opt/truc", "/opt/truc"},
		{"relative path unchanged", "dev/projet", "dev/projet"},
		{"empty unchanged", "", ""},
		// $HOME targets the VM's home: den must never resolve it host-side.
		{"dollar HOME preserved", "$HOME/.local/bin", "$HOME/.local/bin"},
		// a tilde that isn't at the start isn't a home reference.
		{"tilde in the middle preserved", "/opt/~/truc", "/opt/~/truc"},
		{"unsupported user tilde", "~bob/x", "~bob/x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ExpandPath(c.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("ExpandPath(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// $HOME missing from the environment: den must say WHAT it was looking for
// and HOW to work around it. Found by exercising the assembled binary.
//
// ACTUAL output before the fix, `env -u HOME den nest ls`:
//
//	den: $HOME is not defined
//
// The message comes verbatim from os.UserHomeDir: no context whatsoever — it
// says neither that den was looking for the config directory, nor that
// --den-home and DEN_HOME exist and fix the problem on the spot. This happens
// for real under systemd, in a container, or in a cron job.
func TestHomeWithoutHOMESaysWhatItWasLookingForAndHowToWorkAroundIt(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("DEN_HOME", "")

	_, err := Home("")
	if err == nil {
		t.Skip("os.UserHomeDir resolved a home despite an empty HOME: case not exercisable here")
	}
	msg := err.Error()
	for _, want := range []string{"~/.den", "DEN_HOME", "--den-home"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message must mention %q — it's the fallback; got: %s",
				want, msg)
		}
	}
	// The original cause remains inspectable: the message rephrases it, it
	// doesn't replace it.
	if !strings.Contains(msg, "HOME") {
		t.Errorf("the original cause must survive in the message; got: %s", msg)
	}
}

// Same requirement for ExpandPath, the other entry point to the home: it
// expands the "~" in config_dir, ssh.dir and repos. Without context, the user
// reads "$HOME is not defined" without knowing which config field carries the
// offending tilde.
func TestExpandPathWithoutHOMENamesTheOffendingPath(t *testing.T) {
	t.Setenv("HOME", "")

	_, err := ExpandPath("~/.den/agents/claude")
	if err == nil {
		t.Skip("os.UserHomeDir resolved a home despite an empty HOME: case not exercisable here")
	}
	if !strings.Contains(err.Error(), "~/.den/agents/claude") {
		t.Errorf("message must cite the path to expand; got: %v", err)
	}
	if !strings.Contains(err.Error(), "HOME") {
		t.Errorf("the original cause must survive; got: %v", err)
	}
}
