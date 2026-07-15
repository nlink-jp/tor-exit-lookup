package mcp

import (
	"context"
	"encoding/json"
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

func newEngine(t *testing.T, seed bool) *engine.Engine {
	t.Helper()
	cfg := &config.Config{
		BulkURL:          "https://x.test/torbulkexitlist",
		ExitAddressesURL: "https://x.test/exit-addresses",
		StorePath:        filepath.Join(t.TempDir(), "exitlist.json"),
		TTL:              time.Hour,
		AutoUpdate:       true,
	}
	e := engine.New(cfg, fakeFetcher{bulk: sampleBulk, meta: sampleMeta})
	e.Now = func() time.Time { return time.Unix(1720000000, 0) }
	if seed {
		if _, err := e.Update(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

type rawResp struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

func drive(t *testing.T, e *engine.Engine, requests ...string) []rawResp {
	t.Helper()
	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var out strings.Builder
	if err := Serve(context.Background(), e, "test-ver", in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var resps []rawResp
	dec := json.NewDecoder(strings.NewReader(out.String()))
	for {
		var r rawResp
		if err := dec.Decode(&r); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("decode response: %v (buffer: %s)", err, out.String())
		}
		resps = append(resps, r)
	}
	return resps
}

func callText(t *testing.T, result json.RawMessage) (string, bool) {
	t.Helper()
	var tr toolResult
	if err := json.Unmarshal(result, &tr); err != nil {
		t.Fatalf("unmarshal toolResult: %v", err)
	}
	if len(tr.Content) == 0 {
		t.Fatal("empty content")
	}
	return tr.Content[0].Text, tr.IsError
}

func TestServeSequence(t *testing.T) {
	e := newEngine(t, true)
	resps := drive(t, e,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`, // silent
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"check_ip","arguments":{"ip":"1.2.3.4"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"list_status"}}`,
		`{"jsonrpc":"2.0","id":5,"method":"ping"}`,
	)
	if len(resps) != 5 {
		t.Fatalf("got %d responses, want 5 (notification must be silent)", len(resps))
	}

	var initRes struct {
		ServerInfo struct{ Name string } `json:"serverInfo"`
	}
	json.Unmarshal(resps[0].Result, &initRes)
	if initRes.ServerInfo.Name != "tor-exit-lookup" {
		t.Errorf("serverInfo.name = %q", initRes.ServerInfo.Name)
	}

	var listRes struct {
		Tools []struct{ Name string } `json:"tools"`
	}
	json.Unmarshal(resps[1].Result, &listRes)
	if len(listRes.Tools) != 4 {
		t.Errorf("tools = %d, want 4", len(listRes.Tools))
	}

	text, isErr := callText(t, resps[2].Result)
	if isErr || !strings.Contains(text, `"is_exit": true`) || !strings.Contains(text, "AAAA1111") {
		t.Errorf("check_ip text = %s (isErr=%v)", text, isErr)
	}

	text, _ = callText(t, resps[3].Result)
	if !strings.Contains(text, `"exit_nodes": 3`) {
		t.Errorf("list_status text = %s", text)
	}
}

func TestCheckIPNotExit(t *testing.T) {
	e := newEngine(t, true)
	resps := drive(t, e,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"check_ip","arguments":{"ips":["9.9.9.9","bad-ip"]}}}`,
	)
	text, isErr := callText(t, resps[0].Result)
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if !strings.Contains(text, `"is_exit": false`) || !strings.Contains(text, "invalid address") {
		t.Errorf("check_ip text = %s", text)
	}
}

func TestCheckIPNoListHint(t *testing.T) {
	e := newEngine(t, false) // store not written
	resps := drive(t, e,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"check_ip","arguments":{"ip":"1.2.3.4"}}}`,
	)
	text, isErr := callText(t, resps[0].Result)
	if !isErr || !strings.Contains(text, "update_list") {
		t.Errorf("expected error result hinting update_list, got %s (isErr=%v)", text, isErr)
	}
}

func TestUpdateListThenStatus(t *testing.T) {
	e := newEngine(t, false)
	resps := drive(t, e,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"update_list"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_status"}}`,
	)
	text, isErr := callText(t, resps[0].Result)
	if isErr || !strings.Contains(text, `"updated": true`) || !strings.Contains(text, `"meta_count": 1`) {
		t.Errorf("update_list text = %s (isErr=%v)", text, isErr)
	}
	text, isErr = callText(t, resps[1].Result)
	if isErr || !strings.Contains(text, `"exit_nodes": 3`) {
		t.Errorf("list_status after update = %s (isErr=%v)", text, isErr)
	}
}

func TestInitializeInstructionsAndGetUsage(t *testing.T) {
	e := newEngine(t, true)
	resps := drive(t, e,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_usage"}}`,
	)
	var init struct {
		Instructions string `json:"instructions"`
	}
	json.Unmarshal(resps[0].Result, &init)
	if !strings.Contains(init.Instructions, "get_usage") {
		t.Errorf("initialize instructions should mention get_usage: %q", init.Instructions)
	}
	text, isErr := callText(t, resps[1].Result)
	if isErr || !strings.Contains(text, "Recovery table") || !strings.Contains(text, "is_exit") {
		t.Errorf("get_usage manual incomplete: isErr=%v", isErr)
	}
}

func TestUnknownMethod(t *testing.T) {
	e := newEngine(t, true)
	resps := drive(t, e, `{"jsonrpc":"2.0","id":9,"method":"bogus/method"}`)
	if resps[0].Error == nil || resps[0].Error.Code != -32601 {
		t.Errorf("expected -32601 method not found, got %+v", resps[0].Error)
	}
}
