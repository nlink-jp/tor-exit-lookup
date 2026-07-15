// Package app implements the tor-exit-lookup command-line interface: subcommand
// dispatch plus the check / update / status / mcp commands. Core logic lives in
// the exitlist, config, engine, torproject, and mcp packages; this package is
// the thin I/O shell around them.
package app

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/nlink-jp/tor-exit-lookup/internal/config"
	"github.com/nlink-jp/tor-exit-lookup/internal/engine"
	"github.com/nlink-jp/tor-exit-lookup/internal/exitlist"
	"github.com/nlink-jp/tor-exit-lookup/internal/torproject"
)

// Exit codes. `check` uses the grep-style convention so it composes in shell:
//
//	if tor-exit-lookup check "$ip"; then echo "via Tor"; fi
const (
	exitIsExit  = 0 // the IP is a Tor Exit node
	exitNotExit = 1 // the IP is not a Tor Exit node
	exitError   = 2 // usage / lookup error
)

// Run dispatches a subcommand and returns a process exit code.
func Run(args []string, version string) int {
	if len(args) == 0 {
		usage(os.Stderr)
		return exitError
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "check":
		return cmdCheck(rest)
	case "update":
		return cmdUpdate(rest)
	case "status":
		return cmdStatus(rest)
	case "mcp":
		return cmdMCP(rest, version)
	case "version", "--version", "-v":
		fmt.Println("tor-exit-lookup " + version)
		fmt.Println("Data: Tor Project exit list (https://check.torproject.org/).")
		return 0
	case "help", "-h", "--help":
		usage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage(os.Stderr)
		return exitError
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `tor-exit-lookup — is an IP address a Tor Exit node? (offline, cached list)

Usage:
  tor-exit-lookup <command> [flags] [args]

Commands:
  check <IP>...   Report whether each IP is a Tor Exit node (stdin if no args)
  update          Download the exit list and rebuild the local store
  status          Show the cached list's freshness and size
  mcp             Run as a local MCP server (stdio)
  version         Print the version

check flags:
  -j, --json      JSON Lines output (one object per IP)
  --no-update     Do not auto-refetch even if the list is stale

check exit codes (single IP, text mode):
  0  the IP is a Tor Exit node
  1  the IP is not a Tor Exit node
  2  error (invalid IP, no local list, ...)
  (batch mode — multiple IPs, stdin, or --json — exits 0 unless an error occurs)

Common flags:
  -c, --config <path>   Config file (default ~/.config/tor-exit-lookup/config.toml)
  --store <path>        Exit-list store (default ~/.local/share/tor-exit-lookup/exitlist.json)

The list auto-refetches when older than the TTL (default 1h, 30m floor); disable
with --no-update or [torproject] auto_update = false.

Data: Tor Project exit list (https://check.torproject.org/torbulkexitlist,
https://check.torproject.org/exit-addresses).
`)
}

// commonFlags are the config-resolution flags shared by every command.
type commonFlags struct {
	config string
	store  string
	url    string
}

// register binds the common flags onto fs. When withUpdate is true it also
// registers --url (only meaningful for commands that download).
func (c *commonFlags) register(fs *flag.FlagSet, withUpdate bool) {
	fs.StringVar(&c.config, "config", "", "config file path")
	fs.StringVar(&c.config, "c", "", "config file path (shorthand)")
	fs.StringVar(&c.store, "store", "", "exit-list store path override")
	if withUpdate {
		fs.StringVar(&c.url, "url", "", "torbulkexitlist URL override")
	}
}

func (c *commonFlags) buildEngine() (*engine.Engine, error) {
	cfg, err := config.Load(c.config, c.store, c.url)
	if err != nil {
		return nil, err
	}
	return engine.New(cfg, torproject.NewHTTPFetcher()), nil
}

// loadListOrHint loads the store, printing an actionable hint on ErrNoList.
func loadListOrHint(e *engine.Engine, errw io.Writer) (*exitlist.Set, int) {
	set, err := e.LoadList()
	if err != nil {
		if isNoList(err) {
			fmt.Fprintf(errw, "%v\nrun 'tor-exit-lookup update' to download the Tor exit list.\n", err)
			return nil, exitError
		}
		fmt.Fprintf(errw, "error: %v\n", err)
		return nil, exitError
	}
	return set, 0
}

func isNoList(err error) bool { return errors.Is(err, engine.ErrNoList) }

// parseInterspersed parses fs while tolerating flags that appear after
// positional arguments (Go's flag package otherwise stops at the first
// non-flag). It returns the collected positional arguments. IP inputs never
// begin with '-', so there is no ambiguity.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positionals []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		positionals = append(positionals, args[0])
		args = args[1:]
	}
	return positionals, nil
}

// readInputs returns args verbatim, or whitespace-separated tokens read from
// stdin when args is empty. Blank lines and '#' comment lines are skipped.
func readInputs(args []string, stdin io.Reader) []string {
	if len(args) > 0 {
		return args
	}
	var out []string
	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, strings.Fields(line)...)
	}
	return out
}
