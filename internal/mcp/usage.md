# tor-exit-lookup MCP — operating manual

This server reports whether an IP address is a **Tor Exit node**, answered
offline from a locally cached copy of the Tor Project's `torbulkexitlist`
(membership) enriched with `exit-addresses` (per-node metadata). All checks are
offline; only `update_list` touches the network. **No credentials are required.**

Call `list_status` first to confirm a list exists and is fresh. If it does not
exist, call `update_list` (no token needed).

## Tools

### `get_usage`
Returns this manual. No arguments.

### `list_status`
Reports `generated`, `exit_nodes` (with `v4`/`v6`), `meta_count`, `stale`,
`age_hours`, `sources`, and the store `path`. No arguments. Returns an error
result when no list exists yet.

### `update_list`
Downloads the latest `torbulkexitlist` and `exit-addresses` and rebuilds the
local store. No arguments, no credentials. Returns counts on success. A metadata
(`exit-addresses`) fetch failure is soft: membership is still updated and a
`meta_warning` is included; a `torbulkexitlist` failure is a hard error.

### `check_ip`
IP → is it a Tor Exit node?
- Arguments: `ip` (string) **or** `ips` (array of strings). At least one required.
- Result: a JSON array, one object per input, each with `input` and `is_exit`.
  On a hit with metadata, `fingerprint`, `published`, and `last_status` are also
  present. Invalid addresses come back with `is_exit:false` and
  `error:"invalid address"`.
- Membership is authoritative from `torbulkexitlist`; missing metadata never
  changes `is_exit`.

## The list lifecycle

`update_list` downloads the whole exit list once and stores it locally; every
`check_ip` is then an offline membership test. Freshness lives inside the store
(`generated`), not the file mtime. The upstream list refreshes about every 30
minutes; the CLI auto-refetches past a TTL (30-minute floor) out of fetch
etiquette. `list_status` reports `stale:true` once the copy is over 24 hours old.

## Recovery table

| Symptom (result text) | What it means | What to do |
|---|---|---|
| `no local exit list …` | The list has not been downloaded | Call `update_list` |
| `check_ip` → `error:"invalid address"` | The input was not a valid IP | Fix the input |
| `is_exit:false` (no error) | Address is not a known Tor exit | Expected; no action |
| `is_exit:true`, no `fingerprint` | Membership hit without metadata | Expected; membership is still authoritative |
| `update_list` → `meta_warning` set | exit-addresses could not be fetched | Membership is updated; metadata will fill in on a later `update_list` |
| `list_status` → `stale:true` | List older than 24h | Call `update_list` to refresh |

## Attribution

Data: the Tor Project exit list (https://check.torproject.org/torbulkexitlist and
https://check.torproject.org/exit-addresses). The cached copy is local and is not
redistributed.
