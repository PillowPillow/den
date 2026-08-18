package selfupdate

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Fetcher is the ONE seam between the update sequence and the network. Every
// test injects a double; the real implementation below is the only code in this
// package that speaks HTTP, kept as thin as ports.ListenScanner is, for the
// same reason: what cannot be tested hermetically must be the smallest possible
// surface, so everything around it stays tested.
type Fetcher interface {
	ResolveLatest(ctx context.Context) (string, error)
	Get(ctx context.Context, url string) ([]byte, error)
}

// HTTPFetcher is the real Fetcher. Untested by construction: any test here would
// reach github.com, which this suite forbids. The CI smoke job of ci.yml is
// what exercises it.
type HTTPFetcher struct {
	Client *http.Client
}

// NewHTTPFetcher builds the fetcher den ships with. The timeout is generous
// rather than tight: it covers the whole archive download on a slow link, and
// an update that hangs forever is worse than one that says the network failed.
func NewHTTPFetcher() HTTPFetcher {
	return HTTPFetcher{Client: &http.Client{Timeout: 5 * time.Minute}}
}

// ResolveLatest reads the tag out of the URL /releases/latest redirects to —
// the same trick install.sh uses, and for the same reason (no api.github.com
// rate limit, no JSON dependency).
func (f HTTPFetcher) ResolveLatest(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, LatestURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := f.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("cannot reach %s (%v) — check your network, or download an archive from %s",
			LatestURL, err, releasesBase)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s answered %s — download an archive from %s instead",
			LatestURL, resp.Status, releasesBase)
	}
	// resp.Request is the LAST request of the redirect chain, which is where
	// the tag is spelled.
	return TagFromURL(resp.Request.URL.String())
}

func (f HTTPFetcher) Get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := f.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot download %s (%v) — check your network, or download an archive from %s",
			url, err, releasesBase)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cannot download %s: %s — does this release ship your OS and architecture? see %s",
			url, resp.Status, releasesBase)
	}
	return io.ReadAll(resp.Body)
}

// client keeps a zero-value HTTPFetcher usable rather than panicking.
func (f HTTPFetcher) client() *http.Client {
	if f.Client == nil {
		return http.DefaultClient
	}
	return f.Client
}
