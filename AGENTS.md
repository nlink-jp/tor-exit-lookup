# AGENTS.md — tor-exit-lookup

## What this is

A CLI + (Phase 2) local MCP server that reports whether an IP address is a **Tor
Exit node**. It answers offline from a locally cached copy of the Tor Project's
`torbulkexitlist` (a plain, one-IP-per-line text file): `update` downloads the
list once, and every `check` is an in-memory membership test. The offline,
membership-focused sibling of `asn-lookup` (AS/country) and `abuse-lookup`
(reputation).

## Build & test

```bash
make build      # → dist/tor-exit-lookup  (NEVER `go build` directly)
make test       # go test -race -cover ./...
make check      # lint + test + build-all
make build-all  # cross-compile linux/{amd64,arm64}, darwin/arm64, windows/amd64
```

Go 1.25+. **No external dependencies** — standard library only.

## Layout

```
main.go                 Entry point; sets main.version, calls app.Run.
internal/exitlist/      Parsing + membership Set + metadata + on-disk store.
  exitlist.go           ParseBulkList; ParseExitAddresses; Set.Contains/Meta; Serialize/Open.
internal/torproject/    Fetcher interface + HTTPFetcher (public endpoints, User-Agent).
internal/config/        Sectioned-TOML subset parser + env/flag resolution (no secrets).
internal/engine/        Ties config+fetcher+store: LoadList, Update (2-source), Lookup, EnsureFresh, IsStale.
internal/app/           CLI: dispatch, check/update/status/mcp, output; grep-style + batch/JSON.
internal/mcp/           Zero-dep stdio JSON-RPC 2.0 server + tools.
  usage.md              Embedded get_usage manual (pinned by usage_test.go).
```

## Key design decisions

- **Offline DB, not an online API.** Like asn-lookup (and unlike abuse-lookup),
  the whole list is downloaded once and queried locally. `check` never touches
  the network.
- **No credentials.** The endpoint is public; there is no token or API key to
  configure, log, or leak.
- **Engine is shared** by CLI and (Phase 2) MCP so their behaviour cannot diverge.
- **Fetcher is an interface** (`torproject.Fetcher`) so the engine is tested
  without touching the network.
- **Deterministic store.** `exitlist.Serialize` takes `generatedUnix` and sorts
  the addresses, so identical input yields byte-identical output. Only
  `engine.Update` reads the clock. Writes are atomic (temp + rename).
- **Freshness lives in the record** (`generated_at`), not the file mtime, so it
  survives copies/backups. `StaleAfter` (24h) drives the `status` warning;
  `config.TTL` (default 1h, `MinTTL` 30m floor) drives auto-refetch.
- **Two sources, one membership.** `torbulkexitlist` is authoritative for
  `is_exit`; `exit-addresses` (fingerprint / Published / LastStatus) only
  enriches hits. A missing metadata entry never changes membership, and an
  exit-addresses fetch failure is soft (membership still writes, `MetaWarning`
  is set).
- **Auto-refetch degrades gracefully.** `engine.EnsureFresh` returns the stale
  cached set alongside the error when a refetch fails, so `check` warns and
  continues offline instead of failing.

## Gotchas

- **Exit-code contract depends on mode:** single positional IP in text mode is
  grep-style tri-state `0` (is exit) / `1` (not exit) / `2` (error). Multiple
  IPs, stdin, or `--json` switch to batch mode: per-IP results on stdout, exit
  code error-only (`0`/`2`). POSIX exit codes are 0–255 (no negatives). Don't
  "normalize" a single-IP not-found to `0` — callers rely on `1`.
- **v4/v6:** addresses are canonicalized with `Unmap()`, so `::ffff:1.2.3.4`
  matches `1.2.3.4`. torbulkexitlist is currently IPv4-only but may include IPv6;
  both are handled.
- **Fetch etiquette:** the Tor Project asks clients not to poll excessively. Keep
  the descriptive User-Agent, cache the result, and rely on the 30-minute TTL
  floor (`config.MinTTL`) to rate-limit auto-refetch. `status` never fetches.
- **usage.md is pinned:** `internal/mcp/usage.md` is embedded and returned by
  `get_usage`; the initialize `instructions` field points clients to it.
  Adding/renaming a tool or a documented result field means updating usage.md —
  `usage_test.go` fails if the manual omits a tool name or a key term.
- **MCP has no workspace:** results are small (a yes/no + a little metadata), so
  unlike asn-lookup there is no file-mediation. The server caches the parsed set
  keyed by store mtime and reloads after `update_list`.
- **Attribution:** keep the Tor Project data credit in `version` and the READMEs.
  The cached list is local and not redistributed.

## Data sources

- `https://check.torproject.org/torbulkexitlist` — plain IP list, membership
  (authoritative). Public, no authentication.
- `https://check.torproject.org/exit-addresses` — per-node metadata (fingerprint,
  Published, LastStatus). Public, no authentication. Optional: a fetch failure is
  soft and can be disabled via `exit_addresses_url = ""`.
