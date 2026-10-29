package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"interzone/internal/config"
)

func TestAlerts(t *testing.T) {
	a := demoApp(t, 120, 36)
	a.jump(a.index("Alerts"))
	s, ok := a.cur().(*alertsScreen)
	if !ok {
		t.Fatalf("on %s", a.cur().name())
	}
	s.form.get("on").on = true
	s.form.get("url").input.SetValue("ntfy.sh/topic")
	deliver(a, formDoneMsg{})
	if s.err == "" || a.cfg.Alerts.Enabled {
		t.Errorf("a URL without https:// was taken")
	}
	s.form.get("url").input.SetValue("https://ntfy.sh/a-long-random-topic")
	deliver(a, formDoneMsg{})
	want := config.Alerts{Enabled: true, URL: "https://ntfy.sh/a-long-random-topic", Every: "*:0/5"}
	if a.cfg.Alerts != want {
		t.Errorf("saved %+v", a.cfg.Alerts)
	}
}

// Matrix room is picked like the other formats, from the keyboard, and
// swaps the address for the room's fields.
func TestAlertsMatrixRoom(t *testing.T) {
	a := demoApp(t, 120, 36)
	a.jump(a.index("Alerts"))
	s := a.cur().(*alertsScreen)
	s.form.get("on").on = true
	if !strings.Contains(s.view(120, 34), "Matrix room") {
		t.Fatal("no Matrix room choice")
	}
	s.form.focusOn(2)
	if s.form.fields[2].key != "format" {
		t.Fatal("the format isn't the third field")
	}
	s.update(tea.KeyMsg{Type: tea.KeyRight})
	s.update(tea.KeyMsg{Type: tea.KeyRight})
	if s.form.value("format") != "matrix" || s.form.get("url").visible() || !s.form.get("token").visible() {
		t.Fatalf("format %q, url shown %v, token shown %v", s.form.value("format"), s.form.get("url").visible(), s.form.get("token").visible())
	}
	s.form.get("room").input.SetValue("#alerts:example.org")
	s.form.get("token").input.SetValue("syt_secret")
	deliver(a, formDoneMsg{})
	got := a.cfg.Alerts
	if !got.Matrix || got.Homeserver != "https://matrix.example.org" || got.Room != "#alerts:example.org" || got.Token != "syt_secret" {
		t.Errorf("saved %+v (%s)", got, s.err)
	}
}
