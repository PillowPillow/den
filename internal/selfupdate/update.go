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
}

// Run is the §5 sequence, and its ORDER is the contract: everything refusable
// is refused before the first byte is downloaded, so a refusal never leaves a
// half-updated binary or a stray staging file. The tests assert exactly that —
// a refusal that came after a request is a bug even if its message is right.
func Run(ctx context.Context, f Fetcher, req Request, out io.Writer) error {
	if err := MethodRefusal(Classify(req.ExecPath, req.Env), req.ExecPath); err != nil {
		return err
	}
	if !IsUpdatableVersion(req.Current) {
		return &VersionError{Observed: req.Current}
	}
	if err := ProbeWritable(filepath.Dir(req.ExecPath)); err != nil {
		return err
	}

	latest, err := f.ResolveLatest(ctx)
	if err != nil {
		return err
	}
	if !NeedsUpdate(req.Current, latest) {
		fmt.Fprintf(out, "den %s is already the latest release\n", req.Current)
		return nil
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
