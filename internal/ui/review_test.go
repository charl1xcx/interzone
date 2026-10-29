package ui

import (
	"strings"
	"testing"
)

// z on Review swaps vars.yml for the DNS records to create, and back.
func TestReviewZone(t *testing.T) {
	a := demoApp(t, 120, 36)
	a.jump(a.index("Review"))
	s := a.cur().(*reviewScreen)
	press(a, runes("z"))
	if !strings.Contains(strings.Join(s.zone, "\n"), "IN A") || !strings.Contains(s.previewTitle(), "DNS records") {
		t.Fatalf("no records shown: %q", s.zone)
	}
	press(a, runes("z"))
	if s.zone != nil || strings.Contains(s.previewTitle(), "DNS records") {
		t.Errorf("z didn't go back to vars.yml")
	}
}
