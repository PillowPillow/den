package cli

import "testing"

// The Makefile is "the ONLY documented way to build" precisely because a plain
// `go build` leaves Version at "dev". But `go install …/cmd/den@v1.0.0` — the
// reflex of every Go developer — bypasses the Makefile AND carries the tag in
// the module build info. resolveVersion is the arbitration that rescues that
// path: ldflags first (the Makefile and goreleaser both stamp it), build info
// second, "dev" only when neither knows better.

func TestResolveVersionPrefersLdflagsStamp(t *testing.T) {
	// When the Makefile or goreleaser stamped a version, build info must not
	// override it: `git describe` on a dirty checkout says `v1.0.0-3-gabc-dirty`
	// while build info would still answer a clean-looking `(devel)` or a stale
	// module version — the stamp is the more honest of the two.
	got := resolveVersion("v1.0.0-3-gabc1234-dirty", "v1.0.0", false)
	if got != "v1.0.0-3-gabc1234-dirty" {
		t.Fatalf("resolveVersion ignored the ldflags stamp: %q", got)
	}
}

func TestResolveVersionFallsBackToBuildInfoOnGoInstall(t *testing.T) {
	// The `go install @v1.0.0` binary: ldflags never ran (Version still "dev"),
	// but the module system recorded the tag. This is the exact case the
	// fallback exists for — without it the binary answers "den dev" and a bug
	// report against it names no code.
	got := resolveVersion("dev", "v1.0.0", false)
	if got != "v1.0.0" {
		t.Fatalf("resolveVersion did not rescue the go-install path: %q", got)
	}
}

func TestResolveVersionKeepsDevWhenBuildInfoIsDevel(t *testing.T) {
	// A plain `go build` in a checkout: ldflags never ran AND build info
	// answers the literal "(devel)". Substituting "(devel)" for "dev" would
	// swap one non-answer for an uglier one — keep "dev", the documented
	// tell that the binary skipped `make build`.
	got := resolveVersion("dev", "(devel)", false)
	if got != "dev" {
		t.Fatalf("resolveVersion leaked build info's placeholder: %q", got)
	}
}

func TestResolveVersionKeepsDevWhenBuildInfoIsEmpty(t *testing.T) {
	// Stripped binaries or exotic toolchains can yield no build info at all;
	// the zero value must degrade to the same "dev" as always, never to an
	// empty string that would print "den " with nothing after it.
	got := resolveVersion("dev", "", false)
	if got != "dev" {
		t.Fatalf("resolveVersion lost the dev default: %q", got)
	}
}

// THE regression: Go 1.24+ stamps Main.Version from VCS, so a plain `go build`
// in a checkout no longer answers "(devel)" — it answers a pseudo-version, which
// the old two-argument arbitration happily returned. den then named a version
// nobody can check out. Probed on Go 1.26.1, 2026-08-04.
func TestResolveVersionKeepsDevForALocalVCSBuild(t *testing.T) {
	got := resolveVersion("dev", "v1.1.1-0.20260804111234-a28f04a21c08+dirty", true)
	if got != "dev" {
		t.Fatalf("a plain `go build` must stay the documented dev tell, got %q", got)
	}
}

// A local checkout that happens to sit on a tag is still a build that skipped
// the runner. The rule keys on WHERE the build came from, not on whether the
// string it carries looks respectable.
func TestResolveVersionKeepsDevForALocalVCSBuildEvenOnACleanTag(t *testing.T) {
	got := resolveVersion("dev", "v1.0.1", true)
	if got != "dev" {
		t.Fatalf("a local build must not borrow a tag it did not stamp, got %q", got)
	}
}

// The case that must NOT regress: `task build` runs inside a checkout, so
// fromLocalVCS is true there too. The ldflags stamp still has to win — reading
// the new flag before the stamp would break the one documented way to build.
func TestResolveVersionPrefersLdflagsEvenFromALocalVCSBuild(t *testing.T) {
	got := resolveVersion("v1.0.1-5-g364136e-dirty", "v1.1.1-0.20260804111234-a28f04a21c08+dirty", true)
	if got != "v1.0.1-5-g364136e-dirty" {
		t.Fatalf("the ldflags stamp lost to the VCS pseudo-version: %q", got)
	}
}
