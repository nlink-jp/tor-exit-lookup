package app

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/nlink-jp/tor-exit-lookup/internal/engine"
	"github.com/nlink-jp/tor-exit-lookup/internal/exitlist"
	"github.com/nlink-jp/tor-exit-lookup/internal/mcp"
)

// ---- check ----------------------------------------------------------------

func cmdCheck(args []string) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	var c commonFlags
	c.register(fs, false)
	var jsonOut, noUpdate bool
	fs.BoolVar(&jsonOut, "json", false, "JSON Lines output")
	fs.BoolVar(&jsonOut, "j", false, "JSON Lines output (shorthand)")
	fs.BoolVar(&noUpdate, "no-update", false, "do not auto-refetch even if the list is stale")
	positionals, err := parseInterspersed(fs, args)
	if err != nil {
		return exitError
	}
	e, err := c.buildEngine()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitError
	}
	return runCheck(context.Background(), os.Stdout, os.Stderr, os.Stdin, e, jsonOut, noUpdate, positionals)
}

// runCheck evaluates each input against the exit list. A single positional IP in
// text mode uses grep-style tri-state exit codes (0/1/2). Any other shape —
// multiple IPs, stdin input, or --json — is batch mode: results go to stdout and
// the exit code signals errors only (0 / 2).
func runCheck(ctx context.Context, out, errw io.Writer, stdin io.Reader, e *engine.Engine, jsonOut, noUpdate bool, args []string) int {
	tristate := len(args) == 1 && !jsonOut
	inputs := readInputs(args, stdin)
	if len(inputs) == 0 {
		fmt.Fprintln(errw, "no IP addresses given (pass as an argument or on stdin)")
		return exitError
	}
	set, code := obtainSet(ctx, e, errw, noUpdate)
	if code != 0 {
		return code
	}
	now := time.Now().UTC()
	for _, in := range inputs {
		r, err := engine.Lookup(set, in)
		if err != nil { // invalid IP
			if tristate {
				fmt.Fprintf(errw, "error: %v\n", err)
				return exitError
			}
			emitInvalid(out, in, jsonOut, set, now)
			continue
		}
		emitResult(out, in, r, jsonOut, set, now)
		if tristate {
			if r.IsExit {
				return exitIsExit
			}
			return exitNotExit
		}
	}
	return 0
}

// obtainSet loads the exit list, auto-refetching first when enabled and the
// cached copy is missing or stale. A refetch failure with an existing cache is a
// soft warning; only a total absence of data is a hard error.
func obtainSet(ctx context.Context, e *engine.Engine, errw io.Writer, noUpdate bool) (*exitlist.Set, int) {
	if !e.Cfg.AutoUpdate || noUpdate {
		set, code := loadListOrHint(e, errw)
		if code != 0 {
			return nil, code
		}
		warnIfStale(e, set, errw)
		return set, 0
	}
	set, _, err := e.EnsureFresh(ctx, e.Cfg.TTL)
	if err != nil {
		if set == nil {
			fmt.Fprintf(errw, "no local exit list and refetch failed: %v\nrun 'tor-exit-lookup update'.\n", err)
			return nil, exitError
		}
		fmt.Fprintf(errw, "warning: auto-update failed (%v); using cached list (generated %s)\n",
			err, set.Generated().Format("2006-01-02 15:04"))
	}
	return set, 0
}

// ---- update ---------------------------------------------------------------

func cmdUpdate(args []string) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	var c commonFlags
	c.register(fs, true)
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	e, err := c.buildEngine()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitError
	}
	return runUpdate(os.Stdout, os.Stderr, e)
}

func runUpdate(out, errw io.Writer, e *engine.Engine) int {
	fmt.Fprintf(errw, "downloading %s …\n", e.Cfg.BulkURL)
	res, err := e.Update(context.Background())
	if err != nil {
		fmt.Fprintf(errw, "update failed: %v\n", err)
		return exitError
	}
	fmt.Fprintf(out, "updated %s\n", e.Cfg.StorePath)
	fmt.Fprintf(out, "  generated:  %s\n", res.Generated.Format(time.RFC3339))
	fmt.Fprintf(out, "  exit nodes: %d  (v4: %d, v6: %d)  skipped: %d\n",
		res.Count, res.V4Count, res.V6Count, res.Skipped)
	if e.Cfg.ExitAddressesURL != "" {
		fmt.Fprintf(out, "  metadata:   %d nodes\n", res.MetaCount)
	}
	if res.MetaWarning != "" {
		fmt.Fprintf(errw, "warning: metadata source unavailable (%s); membership still updated\n", res.MetaWarning)
	}
	return 0
}

// ---- status ---------------------------------------------------------------

func cmdStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	var c commonFlags
	c.register(fs, false)
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	e, err := c.buildEngine()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitError
	}
	return runStatus(os.Stdout, os.Stderr, e)
}

// runStatus reports the cached list's state as-is; it never triggers a fetch.
func runStatus(out, errw io.Writer, e *engine.Engine) int {
	fmt.Fprintf(out, "store:   %s\n", e.Cfg.StorePath)
	fmt.Fprintf(out, "source:  %s\n", e.Cfg.BulkURL)
	if e.Cfg.ExitAddressesURL != "" {
		fmt.Fprintf(out, "metadata source: %s\n", e.Cfg.ExitAddressesURL)
	}
	set, err := e.LoadList()
	if err != nil {
		if isNoList(err) {
			fmt.Fprintln(out, "status:  NO LIST — run 'tor-exit-lookup update'")
		} else {
			fmt.Fprintf(errw, "status:  ERROR — %v\n", err)
		}
		return exitError
	}
	v4, v6 := set.FamilyCounts()
	fmt.Fprintf(out, "generated:  %s\n", set.Generated().Format(time.RFC3339))
	fmt.Fprintf(out, "exit nodes: %d  (v4: %d, v6: %d)\n", set.Len(), v4, v6)
	fmt.Fprintf(out, "metadata:   %d nodes\n", set.MetaCount())
	if stale, age := e.IsStale(set.Generated()); stale {
		fmt.Fprintf(out, "status:  STALE — %s old; run 'tor-exit-lookup update'\n", roundAge(age))
	} else {
		fmt.Fprintln(out, "status:  OK")
	}
	return 0
}

// ---- mcp ------------------------------------------------------------------

func cmdMCP(args []string, version string) int {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	var c commonFlags
	c.register(fs, true)
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	e, err := c.buildEngine()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitError
	}
	if err := mcp.Serve(context.Background(), e, version, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "mcp: %v\n", err)
		return exitError
	}
	return 0
}

// ---- helpers --------------------------------------------------------------

// warnIfStale prints a freshness warning to errw; it never updates on its own.
func warnIfStale(e *engine.Engine, set *exitlist.Set, errw io.Writer) {
	if stale, age := e.IsStale(set.Generated()); stale {
		fmt.Fprintf(errw, "warning: exit list is %s old (generated %s); run 'tor-exit-lookup update'\n",
			roundAge(age), set.Generated().Format("2006-01-02 15:04"))
	}
}

// roundAge renders a duration as a coarse human string (hours or days).
func roundAge(d time.Duration) string {
	if d >= 48*time.Hour {
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	}
	return fmt.Sprintf("%d hours", int(d.Hours()))
}
