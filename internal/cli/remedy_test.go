package cli

import (
	"strings"
	"testing"
)

// shellSplit undoes shellQuote: it splits a proposed command line into the
// argv a shell would hand den.
//
// It replaces strings.Fields as the replay mechanism, and it has to: Fields
// splits on the very space the quoting protects, so it would declare the
// CORRECTED remedy broken. It is written here, tested here, and used by every
// replay below — a wrong splitter makes all of them vacuous.
//
// Do NOT "simplify" this away by having remedyLine return a []string for the
// test to replay. The property under test is that THE STRING den prints is
// legal when a human types it; replaying an argv bypasses the join and reopens
// exactly this hole.
func shellSplit(t *testing.T, line string) []string {
	t.Helper()
	var out []string
	var cur strings.Builder
	inWord, inQuote := false, false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case inQuote && c == '\'':
			inQuote = false
		case inQuote:
			cur.WriteByte(c)
		// The '\'' idiom (close, escape, reopen) puts a literal quote OUTSIDE the
		// quoted region: a backslash there escapes the next byte rather than
		// starting a new word. Without this case the escaped quote at i+1 is read
		// as an unmatched open, and the scan runs off the end of the line looking
		// for a close that the next real quote already consumed — reproduced: the
		// "an inner single quote" case below faults "unterminated quote" without
		// it.
		case c == '\\' && i+1 < len(line):
			i++
			cur.WriteByte(line[i])
			inWord = true
		case c == '\'':
			inQuote, inWord = true, true
		case c == ' ':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	if inQuote {
		t.Fatalf("unterminated quote in %q", line)
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out
}

func TestShellSplitUndoesTheQuotingRule(t *testing.T) {
	for _, tc := range []struct {
		name string
		line string
		want []string
	}{
		{"plain words", "den exec api true", []string{"den", "exec", "api", "true"}},
		{"a quoted space", "den exec --workdir '/tmp/hot fix' api true",
			[]string{"den", "exec", "--workdir", "/tmp/hot fix", "api", "true"}},
		{"an empty word", "den exec '' true", []string{"den", "exec", "", "true"}},
		// The '\'' idiom: close the quote, an escaped quote, reopen.
		{"an inner single quote", `den exec 'it'\''s' true`,
			[]string{"den", "exec", "it's", "true"}},
		{"a glob character survives quoting", "den up --repo '/dev/proj-*' api",
			[]string{"den", "up", "--repo", "/dev/proj-*", "api"}},
		// Pins the escape case's placement AFTER the two inQuote cases: a
		// backslash inside an open quote must stay a plain byte (shellQuote
		// single-quotes any token containing one, per shellSafe), not trigger
		// the outside-quote escape and eat the following byte.
		{"a backslash inside a quoted token", `den exec '/tmp/a\b' true`,
			[]string{"den", "exec", `/tmp/a\b`, "true"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := shellSplit(t, tc.line)
			if len(got) != len(tc.want) {
				t.Fatalf("split = %q, want %q", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("split = %q, want %q", got, tc.want)
				}
			}
		})
	}
}
