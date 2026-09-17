package sbx

import (
	"fmt"
	"time"
)

// Relative renders an RFC 3339 timestamp as the time elapsed since it, the
// way `den ls` shows `last_used_at`: "-" when absent or unreadable, "<1m"
// under a minute, then whole minutes, hours (from 60 min) and days (from
// 24 h), truncated.
//
// time.RFC3339 as the layout, and NOT a fixed-width one: sbx writes the
// fractional second with as many digits as it has — 5, 6, or none at all on
// an older sandbox (measured 2026-09-16 on v0.43.0, three shapes in one
// listing) — and Go's parser accepts any of them behind that layout.
//
// A timestamp in the future renders "<1m", never a negative: the host clock
// and the daemon's can disagree by a few seconds, and a column is not the
// place to report it.
//
// The caller passes `now`: the real clock is injected through cli.Deps like
// every other system access, so the column is assertable in a test.
func Relative(now time.Time, rfc3339 string) string {
	if rfc3339 == "" {
		return "-"
	}
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return "-"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
}
