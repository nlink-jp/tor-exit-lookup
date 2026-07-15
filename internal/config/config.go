// Package config resolves tor-exit-lookup settings from a sectioned TOML file
// plus environment overrides. It parses only the small TOML subset the tool
// needs, keeping the binary free of external dependencies.
//
// Unlike its siblings, tor-exit-lookup needs no credentials: the torbulkexitlist
// endpoint is public, so there is no token or API key to configure.
package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultBulkURL is the Tor Project torbulkexitlist endpoint (plain IP list,
	// membership; public, no authentication).
	DefaultBulkURL = "https://check.torproject.org/torbulkexitlist"
	// DefaultExitAddressesURL is the exit-addresses endpoint (per-node metadata;
	// public, no authentication).
	DefaultExitAddressesURL = "https://check.torproject.org/exit-addresses"

	// DefaultTTL is how old the cached list may be before an auto-refetch is
	// triggered on the next check/status (when AutoUpdate is on).
	DefaultTTL = time.Hour
	// MinTTL is the floor on TTL. The Tor Project asks clients not to poll
	// excessively (the list refreshes ~every 30 min), so an auto-refetch cannot
	// fire more often than this regardless of configuration.
	MinTTL = 30 * time.Minute
)

// Config holds resolved runtime settings.
type Config struct {
	BulkURL          string        // torbulkexitlist download URL (membership)
	ExitAddressesURL string        // exit-addresses download URL (metadata; "" disables)
	StorePath        string        // path to the local cached exit-list store (JSON)
	Workspace        string        // default MCP output directory (reserved)
	TTL              time.Duration // auto-refetch threshold (floored at MinTTL)
	AutoUpdate       bool          // auto-refetch on check/status when older than TTL
}

// Load resolves configuration. If configPath is empty the default location
// (~/.config/tor-exit-lookup/config.toml) is used when present. Environment
// variables override file values, and any explicit non-empty override* argument
// wins over both.
func Load(configPath, storeOverride, urlOverride string) (*Config, error) {
	cfg := &Config{
		BulkURL:          DefaultBulkURL,
		ExitAddressesURL: DefaultExitAddressesURL,
		StorePath:        DefaultStorePath(),
		Workspace:        DefaultWorkspaceDir(),
		TTL:              DefaultTTL,
		AutoUpdate:       true,
	}

	if configPath == "" {
		configPath = DefaultConfigPath()
	}
	if configPath != "" {
		if f, err := os.Open(configPath); err == nil {
			defer f.Close()
			sections, perr := parseTOML(f)
			if perr != nil {
				return nil, fmt.Errorf("parse config %s: %w", configPath, perr)
			}
			if aerr := applySections(cfg, sections); aerr != nil {
				return nil, fmt.Errorf("config %s: %w", configPath, aerr)
			}
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("open config %s: %w", configPath, err)
		}
	}

	// Environment overrides.
	if v := firstEnv("TOR_EXIT_LOOKUP_URL"); v != "" {
		cfg.BulkURL = v
	}
	if v := firstEnv("TOR_EXIT_LOOKUP_EXIT_ADDRESSES_URL"); v != "" {
		cfg.ExitAddressesURL = v
	}
	if v := firstEnv("TOR_EXIT_LOOKUP_STORE", "TOR_EXIT_LOOKUP_DB"); v != "" {
		cfg.StorePath = v
	}
	if v := firstEnv("TOR_EXIT_LOOKUP_WORKSPACE"); v != "" {
		cfg.Workspace = v
	}
	if v := firstEnv("TOR_EXIT_LOOKUP_TTL_MINUTES"); v != "" {
		d, err := parseTTLMinutes(v)
		if err != nil {
			return nil, fmt.Errorf("TOR_EXIT_LOOKUP_TTL_MINUTES: %w", err)
		}
		cfg.TTL = d
	}
	if v := firstEnv("TOR_EXIT_LOOKUP_AUTO_UPDATE"); v != "" {
		if b, ok := parseBool(v); ok {
			cfg.AutoUpdate = b
		}
	}

	// Explicit flag overrides win.
	if urlOverride != "" {
		cfg.BulkURL = urlOverride
	}
	if storeOverride != "" {
		cfg.StorePath = storeOverride
	}

	// Enforce the polling-etiquette floor on TTL.
	if cfg.TTL < MinTTL {
		cfg.TTL = MinTTL
	}

	return cfg, nil
}

