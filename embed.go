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
