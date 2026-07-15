# RFP: tor-exit-lookup

> Generated: 2026-07-15
> Status: Draft

## 1. Problem Statement

A CLI and MCP server that instantly determines, offline, whether a given IP
address is a **Tor Exit node IP**. It caches the Tor Project's
`torbulkexitlist` (a plain list of IPs) as the authoritative membership source
and `exit-addresses` (carrying fingerprint / LastStatus) as a metadata source,
matching against them with zero external dependencies and no authentication.
The target users are security staff doing IR / SOC work or log analysis who
need to quickly answer "is this source coming through Tor?". It forms one piece
of a three-tool set alongside asn-lookup (AS / country) and abuse-lookup
(IP reputation) for profiling an IP from multiple angles.

## 2. Functional Specification

### Commands / API Surface

**CLI**

| Command | Description |
|---------|-------------|
| `tor-exit-lookup <IP>` | Determine whether a single IP is a Tor Exit node |
| `tor-exit-lookup --json <IP>` | JSON output (`{ip, is_exit, fingerprint?, last_status?, checked_at, list_updated_at}`) |
| `cat ips.txt \| tor-exit-lookup` | stdin batch mode (one IP per line, pipe-friendly) |
| `tor-exit-lookup update` | Re-fetch `torbulkexitlist` / `exit-addresses` and refresh the cache |
| `tor-exit-lookup status` | Cache freshness (last update, entry count, staleness) |

**Exit codes (single-IP check)**

- `0` = the IP is a Tor Exit node (hit)
- `1` = not an Exit node (no hit)
- `2` = error (invalid IP / no cache / fetch failure)

This lets `if tor-exit-lookup $ip; then ...` read naturally, grep-style. In
batch / JSON mode, results go to stdout, so the exit code reflects error
presence only (`0` on success / `2` on error).

**MCP tools** (mirroring asn-lookup)

| tool | Description |
|------|-------------|
| `check_ip` | Tor Exit determination for a single IP (including metadata) |
| `list_status` | Cache freshness check (equivalent to asn-lookup's `db_status`) |
| `update_list` | Re-fetch lists (equivalent to `update_db`) |
| `get_usage` | Tool reference / error-recovery table |

### Input / Output

- Input: a single IP (argument) or stdin batch (one IP per line)
- Output: human-readable text (default) / `--json`
- JSON schema (single): `{ip, is_exit, fingerprint?, published?, last_status?, checked_at, list_updated_at}`
- Metadata is populated from exit-addresses on a hit; if absent, only the
  determination is returned

### Configuration

- Cache location: XDG-compliant local data directory (following asn-lookup)
- TTL: default overridable via flag / env var. **TTL floor is 30 minutes**
  (aligned with Tor's update cadence and fetch etiquette)
- No credentials required (no token stored in any config file)

### External Dependencies

- Source 1: `https://check.torproject.org/torbulkexitlist` (authoritative
  membership, IPs only, public / no auth)
- Source 2: `https://check.torproject.org/exit-addresses` (metadata source:
  fingerprint / Published / LastStatus, public / no auth)
- Implementation dependencies: Go standard library only (`net/http` +
  `net/netip`). Zero external dependencies

## 3. Design Decisions

- **Language = Go.** Consistent with the sibling tools (asn-lookup /
  abuse-lookup), single binary, rides the existing `make build` → sign +
  notarize release pipeline, zero external dependencies
- **Data model**: determination uses `torbulkexitlist` as the authoritative
  source, membership-tested via a hash set of `netip.Addr` (a few thousand
  entries — no index needed, in-memory matching is instant). Metadata is
  populated by IP key from exit-addresses. Known discrepancies between the two
  lists are absorbed by the rule "determination = torbulkexitlist, metadata =
  exit-addresses (missing tolerated)"
- **Complementary relationship**: a three-tool set with asn-lookup (AS /
  country) and abuse-lookup (reputation), interoperable via both CLI pipes and
  MCP
- **Out of scope**: determination of Tor relay / bridge / guard (Exit only),
  real-time connection checks, communication over Tor itself, GUI

## 4. Development Plan

### Phase 1: Core

- `torbulkexitlist` fetch / cache / membership matching (pure functions)
- Single-IP determination + exit codes (0/1/2)
- `update` / `status`
- Tests: matching logic tested as pure functions against isolated real-data
  fixtures

### Phase 2: Features

- stdin batch, `--json`
- exit-addresses metadata integration (fingerprint / LastStatus)
- Auto re-fetch on TTL expiry (30-minute floor)
- MCP server (`check_ip` / `list_status` / `update_list` / `get_usage`)

### Phase 3: Release

- README.md / README.ja.md / CHANGELOG.md, docs/{en,ja}
- notarize + 4-platform build
- submodule pointer / org profile / web catalog / Homebrew tap / check-org.sh

Independently reviewable units: Phase 1 (CLI core) and the Phase 2 MCP can be
reviewed separately.

## 5. Required API Scopes / Permissions

**None.** Both endpoints are public and require no authentication. No API key
or token whatsoever (unlike asn-lookup's ipinfo token or abuse-lookup's
AbuseIPDB key — zero credentials).

## 6. Series Placement

Series: **cybersecurity-series**

Reason: Tor Exit determination is a threat-intelligence-leaning signal, in the
same family as the IP-reputation tool abuse-lookup. The offline-matching
approach resembles asn-lookup (util-series), but because the use case is a
security determination, it is placed in cybersecurity-series.

## 7. External Platform Constraints

- The Tor Project asks for fetch etiquette (no excessive polling, caching
  recommended). `torbulkexitlist` refreshes roughly every ~30 minutes
- → Set a **30-minute TTL floor** so auto re-fetch does not hammer the
  endpoint. Attach an appropriate User-Agent
- IPv6: since the list may contain IPv6 entries, support both v4 / v6 via
  `netip.Addr`
- On network outage: continue determination from the existing cache; surface
  staleness via `status`

---

## Discussion Log

- **Tool name**: decided on `tor-exit-lookup`, faithful to the `<subject>-lookup`
  naming pattern of the siblings asn-lookup / abuse-lookup. Candidates
  `tor-lookup` (could read as covering Tor in general, blurring scope) and
  `torexit-check` (tonal mismatch with the lookup family) were rejected
- **Series**: decided on cybersecurity-series. Offline matching resembles
  asn-lookup (util), but Tor Exit determination = a threat signal, so alignment
  with abuse-lookup was prioritized
- **Freshness handling**: adopted auto re-fetch on TTL expiry, but with a
  30-minute TTL floor to respect Tor's etiquette and avoid over-fetching
- **Exit codes**: taking the user's idea of a "three-state >0 / <0 / 0", and
  since POSIX exit codes are 0–255 with no negatives, reconciled to the grep
  model {0 = hit, 1 = no hit, 2 = error}
- **Metadata**: decided to fetch exit-addresses from v1. Settled on a two-source
  design: determination = torbulkexitlist (authoritative), metadata =
  exit-addresses (missing tolerated)
- **Credentials**: since both endpoints are public / no-auth, API scopes are None
