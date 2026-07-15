// Command tor-exit-lookup reports whether an IP address is a Tor Exit node,
// answered from a locally cached copy of the Tor Project's torbulkexitlist, as
// a CLI and (Phase 2) a local MCP server. The offline, membership-focused
// sibling of asn-lookup (AS/country) and abuse-lookup (reputation).
package main

import (
	"os"

	"github.com/nlink-jp/tor-exit-lookup/internal/app"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(app.Run(os.Args[1:], version))
}
