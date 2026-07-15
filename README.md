# tor-exit-lookup

Is an IP address a **Tor Exit node**? `tor-exit-lookup` answers offline from a
locally cached copy of the Tor Project's
[`torbulkexitlist`](https://check.torproject.org/torbulkexitlist), enriched with
per-node metadata from
[`exit-addresses`](https://check.torproject.org/exit-addresses). Download the
list once with `update` (or let it auto-refetch), then every `check` is an
instant in-memory membership test — no network, no credentials.

The offline, membership-focused sibling of
[`asn-lookup`](https://github.com/nlink-jp/asn-lookup) (AS / country) and
[`abuse-lookup`](https://github.com/nlink-jp/abuse-lookup) (reputation).
Together they profile an IP from three angles, over both CLI pipes and MCP.

## Install

Not yet released. To build from source (Go 1.25+):

```sh
git clone https://github.com/nlink-jp/tor-exit-lookup
cd tor-exit-lookup
make build          # → dist/tor-exit-lookup
```

## Quick start

```sh
# 1. Download the Tor exit list (public endpoints, no auth):
tor-exit-lookup update

# 2. Check an address:
tor-exit-lookup check 2.56.10.36
# → 2.56.10.36 is a Tor Exit node  [5B324A627C4F…, last seen 2026-07-15 01:00]

tor-exit-lookup check 8.8.8.8
# → 8.8.8.8 is not a Tor Exit node        (exit code 1)

# 3. Use the exit code in a script:
if tor-exit-lookup check "$ip"; then
  echo "$ip is coming through Tor"
fi

# 4. Filter a log's IPs in bulk:
cut -f1 access.log | tor-exit-lookup check --json | jq 'select(.is_exit)'
```

## Commands

| Command | Description |
|---------|-------------|
| `check <IP>...` | Report whether each IP is a Tor Exit node (reads stdin if no args) |
| `update` | Download the exit list + metadata and rebuild the local store |
| `status` | Show the cached list's freshness, size, and sources |
| `mcp` | Run as a local MCP server over stdio |
| `version` | Print the version |

### `check` modes and exit codes

A single positional IP in text mode uses the grep convention so it composes in
shell:

| Code | Meaning |
|------|---------|
| `0` | the IP **is** a Tor Exit node |
| `1` | the IP is **not** a Tor Exit node |
| `2` | error (invalid IP, no local list, …) |

Any other shape — multiple IPs, stdin input, or `--json` — is **batch mode**:
one result line per IP on stdout, and the exit code signals errors only
(`0` / `2`). `--json` emits one JSON object per line
(`{ip, is_exit, fingerprint?, published?, last_status?, checked_at, list_updated_at}`).

## Auto-refresh

The Tor exit list changes often (upstream refreshes ~every 30 minutes). By
default, `check` auto-refetches when the cached list is older than the TTL
(default 1 hour, floored at 30 minutes out of fetch etiquette). If the refetch
fails (e.g. offline), the cached list is used with a warning rather than
failing. Disable per-call with `--no-update`, or globally with
`[torproject] auto_update = false`.

## MCP server

`tor-exit-lookup mcp` speaks JSON-RPC 2.0 over stdio (standard library only).
Tools: `check_ip`, `list_status`, `update_list`, and `get_usage` (an embedded
operating manual; the server also advertises it via the initialize
`instructions` field). Example registration:

```json
{
  "mcpServers": {
    "tor-exit-lookup": { "command": "tor-exit-lookup", "args": ["mcp"] }
  }
}
```

## Configuration

No credentials are required — the endpoints are public. Everything has a
sensible default; override via a config file, environment variables, or flags.

```toml
# ~/.config/tor-exit-lookup/config.toml
[torproject]
# bulk_url = "https://check.torproject.org/torbulkexitlist"
# exit_addresses_url = "https://check.torproject.org/exit-addresses"  # "" disables metadata
# ttl_minutes = 60      # auto-refetch threshold (floored at 30)
# auto_update = true    # auto-refetch on check when stale

[store]
# path = "~/.local/share/tor-exit-lookup/exitlist.json"
```

| Setting | Env var | Flag | Default |
|---------|---------|------|---------|
| List URL | `TOR_EXIT_LOOKUP_URL` | `--url` (update) | `…/torbulkexitlist` |
| Metadata URL | `TOR_EXIT_LOOKUP_EXIT_ADDRESSES_URL` | — | `…/exit-addresses` |
| Store path | `TOR_EXIT_LOOKUP_STORE` | `--store` | `~/.local/share/tor-exit-lookup/exitlist.json` |
| TTL (minutes) | `TOR_EXIT_LOOKUP_TTL_MINUTES` | — | `60` (min 30) |
| Auto-update | `TOR_EXIT_LOOKUP_AUTO_UPDATE` | `--no-update` (off) | `true` |
| Config path | — | `-c`, `--config` | `~/.config/tor-exit-lookup/config.toml` |

## How it works

`update` fetches the whole exit list (membership) plus the exit-addresses feed
(metadata), parses them into a set of `netip.Addr` with a metadata sidecar, and
writes a small, deterministic JSON store (atomic temp + rename). `check` loads
that set and does an O(1) membership test — a few thousand addresses need no
index. Membership is authoritative from `torbulkexitlist`; a missing metadata
entry never changes the yes/no answer. Freshness is stamped inside the store
(`generated_at`), not the file mtime, so it survives copies; `status` warns once
the local copy is over 24 hours old.

## Development

```sh
make test        # go test -race -cover ./...
make check       # lint + test + build-all
```

No external dependencies — standard library only. See
[docs/en/architecture.md](docs/en/architecture.md) for the design rationale.

## License

MIT — see [LICENSE](LICENSE). Exit-list data is provided by
[the Tor Project](https://www.torproject.org/); the cached copy is local and is
not redistributed.
