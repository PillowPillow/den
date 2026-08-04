package cli

import "runtime/debug"

// resolveVersion arbitrates between the two ways a binary can know its version.
// The ldflags stamp (Makefile, goreleaser) wins whenever it ran: `git describe`
// distinguishes a dirty checkout from a tag, which module build info never
// does. Build info is the rescue for `go install …/cmd/den@vX`, the one
// install path that bypasses the Makefile yet still carries the tag — without
// this fallback that binary answers "den dev" and a bug report against it
// names no code. "(devel)" is build info's own placeholder for an untagged
// local build; substituting it for "dev" would trade the documented tell for
// an undocumented one, so it is treated as no answer.
func resolveVersion(ldflags, buildinfo string) string {
	if ldflags != "" && ldflags != "dev" {
		return ldflags
	}
	if buildinfo != "" && buildinfo != "(devel)" {
		return buildinfo
	}
	return "dev"
}

// displayVersion is the impure twin: it feeds resolveVersion the process's own
// build info. Kept to two lines so the arbitration above stays fully testable
// without faking debug.ReadBuildInfo.
func displayVersion() string {
	buildinfo := ""
	if info, ok := debug.ReadBuildInfo(); ok {
		buildinfo = info.Main.Version
	}
	return resolveVersion(Version, buildinfo)
}
