// Package den holds the one thing that must live at the module root: the
// embedded example den home. go:embed cannot climb above its own package
// directory with "..", so an embed living under internal/… could only reach
// examples/den-home by keeping a second copy there — exactly the "two copies
// synchronized by hand" the "sole definition" comments elsewhere in this repo
// forbid (config.GlobalPath, agent's mixinDir/mixinPath). Deleting
// examples/den-home instead was rejected too: it is the copy a human reads,
// and internal/config/example_test.go loads it directly. So: one copy, one
// embed, and the embed is what has to move — to the only directory that sits
// above internal/ and still belongs to this module.
package den

import "embed"

// ExampleDenHome is the example den home shipped in examples/den-home,
// embedded into the binary so `den init` works from a release tarball, a
// Homebrew cask, or `go install` — none of which give the user a checkout to
// `cp -R` from.
//
//go:embed examples/den-home
var ExampleDenHome embed.FS

// SourceAwareDenHome is the home `den init --source <url>` writes: personal
// settings only, no placeholder nest and no local stack.
//
// A SECOND example directory rather than a filter over the first: the two
// homes differ by what they must NOT contain, and a filter would have to
// encode that absence in Go — where nothing tells a reader why
// `nests/example.yaml` is skipped, and nothing stops a later file added to
// examples/den-home from silently reaching a source-aware home. Two
// directories make each home a thing a human can read whole, which is the
// same reason examples/den-home exists on disk at all (see above).
//
// Its config.yaml carries no `defaults.stack`: every nest it will resolve
// comes from the installed source and declares its own stack. That is the
// key config.Validate stopped requiring for this file to be legal.
//
//go:embed examples/den-home-source
var SourceAwareDenHome embed.FS
