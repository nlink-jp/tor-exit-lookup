# CLAUDE.md — tor-exit-lookup

**Organization rules (mandatory): https://github.com/nlink-jp/.github/blob/main/CONVENTIONS.md**

## Purpose

CLI + local MCP server that reports whether an IP address is a **Tor Exit
node**, answered offline from a locally cached copy of the Tor Project's
`torbulkexitlist` (membership) enriched with `exit-addresses` (per-node
metadata). Reads IPs, returns yes/no with a grep-style exit code (single) or
batch/JSON output, and refreshes the list on demand or automatically. The
offline, membership-focused sibling of `asn-lookup` (AS/country) and
`abuse-lookup` (reputation) — together they profile an IP from three angles.

## Build & test

```bash
make build       # Build → dist/tor-exit-lookup  (never `go build` directly)
make test        # Tests with race detector + coverage
go test ./...    # Same without Makefile
```

## Architecture

```
main.go                 CLI entry: main.version → app.Run
internal/exitlist/      Parse torbulkexitlist + exit-addresses, membership Set + meta, store (pure)
internal/torproject/    Fetcher interface + HTTPFetcher (public endpoints, User-Agent)
internal/config/        Sectioned-TOML subset + env/flag resolution (no credentials)
internal/engine/        LoadList / Update (2-source, atomic) / Lookup / EnsureFresh / IsStale
internal/app/           Dispatch + check/update/status/mcp; --json, batch, grep-style exit codes
internal/mcp/           Zero-dep stdio JSON-RPC 2.0 server + tools (check_ip/list_status/update_list/get_usage)
```

Core logic takes io.Reader/io.Writer and injected dependencies for testability
(the fetcher is an interface, mocked in tests). **No external dependencies —
standard library only.** See [docs/en/architecture.md](docs/en/architecture.md)
for the "why".

## Key conventions

- **No credentials:** the torbulkexitlist endpoint is public. There is no token
  or API key — unlike asn-lookup (ipinfo token) or abuse-lookup (AbuseIPDB key).
- **Offline membership test:** `update` downloads the whole list once; every
  `check` is an in-memory hash-set lookup (a few thousand addresses, no index).
- **Grep-style exit codes** (`check`): `0` = is an exit node, `1` = not, `2` =
  error. So `if tor-exit-lookup check "$ip"; then …` composes in shell.
- **Deterministic store:** `exitlist.Serialize` takes `generatedUnix` and sorts
  addresses; only `engine.Update` reads the clock. Writes are atomic (temp +
  rename) so a crash never leaves a truncated store.
- **Freshness lives in the record** (`generated_at`), not the file mtime.
  `StaleAfter` is 24h — the list refreshes ~every 30 min, so a day-old copy is
  meaningfully stale.
- **Fetch etiquette:** the Tor Project asks clients not to poll excessively.
  Every request carries a descriptive User-Agent; auto-refresh (Phase 2) will
  enforce a 30-minute TTL floor.

## Key conventions (Phase 2)

- **Two sources, one membership.** `torbulkexitlist` is authoritative for the
  yes/no answer; `exit-addresses` only adds metadata to hits. A missing metadata
  entry never changes `is_exit`, and an exit-addresses fetch failure is soft
  (membership still updates, with a warning).
- **Exit-code contract by mode.** Single positional IP + text = tri-state
  (0/1/2). Multiple IPs / stdin / `--json` = batch (error-only 0/2, results on
  stdout). Don't collapse a not-found into 0 in the single-IP path.
- **Auto-refetch has a 30-min floor** (`config.MinTTL`); it degrades to the
  cached list on network failure rather than erroring.
- **`engine.Update` is the only clock reader** (`exitlist.Serialize` takes
  `generatedUnix`), keeping the store deterministic.
- **usage.md is pinned** by `usage_test.go`: adding/renaming a tool or a result
  field means updating the manual, or the test fails.

## Status

Phase 1 + Phase 2 implemented (`_wip/`, local only — not yet pushed). Next:
Phase 3 (release + integration). Not yet released; no version tags.

## Communication Language

All communication between contributors and Claude Code is conducted in
**Japanese**.