func applySections(cfg *Config, sections map[string]map[string]string) error {
	if t := sections["torproject"]; t != nil {
		if v := t["bulk_url"]; v != "" {
			cfg.BulkURL = v
		}
		if v := t["exit_addresses_url"]; v != "" {
			cfg.ExitAddressesURL = v
		}
		if v := t["ttl_minutes"]; v != "" {
			d, err := parseTTLMinutes(v)
			if err != nil {
				return fmt.Errorf("[torproject] ttl_minutes: %w", err)
			}
			cfg.TTL = d
		}
		if v := t["auto_update"]; v != "" {
			if b, ok := parseBool(v); ok {
				cfg.AutoUpdate = b
			}
		}
	}
	if s := sections["store"]; s != nil {
		if v := s["path"]; v != "" {
			cfg.StorePath = expandHome(v)
		}
	}
	if m := sections["mcp"]; m != nil {
		if v := m["workspace"]; v != "" {
			cfg.Workspace = expandHome(v)
		}
	}
	return nil
}

// parseTTLMinutes parses a non-negative minutes value into a Duration.
func parseTTLMinutes(v string) (time.Duration, error) {
	m, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", v)
	}
	if m < 0 {
		return 0, fmt.Errorf("must not be negative")
	}
	return time.Duration(m * float64(time.Minute)), nil
}

// parseBool accepts the common truthy/falsey spellings.
func parseBool(v string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	}
	return false, false
}

// DefaultConfigPath returns the default config file location, honoring
// XDG_CONFIG_HOME.
func DefaultConfigPath() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "tor-exit-lookup", "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "tor-exit-lookup", "config.toml")
}

// DefaultStorePath returns the default exit-list store location, honoring
// XDG_DATA_HOME.
func DefaultStorePath() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "tor-exit-lookup", "exitlist.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "exitlist.json"
	}
	return filepath.Join(home, ".local", "share", "tor-exit-lookup", "exitlist.json")
}

// DefaultWorkspaceDir returns the default MCP output directory, honoring
// XDG_STATE_HOME (file-mediated results are reproducible, transient state).
func DefaultWorkspaceDir() string {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "tor-exit-lookup", "workspace")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "tor-exit-lookup", "workspace")
}

func firstEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// parseTOML parses the minimal subset tor-exit-lookup needs: [section] headers
// and key = value lines, where value is an optionally quoted string. Comments
// start with '#'. It intentionally does not support arrays, nested tables, or
// typed values.
func parseTOML(r io.Reader) (map[string]map[string]string, error) {
	sections := map[string]map[string]string{}
	current := "" // top-level keys land in the "" section
	sections[current] = map[string]string{}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		if strings.HasPrefix(raw, "[") {
			end := strings.IndexByte(raw, ']')
			if end < 0 {
				return nil, fmt.Errorf("line %d: unterminated section header", line)
			}
			current = strings.TrimSpace(raw[1:end])
			if _, ok := sections[current]; !ok {
				sections[current] = map[string]string{}
			}
			continue
		}
		eq := strings.IndexByte(raw, '=')
		if eq < 0 {
			return nil, fmt.Errorf("line %d: expected key = value", line)
		}
		key := strings.TrimSpace(raw[:eq])
		val := parseValue(strings.TrimSpace(raw[eq+1:]))
		if key == "" {
			return nil, fmt.Errorf("line %d: empty key", line)
		}
		sections[current][key] = val
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return sections, nil
}

// parseValue strips surrounding quotes, or trims a trailing inline comment from
// a bare value.
func parseValue(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') {
		q := v[0]
		if end := strings.IndexByte(v[1:], q); end >= 0 {
			return v[1 : 1+end]
		}
	}
	if hash := strings.IndexByte(v, '#'); hash >= 0 {
		v = strings.TrimSpace(v[:hash])
	}
	return v
}
