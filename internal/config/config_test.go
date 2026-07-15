package config

import (
	"strings"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // no config file present there
	cfg, err := Load("", "", "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.BulkURL != DefaultBulkURL {
		t.Errorf("BulkURL = %q, want default", cfg.BulkURL)
	}
	if cfg.StorePath == "" {
		t.Error("StorePath should have a default")
	}
}

func TestEnvOverride(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TOR_EXIT_LOOKUP_URL", "https://mirror.test/list")
	t.Setenv("TOR_EXIT_LOOKUP_STORE", "/tmp/custom.json")
	cfg, err := Load("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BulkURL != "https://mirror.test/list" {
		t.Errorf("BulkURL = %q", cfg.BulkURL)
	}
	if cfg.StorePath != "/tmp/custom.json" {
		t.Errorf("StorePath = %q", cfg.StorePath)
	}
}

func TestFlagOverrideWins(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TOR_EXIT_LOOKUP_URL", "https://env.test/list")
	cfg, err := Load("", "/flag/store.json", "https://flag.test/list")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BulkURL != "https://flag.test/list" {
		t.Errorf("flag URL should win, got %q", cfg.BulkURL)
	}
	if cfg.StorePath != "/flag/store.json" {
		t.Errorf("flag store should win, got %q", cfg.StorePath)
	}
}

func TestParseTOMLSections(t *testing.T) {
	in := `
[torproject]
bulk_url = "https://toml.test/list"

[store]
path = "/from/toml.json"
`
	sections, err := parseTOML(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parseTOML: %v", err)
	}
	if sections["torproject"]["bulk_url"] != "https://toml.test/list" {
		t.Errorf("bulk_url = %q", sections["torproject"]["bulk_url"])
	}
	if sections["store"]["path"] != "/from/toml.json" {
		t.Errorf("path = %q", sections["store"]["path"])
	}
}

func TestTTLFloor(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TOR_EXIT_LOOKUP_TTL_MINUTES", "5") // below the 30-min floor
	cfg, err := Load("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TTL != MinTTL {
		t.Errorf("TTL = %v, want floored to %v", cfg.TTL, MinTTL)
	}
}

func TestTTLDefaultAndAutoUpdate(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg, err := Load("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TTL != DefaultTTL {
		t.Errorf("TTL = %v, want default %v", cfg.TTL, DefaultTTL)
	}
	if !cfg.AutoUpdate {
		t.Error("AutoUpdate should default to true")
	}
	if cfg.ExitAddressesURL != DefaultExitAddressesURL {
		t.Errorf("ExitAddressesURL = %q", cfg.ExitAddressesURL)
	}
}

func TestAutoUpdateEnvOff(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TOR_EXIT_LOOKUP_AUTO_UPDATE", "false")
	cfg, err := Load("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AutoUpdate {
		t.Error("AutoUpdate should be false when env says false")
	}
}

func TestParseValueStripsCommentAndQuotes(t *testing.T) {
	if got := parseValue(`"quoted"`); got != "quoted" {
		t.Errorf("quoted = %q", got)
	}
	if got := parseValue(`bare # trailing`); got != "bare" {
		t.Errorf("bare = %q", got)
	}
}
