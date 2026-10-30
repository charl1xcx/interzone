package ui

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"interzone/internal/deploy"
)

func TestSwarmMap(t *testing.T) {
	st := &deploy.HostStats{Self: []netip.Addr{netip.MustParseAddr("2.176.0.1")}, PeerList: []deploy.Peer{
		{ID: "a", Addr: netip.MustParseAddr("5.160.0.1")},
		{ID: "b", Addr: netip.MustParseAddr("81.169.145.1")}, // Germany: on the edge
		{ID: "c", Addr: netip.MustParseAddr("10.0.0.1")},     // nowhere: "?"
	}}
	rows := swarmMap(mapScene{st: st, rates: map[string]peerRate{"b": {out: 1e6}}, phase: 0.5}, 80, 24)
	if len(rows) != 24 {
		t.Fatalf("%d rows, want 24", len(rows))
	}
	text := ansi.Strip(strings.Join(rows, "\n"))
	for _, want := range []string{"◆you", "DE", "?", "DB-IP"} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q in\n%s", want, text)
		}
	}
	for _, r := range rows {
		if w := ansi.StringWidth(r); w > 80 {
			t.Errorf("a row is %d wide", w)
		}
	}
}

func TestReadLedgers(t *testing.T) {
	s := &hostScreen{}
	t0 := time.Now()
	s.readLedgers(&deploy.HostStats{PeerList: []deploy.Peer{{ID: "a", Sent: 100, Recv: 1000}}}, t0)
	s.readLedgers(&deploy.HostStats{PeerList: []deploy.Peer{{ID: "a", Sent: 600, Recv: 1000}, {ID: "b", Recv: 50}}}, t0.Add(5*time.Second))
	if r := s.rates["a"]; r.out != 100 || r.in != 0 {
		t.Fatalf("a: %+v", r)
	}
	if _, ok := s.rates["b"]; ok || !s.moving() {
		t.Fatalf("a new peer has no rate yet: %+v", s.rates)
	}
	s.readLedgers(&deploy.HostStats{PeerList: []deploy.Peer{{ID: "b", Recv: 50}}}, t0.Add(10*time.Second))
	if len(s.comings) != 2 || !s.comings[0].join || s.comings[0].peer.ID != "b" || s.comings[1].join || s.comings[1].peer.ID != "a" {
		t.Fatalf("b came, then a left: %+v", s.comings)
	}
	if len(s.ripples) != 2 || len(s.peerHist["b"]) != 1 {
		t.Fatalf("ripples %+v, history %+v", s.ripples, s.peerHist)
	}
}
