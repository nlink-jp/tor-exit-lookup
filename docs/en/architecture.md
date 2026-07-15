# tor-exit-lookup architecture

This document explains *why* tor-exit-lookup is built the way it is. For *what*
each package does, read the package doc comments and [AGENTS.md](../../AGENTS.md).

## Goal

Answer one question fast and offline: **is this IP a Tor Exit node?** The tool
optimizes for a cheap, dependency-free membership test that fits into log
triage, IR, and shell pipelines alongside asn-lookup and abuse-lookup.

## Why an offline list, not a live check

There is a live service (`https://check.torproject.org/`) that answers "are *you*
coming from Tor", but that only inspects the caller's own connection. To classify
*arbitrary* IPs — the ones in a log file — you need the full exit-node set. The
Tor Project publishes exactly that as `torbulkexitlist`: a plain, one-IP-per-line
text file.

So the model mirrors asn-lookup, not abuse-lookup: **download the whole dataset
once, query it locally.** Every `check` is then a pure, offline, O(1) lookup. It
also respects the Tor Project's request that clients not poll per-query — the
list is fetched on `update` and cached.

## Why no credentials

The endpoint is public and unauthenticated. Unlike asn-lookup (ipinfo token) or
abuse-lookup (AbuseIPDB key), there is no secret to store, redact, or leak. This
removes an entire class of config and security concerns; `config` has no key
field, and the fetcher has no token to redact.

## The store

A few thousand exit addresses do not warrant a binary index. The store is a
small JSON file:

```json
{ "generated_at": "…Z", "source": "…/torbulkexitlist", "count": 1392, "exits": ["…"] }
```

On load it is parsed into a `map[netip.Addr]struct{}`; `Contains` is a map
lookup. Two deliberate choices:

- **Deterministic serialization.** `Serialize` sorts the addresses and takes
  `generatedUnix` as a parameter — only `engine.Update` reads the clock. Identical
  input always produces byte-identical output, which keeps the store diffable and
  the tests hermetic.
- **Freshness in the record, not the mtime.** `generated_at` lives inside the
  file, so a copied or restored store keeps its true age. `StaleAfter` is 24h:
  the upstream list refreshes about every 30 minutes, so a day-old copy is
  clearly out of date (contrast asn-lookup's 30 days for a monthly DB).

Writes go through temp + rename so a crash mid-write never leaves a truncated
store to be read back.

## Address canonicalization

Every address is normalized with `Unmap()` on both parse and query, so a
v4-in-v6 form (`::ffff:1.2.3.4`) matches the plain v4 entry. torbulkexitlist is
currently IPv4-dominant, but IPv6 entries are handled the same way.

## Layering

```
app  →  engine  →  { torproject (fetch), exitlist (parse/store) }
                     ↑
                   config
```

`engine` is the single flow shared by the CLI and (Phase 2) the MCP server, so
their behaviour cannot diverge. The fetcher is an interface, so the engine is
tested with a fake — no network in the test suite. `exitlist` is pure and holds
the highest test coverage.

## Exit-code contract

`check` uses the grep convention: `0` = match, `1` = no match, `2` = error.
POSIX exit codes are unsigned 0–255, so a three-state result maps to `{0,1,2}`
(a negative code is not representable). This makes `if tor-exit-lookup check
"$ip"` read naturally.

The tri-state applies only to a **single positional IP in text mode** — the
scriptable path. Any batch shape (multiple IPs, stdin, or `--json`) moves per-IP
results to stdout and reports errors only via the exit code (`0`/`2`), so a
typo'd line in a piped file doesn't fail the whole run.

## Two sources, one membership

Membership comes solely from `torbulkexitlist`; `exit-addresses` supplies
optional per-node metadata (fingerprint, Published, LastStatus) keyed by exit
address. The two feeds are known to differ slightly, so the rule is deliberate:
**`is_exit` is decided by `torbulkexitlist` alone**, and metadata only enriches a
hit. A missing metadata entry is normal and never changes the answer. Because
metadata is non-essential, an `exit-addresses` fetch failure is *soft*: `update`
still writes membership and surfaces a `MetaWarning`, whereas a `torbulkexitlist`
failure is a hard error. Metadata can be disabled entirely with
`exit_addresses_url = ""`.

## Auto-refresh and etiquette

The upstream list changes often, so `check` calls `engine.EnsureFresh`: if the
cached list is older than the TTL it refetches first. Two safeguards keep this
polite and robust. The TTL is floored at 30 minutes (`config.MinTTL`) so
auto-refetch can never hammer the endpoint regardless of configuration. And a
refetch failure is non-fatal when a cache exists — `EnsureFresh` returns the
stale set alongside the error, so `check` warns and answers offline instead of
failing. `status` never fetches; it reports the cached state as-is.

## The MCP surface

`tor-exit-lookup mcp` drives the same `engine` over a standard-library JSON-RPC
2.0 stdio loop, so CLI and MCP answers cannot diverge. Results are small (a
yes/no plus a little metadata), so — unlike asn-lookup's reverse lookups — there
is no file-mediation and no workspace. The server caches the parsed set keyed by
the store's mtime and reloads after `update_list`. The `get_usage` manual is
embedded and pinned by a test against the advertised tool set.

## Security & privacy

No credentials, no telemetry. The only network egress is the `update` /
auto-refetch fetch of two public lists. The cached list is local and is not
redistributed.
