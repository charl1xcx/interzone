package ui

import (
	"testing"

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
