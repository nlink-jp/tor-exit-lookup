# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [0.1.0] - 2026-07-15

### Added

- Initial release.
- `check <IP>...` — report whether IPs are Tor Exit nodes, answered offline from
  the cached list. A single positional IP in text mode uses grep-style exit
  codes (`0` = exit, `1` = not, `2` = error); multiple IPs, stdin, or `--json`
  switch to batch mode (per-IP results on stdout, error-only exit code). `--json`
  emits JSON Lines. On a hit, per-node metadata (fingerprint / published /
  last_status) is shown.
- `update` — download the `torbulkexitlist` (membership) and `exit-addresses`
  (metadata) and rebuild the local store (atomic temp + rename; deterministic,
  sorted serialization). A metadata-source failure is soft: membership is still
  written and a warning is surfaced.
- `status` — show the cached list's sources, generation time, exit-node count
  (v4/v6), metadata count, and staleness (`StaleAfter` = 24h).
- `mcp` — local stdio MCP server (JSON-RPC 2.0, standard library only) exposing
  `check_ip`, `list_status`, `update_list`, and `get_usage`. `get_usage` returns
  an embedded operating manual, advertised via the initialize `instructions`
  field.
- Auto-refetch: `check` refetches when the cached list is older than the TTL
  (default 1h, floored at 30m out of fetch etiquette). A refetch failure falls
  back to the cached list with a warning. Disable with `--no-update` or
  `[torproject] auto_update = false`.
- Offline membership store: parsed into an in-memory `netip.Addr` hash set with a
  metadata sidecar; addresses canonicalized (`Unmap`) so v4-in-v6 inputs match.
  Freshness lives in the record (`generated_at`), not the file mtime.
- Configuration via sectioned TOML (`~/.config/tor-exit-lookup/config.toml`) and
  `TOR_EXIT_LOOKUP_*` environment variables (`URL`, `EXIT_ADDRESSES_URL`,
  `STORE`, `TTL_MINUTES`, `AUTO_UPDATE`). No credentials required.
- Fetch etiquette: a descriptive `User-Agent` on every request; the list is
  cached and auto-refetch is rate-limited by the 30-minute TTL floor.
- Zero external dependencies (standard library only).
- Tor Project attribution in `version` and the READMEs.
