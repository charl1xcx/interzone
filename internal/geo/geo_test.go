package geo

import (
	"net/netip"
	"testing"
)

func TestLocate(t *testing.T) {
	for _, c := range []struct{ ip, country string }{
		{"2.176.0.1", "IR"},    // TCI, Iran
		{"5.160.0.1", "IR"},    // Respina, Iran
		{"8.8.8.8", "US"},      // Google
		{"81.169.145.1", "DE"}, // Strato, Germany
	} {
		p, ok := Locate(netip.MustParseAddr(c.ip))
		if !ok || p.Country != c.country {
			t.Errorf("%s: %+v %v, want %s", c.ip, p, ok, c.country)
		}
		if c.country == "IR" && (p.Town == "" || p.Lat < 25 || p.Lat > 40 || p.Lon < 44 || p.Lon > 64) {
			t.Errorf("%s: %+v isn't a place in Iran", c.ip, p)
		}
	}
	for _, ip := range []string{"192.168.1.1", "127.0.0.1", "10.0.0.1", "fe80::1"} {
		if p, ok := Locate(netip.MustParseAddr(ip)); ok {
			t.Errorf("%s: %+v, want none", ip, p)
		}
	}
	if p, _ := Locate(netip.MustParseAddr("2.176.0.1")); p.ISP != "TCI" {
		t.Errorf("2.176.0.1 is TCI's, not %q", p.ISP)
	}
	if len(Provinces) < 31 {
		t.Errorf("%d provinces", len(Provinces))
	}
	if len(Shapes) == 0 || Shapes[0].Country != "IR" {
		t.Fatal("Iran's border should come first")
	}
}
