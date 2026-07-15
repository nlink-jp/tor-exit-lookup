package app

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/nlink-jp/tor-exit-lookup/internal/engine"
	"github.com/nlink-jp/tor-exit-lookup/internal/exitlist"
)

// checkJSON is the JSONL shape for `check --json` results (one object per line).
type checkJSON struct {
	IP            string     `json:"ip"`
	IsExit        bool       `json:"is_exit"`
	Error         string     `json:"error,omitempty"`
	Fingerprint   string     `json:"fingerprint,omitempty"`
	Published     *time.Time `json:"published,omitempty"`
	LastStatus    *time.Time `json:"last_status,omitempty"`
	CheckedAt     time.Time  `json:"checked_at"`
	ListUpdatedAt time.Time  `json:"list_updated_at"`
}

// emitResult writes one lookup result as text or a JSON line.
func emitResult(out io.Writer, ip string, r engine.Result, jsonOut bool, set *exitlist.Set, now time.Time) {
	if jsonOut {
		j := checkJSON{IP: ip, IsExit: r.IsExit, CheckedAt: now, ListUpdatedAt: set.Generated()}
		if r.HasMeta {
			j.Fingerprint = r.Meta.Fingerprint
			if !r.Meta.Published.IsZero() {
				p := r.Meta.Published
				j.Published = &p
			}
			if !r.Meta.LastStatus.IsZero() {
				l := r.Meta.LastStatus
				j.LastStatus = &l
			}
		}
		_ = jsonLine(out, j)
		return
	}
	if !r.IsExit {
		fmt.Fprintf(out, "%s is not a Tor Exit node\n", ip)
		return
	}
	line := ip + " is a Tor Exit node"
	if r.HasMeta {
		var extra string
		if fp := r.Meta.Fingerprint; fp != "" {
			extra = shortFingerprint(fp)
		}
		if !r.Meta.LastStatus.IsZero() {
			if extra != "" {
				extra += ", "
			}
			extra += "last seen " + r.Meta.LastStatus.Format("2006-01-02 15:04")
		}
		if extra != "" {
			line += "  [" + extra + "]"
		}
	}
	fmt.Fprintln(out, line)
}

// emitInvalid writes an invalid-address entry (batch/JSON mode only; the
// single-IP tri-state path reports invalid input on stderr instead).
func emitInvalid(out io.Writer, ip string, jsonOut bool, set *exitlist.Set, now time.Time) {
	if jsonOut {
		_ = jsonLine(out, checkJSON{IP: ip, IsExit: false, Error: "invalid address", CheckedAt: now, ListUpdatedAt: set.Generated()})
		return
	}
	fmt.Fprintf(out, "%s: invalid address\n", ip)
}

// shortFingerprint abbreviates a 40-hex Tor fingerprint for human output.
func shortFingerprint(fp string) string {
	if len(fp) > 12 {
		return fp[:12] + "…"
	}
	return fp
}

// jsonLine writes v as a single JSON line.
func jsonLine(w io.Writer, v any) error {
	return json.NewEncoder(w).Encode(v)
}
