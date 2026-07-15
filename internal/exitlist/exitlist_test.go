package exitlist

import (
	"bytes"
	"net/netip"
	"strings"
	"testing"
	"time"
)

const sampleList = `# Tor exit list
1.2.3.4
5.6.7.8
2001:db8::1

1.2.3.4
not-an-ip
`

const sampleExitAddresses = `ExitNode AAAA1111BBBB2222CCCC3333DDDD4444EEEE5555
Published 2026-07-14 10:00:00
LastStatus 2026-07-15 02:00:00
ExitAddress 1.2.3.4 2026-07-15 02:05:00
ExitNode FFFF6666
Published 2026-07-13 09:00:00
LastStatus 2026-07-15 01:00:00
ExitAddress 5.6.7.8 2026-07-15 01:05:00
ExitAddress not-an-ip 2026-07-15 01:06:00
`

func mustSources() Sources { return Sources{Bulk: BulkSource, ExitAddresses: ExitAddressesSource} }

func TestParseBulkList(t *testing.T) {
	addrs, skipped, err := ParseBulkList(strings.NewReader(sampleList))
	if err != nil {
		t.Fatalf("ParseBulkList: %v", err)
	}
	if skipped != 1 { // "not-an-ip"
		t.Errorf("skipped = %d, want 1", skipped)
	}
	if len(addrs) != 3 { // 1.2.3.4 (deduped), 5.6.7.8, 2001:db8::1
		t.Fatalf("len(addrs) = %d, want 3 (%v)", len(addrs), addrs)
	}
}

func TestParseExitAddresses(t *testing.T) {
	meta, skipped, err := ParseExitAddresses(strings.NewReader(sampleExitAddresses))
	if err != nil {
		t.Fatalf("ParseExitAddresses: %v", err)
	}
	if skipped != 1 { // "not-an-ip" ExitAddress
		t.Errorf("skipped = %d, want 1", skipped)
	}
	m, ok := meta[netip.MustParseAddr("1.2.3.4")]
	if !ok {
		t.Fatal("missing metadata for 1.2.3.4")
	}
	if m.Fingerprint != "AAAA1111BBBB2222CCCC3333DDDD4444EEEE5555" {
		t.Errorf("fingerprint = %q", m.Fingerprint)
	}
	if !m.Published.Equal(time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("published = %v", m.Published)
	}
	if !m.LastStatus.Equal(time.Date(2026, 7, 15, 2, 0, 0, 0, time.UTC)) {
		t.Errorf("last_status = %v", m.LastStatus)
	}
}

func TestContains(t *testing.T) {
	addrs, _, _ := ParseBulkList(strings.NewReader(sampleList))
	set := New(addrs, nil, time.Unix(1720000000, 0), mustSources())

	cases := []struct {
		ip   string
		want bool
	}{
		{"1.2.3.4", true},
		{"5.6.7.8", true},
		{"2001:db8::1", true},
		{"9.9.9.9", false},
		{"::ffff:1.2.3.4", true}, // v4-in-v6 must canonicalize and match
	}
	for _, tc := range cases {
		if got := set.Contains(netip.MustParseAddr(tc.ip)); got != tc.want {
			t.Errorf("Contains(%s) = %v, want %v", tc.ip, got, tc.want)
		}
	}
}

func TestMetaAttachedToMembership(t *testing.T) {
	addrs, _, _ := ParseBulkList(strings.NewReader(sampleList))
	meta, _, _ := ParseExitAddresses(strings.NewReader(sampleExitAddresses))
	set := New(addrs, meta, time.Unix(1720000000, 0), mustSources())

	if set.MetaCount() != 2 {
		t.Errorf("MetaCount = %d, want 2", set.MetaCount())
	}
	m, ok := set.Meta(netip.MustParseAddr("1.2.3.4"))
	if !ok || m.Fingerprint == "" {
		t.Errorf("Meta(1.2.3.4) = %+v, ok=%v", m, ok)
	}
	if _, ok := set.Meta(netip.MustParseAddr("2001:db8::1")); ok {
		t.Error("2001:db8::1 should have no metadata")
	}
}

func TestFamilyCounts(t *testing.T) {
	addrs, _, _ := ParseBulkList(strings.NewReader(sampleList))
	set := New(addrs, nil, time.Unix(1720000000, 0), mustSources())
	if v4, v6 := set.FamilyCounts(); v4 != 2 || v6 != 1 {
		t.Errorf("FamilyCounts = (%d, %d), want (2, 1)", v4, v6)
	}
}

func TestSerializeDeterministic(t *testing.T) {
	addrs, _, _ := ParseBulkList(strings.NewReader(sampleList))
	meta, _, _ := ParseExitAddresses(strings.NewReader(sampleExitAddresses))
	var a, b bytes.Buffer
	if err := Serialize(&a, addrs, meta, mustSources(), 1720000000); err != nil {
		t.Fatal(err)
	}
	// A different input order must yield identical bytes (addresses sorted, map
	// keys sorted by encoding/json).
	reversed := make([]netip.Addr, len(addrs))
	for i := range addrs {
		reversed[i] = addrs[len(addrs)-1-i]
	}
	if err := Serialize(&b, reversed, meta, mustSources(), 1720000000); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Errorf("Serialize not deterministic:\n%s\nvs\n%s", a.String(), b.String())
	}
}

func TestSerializeOpenRoundTrip(t *testing.T) {
	addrs, _, _ := ParseBulkList(strings.NewReader(sampleList))
	meta, _, _ := ParseExitAddresses(strings.NewReader(sampleExitAddresses))
	var buf bytes.Buffer
	if err := Serialize(&buf, addrs, meta, mustSources(), 1720000000); err != nil {
		t.Fatal(err)
	}
	set, err := Open(buf.Bytes())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if set.Len() != 3 {
		t.Errorf("Len = %d, want 3", set.Len())
	}
	if set.MetaCount() != 2 {
		t.Errorf("MetaCount = %d, want 2", set.MetaCount())
	}
	if !set.Generated().Equal(time.Unix(1720000000, 0).UTC()) {
		t.Errorf("Generated = %v", set.Generated())
	}
	if set.Sources().Bulk != BulkSource {
		t.Errorf("Sources.Bulk = %q", set.Sources().Bulk)
	}
	if !set.Contains(netip.MustParseAddr("2001:db8::1")) {
		t.Error("round-tripped set lost 2001:db8::1")
	}
	m, ok := set.Meta(netip.MustParseAddr("1.2.3.4"))
	if !ok || m.Fingerprint == "" {
		t.Errorf("round-tripped meta lost for 1.2.3.4: %+v ok=%v", m, ok)
	}
}

func TestOpenInvalid(t *testing.T) {
	if _, err := Open([]byte("not json")); err == nil {
		t.Error("Open(garbage) should error")
	}
}
