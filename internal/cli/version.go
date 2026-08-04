package cli

import "runtime/debug"

// resolveVersion arbitrates between the two ways a binary can know its version.
// The ldflags stamp (Taskfile, goreleaser) wins whenever it ran: `git describe`
// distinguishes a dirty checkout from a tag, which module build info never
// does. Build info is the rescue for `go install …/cmd/den@vX`, the one
// install path that bypasses `task build` yet still carries the tag — without
// this fallback that binary answers "den dev" and a bug report against it
// names no code.
//
// fromLocalVCS is what keeps that rescue from swallowing the case it was never
// meant to cover. Since Go 1.24 the toolchain stamps Main.Version from VCS
// state, so a plain `go build` in a checkout no longer answers the "(devel)"
// placeholder — it answers a pseudo-version like
// v1.1.1-0.20260804111234-a28f04a21c08+dirty, which is not a version anybody
// can check out and which the old arbitration returned as though it were.
// Probed on Go 1.26.1 (2026-08-04): a build from a local checkout always
// carries a vcs.revision setting, a build from a downloaded module never does,
// and `go install …@v1.0.1` answers a bare "v1.0.1" with no vcs settings at
// all. Keying on WHERE the build came from is therefore exact, where matching
// the shape of a pseudo-version would only be a guess about a format Go has
// already changed once.
//
// The ldflags check stays first on purpose: `task build` also runs inside a
// checkout, so fromLocalVCS is true for the one documented way to build.
func resolveVersion(ldflags, buildinfo string, fromLocalVCS bool) string {
	if ldflags != "" && ldflags != "dev" {
		return ldflags
	}
	if !fromLocalVCS && buildinfo != "" && buildinfo != "(devel)" {
		return buildinfo
	}
	return "dev"
}

// displayVersion is the impure twin: it feeds resolveVersion the process's own
// build info. It stays a thin reader — finding vcs.revision and copying
// Main.Version, nothing else — so the arbitration above remains fully testable
// without faking debug.ReadBuildInfo.
func displayVersion() string {
	buildinfo := ""
	fromLocalVCS := false
	if info, ok := debug.ReadBuildInfo(); ok {
		buildinfo = info.Main.Version
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				fromLocalVCS = true
				break
			}
		}
	}
	return resolveVersion(Version, buildinfo, fromLocalVCS)
}
