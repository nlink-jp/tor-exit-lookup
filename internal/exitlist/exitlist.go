// Package exitlist parses the Tor Project's exit-list downloads into an
// in-memory membership set and persists it as a small JSON store on disk.
//
// Two sources are combined. torbulkexitlist (a plain, one-IP-per-line file) is
// the authoritative membership set: an address is a Tor Exit node if and only if
// it appears there. exit-addresses (a block-structured file) supplies optional
// per-node metadata — fingerprint, published, and last-status times — keyed by
// exit address; it enriches a hit but never changes membership. A missing
// metadata entry is tolerated.
//
// Membership testing is a hash-set lookup, so a few thousand exit addresses
// answer instantly with no index. The on-disk store carries its own generation
// timestamp inside the record (not the file mtime), so freshness survives copies
// and backups, and serialization is deterministic (addresses and metadata keys
// are sorted, the generation time is injected).
package exitlist

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"sort"
	"strings"
	"time"
)

const (
	// BulkSource is the canonical torbulkexitlist endpoint (membership).
	BulkSource = "https://check.torproject.org/torbulkexitlist"
	// ExitAddressesSource is the canonical exit-addresses endpoint (metadata).
	ExitAddressesSource = "https://check.torproject.org/exit-addresses"

	// exitAddrTimeLayout is the timestamp format used in exit-addresses lines.
	exitAddrTimeLayout = "2006-01-02 15:04:05"
)

// Sources records where each part of the list came from, for provenance.
type Sources struct {
	Bulk          string `json:"bulk"`
	ExitAddresses string `json:"exit_addresses,omitempty"`
}

// Meta is the optional per-node metadata attached to an exit address.
type Meta struct {
	Fingerprint string    `json:"fingerprint,omitempty"`
	Published   time.Time `json:"published,omitempty"`
	LastStatus  time.Time `json:"last_status,omitempty"`
}

// empty reports whether the metadata carries no information.
func (m Meta) empty() bool {
	return m.Fingerprint == "" && m.Published.IsZero() && m.LastStatus.IsZero()
}

// Set is an immutable membership set of Tor Exit node addresses plus optional
// metadata and provenance.
type Set struct {
	addrs     map[netip.Addr]struct{}
	meta      map[netip.Addr]Meta
	generated time.Time
	sources   Sources
}

// New builds a Set from the given membership addresses and (optional) metadata,
// stamping provenance. Duplicate and invalid addresses are dropped. Metadata is
// canonicalized to the same unmapped key space as membership.
func New(addrs []netip.Addr, meta map[netip.Addr]Meta, generated time.Time, sources Sources) *Set {
	m := make(map[netip.Addr]struct{}, len(addrs))
	for _, a := range addrs {
		if a.IsValid() {
			m[a.Unmap()] = struct{}{}
		}
	}
	var md map[netip.Addr]Meta
	if len(meta) > 0 {
		md = make(map[netip.Addr]Meta, len(meta))
		for a, v := range meta {
			if a.IsValid() && !v.empty() {
				md[a.Unmap()] = v
			}
		}
	}
	return &Set{addrs: m, meta: md, generated: generated, sources: sources}
}

// Contains reports whether addr is a known Tor Exit node address. The address is
// compared in its canonical (unmapped) form so a v4-in-v6 input still matches.
func (s *Set) Contains(addr netip.Addr) bool {
	_, ok := s.addrs[addr.Unmap()]
	return ok
}

// Meta returns the metadata for addr, if any was recorded from exit-addresses.
func (s *Set) Meta(addr netip.Addr) (Meta, bool) {
	if s.meta == nil {
		return Meta{}, false
	}
	m, ok := s.meta[addr.Unmap()]
	return m, ok
}

// Len returns the number of exit addresses.
func (s *Set) Len() int { return len(s.addrs) }

// MetaCount returns how many addresses carry metadata.
func (s *Set) MetaCount() int { return len(s.meta) }

// Generated returns when the underlying list was fetched.
func (s *Set) Generated() time.Time { return s.generated }

// Sources returns the endpoints the list was fetched from.
func (s *Set) Sources() Sources { return s.sources }

// FamilyCounts returns how many membership addresses are IPv4 vs IPv6.
func (s *Set) FamilyCounts() (v4, v6 int) {
	for a := range s.addrs {
		if a.Is4() {
			v4++
		} else {
			v6++
		}
	}
	return v4, v6
}

