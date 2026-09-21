# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Fixed

- **`make verify-release` now fails closed.** Its last block chained unzip, the
  packaged binary's `--version` and `spctl` with `&&` and ended the whole chain
  in `|| true`, so a zip that did not unpack or a binary that did not run exited
  0 and the upload proceeded. Each step is now judged on its own, the packaged
  binary's `--version` must contain the tag being released, and only the
  informational `spctl` line may be ignored. Matches the org template
  (CONVENTIONS.md §Code Signing → Verifying a release).

## [0.2.0] - 2026-09-21

### Changed

- **An MCP tool call carrying an argument the tool does not declare now fails
  instead of being quietly ignored.** This is a deliberate behaviour change,
  required by org ADR-021 §4. Until now a misspelt argument was dropped and the
  call ran without it: a batch of addresses sent under a misspelt `ips` checked
  nothing at all and came back with "provide 'ip'", which reads as a missing
  argument rather than a mistyped one. Every tool — including `get_usage`,
  `update_list` and `list_status`, which take no arguments — now decodes with
  `DisallowUnknownFields` and refuses the call, naming the offending field:
  `arguments: json: unknown field "ipx"`.

  A malformed argument object is refused for the same reason. The decode error
  used to be discarded along with the unknown field, so `{"ip": 8}` ran as if
  no address had been supplied. It now reports the type mismatch.

  Nothing runs before the arguments decode, so a rejected call reads no list
  and downloads nothing. Omitting `arguments`, or sending `{}` or `null`, still
  means "no arguments" and is not an error. There is no compatibility shim: an
  argument name this server does not declare has never meant anything, so the
  only fix is to correct it.

### Fixed

- **Every MCP tool input schema is closed.** The schemas omitted
  `additionalProperties: false`, so a mistyped argument read as a legitimate one
  to any client that validates against them. Schemas are now built through a
  single `obj()` helper that sets the flag, and an arch test fails if a tool's
  schema omits it — org ADR-021 §10 requires the test as well as the flag,
  because a rule stated only in prose is re-decided by whoever adds the next
  tool.

- `make check` is green again: `make lint` failed on 17 errcheck findings.
  Seven were `fmt.Fprint*` writes to the CLI's own stdout/stderr, now excluded
  in a new `.golangci.yml` with the reasoning recorded there — reporting a
  failed write to the stream that just failed is circular, and the exit code
  already carries the outcome. The other ten were deliberate discards (reading
  a config file, closing response bodies already consumed, the post-rename
  temp-file unlink, and test-only decodes whose zero value the next assertion
  rejects) and are now written `_ =` with the reason beside each. None was a
  real unchecked error: the one write path that matters, `engine.writeStore`,
  already folds its temp file's `Close` into the returned error.

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
