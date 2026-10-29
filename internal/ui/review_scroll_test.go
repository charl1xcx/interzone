package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"interzone/internal/deploy"
)

// With many checks the preview doesn't fit; scrolling it then must not
// crash once the window grows, and z still shows the DNS records.
func TestReviewHiddenPreview(t *testing.T) {
	a := demoApp(t, 120, 45)
	a.jump(a.index("Review"))
	s := a.cur().(*reviewScreen)
	for i := range 12 {
		s.checks = append(s.checks, deploy.Finding{Group: "Clients", Name: fmt.Sprint("Check ", i), Level: deploy.Fail,
			Detail: "a long reason that wraps onto a second line of the left column, as real failures do"})
	}
	s.view(92, 40)
	for range 40 {
		s.update(tea.KeyMsg{Type: tea.KeyDown})
	}
	s.view(92, 70) // panicked: the offset had run past the end
	s.view(150, 70)

	s.update(zoneMsg{"matrix.example.org. 3600 IN A 203.0.113.7"})
	if out := s.view(92, 40); !strings.Contains(out, "IN A 203.0.113.7") || !strings.Contains(out, "a taller window shows them") {
		t.Errorf("z shows no records:\n%s", out)
	}
}
