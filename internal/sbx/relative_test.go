package sbx

import (
	"testing"
	"time"
)

func TestRelative(t *testing.T) {
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	cases := map[string]string{
		"":                            "-",
		"not a timestamp":             "-",
		"2026-09-16T11:59:30Z":        "<1m",
		"2026-09-16T11:55:00Z":        "5m",
		"2026-09-16T09:00:00Z":        "3h",
		"2026-09-04T12:00:00Z":        "12d",
		"2026-09-16T12:00:30Z":        "<1m", // host clock ahead of the daemon's: never negative
		"2026-09-16T11:54:16.62029Z":  "5m",  // 5 fractional digits (measured)
		"2026-09-16T11:54:16.154909Z": "5m",  // 6 fractional digits (measured)
		// No fractional part at all — the third shape sbx v0.43.0 writes
		// (measured 2026-09-16; two real sandboxes on this machine carry it).
		"2026-08-25T12:00:00Z": "22d",
	}
	for in, want := range cases {
		if got := Relative(now, in); got != want {
			t.Errorf("Relative(%q) = %q, want %q", in, got, want)
		}
	}
}
