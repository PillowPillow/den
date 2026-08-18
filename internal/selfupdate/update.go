package selfupdate

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
)

// Request is everything Run needs from the outside world. GOOS/GOARCH are
// parameters rather than runtime constants so a test can exercise a platform it
// is not running on — the same reason runtime.GOOS is named at the wiring site
// for `den exec`.
type Request struct {
	ExecPath string
	Current  string
	GOOS     string
	GOARCH   string
	Env      Env
	// Resolve resolves a candidate directory's symlinks for Classify, and is
	// injected here rather than called there for the reason GOOS/GOARCH are
	// parameters: internal/selfupdate's tests must not depend on the machine's
	// filesystem. The CLI wires filepath.EvalSymlinks; a nil Resolve compares
	// directories lexically.
	Resolve func(string) (string, error)
}

// Run is the §5 sequence, and its ORDER is the contract: everything refusable
// is refused before the first BYTE IS DOWNLOADED, so a refusal never leaves a
// half-updated binary or a stray staging file. The tests assert exactly that —
// a refusal that came after a download is a bug even if its message is right.
//
// "Before the first download", not "before the first request": ResolveLatest is
// one redirect, and the up-to-date check needs its answer. ProbeWritable sits
// BELOW it on purpose. Above it, an already-current den in a directory it
// cannot write — /usr/local/bin, a read-only mount — failed with "cannot write
// to …" and a non-zero exit instead of the §5 step 3 "already the latest
// release" and exit 0, which breaks any provisioning script that runs
// `den update` idempotently. Refusing to write is only news when den has
// something to write.
func Run(ctx context.Context, f Fetcher, req Request, out io.Writer) error {
	if err := MethodRefusal(Classify(req.ExecPath, req.Env, req.Resolve), req.ExecPath); err != nil {
		return err
	}
	if !IsUpdatableVersion(req.Current) {
		return &VersionError{Observed: req.Current}
	}

	latest, err := f.ResolveLatest(ctx)
	if err != nil {
		return err
	}
	// Two different facts, and one sentence used to state both wrongly:
	// NeedsUpdate is false when the versions are EQUAL and when the local one
	// is AHEAD. Telling someone running v1.9.0 that it "is already the latest
	// release" while the channel serves v1.8.1 hides the only interesting part,
	// which is that the release channel moved backwards under them.
	if IsAhead(req.Current, latest) {
		fmt.Fprintf(out, "den %s is ahead of the latest release %s — nothing to do\n", req.Current, latest)
		return nil
	}
	if !NeedsUpdate(req.Current, latest) {
		fmt.Fprintf(out, "den %s is already the latest release\n", req.Current)
		return nil
	}
	if err := ProbeWritable(filepath.Dir(req.ExecPath)); err != nil {
		return err
	}

	name := ArchiveName(latest, req.GOOS, req.GOARCH)
	archive, err := f.Get(ctx, DownloadURL(latest, name))
	if err != nil {
		return err
	}
	checksums, err := f.Get(ctx, DownloadURL(latest, "checksums.txt"))
	if err != nil {
		return err
	}
	expected, err := ExpectedSum(checksums, name)
	if err != nil {
		return err
	}
	if err := VerifySum(archive, expected); err != nil {
		return err
	}
	binary, err := ExtractBinary(archive)
	if err != nil {
		return err
	}
	if err := SwapBinary(req.ExecPath, binary); err != nil {
		return err
	}
	fmt.Fprintf(out, "den %s → %s (%s)\n", req.Current, latest, req.ExecPath)
	return nil
}
