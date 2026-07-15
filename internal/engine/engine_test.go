package engine

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nlink-jp/tor-exit-lookup/internal/config"
)

const (
	sampleList = "1.2.3.4\n5.6.7.8\n2001:db8::1\n"

	sampleExitAddresses = `ExitNode AAAA1111BBBB2222CCCC3333DDDD4444EEEE5555
Published 2026-07-14 10:00:00
LastStatus 2026-07-15 02:00:00
ExitAddress 1.2.3.4 2026-07-15 02:05:00
`
)

// fakeFetcher routes by URL so a single Update can fetch both sources. A url
// substring of "exit-addresses" returns the metadata body; anything else
// returns the bulk list. An error is returned for any URL matching failURL.
type fakeFetcher struct {
	bulk    string
	meta    string
	failURL string
}

func (f fakeFetcher) Fetch(_ context.Context, url string) (io.ReadCloser, error) {
	if f.failURL != "" && strings.Contains(url, f.failURL) {
		return nil, errors.New("fetch failed: " + url)
	}
	if strings.Contains(url, "exit-addresses") {
		return io.NopCloser(bytes.NewReader([]byte(f.meta))), nil
	}
	return io.NopCloser(bytes.NewReader([]byte(f.bulk))), nil
}

func newEngine(t *testing.T, f fakeFetcher) *Engine {
	t.Helper()
	cfg := &config.Config{
		BulkURL:          "https://example.test/torbulkexitlist",
		ExitAddressesURL: "https://example.test/exit-addresses",
		StorePath:        filepath.Join(t.TempDir(), "exitlist.json"),
		TTL:              time.Hour,
		AutoUpdate:       true,
	}
	e := New(cfg, f)
	e.Now = func() time.Time { return time.Unix(1720000000, 0) }
	return e
}

func TestUpdateThenLookup(t *testing.T) {
	e := newEngine(t, fakeFetcher{bulk: sampleList, meta: sampleExitAddresses})

	res, err := e.Update(context.Background())
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if res.Count != 3 || res.V4Count != 2 || res.V6Count != 1 {
		t.Fatalf("result = %+v", res)
	}
	if res.MetaCount != 1 || res.MetaWarning != "" {
		t.Fatalf("meta result = %+v", res)
	}

	set, err := e.LoadList()
	if err != nil {
		t.Fatalf("LoadList: %v", err)
	}
	r, err := Lookup(set, "1.2.3.4")
	if err != nil || !r.IsExit || !r.HasMeta || r.Meta.Fingerprint == "" {
		t.Errorf("Lookup(1.2.3.4) = %+v, %v", r, err)
	}
	r2, err := Lookup(set, "9.9.9.9")
	if err != nil || r2.IsExit {
		t.Errorf("Lookup(9.9.9.9) = %+v, %v; want not-exit", r2, err)
	}
	// A membership hit without metadata is still a hit.
	r3, _ := Lookup(set, "5.6.7.8")
	if !r3.IsExit || r3.HasMeta {
		t.Errorf("Lookup(5.6.7.8) = %+v; want exit without meta", r3)
	}
}

func TestUpdateMetadataSoftFail(t *testing.T) {
	// The metadata source is down; membership must still be written.
	e := newEngine(t, fakeFetcher{bulk: sampleList, failURL: "exit-addresses"})
	res, err := e.Update(context.Background())
	if err != nil {
		t.Fatalf("Update should succeed despite metadata failure: %v", err)
	}
	if res.Count != 3 {
		t.Errorf("Count = %d, want 3", res.Count)
	}
	if res.MetaWarning == "" {
		t.Error("expected a MetaWarning when exit-addresses fails")
	}
	set, _ := e.LoadList()
	if r, _ := Lookup(set, "1.2.3.4"); !r.IsExit {
		t.Error("membership lost when metadata failed")
	}
}

func TestUpdateBulkFetchError(t *testing.T) {
	e := newEngine(t, fakeFetcher{failURL: "torbulkexitlist"})
	if _, err := e.Update(context.Background()); err == nil {
		t.Error("Update should fail when the bulk source is down")
	}
}

func TestLookupInvalidIP(t *testing.T) {
	e := newEngine(t, fakeFetcher{bulk: sampleList})
	if _, err := e.Update(context.Background()); err != nil {
		t.Fatal(err)
	}
	set, _ := e.LoadList()
	if _, err := Lookup(set, "not-an-ip"); !errors.Is(err, ErrInvalidIP) {
		t.Errorf("err = %v, want ErrInvalidIP", err)
	}
}

func TestLoadListMissing(t *testing.T) {
	e := newEngine(t, fakeFetcher{})
	if _, err := e.LoadList(); !errors.Is(err, ErrNoList) {
		t.Errorf("err = %v, want ErrNoList", err)
	}
}

func TestEnsureFreshFetchesWhenMissing(t *testing.T) {
	e := newEngine(t, fakeFetcher{bulk: sampleList, meta: sampleExitAddresses})
	set, refreshed, err := e.EnsureFresh(context.Background(), time.Hour)
	if err != nil || !refreshed || set == nil {
		t.Fatalf("EnsureFresh = %v, refreshed=%v, err=%v", set, refreshed, err)
	}
	if set.Len() != 3 {
		t.Errorf("Len = %d, want 3", set.Len())
	}
}

func TestEnsureFreshSkipsWhenFresh(t *testing.T) {
	e := newEngine(t, fakeFetcher{bulk: sampleList})
	if _, err := e.Update(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Clock unchanged → the just-written list is fresh; no refetch.
	_, refreshed, err := e.EnsureFresh(context.Background(), time.Hour)
	if err != nil || refreshed {
		t.Errorf("EnsureFresh should not refetch a fresh list (refreshed=%v, err=%v)", refreshed, err)
	}
}

func TestEnsureFreshFallsBackOnFetchError(t *testing.T) {
	// Seed a store, then make the network fail and advance the clock past TTL.
	e := newEngine(t, fakeFetcher{bulk: sampleList})
	if _, err := e.Update(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.Fetcher = fakeFetcher{failURL: "torbulkexitlist"}
	e.Now = func() time.Time { return time.Unix(1720000000, 0).Add(2 * time.Hour) }

	set, refreshed, err := e.EnsureFresh(context.Background(), time.Hour)
	if err == nil {
		t.Error("expected a refetch error to be surfaced")
	}
	if refreshed {
		t.Error("refreshed should be false when the refetch failed")
	}
	if set == nil || set.Len() != 3 {
		t.Errorf("stale set should be returned as a fallback, got %v", set)
	}
}

func TestIsStale(t *testing.T) {
	e := newEngine(t, fakeFetcher{}) // clock at 1720000000
	fresh := time.Unix(1720000000, 0).Add(-1 * time.Hour)
	if stale, _ := e.IsStale(fresh); stale {
		t.Error("1h old should not be stale")
	}
	old := time.Unix(1720000000, 0).Add(-2 * 24 * time.Hour)
	if stale, age := e.IsStale(old); !stale || age < StaleAfter {
		t.Errorf("2d old should be stale (age=%v)", age)
	}
}
