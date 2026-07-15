package app

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
	"github.com/nlink-jp/tor-exit-lookup/internal/engine"
)

const (
	sampleBulk = "1.2.3.4\n5.6.7.8\n2001:db8::1\n"

	sampleMeta = `ExitNode AAAA1111BBBB2222CCCC3333DDDD4444EEEE5555
Published 2026-07-14 10:00:00
LastStatus 2026-07-15 02:00:00
ExitAddress 1.2.3.4 2026-07-15 02:05:00
`
)

type fakeFetcher struct {
	bulk    string
	meta    string
	failURL string
}

func (f fakeFetcher) Fetch(_ context.Context, url string) (io.ReadCloser, error) {
	if f.failURL != "" && strings.Contains(url, f.failURL) {
		return nil, errors.New("fetch failed")
	}
	if strings.Contains(url, "exit-addresses") {
		return io.NopCloser(strings.NewReader(f.meta)), nil
	}
	return io.NopCloser(strings.NewReader(f.bulk)), nil
}

// seededEngine returns an engine whose store is populated from the samples, with
// a fixed clock and auto-update off (so tests are deterministic and offline).
func seededEngine(t *testing.T) *engine.Engine {
	t.Helper()
	cfg := &config.Config{
		BulkURL:          "https://example.test/torbulkexitlist",
		ExitAddressesURL: "https://example.test/exit-addresses",
		StorePath:        filepath.Join(t.TempDir(), "exitlist.json"),
		TTL:              time.Hour,
		AutoUpdate:       false,
	}
	e := engine.New(cfg, fakeFetcher{bulk: sampleBulk, meta: sampleMeta})
	e.Now = func() time.Time { return time.Unix(1720000000, 0) }
	if _, err := e.Update(context.Background()); err != nil {
		t.Fatalf("seed Update: %v", err)
	}
	return e
}

func TestRunCheckTriStateExitCodes(t *testing.T) {
	e := seededEngine(t)
	cases := []struct {
		ip   string
		want int
	}{
		{"1.2.3.4", exitIsExit},
		{"9.9.9.9", exitNotExit},
		{"not-an-ip", exitError},
	}
	for _, tc := range cases {
		var out, errw bytes.Buffer
		got := runCheck(context.Background(), &out, &errw, strings.NewReader(""), e, false, true, []string{tc.ip})
		if got != tc.want {
			t.Errorf("runCheck(%s) = %d, want %d (out=%q err=%q)", tc.ip, got, tc.want, out.String(), errw.String())
		}
	}
}

func TestRunCheckSingleShowsMetadata(t *testing.T) {
	e := seededEngine(t)
	var out, errw bytes.Buffer
	runCheck(context.Background(), &out, &errw, strings.NewReader(""), e, false, true, []string{"1.2.3.4"})
	if !strings.Contains(out.String(), "is a Tor Exit node") || !strings.Contains(out.String(), "AAAA1111") {
		t.Errorf("expected metadata in text output, got %q", out.String())
	}
}

func TestRunCheckBatchStdin(t *testing.T) {
	e := seededEngine(t)
	var out, errw bytes.Buffer
	stdin := strings.NewReader("1.2.3.4\n9.9.9.9\nbad-ip\n")
	// Batch mode (stdin, no positional args): exits 0 regardless of individual results.
	if got := runCheck(context.Background(), &out, &errw, stdin, e, false, true, nil); got != 0 {
		t.Errorf("batch exit = %d, want 0", got)
	}
	s := out.String()
	if !strings.Contains(s, "1.2.3.4 is a Tor Exit node") ||
		!strings.Contains(s, "9.9.9.9 is not a Tor Exit node") ||
		!strings.Contains(s, "bad-ip: invalid address") {
		t.Errorf("batch output missing expected lines:\n%s", s)
	}
}

func TestRunCheckJSON(t *testing.T) {
	e := seededEngine(t)
	var out, errw bytes.Buffer
	// --json forces batch semantics even for a single IP: exit 0.
	if got := runCheck(context.Background(), &out, &errw, strings.NewReader(""), e, true, true, []string{"1.2.3.4"}); got != 0 {
		t.Errorf("json single exit = %d, want 0", got)
	}
	s := out.String()
	if !strings.Contains(s, `"is_exit":true`) || !strings.Contains(s, `"fingerprint":"AAAA1111`) {
		t.Errorf("json output = %q", s)
	}
}

func TestRunCheckAutoUpdateWhenMissing(t *testing.T) {
	cfg := &config.Config{
		BulkURL:          "https://example.test/torbulkexitlist",
		ExitAddressesURL: "https://example.test/exit-addresses",
		StorePath:        filepath.Join(t.TempDir(), "exitlist.json"),
		TTL:              time.Hour,
		AutoUpdate:       true,
	}
	e := engine.New(cfg, fakeFetcher{bulk: sampleBulk, meta: sampleMeta})
	e.Now = func() time.Time { return time.Unix(1720000000, 0) }
	var out, errw bytes.Buffer
	// No store yet, but auto-update on → it fetches, then answers.
	if got := runCheck(context.Background(), &out, &errw, strings.NewReader(""), e, false, false, []string{"1.2.3.4"}); got != exitIsExit {
		t.Errorf("auto-update check = %d, want %d (err=%q)", got, exitIsExit, errw.String())
	}
}

func TestRunCheckNoListNoUpdate(t *testing.T) {
	cfg := &config.Config{
		BulkURL:   "https://example.test/torbulkexitlist",
		StorePath: filepath.Join(t.TempDir(), "missing.json"),
		TTL:       time.Hour,
	}
	e := engine.New(cfg, fakeFetcher{})
	var out, errw bytes.Buffer
	if got := runCheck(context.Background(), &out, &errw, strings.NewReader(""), e, false, true, []string{"1.2.3.4"}); got != exitError {
		t.Errorf("check without list = %d, want %d", got, exitError)
	}
	if !strings.Contains(errw.String(), "update") {
		t.Errorf("missing-list hint should mention update, got %q", errw.String())
	}
}

func TestRunStatusOK(t *testing.T) {
	e := seededEngine(t)
	var out, errw bytes.Buffer
	if got := runStatus(&out, &errw, e); got != 0 {
		t.Errorf("runStatus = %d, want 0", got)
	}
	if !strings.Contains(out.String(), "exit nodes: 3") || !strings.Contains(out.String(), "metadata:   1") {
		t.Errorf("status output = %q", out.String())
	}
}

func TestRunUpdate(t *testing.T) {
	cfg := &config.Config{
		BulkURL:          "https://example.test/torbulkexitlist",
		ExitAddressesURL: "https://example.test/exit-addresses",
		StorePath:        filepath.Join(t.TempDir(), "exitlist.json"),
		TTL:              time.Hour,
	}
	e := engine.New(cfg, fakeFetcher{bulk: sampleBulk, meta: sampleMeta})
	e.Now = func() time.Time { return time.Unix(1720000000, 0) }
	var out, errw bytes.Buffer
	if got := runUpdate(&out, &errw, e); got != 0 {
		t.Errorf("runUpdate = %d, want 0 (err=%q)", got, errw.String())
	}
	if !strings.Contains(out.String(), "exit nodes: 3") || !strings.Contains(out.String(), "metadata:   1") {
		t.Errorf("update output = %q", out.String())
	}
}
