// Package engine ties configuration, downloading, and the on-disk exit-list
// store together. Both the CLI and the MCP server drive the same Engine so their
// behaviour cannot diverge.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"time"

	"github.com/nlink-jp/tor-exit-lookup/internal/config"
	"github.com/nlink-jp/tor-exit-lookup/internal/exitlist"
	"github.com/nlink-jp/tor-exit-lookup/internal/torproject"
)

// StaleAfter is the age past which the cached exit list is reported as stale by
// `status` (independent of the auto-refetch TTL). The Tor Project refreshes the
// list roughly every 30 minutes, so a day-old copy is meaningfully out of date.
const StaleAfter = 24 * time.Hour

// Errors surfaced to callers for friendly handling.
var (
	// ErrNoList means no exit list has been downloaded yet; the caller should
	// suggest `update`.
	ErrNoList = errors.New("no local exit list")
	// ErrInvalidIP means the queried string is not a valid IP address.
	ErrInvalidIP = errors.New("invalid IP address")
)

// Engine performs load, update, and membership operations against the configured
// exit-list store.
type Engine struct {
	Cfg     *config.Config
	Fetcher torproject.Fetcher
	Now     func() time.Time // injectable clock; defaults to time.Now
}

// New returns an Engine with the given config and fetcher.
func New(cfg *config.Config, fetcher torproject.Fetcher) *Engine {
	return &Engine{Cfg: cfg, Fetcher: fetcher, Now: time.Now}
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// LoadList reads and opens the local exit-list store. It returns ErrNoList
// (wrapped) when the file does not exist.
func (e *Engine) LoadList() (*exitlist.Set, error) {
	data, err := os.ReadFile(e.Cfg.StorePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w at %s", ErrNoList, e.Cfg.StorePath)
		}
		return nil, err
	}
	return exitlist.Open(data)
}

// UpdateResult reports what an Update produced.
type UpdateResult struct {
	Count       int
	V4Count     int
	V6Count     int
	Skipped     int
	MetaCount   int
	MetaSkipped int
	MetaWarning string // non-empty when the metadata source could not be fetched
	Generated   time.Time
}

// Update downloads the torbulkexitlist (membership) and, when configured, the
// exit-addresses feed (metadata), then atomically replaces the local store. A
// metadata-source failure is soft: membership is still written and the warning
// is surfaced in the result, because the tool's core answer does not depend on
// metadata.
func (e *Engine) Update(ctx context.Context) (UpdateResult, error) {
	body, err := e.Fetcher.Fetch(ctx, e.Cfg.BulkURL)
	if err != nil {
		return UpdateResult{}, err
	}
	addrs, skipped, err := exitlist.ParseBulkList(body)
	body.Close()
	if err != nil {
		return UpdateResult{}, err
	}

	sources := exitlist.Sources{Bulk: e.Cfg.BulkURL}
	var meta map[netip.Addr]exitlist.Meta
	var metaSkipped int
	var metaWarning string
	if e.Cfg.ExitAddressesURL != "" {
		m, ms, merr := e.fetchMeta(ctx)
		if merr != nil {
			metaWarning = merr.Error() // soft-fail: keep membership, note the miss
		} else {
			meta, metaSkipped = m, ms
			sources.ExitAddresses = e.Cfg.ExitAddressesURL
		}
	}

	generatedUnix := e.now().Unix()
	if err := e.writeStore(addrs, meta, sources, generatedUnix); err != nil {
		return UpdateResult{}, err
	}

	set := exitlist.New(addrs, meta, time.Unix(generatedUnix, 0).UTC(), sources)
	v4, v6 := set.FamilyCounts()
	return UpdateResult{
		Count:       set.Len(),
		V4Count:     v4,
		V6Count:     v6,
		Skipped:     skipped,
		MetaCount:   set.MetaCount(),
		MetaSkipped: metaSkipped,
		MetaWarning: metaWarning,
		Generated:   set.Generated(),
	}, nil
}

func (e *Engine) fetchMeta(ctx context.Context) (map[netip.Addr]exitlist.Meta, int, error) {
	body, err := e.Fetcher.Fetch(ctx, e.Cfg.ExitAddressesURL)
	if err != nil {
		return nil, 0, err
	}
	defer body.Close()
	return exitlist.ParseExitAddresses(body)
}

// writeStore serializes addrs+meta to the store path via temp + rename so a
// crash mid-write never leaves a truncated store to be read back.
func (e *Engine) writeStore(addrs []netip.Addr, meta map[netip.Addr]exitlist.Meta, sources exitlist.Sources, generatedUnix int64) error {
	dir := filepath.Dir(e.Cfg.StorePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "exitlist-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	err = exitlist.Serialize(tmp, addrs, meta, sources, generatedUnix)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(tmpName, e.Cfg.StorePath); err != nil {
		return fmt.Errorf("install store: %w", err)
	}
	return nil
}

// EnsureFresh returns a usable Set, refetching first when the cached list is
// missing or older than ttl. A refetch failure is non-fatal when a cached list
// already exists: the stale Set is returned alongside the error so the caller
// can warn and continue offline. Only a total absence of data (no cache AND the
// fetch failed) is a hard error.
func (e *Engine) EnsureFresh(ctx context.Context, ttl time.Duration) (set *exitlist.Set, refreshed bool, err error) {
	set, loadErr := e.LoadList()
	switch {
	case loadErr == nil:
		if e.now().Sub(set.Generated()) <= ttl {
			return set, false, nil // fresh enough
		}
	case errors.Is(loadErr, ErrNoList):
		set = nil // must fetch
	default:
		return nil, false, loadErr
	}

	if _, uerr := e.Update(ctx); uerr != nil {
		return set, false, uerr // set may be a stale fallback, or nil
	}
	fresh, lerr := e.LoadList()
	if lerr != nil {
		return nil, false, lerr
	}
	return fresh, true, nil
}

// Result is the outcome of a membership Lookup.
type Result struct {
	IsExit  bool
	Meta    exitlist.Meta
	HasMeta bool
}

// Lookup reports whether ip is a Tor Exit node using the already-loaded set,
// attaching metadata when available. An unparseable input returns ErrInvalidIP
// (wrapped).
func Lookup(set *exitlist.Set, ip string) (Result, error) {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return Result{}, fmt.Errorf("%w %q", ErrInvalidIP, ip)
	}
	r := Result{IsExit: set.Contains(addr)}
	if m, ok := set.Meta(addr); ok {
		r.Meta, r.HasMeta = m, true
	}
	return r, nil
}

// IsStale reports whether a list generated at gen is older than StaleAfter
// relative to the engine's clock, and the age.
func (e *Engine) IsStale(gen time.Time) (bool, time.Duration) {
	age := e.now().Sub(gen)
	return age > StaleAfter, age
}
