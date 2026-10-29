package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// Plain mode draws the same layout in ASCII, with nothing animated.
func TestPlain(t *testing.T) {
	defer func() { plain = false }()
	SetPlain()
	a := demoApp(t, 120, 36)
	openDashboard(a)
	v := a.View()
	if strings.ContainsAny(v, "╭│─█▌▁◢\x1b") || !strings.Contains(v, "+---") {
		t.Errorf("not plain:\n%s", v)
	}
	for _, l := range strings.Split(v, "\n") {
		if lipgloss.Width(l) > 120 {
			t.Fatalf("a line grew: %q", l)
		}
	}
	if sp := newSpinner(); len(sp.Spinner.Frames) != 1 {
		t.Error("the spinner still turns")
	}
}
