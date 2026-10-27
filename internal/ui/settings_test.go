package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Settings is its own step, and scrolling down through its last box (the
// extra vars) stays on it instead of moving on to Review.
func TestSettingsStep(t *testing.T) {
	a := demoApp(t, 120, 36)
	a.jump(a.index("Components"))
	press(a, enterMsg)
	if a.cur().name() != "Settings" {
		t.Fatalf("enter on Components led to %s", a.cur().name())
	}
	f := a.cur().(*settingsScreen).form
	for f.move(1) {
	}
	down := tea.KeyMsg{Type: tea.KeyDown}
	for range 5 {
		press(a, down)
		deliver(a, tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress, X: 60, Y: 20})
	}
	if a.cur().name() != "Settings" {
		t.Fatalf("scrolling down moved on to %s", a.cur().name())
	}
	press(a, tea.KeyMsg{Type: tea.KeyEsc})
	if a.cur().name() != "Components" {
		t.Fatalf("esc led to %s", a.cur().name())
	}
}
