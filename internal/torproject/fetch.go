// Package torproject fetches the Tor Project's exit-list downloads.
//
// The endpoints are public and require no authentication, so there is no secret
// to redact. Out of fetch etiquette (the Tor Project asks clients not to poll
// excessively) every request carries a descriptive User-Agent, and callers are
// expected to cache the result rather than re-fetch per lookup.
package torproject

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// userAgent identifies this client to the Tor Project's servers.
const userAgent = "tor-exit-lookup (+https://github.com/nlink-jp/tor-exit-lookup)"

// Fetcher retrieves an exit-list body as a stream. Implementations must return a
// ReadCloser the caller is responsible for closing.
type Fetcher interface {
	Fetch(ctx context.Context, rawURL string) (io.ReadCloser, error)
}

// HTTPFetcher is the production Fetcher.
type HTTPFetcher struct {
	Client *http.Client
}

// NewHTTPFetcher returns a Fetcher with a sane default timeout.
func NewHTTPFetcher() *HTTPFetcher {
	return &HTTPFetcher{Client: &http.Client{Timeout: 2 * time.Minute}}
}

// Fetch performs the GET. On a non-200 response it returns an error including a
// short prefix of the body.
func (f *HTTPFetcher) Fetch(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", rawURL, err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		return nil, fmt.Errorf("download %s: HTTP %d: %s", rawURL, resp.StatusCode, trimBody(body))
	}
	return resp.Body, nil
}

func trimBody(b []byte) string {
	s := string(b)
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
