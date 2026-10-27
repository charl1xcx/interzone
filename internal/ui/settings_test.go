package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Settings is its own step. tab runs off the details onto the extra vars,
// one box per component, and scrolling down through the last box stays on
// the step instead of moving on to Review.
func TestSettingsStep(t *testing.T) {
	a := demoApp(t, 120, 36)
	a.cfg.ExtraYAML = "docker_daemon_options: {}\nmatrix_client_element_hostname: chat.example.org"
	a.jump(a.index("Components"))
	press(a, enterMsg)
	s, ok := a.cur().(*settingsScreen)
	if !ok {
		t.Fatalf("enter on Components led to %s", a.cur().name())
	}
	tab := tea.KeyMsg{Type: tea.KeyTab}
	for range len(s.form.fields) {
		press(a, tab)
	}
	if !s.right || s.adv.current().label != s.extra[0].Name {
		t.Fatalf("tab off the end: right %v", s.right)
	}
	el := -1
	for i, g := range s.extra {
		if g.Name == "Element Web" {
			el = i
		}
	}
	if el < 0 || !strings.Contains(s.extra[el].Text, "chat.example.org") {
		t.Fatalf("Element's box: %+v", s.extra)
	}
	for s.adv.current().label != "Element Web" {
		press(a, tab)
	}
	press(a, tea.KeyMsg{Type: tea.KeyCtrlE}, enterMsg, runes("matrix_client_element_disable_custom_urls: true"))

	down := tea.KeyMsg{Type: tea.KeyDown}
	for range 40 {
		press(a, down)
		deliver(a, tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress, X: 90, Y: 20})
	}
	if a.cur().name() != "Settings" || s.adv.current().label != "Other" {
		t.Fatalf("scrolling down: %s, at %v", a.cur().name(), s.adv.current())
	}
	// ctrl+o: all of it as plain text, and back, losing nothing.
	ctrlO := tea.KeyMsg{Type: tea.KeyCtrlO}
	press(a, ctrlO)
	if len(s.adv.fields) != 1 || !strings.Contains(s.adv.fields[0].Value(), "disable_custom_urls: true") || !strings.Contains(s.adv.fields[0].Value(), "docker_daemon_options") {
		t.Fatalf("plain text: %d boxes", len(s.adv.fields))
	}
	press(a, ctrlO)
	if len(s.adv.fields) < 2 || !s.right {
		t.Fatalf("by component again: %d boxes, right %v", len(s.adv.fields), s.right)
	}
	press(a, tea.KeyMsg{Type: tea.KeyEsc})
	if a.cur().name() != "Components" {
		t.Fatalf("esc led to %s", a.cur().name())
	}
	want := "docker_daemon_options: {}\nmatrix_client_element_hostname: chat.example.org\nmatrix_client_element_disable_custom_urls: true"
	if got := a.cfg.ExtraYAML; !strings.Contains(got, "matrix_client_element_hostname: chat.example.org\nmatrix_client_element_disable_custom_urls: true") || !strings.Contains(got, "docker_daemon_options: {}") {
		t.Errorf("extra vars:\n%s\nwant like\n%s", got, want)
	}
}
