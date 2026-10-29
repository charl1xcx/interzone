package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"interzone/internal/deploy"
)

// The page scrolls as one, whatever the window does, and z scrolls to the
// DNS records.
func TestReviewScrolls(t *testing.T) {
	a := demoApp(t, 120, 45)
	a.jump(a.index("Review"))
	s := a.cur().(*reviewScreen)
	for i := range 12 {
		s.checks = append(s.checks, deploy.Finding{Group: "Clients", Name: fmt.Sprint("Check ", i), Level: deploy.Fail,
			Detail: "a long reason that wraps onto a second line of the left column, as real failures do"})
	}
	s.view(92, 40)
	for range 400 {
		s.update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if out := s.view(92, 40); !strings.Contains(out, "more above") || strings.Contains(out, "more · ↑↓") {
		t.Errorf("not at the bottom:\n%s", out)
	}
	s.view(92, 200)
	s.view(150, 70)

	s.update(zoneMsg{"matrix.example.org. 3600 IN A 203.0.113.7"})
	if out := s.view(92, 40); !strings.Contains(out, "IN A 203.0.113.7") {
		t.Errorf("z shows no records:\n%s", out)
	}
}

// Enter only asks; y deploys, anything else steps back.
func TestReviewConfirms(t *testing.T) {
	a := demoApp(t, 120, 45)
	a.jump(a.index("Review"))
	s := a.cur().(*reviewScreen)
	s.missing = nil
	s.checks = []deploy.Finding{{Group: "Server", Name: "Connect", Level: deploy.Fail, Detail: "no"}}
	if cmd := s.update(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil || !s.confirm {
		t.Fatal("enter didn't just ask")
	}
	out := s.view(92, 40)
	if !strings.Contains(out, "Install Matrix on this machine?") || !strings.Contains(out, "1 check(s) failed: Connect") {
		t.Errorf("the question:\n%s", out)
	}
	if cmd := s.update(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil || s.confirm {
		t.Error("a second enter deployed, or kept asking")
	}
	s.update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd := s.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}); cmd == nil {
		t.Error("y didn't deploy")
	}
}
