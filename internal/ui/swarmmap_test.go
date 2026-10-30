package ui

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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

func TestHostPlace(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	localMirror.Store(true)
	defer localMirror.Store(false)
	s := newHostScreen()
	s.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	for _, r := range "bushe" {
		s.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	s.update(tea.KeyMsg{Type: tea.KeyEnter})
	if s.placing || s.home == nil || s.home.Town != "Bushehr" {
		t.Fatalf("picked %+v, still picking %v", s.home, s.placing)
	}
	if p := loadHome(); p == nil || *p != *s.home {
		t.Fatalf("saved %+v", p)
	}
	s.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	s.update(tea.KeyMsg{Type: tea.KeyEnter}) // nothing typed: back to the address
	if s.home != nil || loadHome() != nil {
		t.Fatal("not cleared")
	}
}

func TestHostStop(t *testing.T) {
	s := newHostScreen()
	s.form.get("mode").choice = 1
	s.form.get("host").input.SetValue("192.0.2.1")
	s.stats, s.statsFor = &deploy.HostStats{Installed: true, Active: true, RepoSize: 6 << 30}, targetKey(s.target())
	has := func(key string) bool {
		for _, k := range s.keys() {
			if k[0] == key {
				return true
			}
		}
		return false
	}
	if !has("ctrl+x") || !has("ctrl+d") {
		t.Fatalf("keys %v", s.keys())
	}
	s.update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if !s.confirmRemove || !strings.Contains(s.view(100, 40), "Remove hosting from 192.0.2.1?") {
		t.Fatal("ctrl+d should ask first")
	}
	s.update(tea.KeyMsg{Type: tea.KeyEsc})
	if s.confirmRemove || s.stopping {
		t.Fatal("esc should keep it")
	}
	s.update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if cmd := s.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}); cmd == nil || !s.stopping {
		t.Fatal("y should remove it") // the command isn't run: it would reach the target
	}
	s.update(hostStoppedMsg{removed: true})
	if s.stopping || !strings.Contains(s.view(100, 40), "removed") {
		t.Fatal("no word of the removal")
	}

	// interzone-mirror's node is docker's to stop, not interzone's.
	s.stats.Mirror = "mirror-ipfs-1"
	if has("ctrl+x") || has("ctrl+d") {
		t.Fatalf("keys %v on the mirror's node", s.keys())
	}
}