// ParseBulkList reads a torbulkexitlist body (one IP per line) and returns the
// parsed, canonicalized addresses. Blank lines and '#' comments are skipped;
// unparseable lines are counted in skipped rather than failing the whole parse.
func ParseBulkList(r io.Reader) (addrs []netip.Addr, skipped int, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	seen := make(map[netip.Addr]struct{})
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		a, perr := netip.ParseAddr(line)
		if perr != nil {
			skipped++
			continue
		}
		a = a.Unmap()
		if _, dup := seen[a]; dup {
			continue
		}
		seen[a] = struct{}{}
		addrs = append(addrs, a)
	}
	if err := sc.Err(); err != nil {
		return nil, skipped, err
	}
	return addrs, skipped, nil
}

// ParseExitAddresses reads an exit-addresses body and returns per-address
// metadata keyed by exit address. The format is block-structured:
//
//	ExitNode <fingerprint>
//	Published <YYYY-MM-DD HH:MM:SS>
//	LastStatus <YYYY-MM-DD HH:MM:SS>
//	ExitAddress <ip> <YYYY-MM-DD HH:MM:SS>
//
// A block may carry several ExitAddress lines; each inherits the block's
// ExitNode / Published / LastStatus. Unparseable ExitAddress lines are counted
// in skipped rather than failing the whole parse.
func ParseExitAddresses(r io.Reader) (meta map[netip.Addr]Meta, skipped int, err error) {
	meta = make(map[netip.Addr]Meta)
	var cur Meta
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "ExitNode":
			cur = Meta{}
			if len(fields) >= 2 {
				cur.Fingerprint = fields[1]
			}
		case "Published":
			cur.Published = parseExitAddrTime(fields[1:])
		case "LastStatus":
			cur.LastStatus = parseExitAddrTime(fields[1:])
		case "ExitAddress":
			if len(fields) < 2 {
				skipped++
				continue
			}
			addr, perr := netip.ParseAddr(fields[1])
			if perr != nil {
				skipped++
				continue
			}
			meta[addr.Unmap()] = cur
		}
	}
	if err := sc.Err(); err != nil {
		return nil, skipped, err
	}
	return meta, skipped, nil
}

// parseExitAddrTime parses the "YYYY-MM-DD HH:MM:SS" date+time carried across two
// whitespace-separated fields, returning the zero time when it cannot.
func parseExitAddrTime(fields []string) time.Time {
	if len(fields) < 2 {
		return time.Time{}
	}
	t, err := time.Parse(exitAddrTimeLayout, fields[0]+" "+fields[1])
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// storeFile is the on-disk JSON shape of a persisted Set.
type storeFile struct {
	GeneratedAt time.Time       `json:"generated_at"`
	Sources     Sources         `json:"sources"`
	Count       int             `json:"count"`
	Exits       []string        `json:"exits"`
	Meta        map[string]Meta `json:"meta,omitempty"`
}

// Serialize writes the membership addresses and metadata as the on-disk store,
// stamping generatedUnix as the generation time (injected so the build is
// deterministic and testable). Addresses are sorted and metadata keys serialize
// in sorted order (encoding/json sorts map keys), so identical input yields
// byte-identical output.
func Serialize(w io.Writer, addrs []netip.Addr, meta map[netip.Addr]Meta, sources Sources, generatedUnix int64) error {
	sorted := make([]netip.Addr, len(addrs))
	copy(sorted, addrs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Less(sorted[j]) })

	exits := make([]string, len(sorted))
	for i, a := range sorted {
		exits[i] = a.String()
	}

	var metaOut map[string]Meta
	if len(meta) > 0 {
		metaOut = make(map[string]Meta, len(meta))
		for a, m := range meta {
			if !m.empty() {
				metaOut[a.Unmap().String()] = m
			}
		}
	}

	sf := storeFile{
		GeneratedAt: time.Unix(generatedUnix, 0).UTC(),
		Sources:     sources,
		Count:       len(exits),
		Exits:       exits,
		Meta:        metaOut,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(sf)
}

// Open parses an on-disk store back into a Set.
func Open(data []byte) (*Set, error) {
	var sf storeFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, fmt.Errorf("parse exit-list store: %w", err)
	}
	addrs := make([]netip.Addr, 0, len(sf.Exits))
	for _, s := range sf.Exits {
		a, err := netip.ParseAddr(s)
		if err != nil {
			// A malformed entry in our own store is not fatal — skip it.
			continue
		}
		addrs = append(addrs, a.Unmap())
	}
	var meta map[netip.Addr]Meta
	if len(sf.Meta) > 0 {
		meta = make(map[netip.Addr]Meta, len(sf.Meta))
		for s, m := range sf.Meta {
			if a, err := netip.ParseAddr(s); err == nil {
				meta[a.Unmap()] = m
			}
		}
	}
	return New(addrs, meta, sf.GeneratedAt, sf.Sources), nil
}
